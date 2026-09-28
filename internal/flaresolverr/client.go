package flaresolverr

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
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultEndpoint = "http://127.0.0.1:8191/v1"

type Config struct {
	URL     string
	Timeout time.Duration
	Debug   bool
}

type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

type noopLogger struct{}

func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Error(string, ...any) {}

type Client struct {
	mu       sync.Mutex
	cfg      Config
	logger   Logger
	sessions map[string]*sessionState
}

type sessionState struct {
	ID        string
	ProxyURL  string
	UserAgent string
	Cookies   []Cookie
}

type Cookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain,omitempty"`
	Path     string  `json:"path,omitempty"`
	Expires  float64 `json:"expires,omitempty"`
	HTTPOnly bool    `json:"httpOnly,omitempty"`
	Secure   bool    `json:"secure,omitempty"`
	SameSite string  `json:"sameSite,omitempty"`
}

type apiResponse struct {
	Status   string   `json:"status"`
	Message  string   `json:"message"`
	Version  string   `json:"version"`
	Solution solution `json:"solution"`
}

type solution struct {
	URL            string            `json:"url"`
	Status         int               `json:"status"`
	Headers        map[string]string `json:"headers"`
	Response       string            `json:"response"`
	Cookies        []Cookie          `json:"cookies"`
	UserAgent      string            `json:"userAgent"`
	TurnstileToken string            `json:"turnstile_token"`
}

func New(cfg Config, logger Logger) *Client {
	if logger == nil {
		logger = noopLogger{}
	}
	return &Client{cfg: normalizeConfig(cfg), logger: logger, sessions: map[string]*sessionState{}}
}

func normalizeConfig(cfg Config) Config {
	cfg.URL = normalizeEndpoint(cfg.URL)
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	return cfg
}

func normalizeEndpoint(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultEndpoint
	}
	raw = strings.TrimRight(raw, "/")
	if !strings.HasSuffix(raw, "/v1") {
		raw += "/v1"
	}
	return raw
}

func (c *Client) UpdateConfig(cfg Config) {
	cfg = normalizeConfig(cfg)
	c.mu.Lock()
	oldCfg := c.cfg
	old := c.sessions
	changed := oldCfg.URL != cfg.URL || oldCfg.Timeout != cfg.Timeout
	c.cfg = cfg
	if changed {
		c.sessions = map[string]*sessionState{}
	}
	c.mu.Unlock()
	if !changed {
		return
	}
	for _, s := range old {
		if s == nil || strings.TrimSpace(s.ID) == "" {
			continue
		}
		go c.destroySession(context.Background(), oldCfg, s.ID)
	}
}

func (c *Client) Close() {
	c.mu.Lock()
	cfg := c.cfg
	sessions := c.sessions
	c.sessions = map[string]*sessionState{}
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, s := range sessions {
		if s != nil && s.ID != "" {
			_ = c.destroySession(ctx, cfg, s.ID)
		}
	}
}

func (c *Client) FetchPage(ctx context.Context, tracker string, rawURL string) ([]byte, error) {
	return c.Request(ctx, tracker, http.MethodGet, rawURL, "", nil, 0, "", "")
}

func (c *Client) FetchPageWait(ctx context.Context, tracker string, rawURL string, timeout time.Duration, expectedURLNeedles []string, requiredAll []string, requiredAny []string, requiredRegex string) ([]byte, error) {
	data, err := c.Request(ctx, tracker, http.MethodGet, rawURL, "", nil, timeout, "", "")
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Request executes a GET or application/x-www-form-urlencoded POST through
// FlareSolverr. A stable FlareSolverr session is kept per tracker so Cloudflare
// clearance and tracker login cookies are reused by subsequent checks.
func (c *Client) Request(ctx context.Context, tracker, method, rawURL, postData string, cookies map[string]string, timeout time.Duration, proxyType, proxyAddress string) ([]byte, error) {
	if err := validateHTTPURL(rawURL); err != nil {
		return nil, err
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodPost {
		return nil, fmt.Errorf("FlareSolverr supports GET/POST only, got %q", method)
	}
	proxyURL, err := canonicalProxyURL(proxyType, proxyAddress)
	if err != nil {
		return nil, err
	}
	state, cfg, err := c.ensureSession(ctx, tracker, proxyURL)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = cfg.Timeout
	}
	cmd := "request.get"
	if method == http.MethodPost {
		cmd = "request.post"
	}
	payload := map[string]any{
		"cmd":        cmd,
		"url":        rawURL,
		"session":    state.ID,
		"maxTimeout": timeout.Milliseconds(),
	}
	if method == http.MethodPost {
		payload["postData"] = postData
	}
	addPayloadCookies(payload, cookies)
	resp, err := c.call(ctx, cfg, payload, timeout)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(resp.Status, "ok") {
		if strings.TrimSpace(resp.Message) == "" {
			resp.Message = "unknown FlareSolverr error"
		}
		return nil, errors.New(resp.Message)
	}
	if resp.Solution.Status >= 400 {
		return nil, fmt.Errorf("%s returned HTTP %d through FlareSolverr", rawURL, resp.Solution.Status)
	}
	c.mu.Lock()
	if current := c.sessions[sessionKey(tracker)]; current != nil && current.ID == state.ID {
		current.UserAgent = resp.Solution.UserAgent
		current.Cookies = append([]Cookie(nil), resp.Solution.Cookies...)
	}
	c.mu.Unlock()
	if cfg.Debug {
		c.logger.Info("FlareSolverr request completed", "tracker", tracker, "url", rawURL, "status", resp.Solution.Status, "cookies", len(resp.Solution.Cookies))
	}
	return []byte(resp.Solution.Response), nil
}

// SolveTurnstile opens the configured page through request.get in the same
// stable tracker session. FlareSolverr uses tabs_till_verify to focus and solve
// the Turnstile widget, then returns the value of cf-turnstile-response.
func (c *Client) SolveTurnstile(ctx context.Context, tracker, rawURL string, cookies map[string]string, tabsTillVerify int, timeout time.Duration, proxyType, proxyAddress string) (string, error) {
	if err := validateHTTPURL(rawURL); err != nil {
		return "", err
	}
	if tabsTillVerify <= 0 {
		return "", errors.New("tabs_till_verify must be greater than zero")
	}
	proxyURL, err := canonicalProxyURL(proxyType, proxyAddress)
	if err != nil {
		return "", err
	}
	state, cfg, err := c.ensureSession(ctx, tracker, proxyURL)
	if err != nil {
		return "", err
	}
	if timeout <= 0 {
		timeout = cfg.Timeout
	}
	payload := map[string]any{
		"cmd":              "request.get",
		"url":              rawURL,
		"session":          state.ID,
		"maxTimeout":       timeout.Milliseconds(),
		"tabs_till_verify": tabsTillVerify,
	}
	addPayloadCookies(payload, cookies)
	resp, err := c.call(ctx, cfg, payload, timeout)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(resp.Status, "ok") {
		if strings.TrimSpace(resp.Message) == "" {
			resp.Message = "unknown FlareSolverr error"
		}
		return "", errors.New(resp.Message)
	}
	if resp.Solution.Status >= 400 {
		return "", fmt.Errorf("%s returned HTTP %d through FlareSolverr", rawURL, resp.Solution.Status)
	}
	c.mu.Lock()
	if current := c.sessions[sessionKey(tracker)]; current != nil && current.ID == state.ID {
		current.UserAgent = resp.Solution.UserAgent
		current.Cookies = append([]Cookie(nil), resp.Solution.Cookies...)
	}
	c.mu.Unlock()
	token := strings.TrimSpace(resp.Solution.TurnstileToken)
	if token == "" {
		return "", fmt.Errorf("FlareSolverr did not return a Turnstile token for %s", rawURL)
	}
	if cfg.Debug {
		c.logger.Info("FlareSolverr Turnstile solved", "tracker", tracker, "url", rawURL)
	}
	return token, nil
}

func addPayloadCookies(payload map[string]any, cookies map[string]string) {
	if len(cookies) == 0 {
		return
	}
	items := make([]map[string]string, 0, len(cookies))
	keys := make([]string, 0, len(cookies))
	for k := range cookies {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.TrimSpace(k) == "" {
			continue
		}
		items = append(items, map[string]string{"name": k, "value": cookies[k]})
	}
	if len(items) > 0 {
		payload["cookies"] = items
	}
}

// Download performs the binary request with the cookies and User-Agent returned
// by FlareSolverr. Current FlareSolverr releases no longer expose the old
// binary download API, so this request intentionally uses the native HTTP
// client while preserving the solved browser identity.
func (c *Client) Download(ctx context.Context, tracker, rawURL string, headers map[string]string, cookieHeader string, timeout time.Duration, proxyType, proxyAddress string) ([]byte, error) {
	if err := validateHTTPURL(rawURL); err != nil {
		return nil, err
	}
	proxyURL, err := canonicalProxyURL(proxyType, proxyAddress)
	if err != nil {
		return nil, err
	}
	state, cfg, err := c.ensureSession(ctx, tracker, proxyURL)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	snapshot := *state
	snapshot.Cookies = append([]Cookie(nil), state.Cookies...)
	c.mu.Unlock()
	if timeout <= 0 {
		timeout = cfg.Timeout
	}
	transport, err := proxyTransport(proxyType, proxyAddress)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Transport: transport, Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		if strings.TrimSpace(k) != "" {
			req.Header.Set(k, v)
		}
	}
	if strings.TrimSpace(snapshot.UserAgent) != "" {
		req.Header.Set("User-Agent", snapshot.UserAgent)
	}
	addCookies(req, snapshot.Cookies, cookieHeader)
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 400 {
		return nil, fmt.Errorf("%s returned HTTP %d", rawURL, res.StatusCode)
	}
	return data, nil
}

func (c *Client) ensureSession(ctx context.Context, tracker, proxyURL string) (*sessionState, Config, error) {
	key := sessionKey(tracker)
	c.mu.Lock()
	cfg := c.cfg
	if s := c.sessions[key]; s != nil && s.ProxyURL == proxyURL {
		c.mu.Unlock()
		return s, cfg, nil
	}
	old := c.sessions[key]
	c.mu.Unlock()
	if old != nil && old.ID != "" {
		_ = c.destroySession(ctx, cfg, old.ID)
	}
	id := "tm-" + sanitizeSessionName(key) + "-" + randomHex(4)
	payload := map[string]any{"cmd": "sessions.create", "session": id}
	if proxyURL != "" {
		payload["proxy"] = map[string]string{"url": proxyURL}
	}
	resp, err := c.call(ctx, cfg, payload, cfg.Timeout)
	if err != nil {
		return nil, cfg, err
	}
	if !strings.EqualFold(resp.Status, "ok") {
		return nil, cfg, fmt.Errorf("create FlareSolverr session: %s", strings.TrimSpace(resp.Message))
	}
	s := &sessionState{ID: id, ProxyURL: proxyURL}
	c.mu.Lock()
	if existing := c.sessions[key]; existing != nil && existing.ProxyURL == proxyURL {
		c.mu.Unlock()
		_ = c.destroySession(context.Background(), cfg, id)
		return existing, cfg, nil
	}
	c.sessions[key] = s
	c.mu.Unlock()
	c.logger.Info("FlareSolverr session created", "tracker", key, "session", id)
	return s, cfg, nil
}

func (c *Client) destroySession(ctx context.Context, cfg Config, id string) error {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	resp, err := c.call(ctx, cfg, map[string]any{"cmd": "sessions.destroy", "session": id}, 10*time.Second)
	if err != nil {
		return err
	}
	if !strings.EqualFold(resp.Status, "ok") {
		return fmt.Errorf("destroy FlareSolverr session %s: %s", id, strings.TrimSpace(resp.Message))
	}
	return nil
}

func (c *Client) call(ctx context.Context, cfg Config, payload map[string]any, timeout time.Duration) (apiResponse, error) {
	var out apiResponse
	body, err := json.Marshal(payload)
	if err != nil {
		return out, err
	}
	reqCtx := ctx
	cancel := func() {}
	if _, ok := ctx.Deadline(); !ok && timeout > 0 {
		reqCtx, cancel = context.WithTimeout(ctx, timeout+5*time.Second)
	}
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := (&http.Client{}).Do(req)
	if err != nil {
		return out, fmt.Errorf("FlareSolverr %s: %w", cfg.URL, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return out, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return out, fmt.Errorf("FlareSolverr returned HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(data)))
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("decode FlareSolverr response: %w", err)
	}
	return out, nil
}

func validateHTTPURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("parse URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("FlareSolverr requires http/https URL, got %q", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("URL %q has no host", raw)
	}
	return nil
}

func sessionKey(tracker string) string {
	tracker = strings.ToLower(strings.TrimSpace(tracker))
	if tracker == "" {
		return "default"
	}
	return tracker
}

func sanitizeSessionName(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(raw) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "default"
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(buf)
}

func canonicalProxyURL(proxyType, proxyAddress string) (string, error) {
	addr := strings.TrimSpace(proxyAddress)
	if addr == "" {
		return "", nil
	}
	kind := strings.ToLower(strings.TrimSpace(proxyType))
	if strings.Contains(addr, "://") {
		u, err := url.Parse(addr)
		if err != nil || u.Host == "" {
			return "", fmt.Errorf("invalid proxy address %q", proxyAddress)
		}
		return u.String(), nil
	}
	switch kind {
	case "", "http", "https":
		return "http://" + addr, nil
	case "socks", "socks5":
		return "socks5://" + addr, nil
	default:
		return "", fmt.Errorf("unsupported proxy type %q", proxyType)
	}
}

func proxyTransport(proxyType, proxyAddress string) (*http.Transport, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	addr := strings.TrimSpace(proxyAddress)
	if addr == "" {
		return transport, nil
	}
	kind := strings.ToLower(strings.TrimSpace(proxyType))
	switch kind {
	case "", "http", "https":
		u, err := url.Parse(addr)
		if err != nil || u.Host == "" {
			u, err = url.Parse("http://" + addr)
			if err != nil || u.Host == "" {
				return nil, fmt.Errorf("invalid proxy address %q", proxyAddress)
			}
		}
		transport.Proxy = http.ProxyURL(u)
		return transport, nil
	case "socks", "socks5":
		raw := addr
		if strings.Contains(raw, "://") {
			u, err := url.Parse(raw)
			if err != nil {
				return nil, err
			}
			raw = u.Host
		}
		if _, _, err := net.SplitHostPort(raw); err != nil {
			return nil, fmt.Errorf("proxy address must be host:port, got %q", proxyAddress)
		}
		dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, "tcp", raw)
			if err != nil {
				return nil, err
			}
			if err := socks5Connect(ctx, conn, address); err != nil {
				_ = conn.Close()
				return nil, err
			}
			return conn, nil
		}
		transport.Proxy = nil
		return transport, nil
	default:
		return nil, fmt.Errorf("unsupported proxy type %q", proxyType)
	}
}

func socks5Connect(ctx context.Context, conn net.Conn, target string) error {
	deadline, ok := ctx.Deadline()
	if ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	}
	defer conn.SetDeadline(time.Time{})
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return err
	}
	buf := make([]byte, 262)
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return err
	}
	if buf[0] != 0x05 || buf[1] != 0x00 {
		return errors.New("SOCKS5 proxy rejected no-auth method")
	}
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 0 || port > 65535 {
		return fmt.Errorf("invalid target port %q", portText)
	}
	req := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			req = append(req, 0x01)
			req = append(req, v4...)
		} else {
			req = append(req, 0x04)
			req = append(req, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return errors.New("target host is too long for SOCKS5")
		}
		req = append(req, 0x03, byte(len(host)))
		req = append(req, host...)
	}
	req = append(req, byte(port>>8), byte(port))
	if _, err := conn.Write(req); err != nil {
		return err
	}
	if _, err := io.ReadFull(conn, buf[:4]); err != nil {
		return err
	}
	if buf[0] != 0x05 || buf[1] != 0x00 {
		return fmt.Errorf("SOCKS5 connect failed with code %d", buf[1])
	}
	var skip int
	switch buf[3] {
	case 0x01:
		skip = 4
	case 0x03:
		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return err
		}
		skip = int(buf[0])
	case 0x04:
		skip = 16
	default:
		return fmt.Errorf("invalid SOCKS5 address type %d", buf[3])
	}
	if skip > 0 {
		if _, err := io.ReadFull(conn, buf[:skip]); err != nil {
			return err
		}
	}
	_, err = io.ReadFull(conn, buf[:2])
	return err
}

func addCookies(req *http.Request, cookies []Cookie, explicitHeader string) {
	u := req.URL
	values := map[string]string{}
	for _, c := range cookies {
		if strings.TrimSpace(c.Name) == "" || !cookieMatches(c, u) {
			continue
		}
		values[c.Name] = c.Value
	}
	for _, part := range strings.Split(explicitHeader, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, value, ok := strings.Cut(part, "=")
		if !ok || strings.TrimSpace(name) == "" {
			continue
		}
		values[strings.TrimSpace(name)] = strings.TrimSpace(value)
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		req.AddCookie(&http.Cookie{Name: k, Value: values[k]})
	}
}

func cookieMatches(c Cookie, u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	domain := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(c.Domain), "."))
	if domain != "" && host != domain && !strings.HasSuffix(host, "."+domain) {
		return false
	}
	path := strings.TrimSpace(c.Path)
	if path != "" && path != "/" && !strings.HasPrefix(u.EscapedPath(), path) {
		return false
	}
	if c.Secure && u.Scheme != "https" {
		return false
	}
	return true
}
