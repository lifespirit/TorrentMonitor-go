package flaresolverr

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClientReusesSessionAndDownloadsWithSolvedIdentity(t *testing.T) {
	var (
		mu       sync.Mutex
		commands []string
	)
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/v1", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		cmd, _ := req["cmd"].(string)
		mu.Lock()
		commands = append(commands, cmd)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch cmd {
		case "sessions.create":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":  "ok",
				"message": "Session created successfully.",
				"session": req["session"],
			})
		case "request.get":
			target, _ := req["url"].(string)
			u, err := url.Parse(target)
			if err != nil {
				t.Fatalf("parse target: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "ok",
				"solution": map[string]any{
					"url":       target,
					"status":    200,
					"response":  "<html>solved</html>",
					"userAgent": "flaresolverr-test-agent",
					"cookies": []map[string]any{{
						"name":   "cf_clearance",
						"value":  "solved",
						"domain": u.Hostname(),
						"path":   "/",
					}},
				},
			})
		case "sessions.destroy":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		default:
			http.Error(w, "unexpected command", http.StatusBadRequest)
		}
	})
	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) {
		if got := r.UserAgent(); got != "flaresolverr-test-agent" {
			t.Fatalf("User-Agent = %q", got)
		}
		cookie, err := r.Cookie("cf_clearance")
		if err != nil || cookie.Value != "solved" {
			t.Fatalf("cf_clearance cookie = %v, err=%v", cookie, err)
		}
		if got := r.Header.Get("Referer"); got != srv.URL+"/topic" {
			t.Fatalf("Referer = %q", got)
		}
		_, _ = w.Write([]byte("d8:announce13:http://tracker4:infod4:name4:testee"))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	client := New(Config{URL: srv.URL + "/v1", Timeout: 5 * time.Second}, nil)
	page, err := client.FetchPage(context.Background(), "tracker.test", srv.URL+"/topic")
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	if string(page) != "<html>solved</html>" {
		t.Fatalf("page = %q", page)
	}

	data, err := client.Download(
		context.Background(),
		"tracker.test",
		srv.URL+"/download",
		map[string]string{"Referer": srv.URL + "/topic"},
		"",
		5*time.Second,
		"",
		"",
	)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !strings.HasPrefix(string(data), "d8:announce") {
		t.Fatalf("unexpected torrent data: %q", data)
	}

	client.Close()
	mu.Lock()
	defer mu.Unlock()
	if got := strings.Join(commands, ","); got != "sessions.create,request.get,sessions.destroy" {
		t.Fatalf("commands = %q", got)
	}
}

func TestClientPostsFormNativelyWithoutReencodingAndSyncsCookies(t *testing.T) {
	var (
		mu       sync.Mutex
		commands []string
		getCount int
	)
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/v1", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		cmd, _ := req["cmd"].(string)
		mu.Lock()
		commands = append(commands, cmd)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch cmd {
		case "sessions.create":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "session": req["session"]})
		case "request.get":
			getCount++
			target, _ := req["url"].(string)
			cookies := []map[string]any{{
				"name": "cf_clearance", "value": "solved", "domain": "127.0.0.1", "path": "/",
			}}
			if getCount > 1 {
				items, _ := req["cookies"].([]any)
				foundAuth := false
				for _, raw := range items {
					item, _ := raw.(map[string]any)
					if item["name"] == "bb_session" && item["value"] == "authorized" {
						foundAuth = true
						cookies = append(cookies, map[string]any{"name": "bb_session", "value": "authorized", "domain": "127.0.0.1", "path": "/"})
					}
				}
				if !foundAuth {
					t.Fatalf("auth cookie was not injected into FlareSolverr GET: %#v", req["cookies"])
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "ok",
				"solution": map[string]any{
					"url": target, "status": 200, "response": "<html>ok</html>",
					"userAgent": "flaresolverr-test-agent", "cookies": cookies,
				},
			})
		case "request.post":
			t.Fatalf("form POST must not use FlareSolverr request.post")
		case "sessions.destroy":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		default:
			http.Error(w, "unexpected command", http.StatusBadRequest)
		}
	})
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.UserAgent() != "flaresolverr-test-agent" {
			t.Fatalf("User-Agent = %q", r.UserAgent())
		}
		if r.Header.Get("Origin") != srv.URL {
			t.Fatalf("Origin = %q", r.Header.Get("Origin"))
		}
		cookie, err := r.Cookie("cf_clearance")
		if err != nil || cookie.Value != "solved" {
			t.Fatalf("cf_clearance cookie = %v, err=%v", cookie, err)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		want := "login=%C2%F5%EE%E4&login_password=pass&login_username=user"
		if string(body) != want {
			t.Fatalf("POST body = %q, want %q", body, want)
		}
		http.SetCookie(w, &http.Cookie{Name: "bb_session", Value: "authorized", Path: "/"})
		_, _ = w.Write([]byte("login ok"))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	client := New(Config{URL: srv.URL + "/v1", Timeout: 5 * time.Second}, nil)
	if _, err := client.FetchPage(context.Background(), "rutracker.org", srv.URL+"/index"); err != nil {
		t.Fatalf("initial GET: %v", err)
	}
	postData := "login=%C2%F5%EE%E4&login_password=pass&login_username=user"
	if _, err := client.Request(
		context.Background(),
		"rutracker.org",
		http.MethodPost,
		srv.URL+"/login",
		postData,
		map[string]string{"Origin": srv.URL},
		nil,
		5*time.Second,
		"",
		"",
	); err != nil {
		t.Fatalf("native POST: %v", err)
	}
	if _, err := client.FetchPage(context.Background(), "rutracker.org", srv.URL+"/index"); err != nil {
		t.Fatalf("authenticated GET: %v", err)
	}

	client.Close()
	mu.Lock()
	defer mu.Unlock()
	if got := strings.Join(commands, ","); got != "sessions.create,request.get,request.get,sessions.destroy" {
		t.Fatalf("commands = %q", got)
	}
}
