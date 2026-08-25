package store

import (
	"context"
	"path/filepath"
	"testing"

	"torrentmonitor-go/internal/core"
)

func TestDiskStorePersistsMutations(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "data.json")

	s, err := NewDiskStore(path)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	created, err := s.CreateTorrent(ctx, core.TorrentItem{
		Tracker:    "rutracker.org",
		Type:       core.TrackerTypeForum,
		Name:       "Persisted release",
		TorrentID:  "123456",
		URL:        core.BuildTrackerURL("rutracker.org", "123456"),
		AutoUpdate: true,
	})
	if err != nil {
		t.Fatalf("CreateTorrent: %v", err)
	}

	reopened, err := NewDiskStore(path)
	if err != nil {
		t.Fatalf("reopen NewDiskStore: %v", err)
	}
	got, err := reopened.GetTorrent(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetTorrent: %v", err)
	}
	if got.Name != "Persisted release" || got.TorrentID != "123456" {
		t.Fatalf("unexpected persisted item: %+v", got)
	}
}
