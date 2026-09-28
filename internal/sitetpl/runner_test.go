package sitetpl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRunnerHTTPTemplateFindsUpdateAndDownloadsTorrent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("login method = %s", r.Method)
		}
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "ok", Path: "/"})
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/topic", func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("sid"); err != nil {
			t.Fatalf("missing login cookie on topic request: %v", err)
		}
		_, _ = w.Write([]byte(`<html><head><title>Release :: Test</title></head><body><span>25-Июн-26 14:30</span></body></html>`))
	})
	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("d8:announce13:http://tracker4:infod4:name4:testee"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	reg := &Registry{}
	reg.Register(Template{
		Version: 1,
		Site:    "example.test",
		Kind:    "forum",
		Mode:    ModeHTTP,
		Auth: Auth{
			LoggedOut: MatchRules{ContainsAll: []string{"login_username", "login_password"}},
			Login:     &HTTPRequest{Method: "POST", URL: srv.URL + "/login", Form: map[string]string{"u": "{{ credentials.login }}", "p": "{{ credentials.password }}"}},
		},
		Item: ItemFlow{
			Page: HTTPRequest{Method: "GET", URL: srv.URL + "/topic?id={{ item.torrent_id }}"},
			Extract: map[string]Extract{
				"title":      {Selector: "title", Cleanup: []CleanupRule{{TrimSuffix: " :: Test"}}},
				"updated_at": {Selector: "body", Regex: `([0-9]{2}-[А-Яа-я]{3}-[0-9]{2} [0-9]{2}:[0-9]{2})`, Layout: "02-Jan-06 15:04", Locale: "ru"},
			},
		},
		Download: DownloadFlow{Request: HTTPRequest{Method: "GET", URL: srv.URL + "/download?id={{ item.torrent_id }}"}, Validate: Validate{BencodeTorrent: true}},
	})

	old := time.Date(2026, 6, 24, 10, 0, 0, 0, time.Local)
	runner := NewRunner(WithRegistry(reg))
	result, err := runner.Check(context.Background(), CheckRequest{
		Item:       Item{Tracker: "example.test", TorrentID: "42", Name: "Old", UpdatedAt: &old},
		Credential: Credential{Login: "user", Password: "pass"},
		Settings:   Settings{UserAgent: "tm-test", Timeout: 5 * time.Second},
	})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !result.Updated {
		t.Fatalf("expected update")
	}
	if result.Title != "Release" {
		t.Fatalf("title = %q", result.Title)
	}
	if len(result.TorrentData) == 0 {
		t.Fatalf("expected torrent data")
	}
}

func TestRunnerExtractsDownloadURLFromPage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/topic", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><head><title>NNM Release :: NNM-Club</title></head><body>
			<a href="download.php?id=777&uk=abc">Скачать</a>
			<span>Зарегистрирован:</span> 13 Дек 2025 10:11:02
		</body></html>`))
	})
	mux.HandleFunc("/download.php", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id") != "777" {
			t.Fatalf("download id = %q", r.URL.Query().Get("id"))
		}
		if ref := r.Header.Get("Referer"); !strings.Contains(ref, "/topic?t=123") {
			t.Fatalf("referer = %q", ref)
		}
		_, _ = w.Write([]byte("d8:announce13:http://tracker4:infod4:name4:testee"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	reg := &Registry{}
	reg.Register(Template{
		Version: 1,
		Site:    "nnm.test",
		Kind:    "forum_topic",
		Mode:    ModeHTTP,
		Item: ItemFlow{
			Page: HTTPRequest{Method: "GET", URL: srv.URL + "/topic?t={{ item.torrent_id }}", Success: MatchRules{Contains: "download.php?id="}},
			Extract: map[string]Extract{
				"title":      {Selector: "title", Cleanup: []CleanupRule{{TrimSuffix: " :: NNM-Club"}}},
				"updated_at": {Selector: "body", Regex: `(?is)Зарегистрирован:\s*(?:&nbsp;|\s|<[^>]*>)*([0-9]{1,2}\s+[А-Яа-я]{3}\s+[0-9]{4}\s+[0-9]{2}:[0-9]{2}:[0-9]{2})`, Layout: "2 Jan 2006 15:04:05", Locale: "ru"},
			},
		},
		Download: DownloadFlow{
			URLFromPage: Extract{Selector: "body", Regex: `href=["']((?:https?://[^"']+)?(?:/forum/)?download\.php\?id=[0-9][^"']*)["']`},
			Before:      DownloadBefore{Headers: map[string]string{"Referer": srv.URL + "/topic?t={{ item.torrent_id }}"}},
			Validate:    Validate{BencodeTorrent: true},
		},
	})
	old := time.Date(2025, 12, 12, 10, 0, 0, 0, time.Local)
	result, err := NewRunner(WithRegistry(reg)).Check(context.Background(), CheckRequest{
		Item:     Item{Tracker: "nnm.test", TorrentID: "123", Name: "Old", UpdatedAt: &old},
		Settings: Settings{UserAgent: "tm-test", Timeout: 5 * time.Second},
	})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !result.Updated || result.Title != "NNM Release" || len(result.TorrentData) == 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestRenderVariables(t *testing.T) {
	got := render("/topic/{{ item.torrent_id }}?u={{ credentials.login }}", map[string]string{"item.torrent_id": "123", "credentials.login": "life"})
	if got != "/topic/123?u=life" {
		t.Fatalf("render = %q", got)
	}
}

func TestRunnerUsesStoredCookieBeforeLogin(t *testing.T) {
	loginCalled := false
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		loginCalled = true
		http.Error(w, "login should not be called", http.StatusForbidden)
	})
	mux.HandleFunc("/index", func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("sid"); err != nil {
			http.Error(w, "missing sid", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`profile.php?mode=viewprofile`))
	})
	mux.HandleFunc("/topic", func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("sid"); err != nil {
			t.Fatalf("missing stored cookie on topic request: %v", err)
		}
		_, _ = w.Write([]byte(`<html><head><title>Release :: Test</title></head><body><span>25-Июн-26 14:30</span></body></html>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	reg := &Registry{}
	reg.Register(Template{
		Version: 1,
		Site:    "cookie.test",
		Kind:    "forum",
		Mode:    ModeHTTP,
		HTTP:    HTTPConfig{BaseURL: srv.URL},
		Auth: Auth{
			Check: &HTTPRequest{Method: "GET", URL: srv.URL + "/index", Success: MatchRules{Contains: "viewprofile"}},
			Login: &HTTPRequest{Method: "POST", URL: srv.URL + "/login", Form: map[string]string{"u": "{{ credentials.login }}"}},
		},
		Item: ItemFlow{
			Page: HTTPRequest{Method: "GET", URL: srv.URL + "/topic?id={{ item.torrent_id }}"},
			Extract: map[string]Extract{
				"title":      {Selector: "title", Cleanup: []CleanupRule{{TrimSuffix: " :: Test"}}},
				"updated_at": {Selector: "body", Regex: `([0-9]{2}-[А-Яа-я]{3}-[0-9]{2} [0-9]{2}:[0-9]{2})`, Layout: "02-Jan-06 15:04", Locale: "ru"},
			},
		},
	})

	old := time.Date(2026, 6, 24, 10, 0, 0, 0, time.Local)
	result, err := NewRunner(WithRegistry(reg)).Check(context.Background(), CheckRequest{
		Item:       Item{Tracker: "cookie.test", TorrentID: "42", Name: "Old", UpdatedAt: &old},
		Credential: Credential{Login: "user", Cookie: "sid=ok"},
		Settings:   Settings{UserAgent: "tm-test", Timeout: 5 * time.Second},
	})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if loginCalled {
		t.Fatalf("stored cookie was valid, login should have been skipped")
	}
	if result.SessionCookie != "sid=ok" {
		t.Fatalf("session cookie = %q", result.SessionCookie)
	}
}

func TestEncodeFormWindows1251(t *testing.T) {
	got := encodeForm(map[string]string{"login": "Вход"}, nil, "windows-1251")
	if got != "login=%C2%F5%EE%E4" {
		t.Fatalf("encoded form = %q", got)
	}
}

func TestRunnerNativeModeDoesNotFallbackToFlareSolverrForForbiddenPage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/topic", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	reg := &Registry{}
	reg.Register(Template{
		Version: 1,
		Site:    "native-no-fallback.test",
		Kind:    "forum",
		Mode:    ModeHTTP,
		Item: ItemFlow{
			Page: HTTPRequest{Method: "GET", URL: srv.URL + "/topic?id={{ item.torrent_id }}"},
			Extract: map[string]Extract{
				"title":      {Selector: "title", Cleanup: []CleanupRule{{TrimSuffix: " :: Test"}}},
				"updated_at": {Selector: "body", Regex: `([0-9]{2}-[А-Яа-я]{3}-[0-9]{2} [0-9]{2}:[0-9]{2})`, Layout: "02-Jan-06 15:04", Locale: "ru"},
			},
		},
	})

	_, err := NewRunner(WithRegistry(reg)).Check(context.Background(), CheckRequest{
		Item:     Item{Tracker: "native-no-fallback.test", TorrentID: "42", Name: "Old"},
		Settings: Settings{UserAgent: "tm-test", Timeout: 5 * time.Second},
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("expected native HTTP 403 without browser fallback, got %v", err)
	}
}

type fakeFlareSolverrFetcher struct {
	calls     []string
	html      string
	guestHTML string
	loggedIn  bool
}

func (f *fakeFlareSolverrFetcher) FetchPage(ctx context.Context, tracker string, rawURL string) ([]byte, error) {
	f.calls = append(f.calls, "FETCH "+tracker+" "+rawURL)
	return []byte(f.html), nil
}

func (f *fakeFlareSolverrFetcher) Request(ctx context.Context, tracker, method, rawURL, postData string, cookies map[string]string, timeout time.Duration, proxyType, proxyAddress string) ([]byte, error) {
	f.calls = append(f.calls, method+" "+tracker+" "+rawURL+" "+postData)
	if method == http.MethodPost {
		f.loggedIn = true
		return []byte("login ok"), nil
	}
	if !f.loggedIn && f.guestHTML != "" && strings.Contains(rawURL, "/topic") {
		return []byte(f.guestHTML), nil
	}
	return []byte(f.html), nil
}

func TestRunnerLegacyChromiumModeUsesFlareSolverrAndPerformsLogin(t *testing.T) {
	loginCalled := false
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		loginCalled = true
		http.Error(w, "native login should not be called", http.StatusForbidden)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	reg := &Registry{}
	reg.Register(Template{
		Version: 1,
		Site:    "flaresolverr.test",
		Kind:    "forum",
		Mode:    ModeHTTP,
		Auth:    Auth{Login: &HTTPRequest{Method: "POST", URL: srv.URL + "/login", Form: map[string]string{"u": "{{ credentials.login }}", "p": "{{ credentials.password }}"}}},
		Item: ItemFlow{
			Page: HTTPRequest{Method: "GET", URL: "https://flaresolverr.test/topic?id={{ item.torrent_id }}"},
			Extract: map[string]Extract{
				"title":      {Selector: "title", Cleanup: []CleanupRule{{TrimSuffix: " :: Test"}}},
				"updated_at": {Selector: "body", Regex: `([0-9]{2}-[А-Яа-я]{3}-[0-9]{2} [0-9]{2}:[0-9]{2})`, Layout: "02-Jan-06 15:04", Locale: "ru"},
			},
		},
	})

	old := time.Date(2026, 6, 24, 10, 0, 0, 0, time.Local)
	solver := &fakeFlareSolverrFetcher{
		html:      `<html><head><title>FlareSolverr Release :: Test</title></head><body><span>25-Июн-26 14:30</span></body></html>`,
		guestHTML: `<html><body><input name="login_username"><input name="login_password"></body></html>`,
	}
	result, err := NewRunner(WithRegistry(reg)).Check(context.Background(), CheckRequest{
		Item:       Item{Tracker: "flaresolverr.test", TorrentID: "42", Name: "Old", UpdatedAt: &old},
		Credential: Credential{Login: "user", Password: "pass", AccessMode: "chromium"},
		Settings:   Settings{UserAgent: "tm-test", Timeout: 5 * time.Second},
		Browser:    solver,
	})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if loginCalled {
		t.Fatalf("native login endpoint was called in FlareSolverr mode")
	}
	if len(solver.calls) != 4 {
		t.Fatalf("FlareSolverr calls = %v, want GET topic + GET login preflight + POST login + GET topic retry", solver.calls)
	}
	if !strings.HasPrefix(solver.calls[0], "GET ") || !strings.Contains(solver.calls[0], "/topic") {
		t.Fatalf("unexpected first topic call: %v", solver.calls)
	}
	if !strings.HasPrefix(solver.calls[1], "GET ") || !strings.Contains(solver.calls[1], "/login") {
		t.Fatalf("unexpected POST preflight call: %v", solver.calls)
	}
	if !strings.HasPrefix(solver.calls[2], "POST ") || !strings.Contains(solver.calls[2], "u=user") || !strings.Contains(solver.calls[2], "p=pass") {
		t.Fatalf("unexpected login call: %v", solver.calls)
	}
	if !strings.HasPrefix(solver.calls[3], "GET ") || !strings.Contains(solver.calls[3], "/topic") {
		t.Fatalf("unexpected topic retry: %v", solver.calls)
	}
	if !result.Updated || result.Title != "FlareSolverr Release" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestFlareSolverrAuthorizedTopicSkipsAuthCheck(t *testing.T) {
	reg := &Registry{}
	reg.Register(Template{
		Version: 1,
		Site:    "flaresolverr-authorized.test",
		Kind:    "forum",
		Mode:    ModeHTTP,
		Auth: Auth{
			Check:     &HTTPRequest{Method: "GET", URL: "https://flaresolverr-authorized.test/index", Success: MatchRules{Contains: "logout"}},
			LoggedOut: MatchRules{ContainsAll: []string{"login_username", "login_password"}},
			Login:     &HTTPRequest{Method: "POST", URL: "https://flaresolverr-authorized.test/login"},
		},
		Item: ItemFlow{
			Page: HTTPRequest{Method: "GET", URL: "https://flaresolverr-authorized.test/topic"},
			Extract: map[string]Extract{
				"title": {Selector: "title"},
			},
		},
	})
	solver := &fakeFlareSolverrFetcher{html: `<html><head><title>Already logged in</title></head><body>ok</body></html>`, loggedIn: true}
	if _, err := NewRunner(WithRegistry(reg)).Check(context.Background(), CheckRequest{
		Item:       Item{Tracker: "flaresolverr-authorized.test", TorrentID: "1", Name: "Old"},
		Credential: Credential{Login: "user", Password: "pass", AccessMode: "flaresolverr"},
		Settings:   Settings{Timeout: 5 * time.Second},
		Browser:    solver,
	}); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(solver.calls) != 1 || !strings.Contains(solver.calls[0], "/topic") {
		t.Fatalf("calls = %v, want only topic GET", solver.calls)
	}
}

type fakeTurnstileFlareSolverr struct {
	calls      []string
	loggedIn   bool
	postData   string
	tabs       int
	captchaURL string
}

func (f *fakeTurnstileFlareSolverr) FetchPage(ctx context.Context, tracker string, rawURL string) ([]byte, error) {
	return f.Request(ctx, tracker, http.MethodGet, rawURL, "", nil, 0, "", "")
}

func (f *fakeTurnstileFlareSolverr) Request(ctx context.Context, tracker, method, rawURL, postData string, cookies map[string]string, timeout time.Duration, proxyType, proxyAddress string) ([]byte, error) {
	f.calls = append(f.calls, method+" "+rawURL)
	if method == http.MethodPost {
		f.postData = postData
		f.loggedIn = true
		return []byte("login ok"), nil
	}
	if strings.HasSuffix(rawURL, "/index") {
		if f.loggedIn {
			return []byte("login.php?logout=true"), nil
		}
		return []byte("guest"), nil
	}
	return []byte("page"), nil
}

func (f *fakeTurnstileFlareSolverr) SolveTurnstile(ctx context.Context, tracker, rawURL string, cookies map[string]string, tabsTillVerify int, timeout time.Duration, proxyType, proxyAddress string) (string, error) {
	f.calls = append(f.calls, "TURNSTILE "+rawURL)
	f.tabs = tabsTillVerify
	f.captchaURL = rawURL
	return "token-123", nil
}

func TestFlareSolverrPrepareSolvesTurnstileBeforeLogin(t *testing.T) {
	solver := &fakeTurnstileFlareSolverr{}
	tmpl := Template{
		Version: 1,
		Site:    "nnm.test",
		Auth: Auth{
			Check: &HTTPRequest{Method: "GET", URL: "https://nnm.test/index", Success: MatchRules{Contains: "logout=true"}},
			Login: &HTTPRequest{
				Method: "POST",
				URL:    "https://nnm.test/login",
				Form: map[string]string{
					"username": "{{ credentials.login }}",
					"password": "{{ credentials.password }}",
					"login":    "1",
				},
				Captcha: &CaptchaConfig{
					Type:           "turnstile",
					URL:            "https://nnm.test/login",
					TabsTillVerify: 36,
					FormField:      "cf-turnstile-response",
				},
			},
		},
	}
	access := &flareSolverrSiteAccess{
		tmpl:    tmpl,
		browser: solver,
		cred:    Credential{Login: "user", Password: "pass"},
	}
	vars := map[string]string{
		"credentials.login":    "user",
		"credentials.password": "pass",
	}
	if err := access.Prepare(context.Background(), vars, Settings{Timeout: 5 * time.Second}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if solver.tabs != 36 {
		t.Fatalf("tabs_till_verify = %d, want 36", solver.tabs)
	}
	if solver.captchaURL != "https://nnm.test/login" {
		t.Fatalf("captcha URL = %q", solver.captchaURL)
	}
	if !strings.Contains(solver.postData, "cf-turnstile-response=token-123") {
		t.Fatalf("POST does not contain Turnstile token: %q", solver.postData)
	}
	if !strings.Contains(solver.postData, "username=user") || !strings.Contains(solver.postData, "password=pass") {
		t.Fatalf("POST lost credentials: %q", solver.postData)
	}
	want := []string{
		"GET https://nnm.test/index",
		"TURNSTILE https://nnm.test/login",
		"POST https://nnm.test/login",
		"GET https://nnm.test/index",
	}
	if strings.Join(solver.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls = %v, want %v", solver.calls, want)
	}
}

func TestDefaultNNMClubTemplateConfiguresTurnstileLogin(t *testing.T) {
	tmpl := DefaultNNMClubTemplate()
	if tmpl.Auth.Login == nil || tmpl.Auth.Login.Captcha == nil {
		t.Fatal("NNM-Club login captcha config is missing")
	}
	if tmpl.Auth.Login.Captcha.Type != "turnstile" {
		t.Fatalf("captcha type = %q", tmpl.Auth.Login.Captcha.Type)
	}
	if tmpl.Auth.Login.Captcha.URL != "https://nnmclub.to/forum/login.php" {
		t.Fatalf("captcha URL = %q", tmpl.Auth.Login.Captcha.URL)
	}
	if tmpl.Auth.Login.Captcha.TabsTillVerify != 36 {
		t.Fatalf("tabs_till_verify = %d", tmpl.Auth.Login.Captcha.TabsTillVerify)
	}
	if tmpl.Auth.Login.Captcha.FormField != "cf-turnstile-response" {
		t.Fatalf("form field = %q", tmpl.Auth.Login.Captcha.FormField)
	}
	if tmpl.Auth.Login.Form["login"] != "1" {
		t.Fatalf("NNM-Club submit value was not normalized to ASCII")
	}
}
