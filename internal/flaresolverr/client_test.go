package flaresolverr

import (
	"context"
	"encoding/json"
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

func TestClientSolvesTurnstileAndReusesSession(t *testing.T) {
	var (
		mu       sync.Mutex
		commands []string
		sessions []string
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		cmd, _ := req["cmd"].(string)
		if session, _ := req["session"].(string); session != "" {
			mu.Lock()
			sessions = append(sessions, session)
			mu.Unlock()
		}
		mu.Lock()
		commands = append(commands, cmd)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch cmd {
		case "sessions.create":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		case "request.get":
			target, _ := req["url"].(string)
			response := map[string]any{
				"url":       target,
				"status":    200,
				"response":  "<html>ok</html>",
				"userAgent": "flaresolverr-test-agent",
				"cookies":   []map[string]any{},
			}
			if tabs, ok := req["tabs_till_verify"]; ok {
				if int(tabs.(float64)) != 34 {
					t.Fatalf("tabs_till_verify = %v", tabs)
				}
				response["turnstile_token"] = "token-123"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "solution": response})
		case "sessions.destroy":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		default:
			http.Error(w, "unexpected command", http.StatusBadRequest)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Config{URL: srv.URL + "/v1", Timeout: 5 * time.Second}, nil)
	token, err := client.SolveTurnstile(
		context.Background(),
		"nnmclub.to",
		"https://nnmclub.to/forum/login.php",
		nil,
		34,
		5*time.Second,
		"",
		"",
	)
	if err != nil {
		t.Fatalf("SolveTurnstile: %v", err)
	}
	if token != "token-123" {
		t.Fatalf("token = %q", token)
	}
	if _, err := client.Request(context.Background(), "nnmclub.to", http.MethodGet, "https://nnmclub.to/forum/index.php", "", nil, 5*time.Second, "", ""); err != nil {
		t.Fatalf("Request after Turnstile: %v", err)
	}
	client.Close()

	mu.Lock()
	defer mu.Unlock()
	if got := strings.Join(commands, ","); got != "sessions.create,request.get,request.get,sessions.destroy" {
		t.Fatalf("commands = %q", got)
	}
	if len(sessions) < 4 {
		t.Fatalf("sessions = %v", sessions)
	}
	createdSession := sessions[0]
	for _, session := range sessions[1:] {
		if session != createdSession {
			t.Fatalf("session changed: %v", sessions)
		}
	}
}

func TestClientRetriesChallengeTimeoutInSameSession(t *testing.T) {
	var (
		mu          sync.Mutex
		commands    []string
		sessions    []string
		requestGets int
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		cmd, _ := req["cmd"].(string)
		mu.Lock()
		commands = append(commands, cmd)
		if session, _ := req["session"].(string); session != "" {
			sessions = append(sessions, session)
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch cmd {
		case "sessions.create":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		case "request.get":
			mu.Lock()
			requestGets++
			attempt := requestGets
			mu.Unlock()
			if attempt == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"status":  "error",
					"message": "Error: Error solving the challenge. Timeout after 0.1 seconds.",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "ok",
				"solution": map[string]any{
					"url":       req["url"],
					"status":    200,
					"response":  "<html>recovered</html>",
					"userAgent": "test",
					"cookies":   []map[string]any{},
				},
			})
		case "sessions.destroy":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		default:
			http.Error(w, "unexpected command", http.StatusBadRequest)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Config{
		URL:               srv.URL + "/v1",
		Timeout:           2 * time.Second,
		ChallengeCooldown: time.Millisecond,
	}, nil)
	data, err := client.FetchPage(context.Background(), "rutracker.org", "https://rutracker.org/forum/viewtopic.php?t=1")
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	if string(data) != "<html>recovered</html>" {
		t.Fatalf("data = %q", data)
	}
	client.Close()

	mu.Lock()
	defer mu.Unlock()
	if requestGets != 2 {
		t.Fatalf("request.get attempts = %d, want 2", requestGets)
	}
	if len(sessions) < 4 {
		t.Fatalf("sessions = %v", sessions)
	}
	created := sessions[0]
	for _, session := range sessions[1:] {
		if session != created {
			t.Fatalf("session changed during recovery: %v", sessions)
		}
	}
}
