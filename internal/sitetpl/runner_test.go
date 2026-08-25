package sitetpl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
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
		Auth:    Auth{Login: &HTTPRequest{Method: "POST", URL: srv.URL + "/login", Form: map[string]string{"u": "{{ credentials.login }}", "p": "{{ credentials.password }}"}}},
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

func TestRunnerNativeModeDoesNotFallbackToBrowserForForbiddenPage(t *testing.T) {
	browser := t.TempDir() + "/fake-chromium"
	if err := os.WriteFile(browser, []byte("#!/bin/sh\nprintf '%s' '<html><head><title>Browser Release :: Test</title></head><body><span>25-Июн-26 14:30</span></body></html>'\n"), 0o755); err != nil {
		t.Fatalf("write fake browser: %v", err)
	}
	t.Setenv("TM_BROWSER_FALLBACK", "1")
	t.Setenv("TM_BROWSER_BINARY", browser)
	t.Setenv("TM_BROWSER_PROFILE", t.TempDir())

	mux := http.NewServeMux()
	mux.HandleFunc("/topic", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	reg := &Registry{}
	reg.Register(Template{
		Version: 1,
		Site:    "browser-fallback.test",
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
		Item:     Item{Tracker: "browser-fallback.test", TorrentID: "42", Name: "Old"},
		Settings: Settings{UserAgent: "tm-test", Timeout: 5 * time.Second},
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("expected native HTTP 403 without browser fallback, got %v", err)
	}
}

type fakeBrowserFetcher struct {
	calls []string
	html  string
}

func (f *fakeBrowserFetcher) FetchPage(ctx context.Context, tracker string, rawURL string) ([]byte, error) {
	f.calls = append(f.calls, tracker+" "+rawURL)
	return []byte(f.html), nil
}

func TestRunnerChromiumModeUsesBrowserFetcherAndSkipsNativeLogin(t *testing.T) {
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
		Site:    "chromium.test",
		Kind:    "forum",
		Mode:    ModeHTTP,
		Auth:    Auth{Login: &HTTPRequest{Method: "POST", URL: srv.URL + "/login", Form: map[string]string{"u": "{{ credentials.login }}"}}},
		Item: ItemFlow{
			Page: HTTPRequest{Method: "GET", URL: "https://chromium.test/topic?id={{ item.torrent_id }}"},
			Extract: map[string]Extract{
				"title":      {Selector: "title", Cleanup: []CleanupRule{{TrimSuffix: " :: Test"}}},
				"updated_at": {Selector: "body", Regex: `([0-9]{2}-[А-Яа-я]{3}-[0-9]{2} [0-9]{2}:[0-9]{2})`, Layout: "02-Jan-06 15:04", Locale: "ru"},
			},
		},
	})

	old := time.Date(2026, 6, 24, 10, 0, 0, 0, time.Local)
	browser := &fakeBrowserFetcher{html: `<html><head><title>Browser Release :: Test</title></head><body><span>25-Июн-26 14:30</span></body></html>`}
	result, err := NewRunner(WithRegistry(reg)).Check(context.Background(), CheckRequest{
		Item:       Item{Tracker: "chromium.test", TorrentID: "42", Name: "Old", UpdatedAt: &old},
		Credential: Credential{Login: "user", AccessMode: "chromium"},
		Settings:   Settings{UserAgent: "tm-test", Timeout: 5 * time.Second},
		Browser:    browser,
	})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if loginCalled {
		t.Fatalf("native login was called in chromium mode")
	}
	if len(browser.calls) != 1 {
		t.Fatalf("browser calls = %v, want one call", browser.calls)
	}
	if !result.Updated || result.Title != "Browser Release" {
		t.Fatalf("unexpected result: %+v", result)
	}
}
