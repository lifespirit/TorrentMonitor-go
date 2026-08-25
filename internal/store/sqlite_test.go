//go:build cgo

package store

import (
	"context"
	"testing"

	"torrentmonitor-go/internal/core"
)

func TestSQLiteStorePersistsTorrents(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/tm.sqlite3"

	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	created, err := s.CreateTorrent(ctx, core.TorrentItem{
		Tracker:    "rutracker.org",
		Type:       core.TrackerTypeForum,
		Name:       "Persisted",
		TorrentID:  "777777",
		URL:        core.BuildTrackerURL("rutracker.org", "777777"),
		Path:       "/downloads",
		AutoUpdate: true,
	})
	if err != nil {
		t.Fatalf("CreateTorrent: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen NewSQLiteStore: %v", err)
	}
	defer s2.Close()
	got, err := s2.GetTorrent(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetTorrent after reopen: %v", err)
	}
	if got.Name != "Persisted" || got.TorrentID != "777777" {
		t.Fatalf("unexpected persisted torrent: %+v", got)
	}

	b, err := s2.Bootstrap(ctx, true)
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if b.Version.Database != "sqlite" {
		t.Fatalf("database marker = %q", b.Version.Database)
	}
}

func TestSQLiteStoreListsCredentialAccessMode(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/tm.sqlite3"

	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer s.Close()

	creds, err := s.ListCredentials(ctx)
	if err != nil {
		t.Fatalf("ListCredentials: %v", err)
	}
	var rutID int64
	for _, c := range creds {
		if c.Tracker == "rutracker.org" {
			rutID = c.ID
			break
		}
	}
	if rutID == 0 {
		t.Fatalf("default rutracker credential not found")
	}
	mode := "chromium"
	if _, err := s.UpdateCredential(ctx, rutID, core.UpdateCredentialRequest{AccessMode: &mode}); err != nil {
		t.Fatalf("UpdateCredential: %v", err)
	}
	creds, err = s.ListCredentials(ctx)
	if err != nil {
		t.Fatalf("ListCredentials after update: %v", err)
	}
	for _, c := range creds {
		if c.ID == rutID {
			if c.AccessMode != core.AccessModeChromium {
				t.Fatalf("AccessMode = %q, want chromium; credential = %+v", c.AccessMode, c)
			}
			if c.Type != core.TrackerTypeForum {
				t.Fatalf("Type = %q, want forum; credential = %+v", c.Type, c)
			}
			return
		}
	}
	t.Fatalf("updated credential not found")
}
