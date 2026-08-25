package torrentclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestQBittorrentLoginAccepts204WithQBTSIDCookie(t *testing.T) {
	var sawCookieOnAdd bool
	var sawCookieOnInfo bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("username") != "admin" || r.Form.Get("password") != "secret" {
				http.Error(w, "bad credentials", http.StatusForbidden)
				return
			}
			if r.Header.Get("Referer") == "" || r.Header.Get("Origin") == "" {
				http.Error(w, "missing referer/origin", http.StatusForbidden)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "QBT_SID_19149", Value: "abc123", Path: "/", Expires: time.Now().Add(time.Hour)})
			w.WriteHeader(http.StatusNoContent)
		case "/api/v2/torrents/add":
			sawCookieOnAdd = strings.Contains(r.Header.Get("Cookie"), "QBT_SID_19149=abc123")
			if !sawCookieOnAdd {
				http.Error(w, "missing SID", http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusOK)
		case "/api/v2/torrents/info":
			sawCookieOnInfo = strings.Contains(r.Header.Get("Cookie"), "QBT_SID_19149=abc123")
			if !sawCookieOnInfo {
				http.Error(w, "missing SID", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"hash":"deadbeef"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, err := NewQBittorrent(Config{
		Kind:     "qBittorrent",
		Address:  srv.URL,
		Login:    "admin",
		Password: "secret",
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := client.Add(context.Background(), AddRequest{FileURL: "https://example.test/file.torrent"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Hash != "deadbeef" {
		t.Fatalf("unexpected hash %q", result.Hash)
	}
	if !sawCookieOnAdd || !sawCookieOnInfo {
		t.Fatalf("SID cookie was not sent on subsequent requests: add=%v info=%v", sawCookieOnAdd, sawCookieOnInfo)
	}
	sess := client.Session()
	if !strings.Contains(sess.Cookie, "QBT_SID_19149=abc123") {
		t.Fatalf("unexpected persisted session cookie %q", sess.Cookie)
	}
	if sess.Expires == nil || time.Until(*sess.Expires) <= 0 {
		t.Fatalf("expected future session expiration, got %#v", sess.Expires)
	}
}

func TestQBittorrentReusesPersistedCookieWithoutLogin(t *testing.T) {
	var loginCalls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			loginCalls.Add(1)
			http.Error(w, "login should not be called", http.StatusInternalServerError)
		case "/api/v2/torrents/add":
			if !strings.Contains(r.Header.Get("Cookie"), "QBT_SID_19149=cached") {
				http.Error(w, "missing cached SID", http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusOK)
		case "/api/v2/torrents/info":
			if !strings.Contains(r.Header.Get("Cookie"), "QBT_SID_19149=cached") {
				http.Error(w, "missing cached SID", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"hash":"feedface"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	exp := time.Now().Add(time.Hour)
	client, err := NewQBittorrent(Config{
		Kind:           "qBittorrent",
		Address:        srv.URL,
		Login:          "admin",
		Password:       "secret",
		SessionCookie:  "QBT_SID_19149=cached",
		SessionExpires: &exp,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Add(context.Background(), AddRequest{FileURL: "https://example.test/file.torrent"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Hash != "feedface" {
		t.Fatalf("unexpected hash %q", result.Hash)
	}
	if loginCalls.Load() != 0 {
		t.Fatalf("login was called %d times", loginCalls.Load())
	}
}

func TestQBittorrentLoginRejects200FailsWithoutCookie(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/auth/login" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Fails."))
	}))
	defer srv.Close()

	client, err := NewQBittorrent(Config{
		Kind:     "qBittorrent",
		Address:  srv.URL,
		Login:    "admin",
		Password: "wrong",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.Add(context.Background(), AddRequest{FileURL: "https://example.test/file.torrent"})
	if err == nil || !strings.Contains(err.Error(), "invalid username or password") {
		t.Fatalf("expected invalid credentials error, got %v", err)
	}
}

func TestQBittorrentTreatsExactDuplicateAsSuccessfulAdd(t *testing.T) {
	const expectedHash = "13fdbc500353cc14e9c170e2f755993eeaa9fb8d"
	torrentData := []byte("d4:infod4:name4:test6:lengthi1eee")
	var infoCalls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/add":
			http.Error(w, "Conflict", http.StatusConflict)
		case "/api/v2/torrents/info":
			infoCalls.Add(1)
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("hashes") != expectedHash {
				t.Fatalf("unexpected hash lookup %q", r.Form.Get("hashes"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"hash":"` + expectedHash + `"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, err := NewQBittorrent(Config{Kind: "qBittorrent", Address: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Add(context.Background(), AddRequest{FileData: torrentData, FileName: "test.torrent"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Hash != expectedHash {
		t.Fatalf("unexpected hash %q", result.Hash)
	}
	if infoCalls.Load() != 1 {
		t.Fatalf("expected one exact hash lookup, got %d", infoCalls.Load())
	}
}

func TestQBittorrentKeepsConflictWhenExactTorrentIsMissing(t *testing.T) {
	torrentData := []byte("d4:infod4:name4:test6:lengthi1eee")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/add":
			http.Error(w, "Conflict", http.StatusConflict)
		case "/api/v2/torrents/info":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, err := NewQBittorrent(Config{Kind: "qBittorrent", Address: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Add(context.Background(), AddRequest{FileData: torrentData, FileName: "test.torrent"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 409") {
		t.Fatalf("expected original conflict, got %v", err)
	}
}

func TestQBittorrentHonorsExplicitSavePath(t *testing.T) {
	torrentData := []byte("d4:infod4:name4:test6:lengthi1eee")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/torrents/add" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if got := r.FormValue("savepath"); got != "/downloads/topic" {
			t.Fatalf("savepath = %q, want /downloads/topic", got)
		}
		if got := r.FormValue("autoTMM"); got != "false" {
			t.Fatalf("autoTMM = %q, want false", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client, err := NewQBittorrent(Config{Kind: "qBittorrent", Address: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Add(context.Background(), AddRequest{
		FileData: torrentData,
		FileName: "test.torrent",
		SavePath: "/downloads/topic",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestQBittorrentCheckConnectionDoesNotAddTorrent(t *testing.T) {
	var addCalls atomic.Int32
	var sawCookieOnVersion bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "QBT_SID_19149", Value: "check123", Path: "/", Expires: time.Now().Add(time.Hour)})
			w.WriteHeader(http.StatusNoContent)
		case "/api/v2/app/version":
			sawCookieOnVersion = strings.Contains(r.Header.Get("Cookie"), "QBT_SID_19149=check123")
			if !sawCookieOnVersion {
				http.Error(w, "missing SID", http.StatusForbidden)
				return
			}
			_, _ = w.Write([]byte("v5.1.2"))
		case "/api/v2/torrents/add":
			addCalls.Add(1)
			http.Error(w, "add should not be called", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, err := NewQBittorrent(Config{Kind: "qBittorrent", Address: srv.URL, Login: "admin", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.CheckConnection(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != "v5.1.2" {
		t.Fatalf("unexpected version %q", result.Version)
	}
	if !sawCookieOnVersion {
		t.Fatal("expected SID cookie on app/version request")
	}
	if addCalls.Load() != 0 {
		t.Fatalf("add was called %d times", addCalls.Load())
	}
}

func TestQBittorrentCheckConnectionRetriesAfterUnauthorizedCachedCookie(t *testing.T) {
	var loginCalls atomic.Int32
	var sawFreshCookieOnVersion bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			loginCalls.Add(1)
			if strings.Contains(r.Header.Get("Cookie"), "QBT_SID_19149=stale") {
				http.Error(w, "stale cookie was not cleared", http.StatusUnauthorized)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "QBT_SID_19149", Value: "fresh", Path: "/", Expires: time.Now().Add(time.Hour)})
			w.WriteHeader(http.StatusNoContent)
		case "/api/v2/app/version":
			cookie := r.Header.Get("Cookie")
			if strings.Contains(cookie, "QBT_SID_19149=stale") {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			sawFreshCookieOnVersion = strings.Contains(cookie, "QBT_SID_19149=fresh")
			if !sawFreshCookieOnVersion {
				http.Error(w, "missing fresh SID", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte("v5.1.2"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	exp := time.Now().Add(time.Hour)
	client, err := NewQBittorrent(Config{
		Kind:           "qBittorrent",
		Address:        srv.URL,
		Login:          "admin",
		Password:       "secret",
		SessionCookie:  "QBT_SID_19149=stale",
		SessionExpires: &exp,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.CheckConnection(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != "v5.1.2" {
		t.Fatalf("unexpected version %q", result.Version)
	}
	if loginCalls.Load() != 1 {
		t.Fatalf("expected one re-login, got %d", loginCalls.Load())
	}
	if !sawFreshCookieOnVersion {
		t.Fatal("expected fresh SID on app/version request")
	}
}
