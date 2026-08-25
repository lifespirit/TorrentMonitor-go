package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"torrentmonitor-go/internal/core"
)

const sessionCookieName = "tm_session"

var publicPaths = map[string]bool{
	"/":                   true,
	"/api/v1/bootstrap":   true,
	"/api/v1/auth/status": true,
	"/api/v1/auth/login":  true,
}

type authSession struct {
	ExpiresAt time.Time
}

type contextKey string

const authContextKey contextKey = "authenticated"

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") || publicPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		if s.isAuthenticated(r) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authContextKey, true)))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusUnauthorized, "Требуется вход в TorrentMonitor")
			return
		}
		http.Redirect(w, r, "/", http.StatusFound)
	})
}

func (s *Server) apiAuthStatus(w http.ResponseWriter, r *http.Request) {
	settings, _ := s.core.GetSettings(r.Context())
	enabled := authEnabled(settings)
	writeJSON(w, map[string]any{
		"enabled":       enabled,
		"password_set":  strings.TrimSpace(settings.AuthPasswordHash) != "",
		"authenticated": !enabled || s.isAuthenticated(r),
	}, nil)
}

func (s *Server) apiAuthLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	settings, err := s.core.GetSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !authEnabled(settings) {
		writeJSON(w, map[string]any{"ok": true, "message": "Авторизация выключена."}, nil)
		return
	}
	if !core.VerifyPassword(req.Password, settings.AuthPasswordHash) {
		writeError(w, http.StatusUnauthorized, "Неверный пароль")
		return
	}
	token, err := randomToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	expires := time.Now().Add(14 * 24 * time.Hour)
	s.sessionMu.Lock()
	s.sessions[token] = authSession{ExpiresAt: expires}
	s.sessionMu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
	})
	writeJSON(w, map[string]any{"ok": true, "message": "Вход выполнен."}, nil)
}

func (s *Server) apiAuthLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		s.sessionMu.Lock()
		delete(s.sessions, c.Value)
		s.sessionMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
	writeJSON(w, map[string]any{"ok": true, "message": "Выход выполнен."}, nil)
}

func (s *Server) isAuthenticated(r *http.Request) bool {
	settings, err := s.core.GetSettings(r.Context())
	if err != nil || !authEnabled(settings) {
		return true
	}
	c, err := r.Cookie(sessionCookieName)
	if err != nil || strings.TrimSpace(c.Value) == "" {
		return false
	}
	now := time.Now()
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	for token, sess := range s.sessions {
		if sess.ExpiresAt.Before(now) {
			delete(s.sessions, token)
		}
	}
	sess, ok := s.sessions[c.Value]
	return ok && sess.ExpiresAt.After(now)
}

func authEnabled(settings core.Settings) bool {
	return settings.Auth && strings.TrimSpace(settings.AuthPasswordHash) != ""
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func authenticatedFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(authContextKey).(bool)
	return v
}
