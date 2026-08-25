package store

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"torrentmonitor-go/internal/core"
)

type MemoryStore struct {
	mu          sync.RWMutex
	nextTorrent int64
	nextWarning int64
	nextNews    int64
	torrents    map[int64]core.TorrentItem
	warnings    map[int64]core.Warning
	news        map[int64]core.News
	credentials []core.Credential
	lastStart   *time.Time
	settings    core.Settings
}

func NewMemoryStore() *MemoryStore {
	now := time.Now()
	q := "sd"
	return &MemoryStore{
		nextTorrent: 3,
		nextWarning: 1,
		nextNews:    2,
		torrents: map[int64]core.TorrentItem{
			1: {
				ID: 1, Tracker: "rutracker.org", Type: core.TrackerTypeForum,
				Name: "Example forum release", URL: core.BuildTrackerURL("rutracker.org", "123456"),
				TorrentID: "123456", Path: "/downloads", UpdatedAt: &now,
				AutoUpdate: true,
			},
			2: {
				ID: 2, Tracker: "lostfilm.tv", Type: core.TrackerTypeRSS,
				Name: "Example Series", Quality: &q, Path: "/downloads/series",
				Episode: "S01E01", UpdatedAt: &now, AutoUpdate: true,
			},
		},
		warnings: map[int64]core.Warning{},
		news: map[int64]core.News{
			1: {ID: 1, Text: "TorrentMonitor Go scaffold started", New: true},
		},
		credentials: []core.Credential{
			{ID: 1, Tracker: "rutracker.org", Type: core.TrackerTypeForum, AccessMode: core.AccessModeNative, Necessarily: true},
			{ID: 2, Tracker: "nnmclub.to", Type: core.TrackerTypeForum, AccessMode: core.AccessModeNative, Necessarily: true},
			{ID: 3, Tracker: "lostfilm.tv", Type: core.TrackerTypeRSS, AccessMode: core.AccessModeNative, Necessarily: true},
			{ID: 4, Tracker: "newstudio.tv", Type: core.TrackerTypeRSS, AccessMode: core.AccessModeNative, Necessarily: true},
		},
		lastStart: &now,
		settings:  core.DefaultSettings(),
	}
}

func (s *MemoryStore) ListTorrents(ctx context.Context, sortBy, dir, filter string) ([]core.TorrentItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	filter = strings.ToLower(strings.TrimSpace(filter))
	out := make([]core.TorrentItem, 0, len(s.torrents))
	for _, item := range s.torrents {
		if filter != "" && !strings.Contains(strings.ToLower(item.Name), filter) && !strings.Contains(strings.ToLower(item.Tracker), filter) {
			continue
		}
		out = append(out, item)
	}

	sort.Slice(out, func(i, j int) bool {
		less := strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		if sortBy == "date" {
			var ti, tj time.Time
			if out[i].UpdatedAt != nil {
				ti = *out[i].UpdatedAt
			}
			if out[j].UpdatedAt != nil {
				tj = *out[j].UpdatedAt
			}
			less = ti.Before(tj)
		}
		if dir == "desc" {
			return !less
		}
		return less
	})

	return out, nil
}

func (s *MemoryStore) GetTorrent(ctx context.Context, id int64) (core.TorrentItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.torrents[id]
	if !ok {
		return core.TorrentItem{}, core.ErrNotFound
	}
	return item, nil
}

func (s *MemoryStore) CreateTorrent(ctx context.Context, item core.TorrentItem) (core.TorrentItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item.ID = s.nextTorrent
	s.nextTorrent++
	s.torrents[item.ID] = item
	return item, nil
}

func (s *MemoryStore) UpdateTorrent(ctx context.Context, id int64, patch core.UpdateTorrentRequest) (core.TorrentItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.torrents[id]
	if !ok {
		return core.TorrentItem{}, core.ErrNotFound
	}
	if patch.Name != nil {
		item.Name = *patch.Name
	}
	if patch.Path != nil {
		item.Path = *patch.Path
	}
	if patch.TorrentID != nil {
		item.TorrentID = *patch.TorrentID
		item.URL = core.BuildTrackerURL(item.Tracker, item.TorrentID)
	}
	if patch.AutoUpdate != nil {
		item.AutoUpdate = *patch.AutoUpdate
	}
	if patch.Paused != nil {
		item.Paused = *patch.Paused
	}
	if patch.ResetTimestamp != nil && *patch.ResetTimestamp {
		item.UpdatedAt = nil
		item.Episode = ""
		item.Hash = ""
	}
	if patch.UpdatedAt != nil {
		t := *patch.UpdatedAt
		item.UpdatedAt = &t
	}
	if patch.Closed != nil {
		item.Closed = *patch.Closed
	}
	if patch.HasError != nil {
		item.HasError = *patch.HasError
	}
	s.torrents[id] = item
	return item, nil
}

func (s *MemoryStore) SetTorrentClientHash(ctx context.Context, id int64, hash string) (core.TorrentItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.torrents[id]
	if !ok {
		return core.TorrentItem{}, core.ErrNotFound
	}
	item.Hash = hash
	now := time.Now()
	item.UpdatedAt = &now
	item.HasError = false
	s.torrents[id] = item
	return item, nil
}

func (s *MemoryStore) DeleteTorrent(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.torrents[id]; !ok {
		return core.ErrNotFound
	}
	delete(s.torrents, id)
	return nil
}

func (s *MemoryStore) ListWarnings(ctx context.Context) ([]core.Warning, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]core.Warning, 0, len(s.warnings))
	for _, w := range s.warnings {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, nil
}

func (s *MemoryStore) CreateWarning(ctx context.Context, warning core.Warning) (core.Warning, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if warning.Time.IsZero() {
		warning.Time = time.Now()
	}
	if warning.ID == 0 {
		warning.ID = s.nextWarning
		s.nextWarning++
	}
	s.warnings[warning.ID] = warning
	return warning, nil
}

func (s *MemoryStore) ClearWarnings(ctx context.Context, tracker string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for id, w := range s.warnings {
		if tracker == "" || strings.EqualFold(w.Where, tracker) || strings.EqualFold(w.Tracker, tracker) {
			delete(s.warnings, id)
			count++
		}
	}
	return count, nil
}

func (s *MemoryStore) ListNews(ctx context.Context) ([]core.News, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]core.News, 0, len(s.news))
	for _, n := range s.news {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

func (s *MemoryStore) MarkNewsRead(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.news[id]
	if !ok {
		return core.ErrNotFound
	}
	n.New = false
	s.news[id] = n
	return nil
}

func (s *MemoryStore) ListCredentials(ctx context.Context) ([]core.Credential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]core.Credential, len(s.credentials))
	copy(out, s.credentials)
	return out, nil
}

func (s *MemoryStore) EnsureCredentials(ctx context.Context, credentials []core.Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	maxID := int64(0)
	byTracker := map[string]int{}
	for i, c := range s.credentials {
		if c.ID > maxID {
			maxID = c.ID
		}
		tracker := strings.ToLower(strings.TrimSpace(c.Tracker))
		if tracker != "" {
			byTracker[tracker] = i
		}
	}
	for _, c := range credentials {
		tracker := strings.ToLower(strings.TrimSpace(c.Tracker))
		if tracker == "" {
			continue
		}
		if idx, ok := byTracker[tracker]; ok {
			if s.credentials[idx].Type == "" {
				s.credentials[idx].Type = c.Type
			}
			if s.credentials[idx].AccessMode == "" {
				s.credentials[idx].AccessMode = c.AccessMode
			}
			s.credentials[idx].Necessarily = c.Necessarily
			continue
		}
		maxID++
		c.ID = maxID
		c.Tracker = tracker
		if c.Type == "" {
			c.Type = core.TrackerTypeForum
		}
		if c.AccessMode == "" {
			c.AccessMode = core.AccessModeNative
		}
		s.credentials = append(s.credentials, c)
		byTracker[tracker] = len(s.credentials) - 1
	}
	return nil
}

func (s *MemoryStore) UpdateCredential(ctx context.Context, id int64, patch core.UpdateCredentialRequest) (core.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.credentials {
		if s.credentials[i].ID != id {
			continue
		}
		if patch.Login != nil {
			s.credentials[i].Login = *patch.Login
		}
		if patch.Password != nil {
			s.credentials[i].Password = *patch.Password
		}
		if patch.Passkey != nil {
			s.credentials[i].Passkey = *patch.Passkey
		}
		if patch.AccessMode != nil {
			s.credentials[i].AccessMode = core.NormalizeAccessMode(*patch.AccessMode)
		}
		if patch.UseProxy != nil {
			s.credentials[i].UseProxy = *patch.UseProxy
		}
		if patch.Cookie != nil {
			s.credentials[i].Cookie = strings.TrimSpace(*patch.Cookie)
		}
		return s.credentials[i], nil
	}
	return core.Credential{}, core.ErrNotFound
}

func (s *MemoryStore) GetSettings(ctx context.Context) (core.Settings, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings, nil
}

func (s *MemoryStore) UpdateSettings(ctx context.Context, patch core.UpdateSettingsRequest) (core.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = core.ApplySettingsPatch(s.settings, patch)
	return s.settings, nil
}

func (s *MemoryStore) SaveTorrentClientSession(ctx context.Context, cookie string, expires *time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings.TorrentSessionCookie = cookie
	s.settings.TorrentSessionCookieExpires = expires
	return nil
}

func (s *MemoryStore) Bootstrap(ctx context.Context, authenticated bool) (core.Bootstrap, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	warnings := len(s.warnings)
	news := 0
	for _, n := range s.news {
		if n.New {
			news++
		}
	}

	return core.Bootstrap{
		Version:         core.VersionInfo{App: "0.1.0-dev", Database: "memory"},
		Auth:            core.AuthInfo{Enabled: true, Authenticated: authenticated},
		Counters:        core.Counters{Warnings: warnings, News: news, Torrents: len(s.torrents), WatchUsers: 0},
		LastStart:       s.lastStart,
		UpdateAvailable: false,
		Features: map[string][]string{
			"torrent_clients": {"transmission", "qbittorrent", "deluge"},
			"notifications":   {"email", "telegram", "pushover", "pushbullet", "pushall", "prowl"},
		},
	}, nil
}
