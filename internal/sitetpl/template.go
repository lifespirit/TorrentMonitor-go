package sitetpl

import "time"

type Mode string

const (
	ModeHTTP    Mode = "http"
	ModeBrowser Mode = "browser"
)

type Template struct {
	Version       int          `json:"version" yaml:"version"`
	SchemaVersion int          `json:"schema_version" yaml:"schema_version"`
	Site          string       `json:"site" yaml:"site"`
	ID            string       `json:"id" yaml:"id"`
	Name          string       `json:"name" yaml:"name"`
	Domains       []string     `json:"domains" yaml:"domains"`
	Kind          string       `json:"kind" yaml:"kind"`
	Mode          Mode         `json:"mode" yaml:"mode"`
	Defaults      Defaults     `json:"defaults" yaml:"defaults"`
	URLs          URLs         `json:"urls" yaml:"urls"`
	Encoding      Encoding     `json:"encoding" yaml:"encoding"`
	HTTP          HTTPConfig   `json:"http" yaml:"http"`
	Auth          Auth         `json:"auth" yaml:"auth"`
	Item          ItemFlow     `json:"item" yaml:"item"`
	Topic         TopicFlow    `json:"topic" yaml:"topic"`
	Download      DownloadFlow `json:"download" yaml:"download"`
	Source        string       `json:"-" yaml:"-"`
}

type Defaults struct {
	AccessMode         string `json:"access_mode" yaml:"access_mode"`
	DefaultAccessMode  string `json:"default_access_mode" yaml:"default_access_mode"`
	Timezone           string `json:"timezone" yaml:"timezone"`
	PageTimeoutSeconds int    `json:"page_timeout_seconds" yaml:"page_timeout_seconds"`
}

type URLs struct {
	Base      string `json:"base" yaml:"base"`
	Login     string `json:"login" yaml:"login"`
	AuthCheck string `json:"auth_check" yaml:"auth_check"`
	Topic     string `json:"topic" yaml:"topic"`
	Download  string `json:"download" yaml:"download"`
}

type Encoding struct {
	Response string `json:"response" yaml:"response"`
	Page     string `json:"page" yaml:"page"`
	Target   string `json:"target" yaml:"target"`
	Form     string `json:"form" yaml:"form"`
}

type HTTPConfig struct {
	BaseURL string `json:"base_url" yaml:"base_url"`
	Proxy   string `json:"proxy" yaml:"proxy"`
}

type Auth struct {
	Type      string       `json:"type" yaml:"type"`
	Check     *HTTPRequest `json:"check" yaml:"check"`
	Login     *HTTPRequest `json:"login" yaml:"login"`
	LoginForm *HTTPRequest `json:"login_form" yaml:"login_form"`
	LoggedIn  MatchRules   `json:"logged_in" yaml:"logged_in"`
	Steps     []Step       `json:"steps" yaml:"steps"`
}

type ItemFlow struct {
	ID      ItemID             `json:"id" yaml:"id"`
	Page    HTTPRequest        `json:"page" yaml:"page"`
	Extract map[string]Extract `json:"extract" yaml:"extract"`
	Closed  MatchRules         `json:"closed" yaml:"closed"`
	Steps   []Step             `json:"steps" yaml:"steps"`
}

type ItemID struct {
	FromURL FromURLRule `json:"from_url" yaml:"from_url"`
}

type FromURLRule struct {
	Regex  string `json:"regex" yaml:"regex"`
	Regexp string `json:"regexp" yaml:"regexp"`
}

type TopicFlow struct {
	Ready     ReadyRules `json:"ready" yaml:"ready"`
	Title     Extract    `json:"title" yaml:"title"`
	UpdatedAt Extract    `json:"updated_at" yaml:"updated_at"`
	Closed    MatchRules `json:"closed" yaml:"closed"`
}

type ReadyRules struct {
	URL            URLReady `json:"url" yaml:"url"`
	DOMContains    []string `json:"dom_contains" yaml:"dom_contains"`
	DOMContainsAll []string `json:"dom_contains_all" yaml:"dom_contains_all"`
	DOMContainsAny []string `json:"dom_contains_any" yaml:"dom_contains_any"`
	DOMRegex       string   `json:"dom_regex" yaml:"dom_regex"`
	Regex          string   `json:"regex" yaml:"regex"`
}

type URLReady struct {
	Exact       string   `json:"exact" yaml:"exact"`
	Contains    string   `json:"contains" yaml:"contains"`
	ContainsAny []string `json:"contains_any" yaml:"contains_any"`
}

type DownloadFlow struct {
	URL         string         `json:"url" yaml:"url"`
	URLFromPage Extract        `json:"url_from_page" yaml:"url_from_page"`
	Before      DownloadBefore `json:"before" yaml:"before"`
	Ready       DownloadReady  `json:"ready" yaml:"ready"`
	Request     HTTPRequest    `json:"request" yaml:"request"`
	Validate    Validate       `json:"validate" yaml:"validate"`
	Steps       []Step         `json:"steps" yaml:"steps"`
}

type DownloadBefore struct {
	SetCookie []CookieAction    `json:"set_cookie" yaml:"set_cookie"`
	Headers   map[string]string `json:"headers" yaml:"headers"`
}

type CookieAction struct {
	Name   string `json:"name" yaml:"name"`
	Value  string `json:"value" yaml:"value"`
	Domain string `json:"domain" yaml:"domain"`
	Path   string `json:"path" yaml:"path"`
}

type DownloadReady struct {
	ExpectedFilename MatchRules `json:"expected_filename" yaml:"expected_filename"`
}

type HTTPRequest struct {
	Method       string            `json:"method" yaml:"method"`
	URL          string            `json:"url" yaml:"url"`
	Form         map[string]string `json:"form" yaml:"form"`
	FormEncoding string            `json:"form_encoding" yaml:"form_encoding"`
	Headers      map[string]string `json:"headers" yaml:"headers"`
	Cookies      map[string]string `json:"cookies" yaml:"cookies"`
	Success      MatchRules        `json:"success" yaml:"success"`
}

type MatchRules struct {
	Contains    string   `json:"contains" yaml:"contains"`
	ContainsAll []string `json:"contains_all" yaml:"contains_all"`
	ContainsAny []string `json:"contains_any" yaml:"contains_any"`
	Regex       string   `json:"regex" yaml:"regex"`
	Regexp      string   `json:"regexp" yaml:"regexp"`
}

type Extract struct {
	Selector string        `json:"selector" yaml:"selector"`
	Attr     string        `json:"attr" yaml:"attr"`
	Regex    string        `json:"regex" yaml:"regex"`
	Regexp   string        `json:"regexp" yaml:"regexp"`
	Layout   string        `json:"layout" yaml:"layout"`
	Layouts  []string      `json:"layouts" yaml:"layouts"`
	Locale   string        `json:"locale" yaml:"locale"`
	Timezone string        `json:"timezone" yaml:"timezone"`
	Cleanup  []CleanupRule `json:"cleanup" yaml:"cleanup"`
}

type CleanupRule struct {
	TrimPrefix string `json:"trim_prefix" yaml:"trim_prefix"`
	TrimSuffix string `json:"trim_suffix" yaml:"trim_suffix"`
	Replace    string `json:"replace" yaml:"replace"`
	With       string `json:"with" yaml:"with"`
	Regex      string `json:"regex" yaml:"regex"`
	Regexp     string `json:"regexp" yaml:"regexp"`
}

type Validate struct {
	BencodeTorrent     bool     `json:"bencode_torrent" yaml:"bencode_torrent"`
	MaxSizeMB          int      `json:"max_size_mb" yaml:"max_size_mb"`
	RejectContentTypes []string `json:"reject_content_types" yaml:"reject_content_types"`
	RejectIfStartsWith []string `json:"reject_if_starts_with" yaml:"reject_if_starts_with"`
}

type Step struct {
	Goto            string            `json:"goto" yaml:"goto"`
	Fill            map[string]string `json:"fill" yaml:"fill"`
	Click           string            `json:"click" yaml:"click"`
	WaitForSelector string            `json:"wait_for_selector" yaml:"wait_for_selector"`
	ExtractText     *NamedSelector    `json:"extract_text" yaml:"extract_text"`
	ExtractAttr     *NamedAttr        `json:"extract_attr" yaml:"extract_attr"`
	WaitDownload    bool              `json:"wait_download" yaml:"wait_download"`
}

type NamedSelector struct {
	Name     string `json:"name" yaml:"name"`
	Selector string `json:"selector" yaml:"selector"`
}

type NamedAttr struct {
	Name     string `json:"name" yaml:"name"`
	Selector string `json:"selector" yaml:"selector"`
	Attr     string `json:"attr" yaml:"attr"`
}

type CheckResult struct {
	Updated       bool
	Title         string
	UpdatedAt     *time.Time
	Episode       string
	Closed        bool
	TorrentData   []byte
	Message       string
	SessionCookie string
}
