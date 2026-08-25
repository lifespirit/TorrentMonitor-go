package web

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"torrentmonitor-go/internal/browserbroker"
	"torrentmonitor-go/internal/core"
	"torrentmonitor-go/internal/scheduler"
	"torrentmonitor-go/ui"
)

type Config struct{}

type Core interface {
	Bootstrap(ctx context.Context, authenticated bool) (core.Bootstrap, error)
	ListTorrents(ctx context.Context, sortBy, dir, filter string) ([]core.TorrentItem, error)
	GetTorrent(ctx context.Context, id int64) (core.TorrentItem, error)
	AddTorrent(ctx context.Context, req core.AddTorrentRequest) (core.TorrentItem, error)
	UpdateTorrent(ctx context.Context, id int64, patch core.UpdateTorrentRequest) (core.TorrentItem, error)
	DeleteTorrent(ctx context.Context, id int64) error
	ListWarnings(ctx context.Context) ([]core.Warning, error)
	ClearWarnings(ctx context.Context, tracker string) (int, error)
	ListNews(ctx context.Context) ([]core.News, error)
	MarkNewsRead(ctx context.Context, id int64) error
	ListCredentials(ctx context.Context) ([]core.Credential, error)
	UpdateCredential(ctx context.Context, id int64, patch core.UpdateCredentialRequest) (core.Credential, error)
	CheckCredentialLogin(ctx context.Context, id int64, req core.CredentialLoginCheckRequest) (core.CredentialLoginCheckResult, error)
	GetSettings(ctx context.Context) (core.Settings, error)
	UpdateSettings(ctx context.Context, patch core.UpdateSettingsRequest) (core.Settings, error)
	CheckTorrentClient(ctx context.Context, req core.TorrentClientCheckRequest) (core.TorrentClientCheckResult, error)
	AddTorrentToClient(ctx context.Context, id int64, req core.TorrentAddToClientRequest) (core.TorrentAddToClientResult, error)
	CheckTorrent(ctx context.Context, id int64) (core.TorrentCheckResult, error)
	UpdateTemplates(ctx context.Context) (core.TemplateUpdateResult, error)
	TemplatesStatus(ctx context.Context) (core.TemplatesStatus, error)
	TestNotification(ctx context.Context, req core.NotificationTestRequest) (core.NotificationTestResult, error)
}

type Server struct {
	cfg       Config
	core      Core
	scheduler *scheduler.Scheduler
	browser   *browserbroker.Broker
	logger    *slog.Logger
	mux       *http.ServeMux
	sessionMu sync.Mutex
	sessions  map[string]authSession
}

func NewServer(cfg Config, core Core, scheduler *scheduler.Scheduler, logger *slog.Logger, browser *browserbroker.Broker) *Server {
	s := &Server{cfg: cfg, core: core, scheduler: scheduler, browser: browser, logger: logger, mux: http.NewServeMux(), sessions: map[string]authSession{}}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.authMiddleware(s.mux)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /{$}", s.index)
	s.mux.HandleFunc("GET /assets/", s.assets)
	s.mux.HandleFunc("GET /browser/session/", s.browserSessionPage)
	s.mux.HandleFunc("GET /api/v1/browser/sessions", s.apiBrowserSessions)
	s.mux.HandleFunc("POST /api/v1/browser/sessions", s.apiBrowserSessions)
	s.mux.HandleFunc("GET /api/v1/browser/sessions/", s.apiBrowserSessionByID)
	s.mux.HandleFunc("POST /api/v1/browser/sessions/", s.apiBrowserSessionByID)
	s.mux.HandleFunc("DELETE /api/v1/browser/sessions/", s.apiBrowserSessionByID)
	s.mux.HandleFunc("GET /api/v1/auth/status", s.apiAuthStatus)
	s.mux.HandleFunc("POST /api/v1/auth/login", s.apiAuthLogin)
	s.mux.HandleFunc("POST /api/v1/auth/logout", s.apiAuthLogout)

	s.mux.HandleFunc("GET /api/v1/bootstrap", s.apiBootstrap)
	s.mux.HandleFunc("GET /api/v1/torrents", s.apiListTorrents)
	s.mux.HandleFunc("POST /api/v1/torrents", s.apiCreateTorrent)
	s.mux.HandleFunc("GET /api/v1/torrents/", s.apiTorrentByID)
	s.mux.HandleFunc("POST /api/v1/torrents/", s.apiTorrentByID)
	s.mux.HandleFunc("PATCH /api/v1/torrents/", s.apiTorrentByID)
	s.mux.HandleFunc("DELETE /api/v1/torrents/", s.apiTorrentByID)

	s.mux.HandleFunc("GET /api/v1/warnings", s.apiListWarnings)
	s.mux.HandleFunc("DELETE /api/v1/warnings", s.apiClearWarnings)
	s.mux.HandleFunc("GET /api/v1/news", s.apiListNews)
	s.mux.HandleFunc("PATCH /api/v1/news/", s.apiNewsByID)
	s.mux.HandleFunc("GET /api/v1/credentials", s.apiListCredentials)
	s.mux.HandleFunc("PATCH /api/v1/credentials/", s.apiCredentialByID)
	s.mux.HandleFunc("POST /api/v1/credentials/", s.apiCredentialByID)
	s.mux.HandleFunc("GET /api/v1/settings", s.apiGetSettings)
	s.mux.HandleFunc("PATCH /api/v1/settings", s.apiUpdateSettings)
	s.mux.HandleFunc("POST /api/v1/torrent-client/check", s.apiTorrentClientCheck)
	s.mux.HandleFunc("POST /api/v1/notifications/test", s.apiNotificationTest)
	s.mux.HandleFunc("GET /api/v1/templates", s.apiTemplatesStatus)
	s.mux.HandleFunc("POST /api/v1/templates/update", s.apiTemplatesUpdate)
	// Compatibility alias for earlier scaffold builds. It now performs a non-mutating connection check.
	s.mux.HandleFunc("POST /api/v1/torrent-client/test", s.apiTorrentClientCheck)
	s.mux.HandleFunc("GET /api/v1/scheduler/status", s.apiSchedulerStatus)
	s.mux.HandleFunc("POST /api/v1/scheduler/run", s.apiSchedulerRun)

	// Legacy compatibility. New UI does not use these, but old clients can keep working while migrating.
	s.mux.HandleFunc("GET /action.php", s.legacyAction)
	s.mux.HandleFunc("POST /action.php", s.legacyAction)
	s.mux.HandleFunc("GET /include/", s.legacyIncludeGone)
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	b, err := ui.FS.ReadFile("index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

func (s *Server) assets(w http.ResponseWriter, r *http.Request) {
	http.FileServer(http.FS(ui.FS)).ServeHTTP(w, r)
}

func (s *Server) apiBootstrap(w http.ResponseWriter, r *http.Request) {
	b, err := s.core.Bootstrap(r.Context(), s.isAuthenticated(r) || authenticatedFromContext(r.Context()))
	writeJSON(w, b, err)
}

func (s *Server) apiListTorrents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, err := s.core.ListTorrents(r.Context(), q.Get("sort"), q.Get("dir"), q.Get("filter"))
	writeJSON(w, map[string]any{"items": items}, err)
}

func (s *Server) apiCreateTorrent(w http.ResponseWriter, r *http.Request) {
	var req core.AddTorrentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	item, err := s.core.AddTorrent(r.Context(), req)
	if err != nil {
		if errors.Is(err, core.ErrTorrentExists) {
			writeError(w, http.StatusConflict, "Тема уже существует.")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.scheduler.QueueTorrentCheck(item.ID)
	writeJSONStatus(w, http.StatusCreated, item)
}

func (s *Server) apiTorrentByID(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r.URL.Path, "/api/v1/torrents/")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if r.Method == http.MethodPost && strings.HasSuffix(strings.TrimRight(r.URL.Path, "/"), "/check") {
		result, err := s.core.CheckTorrent(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, result, nil)
		return
	}
	if r.Method == http.MethodPost && strings.HasSuffix(strings.TrimRight(r.URL.Path, "/"), "/add-to-client") {
		var req core.TorrentAddToClientRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		result, err := s.core.AddTorrentToClient(r.Context(), id, req)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, result, nil)
		return
	}
	switch r.Method {
	case http.MethodGet:
		item, err := s.core.GetTorrent(r.Context(), id)
		writeJSON(w, item, err)
	case http.MethodPatch:
		var patch core.UpdateTorrentRequest
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		item, err := s.core.UpdateTorrent(r.Context(), id, patch)
		writeJSON(w, item, err)
	case http.MethodDelete:
		err := s.core.DeleteTorrent(r.Context(), id)
		writeJSON(w, map[string]any{"ok": err == nil, "message": "Тема удалена."}, err)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) apiListWarnings(w http.ResponseWriter, r *http.Request) {
	warnings, err := s.core.ListWarnings(r.Context())
	writeJSON(w, map[string]any{"items": warnings}, err)
}

func (s *Server) apiClearWarnings(w http.ResponseWriter, r *http.Request) {
	tracker := r.URL.Query().Get("tracker")
	count, err := s.core.ClearWarnings(r.Context(), tracker)
	writeJSON(w, map[string]any{"ok": err == nil, "deleted": count}, err)
}

func (s *Server) apiListNews(w http.ResponseWriter, r *http.Request) {
	news, err := s.core.ListNews(r.Context())
	writeJSON(w, map[string]any{"items": news}, err)
}

func (s *Server) apiNewsByID(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r.URL.Path, "/api/v1/news/")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !strings.HasSuffix(r.URL.Path, "/read") {
		writeError(w, http.StatusNotFound, "unknown news action")
		return
	}
	err = s.core.MarkNewsRead(r.Context(), id)
	writeJSON(w, map[string]any{"ok": err == nil}, err)
}

func (s *Server) apiListCredentials(w http.ResponseWriter, r *http.Request) {
	credentials, err := s.core.ListCredentials(r.Context())
	writeJSON(w, map[string]any{"items": credentials}, err)
}

func (s *Server) apiCredentialByID(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r.URL.Path, "/api/v1/credentials/")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if r.Method == http.MethodPost && strings.HasSuffix(strings.TrimRight(r.URL.Path, "/"), "/check-login") {
		var req core.CredentialLoginCheckRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		result, err := s.core.CheckCredentialLogin(r.Context(), id, req)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, result, nil)
		return
	}
	if r.Method != http.MethodPatch {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var patch core.UpdateCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cred, err := s.core.UpdateCredential(r.Context(), id, patch)
	writeJSON(w, cred, err)
}

func (s *Server) apiGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.core.GetSettings(r.Context())
	writeJSON(w, settings, err)
}

func (s *Server) apiUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var patch core.UpdateSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	settings, err := s.core.UpdateSettings(r.Context(), patch)
	if err == nil {
		if settings.MonitorIntervalMinutes > 0 {
			s.scheduler.SetMonitorInterval(time.Duration(settings.MonitorIntervalMinutes) * time.Minute)
		}
		s.scheduler.SetTemplateInterval(time.Duration(settings.TemplateUpdateIntervalMinutes) * time.Minute)
	}
	writeJSON(w, settings, err)
}

func (s *Server) apiTemplatesStatus(w http.ResponseWriter, r *http.Request) {
	result, err := s.core.TemplatesStatus(r.Context())
	writeJSON(w, result, err)
}

func (s *Server) apiTemplatesUpdate(w http.ResponseWriter, r *http.Request) {
	result, err := s.core.UpdateTemplates(r.Context())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, result, nil)
}

func (s *Server) apiTorrentClientCheck(w http.ResponseWriter, r *http.Request) {
	var req core.TorrentClientCheckRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	result, err := s.core.CheckTorrentClient(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, result, nil)
}

func (s *Server) apiNotificationTest(w http.ResponseWriter, r *http.Request) {
	var req core.NotificationTestRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	result, err := s.core.TestNotification(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, result, nil)
}

func (s *Server) apiSchedulerStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.scheduler.Status(), nil)
}

func (s *Server) apiSchedulerRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Job string `json:"job"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Job == "" {
		req.Job = "monitor"
	}
	go func() {
		if err := s.scheduler.RunNow(context.Background(), req.Job); err != nil {
			s.logger.Error("manual scheduler run failed", "job", req.Job, "error", err)
		}
	}()
	writeJSON(w, map[string]any{"ok": true, "message": "Задание запущено.", "job": req.Job}, nil)
}

func idFromPath(path, prefix string) (int64, error) {
	raw := strings.TrimPrefix(path, prefix)
	raw = strings.Trim(raw, "/")
	raw = strings.TrimSuffix(raw, "/read")
	raw = strings.TrimSuffix(raw, "/add-to-client")
	raw = strings.TrimSuffix(raw, "/check")
	raw = strings.TrimSuffix(raw, "/check-login")
	if raw == "" {
		return 0, errors.New("missing id")
	}
	return strconv.ParseInt(raw, 10, 64)
}

func writeJSON(w http.ResponseWriter, v any, err error) {
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONStatus(w, http.StatusOK, v)
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSONStatus(w, status, map[string]any{"error": true, "message": msg})
}
