package browserbroker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Binary      string
	ProfileBase string
	ProfilePath string
	// ConnectURL points to an already running Chrome/Chromium DevTools endpoint.
	// When set, BrowserBroker does not start or stop Chromium itself.
	// Supported forms: http://127.0.0.1:9222, 127.0.0.1:9222, :9222, 9222.
	ConnectURL string
	Debug      bool
	ViewportW  int
	ViewportH  int
	Quality    int
}

type Broker struct {
	cfg    Config
	logger Logger

	mu        sync.Mutex
	sessions  map[string]*Session
	byTracker map[string]string
	browser   *browserProcess
}

type browserProcess struct {
	cmd          *exec.Cmd
	port         int
	debuggerBase string
	binary       string
	profilePath  string
	external     bool
}

type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// NeedsInteractionError is returned by Chromium page waits when the expected
// tracker page did not render before timeout. The browser session is kept open
// and ViewerURL should be shown to the user so they can finish Cloudflare,
// CAPTCHA, or tracker login manually.
type NeedsInteractionError struct {
	Tracker   string
	URL       string
	SessionID string
	ViewerURL string
	Message   string
}

func (e *NeedsInteractionError) Error() string {
	if e == nil {
		return "browser interaction is required"
	}
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return "browser interaction is required"
}

type OpenRequest struct {
	Tracker     string
	URL         string
	ProfilePath string // deprecated: BrowserBroker intentionally uses one shared profile for all trackers.
}

type OpenResult struct {
	ID          string    `json:"id"`
	Tracker     string    `json:"tracker"`
	URL         string    `json:"url"`
	ProfilePath string    `json:"profile_path"`
	ViewerURL   string    `json:"viewer_url"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

type SessionInfo struct {
	ID                     string        `json:"id"`
	Tracker                string        `json:"tracker"`
	URL                    string        `json:"url"`
	CurrentURL             string        `json:"current_url,omitempty"`
	Title                  string        `json:"title,omitempty"`
	ProfilePath            string        `json:"profile_path"`
	Status                 string        `json:"status"`
	LastError              string        `json:"last_error,omitempty"`
	CreatedAt              time.Time     `json:"created_at"`
	UpdatedAt              time.Time     `json:"updated_at"`
	Width                  int           `json:"width"`
	Height                 int           `json:"height"`
	CookieNames            []string      `json:"cookie_names,omitempty"`
	CookieDetails          []CookieDebug `json:"cookie_details,omitempty"`
	HasCloudflareClearance bool          `json:"has_cloudflare_clearance,omitempty"`
	LooksLikeCloudflare    bool          `json:"looks_like_cloudflare,omitempty"`
	LooksLikeLoginPage     bool          `json:"looks_like_login_page,omitempty"`
}

type CookieDebug struct {
	Name     string  `json:"name"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires,omitempty"`
	Session  bool    `json:"session"`
	HTTPOnly bool    `json:"http_only"`
	Secure   bool    `json:"secure"`
	SameSite string  `json:"same_site,omitempty"`
}

type InputEvent struct {
	Kind                  string  `json:"kind"`
	Type                  string  `json:"type"`
	X                     float64 `json:"x"`
	Y                     float64 `json:"y"`
	DeltaX                float64 `json:"delta_x"`
	DeltaY                float64 `json:"delta_y"`
	Button                string  `json:"button"`
	ClickCount            int     `json:"click_count"`
	Modifiers             int     `json:"modifiers"`
	Key                   string  `json:"key"`
	Code                  string  `json:"code"`
	Text                  string  `json:"text"`
	UnmodifiedText        string  `json:"unmodified_text"`
	WindowsVirtualKeyCode int     `json:"windows_virtual_key_code"`
	NativeVirtualKeyCode  int     `json:"native_virtual_key_code"`
	Location              int     `json:"location"`
	AutoRepeat            bool    `json:"auto_repeat"`
	IsKeypad              bool    `json:"is_keypad"`
}

func (c Config) sharedProfilePath() string {
	if p := strings.TrimSpace(c.ProfilePath); p != "" {
		return p
	}
	if p := strings.TrimSpace(os.Getenv("TM_BROWSER_PROFILE")); p != "" {
		return p
	}
	base := strings.TrimSpace(c.ProfileBase)
	if base == "" {
		base = defaultProfileBase()
	}
	return filepath.Join(base, "torrentmonitor")
}
