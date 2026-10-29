package sitetpl

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

type rutrackerMockDownloader struct {
	method string
	cookie string
	header string
}

func (m *rutrackerMockDownloader) FetchPage(_ context.Context, tracker, rawURL string) ([]byte, error) {
	return []byte(`<html><head><title>Updated Release :: RuTracker.org</title></head>
<body><a href="dl.php?t=42">Скачать</a><span>25-Июн-26 14:30</span></body></html>`), nil
}

func (m *rutrackerMockDownloader) Download(_ context.Context, tracker, method, rawURL string, headers map[string]string, cookieHeader string, timeout time.Duration, proxyType, proxyAddress string) ([]byte, error) {
	m.method = method
	m.cookie = cookieHeader
	m.header = headers["Referer"]
	return []byte("d8:announce14:http://tracker4:infod4:name4:testee"), nil
}

func TestRuTrackerDownloadsWithPostWhenOnlyTitleChanges(t *testing.T) {
	tmpl := DefaultRutrackerTemplate()
	if tmpl.Download.Request.Method != http.MethodPost {
		t.Fatalf("RuTracker download method = %q, want POST", tmpl.Download.Request.Method)
	}
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 6, 25, 14, 30, 0, 0, loc)
	browser := &rutrackerMockDownloader{}
	result, err := NewRunner().Check(context.Background(), CheckRequest{
		Item: Item{
			Tracker:     "rutracker.org",
			TorrentID:   "42",
			Name:        "Previous Release",
			UpdatedAt:   &old,
			UpdateTitle: true,
		},
		Credential: Credential{AccessMode: "flaresolverr"},
		Browser:    browser,
		Settings:   Settings{Timeout: 5 * time.Second},
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !result.Updated || len(result.TorrentData) == 0 {
		t.Fatalf("title change should trigger a torrent download: %+v", result)
	}
	if browser.method != http.MethodPost {
		t.Fatalf("download method = %q, want POST", browser.method)
	}
	if !strings.Contains(browser.cookie, "bb_dl=42") {
		t.Fatalf("download cookies missing bb_dl: %q", browser.cookie)
	}
	if browser.header != "https://rutracker.org/forum/viewtopic.php?t=42" {
		t.Fatalf("download Referer = %q", browser.header)
	}
}
