package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"torrentmonitor-go/internal/core"
)

type DiskStore struct {
	path string
	mem  *MemoryStore
}

type diskSnapshot struct {
	Version                     int                `json:"version"`
	NextTorrent                 int64              `json:"next_torrent"`
	NextWarning                 int64              `json:"next_warning"`
	NextNews                    int64              `json:"next_news"`
	Torrents                    []core.TorrentItem `json:"torrents"`
	Warnings                    []core.Warning     `json:"warnings"`
	News                        []core.News        `json:"news"`
	Credentials                 []core.Credential  `json:"credentials"`
	LastStart                   *time.Time         `json:"last_start"`
	Settings                    core.Settings      `json:"settings"`
	TorrentSessionCookie        string             `json:"torrent_session_cookie,omitempty"`
	TorrentSessionCookieExpires *time.Time         `json:"torrent_session_cookie_expires,omitempty"`
}

func NewDiskStore(path string) (*DiskStore, error) {
	if path == "" {
		return nil, errors.New("disk store path is empty")
	}
	path = filepath.Clean(path)

	mem := NewMemoryStore()
	ds := &DiskStore{path: path, mem: mem}

	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := ds.save(); err != nil {
			return nil, err
		}
		return ds, nil
	} else if err != nil {
		return nil, fmt.Errorf("stat data file: %w", err)
	}

	if err := ds.load(); err != nil {
		return nil, err
	}
	return ds, nil
}

func (s *DiskStore) ListTorrents(ctx context.Context, sortBy, dir, filter string) ([]core.TorrentItem, error) {
	return s.mem.ListTorrents(ctx, sortBy, dir, filter)
}

func (s *DiskStore) GetTorrent(ctx context.Context, id int64) (core.TorrentItem, error) {
	return s.mem.GetTorrent(ctx, id)
}

func (s *DiskStore) CreateTorrent(ctx context.Context, item core.TorrentItem) (core.TorrentItem, error) {
	created, err := s.mem.CreateTorrent(ctx, item)
	if err != nil {
		return core.TorrentItem{}, err
	}
	return created, s.save()
}

func (s *DiskStore) UpdateTorrent(ctx context.Context, id int64, patch core.UpdateTorrentRequest) (core.TorrentItem, error) {
	updated, err := s.mem.UpdateTorrent(ctx, id, patch)
	if err != nil {
		return core.TorrentItem{}, err
	}
	return updated, s.save()
}

func (s *DiskStore) SetTorrentClientHash(ctx context.Context, id int64, hash string) (core.TorrentItem, error) {
	updated, err := s.mem.SetTorrentClientHash(ctx, id, hash)
	if err != nil {
		return core.TorrentItem{}, err
	}
	return updated, s.save()
}

func (s *DiskStore) DeleteTorrent(ctx context.Context, id int64) error {
	if err := s.mem.DeleteTorrent(ctx, id); err != nil {
		return err
	}
	return s.save()
}

func (s *DiskStore) ListWarnings(ctx context.Context) ([]core.Warning, error) {
	return s.mem.ListWarnings(ctx)
}

func (s *DiskStore) CreateWarning(ctx context.Context, warning core.Warning) (core.Warning, error) {
	created, err := s.mem.CreateWarning(ctx, warning)
	if err != nil {
		return core.Warning{}, err
	}
	return created, s.save()
}

func (s *DiskStore) ClearWarnings(ctx context.Context, tracker string) (int, error) {
	count, err := s.mem.ClearWarnings(ctx, tracker)
	if err != nil {
		return count, err
	}
	return count, s.save()
}

func (s *DiskStore) ListNews(ctx context.Context) ([]core.News, error) {
	return s.mem.ListNews(ctx)
}

func (s *DiskStore) MarkNewsRead(ctx context.Context, id int64) error {
	if err := s.mem.MarkNewsRead(ctx, id); err != nil {
		return err
	}
	return s.save()
}

func (s *DiskStore) ListCredentials(ctx context.Context) ([]core.Credential, error) {
	return s.mem.ListCredentials(ctx)
}

func (s *DiskStore) EnsureCredentials(ctx context.Context, credentials []core.Credential) error {
	if err := s.mem.EnsureCredentials(ctx, credentials); err != nil {
		return err
	}
	return s.save()
}

func (s *DiskStore) UpdateCredential(ctx context.Context, id int64, patch core.UpdateCredentialRequest) (core.Credential, error) {
	updated, err := s.mem.UpdateCredential(ctx, id, patch)
	if err != nil {
		return core.Credential{}, err
	}
	return updated, s.save()
}

func (s *DiskStore) GetSettings(ctx context.Context) (core.Settings, error) {
	return s.mem.GetSettings(ctx)
}

func (s *DiskStore) UpdateSettings(ctx context.Context, patch core.UpdateSettingsRequest) (core.Settings, error) {
	updated, err := s.mem.UpdateSettings(ctx, patch)
	if err != nil {
		return core.Settings{}, err
	}
	return updated, s.save()
}

func (s *DiskStore) SaveTorrentClientSession(ctx context.Context, cookie string, expires *time.Time) error {
	if err := s.mem.SaveTorrentClientSession(ctx, cookie, expires); err != nil {
		return err
	}
	return s.save()
}

func (s *DiskStore) Bootstrap(ctx context.Context, authenticated bool) (core.Bootstrap, error) {
	b, err := s.mem.Bootstrap(ctx, authenticated)
	if err != nil {
		return core.Bootstrap{}, err
	}
	b.Version.Database = "json"
	return b, nil
}

func (s *DiskStore) load() error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return fmt.Errorf("read data file: %w", err)
	}
	var snap diskSnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return fmt.Errorf("parse data file %s: %w", s.path, err)
	}
	if snap.Version != 1 {
		return fmt.Errorf("unsupported data file version: %d", snap.Version)
	}

	mem := &MemoryStore{
		nextTorrent: snap.NextTorrent,
		nextWarning: snap.NextWarning,
		nextNews:    snap.NextNews,
		torrents:    make(map[int64]core.TorrentItem, len(snap.Torrents)),
		warnings:    make(map[int64]core.Warning, len(snap.Warnings)),
		news:        make(map[int64]core.News, len(snap.News)),
		credentials: append([]core.Credential(nil), snap.Credentials...),
		lastStart:   snap.LastStart,
		settings:    snap.Settings,
	}
	mem.settings.TorrentSessionCookie = snap.TorrentSessionCookie
	mem.settings.TorrentSessionCookieExpires = snap.TorrentSessionCookieExpires
	for _, item := range snap.Torrents {
		mem.torrents[item.ID] = item
		if item.ID >= mem.nextTorrent {
			mem.nextTorrent = item.ID + 1
		}
	}
	for _, w := range snap.Warnings {
		mem.warnings[w.ID] = w
		if w.ID >= mem.nextWarning {
			mem.nextWarning = w.ID + 1
		}
	}
	for _, n := range snap.News {
		mem.news[n.ID] = n
		if n.ID >= mem.nextNews {
			mem.nextNews = n.ID + 1
		}
	}
	if mem.nextTorrent == 0 {
		mem.nextTorrent = 1
	}
	if mem.nextWarning == 0 {
		mem.nextWarning = 1
	}
	if mem.nextNews == 0 {
		mem.nextNews = 1
	}
	if mem.lastStart == nil {
		now := time.Now()
		mem.lastStart = &now
	}
	if mem.settings.UserAgent == "" {
		mem.settings = core.DefaultSettings()
	}
	mem.settings.TelegramBotTokenSet = mem.settings.TelegramBotToken != ""
	s.mem = mem
	return nil
}

func (s *DiskStore) save() error {
	snap := s.snapshot()
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal data file: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write data file: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("replace data file: %w", err)
	}
	return nil
}

func (s *DiskStore) snapshot() diskSnapshot {
	s.mem.mu.RLock()
	defer s.mem.mu.RUnlock()

	torrents := make([]core.TorrentItem, 0, len(s.mem.torrents))
	for _, item := range s.mem.torrents {
		torrents = append(torrents, item)
	}
	sort.Slice(torrents, func(i, j int) bool { return torrents[i].ID < torrents[j].ID })

	warnings := make([]core.Warning, 0, len(s.mem.warnings))
	for _, w := range s.mem.warnings {
		warnings = append(warnings, w)
	}
	sort.Slice(warnings, func(i, j int) bool { return warnings[i].ID < warnings[j].ID })

	news := make([]core.News, 0, len(s.mem.news))
	for _, n := range s.mem.news {
		news = append(news, n)
	}
	sort.Slice(news, func(i, j int) bool { return news[i].ID < news[j].ID })

	return diskSnapshot{
		Version:                     1,
		NextTorrent:                 s.mem.nextTorrent,
		NextWarning:                 s.mem.nextWarning,
		NextNews:                    s.mem.nextNews,
		Torrents:                    torrents,
		Warnings:                    warnings,
		News:                        news,
		Credentials:                 append([]core.Credential(nil), s.mem.credentials...),
		LastStart:                   s.mem.lastStart,
		Settings:                    s.mem.settings,
		TorrentSessionCookie:        s.mem.settings.TorrentSessionCookie,
		TorrentSessionCookieExpires: s.mem.settings.TorrentSessionCookieExpires,
	}
}

func (s *DiskStore) Path() string {
	return s.path
}
