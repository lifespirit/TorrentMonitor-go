package core

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"torrentmonitor-go/internal/browserbroker"
)

type TorrentKind string

const (
	TorrentKindTheme  TorrentKind = "theme"
	TorrentKindSeries TorrentKind = "series"
)

type TrackerType string

type AccessMode string

const (
	TrackerTypeForum TrackerType = "forum"
	TrackerTypeRSS   TrackerType = "RSS"
)

const (
	AccessModeNative   AccessMode = "native"
	AccessModeChromium AccessMode = "chromium"
)

type TorrentItem struct {
	ID          int64       `json:"id"`
	Tracker     string      `json:"tracker"`
	Type        TrackerType `json:"type"`
	Name        string      `json:"name"`
	URL         string      `json:"url"`
	TorrentID   string      `json:"torrent_id"`
	Quality     *string     `json:"quality"`
	Path        string      `json:"path"`
	Episode     string      `json:"episode"`
	UpdatedAt   *time.Time  `json:"updated_at"`
	AutoUpdate  bool        `json:"auto_update"`
	Hash        string      `json:"hash"`
	Paused      bool        `json:"paused"`
	HasError    bool        `json:"has_error"`
	Closed      bool        `json:"closed"`
	UpdateTitle bool        `json:"update_title"`
}

type AddTorrentRequest struct {
	Kind         TorrentKind `json:"kind"`
	URL          string      `json:"url"`
	Tracker      string      `json:"tracker"`
	Name         string      `json:"name"`
	Path         string      `json:"path"`
	HD           int         `json:"hd"`
	UpdateHeader bool        `json:"update_header"`
}

type UpdateTorrentRequest struct {
	Name           *string    `json:"name"`
	Path           *string    `json:"path"`
	TorrentID      *string    `json:"torrent_id"`
	AutoUpdate     *bool      `json:"auto_update"`
	ResetTimestamp *bool      `json:"reset_timestamp"`
	Paused         *bool      `json:"paused"`
	UpdatedAt      *time.Time `json:"updated_at"`
	Closed         *bool      `json:"closed"`
	HasError       *bool      `json:"has_error"`
}

type UpdateCredentialRequest struct {
	Login      *string `json:"login"`
	Password   *string `json:"password"`
	Passkey    *string `json:"passkey"`
	AccessMode *string `json:"access_mode"`
	UseProxy   *bool   `json:"use_proxy"`
	Cookie     *string `json:"cookie"` // internal/native session compatibility; hidden from the UI
}

type Warning struct {
	ID          int64     `json:"id"`
	Time        time.Time `json:"time"`
	Where       string    `json:"where"`
	Reason      string    `json:"reason"`
	TorrentID   *int64    `json:"torrent_id,omitempty"`
	Tracker     string    `json:"tracker,omitempty"`
	ActionKind  string    `json:"action_kind,omitempty"`
	ActionLabel string    `json:"action_label,omitempty"`
	ActionURL   string    `json:"action_url,omitempty"`
}

type News struct {
	ID   int64  `json:"id"`
	Text string `json:"text"`
	New  bool   `json:"new"`
}

type Credential struct {
	ID          int64       `json:"id"`
	Tracker     string      `json:"tracker"`
	Login       string      `json:"login"`
	Password    string      `json:"password,omitempty"`
	Passkey     string      `json:"passkey"`
	AccessMode  AccessMode  `json:"access_mode"`
	UseProxy    bool        `json:"use_proxy"`
	Cookie      string      `json:"-"`
	Type        TrackerType `json:"type"`
	Necessarily bool        `json:"necessarily"`
}

type CredentialLoginCheckRequest struct {
	OpenBrowser bool   `json:"open_browser"`
	AccessMode  string `json:"access_mode"`
}

type CredentialLoginCheckResult struct {
	OK               bool       `json:"ok"`
	Tracker          string     `json:"tracker"`
	Mode             AccessMode `json:"mode"`
	Message          string     `json:"message"`
	LoginURL         string     `json:"login_url,omitempty"`
	ProfilePath      string     `json:"profile_path,omitempty"`
	BrowserSessionID string     `json:"browser_session_id,omitempty"`
	ViewerURL        string     `json:"viewer_url,omitempty"`
	SessionSaved     bool       `json:"session_saved"`
}

type NotificationTestRequest struct {
	TelegramEnabled  *bool   `json:"telegram_enabled"`
	TelegramBotToken *string `json:"telegram_bot_token"`
	TelegramChatID   *string `json:"telegram_chat_id"`
	TelegramThreadID *string `json:"telegram_thread_id"`
	TelegramSilent   *bool   `json:"telegram_silent"`
	TelegramUseProxy *bool   `json:"telegram_use_proxy"`
	Proxy            *bool   `json:"proxy"`
	ProxyAddress     *string `json:"proxy_address"`
	ProxyType        *string `json:"proxy_type"`
}

type NotificationTestResult struct {
	OK      bool   `json:"ok"`
	Channel string `json:"channel"`
	Message string `json:"message"`
}

type TorrentClientCheckRequest struct {
	TorrentClient      *string `json:"torrent_client"`
	TorrentAddress     *string `json:"torrent_address"`
	TorrentLogin       *string `json:"torrent_login"`
	TorrentPassword    *string `json:"torrent_password"`
	HTTPTimeoutSeconds *int    `json:"http_timeout_seconds"`
}

type TorrentClientCheckResult struct {
	OK      bool   `json:"ok"`
	Client  string `json:"client"`
	Version string `json:"version,omitempty"`
	Message string `json:"message"`
}

type TrackerTemplateInfo struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Domains           []string `json:"domains"`
	Kind              string   `json:"kind"`
	DefaultAccessMode string   `json:"default_access_mode"`
	Source            string   `json:"source"`
}

type TemplatesStatus struct {
	SourceURL             string                `json:"source_url"`
	Directory             string                `json:"directory"`
	UpdateIntervalMinutes int                   `json:"update_interval_minutes"`
	Loaded                int                   `json:"loaded"`
	Templates             []TrackerTemplateInfo `json:"templates"`
}

type TemplateUpdateResult struct {
	SourceURL string    `json:"source_url"`
	Directory string    `json:"directory"`
	UpdatedAt time.Time `json:"updated_at"`
	Loaded    int       `json:"loaded"`
	Files     []string  `json:"files"`
	Skipped   bool      `json:"skipped"`
	Message   string    `json:"message"`
}

// TorrentAddToClientRequest is used by the manual action for an existing
// monitored item. The site runner will eventually call the same service path
// after it downloads or discovers a fresh .torrent URL.

type TorrentCheckResult struct {
	OK          bool       `json:"ok"`
	Updated     bool       `json:"updated"`
	Title       string     `json:"title"`
	UpdatedAt   *time.Time `json:"updated_at"`
	Closed      bool       `json:"closed"`
	Message     string     `json:"message"`
	TorrentSize int        `json:"torrent_size"`
}

type TorrentAddToClientRequest struct {
	URL         string `json:"url"`
	SavePath    string `json:"save_path"`
	DeleteFiles *bool  `json:"delete_files"`
}

type TorrentAddToClientResult struct {
	OK      bool        `json:"ok"`
	Client  string      `json:"client"`
	Hash    string      `json:"hash"`
	Message string      `json:"message"`
	Item    TorrentItem `json:"item"`
}

type Settings struct {
	Auth                          bool       `json:"auth"`
	AuthPasswordHash              string     `json:"-"`
	AuthPasswordSet               bool       `json:"auth_password_set"`
	Send                          bool       `json:"send"`
	SendWarning                   bool       `json:"send_warning"`
	SendUpdate                    bool       `json:"send_update"`
	TelegramEnabled               bool       `json:"telegram_enabled"`
	TelegramBotToken              string     `json:"-"`
	TelegramBotTokenSet           bool       `json:"telegram_bot_token_set"`
	TelegramChatID                string     `json:"telegram_chat_id"`
	TelegramThreadID              string     `json:"telegram_thread_id"`
	TelegramSilent                bool       `json:"telegram_silent"`
	TelegramUseProxy              bool       `json:"telegram_use_proxy"`
	Proxy                         bool       `json:"proxy"`
	ProxyAddress                  string     `json:"proxy_address"`
	ProxyType                     string     `json:"proxy_type"`
	UseTorrent                    bool       `json:"use_torrent"`
	TorrentClient                 string     `json:"torrent_client"`
	TorrentAddress                string     `json:"torrent_address"`
	TorrentLogin                  string     `json:"torrent_login"`
	TorrentPassword               string     `json:"torrent_password,omitempty"`
	TorrentSessionCookie          string     `json:"-"`
	TorrentSessionCookieExpires   *time.Time `json:"-"`
	PathToDownload                string     `json:"path_to_download"`
	DeleteOldFiles                bool       `json:"delete_old_files"`
	DeleteDistribution            bool       `json:"delete_distribution"`
	ServerAddress                 string     `json:"server_address"`
	Debug                         bool       `json:"debug"`
	AutoUpdate                    bool       `json:"auto_update"`
	HTTPTimeoutSeconds            int        `json:"http_timeout_seconds"`
	MonitorIntervalMinutes        int        `json:"monitor_interval_minutes"`
	PostUpdateScript              string     `json:"post_update_script"`
	BrowserMode                   string     `json:"browser_mode"`
	BrowserBinary                 string     `json:"browser_binary"`
	BrowserProfile                string     `json:"browser_profile"`
	BrowserConnectURL             string     `json:"browser_connect_url"`
	TemplateSourceURL             string     `json:"template_source_url"`
	TemplateUpdateIntervalMinutes int        `json:"template_update_interval_minutes"`
	TemplateDirectory             string     `json:"template_directory"`
	UserAgent                     string     `json:"user_agent"`
}

type UpdateSettingsRequest struct {
	Auth                          *bool   `json:"auth"`
	AdminPassword                 *string `json:"admin_password"`
	AdminPasswordHash             *string `json:"-"`
	Send                          *bool   `json:"send"`
	SendWarning                   *bool   `json:"send_warning"`
	SendUpdate                    *bool   `json:"send_update"`
	TelegramEnabled               *bool   `json:"telegram_enabled"`
	TelegramBotToken              *string `json:"telegram_bot_token"`
	TelegramChatID                *string `json:"telegram_chat_id"`
	TelegramThreadID              *string `json:"telegram_thread_id"`
	TelegramSilent                *bool   `json:"telegram_silent"`
	TelegramUseProxy              *bool   `json:"telegram_use_proxy"`
	Proxy                         *bool   `json:"proxy"`
	ProxyAddress                  *string `json:"proxy_address"`
	ProxyType                     *string `json:"proxy_type"`
	UseTorrent                    *bool   `json:"use_torrent"`
	TorrentClient                 *string `json:"torrent_client"`
	TorrentAddress                *string `json:"torrent_address"`
	TorrentLogin                  *string `json:"torrent_login"`
	TorrentPassword               *string `json:"torrent_password"`
	PathToDownload                *string `json:"path_to_download"`
	DeleteOldFiles                *bool   `json:"delete_old_files"`
	DeleteDistribution            *bool   `json:"delete_distribution"`
	ServerAddress                 *string `json:"server_address"`
	Debug                         *bool   `json:"debug"`
	AutoUpdate                    *bool   `json:"auto_update"`
	HTTPTimeoutSeconds            *int    `json:"http_timeout_seconds"`
	MonitorIntervalMinutes        *int    `json:"monitor_interval_minutes"`
	PostUpdateScript              *string `json:"post_update_script"`
	BrowserMode                   *string `json:"browser_mode"`
	BrowserBinary                 *string `json:"browser_binary"`
	BrowserProfile                *string `json:"browser_profile"`
	BrowserConnectURL             *string `json:"browser_connect_url"`
	TemplateSourceURL             *string `json:"template_source_url"`
	TemplateUpdateIntervalMinutes *int    `json:"template_update_interval_minutes"`
	TemplateDirectory             *string `json:"template_directory"`
	UserAgent                     *string `json:"user_agent"`
}

func NormalizeAccessMode(mode string) AccessMode {
	switch AccessMode(mode) {
	case AccessModeChromium:
		return AccessModeChromium
	default:
		return AccessModeNative
	}
}

const (
	BrowserModeEmbedded = "embedded"
	BrowserModeExternal = "external"
)

func NormalizeBrowserMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case BrowserModeExternal, "connect", "remote":
		return BrowserModeExternal
	default:
		return BrowserModeEmbedded
	}
}

func defaultBrowserMode() string {
	if strings.TrimSpace(os.Getenv("TM_BROWSER_CONNECT_URL")) != "" {
		return BrowserModeExternal
	}
	return BrowserModeEmbedded
}

func BrowserConfigFromSettings(settings Settings) browserbroker.Config {
	mode := NormalizeBrowserMode(settings.BrowserMode)
	cfg := browserbroker.Config{
		Binary:      strings.TrimSpace(settings.BrowserBinary),
		ProfilePath: strings.TrimSpace(settings.BrowserProfile),
		Debug:       settings.Debug,
	}
	if mode == BrowserModeExternal {
		cfg.ConnectURL = strings.TrimSpace(settings.BrowserConnectURL)
	}
	return cfg
}

func defaultTemplateDirectory() string {
	if dir := strings.TrimSpace(os.Getenv("TM_TEMPLATE_DIR")); dir != "" {
		return dir
	}
	if dataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dataHome != "" {
		return filepath.Join(dataHome, "torrentmonitor-go", "templates")
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".local", "share", "torrentmonitor-go", "templates")
	}
	return filepath.Join(".", "templates")
}

func envBoolDefault(key string, fallback bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func envIntDefault(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

func DefaultSettings() Settings {
	adminHash := HashPassword(strings.TrimSpace(os.Getenv("TM_ADMIN_PASSWORD")))
	return Settings{
		Auth:                          true,
		AuthPasswordHash:              adminHash,
		AuthPasswordSet:               adminHash != "",
		Send:                          envBoolDefault("TM_SEND_NOTIFICATIONS", false),
		SendWarning:                   envBoolDefault("TM_SEND_WARNING", false),
		SendUpdate:                    envBoolDefault("TM_SEND_UPDATE", false),
		TelegramEnabled:               envBoolDefault("TM_TELEGRAM_ENABLED", strings.TrimSpace(os.Getenv("TM_TELEGRAM_BOT_TOKEN")) != "" && strings.TrimSpace(os.Getenv("TM_TELEGRAM_CHAT_ID")) != ""),
		TelegramBotToken:              strings.TrimSpace(os.Getenv("TM_TELEGRAM_BOT_TOKEN")),
		TelegramBotTokenSet:           strings.TrimSpace(os.Getenv("TM_TELEGRAM_BOT_TOKEN")) != "",
		TelegramChatID:                strings.TrimSpace(os.Getenv("TM_TELEGRAM_CHAT_ID")),
		TelegramThreadID:              strings.TrimSpace(os.Getenv("TM_TELEGRAM_THREAD_ID")),
		TelegramSilent:                envBoolDefault("TM_TELEGRAM_SILENT", false),
		TelegramUseProxy:              envBoolDefault("TM_TELEGRAM_USE_PROXY", false),
		Proxy:                         false,
		ProxyAddress:                  "127.0.0.1:9050",
		ProxyType:                     "SOCKS5",
		UseTorrent:                    false,
		TorrentClient:                 "",
		TorrentAddress:                "127.0.0.1:9091",
		TorrentLogin:                  "",
		TorrentPassword:               "",
		TorrentSessionCookie:          "",
		TorrentSessionCookieExpires:   nil,
		PathToDownload:                "",
		DeleteOldFiles:                false,
		DeleteDistribution:            false,
		ServerAddress:                 "",
		Debug:                         false,
		AutoUpdate:                    false,
		HTTPTimeoutSeconds:            15,
		MonitorIntervalMinutes:        15,
		PostUpdateScript:              "",
		BrowserMode:                   defaultBrowserMode(),
		BrowserBinary:                 strings.TrimSpace(os.Getenv("TM_BROWSER_BINARY")),
		BrowserProfile:                strings.TrimSpace(os.Getenv("TM_BROWSER_PROFILE")),
		BrowserConnectURL:             strings.TrimSpace(os.Getenv("TM_BROWSER_CONNECT_URL")),
		TemplateSourceURL:             strings.TrimSpace(os.Getenv("TM_TEMPLATE_SOURCE_URL")),
		TemplateUpdateIntervalMinutes: envIntDefault("TM_TEMPLATE_UPDATE_INTERVAL_MINUTES", 1440),
		TemplateDirectory:             defaultTemplateDirectory(),
		UserAgent:                     "Mozilla/5.0 (X11; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0",
	}
}

func clearTorrentClientSession(s *Settings) {
	s.TorrentSessionCookie = ""
	s.TorrentSessionCookieExpires = nil
}

func ApplySettingsPatch(s Settings, patch UpdateSettingsRequest) Settings {
	if patch.Auth != nil {
		s.Auth = *patch.Auth
	}
	if patch.AdminPasswordHash != nil {
		s.AuthPasswordHash = strings.TrimSpace(*patch.AdminPasswordHash)
	}
	if patch.AdminPassword != nil {
		password := strings.TrimSpace(*patch.AdminPassword)
		if password != "" {
			s.AuthPasswordHash = HashPassword(password)
		}
	}
	s.AuthPasswordSet = strings.TrimSpace(s.AuthPasswordHash) != ""
	if patch.Send != nil {
		s.Send = *patch.Send
	}
	if patch.SendWarning != nil {
		s.SendWarning = *patch.SendWarning
	}
	if patch.SendUpdate != nil {
		s.SendUpdate = *patch.SendUpdate
	}
	if patch.TelegramEnabled != nil {
		s.TelegramEnabled = *patch.TelegramEnabled
	}
	if patch.TelegramBotToken != nil {
		token := strings.TrimSpace(*patch.TelegramBotToken)
		if token != "" {
			s.TelegramBotToken = token
		}
	}
	if patch.TelegramChatID != nil {
		s.TelegramChatID = strings.TrimSpace(*patch.TelegramChatID)
	}
	if patch.TelegramThreadID != nil {
		s.TelegramThreadID = strings.TrimSpace(*patch.TelegramThreadID)
	}
	if patch.TelegramSilent != nil {
		s.TelegramSilent = *patch.TelegramSilent
	}
	if patch.TelegramUseProxy != nil {
		s.TelegramUseProxy = *patch.TelegramUseProxy
	}
	s.TelegramBotTokenSet = strings.TrimSpace(s.TelegramBotToken) != ""
	if patch.Proxy != nil {
		s.Proxy = *patch.Proxy
	}
	if patch.ProxyAddress != nil {
		s.ProxyAddress = *patch.ProxyAddress
	}
	if patch.ProxyType != nil {
		s.ProxyType = *patch.ProxyType
	}
	if patch.UseTorrent != nil {
		s.UseTorrent = *patch.UseTorrent
	}
	if patch.TorrentClient != nil {
		s.TorrentClient = *patch.TorrentClient
		clearTorrentClientSession(&s)
	}
	if patch.TorrentAddress != nil {
		s.TorrentAddress = *patch.TorrentAddress
		clearTorrentClientSession(&s)
	}
	if patch.TorrentLogin != nil {
		s.TorrentLogin = *patch.TorrentLogin
		clearTorrentClientSession(&s)
	}
	if patch.TorrentPassword != nil {
		s.TorrentPassword = *patch.TorrentPassword
		clearTorrentClientSession(&s)
	}
	if patch.PathToDownload != nil {
		s.PathToDownload = *patch.PathToDownload
	}
	if patch.DeleteOldFiles != nil {
		s.DeleteOldFiles = *patch.DeleteOldFiles
	}
	if patch.DeleteDistribution != nil {
		s.DeleteDistribution = *patch.DeleteDistribution
	}
	if patch.ServerAddress != nil {
		s.ServerAddress = *patch.ServerAddress
	}
	if patch.Debug != nil {
		s.Debug = *patch.Debug
	}
	if patch.AutoUpdate != nil {
		s.AutoUpdate = *patch.AutoUpdate
	}
	if patch.HTTPTimeoutSeconds != nil {
		s.HTTPTimeoutSeconds = *patch.HTTPTimeoutSeconds
	}
	if patch.MonitorIntervalMinutes != nil {
		if *patch.MonitorIntervalMinutes > 0 {
			s.MonitorIntervalMinutes = *patch.MonitorIntervalMinutes
		}
	}
	if patch.PostUpdateScript != nil {
		s.PostUpdateScript = *patch.PostUpdateScript
	}
	if patch.BrowserMode != nil {
		s.BrowserMode = NormalizeBrowserMode(*patch.BrowserMode)
	}
	if patch.BrowserBinary != nil {
		s.BrowserBinary = strings.TrimSpace(*patch.BrowserBinary)
	}
	if patch.BrowserProfile != nil {
		s.BrowserProfile = strings.TrimSpace(*patch.BrowserProfile)
	}
	if patch.BrowserConnectURL != nil {
		s.BrowserConnectURL = strings.TrimSpace(*patch.BrowserConnectURL)
	}
	if patch.TemplateSourceURL != nil {
		s.TemplateSourceURL = strings.TrimSpace(*patch.TemplateSourceURL)
	}
	if patch.TemplateUpdateIntervalMinutes != nil {
		if *patch.TemplateUpdateIntervalMinutes >= 0 {
			s.TemplateUpdateIntervalMinutes = *patch.TemplateUpdateIntervalMinutes
		}
	}
	if patch.TemplateDirectory != nil {
		s.TemplateDirectory = strings.TrimSpace(*patch.TemplateDirectory)
	}
	if strings.TrimSpace(s.TemplateDirectory) == "" {
		s.TemplateDirectory = defaultTemplateDirectory()
	}
	if patch.UserAgent != nil {
		s.UserAgent = *patch.UserAgent
	}
	return s
}

type Bootstrap struct {
	Version         VersionInfo         `json:"version"`
	Auth            AuthInfo            `json:"auth"`
	Counters        Counters            `json:"counters"`
	LastStart       *time.Time          `json:"last_start"`
	UpdateAvailable bool                `json:"update_available"`
	Features        map[string][]string `json:"features"`
}

type VersionInfo struct {
	App      string `json:"app"`
	Database string `json:"database"`
}

type AuthInfo struct {
	Enabled       bool `json:"enabled"`
	Authenticated bool `json:"authenticated"`
	PasswordSet   bool `json:"password_set"`
}

type Counters struct {
	Warnings   int `json:"warnings"`
	News       int `json:"news"`
	Torrents   int `json:"torrents"`
	WatchUsers int `json:"watch_users"`
}
