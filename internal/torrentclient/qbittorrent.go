package torrentclient

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	Kind           string
	Address        string
	Login          string
	Password       string
	Timeout        time.Duration
	SessionCookie  string
	SessionExpires *time.Time
}

func NewFromConfig(cfg Config) (Client, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Kind)) {
	case "qbittorrent", "qbit", "qb":
		return NewQBittorrent(cfg)
	case "", "none":
		return nil, errors.New("torrent client is not configured")
	case "transmission", "deluge":
		return nil, fmt.Errorf("%s adapter is not implemented yet", cfg.Kind)
	default:
		return nil, fmt.Errorf("unsupported torrent client %q", cfg.Kind)
	}
}

type QBittorrent struct {
	baseURL        string
	login          string
	password       string
	http           *http.Client
	sessionCookie  string
	sessionExpires *time.Time
}

type httpStatusError struct {
	operation string
	status    int
	body      string
}

func (e *httpStatusError) Error() string {
	if e.body == "" {
		return fmt.Sprintf("qBittorrent %s failed: HTTP %d", e.operation, e.status)
	}
	return fmt.Sprintf("qBittorrent %s failed: HTTP %d: %s", e.operation, e.status, e.body)
}

func NewQBittorrent(cfg Config) (*QBittorrent, error) {
	base := normalizeBaseURL(cfg.Address)
	if base == "" {
		return nil, errors.New("qBittorrent address is empty")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	q := &QBittorrent{
		baseURL:  strings.TrimRight(base, "/"),
		login:    cfg.Login,
		password: cfg.Password,
		http:     &http.Client{Timeout: timeout, Jar: jar},
	}
	q.restoreSessionCookie(cfg.SessionCookie, cfg.SessionExpires)
	return q, nil
}

func (q *QBittorrent) CheckConnection(ctx context.Context) (CheckResult, error) {
	if err := q.ensureSession(ctx); err != nil {
		return CheckResult{}, err
	}
	version, err := q.appVersion(ctx)
	if err != nil {
		if isAuthError(err) {
			q.clearSession()
			if loginErr := q.loginSession(ctx); loginErr != nil {
				return CheckResult{}, loginErr
			}
			version, err = q.appVersion(ctx)
		}
		if err != nil {
			return CheckResult{}, err
		}
	}
	return CheckResult{Version: version}, nil
}

func (q *QBittorrent) appVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, q.baseURL+"/api/v2/app/version", nil)
	if err != nil {
		return "", err
	}
	q.setCommonHeaders(req)
	resp, err := q.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", statusError(resp, "app/version")
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	version := strings.TrimSpace(string(body))
	if version == "" {
		return "", errors.New("qBittorrent returned empty app version")
	}
	return version, nil
}

func (q *QBittorrent) Add(ctx context.Context, req AddRequest) (AddResult, error) {
	if strings.TrimSpace(req.FileURL) == "" && len(req.FileData) == 0 {
		return AddResult{}, errors.New("torrent URL or torrent file data is required")
	}
	if err := q.ensureSession(ctx); err != nil {
		return AddResult{}, err
	}

	if strings.TrimSpace(req.OldHash) != "" {
		if err := q.Remove(ctx, req.OldHash, req.DeleteFiles); err != nil {
			if isAuthError(err) {
				q.clearSession()
				if loginErr := q.loginSession(ctx); loginErr != nil {
					return AddResult{}, loginErr
				}
				err = q.Remove(ctx, req.OldHash, req.DeleteFiles)
			}
			if err != nil {
				return AddResult{}, err
			}
		}
	}

	expectedHash, _ := torrentInfoHash(req.FileData)
	if err := q.addTorrent(ctx, req); err != nil {
		if isAuthError(err) {
			q.clearSession()
			if loginErr := q.loginSession(ctx); loginErr != nil {
				return AddResult{}, loginErr
			}
			err = q.addTorrent(ctx, req)
		}
		if err != nil {
			if !isConflictError(err) || expectedHash == "" {
				return AddResult{}, err
			}
			exists, lookupErr := q.hasTorrent(ctx, expectedHash)
			if lookupErr != nil {
				return AddResult{}, lookupErr
			}
			if !exists {
				return AddResult{}, err
			}
			return AddResult{Hash: expectedHash}, nil
		}
	}
	if expectedHash != "" {
		return AddResult{Hash: expectedHash}, nil
	}

	hash, err := q.latestHash(ctx)
	if err != nil {
		if isAuthError(err) {
			q.clearSession()
			if loginErr := q.loginSession(ctx); loginErr != nil {
				return AddResult{}, loginErr
			}
			hash, err = q.latestHash(ctx)
		}
		if err != nil {
			return AddResult{}, err
		}
	}
	return AddResult{Hash: hash}, nil
}

func (q *QBittorrent) hasTorrent(ctx context.Context, hash string) (bool, error) {
	form := url.Values{}
	form.Set("hashes", hash)
	resp, err := q.postForm(ctx, "/api/v2/torrents/info", form)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, statusError(resp, "info")
	}
	var items []struct {
		Hash string `json:"hash"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return false, err
	}
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.Hash), hash) {
			return true, nil
		}
	}
	return false, nil
}

func (q *QBittorrent) Remove(ctx context.Context, hash string, deleteFiles bool) error {
	if strings.TrimSpace(hash) == "" {
		return nil
	}
	if err := q.ensureSession(ctx); err != nil {
		return err
	}
	form := url.Values{}
	form.Set("hashes", hash)
	if deleteFiles {
		form.Set("deleteFiles", "true")
	} else {
		form.Set("deleteFiles", "false")
	}
	resp, err := q.postForm(ctx, "/api/v2/torrents/delete", form)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 204 {
		return statusError(resp, "delete")
	}
	return nil
}

func (q *QBittorrent) ensureSession(ctx context.Context) error {
	if q.hasSessionCookie() {
		return nil
	}
	return q.loginSession(ctx)
}

func (q *QBittorrent) loginSession(ctx context.Context) error {
	if q.login == "" && q.password == "" {
		// qBittorrent can be configured with bypass auth for localhost. Try unauthenticated session.
		return nil
	}
	form := url.Values{}
	form.Set("username", q.login)
	form.Set("password", q.password)
	resp, err := q.postForm(ctx, "/api/v2/auth/login", form)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	q.captureSessionCookies(resp.Cookies())

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	bodyText := strings.TrimSpace(string(body))

	// qBittorrent docs describe cookie-based auth: successful login sets an SID cookie.
	// Newer builds may name it QBT_SID_<port> and may answer 204 No Content.
	// Therefore cookie presence, not a hard-coded body/status pair, is the source of truth.
	if resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("qBittorrent login failed: HTTP 403: IP is banned or credentials are rejected")
	}
	if resp.StatusCode == http.StatusUnauthorized {
		if bodyText == "" {
			bodyText = "Unauthorized"
		}
		return fmt.Errorf("qBittorrent login failed: HTTP 401: %s; check saved login/password or paste the password into Settings and try again", bodyText)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 204 {
		return fmt.Errorf("qBittorrent login failed: HTTP %d: %s", resp.StatusCode, bodyText)
	}
	if bodyText == "Fails." {
		return fmt.Errorf("qBittorrent login failed: invalid username or password")
	}
	if bodyText == "Ok." || q.hasSessionCookie() {
		return nil
	}
	return fmt.Errorf("qBittorrent login failed: HTTP %d without SID cookie: %s", resp.StatusCode, bodyText)
}

func (q *QBittorrent) addTorrent(ctx context.Context, req AddRequest) error {
	if len(req.FileData) > 0 {
		name := strings.TrimSpace(req.FileName)
		if name == "" {
			if req.ID > 0 {
				name = fmt.Sprintf("torrent-%d.torrent", req.ID)
			} else {
				name = "torrent.torrent"
			}
		}
		return q.addFile(ctx, req.FileData, name, req.SavePath)
	}
	return q.addURL(ctx, req.FileURL, req.SavePath)
}

func (q *QBittorrent) addURL(ctx context.Context, torrentURL, savePath string) error {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := mw.WriteField("urls", torrentURL); err != nil {
		return err
	}
	if savePath != "" {
		if err := mw.WriteField("savepath", savePath); err != nil {
			return err
		}
	}
	_ = mw.WriteField("autoTMM", "false")
	_ = mw.WriteField("root_folder", "true")
	if err := mw.Close(); err != nil {
		return err
	}

	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, q.baseURL+"/api/v2/torrents/add", &body)
	if err != nil {
		return err
	}
	hreq.Header.Set("Content-Type", mw.FormDataContentType())
	q.setCommonHeaders(hreq)
	resp, err := q.http.Do(hreq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 204 {
		return statusError(resp, "add")
	}
	return nil
}

func (q *QBittorrent) addFile(ctx context.Context, data []byte, filename, savePath string) error {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("torrents", filename)
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if savePath != "" {
		if err := mw.WriteField("savepath", savePath); err != nil {
			return err
		}
	}
	_ = mw.WriteField("autoTMM", "false")
	_ = mw.WriteField("root_folder", "true")
	if err := mw.Close(); err != nil {
		return err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, q.baseURL+"/api/v2/torrents/add", &body)
	if err != nil {
		return err
	}
	hreq.Header.Set("Content-Type", mw.FormDataContentType())
	q.setCommonHeaders(hreq)
	resp, err := q.http.Do(hreq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 204 {
		return statusError(resp, "add")
	}
	return nil
}

func (q *QBittorrent) latestHash(ctx context.Context) (string, error) {
	form := url.Values{}
	form.Set("filter", "all")
	form.Set("limit", "1")
	form.Set("sort", "added_on")
	form.Set("reverse", "true")
	resp, err := q.postForm(ctx, "/api/v2/torrents/info", form)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", statusError(resp, "info")
	}
	var items []struct {
		Hash string `json:"hash"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return "", err
	}
	if len(items) == 0 || items[0].Hash == "" {
		return "", errors.New("qBittorrent did not return torrent hash")
	}
	return items[0].Hash, nil
}

func (q *QBittorrent) postForm(ctx context.Context, path string, form url.Values) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.baseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	q.setCommonHeaders(req)
	return q.http.Do(req)
}

func (q *QBittorrent) setCommonHeaders(req *http.Request) {
	// qBittorrent WebAPI requires Referer or Origin to match the Host/port used
	// for the request. Setting both keeps it compatible with stricter WebUI settings.
	req.Header.Set("Referer", q.baseURL)
	req.Header.Set("Origin", q.baseURL)
}

func (q *QBittorrent) restoreSessionCookie(cookieHeader string, expires *time.Time) {
	cookieHeader = strings.TrimSpace(cookieHeader)
	if cookieHeader == "" {
		return
	}
	if expires != nil && time.Now().After(*expires) {
		return
	}
	u := q.baseParsedURL()
	if u == nil || q.http == nil || q.http.Jar == nil {
		return
	}
	for _, part := range strings.Split(cookieHeader, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || !isSessionCookieName(name) || value == "" {
			continue
		}
		cookie := &http.Cookie{Name: name, Value: value, Path: "/"}
		if expires != nil {
			cookie.Expires = *expires
		}
		q.http.Jar.SetCookies(u, []*http.Cookie{cookie})
		q.sessionCookie = name + "=" + value
		q.sessionExpires = expires
		return
	}
}

func (q *QBittorrent) captureSessionCookies(cookies []*http.Cookie) {
	u := q.baseParsedURL()
	for _, c := range cookies {
		if c == nil || !isSessionCookieName(c.Name) || c.Value == "" {
			continue
		}
		if u != nil && q.http != nil && q.http.Jar != nil {
			q.http.Jar.SetCookies(u, []*http.Cookie{c})
		}
		q.sessionCookie = c.Name + "=" + c.Value
		if !c.Expires.IsZero() {
			exp := c.Expires
			q.sessionExpires = &exp
		} else {
			q.sessionExpires = nil
		}
	}
}

func (q *QBittorrent) hasSessionCookie() bool {
	if q.http == nil || q.http.Jar == nil {
		return false
	}
	u := q.baseParsedURL()
	if u == nil {
		return false
	}
	for _, c := range q.http.Jar.Cookies(u) {
		if isSessionCookieName(c.Name) && c.Value != "" {
			return true
		}
	}
	return false
}

func (q *QBittorrent) clearSession() {
	q.sessionCookie = ""
	q.sessionExpires = nil
	if q.http == nil {
		return
	}
	// qBittorrent may answer 401 for requests that carry an old or invalid
	// QBT_SID_* cookie. Replacing the jar is safer than trying to delete a
	// dynamically named cookie with a possibly different path/domain variant.
	jar, err := cookiejar.New(nil)
	if err == nil {
		q.http.Jar = jar
	}
}

func (q *QBittorrent) Session() Session {
	if q.sessionCookie == "" {
		q.refreshSessionFromJar()
	}
	return Session{Cookie: q.sessionCookie, Expires: q.sessionExpires}
}

func (q *QBittorrent) refreshSessionFromJar() {
	u := q.baseParsedURL()
	if u == nil || q.http == nil || q.http.Jar == nil {
		return
	}
	for _, c := range q.http.Jar.Cookies(u) {
		if isSessionCookieName(c.Name) && c.Value != "" {
			q.sessionCookie = c.Name + "=" + c.Value
			return
		}
	}
}

func (q *QBittorrent) baseParsedURL() *url.URL {
	u, err := url.Parse(q.baseURL)
	if err != nil {
		return nil
	}
	return u
}

func isSessionCookieName(name string) bool {
	upper := strings.ToUpper(strings.TrimSpace(name))
	return upper == "SID" || upper == "QBT_SID" || strings.HasPrefix(upper, "QBT_SID_")
}

func isAuthError(err error) bool {
	var se *httpStatusError
	return errors.As(err, &se) && (se.status == http.StatusForbidden || se.status == http.StatusUnauthorized)
}

func isConflictError(err error) bool {
	var se *httpStatusError
	return errors.As(err, &se) && se.status == http.StatusConflict
}

func torrentInfoHash(data []byte) (string, error) {
	if len(data) == 0 || data[0] != 'd' {
		return "", errors.New("torrent metadata is not a bencoded dictionary")
	}
	for pos := 1; pos < len(data) && data[pos] != 'e'; {
		keyStart, keyEnd, next, err := bencodeString(data, pos)
		if err != nil {
			return "", err
		}
		valueStart := next
		valueEnd, err := skipBencodeValue(data, valueStart)
		if err != nil {
			return "", err
		}
		if string(data[keyStart:keyEnd]) == "info" {
			sum := sha1.Sum(data[valueStart:valueEnd])
			return fmt.Sprintf("%x", sum), nil
		}
		pos = valueEnd
	}
	return "", errors.New("torrent metadata does not contain info dictionary")
}

func skipBencodeValue(data []byte, pos int) (int, error) {
	if pos >= len(data) {
		return 0, errors.New("unexpected end of bencoded data")
	}
	switch data[pos] {
	case 'i':
		end := bytes.IndexByte(data[pos+1:], 'e')
		if end < 0 {
			return 0, errors.New("unterminated bencoded integer")
		}
		return pos + end + 2, nil
	case 'l', 'd':
		pos++
		for pos < len(data) && data[pos] != 'e' {
			var err error
			pos, err = skipBencodeValue(data, pos)
			if err != nil {
				return 0, err
			}
		}
		if pos >= len(data) {
			return 0, errors.New("unterminated bencoded collection")
		}
		return pos + 1, nil
	default:
		_, _, next, err := bencodeString(data, pos)
		return next, err
	}
}

func bencodeString(data []byte, pos int) (start, end, next int, err error) {
	if pos >= len(data) || data[pos] < '0' || data[pos] > '9' {
		return 0, 0, 0, errors.New("invalid bencoded string")
	}
	length := 0
	for pos < len(data) && data[pos] != ':' {
		if data[pos] < '0' || data[pos] > '9' {
			return 0, 0, 0, errors.New("invalid bencoded string length")
		}
		digit := int(data[pos] - '0')
		if length > (len(data)-digit)/10 {
			return 0, 0, 0, errors.New("bencoded string exceeds input")
		}
		length = length*10 + digit
		pos++
	}
	if pos >= len(data) {
		return 0, 0, 0, errors.New("unterminated bencoded string length")
	}
	start = pos + 1
	end = start + length
	if end > len(data) {
		return 0, 0, 0, errors.New("bencoded string exceeds input")
	}
	return start, end, end, nil
}

func statusError(resp *http.Response, op string) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return &httpStatusError{operation: op, status: resp.StatusCode, body: strings.TrimSpace(string(body))}
}

func normalizeBaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	return raw
}
