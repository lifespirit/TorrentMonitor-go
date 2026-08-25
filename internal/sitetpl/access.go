package sitetpl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type siteAccess interface {
	Prepare(ctx context.Context, vars map[string]string, settings Settings) error
	FetchPage(ctx context.Context, spec HTTPRequest, vars map[string]string, settings Settings) ([]byte, error)
	DownloadTorrent(ctx context.Context, spec HTTPRequest, vars map[string]string, settings Settings) ([]byte, error)
	SessionCookie() string
}

func (r *Runner) newSiteAccess(tmpl Template, client *http.Client, cred Credential, browser BrowserPageFetcher) (siteAccess, error) {
	switch normalizeAccessMode(cred.AccessMode) {
	case "chromium":
		if browser == nil {
			return nil, errors.New("chromium access mode requires BrowserBroker")
		}
		return &chromiumSiteAccess{tmpl: tmpl, browser: browser}, nil
	case "native":
		fallthrough
	default:
		return &nativeSiteAccess{runner: r, tmpl: tmpl, client: client, cred: cred}, nil
	}
}

func normalizeAccessMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "chromium", "browser":
		return "chromium"
	default:
		return "native"
	}
}

type nativeSiteAccess struct {
	runner *Runner
	tmpl   Template
	client *http.Client
	cred   Credential
}

func (a *nativeSiteAccess) Prepare(ctx context.Context, vars map[string]string, settings Settings) error {
	if err := applyCredentialCookies(a.client, a.tmpl, a.cred.Cookie); err != nil {
		return err
	}

	needLogin := a.tmpl.Auth.Login != nil && strings.TrimSpace(a.cred.Login) != ""
	if a.tmpl.Auth.Check != nil && strings.TrimSpace(a.cred.Cookie) != "" {
		if _, err := a.runner.do(ctx, a.client, *a.tmpl.Auth.Check, vars, settings); err == nil {
			needLogin = false
		}
	}
	if needLogin {
		if _, err := a.runner.do(ctx, a.client, *a.tmpl.Auth.Login, vars, settings); err != nil {
			return fmt.Errorf("auth login: %w", err)
		}
	}
	return nil
}

func (a *nativeSiteAccess) FetchPage(ctx context.Context, spec HTTPRequest, vars map[string]string, settings Settings) ([]byte, error) {
	return a.runner.do(ctx, a.client, spec, vars, settings)
}

func (a *nativeSiteAccess) DownloadTorrent(ctx context.Context, spec HTTPRequest, vars map[string]string, settings Settings) ([]byte, error) {
	return a.runner.do(ctx, a.client, spec, vars, settings)
}

func (a *nativeSiteAccess) SessionCookie() string {
	return cookieHeaderForTemplate(a.client, a.tmpl)
}

type chromiumSiteAccess struct {
	tmpl    Template
	browser BrowserPageFetcher
}

func (a *chromiumSiteAccess) Prepare(ctx context.Context, vars map[string]string, settings Settings) error {
	if a.browser == nil {
		return errors.New("chromium access mode requires BrowserBroker")
	}
	return nil
}

func (a *chromiumSiteAccess) FetchPage(ctx context.Context, spec HTTPRequest, vars map[string]string, settings Settings) ([]byte, error) {
	if a.browser == nil {
		return nil, errors.New("chromium access mode requires BrowserBroker")
	}
	rawURL := render(spec.URL, vars)
	if strings.TrimSpace(rawURL) == "" {
		return nil, errors.New("browser page URL is empty")
	}
	if !requestCanUseBrowser(spec, rawURL) {
		return nil, fmt.Errorf("chromium page fetch supports browser-loadable GET URLs only: method=%q url=%q", spec.Method, rawURL)
	}
	if waiter, ok := a.browser.(BrowserPageWaiter); ok {
		success := renderMatchRules(spec.Success, vars)
		return waiter.FetchPageWait(ctx, a.tmpl.Site, rawURL, settings.Timeout, expectedURLNeedles(rawURL), requiredAllMarkers(success), requiredAnyMarkers(success), strings.TrimSpace(success.Regex))
	}
	return a.browser.FetchPage(ctx, a.tmpl.Site, rawURL)
}

func (a *chromiumSiteAccess) DownloadTorrent(ctx context.Context, spec HTTPRequest, vars map[string]string, settings Settings) ([]byte, error) {
	if a.browser == nil {
		return nil, errors.New("chromium access mode requires BrowserBroker")
	}
	downloader, ok := a.browser.(BrowserDownloader)
	if !ok {
		return nil, errors.New("chromium access mode requires BrowserBroker download support")
	}
	rawURL := render(spec.URL, vars)
	if strings.TrimSpace(rawURL) == "" {
		return nil, errors.New("browser download URL is empty")
	}
	return downloader.Download(ctx, a.tmpl.Site, rawURL, renderedHeaders(spec.Headers, vars), renderedCookieHeader(spec.Cookies, vars))
}

func (a *chromiumSiteAccess) SessionCookie() string { return "" }

func cloneClientWithTimeout(client *http.Client, settings Settings) *http.Client {
	if settings.Timeout <= 0 || client.Timeout == settings.Timeout {
		return client
	}
	clone := *client
	clone.Timeout = settings.Timeout
	if clone.Jar == nil {
		jar, _ := cookiejar.New(nil)
		clone.Jar = jar
	}
	return &clone
}

func cloneClientForCredential(client *http.Client, settings Settings, cred Credential) (*http.Client, error) {
	clone := cloneClientWithTimeout(client, settings)
	useProxy := settings.ProxyEnabled || cred.UseProxy
	if !useProxy || strings.TrimSpace(settings.ProxyAddress) == "" {
		return clone, nil
	}
	withProxy := *clone
	transport, err := transportWithProxy(settings)
	if err != nil {
		return nil, err
	}
	withProxy.Transport = transport
	if withProxy.Jar == nil {
		jar, _ := cookiejar.New(nil)
		withProxy.Jar = jar
	}
	return &withProxy, nil
}

func transportWithProxy(settings Settings) (*http.Transport, error) {
	return ProxyTransport(settings.ProxyType, settings.ProxyAddress)
}

// ProxyTransport builds an HTTP transport for the proxy formats supported by
// TorrentMonitor. It is shared by tracker access and notification adapters.
func ProxyTransport(proxyType, proxyAddress string) (*http.Transport, error) {
	proxyType = strings.ToLower(strings.TrimSpace(proxyType))
	proxyAddr := strings.TrimSpace(proxyAddress)
	if proxyAddr == "" {
		return http.DefaultTransport.(*http.Transport).Clone(), nil
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	switch proxyType {
	case "", "http", "https":
		proxyURL, err := parseProxyURL(proxyAddr, "http")
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	case "socks", "socks5":
		addr, err := proxyHostPort(proxyAddr)
		if err != nil {
			return nil, err
		}
		dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, "tcp", addr)
			if err != nil {
				return nil, err
			}
			if err := socks5Connect(ctx, conn, address); err != nil {
				_ = conn.Close()
				return nil, err
			}
			return conn, nil
		}
		transport.Proxy = nil
	default:
		return nil, fmt.Errorf("unsupported proxy type %q", proxyType)
	}
	return transport, nil
}

func parseProxyURL(raw string, defaultScheme string) (*url.URL, error) {
	if !strings.Contains(raw, "://") {
		raw = defaultScheme + "://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse proxy address: %w", err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("proxy address %q has no host", raw)
	}
	return u, nil
}

func proxyHostPort(raw string) (string, error) {
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", fmt.Errorf("parse proxy address: %w", err)
		}
		raw = u.Host
	}
	if _, _, err := net.SplitHostPort(raw); err == nil {
		return raw, nil
	}
	return "", fmt.Errorf("proxy address must be host:port, got %q", raw)
}

func socks5Connect(ctx context.Context, conn net.Conn, target string) error {
	deadline, ok := ctx.Deadline()
	if ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	}
	defer conn.SetDeadline(time.Time{})

	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return err
	}
	buf := make([]byte, 262)
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return err
	}
	if buf[0] != 0x05 || buf[1] != 0x00 {
		return fmt.Errorf("SOCKS5 proxy rejected no-auth method")
	}
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 0 || port > 65535 {
		return fmt.Errorf("invalid target port %q", portText)
	}
	req := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			req = append(req, 0x01)
			req = append(req, v4...)
		} else {
			req = append(req, 0x04)
			req = append(req, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return fmt.Errorf("target host is too long for SOCKS5")
		}
		req = append(req, 0x03, byte(len(host)))
		req = append(req, host...)
	}
	req = append(req, byte(port>>8), byte(port))
	if _, err := conn.Write(req); err != nil {
		return err
	}
	if _, err := io.ReadFull(conn, buf[:4]); err != nil {
		return err
	}
	if buf[0] != 0x05 {
		return fmt.Errorf("invalid SOCKS5 response version %d", buf[0])
	}
	if buf[1] != 0x00 {
		return fmt.Errorf("SOCKS5 connect failed with code %d", buf[1])
	}
	var skip int
	switch buf[3] {
	case 0x01:
		skip = 4
	case 0x03:
		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return err
		}
		skip = int(buf[0])
	case 0x04:
		skip = 16
	default:
		return fmt.Errorf("invalid SOCKS5 address type %d", buf[3])
	}
	if skip > 0 {
		if _, err := io.ReadFull(conn, buf[:skip]); err != nil {
			return err
		}
	}
	_, err = io.ReadFull(conn, buf[:2])
	return err
}

func matchSuccess(page string, rules MatchRules) bool {
	rules = normalizeMatchRules(rules)
	if needle := strings.TrimSpace(rules.Contains); needle != "" && !strings.Contains(page, needle) {
		return false
	}
	for _, needle := range rules.ContainsAll {
		needle = strings.TrimSpace(needle)
		if needle != "" && !strings.Contains(page, needle) {
			return false
		}
	}
	if len(rules.ContainsAny) > 0 {
		matchedAny := false
		for _, needle := range rules.ContainsAny {
			needle = strings.TrimSpace(needle)
			if needle != "" && strings.Contains(page, needle) {
				matchedAny = true
				break
			}
		}
		if !matchedAny {
			return false
		}
	}
	if strings.TrimSpace(rules.Regex) != "" {
		matched, err := regexp.MatchString(rules.Regex, page)
		return err == nil && matched
	}
	return true
}

func renderMatchRules(rules MatchRules, vars map[string]string) MatchRules {
	rules = normalizeMatchRules(rules)
	out := MatchRules{Contains: render(rules.Contains, vars), Regex: render(rules.Regex, vars)}
	if len(rules.ContainsAll) > 0 {
		out.ContainsAll = make([]string, 0, len(rules.ContainsAll))
		for _, needle := range rules.ContainsAll {
			out.ContainsAll = append(out.ContainsAll, render(needle, vars))
		}
	}
	if len(rules.ContainsAny) > 0 {
		out.ContainsAny = make([]string, 0, len(rules.ContainsAny))
		for _, needle := range rules.ContainsAny {
			out.ContainsAny = append(out.ContainsAny, render(needle, vars))
		}
	}
	return out
}

func requiredAllMarkers(rules MatchRules) []string {
	out := make([]string, 0, len(rules.ContainsAll)+1)
	if needle := strings.TrimSpace(rules.Contains); needle != "" {
		out = append(out, needle)
	}
	for _, needle := range rules.ContainsAll {
		if strings.TrimSpace(needle) != "" {
			out = append(out, strings.TrimSpace(needle))
		}
	}
	return out
}

func requiredAnyMarkers(rules MatchRules) []string {
	out := make([]string, 0, len(rules.ContainsAny))
	for _, needle := range rules.ContainsAny {
		needle = strings.TrimSpace(needle)
		if needle != "" {
			out = append(out, needle)
		}
	}
	return out
}

func expectedURLNeedles(rawURL string) []string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil
	}
	return []string{rawURL}
}
