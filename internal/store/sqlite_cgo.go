//go:build cgo

package store

/*
#cgo pkg-config: sqlite3
#include <sqlite3.h>
#include <stdlib.h>
static inline int bind_text_transient(sqlite3_stmt *stmt, int idx, const char *value) {
    return sqlite3_bind_text(stmt, idx, value, -1, SQLITE_TRANSIENT);
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"torrentmonitor-go/internal/core"
)

type SQLiteStore struct {
	mu   sync.Mutex
	db   *C.sqlite3
	path string
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	if path == "" {
		return nil, errors.New("sqlite store path is empty")
	}
	path = filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite dir: %w", err)
	}

	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	var db *C.sqlite3
	flags := C.SQLITE_OPEN_READWRITE | C.SQLITE_OPEN_CREATE | C.SQLITE_OPEN_FULLMUTEX
	if rc := C.sqlite3_open_v2(cpath, &db, C.int(flags), nil); rc != C.SQLITE_OK {
		msg := sqliteErr(db)
		if db != nil {
			C.sqlite3_close(db)
		}
		return nil, fmt.Errorf("open sqlite database: %s", msg)
	}

	s := &SQLiteStore{db: db, path: path}
	if err := s.migrate(); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLiteStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	if rc := C.sqlite3_close(s.db); rc != C.SQLITE_OK {
		return fmt.Errorf("close sqlite database: %s", sqliteErr(s.db))
	}
	s.db = nil
	return nil
}

func (s *SQLiteStore) Path() string { return s.path }

func (s *SQLiteStore) migrate() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	statements := []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA synchronous = NORMAL`,
		`PRAGMA foreign_keys = ON`,
		`PRAGMA busy_timeout = 5000`,
		`CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS torrent (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tracker TEXT NOT NULL,
			type TEXT NOT NULL DEFAULT 'forum',
			name TEXT NOT NULL DEFAULT '',
			url TEXT NOT NULL DEFAULT '',
			torrent_id TEXT NOT NULL DEFAULT '',
			quality TEXT,
			path TEXT NOT NULL DEFAULT '',
			episode TEXT NOT NULL DEFAULT '',
			updated_at TEXT,
			auto_update INTEGER NOT NULL DEFAULT 0,
			hash TEXT NOT NULL DEFAULT '',
			paused INTEGER NOT NULL DEFAULT 0,
			has_error INTEGER NOT NULL DEFAULT 0,
			closed INTEGER NOT NULL DEFAULT 0,
			update_title INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_torrent_name ON torrent(name COLLATE NOCASE)`,
		`CREATE INDEX IF NOT EXISTS idx_torrent_tracker ON torrent(tracker)`,
		`CREATE INDEX IF NOT EXISTS idx_torrent_updated_at ON torrent(updated_at)`,
		`CREATE TABLE IF NOT EXISTS warning (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			time TEXT NOT NULL,
			where_name TEXT NOT NULL,
			reason TEXT NOT NULL,
			torrent_id INTEGER,
			tracker TEXT NOT NULL DEFAULT '',
			action_kind TEXT NOT NULL DEFAULT '',
			action_label TEXT NOT NULL DEFAULT '',
			action_url TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_warning_where ON warning(where_name)`,
		`CREATE TABLE IF NOT EXISTS news (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			text TEXT NOT NULL,
			new INTEGER NOT NULL DEFAULT 1
		)`,
		`CREATE TABLE IF NOT EXISTS credentials (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tracker TEXT NOT NULL UNIQUE,
			login TEXT NOT NULL DEFAULT '',
			password TEXT NOT NULL DEFAULT '',
			passkey TEXT NOT NULL DEFAULT '',
			cookie TEXT NOT NULL DEFAULT '',
			access_mode TEXT NOT NULL DEFAULT 'native',
			use_proxy INTEGER NOT NULL DEFAULT 0,
			type TEXT NOT NULL DEFAULT 'forum',
			necessarily INTEGER NOT NULL DEFAULT 1
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT ''
		)`,
	}
	for _, st := range statements {
		if err := s.execLocked(st); err != nil {
			return err
		}
	}
	if err := s.execLocked(`ALTER TABLE credentials ADD COLUMN cookie TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
		return err
	}
	if err := s.execLocked(`ALTER TABLE credentials ADD COLUMN access_mode TEXT NOT NULL DEFAULT 'native'`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
		return err
	}
	if err := s.execLocked(`ALTER TABLE credentials ADD COLUMN use_proxy INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
		return err
	}
	for _, st := range []string{
		`ALTER TABLE warning ADD COLUMN tracker TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE warning ADD COLUMN action_kind TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE warning ADD COLUMN action_label TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE warning ADD COLUMN action_url TEXT NOT NULL DEFAULT ''`,
	} {
		if err := s.execLocked(st); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	// Per-torrent scripts were replaced by one global post_update_script setting.
	// Drop the obsolete column for existing SQLite databases when supported.
	if err := s.execLocked(`ALTER TABLE torrent DROP COLUMN script`); err != nil &&
		!strings.Contains(err.Error(), "no such column") &&
		!strings.Contains(err.Error(), "no such column: script") &&
		!strings.Contains(err.Error(), `no such column: "script"`) {
		return err
	}
	return s.seedLocked()
}

func (s *SQLiteStore) seedLocked() error {
	count, err := s.countLocked(`SELECT COUNT(*) FROM credentials`)
	if err != nil {
		return err
	}
	if count == 0 {
		defaults := []core.Credential{
			{Tracker: "rutracker.org", Type: core.TrackerTypeForum, AccessMode: core.AccessModeNative, Necessarily: true},
			{Tracker: "nnmclub.to", Type: core.TrackerTypeForum, AccessMode: core.AccessModeNative, Necessarily: true},
			{Tracker: "lostfilm.tv", Type: core.TrackerTypeRSS, AccessMode: core.AccessModeNative, Necessarily: true},
			{Tracker: "newstudio.tv", Type: core.TrackerTypeRSS, AccessMode: core.AccessModeNative, Necessarily: true},
		}
		for _, c := range defaults {
			if err := s.insertCredentialLocked(c); err != nil {
				return err
			}
		}
	}

	count, err = s.countLocked(`SELECT COUNT(*) FROM torrent`)
	if err != nil {
		return err
	}
	if count == 0 {
		now := time.Now()
		q := "sd"
		if _, err := s.createTorrentLocked(core.TorrentItem{
			Tracker: "rutracker.org", Type: core.TrackerTypeForum,
			Name: "Example forum release", URL: core.BuildTrackerURL("rutracker.org", "123456"),
			TorrentID: "123456", Path: "/downloads", UpdatedAt: &now,
			AutoUpdate: true, UpdateTitle: true,
		}); err != nil {
			return err
		}
		if _, err := s.createTorrentLocked(core.TorrentItem{
			Tracker: "lostfilm.tv", Type: core.TrackerTypeRSS,
			Name: "Example Series", Quality: &q, Path: "/downloads/series",
			Episode: "S01E01", UpdatedAt: &now, AutoUpdate: true, UpdateTitle: true,
		}); err != nil {
			return err
		}
	}

	// Older scaffold builds seeded a placeholder warning before the site runner was wired.
	// Keep existing user warnings, but remove that stale placeholder during startup.
	if err := s.execLocked(`DELETE FROM warning WHERE where_name = 'system' AND reason = 'Go scaffold mode: site runner is not connected yet'`); err != nil {
		return err
	}

	count, err = s.countLocked(`SELECT COUNT(*) FROM news`)
	if err != nil {
		return err
	}
	if count == 0 {
		if err := s.insertNewsLocked(core.News{Text: "TorrentMonitor Go scaffold started", New: true}); err != nil {
			return err
		}
	}

	count, err = s.countLocked(`SELECT COUNT(*) FROM settings`)
	if err != nil {
		return err
	}
	if count == 0 {
		if err := s.saveSettingsLocked(core.DefaultSettings()); err != nil {
			return err
		}
	}

	count, err = s.countLocked(`SELECT COUNT(*) FROM meta WHERE key = 'last_start'`)
	if err != nil {
		return err
	}
	if count == 0 {
		return s.setMetaLocked("last_start", time.Now().Format(time.RFC3339Nano))
	}
	return nil
}

func (s *SQLiteStore) ListTorrents(ctx context.Context, sortBy, dir, filter string) ([]core.TorrentItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	order := "LOWER(name) ASC, id ASC"
	if sortBy == "date" {
		order = "COALESCE(updated_at, '') ASC, id ASC"
	}
	if strings.EqualFold(dir, "desc") {
		if sortBy == "date" {
			order = "COALESCE(updated_at, '') DESC, id DESC"
		} else {
			order = "LOWER(name) DESC, id DESC"
		}
	}

	query := `SELECT id, tracker, type, name, url, torrent_id, quality, path, episode, updated_at, auto_update, hash, paused, has_error, closed, update_title FROM torrent`
	var args []any
	filter = strings.TrimSpace(filter)
	if filter != "" {
		query += ` WHERE LOWER(name) LIKE LOWER(?) OR LOWER(tracker) LIKE LOWER(?)`
		like := "%" + filter + "%"
		args = append(args, like, like)
	}
	query += ` ORDER BY ` + order

	stmt, err := s.prepareLocked(query)
	if err != nil {
		return nil, err
	}
	defer C.sqlite3_finalize(stmt)
	if err := bindAll(stmt, args...); err != nil {
		return nil, err
	}

	items := make([]core.TorrentItem, 0)
	for {
		rc := C.sqlite3_step(stmt)
		switch rc {
		case C.SQLITE_ROW:
			items = append(items, scanTorrent(stmt))
		case C.SQLITE_DONE:
			return items, nil
		default:
			return nil, fmt.Errorf("list torrents: %s", sqliteErr(s.db))
		}
	}
}

func (s *SQLiteStore) GetTorrent(ctx context.Context, id int64) (core.TorrentItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getTorrentLocked(id)
}

func (s *SQLiteStore) CreateTorrent(ctx context.Context, item core.TorrentItem) (core.TorrentItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createTorrentLocked(item)
}

func (s *SQLiteStore) UpdateTorrent(ctx context.Context, id int64, patch core.UpdateTorrentRequest) (core.TorrentItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	item, err := s.getTorrentLocked(id)
	if err != nil {
		return core.TorrentItem{}, err
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

	stmt, err := s.prepareLocked(`UPDATE torrent SET name = ?, path = ?, torrent_id = ?, url = ?, episode = ?, updated_at = ?, auto_update = ?, hash = ?, paused = ?, has_error = ?, closed = ?, update_title = ? WHERE id = ?`)
	if err != nil {
		return core.TorrentItem{}, err
	}
	defer C.sqlite3_finalize(stmt)
	if err := bindAll(stmt,
		item.Name, item.Path, item.TorrentID, item.URL, item.Episode, timePtrToDB(item.UpdatedAt), boolInt(item.AutoUpdate), item.Hash,
		boolInt(item.Paused), boolInt(item.HasError), boolInt(item.Closed), boolInt(item.UpdateTitle), id,
	); err != nil {
		return core.TorrentItem{}, err
	}
	if err := stepDone(s.db, stmt, "update torrent"); err != nil {
		return core.TorrentItem{}, err
	}
	return item, nil
}

func (s *SQLiteStore) SetTorrentClientHash(ctx context.Context, id int64, hash string) (core.TorrentItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, err := s.getTorrentLocked(id)
	if err != nil {
		return core.TorrentItem{}, err
	}
	now := time.Now()
	item.Hash = hash
	item.UpdatedAt = &now
	item.HasError = false

	stmt, err := s.prepareLocked(`UPDATE torrent SET hash = ?, updated_at = ?, has_error = 0 WHERE id = ?`)
	if err != nil {
		return core.TorrentItem{}, err
	}
	defer C.sqlite3_finalize(stmt)
	if err := bindAll(stmt, item.Hash, timePtrToDB(item.UpdatedAt), id); err != nil {
		return core.TorrentItem{}, err
	}
	if err := stepDone(s.db, stmt, "set torrent client hash"); err != nil {
		return core.TorrentItem{}, err
	}
	return item, nil
}

func (s *SQLiteStore) DeleteTorrent(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stmt, err := s.prepareLocked(`DELETE FROM torrent WHERE id = ?`)
	if err != nil {
		return err
	}
	defer C.sqlite3_finalize(stmt)
	if err := bindAll(stmt, id); err != nil {
		return err
	}
	if err := stepDone(s.db, stmt, "delete torrent"); err != nil {
		return err
	}
	if C.sqlite3_changes(s.db) == 0 {
		return core.ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) ListWarnings(ctx context.Context) ([]core.Warning, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stmt, err := s.prepareLocked(`SELECT id, time, where_name, reason, torrent_id, tracker, action_kind, action_label, action_url FROM warning ORDER BY time DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer C.sqlite3_finalize(stmt)

	out := make([]core.Warning, 0)
	for {
		rc := C.sqlite3_step(stmt)
		switch rc {
		case C.SQLITE_ROW:
			out = append(out, scanWarning(stmt))
		case C.SQLITE_DONE:
			return out, nil
		default:
			return nil, fmt.Errorf("list warnings: %s", sqliteErr(s.db))
		}
	}
}

func (s *SQLiteStore) CreateWarning(ctx context.Context, warning core.Warning) (core.Warning, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if warning.Time.IsZero() {
		warning.Time = time.Now()
	}
	if err := s.insertWarningLocked(warning); err != nil {
		return core.Warning{}, err
	}
	warning.ID = int64(C.sqlite3_last_insert_rowid(s.db))
	return warning, nil
}

func (s *SQLiteStore) ClearWarnings(ctx context.Context, tracker string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `DELETE FROM warning`
	var args []any
	tracker = strings.TrimSpace(tracker)
	if tracker != "" {
		query += ` WHERE where_name = ? OR tracker = ?`
		args = append(args, tracker, tracker)
	}
	stmt, err := s.prepareLocked(query)
	if err != nil {
		return 0, err
	}
	defer C.sqlite3_finalize(stmt)
	if err := bindAll(stmt, args...); err != nil {
		return 0, err
	}
	if err := stepDone(s.db, stmt, "clear warnings"); err != nil {
		return 0, err
	}
	return int(C.sqlite3_changes(s.db)), nil
}

func (s *SQLiteStore) ListNews(ctx context.Context) ([]core.News, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stmt, err := s.prepareLocked(`SELECT id, text, new FROM news ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer C.sqlite3_finalize(stmt)
	out := make([]core.News, 0)
	for {
		rc := C.sqlite3_step(stmt)
		switch rc {
		case C.SQLITE_ROW:
			out = append(out, core.News{ID: int64(C.sqlite3_column_int64(stmt, 0)), Text: colText(stmt, 1), New: colBool(stmt, 2)})
		case C.SQLITE_DONE:
			return out, nil
		default:
			return nil, fmt.Errorf("list news: %s", sqliteErr(s.db))
		}
	}
}

func (s *SQLiteStore) MarkNewsRead(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stmt, err := s.prepareLocked(`UPDATE news SET new = 0 WHERE id = ?`)
	if err != nil {
		return err
	}
	defer C.sqlite3_finalize(stmt)
	if err := bindAll(stmt, id); err != nil {
		return err
	}
	if err := stepDone(s.db, stmt, "mark news read"); err != nil {
		return err
	}
	if C.sqlite3_changes(s.db) == 0 {
		return core.ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) ListCredentials(ctx context.Context) ([]core.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stmt, err := s.prepareLocked(`SELECT id, tracker, login, password, passkey, cookie, access_mode, use_proxy, type, necessarily FROM credentials ORDER BY tracker`)
	if err != nil {
		return nil, err
	}
	defer C.sqlite3_finalize(stmt)
	out := make([]core.Credential, 0)
	for {
		rc := C.sqlite3_step(stmt)
		switch rc {
		case C.SQLITE_ROW:
			out = append(out, scanCredential(stmt))
		case C.SQLITE_DONE:
			return out, nil
		default:
			return nil, fmt.Errorf("list credentials: %s", sqliteErr(s.db))
		}
	}
}

func (s *SQLiteStore) EnsureCredentials(ctx context.Context, credentials []core.Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range credentials {
		tracker := strings.ToLower(strings.TrimSpace(c.Tracker))
		if tracker == "" {
			continue
		}
		c.Tracker = tracker
		if c.Type == "" {
			c.Type = core.TrackerTypeForum
		}
		if c.AccessMode == "" {
			c.AccessMode = core.AccessModeNative
		}

		stmt, err := s.prepareLocked(`SELECT id, access_mode FROM credentials WHERE tracker = ?`)
		if err != nil {
			return err
		}
		if err := bindAll(stmt, tracker); err != nil {
			C.sqlite3_finalize(stmt)
			return err
		}
		rc := C.sqlite3_step(stmt)
		var id int64
		var accessMode string
		if rc == C.SQLITE_ROW {
			id = int64(C.sqlite3_column_int64(stmt, 0))
			accessMode = colText(stmt, 1)
		}
		if rc != C.SQLITE_ROW && rc != C.SQLITE_DONE {
			err := fmt.Errorf("ensure credential lookup: %s", sqliteErr(s.db))
			C.sqlite3_finalize(stmt)
			return err
		}
		C.sqlite3_finalize(stmt)

		if id == 0 {
			if err := s.insertCredentialLocked(c); err != nil {
				return err
			}
			continue
		}

		if strings.TrimSpace(accessMode) == "" {
			accessMode = string(c.AccessMode)
		}
		stmt, err = s.prepareLocked(`UPDATE credentials SET type = ?, necessarily = ?, access_mode = ? WHERE id = ?`)
		if err != nil {
			return err
		}
		if err := bindAll(stmt, string(c.Type), boolInt(c.Necessarily), accessMode, id); err != nil {
			C.sqlite3_finalize(stmt)
			return err
		}
		if err := stepDone(s.db, stmt, "ensure credential update"); err != nil {
			C.sqlite3_finalize(stmt)
			return err
		}
		C.sqlite3_finalize(stmt)
	}
	return nil
}

func (s *SQLiteStore) UpdateCredential(ctx context.Context, id int64, patch core.UpdateCredentialRequest) (core.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cred, err := s.getCredentialLocked(id)
	if err != nil {
		return core.Credential{}, err
	}
	if patch.Login != nil {
		cred.Login = strings.TrimSpace(*patch.Login)
	}
	if patch.Password != nil {
		cred.Password = *patch.Password
	}
	if patch.Passkey != nil {
		cred.Passkey = strings.TrimSpace(*patch.Passkey)
	}
	if patch.AccessMode != nil {
		cred.AccessMode = core.NormalizeAccessMode(*patch.AccessMode)
	}
	if patch.UseProxy != nil {
		cred.UseProxy = *patch.UseProxy
	}
	if patch.Cookie != nil {
		cred.Cookie = strings.TrimSpace(*patch.Cookie)
	}

	stmt, err := s.prepareLocked(`UPDATE credentials SET login = ?, password = ?, passkey = ?, cookie = ?, access_mode = ?, use_proxy = ? WHERE id = ?`)
	if err != nil {
		return core.Credential{}, err
	}
	defer C.sqlite3_finalize(stmt)
	if err := bindAll(stmt, cred.Login, cred.Password, cred.Passkey, cred.Cookie, string(cred.AccessMode), boolInt(cred.UseProxy), id); err != nil {
		return core.Credential{}, err
	}
	if err := stepDone(s.db, stmt, "update credential"); err != nil {
		return core.Credential{}, err
	}
	return cred, nil
}

func (s *SQLiteStore) GetSettings(ctx context.Context) (core.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getSettingsLocked()
}

func (s *SQLiteStore) UpdateSettings(ctx context.Context, patch core.UpdateSettingsRequest) (core.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, err := s.getSettingsLocked()
	if err != nil {
		return core.Settings{}, err
	}
	settings = core.ApplySettingsPatch(settings, patch)
	if err := s.saveSettingsLocked(settings); err != nil {
		return core.Settings{}, err
	}
	return settings, nil
}

func (s *SQLiteStore) SaveTorrentClientSession(ctx context.Context, cookie string, expires *time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, err := s.getSettingsLocked()
	if err != nil {
		return err
	}
	settings.TorrentSessionCookie = cookie
	settings.TorrentSessionCookieExpires = expires
	return s.saveSettingsLocked(settings)
}

func (s *SQLiteStore) Bootstrap(ctx context.Context, authenticated bool) (core.Bootstrap, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	warnings, err := s.countLocked(`SELECT COUNT(*) FROM warning`)
	if err != nil {
		return core.Bootstrap{}, err
	}
	news, err := s.countLocked(`SELECT COUNT(*) FROM news WHERE new = 1`)
	if err != nil {
		return core.Bootstrap{}, err
	}
	torrents, err := s.countLocked(`SELECT COUNT(*) FROM torrent`)
	if err != nil {
		return core.Bootstrap{}, err
	}
	lastStart := s.lastStartLocked()

	return core.Bootstrap{
		Version:         core.VersionInfo{App: "0.1.0-dev", Database: "sqlite"},
		Auth:            core.AuthInfo{Enabled: true, Authenticated: authenticated},
		Counters:        core.Counters{Warnings: warnings, News: news, Torrents: torrents, WatchUsers: 0},
		LastStart:       lastStart,
		UpdateAvailable: false,
		Features: map[string][]string{
			"torrent_clients": {"transmission", "qbittorrent", "deluge"},
			"notifications":   {"email", "telegram", "pushover", "pushbullet", "pushall", "prowl"},
		},
	}, nil
}

func (s *SQLiteStore) getSettingsLocked() (core.Settings, error) {
	stmt, err := s.prepareLocked(`SELECT key, value FROM settings`)
	if err != nil {
		return core.Settings{}, err
	}
	defer C.sqlite3_finalize(stmt)

	values := map[string]string{}
	for {
		rc := C.sqlite3_step(stmt)
		switch rc {
		case C.SQLITE_ROW:
			values[colText(stmt, 0)] = colText(stmt, 1)
		case C.SQLITE_DONE:
			return settingsFromKV(values), nil
		default:
			return core.Settings{}, fmt.Errorf("get settings: %s", sqliteErr(s.db))
		}
	}
}

func (s *SQLiteStore) saveSettingsLocked(settings core.Settings) error {
	for k, v := range kvFromSettings(settings) {
		stmt, err := s.prepareLocked(`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`)
		if err != nil {
			return err
		}
		if err := bindAll(stmt, k, v); err != nil {
			C.sqlite3_finalize(stmt)
			return err
		}
		if err := stepDone(s.db, stmt, "save setting"); err != nil {
			C.sqlite3_finalize(stmt)
			return err
		}
		C.sqlite3_finalize(stmt)
	}
	return nil
}

func kvFromSettings(settings core.Settings) map[string]string {
	return map[string]string{
		"auth":                             boolString(settings.Auth),
		"admin_password_hash":              settings.AuthPasswordHash,
		"send":                             boolString(settings.Send),
		"send_warning":                     boolString(settings.SendWarning),
		"send_update":                      boolString(settings.SendUpdate),
		"telegram_enabled":                 boolString(settings.TelegramEnabled),
		"telegram_bot_token":               settings.TelegramBotToken,
		"telegram_chat_id":                 settings.TelegramChatID,
		"telegram_thread_id":               settings.TelegramThreadID,
		"telegram_silent":                  boolString(settings.TelegramSilent),
		"telegram_use_proxy":               boolString(settings.TelegramUseProxy),
		"proxy":                            boolString(settings.Proxy),
		"proxy_address":                    settings.ProxyAddress,
		"proxy_type":                       settings.ProxyType,
		"use_torrent":                      boolString(settings.UseTorrent),
		"torrent_client":                   settings.TorrentClient,
		"torrent_address":                  settings.TorrentAddress,
		"torrent_login":                    settings.TorrentLogin,
		"torrent_password":                 settings.TorrentPassword,
		"torrent_session_cookie":           settings.TorrentSessionCookie,
		"torrent_session_cookie_expires":   timePtrSetting(settings.TorrentSessionCookieExpires),
		"path_to_download":                 settings.PathToDownload,
		"delete_old_files":                 boolString(settings.DeleteOldFiles),
		"delete_distribution":              boolString(settings.DeleteDistribution),
		"server_address":                   settings.ServerAddress,
		"debug":                            boolString(settings.Debug),
		"rss":                              boolString(settings.RSS),
		"auto_update":                      boolString(settings.AutoUpdate),
		"http_timeout_seconds":             strconv.Itoa(settings.HTTPTimeoutSeconds),
		"monitor_interval_minutes":         strconv.Itoa(settings.MonitorIntervalMinutes),
		"post_update_script":               settings.PostUpdateScript,
		"browser_mode":                     core.NormalizeBrowserMode(settings.BrowserMode),
		"browser_binary":                   settings.BrowserBinary,
		"browser_profile":                  settings.BrowserProfile,
		"browser_connect_url":              settings.BrowserConnectURL,
		"template_source_url":              settings.TemplateSourceURL,
		"template_update_interval_minutes": strconv.Itoa(settings.TemplateUpdateIntervalMinutes),
		"template_directory":               settings.TemplateDirectory,
		"user_agent":                       settings.UserAgent,
	}
}

func settingsFromKV(values map[string]string) core.Settings {
	settings := core.DefaultSettings()
	if v, ok := values["auth"]; ok {
		settings.Auth = parseSettingBool(v)
	}
	if v, ok := values["admin_password_hash"]; ok {
		settings.AuthPasswordHash = v
	}
	settings.AuthPasswordSet = strings.TrimSpace(settings.AuthPasswordHash) != ""
	if v, ok := values["send"]; ok {
		settings.Send = parseSettingBool(v)
	}
	if v, ok := values["send_warning"]; ok {
		settings.SendWarning = parseSettingBool(v)
	}
	if v, ok := values["send_update"]; ok {
		settings.SendUpdate = parseSettingBool(v)
	}
	if v, ok := values["telegram_enabled"]; ok {
		settings.TelegramEnabled = parseSettingBool(v)
	}
	if v, ok := values["telegram_bot_token"]; ok {
		settings.TelegramBotToken = v
	}
	settings.TelegramBotTokenSet = strings.TrimSpace(settings.TelegramBotToken) != ""
	if v, ok := values["telegram_chat_id"]; ok {
		settings.TelegramChatID = v
	}
	if v, ok := values["telegram_thread_id"]; ok {
		settings.TelegramThreadID = v
	}
	if v, ok := values["telegram_silent"]; ok {
		settings.TelegramSilent = parseSettingBool(v)
	}
	if v, ok := values["telegram_use_proxy"]; ok {
		settings.TelegramUseProxy = parseSettingBool(v)
	}
	if v, ok := values["proxy"]; ok {
		settings.Proxy = parseSettingBool(v)
	}
	if v, ok := values["proxy_address"]; ok {
		settings.ProxyAddress = v
	}
	if v, ok := values["proxy_type"]; ok {
		settings.ProxyType = v
	}
	if v, ok := values["use_torrent"]; ok {
		settings.UseTorrent = parseSettingBool(v)
	}
	if v, ok := values["torrent_client"]; ok {
		settings.TorrentClient = v
	}
	if v, ok := values["torrent_address"]; ok {
		settings.TorrentAddress = v
	}
	if v, ok := values["torrent_login"]; ok {
		settings.TorrentLogin = v
	}
	if v, ok := values["torrent_password"]; ok {
		settings.TorrentPassword = v
	}
	if v, ok := values["torrent_session_cookie"]; ok {
		settings.TorrentSessionCookie = v
	}
	if v, ok := values["torrent_session_cookie_expires"]; ok {
		settings.TorrentSessionCookieExpires = parseTimePtr(v)
	}
	if v, ok := values["path_to_download"]; ok {
		settings.PathToDownload = v
	}
	if v, ok := values["delete_old_files"]; ok {
		settings.DeleteOldFiles = parseSettingBool(v)
	}
	if v, ok := values["delete_distribution"]; ok {
		settings.DeleteDistribution = parseSettingBool(v)
	}
	if v, ok := values["server_address"]; ok {
		settings.ServerAddress = v
	}
	if v, ok := values["debug"]; ok {
		settings.Debug = parseSettingBool(v)
	}
	if v, ok := values["rss"]; ok {
		settings.RSS = parseSettingBool(v)
	}
	if v, ok := values["auto_update"]; ok {
		settings.AutoUpdate = parseSettingBool(v)
	}
	if v, ok := values["http_timeout_seconds"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			settings.HTTPTimeoutSeconds = n
		}
	}
	if v, ok := values["monitor_interval_minutes"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			settings.MonitorIntervalMinutes = n
		}
	}
	if v, ok := values["post_update_script"]; ok {
		settings.PostUpdateScript = v
	}
	if v, ok := values["browser_mode"]; ok {
		settings.BrowserMode = core.NormalizeBrowserMode(v)
	}
	if v, ok := values["browser_binary"]; ok {
		settings.BrowserBinary = v
	}
	if v, ok := values["browser_profile"]; ok {
		settings.BrowserProfile = v
	}
	if v, ok := values["browser_connect_url"]; ok {
		settings.BrowserConnectURL = v
	}
	if v, ok := values["template_source_url"]; ok {
		settings.TemplateSourceURL = v
	}
	if v, ok := values["template_update_interval_minutes"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			settings.TemplateUpdateIntervalMinutes = n
		}
	}
	if v, ok := values["template_directory"]; ok {
		settings.TemplateDirectory = v
	}
	if v, ok := values["user_agent"]; ok {
		settings.UserAgent = v
	}
	return settings
}

func timePtrSetting(v *time.Time) string {
	if v == nil {
		return ""
	}
	return v.Format(time.RFC3339Nano)
}

func boolString(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func parseSettingBool(v string) bool {
	v = strings.TrimSpace(strings.ToLower(v))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func (s *SQLiteStore) createTorrentLocked(item core.TorrentItem) (core.TorrentItem, error) {
	stmt, err := s.prepareLocked(`INSERT INTO torrent (tracker, type, name, url, torrent_id, quality, path, episode, updated_at, auto_update, hash, paused, has_error, closed, update_title) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return core.TorrentItem{}, err
	}
	defer C.sqlite3_finalize(stmt)
	if err := bindAll(stmt,
		item.Tracker, string(item.Type), item.Name, item.URL, item.TorrentID, stringPtrToDB(item.Quality), item.Path, item.Episode, timePtrToDB(item.UpdatedAt),
		boolInt(item.AutoUpdate), item.Hash, boolInt(item.Paused), boolInt(item.HasError), boolInt(item.Closed), boolInt(item.UpdateTitle),
	); err != nil {
		return core.TorrentItem{}, err
	}
	if err := stepDone(s.db, stmt, "insert torrent"); err != nil {
		return core.TorrentItem{}, err
	}
	item.ID = int64(C.sqlite3_last_insert_rowid(s.db))
	return item, nil
}

func (s *SQLiteStore) getTorrentLocked(id int64) (core.TorrentItem, error) {
	stmt, err := s.prepareLocked(`SELECT id, tracker, type, name, url, torrent_id, quality, path, episode, updated_at, auto_update, hash, paused, has_error, closed, update_title FROM torrent WHERE id = ?`)
	if err != nil {
		return core.TorrentItem{}, err
	}
	defer C.sqlite3_finalize(stmt)
	if err := bindAll(stmt, id); err != nil {
		return core.TorrentItem{}, err
	}
	rc := C.sqlite3_step(stmt)
	if rc == C.SQLITE_ROW {
		return scanTorrent(stmt), nil
	}
	if rc == C.SQLITE_DONE {
		return core.TorrentItem{}, core.ErrNotFound
	}
	return core.TorrentItem{}, fmt.Errorf("get torrent: %s", sqliteErr(s.db))
}

func (s *SQLiteStore) getCredentialLocked(id int64) (core.Credential, error) {
	stmt, err := s.prepareLocked(`SELECT id, tracker, login, password, passkey, cookie, access_mode, use_proxy, type, necessarily FROM credentials WHERE id = ?`)
	if err != nil {
		return core.Credential{}, err
	}
	defer C.sqlite3_finalize(stmt)
	if err := bindAll(stmt, id); err != nil {
		return core.Credential{}, err
	}
	rc := C.sqlite3_step(stmt)
	if rc == C.SQLITE_ROW {
		return scanCredential(stmt), nil
	}
	if rc == C.SQLITE_DONE {
		return core.Credential{}, core.ErrNotFound
	}
	return core.Credential{}, fmt.Errorf("get credential: %s", sqliteErr(s.db))
}

func (s *SQLiteStore) insertCredentialLocked(c core.Credential) error {
	stmt, err := s.prepareLocked(`INSERT INTO credentials (tracker, login, password, passkey, cookie, access_mode, use_proxy, type, necessarily) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer C.sqlite3_finalize(stmt)
	if err := bindAll(stmt, c.Tracker, c.Login, c.Password, c.Passkey, c.Cookie, string(c.AccessMode), boolInt(c.UseProxy), string(c.Type), boolInt(c.Necessarily)); err != nil {
		return err
	}
	return stepDone(s.db, stmt, "insert credential")
}

func (s *SQLiteStore) insertWarningLocked(w core.Warning) error {
	stmt, err := s.prepareLocked(`INSERT INTO warning (time, where_name, reason, torrent_id, tracker, action_kind, action_label, action_url) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer C.sqlite3_finalize(stmt)
	if err := bindAll(stmt, w.Time.Format(time.RFC3339Nano), w.Where, w.Reason, int64PtrToDB(w.TorrentID), w.Tracker, w.ActionKind, w.ActionLabel, w.ActionURL); err != nil {
		return err
	}
	return stepDone(s.db, stmt, "insert warning")
}

func (s *SQLiteStore) insertNewsLocked(n core.News) error {
	stmt, err := s.prepareLocked(`INSERT INTO news (text, new) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer C.sqlite3_finalize(stmt)
	if err := bindAll(stmt, n.Text, boolInt(n.New)); err != nil {
		return err
	}
	return stepDone(s.db, stmt, "insert news")
}

func (s *SQLiteStore) setMetaLocked(key, value string) error {
	stmt, err := s.prepareLocked(`INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`)
	if err != nil {
		return err
	}
	defer C.sqlite3_finalize(stmt)
	if err := bindAll(stmt, key, value); err != nil {
		return err
	}
	return stepDone(s.db, stmt, "set meta")
}

func (s *SQLiteStore) lastStartLocked() *time.Time {
	stmt, err := s.prepareLocked(`SELECT value FROM meta WHERE key = 'last_start'`)
	if err != nil {
		return nil
	}
	defer C.sqlite3_finalize(stmt)
	if C.sqlite3_step(stmt) != C.SQLITE_ROW {
		return nil
	}
	return parseTimePtr(colText(stmt, 0))
}

func (s *SQLiteStore) countLocked(query string) (int, error) {
	stmt, err := s.prepareLocked(query)
	if err != nil {
		return 0, err
	}
	defer C.sqlite3_finalize(stmt)
	rc := C.sqlite3_step(stmt)
	if rc != C.SQLITE_ROW {
		return 0, fmt.Errorf("count query failed: %s", sqliteErr(s.db))
	}
	return int(C.sqlite3_column_int64(stmt, 0)), nil
}

func (s *SQLiteStore) execLocked(query string) error {
	cquery := C.CString(query)
	defer C.free(unsafe.Pointer(cquery))
	var errmsg *C.char
	if rc := C.sqlite3_exec(s.db, cquery, nil, nil, &errmsg); rc != C.SQLITE_OK {
		msg := sqliteErr(s.db)
		if errmsg != nil {
			msg = C.GoString(errmsg)
			C.sqlite3_free(unsafe.Pointer(errmsg))
		}
		return fmt.Errorf("sqlite exec failed: %s", msg)
	}
	return nil
}

func (s *SQLiteStore) prepareLocked(query string) (*C.sqlite3_stmt, error) {
	cquery := C.CString(query)
	defer C.free(unsafe.Pointer(cquery))
	var stmt *C.sqlite3_stmt
	if rc := C.sqlite3_prepare_v2(s.db, cquery, -1, &stmt, nil); rc != C.SQLITE_OK {
		return nil, fmt.Errorf("prepare sqlite query: %s", sqliteErr(s.db))
	}
	return stmt, nil
}

func scanTorrent(stmt *C.sqlite3_stmt) core.TorrentItem {
	item := core.TorrentItem{
		ID:          int64(C.sqlite3_column_int64(stmt, 0)),
		Tracker:     colText(stmt, 1),
		Type:        core.TrackerType(colText(stmt, 2)),
		Name:        colText(stmt, 3),
		URL:         colText(stmt, 4),
		TorrentID:   colText(stmt, 5),
		Quality:     colStringPtr(stmt, 6),
		Path:        colText(stmt, 7),
		Episode:     colText(stmt, 8),
		UpdatedAt:   parseTimePtr(colNullableText(stmt, 9)),
		AutoUpdate:  colBool(stmt, 10),
		Hash:        colText(stmt, 11),
		Paused:      colBool(stmt, 12),
		HasError:    colBool(stmt, 13),
		Closed:      colBool(stmt, 14),
		UpdateTitle: colBool(stmt, 15),
	}
	if item.URL == "" && item.TorrentID != "" {
		item.URL = core.BuildTrackerURL(item.Tracker, item.TorrentID)
	}
	return item
}

func scanCredential(stmt *C.sqlite3_stmt) core.Credential {
	return core.Credential{
		ID:          int64(C.sqlite3_column_int64(stmt, 0)),
		Tracker:     colText(stmt, 1),
		Login:       colText(stmt, 2),
		Password:    colText(stmt, 3),
		Passkey:     colText(stmt, 4),
		Cookie:      colText(stmt, 5),
		AccessMode:  core.NormalizeAccessMode(colText(stmt, 6)),
		UseProxy:    colBool(stmt, 7),
		Type:        core.TrackerType(colText(stmt, 8)),
		Necessarily: colBool(stmt, 9),
	}
}

func scanWarning(stmt *C.sqlite3_stmt) core.Warning {
	var tid *int64
	if C.sqlite3_column_type(stmt, 4) != C.SQLITE_NULL {
		v := int64(C.sqlite3_column_int64(stmt, 4))
		tid = &v
	}
	tm := time.Now()
	if parsed := parseTimePtr(colText(stmt, 1)); parsed != nil {
		tm = *parsed
	}
	return core.Warning{
		ID:          int64(C.sqlite3_column_int64(stmt, 0)),
		Time:        tm,
		Where:       colText(stmt, 2),
		Reason:      colText(stmt, 3),
		TorrentID:   tid,
		Tracker:     colText(stmt, 5),
		ActionKind:  colText(stmt, 6),
		ActionLabel: colText(stmt, 7),
		ActionURL:   colText(stmt, 8),
	}
}

func bindAll(stmt *C.sqlite3_stmt, values ...any) error {
	for i, v := range values {
		idx := C.int(i + 1)
		switch x := v.(type) {
		case nil:
			if rc := C.sqlite3_bind_null(stmt, idx); rc != C.SQLITE_OK {
				return fmt.Errorf("bind null at %d failed", i+1)
			}
		case dbNull:
			if rc := C.sqlite3_bind_null(stmt, idx); rc != C.SQLITE_OK {
				return fmt.Errorf("bind null at %d failed", i+1)
			}
		case string:
			cs := C.CString(x)
			if rc := C.bind_text_transient(stmt, idx, cs); rc != C.SQLITE_OK {
				C.free(unsafe.Pointer(cs))
				return fmt.Errorf("bind text at %d failed", i+1)
			}
			C.free(unsafe.Pointer(cs))
		case int:
			if rc := C.sqlite3_bind_int64(stmt, idx, C.sqlite3_int64(x)); rc != C.SQLITE_OK {
				return fmt.Errorf("bind int at %d failed", i+1)
			}
		case int64:
			if rc := C.sqlite3_bind_int64(stmt, idx, C.sqlite3_int64(x)); rc != C.SQLITE_OK {
				return fmt.Errorf("bind int64 at %d failed", i+1)
			}
		default:
			return fmt.Errorf("unsupported sqlite bind type %T at %d", v, i+1)
		}
	}
	return nil
}

func stepDone(db *C.sqlite3, stmt *C.sqlite3_stmt, label string) error {
	if rc := C.sqlite3_step(stmt); rc != C.SQLITE_DONE {
		return fmt.Errorf("%s: %s", label, sqliteErr(db))
	}
	return nil
}

func sqliteErr(db *C.sqlite3) string {
	if db == nil {
		return "unknown sqlite error"
	}
	return C.GoString(C.sqlite3_errmsg(db))
}

func colText(stmt *C.sqlite3_stmt, col int) string {
	return colNullableText(stmt, col)
}

func colNullableText(stmt *C.sqlite3_stmt, col int) string {
	if C.sqlite3_column_type(stmt, C.int(col)) == C.SQLITE_NULL {
		return ""
	}
	ptr := C.sqlite3_column_text(stmt, C.int(col))
	if ptr == nil {
		return ""
	}
	return C.GoString((*C.char)(unsafe.Pointer(ptr)))
}

func colStringPtr(stmt *C.sqlite3_stmt, col int) *string {
	if C.sqlite3_column_type(stmt, C.int(col)) == C.SQLITE_NULL {
		return nil
	}
	v := colText(stmt, col)
	return &v
}

func colBool(stmt *C.sqlite3_stmt, col int) bool {
	return C.sqlite3_column_int(stmt, C.int(col)) != 0
}

func parseTimePtr(raw string) *time.Time {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return &t
	}
	if t, err := time.Parse("2006-01-02 15:04:05", raw); err == nil {
		return &t
	}
	return nil
}

type dbNull struct{}

func stringPtrToDB(v *string) any {
	if v == nil {
		return dbNull{}
	}
	return *v
}

func timePtrToDB(v *time.Time) any {
	if v == nil {
		return dbNull{}
	}
	return v.Format(time.RFC3339Nano)
}

func int64PtrToDB(v *int64) any {
	if v == nil {
		return dbNull{}
	}
	return *v
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
