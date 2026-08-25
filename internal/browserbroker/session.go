package browserbroker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Session struct {
	id           string
	tracker      string
	url          string
	profilePath  string
	port         int
	debuggerBase string
	targetID     string
	wsURL        string
	cfg          Config
	logger       Logger

	mu                     sync.RWMutex
	status                 string
	lastError              string
	createdAt              time.Time
	updatedAt              time.Time
	width                  int
	height                 int
	frame                  []byte
	currentURL             string
	title                  string
	cookieNames            []string
	cookieDetails          []CookieDebug
	hasCloudflareClearance bool
	looksLikeCloudflare    bool
	looksLikeLoginPage     bool

	cdp    *cdpConn
	closed chan struct{}
	ready  chan struct{}
}

func (s *Session) attach(ctx context.Context) error {
	if s.cfg.Debug {
		s.logger.Info("attaching browser session", "session", s.id, "tracker", s.tracker, "url", s.url, "profile", s.profilePath, "target", s.targetID, "debugger_base", s.debuggerBase, "port", s.port)
	}
	cdp, err := newCDP(ctx, s.wsURL)
	if err != nil {
		return err
	}
	s.cdp = cdp
	if _, err := cdp.Call(ctx, "Page.enable", nil); err != nil {
		s.Close()
		return err
	}
	if _, err := cdp.Call(ctx, "Runtime.enable", nil); err != nil {
		s.Close()
		return err
	}
	if _, err := cdp.Call(ctx, "Network.enable", nil); err != nil {
		s.Close()
		return err
	}
	_, _ = cdp.Call(ctx, "Page.bringToFront", nil)
	if s.cfg.ViewportW > 0 && s.cfg.ViewportH > 0 {
		// Keep the page viewport, screencast metadata, and viewer coordinate
		// system aligned. This is especially important when TorrentMonitor
		// attaches to an external Chromium whose real window can be a different
		// size from the JPEG frame rendered in the web UI.
		_, _ = cdp.Call(ctx, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": s.cfg.ViewportW, "height": s.cfg.ViewportH, "deviceScaleFactor": 1, "mobile": false,
		})
	}
	if _, err := cdp.Call(ctx, "Page.startScreencast", map[string]any{
		"format": "jpeg", "quality": s.cfg.Quality, "maxWidth": s.cfg.ViewportW, "maxHeight": s.cfg.ViewportH, "everyNthFrame": 1,
	}); err != nil {
		s.Close()
		return err
	}
	s.setStatus("running")
	close(s.ready)
	go s.eventLoop()
	if s.cfg.Debug {
		s.logger.Info("browser session ready", "session", s.id, "tracker", s.tracker, "debugger_base", s.debuggerBase, "debugger_port", s.port)
	}
	return nil
}

func waitDebugger(ctx context.Context, base string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 1 * time.Second}
	var lastErr error
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		wsURL, err := debuggerWebsocketURL(client, base)
		if err == nil && wsURL != "" {
			return wsURL, nil
		}
		lastErr = err
		time.Sleep(250 * time.Millisecond)
	}
	if lastErr != nil {
		return "", fmt.Errorf("wait Chromium debugger: %w", lastErr)
	}
	return "", errors.New("wait Chromium debugger: timeout")
}

func debuggerWebsocketURL(client *http.Client, base string) (string, error) {
	resp, err := client.Get(strings.TrimRight(base, "/") + "/json/version")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("/json/version returned HTTP %d", resp.StatusCode)
	}
	var version struct {
		Browser              string `json:"Browser"`
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(data, &version); err != nil {
		return "", err
	}
	if strings.TrimSpace(version.WebSocketDebuggerURL) == "" {
		return "", errors.New("Chromium debugger is available but /json/version did not expose browser websocket URL")
	}
	return version.WebSocketDebuggerURL, nil
}

func (s *Session) eventLoop() {
	for ev := range s.cdp.Events() {
		switch ev.Method {
		case "Page.screencastFrame":
			var p struct {
				Data      string `json:"data"`
				SessionID int64  `json:"sessionId"`
				Metadata  struct {
					DeviceWidth  float64 `json:"deviceWidth"`
					DeviceHeight float64 `json:"deviceHeight"`
				} `json:"metadata"`
			}
			if err := json.Unmarshal(ev.Params, &p); err != nil {
				continue
			}
			if p.SessionID != 0 {
				_, _ = s.cdp.Call(context.Background(), "Page.screencastFrameAck", map[string]any{"sessionId": p.SessionID})
			}
			img, err := base64.StdEncoding.DecodeString(p.Data)
			if err != nil || len(img) == 0 {
				continue
			}
			s.mu.Lock()
			s.frame = img
			s.updatedAt = time.Now()
			if p.Metadata.DeviceWidth > 0 {
				s.width = int(p.Metadata.DeviceWidth)
			}
			if p.Metadata.DeviceHeight > 0 {
				s.height = int(p.Metadata.DeviceHeight)
			}
			s.mu.Unlock()
		case "Inspector.detached":
			s.setStatus("detached")
		}
	}
}

func (s *Session) Status() SessionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cookieNames := append([]string(nil), s.cookieNames...)
	cookieDetails := append([]CookieDebug(nil), s.cookieDetails...)
	return SessionInfo{ID: s.id, Tracker: s.tracker, URL: s.url, CurrentURL: s.currentURL, Title: s.title, ProfilePath: s.profilePath, Status: s.status, LastError: s.lastError, CreatedAt: s.createdAt, UpdatedAt: s.updatedAt, Width: s.width, Height: s.height, CookieNames: cookieNames, CookieDetails: cookieDetails, HasCloudflareClearance: s.hasCloudflareClearance, LooksLikeCloudflare: s.looksLikeCloudflare, LooksLikeLoginPage: s.looksLikeLoginPage}
}

func (s *Session) Frame() ([]byte, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.frame) == 0 {
		return nil, "", errors.New("browser frame is not ready yet")
	}
	out := make([]byte, len(s.frame))
	copy(out, s.frame)
	return out, "image/jpeg", nil
}

func (s *Session) Input(ctx context.Context, ev InputEvent) error {
	if s.cdp == nil {
		return errors.New("browser session is not ready")
	}
	s.updatedNow()
	switch strings.ToLower(ev.Kind) {
	case "mouse":
		typ := ev.Type
		if typ == "" {
			typ = "mouseMoved"
		}
		button := ev.Button
		if button == "" {
			button = "left"
		}
		params := map[string]any{"type": typ, "x": ev.X, "y": ev.Y, "modifiers": ev.Modifiers}
		if typ == "mouseWheel" {
			params["deltaX"] = ev.DeltaX
			params["deltaY"] = ev.DeltaY
		} else {
			params["button"] = button
			if ev.ClickCount > 0 {
				params["clickCount"] = ev.ClickCount
			}
		}
		_, err := s.cdp.Call(ctx, "Input.dispatchMouseEvent", params)
		return err
	case "keyboard":
		// Keep the target tab focused before every key event. This is important
		// for externally managed Chromium instances where another tab/window may
		// have become active between screencast frames.
		_, _ = s.cdp.Call(ctx, "Page.bringToFront", nil)
		typ := ev.Type
		if typ == "" {
			typ = "rawKeyDown"
		}
		if typ == "insertText" {
			if ev.Text == "" {
				return nil
			}
			_, err := s.cdp.Call(ctx, "Input.insertText", map[string]any{"text": ev.Text})
			return err
		}
		params := map[string]any{"type": typ, "modifiers": ev.Modifiers}
		if ev.Key != "" {
			params["key"] = ev.Key
		}
		if ev.Code != "" {
			params["code"] = ev.Code
		}
		if ev.Text != "" {
			params["text"] = ev.Text
		}
		if ev.UnmodifiedText != "" {
			params["unmodifiedText"] = ev.UnmodifiedText
		}
		if ev.WindowsVirtualKeyCode != 0 {
			params["windowsVirtualKeyCode"] = ev.WindowsVirtualKeyCode
		}
		if ev.NativeVirtualKeyCode != 0 {
			params["nativeVirtualKeyCode"] = ev.NativeVirtualKeyCode
		}
		if ev.Location != 0 {
			params["location"] = ev.Location
		}
		if ev.AutoRepeat {
			params["autoRepeat"] = true
		}
		if ev.IsKeypad {
			params["isKeypad"] = true
		}
		_, err := s.cdp.Call(ctx, "Input.dispatchKeyEvent", params)
		return err
	default:
		return fmt.Errorf("unsupported input kind %q", ev.Kind)
	}
}

func (s *Session) Evaluate(ctx context.Context, expression string) error {
	if s.cdp == nil {
		return errors.New("browser session is not ready")
	}
	_, err := s.cdp.Call(ctx, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true})
	return err
}

func (s *Session) Download(ctx context.Context, rawURL string, headers map[string]string, cookieHeader string) ([]byte, error) {
	if s.cdp == nil {
		return nil, errors.New("browser session is not ready")
	}
	if strings.TrimSpace(rawURL) == "" {
		return nil, errors.New("browser download URL is empty")
	}
	if strings.TrimSpace(cookieHeader) != "" {
		for _, c := range parseCookieHeader(cookieHeader) {
			_, _ = s.cdp.Call(ctx, "Network.setCookie", map[string]any{"name": c.name, "value": c.value, "url": rawURL, "path": "/"})
		}
	}
	data, err := s.downloadViaFetch(ctx, rawURL, headers)
	if err != nil {
		return nil, err
	}
	s.updatedNow()
	return data, nil
}

func (s *Session) downloadViaFetch(ctx context.Context, rawURL string, headers map[string]string) ([]byte, error) {
	cleanHeaders, referer := browserDownloadHeaders(headers)
	if referer != "" {
		cleanHeaders["Referer"] = referer
	}
	if len(cleanHeaders) > 0 {
		_, _ = s.cdp.Call(ctx, "Network.setExtraHTTPHeaders", map[string]any{"headers": cleanHeaders})
		defer func() {
			_, _ = s.cdp.Call(context.Background(), "Network.setExtraHTTPHeaders", map[string]any{"headers": map[string]string{}})
		}()
	}

	// Prefer CDP Network.loadNetworkResource over page fetch(). It runs through
	// the browser network stack with profile cookies, but is not subject to page
	// JavaScript/CORS restrictions. This matters for tracker download endpoints:
	// they often return an attachment from a slightly different origin/alias, so
	// in-page fetch() can fail with "TypeError: Failed to fetch" even while a
	// normal browser navigation/download succeeds.
	if data, err := s.downloadViaNetworkResource(ctx, rawURL); err == nil {
		return data, nil
	} else if !isUnsupportedCDPMethod(err) {
		return nil, err
	}

	return s.downloadViaPageFetch(ctx, rawURL)
}

func (s *Session) downloadViaNetworkResource(ctx context.Context, rawURL string) ([]byte, error) {
	frameID, err := s.mainFrameID(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := s.cdp.Call(ctx, "Network.loadNetworkResource", map[string]any{
		"frameId": frameID,
		"url":     rawURL,
		"options": map[string]any{"disableCache": true, "includeCredentials": true},
	})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Resource struct {
			Success        bool   `json:"success"`
			NetError       string `json:"netError"`
			HTTPStatusCode int    `json:"httpStatusCode"`
			Stream         string `json:"stream"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	if !resp.Resource.Success {
		if resp.Resource.HTTPStatusCode != 0 {
			return nil, fmt.Errorf("browser network download returned HTTP %d", resp.Resource.HTTPStatusCode)
		}
		if resp.Resource.NetError != "" {
			return nil, fmt.Errorf("browser network download failed: %s", resp.Resource.NetError)
		}
		return nil, errors.New("browser network download failed")
	}
	if resp.Resource.HTTPStatusCode >= 400 {
		return nil, fmt.Errorf("browser network download returned HTTP %d", resp.Resource.HTTPStatusCode)
	}
	if resp.Resource.Stream == "" {
		return nil, errors.New("browser network download did not return a stream")
	}
	data, err := s.readIOStream(ctx, resp.Resource.Stream)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("browser network download returned empty body")
	}
	return data, nil
}

func (s *Session) mainFrameID(ctx context.Context) (string, error) {
	raw, err := s.cdp.Call(ctx, "Page.getFrameTree", nil)
	if err != nil {
		return "", err
	}
	var tree struct {
		FrameTree struct {
			Frame struct {
				ID string `json:"id"`
			} `json:"frame"`
		} `json:"frameTree"`
	}
	if err := json.Unmarshal(raw, &tree); err != nil {
		return "", err
	}
	if tree.FrameTree.Frame.ID == "" {
		return "", errors.New("browser frame id is empty")
	}
	return tree.FrameTree.Frame.ID, nil
}

func (s *Session) readIOStream(ctx context.Context, handle string) ([]byte, error) {
	defer func() { _, _ = s.cdp.Call(context.Background(), "IO.close", map[string]any{"handle": handle}) }()
	var out []byte
	for {
		raw, err := s.cdp.Call(ctx, "IO.read", map[string]any{"handle": handle})
		if err != nil {
			return nil, err
		}
		var chunk struct {
			Data          string `json:"data"`
			Base64Encoded bool   `json:"base64Encoded"`
			EOF           bool   `json:"eof"`
		}
		if err := json.Unmarshal(raw, &chunk); err != nil {
			return nil, err
		}
		if chunk.Data != "" {
			if chunk.Base64Encoded {
				decoded, err := base64.StdEncoding.DecodeString(chunk.Data)
				if err != nil {
					return nil, err
				}
				out = append(out, decoded...)
			} else {
				out = append(out, []byte(chunk.Data)...)
			}
		}
		if chunk.EOF {
			break
		}
	}
	return out, nil
}

func isUnsupportedCDPMethod(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "wasn't found") || strings.Contains(msg, "not found") || strings.Contains(msg, "Unknown method")
}

func (s *Session) downloadViaPageFetch(ctx context.Context, rawURL string) ([]byte, error) {
	urlJSON, _ := json.Marshal(rawURL)
	expr := `(async () => {
  const url = ` + string(urlJSON) + `;
  const resp = await fetch(url, { method: 'GET', credentials: 'include', cache: 'no-store' });
  const ab = await resp.arrayBuffer();
  const bytes = new Uint8Array(ab);
  let binary = '';
  const chunk = 0x8000;
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode.apply(null, bytes.subarray(i, i + chunk));
  }
  return JSON.stringify({
    ok: resp.ok,
    status: resp.status,
    statusText: resp.statusText,
    url: resp.url,
    contentType: resp.headers.get('content-type') || '',
    body: btoa(binary)
  });
})()`
	raw, err := s.cdp.Call(ctx, "Runtime.evaluate", map[string]any{"expression": expr, "awaitPromise": true, "returnByValue": true})
	if err != nil {
		return nil, err
	}
	var eval struct {
		Result struct {
			Type        string          `json:"type"`
			Value       json.RawMessage `json:"value"`
			Description string          `json:"description"`
		} `json:"result"`
		ExceptionDetails any `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &eval); err != nil {
		return nil, err
	}
	if eval.ExceptionDetails != nil {
		return nil, fmt.Errorf("browser fetch raised JavaScript exception: %v", eval.ExceptionDetails)
	}

	var res struct {
		OK          bool   `json:"ok"`
		Status      int    `json:"status"`
		StatusText  string `json:"statusText"`
		URL         string `json:"url"`
		ContentType string `json:"contentType"`
		Body        string `json:"body"`
	}
	if err := decodeCDPFetchResult(eval.Result.Value, &res); err != nil {
		return nil, err
	}
	if !res.OK {
		if res.Status == 0 {
			return nil, fmt.Errorf("browser fetch download failed: %s", res.StatusText)
		}
		return nil, fmt.Errorf("browser fetch download returned HTTP %d: %s", res.Status, res.StatusText)
	}
	data, err := base64.StdEncoding.DecodeString(res.Body)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("browser fetch returned empty download body")
	}
	return data, nil
}

func decodeCDPFetchResult(raw json.RawMessage, dst any) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return errors.New("browser fetch returned empty CDP result")
	}

	// Runtime.evaluate may return either a JSON string (when the page returns
	// JSON.stringify(...)) or a JSON object (when the browser/backend preserves
	// the returned object by value). Accept both forms so remote Chromium
	// versions/protocol variants do not break downloads.
	if strings.HasPrefix(trimmed, "{") {
		if err := json.Unmarshal(raw, dst); err != nil {
			return fmt.Errorf("decode browser fetch object result: %w", err)
		}
		return nil
	}

	var payload string
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("decode browser fetch string result: %w", err)
	}
	if strings.TrimSpace(payload) == "" {
		return errors.New("browser fetch returned empty CDP payload")
	}
	if err := json.Unmarshal([]byte(payload), dst); err != nil {
		return fmt.Errorf("decode browser fetch JSON payload: %w", err)
	}
	return nil
}

func browserDownloadHeaders(headers map[string]string) (map[string]string, string) {
	cleanHeaders := map[string]string{}
	referrer := ""
	for k, v := range headers {
		key := strings.TrimSpace(k)
		val := strings.TrimSpace(v)
		if key == "" || val == "" {
			continue
		}
		switch strings.ToLower(key) {
		case "cookie", "host", "content-length", "connection", "user-agent":
			continue
		case "referer", "referrer":
			referrer = val
			continue
		}
		cleanHeaders[key] = val
	}
	return cleanHeaders, referrer
}

type browserCookie struct{ name, value string }

func parseCookieHeader(raw string) []browserCookie {
	parts := strings.Split(raw, ";")
	out := make([]browserCookie, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || !strings.Contains(part, "=") {
			continue
		}
		name, value, _ := strings.Cut(part, "=")
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name == "" || strings.EqualFold(name, "path") || strings.EqualFold(name, "expires") || strings.EqualFold(name, "samesite") || strings.EqualFold(name, "httponly") {
			continue
		}
		out = append(out, browserCookie{name: name, value: value})
	}
	return out
}

func (s *Session) MarkDone(ctx context.Context) (SessionInfo, error) {
	if s.cdp == nil {
		return SessionInfo{}, errors.New("browser session is not ready")
	}
	if err := s.refreshPageSummary(ctx); err != nil {
		s.logger.Warn("failed to inspect browser page", "session", s.id, "tracker", s.tracker, "error", err)
	}
	if err := s.refreshCookieSummary(ctx); err != nil {
		s.logger.Warn("failed to inspect browser cookies", "session", s.id, "tracker", s.tracker, "error", err)
	}
	info := s.Status()
	if info.LooksLikeCloudflare || info.LooksLikeLoginPage {
		s.setStatus("needs_interaction")
	} else {
		s.setStatus("user_done")
	}
	info = s.Status()
	s.logger.Info("browser session marked done", "session", s.id, "tracker", s.tracker, "current_url", info.CurrentURL, "title", info.Title, "cookies", info.CookieNames, "has_cf_clearance", info.HasCloudflareClearance, "looks_like_cloudflare", info.LooksLikeCloudflare, "looks_like_login", info.LooksLikeLoginPage, "profile", info.ProfilePath, "status", info.Status)
	return info, nil
}

func (s *Session) refreshPageSummary(ctx context.Context) error {
	if s.cdp == nil {
		return errors.New("browser session is not ready")
	}
	currentURL, err := s.evalString(ctx, "location.href")
	if err != nil {
		return err
	}
	title, _ := s.evalString(ctx, "document.title || ''")
	looksCF, _ := s.evalBool(ctx, `(() => {
		const body = document.body ? document.body.innerText : '';
		const html = document.documentElement ? document.documentElement.outerHTML : '';
		const s = (document.title + ' ' + location.href + ' ' + body.slice(0, 5000) + ' ' + html.slice(0, 5000)).toLowerCase();
		return s.includes('cloudflare') || s.includes('cf-challenge') || s.includes('turnstile') || s.includes('checking your browser') || s.includes('just a moment') || s.includes('verify you are human');
	})()`)
	looksLogin, _ := s.evalBool(ctx, `(() => {
		const body = document.body ? document.body.innerText : '';
		const s = (document.title + ' ' + location.href + ' ' + body.slice(0, 3000)).toLowerCase();
		return !!document.querySelector('input[type="password"], input[name="password"], form[action*="login"], form[action*="login.php"]') || s.includes('login.php') || s.includes('вход') || s.includes('авторизац');
	})()`)
	s.mu.Lock()
	s.currentURL = currentURL
	s.title = title
	s.looksLikeCloudflare = looksCF
	s.looksLikeLoginPage = looksLogin
	s.updatedAt = time.Now()
	s.mu.Unlock()
	return nil
}

func (s *Session) refreshCookieSummary(ctx context.Context) error {
	if s.cdp == nil {
		return errors.New("browser session is not ready")
	}
	raw, err := s.cdp.Call(ctx, "Network.getAllCookies", nil)
	if err != nil {
		return err
	}
	var res struct {
		Cookies []struct {
			Name     string  `json:"name"`
			Domain   string  `json:"domain"`
			Path     string  `json:"path"`
			Expires  float64 `json:"expires"`
			Session  bool    `json:"session"`
			HTTPOnly bool    `json:"httpOnly"`
			Secure   bool    `json:"secure"`
			SameSite string  `json:"sameSite"`
		} `json:"cookies"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return err
	}
	seen := map[string]bool{}
	names := make([]string, 0, len(res.Cookies))
	details := make([]CookieDebug, 0, len(res.Cookies))
	hasCF := false
	for _, c := range res.Cookies {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			continue
		}
		details = append(details, CookieDebug{Name: name, Domain: c.Domain, Path: c.Path, Expires: c.Expires, Session: c.Session, HTTPOnly: c.HTTPOnly, Secure: c.Secure, SameSite: c.SameSite})
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
		if name == "cf_clearance" {
			hasCF = true
		}
	}
	s.mu.Lock()
	s.cookieNames = names
	s.cookieDetails = details
	s.hasCloudflareClearance = hasCF
	s.updatedAt = time.Now()
	s.mu.Unlock()
	return nil
}

func (s *Session) evalString(ctx context.Context, expression string) (string, error) {
	raw, err := s.cdp.Call(ctx, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true})
	if err != nil {
		return "", err
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", err
	}
	return res.Result.Value, nil
}

func (s *Session) evalBool(ctx context.Context, expression string) (bool, error) {
	raw, err := s.cdp.Call(ctx, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true})
	if err != nil {
		return false, err
	}
	var res struct {
		Result struct {
			Value bool `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return false, err
	}
	return res.Result.Value, nil
}

func (s *Session) WaitForReady(ctx context.Context, timeout time.Duration) error {
	if s.cdp == nil {
		return errors.New("browser session is not ready")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	var lastState string
	for time.Now().Before(deadline) {
		state, err := s.evalString(ctx, "document.readyState || ''")
		if err == nil {
			lastState = state
			if state == "complete" || state == "interactive" {
				// Give late redirects/challenge scripts a short chance to settle.
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(750 * time.Millisecond):
				}
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	if lastState != "" {
		return fmt.Errorf("wait page ready timeout, last readyState=%s", lastState)
	}
	return errors.New("wait page ready timeout")
}

func (s *Session) HTML(ctx context.Context) ([]byte, error) {
	if s.cdp == nil {
		return nil, errors.New("browser session is not ready")
	}
	if err := s.refreshPageSummary(ctx); err != nil {
		return nil, err
	}
	html, err := s.evalString(ctx, `document.documentElement ? document.documentElement.outerHTML : ''`)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(html) == "" {
		return nil, errors.New("browser returned empty DOM")
	}
	return []byte(html), nil
}

func (s *Session) Navigate(ctx context.Context, rawURL string) error {
	if s.cdp == nil {
		return errors.New("browser session is not ready")
	}
	s.mu.Lock()
	s.url = rawURL
	s.frame = nil
	s.status = "running"
	s.updatedAt = time.Now()
	s.mu.Unlock()
	_, err := s.cdp.Call(ctx, "Page.navigate", map[string]any{"url": rawURL})
	return err
}

func (s *Session) Close() {
	if s.cdp != nil {
		_, _ = s.cdp.Call(context.Background(), "Page.close", nil)
		_ = s.cdp.Close()
	}
	s.setStatus("closed")
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
}

func (s *Session) Detach() {
	if s.cdp != nil {
		_, _ = s.cdp.Call(context.Background(), "Page.stopScreencast", nil)
		_ = s.cdp.Close()
	}
	s.setStatus("detached")
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
}

func (s *Session) isClosed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status == "closed" || s.status == "error" || s.status == "detached"
}

func (s *Session) setStatus(st string) {
	s.mu.Lock()
	s.status = st
	s.updatedAt = time.Now()
	s.mu.Unlock()
}
func (s *Session) setError(msg string) {
	s.mu.Lock()
	s.status = "error"
	s.lastError = msg
	s.updatedAt = time.Now()
	s.mu.Unlock()
}
func (s *Session) updatedNow() { s.mu.Lock(); s.updatedAt = time.Now(); s.mu.Unlock() }
