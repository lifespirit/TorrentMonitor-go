package core

import (
	"context"
	"testing"
)

func TestBuildTrackerURLTapochek(t *testing.T) {
	want := "https://tapochek.net/viewtopic.php?t=287865"
	if got := BuildTrackerURL("tapochek.net", "287865"); got != want {
		t.Fatalf("BuildTrackerURL = %q, want %q", got, want)
	}
}

func TestAddTorrentUsesTopicIDWhenURLHasOtherQueryParameters(t *testing.T) {
	for _, raw := range []string{
		"https://tapochek.net/viewtopic.php?start=40&t=287865",
		"https://rutracker.org/forum/viewtopic.php?start=40&t=287865",
	} {
		tracker, torrentID, err := parseTrackerURL(raw)
		if err != nil {
			t.Fatalf("parseTrackerURL(%q): %v", raw, err)
		}
		if torrentID != "287865" {
			t.Errorf("parseTrackerURL(%q) id = %q, want 287865", raw, torrentID)
		}
		if tracker != "tapochek.net" && tracker != "rutracker.org" {
			t.Errorf("unexpected tracker %q", tracker)
		}
	}
}

func TestAddTorrentTapochekKeepsWorkingTopicLink(t *testing.T) {
	repo := &credentialRepo{}
	svc := NewService(repo, nil)
	item, err := svc.AddTorrent(context.Background(), AddTorrentRequest{
		Kind: TorrentKindTheme,
		URL:  "https://tapochek.net/viewtopic.php?t=287865",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://tapochek.net/viewtopic.php?t=287865"; item.URL != want {
		t.Fatalf("item.URL = %q, want %q", item.URL, want)
	}
}
