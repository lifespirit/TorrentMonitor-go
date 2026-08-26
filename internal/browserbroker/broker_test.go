package browserbroker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCloseBrowserTarget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/json/close/target-1" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if err := closeBrowserTarget(context.Background(), server.URL, "target-1"); err != nil {
		t.Fatalf("closeBrowserTarget: %v", err)
	}
}

type discardLogger struct{}

func (discardLogger) Info(string, ...any)  {}
func (discardLogger) Warn(string, ...any)  {}
func (discardLogger) Error(string, ...any) {}

func TestCloseTrackerRemovesAndClosesSession(t *testing.T) {
	session := &Session{tracker: "rutracker.org", status: "running", closed: make(chan struct{})}
	broker := &Broker{
		sessions:  map[string]*Session{"session-1": session},
		byTracker: map[string]string{"rutracker.org": "session-1"},
	}

	if err := broker.CloseTracker(" RUTRACKER.ORG "); err != nil {
		t.Fatalf("CloseTracker: %v", err)
	}
	if got := len(broker.sessions); got != 0 {
		t.Fatalf("sessions left = %d, want 0", got)
	}
	if got := len(broker.byTracker); got != 0 {
		t.Fatalf("tracker mappings left = %d, want 0", got)
	}
	if !session.isClosed() {
		t.Fatal("session was not closed")
	}
}

func TestDetachRemovesSessionWithoutClosingPage(t *testing.T) {
	session := &Session{tracker: "torrentmonitor-extensions", status: "running", closed: make(chan struct{})}
	broker := &Broker{
		sessions:  map[string]*Session{"session-1": session},
		byTracker: map[string]string{"torrentmonitor-extensions": "session-1"},
	}

	if err := broker.Detach("session-1"); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	if len(broker.sessions) != 0 || len(broker.byTracker) != 0 {
		t.Fatal("detached session remains registered")
	}
	if got := session.Status().Status; got != "detached" {
		t.Fatalf("session status = %q, want detached", got)
	}
}

func TestRetainedInteractionSessionExpires(t *testing.T) {
	session := &Session{tracker: "rutracker.org", status: "running", closed: make(chan struct{})}
	broker := &Broker{
		sessions:  map[string]*Session{"session-1": session},
		byTracker: map[string]string{"rutracker.org": "session-1"},
		logger:     discardLogger{},
	}

	broker.retainInteraction("session-1", 10*time.Millisecond)
	select {
	case <-session.closed:
	case <-time.After(time.Second):
		t.Fatal("interactive session was not closed after TTL")
	}
	if got := len(broker.sessions); got != 0 {
		t.Fatalf("sessions left = %d, want 0", got)
	}
}

func TestNormalizeDevToolsBase(t *testing.T) {
	cases := []struct {
		in   string
		want string
		port int
	}{
		{"9222", "http://127.0.0.1:9222", 9222},
		{":9223", "http://127.0.0.1:9223", 9223},
		{"127.0.0.1:9224", "http://127.0.0.1:9224", 9224},
		{"http://127.0.0.1:9225/", "http://127.0.0.1:9225", 9225},
		{"ws://127.0.0.1:9226/devtools/browser/abc", "http://127.0.0.1:9226", 9226},
	}
	for _, tc := range cases {
		got, port, err := normalizeDevToolsBase(tc.in)
		if err != nil {
			t.Fatalf("normalizeDevToolsBase(%q): %v", tc.in, err)
		}
		if got != tc.want || port != tc.port {
			t.Fatalf("normalizeDevToolsBase(%q) = (%q,%d), want (%q,%d)", tc.in, got, port, tc.want, tc.port)
		}
	}
}
