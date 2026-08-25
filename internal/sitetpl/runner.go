package sitetpl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type Registry struct {
	mu        sync.RWMutex
	templates map[string]Template
}

func DefaultRegistry() *Registry {
	r := &Registry{templates: map[string]Template{}}
	r.Register(DefaultRutrackerTemplate())
	r.Register(DefaultNNMClubTemplate())
	r.Register(DefaultTapochekTemplate())
	return r
}

func (r *Registry) Register(t Template) {
	if r == nil {
		return
	}
	NormalizeTemplate(&t)
	if t.Site == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.templates == nil {
		r.templates = make(map[string]Template)
	}
	r.templates[strings.ToLower(t.Site)] = t
	for _, domain := range t.Domains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain != "" {
			r.templates[domain] = t
		}
	}
}

func (r *Registry) Get(site string) (Template, bool) {
	if r == nil {
		return Template{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.templates[strings.ToLower(strings.TrimSpace(site))]
	return t, ok
}

func NormalizeTemplate(t *Template) {
	if t == nil {
		return
	}
	if t.Version == 0 {
		t.Version = t.SchemaVersion
	}
	if strings.TrimSpace(t.Site) == "" {
		t.Site = strings.TrimSpace(t.ID)
	}
	t.Site = strings.ToLower(strings.TrimSpace(t.Site))
	if strings.TrimSpace(t.ID) == "" {
		t.ID = t.Site
	}
	if len(t.Domains) == 0 && t.Site != "" {
		t.Domains = []string{t.Site}
	}
	if strings.TrimSpace(t.Kind) == "" {
		t.Kind = "forum_topic"
	}
	if t.Mode == "" {
		t.Mode = ModeHTTP
	}

	// Template v1 has a readable declarative shape (urls/topic/download.before),
	// while the runner internally consumes HTTPRequest/ItemFlow/DownloadFlow. Keep
	// this compatibility layer here so external tracker templates do not need to
	// know the old scaffold structs.
	if strings.TrimSpace(t.HTTP.BaseURL) == "" && strings.TrimSpace(t.URLs.Base) != "" {
		t.HTTP.BaseURL = strings.TrimSpace(t.URLs.Base)
	}
	if strings.TrimSpace(t.Encoding.Response) == "" {
		t.Encoding.Response = firstNonEmpty(t.Encoding.Page, t.Encoding.Target)
	}
	if strings.TrimSpace(t.Encoding.Form) != "" {
		if t.Auth.LoginForm != nil && strings.TrimSpace(t.Auth.LoginForm.FormEncoding) == "" {
			t.Auth.LoginForm.FormEncoding = t.Encoding.Form
		}
		if t.Auth.Login != nil && strings.TrimSpace(t.Auth.Login.FormEncoding) == "" {
			t.Auth.Login.FormEncoding = t.Encoding.Form
		}
	}

	if t.Auth.Login == nil && t.Auth.LoginForm != nil {
		t.Auth.Login = t.Auth.LoginForm
	}
	if t.Auth.Login != nil && strings.TrimSpace(t.Auth.Login.URL) == "" && strings.TrimSpace(t.URLs.Login) != "" {
		t.Auth.Login.URL = strings.TrimSpace(t.URLs.Login)
	}
	if t.Auth.Check == nil && strings.TrimSpace(t.URLs.AuthCheck) != "" {
		t.Auth.Check = &HTTPRequest{Method: "GET", URL: strings.TrimSpace(t.URLs.AuthCheck), Success: normalizeMatchRules(t.Auth.LoggedIn)}
	}
	if t.Auth.Check != nil {
		if strings.TrimSpace(t.Auth.Check.Method) == "" {
			t.Auth.Check.Method = "GET"
		}
		if strings.TrimSpace(t.Auth.Check.URL) == "" && strings.TrimSpace(t.URLs.AuthCheck) != "" {
			t.Auth.Check.URL = strings.TrimSpace(t.URLs.AuthCheck)
		}
		t.Auth.Check.Success = normalizeMatchRules(t.Auth.Check.Success)
	}
	if t.Auth.Login != nil {
		if strings.TrimSpace(t.Auth.Login.Method) == "" {
			t.Auth.Login.Method = "POST"
		}
		t.Auth.Login.Success = normalizeMatchRules(t.Auth.Login.Success)
	}

	if strings.TrimSpace(t.Item.Page.URL) == "" && strings.TrimSpace(t.URLs.Topic) != "" {
		t.Item.Page = HTTPRequest{Method: "GET", URL: strings.TrimSpace(t.URLs.Topic)}
	}
	if strings.TrimSpace(t.Item.Page.Method) == "" && strings.TrimSpace(t.Item.Page.URL) != "" {
		t.Item.Page.Method = "GET"
	}
	if topicReadyDefined(t.Topic.Ready) {
		t.Item.Page.Success = mergeReadyIntoMatchRules(t.Item.Page.Success, t.Topic.Ready)
	}
	t.Item.Page.Success = normalizeMatchRules(t.Item.Page.Success)
	if t.Item.Extract == nil {
		t.Item.Extract = map[string]Extract{}
	}
	if extractDefined(t.Topic.Title) {
		t.Item.Extract["title"] = normalizeExtract(t.Topic.Title, t.Defaults)
	}
	if extractDefined(t.Topic.UpdatedAt) {
		t.Item.Extract["updated_at"] = normalizeExtract(t.Topic.UpdatedAt, t.Defaults)
	}
	for k, ex := range t.Item.Extract {
		t.Item.Extract[k] = normalizeExtract(ex, t.Defaults)
	}
	if matchRulesDefined(t.Topic.Closed) {
		t.Item.Closed = normalizeMatchRules(t.Topic.Closed)
	} else {
		t.Item.Closed = normalizeMatchRules(t.Item.Closed)
	}

	if strings.TrimSpace(t.Download.Request.URL) == "" && strings.TrimSpace(t.Download.URL) != "" {
		t.Download.Request.URL = strings.TrimSpace(t.Download.URL)
	}
	if strings.TrimSpace(t.Download.Request.Method) == "" && strings.TrimSpace(t.Download.Request.URL) != "" {
		t.Download.Request.Method = "GET"
	}
	if len(t.Download.Before.Headers) > 0 {
		if t.Download.Request.Headers == nil {
			t.Download.Request.Headers = map[string]string{}
		}
		for k, v := range t.Download.Before.Headers {
			if strings.TrimSpace(k) != "" {
				t.Download.Request.Headers[k] = v
			}
		}
	}
	if len(t.Download.Before.SetCookie) > 0 {
		if t.Download.Request.Cookies == nil {
			t.Download.Request.Cookies = map[string]string{}
		}
		for _, c := range t.Download.Before.SetCookie {
			if strings.TrimSpace(c.Name) != "" {
				t.Download.Request.Cookies[strings.TrimSpace(c.Name)] = c.Value
			}
		}
	}
	t.Download.URLFromPage = normalizeExtract(t.Download.URLFromPage, t.Defaults)
	t.Download.Request.Success = normalizeMatchRules(t.Download.Request.Success)
}

// TemplateInfo is the small public subset the core/UI need to build tracker menus.
type TemplateInfo struct {
	ID                string
	Name              string
	Domains           []string
	Kind              string
	DefaultAccessMode string
	Source            string
}

func (r *Registry) List() []TemplateInfo {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	byID := map[string]TemplateInfo{}
	for _, t := range r.templates {
		NormalizeTemplate(&t)
		id := strings.ToLower(strings.TrimSpace(firstNonEmpty(t.Site, t.ID)))
		if id == "" {
			continue
		}
		if _, exists := byID[id]; exists {
			continue
		}
		mode := firstNonEmpty(t.Defaults.AccessMode, t.Defaults.DefaultAccessMode)
		if strings.TrimSpace(mode) == "" {
			mode = "native"
		}
		byID[id] = TemplateInfo{
			ID:                id,
			Name:              strings.TrimSpace(t.Name),
			Domains:           append([]string(nil), t.Domains...),
			Kind:              strings.TrimSpace(t.Kind),
			DefaultAccessMode: strings.ToLower(strings.TrimSpace(mode)),
			Source:            strings.TrimSpace(t.Source),
		}
	}
	out := make([]TemplateInfo, 0, len(byID))
	for _, info := range byID {
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *Runner) ListTemplates() []TemplateInfo {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	registry := r.registry
	r.mu.RUnlock()
	return registry.List()
}

type Runner struct {
	mu       sync.RWMutex
	registry *Registry
	client   *http.Client
	now      func() time.Time
}

type RunnerOption func(*Runner)

func WithHTTPClient(client *http.Client) RunnerOption {
	return func(r *Runner) {
		if client != nil {
			r.client = client
		}
	}
}

func WithRegistry(registry *Registry) RunnerOption {
	return func(r *Runner) {
		if registry != nil {
			r.registry = registry
		}
	}
}

func NewRunner(opts ...RunnerOption) *Runner {
	jar, _ := cookiejar.New(nil)
	r := &Runner{
		registry: DefaultRegistry(),
		client: &http.Client{
			Timeout: 30 * time.Second,
			Jar:     jar,
		},
		now: time.Now,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

type LoginCheckRequest struct {
	Tracker     string
	Credential  Credential
	Settings    Settings
	OpenBrowser bool
	Browser     BrowserPageFetcher
}

type LoginCheckResult struct {
	OK            bool
	Message       string
	LoginURL      string
	ProfilePath   string
	SessionCookie string
}

type CheckRequest struct {
	Item       Item
	Credential Credential
	Settings   Settings
	Browser    BrowserPageFetcher
}

type Item struct {
	Tracker   string
	Name      string
	URL       string
	TorrentID string
	UpdatedAt *time.Time
}

type Credential struct {
	Login      string
	Password   string
	Passkey    string
	Cookie     string
	AccessMode string
	UseProxy   bool
}

type Settings struct {
	UserAgent    string
	Timeout      time.Duration
	ProxyEnabled bool
	ProxyType    string
	ProxyAddress string
}

// BrowserPageFetcher is implemented by the BrowserBroker. It lets the HTTP
// template runner reuse the shared interactive Chromium profile for regular
// page loads when credentials.access_mode == chromium.
type BrowserPageFetcher interface {
	FetchPage(ctx context.Context, tracker string, rawURL string) ([]byte, error)
}

// BrowserPageWaiter is implemented by newer BrowserBroker versions. It waits
// for the expected tracker URL and optional positive page markers instead of
// trying to classify Cloudflare/challenge pages by their titles or text.
type BrowserPageWaiter interface {
	FetchPageWait(ctx context.Context, tracker string, rawURL string, timeout time.Duration, expectedURLNeedles []string, requiredAll []string, requiredAny []string, requiredRegex string) ([]byte, error)
}

// BrowserDownloader is implemented by BrowserBroker. It uses the same shared
// Chromium profile to download binary files, which is required for trackers
// that allow topic pages through Cloudflare but still reject native HTTP
// requests to /dl.php.
type BrowserDownloader interface {
	Download(ctx context.Context, tracker string, rawURL string, headers map[string]string, cookieHeader string) ([]byte, error)
}

func (r *Runner) SetRegistry(registry *Registry) {
	if registry == nil {
		return
	}
	r.mu.Lock()
	r.registry = registry
	r.mu.Unlock()
}

func (r *Runner) Registry() *Registry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.registry
}

func (r *Runner) Check(ctx context.Context, req CheckRequest) (CheckResult, error) {
	tmpl, ok := r.Registry().Get(req.Item.Tracker)
	if !ok {
		return CheckResult{}, fmt.Errorf("site template for %s is not registered", req.Item.Tracker)
	}
	if tmpl.Mode == "" {
		tmpl.Mode = ModeHTTP
	}
	if tmpl.Mode != ModeHTTP {
		return CheckResult{}, fmt.Errorf("template %s uses unsupported mode %q", tmpl.Site, tmpl.Mode)
	}

	client, err := cloneClientForCredential(r.client, req.Settings, req.Credential)
	if err != nil {
		return CheckResult{}, err
	}
	vars := varsFromRequest(req)
	access, err := r.newSiteAccess(tmpl, client, req.Credential, req.Browser)
	if err != nil {
		return CheckResult{}, err
	}
	if err := access.Prepare(ctx, vars, req.Settings); err != nil {
		return CheckResult{}, err
	}

	body, err := access.FetchPage(ctx, tmpl.Item.Page, vars, req.Settings)
	if err != nil {
		return CheckResult{}, fmt.Errorf("load item page: %w", err)
	}
	page := decodeBody(body, tmpl.Encoding.Response)

	result := CheckResult{}
	if tmpl.Item.Closed.Contains != "" && strings.Contains(page, tmpl.Item.Closed.Contains) {
		result.Closed = true
	}
	for _, needle := range tmpl.Item.Closed.ContainsAny {
		if needle != "" && strings.Contains(page, needle) {
			result.Closed = true
			break
		}
	}

	if ex, ok := tmpl.Item.Extract["title"]; ok {
		result.Title = extractString(page, ex)
	}
	if result.Title == "" {
		result.Title = req.Item.Name
	}

	if ex, ok := tmpl.Item.Extract["updated_at"]; ok {
		value := extractString(page, ex)
		if value != "" {
			parsed, err := parseTemplateTime(value, ex)
			if err != nil {
				return CheckResult{}, fmt.Errorf("parse updated_at %q: %w", value, err)
			}
			result.UpdatedAt = &parsed
			if req.Item.UpdatedAt == nil || !sameSecond(*req.Item.UpdatedAt, parsed) {
				result.Updated = true
			}
		}
	}

	if result.Updated {
		result.Message = "Найдено обновление."
		downloadReq := tmpl.Download.Request
		if strings.TrimSpace(downloadReq.URL) == "" && extractDefined(tmpl.Download.URLFromPage) {
			downloadURL := extractString(page, tmpl.Download.URLFromPage)
			if strings.TrimSpace(downloadURL) == "" {
				return CheckResult{}, errors.New("download torrent: download URL was not found on topic page")
			}
			resolved, err := resolveTemplateURL(tmpl, render(tmpl.Item.Page.URL, vars), downloadURL)
			if err != nil {
				return CheckResult{}, fmt.Errorf("download torrent: resolve URL %q: %w", downloadURL, err)
			}
			downloadReq.URL = resolved
		}
		if strings.TrimSpace(downloadReq.URL) != "" {
			torrent, err := access.DownloadTorrent(ctx, downloadReq, vars, req.Settings)
			if err != nil {
				return CheckResult{}, fmt.Errorf("download torrent: %w", err)
			}
			if err := validateDownloadedTorrent(torrent, tmpl.Download.Validate); err != nil {
				return CheckResult{}, err
			}
			result.TorrentData = torrent
		}
	} else {
		result.Message = "Обновлений не найдено."
	}
	if result.Closed {
		result.Message = strings.TrimSpace(result.Message + " Тема закрыта.")
	}
	result.SessionCookie = access.SessionCookie()
	return result, nil
}

func (r *Runner) BrowserLoginTarget(tracker string) (LoginCheckResult, error) {
	tmpl, ok := r.Registry().Get(tracker)
	if !ok {
		return LoginCheckResult{}, fmt.Errorf("site template for %s is not registered", tracker)
	}
	return LoginCheckResult{
		OK:       false,
		Message:  "Требуется интерактивная Chromium-сессия.",
		LoginURL: browserLoginURL(tmpl),
	}, nil
}

func (r *Runner) CheckLogin(ctx context.Context, req LoginCheckRequest) (LoginCheckResult, error) {
	tmpl, ok := r.Registry().Get(req.Tracker)
	if !ok {
		return LoginCheckResult{}, fmt.Errorf("site template for %s is not registered", req.Tracker)
	}
	if strings.EqualFold(req.Credential.AccessMode, "chromium") {
		return r.checkLoginChromium(ctx, tmpl, req)
	}
	return r.checkLoginNative(ctx, tmpl, req)
}

func (r *Runner) checkLoginNative(ctx context.Context, tmpl Template, req LoginCheckRequest) (LoginCheckResult, error) {
	client, err := cloneClientForCredential(r.client, req.Settings, req.Credential)
	if err != nil {
		return LoginCheckResult{}, err
	}
	vars := varsFromLoginRequest(req)
	if err := applyCredentialCookies(client, tmpl, req.Credential.Cookie); err != nil {
		return LoginCheckResult{}, err
	}
	if tmpl.Auth.Check != nil {
		if _, err := r.do(ctx, client, *tmpl.Auth.Check, vars, req.Settings); err == nil {
			return LoginCheckResult{OK: true, Message: "Сохранённая native-сессия трекера рабочая.", SessionCookie: cookieHeaderForTemplate(client, tmpl)}, nil
		}
	}
	if tmpl.Auth.Login == nil {
		return LoginCheckResult{}, errors.New("site template has no native login flow")
	}
	if strings.TrimSpace(req.Credential.Login) == "" || strings.TrimSpace(req.Credential.Password) == "" {
		return LoginCheckResult{}, errors.New("login and password are required for native mode")
	}
	if _, err := r.do(ctx, client, *tmpl.Auth.Login, vars, req.Settings); err != nil {
		return LoginCheckResult{}, fmt.Errorf("native login: %w", err)
	}
	if tmpl.Auth.Check != nil {
		if _, err := r.do(ctx, client, *tmpl.Auth.Check, vars, req.Settings); err != nil {
			return LoginCheckResult{}, fmt.Errorf("native login check: %w", err)
		}
	}
	return LoginCheckResult{OK: true, Message: "Native-логин успешен, session cookie сохранена.", SessionCookie: cookieHeaderForTemplate(client, tmpl)}, nil
}

func (r *Runner) checkLoginChromium(ctx context.Context, tmpl Template, req LoginCheckRequest) (LoginCheckResult, error) {
	loginURL := browserLoginURL(tmpl)
	if req.Browser == nil {
		return LoginCheckResult{}, errors.New("chromium access mode requires BrowserBroker")
	}
	vars := varsFromLoginRequest(req)
	if tmpl.Auth.Check != nil {
		checkURL := render(tmpl.Auth.Check.URL, vars)
		if strings.TrimSpace(checkURL) != "" {
			success := renderMatchRules(tmpl.Auth.Check.Success, vars)
			var dom []byte
			var err error
			if waiter, ok := req.Browser.(BrowserPageWaiter); ok {
				dom, err = waiter.FetchPageWait(ctx, tmpl.Site, checkURL, req.Settings.Timeout, expectedURLNeedles(checkURL), requiredAllMarkers(success), requiredAnyMarkers(success), strings.TrimSpace(success.Regex))
			} else {
				dom, err = req.Browser.FetchPage(ctx, tmpl.Site, checkURL)
			}
			if err == nil {
				page := decodeBody(dom, tmpl.Encoding.Response)
				if matchSuccess(page, success) {
					return LoginCheckResult{OK: true, Message: "Chromium-профиль авторизован на трекере.", LoginURL: loginURL}, nil
				}
			}
		}
	}
	if req.OpenBrowser {
		return LoginCheckResult{OK: false, Message: "Открой интерактивную Chromium-сессию и пройди Cloudflare/CAPTCHA/login вручную.", LoginURL: loginURL}, nil
	}
	return LoginCheckResult{OK: false, Message: "Chromium-профиль пока не авторизован. Нажми «Проверить логин» с открытием браузера и пройди Cloudflare/CAPTCHA/login вручную.", LoginURL: loginURL}, nil
}

func renderedHeaders(headers map[string]string, vars map[string]string) map[string]string {
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		out[k] = render(v, vars)
	}
	return out
}

func renderedCookieHeader(cookies map[string]string, vars map[string]string) string {
	if len(cookies) == 0 {
		return ""
	}
	keys := make([]string, 0, len(cookies))
	for k := range cookies {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := strings.TrimSpace(render(cookies[k], vars))
		if strings.TrimSpace(k) == "" || v == "" {
			continue
		}
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "; ")
}

func (r *Runner) do(ctx context.Context, client *http.Client, spec HTTPRequest, vars map[string]string, settings Settings) ([]byte, error) {
	method := strings.ToUpper(strings.TrimSpace(spec.Method))
	if method == "" {
		method = http.MethodGet
	}
	rawURL := render(spec.URL, vars)
	if rawURL == "" {
		return nil, errors.New("request URL is empty")
	}

	var body io.Reader
	if len(spec.Form) > 0 {
		body = strings.NewReader(encodeForm(spec.Form, vars, spec.FormEncoding))
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}
	if len(spec.Form) > 0 {
		httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if settings.UserAgent != "" {
		httpReq.Header.Set("User-Agent", settings.UserAgent)
	}
	for k, v := range spec.Headers {
		httpReq.Header.Set(k, render(v, vars))
	}
	for k, v := range spec.Cookies {
		httpReq.AddCookie(&http.Cookie{Name: k, Value: render(v, vars)})
	}

	res, err := client.Do(httpReq)
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
	success := renderMatchRules(spec.Success, vars)
	if !matchSuccess(string(data), success) {
		return nil, fmt.Errorf("success markers were not found in %s", rawURL)
	}
	return data, nil
}

func applyCredentialCookies(client *http.Client, tmpl Template, rawCookie string) error {
	rawCookie = strings.TrimSpace(rawCookie)
	if rawCookie == "" {
		return nil
	}
	if client.Jar == nil {
		jar, _ := cookiejar.New(nil)
		client.Jar = jar
	}
	u, err := cookieURLForTemplate(tmpl)
	if err != nil {
		return err
	}
	cookies := parseCookieHeader(rawCookie)
	if len(cookies) == 0 {
		return nil
	}
	client.Jar.SetCookies(u, cookies)
	return nil
}

func applyCookiesForURL(client *http.Client, rawURL string, rawCookie string) error {
	if strings.TrimSpace(rawCookie) == "" {
		return nil
	}
	if client.Jar == nil {
		jar, _ := cookiejar.New(nil)
		client.Jar = jar
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	client.Jar.SetCookies(u, parseCookieHeader(rawCookie))
	return nil
}

func cookieHeaderForTemplate(client *http.Client, tmpl Template) string {
	if client == nil || client.Jar == nil {
		return ""
	}
	u, err := cookieURLForTemplate(tmpl)
	if err != nil {
		return ""
	}
	cookies := client.Jar.Cookies(u)
	parts := make([]string, 0, len(cookies))
	for _, c := range cookies {
		if c.Name == "" || c.Value == "" {
			continue
		}
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

func cookieURLForTemplate(tmpl Template) (*url.URL, error) {
	raw := strings.TrimSpace(tmpl.HTTP.BaseURL)
	if raw == "" {
		raw = strings.TrimSpace(tmpl.Item.Page.URL)
	}
	if raw == "" && tmpl.Auth.Login != nil {
		raw = strings.TrimSpace(tmpl.Auth.Login.URL)
	}
	if raw == "" {
		return nil, errors.New("template does not define a base URL for cookies")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" {
		u.Scheme = "https"
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return u, nil
}

func parseCookieHeader(raw string) []*http.Cookie {
	parts := strings.Split(raw, ";")
	cookies := make([]*http.Cookie, 0, len(parts))
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
		cookies = append(cookies, &http.Cookie{Name: name, Value: value, Path: "/"})
	}
	return cookies
}

func encodeForm(form map[string]string, vars map[string]string, encoding string) string {
	keys := make([]string, 0, len(form))
	for k := range form {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		value := render(form[k], vars)
		parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(encodeString(value, encoding)))
	}
	return strings.Join(parts, "&")
}

func encodeString(s string, encoding string) string {
	if strings.EqualFold(encoding, "windows-1251") || strings.EqualFold(encoding, "cp1251") {
		return string(encodeWindows1251(s))
	}
	return s
}

func requestCanUseBrowser(spec HTTPRequest, rawURL string) bool {
	method := strings.ToUpper(strings.TrimSpace(spec.Method))
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet || len(spec.Form) > 0 {
		return false
	}
	lowerURL := strings.ToLower(rawURL)
	return !strings.Contains(lowerURL, "/dl.php") && !strings.Contains(lowerURL, ".torrent")
}

func browserLoginURL(tmpl Template) string {
	if tmpl.Auth.Login != nil && strings.TrimSpace(tmpl.Auth.Login.URL) != "" {
		return strings.TrimSpace(tmpl.Auth.Login.URL)
	}
	if strings.TrimSpace(tmpl.HTTP.BaseURL) != "" {
		return strings.TrimRight(tmpl.HTTP.BaseURL, "/") + "/"
	}
	return strings.TrimSpace(tmpl.Item.Page.URL)
}

func varsFromLoginRequest(req LoginCheckRequest) map[string]string {
	return map[string]string{
		"credentials.login":    req.Credential.Login,
		"credentials.password": req.Credential.Password,
		"credentials.passkey":  req.Credential.Passkey,
		"credentials.cookie":   req.Credential.Cookie,
	}
}

func varsFromRequest(req CheckRequest) map[string]string {
	return map[string]string{
		"item.tracker":         req.Item.Tracker,
		"item.name":            req.Item.Name,
		"item.url":             req.Item.URL,
		"item.torrent_id":      req.Item.TorrentID,
		"credentials.login":    req.Credential.Login,
		"credentials.password": req.Credential.Password,
		"credentials.passkey":  req.Credential.Passkey,
		"credentials.cookie":   req.Credential.Cookie,
	}
}

func resolveTemplateURL(tmpl Template, baseRaw string, raw string) (string, error) {
	raw = strings.TrimSpace(htmlUnescape(raw))
	if raw == "" {
		return "", errors.New("empty URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.IsAbs() {
		return u.String(), nil
	}
	base := strings.TrimSpace(baseRaw)
	if base == "" {
		base = firstNonEmpty(tmpl.URLs.Base, tmpl.HTTP.BaseURL)
	}
	if base == "" {
		return "", errors.New("template has no base URL")
	}
	bu, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	return bu.ResolveReference(u).String(), nil
}

var templateVarRe = regexp.MustCompile(`\{\{\s*([^}]+?)\s*\}\}`)

func render(s string, vars map[string]string) string {
	return templateVarRe.ReplaceAllStringFunc(s, func(token string) string {
		m := templateVarRe.FindStringSubmatch(token)
		if len(m) != 2 {
			return token
		}
		return vars[strings.TrimSpace(m[1])]
	})
}

func extractString(page string, ex Extract) string {
	source := page
	if ex.Selector == "title" {
		re := regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
		if m := re.FindStringSubmatch(page); len(m) == 2 {
			source = m[1]
		} else {
			source = ""
		}
	}
	if strings.TrimSpace(ex.Regex) != "" {
		re, err := regexp.Compile(ex.Regex)
		if err == nil {
			if m := re.FindStringSubmatch(source); len(m) > 1 {
				source = m[1]
			} else if m := re.FindStringSubmatch(page); len(m) > 1 {
				source = m[1]
			} else {
				source = ""
			}
		}
	}
	source = htmlUnescape(stripTags(strings.TrimSpace(source)))
	for _, rule := range ex.Cleanup {
		if rule.TrimPrefix != "" {
			source = strings.TrimPrefix(source, rule.TrimPrefix)
		}
		if rule.TrimSuffix != "" {
			source = strings.TrimSuffix(source, rule.TrimSuffix)
		}
		if rule.Replace != "" {
			source = strings.ReplaceAll(source, rule.Replace, rule.With)
		}
		pattern := firstNonEmpty(rule.Regex, rule.Regexp)
		if pattern != "" {
			if re, err := regexp.Compile(pattern); err == nil {
				source = re.ReplaceAllString(source, rule.With)
			}
		}
	}
	return strings.TrimSpace(source)
}

func stripTags(s string) string {
	return regexp.MustCompile(`(?s)<[^>]*>`).ReplaceAllString(s, "")
}

func htmlUnescape(s string) string {
	replacer := strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", "\"", "&#39;", "'", "&nbsp;", " ")
	return replacer.Replace(s)
}

func parseTemplateTime(value string, ex Extract) (time.Time, error) {
	v := strings.TrimSpace(value)
	if strings.EqualFold(ex.Locale, "ru") {
		v = replaceRussianMonths(v)
	}
	loc := time.Local
	if strings.TrimSpace(ex.Timezone) != "" {
		if loaded, err := time.LoadLocation(strings.TrimSpace(ex.Timezone)); err == nil {
			loc = loaded
		}
	}
	layouts := make([]string, 0, 6+len(ex.Layouts))
	if strings.TrimSpace(ex.Layout) != "" {
		layouts = append(layouts, ex.Layout)
	}
	layouts = append(layouts, ex.Layouts...)
	layouts = append(layouts, "02-Jan-06 15:04", "02-01-06 15:04", "02.01.2006, 15:04", "02.01.2006, 15:04:05", "2006-01-02 15:04:05", time.RFC3339)
	var lastErr error
	for _, layout := range layouts {
		if strings.TrimSpace(layout) == "" {
			continue
		}
		parsed, err := time.ParseInLocation(layout, v, loc)
		if err == nil {
			return parsed, nil
		}
		lastErr = err
	}
	return time.Time{}, lastErr
}

func replaceRussianMonths(v string) string {
	repl := strings.NewReplacer(
		"Янв", "Jan", "янв", "Jan", "Фев", "Feb", "фев", "Feb", "Мар", "Mar", "мар", "Mar",
		"Апр", "Apr", "апр", "Apr", "Май", "May", "май", "May", "Июн", "Jun", "июн", "Jun",
		"Июл", "Jul", "июл", "Jul", "Авг", "Aug", "авг", "Aug", "Сен", "Sep", "сен", "Sep",
		"Окт", "Oct", "окт", "Oct", "Ноя", "Nov", "ноя", "Nov", "Дек", "Dec", "дек", "Dec",
	)
	return repl.Replace(v)
}

func sameSecond(a, b time.Time) bool {
	return a.Truncate(time.Second).Equal(b.Truncate(time.Second))
}

func validateDownloadedTorrent(data []byte, v Validate) error {
	if v.MaxSizeMB > 0 && len(data) > v.MaxSizeMB<<20 {
		return fmt.Errorf("downloaded torrent is too large: %d bytes", len(data))
	}
	trimmed := bytes.TrimSpace(data)
	lower := strings.ToLower(string(trimmed[:min(len(trimmed), 512)]))
	for _, prefix := range v.RejectIfStartsWith {
		prefix = strings.ToLower(strings.TrimSpace(prefix))
		if prefix != "" && strings.HasPrefix(lower, prefix) {
			return errors.New("downloaded file is an HTML/error page, not a torrent")
		}
	}
	if v.BencodeTorrent && !looksLikeTorrent(data) {
		return errors.New("downloaded file does not look like a torrent")
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func looksLikeTorrent(data []byte) bool {
	trimmed := bytes.TrimSpace(data)
	return len(trimmed) > 20 && trimmed[0] == 'd' && bytes.Contains(trimmed, []byte("announce")) && bytes.Contains(trimmed, []byte("info"))
}

func decodeBody(data []byte, encoding string) string {
	if strings.EqualFold(encoding, "windows-1251") || strings.EqualFold(encoding, "cp1251") {
		// Browser fallback returns an already-rendered UTF-8 DOM. Avoid decoding
		// it as cp1251, while still decoding legacy tracker HTML correctly.
		if utf8.Valid(data) {
			return string(data)
		}
		return decodeWindows1251(data)
	}
	return string(data)
}

func decodeWindows1251(data []byte) string {
	runes := make([]rune, 0, len(data))
	for _, b := range data {
		switch {
		case b < 0x80:
			runes = append(runes, rune(b))
		case b >= 0xC0:
			runes = append(runes, rune(0x0410+int(b)-0xC0))
		case b >= 0xE0:
			runes = append(runes, rune(0x0430+int(b)-0xE0))
		default:
			runes = append(runes, cp1251Extra[b])
		}
	}
	return string(runes)
}

var cp1251Extra = map[byte]rune{
	0xA8: 'Ё', 0xB8: 'ё', 0xB9: '№', 0xAB: '«', 0xBB: '»',
	0x80: 'Ђ', 0x81: 'Ѓ', 0x82: '‚', 0x83: 'ѓ', 0x84: '„', 0x85: '…', 0x86: '†', 0x87: '‡',
	0x88: '€', 0x89: '‰', 0x8A: 'Љ', 0x8B: '‹', 0x8C: 'Њ', 0x8D: 'Ќ', 0x8E: 'Ћ', 0x8F: 'Џ',
	0x90: 'ђ', 0x91: '‘', 0x92: '’', 0x93: '“', 0x94: '”', 0x95: '•', 0x96: '–', 0x97: '—',
	0x99: '™', 0x9A: 'љ', 0x9B: '›', 0x9C: 'њ', 0x9D: 'ќ', 0x9E: 'ћ', 0x9F: 'џ',
	0xA1: 'Ў', 0xA2: 'ў', 0xA3: 'Ј', 0xA5: 'Ґ', 0xAA: 'Є', 0xAF: 'Ї', 0xB2: 'І', 0xB3: 'і', 0xB4: 'ґ', 0xBA: 'є', 0xBF: 'ї',
}

func encodeWindows1251(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r < 0x80:
			out = append(out, byte(r))
		case r >= 'А' && r <= 'я':
			out = append(out, byte(0xC0+int(r-'А')))
		case r == 'Ё':
			out = append(out, 0xA8)
		case r == 'ё':
			out = append(out, 0xB8)
		case r == '№':
			out = append(out, 0xB9)
		case r == '«':
			out = append(out, 0xAB)
		case r == '»':
			out = append(out, 0xBB)
		default:
			out = append(out, '?')
		}
	}
	return out
}
