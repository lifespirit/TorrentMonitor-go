package browserbroker

import "testing"

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
