package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"torrentmonitor-go/internal/browserbroker"
	"torrentmonitor-go/internal/notify"
	"torrentmonitor-go/internal/sitetpl"
	"torrentmonitor-go/internal/torrentclient"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrTorrentExists = errors.New("тема уже существует")
)

type Repository interface {
	ListTorrents(ctx context.Context, sortBy, dir, filter string) ([]TorrentItem, error)
	GetTorrent(ctx context.Context, id int64) (TorrentItem, error)
	CreateTorrent(ctx context.Context, item TorrentItem) (TorrentItem, error)
	UpdateTorrent(ctx context.Context, id int64, patch UpdateTorrentRequest) (TorrentItem, error)
	SetTorrentClientHash(ctx context.Context, id int64, hash string) (TorrentItem, error)
	DeleteTorrent(ctx context.Context, id int64) error

	ListWarnings(ctx context.Context) ([]Warning, error)
	CreateWarning(ctx context.Context, warning Warning) (Warning, error)
	ClearWarnings(ctx context.Context, tracker string) (int, error)
	ListNews(ctx context.Context) ([]News, error)
	MarkNewsRead(ctx context.Context, id int64) error
	ListCredentials(ctx context.Context) ([]Credential, error)
	EnsureCredentials(ctx context.Context, credentials []Credential) error
	UpdateCredential(ctx context.Context, id int64, patch UpdateCredentialRequest) (Credential, error)
	GetSettings(ctx context.Context) (Settings, error)
	UpdateSettings(ctx context.Context, patch UpdateSettingsRequest) (Settings, error)
	SaveTorrentClientSession(ctx context.Context, cookie string, expires *time.Time) error
	Bootstrap(ctx context.Context, authenticated bool) (Bootstrap, error)
}

type Service struct {
	repo    Repository
	logger  *slog.Logger
	runner  *sitetpl.Runner
	browser *browserbroker.Broker
	addMu   sync.Mutex
}

func NewService(repo Repository, logger *slog.Logger) *Service {
	return &Service{repo: repo, logger: logger, runner: sitetpl.NewRunner()}
}

func NewServiceWithRunner(repo Repository, logger *slog.Logger, runner *sitetpl.Runner) *Service {
	if runner == nil {
		runner = sitetpl.NewRunner()
	}
	return &Service{repo: repo, logger: logger, runner: runner}
}

func NewServiceWithBrowser(repo Repository, logger *slog.Logger, runner *sitetpl.Runner, browser *browserbroker.Broker) *Service {
	if runner == nil {
		runner = sitetpl.NewRunner()
	}
	return &Service{repo: repo, logger: logger, runner: runner, browser: browser}
}

func (s *Service) Bootstrap(ctx context.Context, authenticated bool) (Bootstrap, error) {
	b, err := s.repo.Bootstrap(ctx, authenticated)
	if err != nil {
		return b, err
	}
	settings, settingsErr := s.repo.GetSettings(ctx)
	if settingsErr == nil {
		passwordSet := strings.TrimSpace(settings.AuthPasswordHash) != ""
		b.Auth.Enabled = settings.Auth && passwordSet
		b.Auth.PasswordSet = passwordSet
		b.Auth.Authenticated = authenticated || !b.Auth.Enabled
	}
	return b, nil
}

func (s *Service) ListTorrents(ctx context.Context, sortBy, dir, filter string) ([]TorrentItem, error) {
	return s.repo.ListTorrents(ctx, sortBy, dir, filter)
}

func (s *Service) GetTorrent(ctx context.Context, id int64) (TorrentItem, error) {
	return s.repo.GetTorrent(ctx, id)
}

func (s *Service) AddTorrent(ctx context.Context, req AddTorrentRequest) (TorrentItem, error) {
	s.addMu.Lock()
	defer s.addMu.Unlock()

	if req.Kind == "" {
		req.Kind = TorrentKindTheme
	}

	item := TorrentItem{
		Name:        strings.TrimSpace(req.Name),
		Path:        strings.TrimSpace(req.Path),
		AutoUpdate:  req.UpdateHeader,
		UpdateTitle: req.UpdateHeader,
	}

	switch req.Kind {
	case TorrentKindTheme:
		tracker, tid, err := parseTrackerURL(req.URL)
		if err != nil {
			return TorrentItem{}, err
		}
		item.Tracker = normalizeTracker(tracker)
		item.Type = TrackerTypeForum
		item.TorrentID = tid
		item.URL = BuildTrackerURL(item.Tracker, tid)
		if item.Name == "" {
			item.Name = item.URL
		}
	case TorrentKindSeries:
		if strings.TrimSpace(req.Tracker) == "" || strings.TrimSpace(req.Name) == "" {
			return TorrentItem{}, errors.New("tracker and name are required")
		}
		item.Tracker = strings.TrimSpace(strings.ToLower(req.Tracker))
		item.Type = TrackerTypeRSS
		item.Name = strings.TrimSpace(req.Name)
		item.Quality = qualityFor(item.Tracker, req.HD)
	default:
		return TorrentItem{}, fmt.Errorf("unsupported torrent kind: %s", req.Kind)
	}
	if item.Type == TrackerTypeForum {
		items, err := s.repo.ListTorrents(ctx, "name", "asc", "")
		if err != nil {
			return TorrentItem{}, err
		}
		for _, existing := range items {
			if existing.Type == TrackerTypeForum && strings.EqualFold(existing.Tracker, item.Tracker) && existing.TorrentID == item.TorrentID {
				return existing, ErrTorrentExists
			}
		}
	}

	created, err := s.repo.CreateTorrent(ctx, item)
	if err == nil {
		s.logger.Info("torrent added", "id", created.ID, "tracker", created.Tracker, "name", created.Name)
	}
	return created, err
}

func (s *Service) UpdateTorrent(ctx context.Context, id int64, patch UpdateTorrentRequest) (TorrentItem, error) {
	return s.repo.UpdateTorrent(ctx, id, patch)
}

func (s *Service) DeleteTorrent(ctx context.Context, id int64) error {
	return s.repo.DeleteTorrent(ctx, id)
}

func (s *Service) ListWarnings(ctx context.Context) ([]Warning, error) {
	return s.repo.ListWarnings(ctx)
}

func (s *Service) CreateWarning(ctx context.Context, warning Warning) (Warning, error) {
	return s.repo.CreateWarning(ctx, warning)
}

func (s *Service) ClearWarnings(ctx context.Context, tracker string) (int, error) {
	return s.repo.ClearWarnings(ctx, tracker)
}

func (s *Service) ListNews(ctx context.Context) ([]News, error) {
	return s.repo.ListNews(ctx)
}

func (s *Service) MarkNewsRead(ctx context.Context, id int64) error {
	return s.repo.MarkNewsRead(ctx, id)
}

func (s *Service) ListCredentials(ctx context.Context) ([]Credential, error) {
	return s.listTemplateCredentials(ctx)
}

func (s *Service) listTemplateCredentials(ctx context.Context) ([]Credential, error) {
	defaults := s.templateCredentialDefaults()
	if len(defaults) > 0 {
		if err := s.repo.EnsureCredentials(ctx, defaults); err != nil {
			return nil, err
		}
	}
	stored, err := s.repo.ListCredentials(ctx)
	if err != nil {
		return nil, err
	}
	if len(defaults) == 0 {
		return stored, nil
	}
	allowed := map[string]Credential{}
	order := make([]string, 0, len(defaults))
	for _, c := range defaults {
		tracker := strings.ToLower(strings.TrimSpace(c.Tracker))
		if tracker == "" {
			continue
		}
		allowed[tracker] = c
		order = append(order, tracker)
	}
	byTracker := map[string]Credential{}
	for _, c := range stored {
		tracker := strings.ToLower(strings.TrimSpace(c.Tracker))
		if _, ok := allowed[tracker]; !ok {
			continue
		}
		if c.Type == "" {
			c.Type = allowed[tracker].Type
		}
		if c.AccessMode == "" {
			c.AccessMode = allowed[tracker].AccessMode
		}
		c.Necessarily = allowed[tracker].Necessarily
		byTracker[tracker] = c
	}
	out := make([]Credential, 0, len(order))
	for _, tracker := range order {
		if c, ok := byTracker[tracker]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *Service) templateCredentialDefaults() []Credential {
	if s == nil || s.runner == nil {
		return nil
	}
	infos := s.runner.ListTemplates()
	out := make([]Credential, 0, len(infos))
	for _, info := range infos {
		tracker := strings.ToLower(strings.TrimSpace(info.ID))
		if tracker == "" {
			continue
		}
		typeValue := TrackerTypeForum
		if strings.EqualFold(info.Kind, "rss") || strings.EqualFold(info.Kind, "series") || strings.EqualFold(info.Kind, "series_search") {
			typeValue = TrackerTypeRSS
		}
		mode := NormalizeAccessMode(info.DefaultAccessMode)
		out = append(out, Credential{
			Tracker:     tracker,
			Type:        typeValue,
			AccessMode:  mode,
			Necessarily: true,
		})
	}
	return out
}

func (s *Service) UpdateCredential(ctx context.Context, id int64, patch UpdateCredentialRequest) (Credential, error) {
	return s.repo.UpdateCredential(ctx, id, patch)
}

func (s *Service) CheckCredentialLogin(ctx context.Context, id int64, req CredentialLoginCheckRequest) (CredentialLoginCheckResult, error) {
	credentials, err := s.ListCredentials(ctx)
	if err != nil {
		return CredentialLoginCheckResult{}, err
	}
	var cred Credential
	for _, c := range credentials {
		if c.ID == id {
			cred = c
			break
		}
	}
	if cred.ID == 0 {
		return CredentialLoginCheckResult{}, ErrNotFound
	}
	settings, err := s.repo.GetSettings(ctx)
	if err != nil {
		return CredentialLoginCheckResult{}, err
	}
	mode := cred.AccessMode
	if strings.TrimSpace(req.AccessMode) != "" {
		mode = NormalizeAccessMode(req.AccessMode)
	}
	if mode == "" {
		mode = AccessModeNative
	}
	loginReq := sitetpl.LoginCheckRequest{
		Tracker: cred.Tracker,
		Credential: sitetpl.Credential{
			Login: cred.Login, Password: cred.Password, Passkey: cred.Passkey, Cookie: cred.Cookie, AccessMode: string(mode), UseProxy: cred.UseProxy,
		},
		Settings: sitetpl.Settings{
			UserAgent:    settings.UserAgent,
			Timeout:      time.Duration(settings.HTTPTimeoutSeconds) * time.Second,
			ProxyEnabled: settings.Proxy,
			ProxyType:    settings.ProxyType,
			ProxyAddress: settings.ProxyAddress,
		},
		OpenBrowser: req.OpenBrowser,
		Browser:     s.browser,
	}
	if mode == AccessModeChromium && req.OpenBrowser && s.browser != nil {
		target, err := s.runner.BrowserLoginTarget(cred.Tracker)
		if err != nil {
			return CredentialLoginCheckResult{}, err
		}
		br, err := s.browser.Open(ctx, browserbroker.OpenRequest{Tracker: cred.Tracker, URL: target.LoginURL, ProfilePath: target.ProfilePath})
		if err != nil {
			return CredentialLoginCheckResult{}, err
		}
		return CredentialLoginCheckResult{
			OK:               false,
			Tracker:          cred.Tracker,
			Mode:             mode,
			Message:          "Открыта серверная Chromium-сессия. Пройди Cloudflare/CAPTCHA/login в окне TorrentMonitor, затем нажми «Готово» и повтори проверку.",
			LoginURL:         target.LoginURL,
			ProfilePath:      br.ProfilePath,
			BrowserSessionID: br.ID,
			ViewerURL:        br.ViewerURL,
		}, nil
	}
	result, err := s.runner.CheckLogin(ctx, loginReq)
	if err != nil {
		s.recordWarning(ctx, Warning{
			Where:   cred.Tracker,
			Tracker: cred.Tracker,
			Reason:  "Ошибка проверки логина: " + err.Error(),
		})
		return CredentialLoginCheckResult{}, err
	}
	if result.OK {
		if _, clearErr := s.repo.ClearWarnings(ctx, cred.Tracker); clearErr != nil {
			s.logger.Warn("failed to clear tracker warnings after successful login check", "tracker", cred.Tracker, "error", clearErr)
		}
	}
	if mode == AccessModeNative && result.SessionCookie != "" && result.SessionCookie != cred.Cookie {
		cookie := result.SessionCookie
		if _, saveErr := s.repo.UpdateCredential(ctx, cred.ID, UpdateCredentialRequest{Cookie: &cookie}); saveErr != nil {
			s.logger.Warn("failed to persist native tracker session", "tracker", cred.Tracker, "error", saveErr)
		} else {
			result.Message = strings.TrimSpace(result.Message + " Сессия сохранена.")
		}
	}
	return CredentialLoginCheckResult{
		OK:           result.OK,
		Tracker:      cred.Tracker,
		Mode:         mode,
		Message:      result.Message,
		LoginURL:     result.LoginURL,
		ProfilePath:  result.ProfilePath,
		SessionSaved: mode == AccessModeNative && result.SessionCookie != "",
	}, nil
}

func (s *Service) GetSettings(ctx context.Context) (Settings, error) {
	return s.repo.GetSettings(ctx)
}

func (s *Service) UpdateSettings(ctx context.Context, patch UpdateSettingsRequest) (Settings, error) {
	oldSettings, _ := s.repo.GetSettings(ctx)
	settings, err := s.repo.UpdateSettings(ctx, patch)
	if err != nil {
		return Settings{}, err
	}
	if s.browser != nil {
		s.browser.UpdateConfig(BrowserConfigFromSettings(settings))
	}
	if strings.TrimSpace(oldSettings.TemplateDirectory) != strings.TrimSpace(settings.TemplateDirectory) {
		if _, loadErr := s.ReloadTemplatesFromSettings(ctx); loadErr != nil {
			s.logger.Warn("failed to reload templates after settings change", "error", loadErr)
		}
	}
	return settings, nil
}

func (s *Service) AddTorrentToClient(ctx context.Context, id int64, req TorrentAddToClientRequest) (TorrentAddToClientResult, error) {
	item, err := s.repo.GetTorrent(ctx, id)
	if err != nil {
		return TorrentAddToClientResult{}, err
	}
	settings, err := s.repo.GetSettings(ctx)
	if err != nil {
		return TorrentAddToClientResult{}, err
	}
	client, err := torrentclient.NewFromConfig(torrentclient.Config{
		Kind:           settings.TorrentClient,
		Address:        settings.TorrentAddress,
		Login:          settings.TorrentLogin,
		Password:       settings.TorrentPassword,
		Timeout:        time.Duration(settings.HTTPTimeoutSeconds) * time.Second,
		SessionCookie:  settings.TorrentSessionCookie,
		SessionExpires: settings.TorrentSessionCookieExpires,
	})
	if err != nil {
		return TorrentAddToClientResult{}, err
	}

	torrentURL := strings.TrimSpace(req.URL)
	if torrentURL == "" {
		return TorrentAddToClientResult{}, errors.New("torrent URL is required until site runner is connected")
	}
	savePath := strings.TrimSpace(req.SavePath)
	if savePath == "" {
		savePath = item.Path
	}
	if savePath == "" {
		savePath = settings.PathToDownload
	}
	deleteFiles := settings.DeleteOldFiles
	if req.DeleteFiles != nil {
		deleteFiles = *req.DeleteFiles
	}
	oldHash := ""
	if settings.DeleteDistribution {
		oldHash = strings.TrimSpace(item.Hash)
	}

	result, err := client.Add(ctx, torrentclient.AddRequest{
		ID:          item.ID,
		FileURL:     torrentURL,
		SavePath:    savePath,
		OldHash:     oldHash,
		Tracker:     item.Tracker,
		DeleteFiles: deleteFiles,
	})
	if sp, ok := client.(torrentclient.SessionProvider); ok {
		sess := sp.Session()
		if sess.Cookie != "" {
			if saveErr := s.repo.SaveTorrentClientSession(ctx, sess.Cookie, sess.Expires); saveErr != nil {
				s.logger.Warn("failed to persist torrent client session", "error", saveErr)
			}
		}
	}
	if err != nil {
		return TorrentAddToClientResult{}, err
	}

	if result.Hash != "" {
		item, err = s.repo.SetTorrentClientHash(ctx, item.ID, result.Hash)
		if err != nil {
			return TorrentAddToClientResult{}, err
		}
	}
	s.logger.Info("torrent item sent to client", "id", item.ID, "tracker", item.Tracker, "hash", result.Hash)
	return TorrentAddToClientResult{
		OK:      true,
		Client:  settings.TorrentClient,
		Hash:    result.Hash,
		Message: "Раздача отправлена в torrent-клиент, hash сохранён в теме.",
		Item:    item,
	}, nil
}

func (s *Service) ReloadTemplatesFromSettings(ctx context.Context) (TemplateUpdateResult, error) {
	settings, err := s.repo.GetSettings(ctx)
	if err != nil {
		return TemplateUpdateResult{}, err
	}
	return s.reloadTemplatesFromDirectory(settings.TemplateDirectory)
}

func (s *Service) RunTemplateUpdate(ctx context.Context) error {
	_, err := s.UpdateTemplates(ctx)
	return err
}

func (s *Service) TemplatesStatus(ctx context.Context) (TemplatesStatus, error) {
	settings, err := s.repo.GetSettings(ctx)
	if err != nil {
		return TemplatesStatus{}, err
	}
	infos := s.runner.ListTemplates()
	out := TemplatesStatus{
		SourceURL:             settings.TemplateSourceURL,
		Directory:             settings.TemplateDirectory,
		UpdateIntervalMinutes: settings.TemplateUpdateIntervalMinutes,
		Loaded:                len(infos),
		Templates:             make([]TrackerTemplateInfo, 0, len(infos)),
	}
	for _, info := range infos {
		out.Templates = append(out.Templates, TrackerTemplateInfo{
			ID:                info.ID,
			Name:              info.Name,
			Domains:           append([]string(nil), info.Domains...),
			Kind:              info.Kind,
			DefaultAccessMode: info.DefaultAccessMode,
			Source:            info.Source,
		})
	}
	return out, nil
}

func (s *Service) UpdateTemplates(ctx context.Context) (TemplateUpdateResult, error) {
	settings, err := s.repo.GetSettings(ctx)
	if err != nil {
		return TemplateUpdateResult{}, err
	}
	res, err := sitetpl.UpdateTemplatesFromSource(ctx, settings.TemplateSourceURL, settings.TemplateDirectory, &http.Client{Timeout: time.Duration(settings.HTTPTimeoutSeconds) * time.Second})
	if err != nil {
		s.recordWarning(ctx, Warning{Where: "templates", Reason: "Ошибка обновления шаблонов: " + err.Error()})
		return TemplateUpdateResult{}, err
	}
	if !res.Skipped {
		if _, err := s.reloadTemplatesFromDirectory(settings.TemplateDirectory); err != nil {
			s.recordWarning(ctx, Warning{Where: "templates", Reason: "Ошибка загрузки шаблонов: " + err.Error()})
			return TemplateUpdateResult{}, err
		}
	}
	return templateUpdateResultFromSiteTPL(res), nil
}

func (s *Service) reloadTemplatesFromDirectory(dir string) (TemplateUpdateResult, error) {
	reg, loaded, err := sitetpl.LoadRegistryFromDirectory(dir)
	if err != nil {
		return TemplateUpdateResult{}, err
	}
	s.runner.SetRegistry(reg)
	return TemplateUpdateResult{Directory: loaded.Directory, Loaded: loaded.Loaded, Files: loaded.Files, Message: fmt.Sprintf("Загружено шаблонов: %d.", loaded.Loaded)}, nil
}

func templateUpdateResultFromSiteTPL(res sitetpl.UpdateTemplatesResult) TemplateUpdateResult {
	return TemplateUpdateResult{SourceURL: res.SourceURL, Directory: res.Directory, UpdatedAt: res.UpdatedAt, Loaded: res.Loaded, Files: res.Files, Skipped: res.Skipped, Message: res.Message}
}

func (s *Service) CheckTorrentClient(ctx context.Context, req TorrentClientCheckRequest) (TorrentClientCheckResult, error) {
	settings, err := s.repo.GetSettings(ctx)
	if err != nil {
		return TorrentClientCheckResult{}, err
	}

	clientKind := settings.TorrentClient
	if req.TorrentClient != nil {
		clientKind = *req.TorrentClient
	}
	address := settings.TorrentAddress
	if req.TorrentAddress != nil {
		address = *req.TorrentAddress
	}
	login := settings.TorrentLogin
	if req.TorrentLogin != nil {
		login = *req.TorrentLogin
	}
	password := settings.TorrentPassword
	if req.TorrentPassword != nil {
		password = *req.TorrentPassword
	}
	timeoutSeconds := settings.HTTPTimeoutSeconds
	if req.HTTPTimeoutSeconds != nil {
		timeoutSeconds = *req.HTTPTimeoutSeconds
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = 15
	}

	client, err := torrentclient.NewFromConfig(torrentclient.Config{
		Kind:           clientKind,
		Address:        address,
		Login:          login,
		Password:       password,
		Timeout:        time.Duration(timeoutSeconds) * time.Second,
		SessionCookie:  settings.TorrentSessionCookie,
		SessionExpires: settings.TorrentSessionCookieExpires,
	})
	if err != nil {
		return TorrentClientCheckResult{}, err
	}
	check, err := client.CheckConnection(ctx)
	if sp, ok := client.(torrentclient.SessionProvider); ok {
		sess := sp.Session()
		if sess.Cookie != "" {
			if saveErr := s.repo.SaveTorrentClientSession(ctx, sess.Cookie, sess.Expires); saveErr != nil {
				s.logger.Warn("failed to persist torrent client session", "error", saveErr)
			}
		}
	}
	if err != nil {
		return TorrentClientCheckResult{}, err
	}
	msg := "Соединение с torrent-клиентом установлено."
	if strings.TrimSpace(check.Version) != "" {
		msg += " Версия: " + strings.TrimSpace(check.Version) + "."
	}
	return TorrentClientCheckResult{
		OK:      true,
		Client:  clientKind,
		Version: check.Version,
		Message: msg,
	}, nil
}

func (s *Service) RunMonitorOnce(ctx context.Context) error {
	items, err := s.repo.ListTorrents(ctx, "name", "asc", "")
	if err != nil {
		return err
	}
	blocked, blockErr := s.blockedTrackers(ctx)
	if blockErr != nil {
		s.logger.Warn("failed to load tracker warning blocks", "error", blockErr)
	}
	s.logger.Info("monitor cycle started", "items", len(items))
	for _, item := range items {
		if item.Paused || item.Type != TrackerTypeForum {
			continue
		}
		if blocked[strings.ToLower(item.Tracker)] {
			s.logger.Info("tracker is paused by unresolved interactive warning", "tracker", item.Tracker, "id", item.ID)
			continue
		}
		result, err := s.CheckTorrent(ctx, item.ID)
		if err != nil {
			s.logger.Warn("torrent check failed", "id", item.ID, "tracker", item.Tracker, "error", err)
			if needsBrowserInteraction(err) != nil {
				blocked[strings.ToLower(item.Tracker)] = true
			}
			continue
		}
		if result.Updated {
			s.logger.Info("torrent update detected", "id", item.ID, "tracker", item.Tracker, "title", result.Title, "torrent_bytes", result.TorrentSize)
		}
	}
	s.logger.Info("monitor cycle finished", "items", len(items))
	return nil
}

func (s *Service) RunTorrentOnce(ctx context.Context, id int64) error {
	result, err := s.CheckTorrent(ctx, id)
	if err != nil {
		return err
	}
	if result.Updated {
		s.logger.Info("torrent update detected", "id", id, "title", result.Title, "torrent_bytes", result.TorrentSize)
	}
	return nil
}

func (s *Service) CheckTorrent(ctx context.Context, id int64) (TorrentCheckResult, error) {
	item, err := s.repo.GetTorrent(ctx, id)
	if err != nil {
		return TorrentCheckResult{}, err
	}
	settings, err := s.repo.GetSettings(ctx)
	if err != nil {
		return TorrentCheckResult{}, err
	}
	credentials, err := s.ListCredentials(ctx)
	if err != nil {
		return TorrentCheckResult{}, err
	}
	cred := findCredential(credentials, item.Tracker)
	var browser sitetpl.BrowserPageFetcher
	releaseBrowserSession := false
	if cred.AccessMode == AccessModeChromium {
		browser = s.browser
		releaseBrowserSession = s.browser != nil
		defer func() {
			if !releaseBrowserSession {
				return
			}
			if closeErr := s.browser.CloseTracker(item.Tracker); closeErr != nil && !errors.Is(closeErr, browserbroker.ErrSessionNotFound) {
				s.logger.Warn("failed to close Chromium tracker page", "tracker", item.Tracker, "error", closeErr)
			}
		}()
	}
	result, err := s.runner.Check(ctx, sitetpl.CheckRequest{
		Item: sitetpl.Item{
			Tracker:   item.Tracker,
			Name:      item.Name,
			URL:       item.URL,
			TorrentID: item.TorrentID,
			UpdatedAt: item.UpdatedAt,
		},
		Credential: sitetpl.Credential{Login: cred.Login, Password: cred.Password, Passkey: cred.Passkey, Cookie: cred.Cookie, AccessMode: string(cred.AccessMode), UseProxy: cred.UseProxy},
		Settings: sitetpl.Settings{
			UserAgent:    settings.UserAgent,
			Timeout:      time.Duration(settings.HTTPTimeoutSeconds) * time.Second,
			ProxyEnabled: settings.Proxy,
			ProxyType:    settings.ProxyType,
			ProxyAddress: settings.ProxyAddress,
		},
		Browser: browser,
	})
	if err != nil {
		// An interactive challenge is the only reason to retain a live page.
		// The user needs that exact target to finish CAPTCHA or authentication.
		if needsBrowserInteraction(err) != nil {
			releaseBrowserSession = false
		}
		s.handleTorrentCheckError(ctx, item, err)
		return TorrentCheckResult{}, err
	}
	if _, clearErr := s.repo.ClearWarnings(ctx, item.Tracker); clearErr != nil {
		s.logger.Warn("failed to clear tracker warnings after successful check", "tracker", item.Tracker, "error", clearErr)
	}
	if cred.ID != 0 && result.SessionCookie != "" && result.SessionCookie != cred.Cookie {
		cookie := result.SessionCookie
		if _, saveErr := s.repo.UpdateCredential(ctx, cred.ID, UpdateCredentialRequest{Cookie: &cookie}); saveErr != nil {
			s.logger.Warn("failed to persist tracker session cookie", "tracker", item.Tracker, "error", saveErr)
		}
	}
	patch := UpdateTorrentRequest{}
	if strings.TrimSpace(result.Title) != "" && item.UpdateTitle && result.Title != item.Name {
		title := result.Title
		patch.Name = &title
	}
	if result.UpdatedAt != nil {
		patch.UpdatedAt = result.UpdatedAt
	}
	closed := result.Closed
	patch.Closed = &closed
	hasError := false
	patch.HasError = &hasError
	if patch.Name != nil || patch.UpdatedAt != nil || patch.Closed != nil || patch.HasError != nil {
		if updated, saveErr := s.repo.UpdateTorrent(ctx, item.ID, patch); saveErr != nil {
			s.logger.Warn("failed to persist torrent check state", "id", item.ID, "tracker", item.Tracker, "error", saveErr)
		} else {
			item = updated
		}
	}

	clientHash := strings.TrimSpace(item.Hash)
	clientAddOK := false
	if len(result.TorrentData) > 0 && torrentClientConfigured(settings) {
		client, err := torrentclient.NewFromConfig(torrentclient.Config{
			Kind:           settings.TorrentClient,
			Address:        settings.TorrentAddress,
			Login:          settings.TorrentLogin,
			Password:       settings.TorrentPassword,
			Timeout:        time.Duration(settings.HTTPTimeoutSeconds) * time.Second,
			SessionCookie:  settings.TorrentSessionCookie,
			SessionExpires: settings.TorrentSessionCookieExpires,
		})
		if err != nil {
			return TorrentCheckResult{}, err
		}
		deleteFiles := settings.DeleteOldFiles
		oldHash := ""
		if settings.DeleteDistribution {
			oldHash = strings.TrimSpace(item.Hash)
		}
		savePath := strings.TrimSpace(item.Path)
		if savePath == "" {
			savePath = settings.PathToDownload
		}
		add, addErr := client.Add(ctx, torrentclient.AddRequest{
			ID: item.ID, FileData: result.TorrentData, FileName: item.Tracker + "-" + item.TorrentID + ".torrent", SavePath: savePath, OldHash: oldHash, Tracker: item.Tracker, DeleteFiles: deleteFiles,
		})
		if sp, ok := client.(torrentclient.SessionProvider); ok {
			sess := sp.Session()
			if sess.Cookie != "" {
				if saveErr := s.repo.SaveTorrentClientSession(ctx, sess.Cookie, sess.Expires); saveErr != nil {
					s.logger.Warn("failed to persist torrent client session", "error", saveErr)
				}
			}
		}
		if addErr != nil {
			return TorrentCheckResult{}, addErr
		}
		clientAddOK = true
		if add.Hash != "" {
			clientHash = add.Hash
			updated, hashErr := s.repo.SetTorrentClientHash(ctx, item.ID, add.Hash)
			if hashErr != nil {
				return TorrentCheckResult{}, hashErr
			}
			item = updated
			if result.UpdatedAt != nil {
				if updated, saveErr := s.repo.UpdateTorrent(ctx, item.ID, UpdateTorrentRequest{UpdatedAt: result.UpdatedAt}); saveErr == nil {
					item = updated
				}
			}
		}
	}

	if result.Updated && strings.TrimSpace(settings.PostUpdateScript) != "" {
		if hookErr := s.runPostUpdateScript(ctx, settings, item, result, clientHash, clientAddOK); hookErr != nil {
			id := item.ID
			s.recordWarning(ctx, Warning{
				Where:     item.Tracker,
				Tracker:   item.Tracker,
				TorrentID: &id,
				Reason:    "Ошибка скрипта после обновления: " + hookErr.Error(),
			})
			hasError := true
			if _, saveErr := s.repo.UpdateTorrent(ctx, item.ID, UpdateTorrentRequest{HasError: &hasError}); saveErr != nil {
				s.logger.Warn("failed to mark torrent as errored after post-update hook", "id", item.ID, "tracker", item.Tracker, "error", saveErr)
			}
			return TorrentCheckResult{}, hookErr
		}
	}

	if result.Updated {
		s.notifyTorrentUpdate(ctx, settings, item, result, clientHash)
	}

	out := TorrentCheckResult{
		OK:          true,
		Updated:     result.Updated,
		Title:       result.Title,
		UpdatedAt:   result.UpdatedAt,
		Closed:      result.Closed,
		Message:     result.Message,
		TorrentSize: len(result.TorrentData),
	}
	return out, nil
}

func torrentClientConfigured(settings Settings) bool {
	return settings.UseTorrent && strings.TrimSpace(settings.TorrentClient) != ""
}

func (s *Service) runPostUpdateScript(ctx context.Context, settings Settings, item TorrentItem, result sitetpl.CheckResult, clientHash string, clientAddOK bool) error {
	script := strings.TrimSpace(settings.PostUpdateScript)
	if script == "" {
		return nil
	}

	torrentFile := ""
	if len(result.TorrentData) > 0 {
		f, err := os.CreateTemp("", "torrentmonitor-*.torrent")
		if err != nil {
			return fmt.Errorf("create temp torrent file: %w", err)
		}
		torrentFile = f.Name()
		if _, err := f.Write(result.TorrentData); err != nil {
			_ = f.Close()
			_ = os.Remove(torrentFile)
			return fmt.Errorf("write temp torrent file: %w", err)
		}
		if err := f.Close(); err != nil {
			_ = os.Remove(torrentFile)
			return fmt.Errorf("close temp torrent file: %w", err)
		}
		defer os.Remove(torrentFile)
	}

	savePath := strings.TrimSpace(item.Path)
	if savePath == "" {
		savePath = settings.PathToDownload
	}
	if clientHash == "" {
		clientHash = strings.TrimSpace(item.Hash)
	}

	hookCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(hookCtx, "/bin/sh", "-c", script)
	cmd.Env = postUpdateEnv(os.Environ(), settings, item, result, clientHash, savePath, torrentFile, clientAddOK)
	output, err := cmd.CombinedOutput()
	if hookCtx.Err() == context.DeadlineExceeded {
		return errors.New("script timeout after 5m")
	}
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg != "" {
			return fmt.Errorf("%w: %s", err, truncateForWarning(msg, 2000))
		}
		return err
	}
	if len(output) > 0 {
		s.logger.Info("post-update script finished", "id", item.ID, "tracker", item.Tracker, "output", truncateForWarning(strings.TrimSpace(string(output)), 500))
	} else {
		s.logger.Info("post-update script finished", "id", item.ID, "tracker", item.Tracker)
	}
	return nil
}

func postUpdateEnv(base []string, settings Settings, item TorrentItem, result sitetpl.CheckResult, clientHash, savePath, torrentFile string, clientAddOK bool) []string {
	updatedAt := ""
	if result.UpdatedAt != nil {
		updatedAt = result.UpdatedAt.Format(time.RFC3339)
	} else if item.UpdatedAt != nil {
		updatedAt = item.UpdatedAt.Format(time.RFC3339)
	}
	name := item.Name
	if strings.TrimSpace(result.Title) != "" {
		name = result.Title
	}
	client := strings.TrimSpace(settings.TorrentClient)
	clientEnabled := torrentClientConfigured(settings)
	if !clientEnabled {
		client = ""
		clientAddOK = false
	}
	vars := map[string]string{
		"TM_TORRENT_DB_ID":          strconv.FormatInt(item.ID, 10),
		"TM_TORRENT_ID":             item.TorrentID,
		"TM_TORRENT_TRACKER":        item.Tracker,
		"TM_TORRENT_NAME":           name,
		"TM_TORRENT_TITLE":          name,
		"TM_TORRENT_URL":            item.URL,
		"TM_TORRENT_PATH":           item.Path,
		"TM_TORRENT_SAVE_PATH":      savePath,
		"TM_SAVE_PATH":              savePath,
		"TM_TORRENT_HASH":           clientHash,
		"TM_TORRENT_UPDATED_AT":     updatedAt,
		"TM_TORRENT_CLOSED":         strconv.FormatBool(result.Closed),
		"TM_TORRENT_UPDATED":        strconv.FormatBool(result.Updated),
		"TM_TORRENT_FILE":           torrentFile,
		"TM_TORRENT_FILE_SIZE":      strconv.Itoa(len(result.TorrentData)),
		"TM_CLIENT":                 client,
		"TM_TORRENT_CLIENT":         client,
		"TM_TORRENT_CLIENT_ENABLED": strconv.FormatBool(clientEnabled),
		"TM_TORRENT_CLIENT_ADD_OK":  strconv.FormatBool(clientAddOK),
		"TM_TORRENT_CLIENT_HASH":    clientHash,
	}
	out := append([]string{}, base...)
	for k, v := range vars {
		out = append(out, k+"="+v)
	}
	return out
}

func truncateForWarning(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func (s *Service) recordWarning(ctx context.Context, warning Warning) {
	if strings.TrimSpace(warning.Where) == "" {
		warning.Where = "system"
	}
	if strings.TrimSpace(warning.Tracker) == "" && strings.TrimSpace(warning.Where) != "system" {
		warning.Tracker = warning.Where
	}
	if warning.Time.IsZero() {
		warning.Time = time.Now()
	}
	created, err := s.repo.CreateWarning(ctx, warning)
	if err != nil {
		s.logger.Warn("failed to record warning", "where", warning.Where, "error", err)
		return
	}
	settings, settingsErr := s.repo.GetSettings(ctx)
	if settingsErr != nil {
		s.logger.Warn("failed to load settings for warning notification", "where", warning.Where, "error", settingsErr)
		return
	}
	s.notifyWarning(ctx, settings, created)
}

func (s *Service) TestNotification(ctx context.Context, req NotificationTestRequest) (NotificationTestResult, error) {
	settings, err := s.repo.GetSettings(ctx)
	if err != nil {
		return NotificationTestResult{}, err
	}
	if req.TelegramBotToken != nil && strings.TrimSpace(*req.TelegramBotToken) != "" {
		settings.TelegramBotToken = strings.TrimSpace(*req.TelegramBotToken)
	}
	if req.TelegramChatID != nil {
		settings.TelegramChatID = strings.TrimSpace(*req.TelegramChatID)
	}
	if req.TelegramThreadID != nil {
		settings.TelegramThreadID = strings.TrimSpace(*req.TelegramThreadID)
	}
	if req.TelegramSilent != nil {
		settings.TelegramSilent = *req.TelegramSilent
	}
	if req.TelegramUseProxy != nil {
		settings.TelegramUseProxy = *req.TelegramUseProxy
	}
	if req.Proxy != nil {
		settings.Proxy = *req.Proxy
	}
	if req.ProxyAddress != nil {
		settings.ProxyAddress = strings.TrimSpace(*req.ProxyAddress)
	}
	if req.ProxyType != nil {
		settings.ProxyType = strings.TrimSpace(*req.ProxyType)
	}
	n, err := s.telegramNotifier(settings)
	if err != nil {
		return NotificationTestResult{}, err
	}
	if err := n.Send(ctx, notify.TelegramMessage{Text: "TorrentMonitor: тестовое Telegram-уведомление отправлено."}); err != nil {
		return NotificationTestResult{}, err
	}
	return NotificationTestResult{OK: true, Channel: "telegram", Message: "Telegram-уведомление отправлено."}, nil
}

func (s *Service) notifyWarning(ctx context.Context, settings Settings, warning Warning) {
	if !settings.Send || !settings.SendWarning || !settings.TelegramEnabled {
		return
	}
	n, err := s.telegramNotifier(settings)
	if err != nil {
		s.logger.Warn("telegram warning notification is not configured", "error", err)
		return
	}
	where := strings.TrimSpace(warning.Where)
	if warning.Tracker != "" {
		where = warning.Tracker
	}
	if where == "" {
		where = "system"
	}
	text := "⚠️ TorrentMonitor: ошибка\n" +
		"Где: " + where + "\n" +
		"Время: " + warning.Time.Format("2006-01-02 15:04:05") + "\n" +
		truncateForWarning(strings.TrimSpace(warning.Reason), 3000)
	if warning.TorrentID != nil {
		text += "\nТема ID: " + strconv.FormatInt(*warning.TorrentID, 10)
	}
	msg := notify.TelegramMessage{Text: text}
	if url := s.notificationActionURL(settings, warning.ActionURL); url != "" && strings.TrimSpace(warning.ActionLabel) != "" {
		msg.Button = &notify.Button{Label: warning.ActionLabel, URL: url}
	}
	if err := n.Send(ctx, msg); err != nil {
		s.logger.Warn("failed to send telegram warning notification", "where", warning.Where, "error", err)
	}
}

func (s *Service) notifyTorrentUpdate(ctx context.Context, settings Settings, item TorrentItem, result sitetpl.CheckResult, clientHash string) {
	if !settings.Send || !settings.SendUpdate || !settings.TelegramEnabled {
		return
	}
	n, err := s.telegramNotifier(settings)
	if err != nil {
		s.logger.Warn("telegram update notification is not configured", "error", err)
		return
	}
	title := strings.TrimSpace(result.Title)
	if title == "" {
		title = strings.TrimSpace(item.Name)
	}
	updatedAt := "—"
	if result.UpdatedAt != nil {
		updatedAt = result.UpdatedAt.Format("2006-01-02 15:04:05")
	}
	lines := []string{
		"✅ TorrentMonitor: раздача обновлена",
		"Трекер: " + item.Tracker,
		"Название: " + title,
		"Дата обновления: " + updatedAt,
	}
	if item.TorrentID != "" {
		lines = append(lines, "Torrent ID: "+item.TorrentID)
	}
	if clientHash != "" {
		lines = append(lines, "Hash: "+clientHash)
	}
	if len(result.TorrentData) > 0 {
		lines = append(lines, "Размер .torrent: "+strconv.Itoa(len(result.TorrentData))+" байт")
	}
	msg := notify.TelegramMessage{Text: strings.Join(lines, "\n")}
	if strings.TrimSpace(item.URL) != "" {
		msg.Button = &notify.Button{Label: "Открыть тему", URL: item.URL}
	}
	if err := n.Send(ctx, msg); err != nil {
		s.logger.Warn("failed to send telegram update notification", "id", item.ID, "tracker", item.Tracker, "error", err)
	}
}

func (s *Service) telegramNotifier(settings Settings) (*notify.TelegramNotifier, error) {
	return notify.NewTelegram(notify.TelegramConfig{
		BotToken:     settings.TelegramBotToken,
		ChatID:       settings.TelegramChatID,
		ThreadID:     settings.TelegramThreadID,
		Silent:       settings.TelegramSilent,
		UseProxy:     settings.Proxy || settings.TelegramUseProxy,
		ProxyType:    settings.ProxyType,
		ProxyAddress: settings.ProxyAddress,
		Timeout:      time.Duration(settings.HTTPTimeoutSeconds) * time.Second,
	})
}

func (s *Service) notificationActionURL(settings Settings, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	base := strings.TrimSpace(settings.ServerAddress)
	if base == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(raw, "/")
}

func (s *Service) handleTorrentCheckError(ctx context.Context, item TorrentItem, err error) {
	id := item.ID
	warning := Warning{
		Where:     item.Tracker,
		Tracker:   item.Tracker,
		TorrentID: &id,
		Reason:    "Ошибка проверки темы: " + err.Error(),
	}
	if need := needsBrowserInteraction(err); need != nil {
		warning.Reason = "Требуется интерактивная проверка трекера. Открой браузерную сессию, заверши Cloudflare/CAPTCHA/login и повтори проверку. Подробности: " + need.Error()
		warning.ActionKind = "browser_session"
		warning.ActionLabel = "Открыть браузер"
		warning.ActionURL = need.ViewerURL
	}
	s.recordWarning(ctx, warning)
	hasError := true
	if _, saveErr := s.repo.UpdateTorrent(ctx, item.ID, UpdateTorrentRequest{HasError: &hasError}); saveErr != nil {
		s.logger.Warn("failed to mark torrent as errored", "id", item.ID, "tracker", item.Tracker, "error", saveErr)
	}
}

func (s *Service) blockedTrackers(ctx context.Context) (map[string]bool, error) {
	warnings, err := s.repo.ListWarnings(ctx)
	if err != nil {
		return nil, err
	}
	blocked := map[string]bool{}
	for _, w := range warnings {
		if w.ActionKind != "browser_session" {
			continue
		}
		tracker := strings.ToLower(strings.TrimSpace(w.Tracker))
		if tracker == "" {
			tracker = strings.ToLower(strings.TrimSpace(w.Where))
		}
		if tracker != "" {
			blocked[tracker] = true
		}
	}
	return blocked, nil
}

func needsBrowserInteraction(err error) *browserbroker.NeedsInteractionError {
	var need *browserbroker.NeedsInteractionError
	if errors.As(err, &need) {
		return need
	}
	return nil
}

func findCredential(credentials []Credential, tracker string) Credential {
	for _, c := range credentials {
		if strings.EqualFold(c.Tracker, tracker) {
			return c
		}
	}
	return Credential{}
}

func parseTrackerURL(raw string) (tracker string, torrentID string, err error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", "", errors.New("invalid url")
	}

	host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	host = normalizeTracker(host)

	switch host {
	case "rutor.is":
		re := regexp.MustCompile(`\d{4,8}`)
		torrentID = re.FindString(u.Path)
	case "anidub.com", "riperam.org":
		torrentID = strings.TrimSpace(u.Path)
	case "animelayer.ru":
		path := strings.TrimPrefix(u.Path, "/torrent")
		torrentID = strings.Trim(path, "/")
	case "casstudio.tk":
		torrentID = u.Query().Get("t")
	default:
		torrentID = firstQueryValue(u)
	}

	if torrentID == "" {
		return "", "", errors.New("cannot extract torrent id")
	}
	return host, torrentID, nil
}

func firstQueryValue(u *url.URL) string {
	for _, values := range u.Query() {
		if len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func normalizeTracker(tracker string) string {
	tracker = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(tracker), "www."))
	switch tracker {
	case "tr.anidub.com":
		return "anidub.com"
	case "baibako.tv":
		return "baibako.tv_forum"
	default:
		return tracker
	}
}

func BuildTrackerURL(tracker, id string) string {
	switch tracker {
	case "rutracker.org", "nnmclub.to", "pornolab.net", "rustorka.com", "tfile.cc":
		scheme := "http"
		if tracker == "rutracker.org" || tracker == "nnmclub.to" {
			scheme = "https"
		}
		return fmt.Sprintf("%s://%s/forum/viewtopic.php?t=%s", scheme, tracker, id)
	case "kinozal.me", "kinozal.tv", "kinozal.guru":
		return fmt.Sprintf("http://%s/details.php?id=%s", tracker, id)
	case "rutor.is", "animelayer.ru":
		return fmt.Sprintf("http://%s/torrent/%s", tracker, id)
	case "anidub.com":
		return fmt.Sprintf("https://tr.anidub.com/%s", strings.TrimPrefix(id, "/"))
	case "riperam.org":
		return "http://riperam.org" + id
	case "baibako.tv_forum":
		return fmt.Sprintf("http://baibako.tv/details.php?id=%s", id)
	default:
		return ""
	}
}

func qualityFor(tracker string, hd int) *string {
	q := "sd"
	if tracker == "lostfilm.tv" || tracker == "lostfilm-mirror" {
		if hd == 1 {
			q = "1080"
		} else if hd == 2 {
			q = "720"
		}
		return &q
	}
	if hd == 1 {
		q = "720"
	} else if hd == 2 {
		q = "1080"
	}
	return &q
}
