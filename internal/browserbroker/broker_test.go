package browserbroker

import "testing"

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
