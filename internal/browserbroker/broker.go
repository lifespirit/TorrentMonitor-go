package browserbroker

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var ErrSessionNotFound = errors.New("browser session not found")

const interactionSessionTTL = time.Hour

type noopLogger struct{}

func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Error(string, ...any) {}

func New(cfg Config, logger Logger) *Broker {
	if logger == nil {
		logger = noopLogger{}
	}
	cfg = normalizeConfig(cfg)
	return &Broker{cfg: cfg, logger: logger, sessions: map[string]*Session{}, byTracker: map[string]string{}}
}

func normalizeConfig(cfg Config) Config {
	if cfg.ViewportW <= 0 {
		cfg.ViewportW = 1280
	}
	if cfg.ViewportH <= 0 {
		cfg.ViewportH = 900
	}
	if cfg.Quality <= 0 {
		cfg.Quality = 70
	}
	if strings.TrimSpace(cfg.ProfileBase) == "" {
		cfg.ProfileBase = defaultProfileBase()
	}
	cfg.Binary = strings.TrimSpace(cfg.Binary)
	cfg.ProfileBase = strings.TrimSpace(cfg.ProfileBase)
	cfg.ProfilePath = strings.TrimSpace(cfg.ProfilePath)
	cfg.ConnectURL = strings.TrimSpace(cfg.ConnectURL)
	return cfg
}

func browserConfigEqual(a, b Config) bool {
	return strings.TrimSpace(a.Binary) == strings.TrimSpace(b.Binary) &&
		strings.TrimSpace(a.ProfileBase) == strings.TrimSpace(b.ProfileBase) &&
		strings.TrimSpace(a.ProfilePath) == strings.TrimSpace(b.ProfilePath) &&
		strings.TrimSpace(a.ConnectURL) == strings.TrimSpace(b.ConnectURL) &&
		a.ViewportW == b.ViewportW &&
		a.ViewportH == b.ViewportH &&
		a.Quality == b.Quality
}

func (b *Broker) UpdateConfig(cfg Config) {
	cfg = normalizeConfig(cfg)
	b.mu.Lock()
	oldCfg := b.cfg
	if browserConfigEqual(oldCfg, cfg) {
		b.cfg = cfg
		b.mu.Unlock()
		return
	}
	oldBrowser := b.browser
	sessions := make([]*Session, 0, len(b.sessions))
	for _, s := range b.sessions {
		sessions = append(sessions, s)
	}
	b.cfg = cfg
	b.sessions = map[string]*Session{}
	b.byTracker = map[string]string{}
	b.browser = nil
	b.mu.Unlock()

	for _, s := range sessions {
		s.setError("browser configuration changed")
		s.Close()
	}
	if oldBrowser != nil && !oldBrowser.external {
		go shutdownManagedBrowser(oldBrowser, b.logger)
	}
}

func shutdownManagedBrowser(p *browserProcess, logger Logger) {
	if p == nil || p.external || p.cmd == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if wsURL, err := debuggerWebsocketURL(&http.Client{Timeout: 2 * time.Second}, p.debuggerBase); err == nil {
		if cdp, err := newCDP(ctx, wsURL); err == nil {
			_, _ = cdp.Call(ctx, "Browser.close", nil)
			_ = cdp.Close()
		}
	}
	done := make(chan struct{})
	go func() {
		if p.cmd != nil {
			_ = p.cmd.Wait()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		if p.cmd.Process != nil {
			if logger != nil {
				logger.Warn("forcing old Chromium process to exit after browser config change", "profile", p.profilePath)
			}
			_ = p.cmd.Process.Kill()
		}
	}
}

func (b *Broker) Open(ctx context.Context, req OpenRequest) (OpenResult, error) {
	tracker := normalizeName(req.Tracker)
	if tracker == "" {
		tracker = "default"
	}
	rawURL := strings.TrimSpace(req.URL)
	if rawURL == "" {
		return OpenResult{}, errors.New("browser session URL is required")
	}

	b.mu.Lock()
	profile := b.cfg.sharedProfilePath()
	if err := os.MkdirAll(profile, 0o700); err != nil {
		b.mu.Unlock()
		return OpenResult{}, fmt.Errorf("create shared browser profile: %w", err)
	}
	if err := b.ensureBrowserLocked(ctx, profile); err != nil {
		b.mu.Unlock()
		return OpenResult{}, err
	}
	if id := b.byTracker[tracker]; id != "" {
		if s := b.sessions[id]; s != nil && !s.isClosed() {
			b.mu.Unlock()
			info := s.Status()
			if info.Status == "needs_interaction" {
				return OpenResult{ID: info.ID, Tracker: info.Tracker, URL: info.URL, ProfilePath: info.ProfilePath, ViewerURL: "/browser/session/" + info.ID, Status: info.Status, CreatedAt: info.CreatedAt}, nil
			}
			if err := s.Navigate(ctx, rawURL); err != nil {
				return OpenResult{}, err
			}
			info = s.Status()
			return OpenResult{ID: info.ID, Tracker: info.Tracker, URL: rawURL, ProfilePath: info.ProfilePath, ViewerURL: "/browser/session/" + info.ID, Status: info.Status, CreatedAt: info.CreatedAt}, nil
		}
	}

	wsURL, targetID, err := createBrowserTarget(ctx, b.browser.debuggerBase, rawURL)
	if err != nil {
		b.mu.Unlock()
		return OpenResult{}, err
	}
	id := "br_" + randomHex(8)
	created := time.Now()
	s := &Session{
		id: id, tracker: tracker, url: rawURL, profilePath: b.browser.profilePath, port: b.browser.port, debuggerBase: b.browser.debuggerBase,
		targetID: targetID, wsURL: wsURL, cfg: b.cfg, logger: b.logger,
		createdAt: created, updatedAt: created, status: "starting", closed: make(chan struct{}), ready: make(chan struct{}),
	}
	b.sessions[id] = s
	b.byTracker[tracker] = id
	b.mu.Unlock()

	if err := s.attach(ctx); err != nil {
		b.Close(id)
		return OpenResult{}, err
	}
	return OpenResult{ID: id, Tracker: tracker, URL: rawURL, ProfilePath: s.profilePath, ViewerURL: "/browser/session/" + id, Status: s.Status().Status, CreatedAt: created}, nil
}

func (b *Broker) ensureBrowserLocked(ctx context.Context, profile string) error {
	if connect := strings.TrimSpace(b.cfg.ConnectURL); connect != "" {
		base, port, err := normalizeDevToolsBase(connect)
		if err != nil {
			return err
		}
		if b.browser != nil && b.browser.external && strings.TrimRight(b.browser.debuggerBase, "/") == strings.TrimRight(base, "/") {
			if debuggerAlive(base) {
				return nil
			}
		}
		if _, err := waitDebugger(ctx, base, 10*time.Second); err != nil {
			return fmt.Errorf("connect to external Chromium debugger %s: %w", base, err)
		}
		if b.cfg.Debug {
			b.logger.Info("using external Chromium debugger", "debugger_base", base, "profile", profile)
		}
		b.browser = &browserProcess{cmd: nil, port: port, debuggerBase: base, binary: "external", profilePath: profile, external: true}
		return nil
	}

	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return errors.New("display-mode Chromium session requires DISPLAY or WAYLAND_DISPLAY; run TorrentMonitor under a graphical session, Xvfb, wolf, cage, weston, xpra, or another compositor/display provider; or set TM_BROWSER_CONNECT_URL to attach to an already running Chromium")
	}
	if b.browser != nil && b.browser.cmd != nil && b.browser.cmd.ProcessState == nil {
		if debuggerAlive(b.browser.debuggerBase) {
			return nil
		}
	}
	binary, err := b.chromiumBinary()
	if err != nil {
		return err
	}
	port, err := freePort()
	if err != nil {
		return err
	}
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	args := []string{
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=" + strconv.Itoa(port),
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-dev-shm-usage",
		"--window-size=" + strconv.Itoa(b.cfg.ViewportW) + "," + strconv.Itoa(b.cfg.ViewportH),
		"about:blank",
	}
	if b.cfg.Debug {
		b.logger.Info("starting shared browser process", "profile", profile, "binary", binary, "display", os.Getenv("DISPLAY"), "wayland_display", os.Getenv("WAYLAND_DISPLAY"), "debugger_base", base, "port", port)
	}
	cmd := exec.CommandContext(context.Background(), binary, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start Chromium: %w", err)
	}
	b.browser = &browserProcess{cmd: cmd, port: port, debuggerBase: base, binary: binary, profilePath: profile}
	go func() {
		err := cmd.Wait()
		if err != nil {
			b.logger.Warn("shared Chromium process exited", "error", err, "stderr", strings.TrimSpace(stderr.String()))
		} else if b.cfg.Debug {
			b.logger.Info("shared Chromium process exited")
		}
		b.markBrowserExited(cmd)
	}()
	if _, err := waitDebugger(ctx, base, 20*time.Second); err != nil {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		b.browser = nil
		return err
	}
	if b.cfg.Debug {
		b.logger.Info("shared browser process ready", "profile", profile, "debugger_base", base, "debugger_port", port)
	}
	return nil
}

func (b *Broker) markBrowserExited(cmd *exec.Cmd) {
	b.mu.Lock()
	if b.browser != nil && b.browser.cmd == cmd {
		b.browser = nil
		for _, s := range b.sessions {
			s.setError("shared Chromium process exited")
		}
	}
	b.mu.Unlock()
}

func debuggerAlive(base string) bool {
	client := &http.Client{Timeout: time.Second}
	_, err := debuggerWebsocketURL(client, base)
	return err == nil
}

func createBrowserTarget(ctx context.Context, base string, rawURL string) (string, string, error) {
	endpoint := strings.TrimRight(base, "/") + "/json/new?" + neturl.QueryEscape(rawURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("create Chromium tab: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("create Chromium tab returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var target struct {
		ID                    string `json:"id"`
		WebSocketDebuggerURL  string `json:"webSocketDebuggerUrl"`
		WebSocketDebuggerURL2 string `json:"webSocketDebuggerURL"`
	}
	if err := json.Unmarshal(data, &target); err != nil {
		return "", "", err
	}
	wsURL := target.WebSocketDebuggerURL
	if wsURL == "" {
		wsURL = target.WebSocketDebuggerURL2
	}
	if wsURL == "" {
		return "", "", errors.New("created Chromium tab without websocket debugger URL")
	}
	return wsURL, target.ID, nil
}

type browserTarget struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	Title                string `json:"title"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func listBrowserTargets(ctx context.Context, base string) ([]browserTarget, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/json/list", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list Chromium tabs: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list Chromium tabs returned HTTP %d", resp.StatusCode)
	}
	var targets []browserTarget
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, fmt.Errorf("decode Chromium tabs: %w", err)
	}
	return targets, nil
}

func closeBrowserTarget(ctx context.Context, base string, targetID string) error {
	endpoint := strings.TrimRight(base, "/") + "/json/close/" + neturl.PathEscape(targetID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("close Chromium tab: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("close Chromium tab returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// FetchPage opens rawURL in the shared Chromium profile and returns the live DOM.
// It is used by Chromium access mode in the site template runner, so monitor jobs
// reuse the same browser session that the user used to pass Cloudflare/login.
func (b *Broker) FetchPage(ctx context.Context, tracker string, rawURL string) ([]byte, error) {
	opened, err := b.Open(ctx, OpenRequest{Tracker: tracker, URL: rawURL})
	if err != nil {
		return nil, err
	}
	s := b.session(opened.ID)
	if s == nil {
		return nil, ErrSessionNotFound
	}
	if err := s.WaitForReady(ctx, 30*time.Second); err != nil {
		return nil, err
	}
	return s.HTML(ctx)
}

// FetchPageWait opens rawURL and waits until Chromium is really on the
// expected tracker page. Readiness is intentionally based on URL and positive
// tracker markers only; page title and Cloudflare/challenge names are diagnostic
// signals, not success/failure criteria for the monitor pipeline.
func (b *Broker) FetchPageWait(ctx context.Context, tracker string, rawURL string, timeout time.Duration, expectedURLNeedles []string, requiredAll []string, requiredAny []string, requiredRegex string) ([]byte, error) {
	opened, err := b.Open(ctx, OpenRequest{Tracker: tracker, URL: rawURL})
	if err != nil {
		return nil, err
	}
	s := b.session(opened.ID)
	if s == nil {
		return nil, ErrSessionNotFound
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	var lastURL string
	var lastReadyState string
	var lastMissing string
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		readyState, _ := s.evalString(ctx, "document.readyState || ''")
		if readyState != "" {
			lastReadyState = readyState
		}
		currentURL, _ := s.evalString(ctx, "location.href || ''")
		if currentURL != "" {
			lastURL = currentURL
		}
		html, htmlErr := s.evalString(ctx, `document.documentElement ? document.documentElement.outerHTML : ''`)
		if htmlErr == nil && strings.TrimSpace(html) != "" {
			urlOK := browserURLMatches(currentURL, rawURL, expectedURLNeedles)
			markersOK, missing := browserHTMLMarkersMatch(html, requiredAll, requiredAny, requiredRegex)
			lastMissing = missing
			if urlOK && markersOK {
				_ = s.refreshPageSummary(ctx)
				return []byte(html), nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	if lastURL == "" {
		lastURL = "unknown"
	}
	if lastReadyState == "" {
		lastReadyState = "unknown"
	}
	msg := fmt.Sprintf("expected tracker page did not render before timeout after %s (expected_url=%q current_url=%q ready_state=%q); open /browser/session/%s and finish the interactive check", timeout, rawURL, lastURL, lastReadyState, opened.ID)
	if strings.TrimSpace(lastMissing) != "" {
		msg += "; missing=" + lastMissing
	}
	b.retainInteraction(opened.ID, interactionSessionTTL)
	return nil, &NeedsInteractionError{
		Tracker:   normalizeName(tracker),
		URL:       rawURL,
		SessionID: opened.ID,
		ViewerURL: "/browser/session/" + opened.ID,
		Message:   msg,
	}
}

// Download downloads rawURL through the shared Chromium profile. It is used for
// tracker endpoints such as rutracker.org/forum/dl.php where native HTTP may be
// rejected even after the topic page is accessible in Chromium.
func (b *Broker) Download(ctx context.Context, tracker string, rawURL string, headers map[string]string, cookieHeader string) ([]byte, error) {
	if strings.TrimSpace(rawURL) == "" {
		return nil, errors.New("download URL is empty")
	}
	s, err := b.sessionForTracker(ctx, tracker, rawURL)
	if err != nil {
		return nil, err
	}
	return s.Download(ctx, rawURL, headers, cookieHeader)
}

func (b *Broker) CookieHeader(ctx context.Context, tracker string, rawURL string) (string, error) {
	s, err := b.sessionForTracker(ctx, tracker, rawURL)
	if err != nil {
		return "", err
	}
	raw, err := s.cdp.Call(ctx, "Network.getCookies", map[string]any{"urls": []string{rawURL}})
	if err != nil {
		return "", err
	}
	var res struct {
		Cookies []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"cookies"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", err
	}
	parts := make([]string, 0, len(res.Cookies))
	for _, c := range res.Cookies {
		if strings.TrimSpace(c.Name) == "" {
			continue
		}
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; "), nil
}

func (b *Broker) UserAgent(ctx context.Context, tracker string) (string, error) {
	s, err := b.sessionForTracker(ctx, tracker, "about:blank")
	if err != nil {
		return "", err
	}
	return s.evalString(ctx, "navigator.userAgent || ''")
}

func (b *Broker) sessionForTracker(ctx context.Context, tracker string, fallbackURL string) (*Session, error) {
	tracker = normalizeName(tracker)
	if tracker == "" {
		tracker = "default"
	}
	b.mu.Lock()
	if id := b.byTracker[tracker]; id != "" {
		if s := b.sessions[id]; s != nil && !s.isClosed() {
			b.mu.Unlock()
			return s, nil
		}
	}
	b.mu.Unlock()
	if strings.TrimSpace(fallbackURL) == "" {
		fallbackURL = "about:blank"
	}
	opened, err := b.Open(ctx, OpenRequest{Tracker: tracker, URL: fallbackURL})
	if err != nil {
		return nil, err
	}
	s := b.session(opened.ID)
	if s == nil {
		return nil, ErrSessionNotFound
	}
	return s, nil
}

func (b *Broker) Get(id string) (SessionInfo, error) {
	s := b.session(id)
	if s == nil {
		return SessionInfo{}, ErrSessionNotFound
	}
	return s.Status(), nil
}

func (b *Broker) Frame(id string) ([]byte, string, error) {
	s := b.session(id)
	if s == nil {
		return nil, "", ErrSessionNotFound
	}
	return s.Frame()
}

func (b *Broker) Input(ctx context.Context, id string, ev InputEvent) error {
	s := b.session(id)
	if s == nil {
		return ErrSessionNotFound
	}
	return s.Input(ctx, ev)
}

func (b *Broker) Navigate(ctx context.Context, id string, rawURL string) error {
	s := b.session(id)
	if s == nil {
		return ErrSessionNotFound
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return errors.New("browser navigation URL is required")
	}
	return s.Navigate(ctx, rawURL)
}

func (b *Broker) Tabs(ctx context.Context, id string) ([]BrowserTab, error) {
	s := b.session(id)
	if s == nil {
		return nil, ErrSessionNotFound
	}
	targets, err := listBrowserTargets(ctx, s.debuggerBase)
	if err != nil {
		return nil, err
	}
	tabs := make([]BrowserTab, 0, len(targets))
	for _, target := range targets {
		if target.Type != "page" || target.WebSocketDebuggerURL == "" {
			continue
		}
		tabs = append(tabs, BrowserTab{ID: target.ID, URL: target.URL, Title: target.Title, Active: target.ID == s.targetID})
	}
	return tabs, nil
}

func (b *Broker) SwitchTab(ctx context.Context, id string, targetID string) error {
	old := b.session(id)
	if old == nil {
		return ErrSessionNotFound
	}
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return errors.New("browser target id is required")
	}
	targets, err := listBrowserTargets(ctx, old.debuggerBase)
	if err != nil {
		return err
	}
	var candidate *browserTarget
	for i := range targets {
		target := &targets[i]
		if target.Type == "page" && target.ID == targetID && target.WebSocketDebuggerURL != "" {
			candidate = target
			break
		}
	}
	if candidate == nil {
		return errors.New("browser tab not found")
	}
	if candidate.ID == old.targetID {
		return nil
	}

	created := old.createdAt
	next := &Session{
		id: id, tracker: old.tracker, url: candidate.URL, profilePath: old.profilePath, port: old.port, debuggerBase: old.debuggerBase,
		targetID: candidate.ID, wsURL: candidate.WebSocketDebuggerURL, cfg: old.cfg, logger: old.logger,
		createdAt: created, updatedAt: time.Now(), status: "starting", closed: make(chan struct{}), ready: make(chan struct{}),
	}
	if err := next.attach(ctx); err != nil {
		return err
	}

	b.mu.Lock()
	if b.sessions[id] != old {
		b.mu.Unlock()
		next.Detach()
		return nil
	}
	b.sessions[id] = next
	b.mu.Unlock()
	old.Detach()
	b.logger.Info("browser viewer switched tab", "session", id, "tracker", next.tracker, "url", candidate.URL, "target", candidate.ID)
	return nil
}

// CloseTab closes a selected Chromium page. When it is the viewer's active
// target, the viewer is moved to another page first so its window can remain
// usable. The returned value reports that no replacement page was available.
func (b *Broker) CloseTab(ctx context.Context, id string, targetID string) (bool, error) {
	current := b.session(id)
	if current == nil {
		return false, ErrSessionNotFound
	}
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		targetID = current.targetID
	}
	if targetID != current.targetID {
		return false, closeBrowserTarget(ctx, current.debuggerBase, targetID)
	}

	targets, err := listBrowserTargets(ctx, current.debuggerBase)
	if err != nil {
		return false, err
	}
	var replacement string
	for _, target := range targets {
		if target.Type == "page" && target.ID != targetID && target.WebSocketDebuggerURL != "" && target.URL != "about:blank" {
			replacement = target.ID
			break
		}
	}
	if replacement == "" {
		for _, target := range targets {
			if target.Type == "page" && target.ID != targetID && target.WebSocketDebuggerURL != "" {
				replacement = target.ID
				break
			}
		}
	}
	if replacement == "" {
		if err := b.Close(id); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := b.SwitchTab(ctx, id, replacement); err != nil {
		return false, err
	}
	if err := closeBrowserTarget(ctx, current.debuggerBase, targetID); err != nil {
		return false, err
	}
	return false, nil
}

func (b *Broker) Done(ctx context.Context, id string) (SessionInfo, error) {
	s := b.session(id)
	if s == nil {
		return SessionInfo{}, ErrSessionNotFound
	}
	info, err := s.MarkDone(ctx)
	if err != nil {
		return info, err
	}
	if info.Status == "needs_interaction" {
		b.retainInteraction(id, interactionSessionTTL)
		return info, nil
	}
	// Cookies live in the shared profile, not in the page target. Once the user
	// has finished the challenge there is no reason to keep its renderer alive.
	if err := b.Close(id); err != nil && !errors.Is(err, ErrSessionNotFound) {
		return info, err
	}
	return info, nil
}

func (b *Broker) retainInteraction(id string, ttl time.Duration) {
	if ttl <= 0 {
		ttl = interactionSessionTTL
	}
	s := b.session(id)
	if s == nil {
		return
	}
	s.setStatus("needs_interaction")
	time.AfterFunc(ttl, func() {
		current := b.session(id)
		if current == nil || current.Status().Status != "needs_interaction" {
			return
		}
		if err := b.Close(id); err == nil {
			b.logger.Info("closed expired interactive browser session", "session", id, "tracker", current.tracker, "ttl", ttl)
		}
	})
}

func (b *Broker) Close(id string) error {
	b.mu.Lock()
	s := b.sessions[id]
	if s != nil {
		delete(b.sessions, id)
		if b.byTracker[s.tracker] == id {
			delete(b.byTracker, s.tracker)
		}
	}
	b.mu.Unlock()
	if s == nil {
		return ErrSessionNotFound
	}
	s.Close()
	return nil
}

// CloseTracker closes the live page associated with tracker. Browser profile
// data (including cookies) remains in the shared Chromium profile, so the next
// check can open a clean page without losing an authenticated session.
func (b *Broker) CloseTracker(tracker string) error {
	tracker = normalizeName(tracker)
	if tracker == "" {
		tracker = "default"
	}
	b.mu.Lock()
	id := b.byTracker[tracker]
	b.mu.Unlock()
	if id == "" {
		return ErrSessionNotFound
	}
	return b.Close(id)
}

func (b *Broker) List() []SessionInfo {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]SessionInfo, 0, len(b.sessions))
	for _, s := range b.sessions {
		out = append(out, s.Status())
	}
	return out
}

func (b *Broker) session(id string) *Session {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sessions[strings.TrimSpace(id)]
}

func (b *Broker) chromiumBinary() (string, error) {
	if strings.TrimSpace(b.cfg.Binary) != "" {
		return b.cfg.Binary, nil
	}
	if env := strings.TrimSpace(os.Getenv("TM_BROWSER_BINARY")); env != "" {
		return env, nil
	}
	for _, candidate := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "chrome"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", errors.New("no Chromium/Chrome binary was found; set TM_BROWSER_BINARY")
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func normalizeDevToolsBase(raw string) (string, int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", 0, errors.New("Chromium DevTools connect URL is empty")
	}
	if strings.HasPrefix(raw, ":") {
		raw = "127.0.0.1" + raw
	}
	if _, err := strconv.Atoi(raw); err == nil {
		raw = "127.0.0.1:" + raw
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := neturl.Parse(raw)
	if err != nil {
		return "", 0, fmt.Errorf("parse Chromium DevTools connect URL: %w", err)
	}
	if u.Scheme == "ws" || u.Scheme == "wss" {
		if u.Scheme == "ws" {
			u.Scheme = "http"
		} else {
			u.Scheme = "https"
		}
		// A browser websocket URL looks like /devtools/browser/<id>.
		// The HTTP DevTools API lives at the same scheme+host root.
		u.Path = ""
		u.RawQuery = ""
		u.Fragment = ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", 0, fmt.Errorf("unsupported Chromium DevTools URL scheme %q", u.Scheme)
	}
	if strings.TrimSpace(u.Host) == "" {
		return "", 0, fmt.Errorf("Chromium DevTools connect URL %q has no host", raw)
	}
	base := strings.TrimRight(u.String(), "/")
	port := 0
	if p := u.Port(); p != "" {
		port, _ = strconv.Atoi(p)
	}
	return base, port, nil
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func defaultProfileBase() string {
	if env := strings.TrimSpace(os.Getenv("TM_BROWSER_PROFILE_BASE")); env != "" {
		return env
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdg != "" {
		return filepath.Join(xdg, "torrentmonitor-go", "browser")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "share", "torrentmonitor-go", "browser")
	}
	return filepath.Join(os.TempDir(), "torrentmonitor-go", "browser")
}

func normalizeName(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func browserURLMatches(current string, expected string, extraNeedles []string) bool {
	current = strings.TrimSpace(current)
	expected = strings.TrimSpace(expected)
	if current == "" {
		return false
	}
	if sameBrowserURL(current, expected) {
		return true
	}
	for _, needle := range extraNeedles {
		needle = strings.TrimSpace(needle)
		if needle == "" {
			continue
		}
		if sameBrowserURL(current, needle) || strings.Contains(current, needle) {
			return true
		}
	}
	return false
}

func sameBrowserURL(a string, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	if strings.TrimRight(a, "/") == strings.TrimRight(b, "/") {
		return true
	}
	au, aerr := neturl.Parse(a)
	bu, berr := neturl.Parse(b)
	if aerr != nil || berr != nil || au.Host == "" || bu.Host == "" {
		return false
	}
	return strings.EqualFold(au.Scheme, bu.Scheme) && strings.EqualFold(au.Host, bu.Host) && au.EscapedPath() == bu.EscapedPath() && au.RawQuery == bu.RawQuery
}

func browserHTMLMarkersMatch(html string, requiredAll []string, requiredAny []string, requiredRegex string) (bool, string) {
	for _, needle := range requiredAll {
		needle = strings.TrimSpace(needle)
		if needle != "" && !strings.Contains(html, needle) {
			return false, fmt.Sprintf("marker %q", needle)
		}
	}
	if len(requiredAny) > 0 {
		matchedAny := false
		for _, needle := range requiredAny {
			needle = strings.TrimSpace(needle)
			if needle != "" && strings.Contains(html, needle) {
				matchedAny = true
				break
			}
		}
		if !matchedAny {
			return false, "one of expected page markers"
		}
	}
	if strings.TrimSpace(requiredRegex) != "" {
		matched, err := regexp.MatchString(requiredRegex, html)
		if err != nil || !matched {
			return false, "regex marker"
		}
	}
	return true, ""
}
func safeName(site string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9._-]+`)
	out := re.ReplaceAllString(strings.ToLower(strings.TrimSpace(site)), "_")
	if out == "" {
		return "default"
	}
	return out
}
