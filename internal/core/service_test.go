package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"torrentmonitor-go/internal/sitetpl"
)

func TestPostUpdateEnvIncludesTorrentContext(t *testing.T) {
	updatedAt := time.Date(2026, 8, 25, 12, 3, 4, 0, time.UTC)
	env := postUpdateEnv([]string{"BASE=1"}, Settings{UseTorrent: true, TorrentClient: "qBittorrent"}, TorrentItem{
		ID: 42, Tracker: "rutracker.org", TorrentID: "6580159", Name: "old", URL: "https://rutracker.org/forum/viewtopic.php?t=6580159", Path: "/downloads/anime",
	}, sitetpl.CheckResult{Updated: true, Title: "new title", UpdatedAt: &updatedAt, TorrentData: []byte("d8:announce")}, "deadbeef", "/downloads/anime", "/tmp/item.torrent", true)

	got := map[string]string{}
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if ok {
			got[k] = v
		}
	}
	checks := map[string]string{
		"BASE":                      "1",
		"TM_TORRENT_DB_ID":          "42",
		"TM_TORRENT_ID":             "6580159",
		"TM_TORRENT_TRACKER":        "rutracker.org",
		"TM_TORRENT_NAME":           "new title",
		"TM_TORRENT_HASH":           "deadbeef",
		"TM_TORRENT_FILE":           "/tmp/item.torrent",
		"TM_TORRENT_FILE_SIZE":      "11",
		"TM_CLIENT":                 "qBittorrent",
		"TM_TORRENT_CLIENT":         "qBittorrent",
		"TM_TORRENT_CLIENT_ENABLED": "true",
		"TM_TORRENT_CLIENT_ADD_OK":  "true",
		"TM_TORRENT_CLIENT_HASH":    "deadbeef",
		"TM_SAVE_PATH":              "/downloads/anime",
	}
	for k, want := range checks {
		if got[k] != want {
			t.Fatalf("%s = %q, want %q", k, got[k], want)
		}
	}
	if got["TM_TORRENT_UPDATED_AT"] != updatedAt.Format(time.RFC3339) {
		t.Fatalf("unexpected updated_at: %q", got["TM_TORRENT_UPDATED_AT"])
	}
}

func TestPostUpdateEnvMarksTorrentClientOptional(t *testing.T) {
	env := postUpdateEnv(nil, Settings{UseTorrent: false, TorrentClient: "qBittorrent"}, TorrentItem{ID: 1}, sitetpl.CheckResult{Updated: true}, "", "", "", false)
	got := map[string]string{}
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if ok {
			got[k] = v
		}
	}
	if got["TM_TORRENT_CLIENT_ENABLED"] != "false" {
		t.Fatalf("TM_TORRENT_CLIENT_ENABLED = %q, want false", got["TM_TORRENT_CLIENT_ENABLED"])
	}
	if got["TM_TORRENT_CLIENT_ADD_OK"] != "false" {
		t.Fatalf("TM_TORRENT_CLIENT_ADD_OK = %q, want false", got["TM_TORRENT_CLIENT_ADD_OK"])
	}
	if got["TM_CLIENT"] != "" || got["TM_TORRENT_CLIENT"] != "" {
		t.Fatalf("client env should be empty when torrent client is not configured: TM_CLIENT=%q TM_TORRENT_CLIENT=%q", got["TM_CLIENT"], got["TM_TORRENT_CLIENT"])
	}
}

type credentialRepo struct {
	creds       []Credential
	torrents    []TorrentItem
	createCalls int
}

func (r *credentialRepo) ListTorrents(ctx context.Context, sortBy, dir, filter string) ([]TorrentItem, error) {
	return append([]TorrentItem(nil), r.torrents...), nil
}
func (r *credentialRepo) GetTorrent(ctx context.Context, id int64) (TorrentItem, error) {
	return TorrentItem{}, ErrNotFound
}
func (r *credentialRepo) CreateTorrent(ctx context.Context, item TorrentItem) (TorrentItem, error) {
	r.createCalls++
	item.ID = int64(len(r.torrents) + 1)
	r.torrents = append(r.torrents, item)
	return item, nil
}
func (r *credentialRepo) UpdateTorrent(ctx context.Context, id int64, patch UpdateTorrentRequest) (TorrentItem, error) {
	return TorrentItem{}, ErrNotFound
}
func (r *credentialRepo) SetTorrentClientHash(ctx context.Context, id int64, hash string) (TorrentItem, error) {
	return TorrentItem{}, ErrNotFound
}
func (r *credentialRepo) DeleteTorrent(ctx context.Context, id int64) error   { return ErrNotFound }
func (r *credentialRepo) ListWarnings(ctx context.Context) ([]Warning, error) { return nil, nil }
func (r *credentialRepo) CreateWarning(ctx context.Context, warning Warning) (Warning, error) {
	return warning, nil
}
func (r *credentialRepo) ClearWarnings(ctx context.Context, tracker string) (int, error) {
	return 0, nil
}
func (r *credentialRepo) ListNews(ctx context.Context) ([]News, error)     { return nil, nil }
func (r *credentialRepo) MarkNewsRead(ctx context.Context, id int64) error { return nil }
func (r *credentialRepo) ListCredentials(ctx context.Context) ([]Credential, error) {
	return append([]Credential(nil), r.creds...), nil
}
func (r *credentialRepo) EnsureCredentials(ctx context.Context, credentials []Credential) error {
	maxID := int64(0)
	byTracker := map[string]int{}
	for i, c := range r.creds {
		if c.ID > maxID {
			maxID = c.ID
		}
		byTracker[c.Tracker] = i
	}
	for _, c := range credentials {
		if _, ok := byTracker[c.Tracker]; ok {
			continue
		}
		maxID++
		c.ID = maxID
		r.creds = append(r.creds, c)
		byTracker[c.Tracker] = len(r.creds) - 1
	}
	return nil
}
func (r *credentialRepo) UpdateCredential(ctx context.Context, id int64, patch UpdateCredentialRequest) (Credential, error) {
	return Credential{}, ErrNotFound
}
func (r *credentialRepo) GetSettings(ctx context.Context) (Settings, error) {
	return DefaultSettings(), nil
}
func (r *credentialRepo) UpdateSettings(ctx context.Context, patch UpdateSettingsRequest) (Settings, error) {
	return DefaultSettings(), nil
}
func (r *credentialRepo) SaveTorrentClientSession(ctx context.Context, cookie string, expires *time.Time) error {
	return nil
}
func (r *credentialRepo) Bootstrap(ctx context.Context, authenticated bool) (Bootstrap, error) {
	return Bootstrap{}, nil
}

func TestAddTorrentRejectsExistingForumTopic(t *testing.T) {
	repo := &credentialRepo{}
	svc := NewService(repo, nil)
	req := AddTorrentRequest{Kind: TorrentKindTheme, URL: "https://rutracker.org/forum/viewtopic.php?t=123456"}

	first, err := svc.AddTorrent(context.Background(), req)
	if err != nil {
		t.Fatalf("first AddTorrent: %v", err)
	}
	second, err := svc.AddTorrent(context.Background(), req)
	if !errors.Is(err, ErrTorrentExists) {
		t.Fatalf("second AddTorrent error = %v, want ErrTorrentExists", err)
	}
	if second.ID != first.ID {
		t.Fatalf("duplicate returned id %d, want existing id %d", second.ID, first.ID)
	}
	if repo.createCalls != 1 {
		t.Fatalf("CreateTorrent called %d times, want 1", repo.createCalls)
	}
}

func TestListCredentialsFollowsTemplateRegistry(t *testing.T) {
	repo := &credentialRepo{creds: []Credential{{ID: 1, Tracker: "lostfilm.tv", Type: TrackerTypeRSS, AccessMode: AccessModeNative, Necessarily: true}}}
	reg := sitetpl.DefaultRegistry()
	svc := NewServiceWithRunner(repo, nil, sitetpl.NewRunner(sitetpl.WithRegistry(reg)))
	creds, err := svc.ListCredentials(context.Background())
	if err != nil {
		t.Fatalf("ListCredentials: %v", err)
	}
	got := map[string]Credential{}
	for _, c := range creds {
		got[c.Tracker] = c
	}
	if _, ok := got["rutracker.org"]; !ok {
		t.Fatalf("rutracker.org credential missing: %+v", creds)
	}
	if _, ok := got["nnmclub.to"]; !ok {
		t.Fatalf("nnmclub.to credential missing: %+v", creds)
	}
	if _, ok := got["lostfilm.tv"]; ok {
		t.Fatalf("lostfilm.tv should be hidden because there is no loaded template: %+v", creds)
	}
	if got["rutracker.org"].ID == 0 || got["nnmclub.to"].ID == 0 {
		t.Fatalf("credentials should be persisted rows with positive IDs: %+v", creds)
	}
}
