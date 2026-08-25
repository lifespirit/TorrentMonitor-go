package web

import (
	"encoding/json"
	"net/http"
	"strconv"

	"torrentmonitor-go/internal/core"
)

type legacyResponse struct {
	Error bool   `json:"error"`
	Msg   string `json:"msg,omitempty"`
}

func (s *Server) legacyAction(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeLegacy(w, true, err.Error())
		return
	}
	action := r.FormValue("action")

	switch action {
	case "enter":
		// Compatibility placeholder. Real auth will validate settings.password and set signed session cookie.
		http.SetCookie(w, &http.Cookie{Name: "TM", Value: "dev-session", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
		writeLegacy(w, false, "")
	case "torrent_add":
		item, err := s.core.AddTorrent(r.Context(), core.AddTorrentRequest{
			Kind:         core.TorrentKindTheme,
			URL:          r.FormValue("url"),
			Name:         r.FormValue("name"),
			Path:         r.FormValue("path"),
			UpdateHeader: parseLegacyBool(r.FormValue("update_header")),
		})
		if err != nil {
			writeLegacy(w, true, err.Error())
			return
		}
		writeLegacy(w, false, "Тема добавлена для мониторинга: "+item.Name)
	case "serial_add":
		hd, _ := strconv.Atoi(r.FormValue("hd"))
		item, err := s.core.AddTorrent(r.Context(), core.AddTorrentRequest{
			Kind:    core.TorrentKindSeries,
			Tracker: r.FormValue("tracker"),
			Name:    r.FormValue("name"),
			Path:    r.FormValue("path"),
			HD:      hd,
		})
		if err != nil {
			writeLegacy(w, true, err.Error())
			return
		}
		writeLegacy(w, false, "Сериал добавлен для мониторинга: "+item.Name)
	case "update_credentials":
		id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
		password := r.FormValue("pass")
		patch := core.UpdateCredentialRequest{
			Login:   stringPtr(r.FormValue("log")),
			Passkey: stringPtr(r.FormValue("passkey")),
			Cookie:  stringPtr(r.FormValue("cookie")),
		}
		if password != "" {
			patch.Password = &password
		}
		if _, err := s.core.UpdateCredential(r.Context(), id, patch); err != nil {
			writeLegacy(w, true, err.Error())
			return
		}
		writeLegacy(w, false, "Учётные данные обновлены.")

	case "update_settings":
		timeout, _ := strconv.Atoi(r.FormValue("httpTimeout"))
		patch := core.UpdateSettingsRequest{
			Proxy:              boolPtr(parseLegacyBool(r.FormValue("proxy"))),
			ProxyAddress:       stringPtr(r.FormValue("proxyAddress")),
			ProxyType:          stringPtr(r.FormValue("proxyType")),
			UseTorrent:         boolPtr(parseLegacyBool(r.FormValue("useTorrent"))),
			TorrentClient:      stringPtr(r.FormValue("torrentClient")),
			TorrentAddress:     stringPtr(r.FormValue("torrentAddress")),
			TorrentLogin:       stringPtr(r.FormValue("torrentLogin")),
			PathToDownload:     stringPtr(r.FormValue("pathToDownload")),
			DeleteOldFiles:     boolPtr(parseLegacyBool(r.FormValue("deleteOldFiles"))),
			DeleteDistribution: boolPtr(parseLegacyBool(r.FormValue("deleteDistribution"))),
			ServerAddress:      stringPtr(r.FormValue("serverAddress")),
			Debug:              boolPtr(parseLegacyBool(r.FormValue("debug"))),
			RSS:                boolPtr(parseLegacyBool(r.FormValue("rss"))),
			AutoUpdate:         boolPtr(parseLegacyBool(r.FormValue("autoUpdate"))),
			HTTPTimeoutSeconds: intPtr(timeout),
			UserAgent:          stringPtr(r.FormValue("userAgent")),
		}
		if password := r.FormValue("torrentPassword"); password != "" {
			patch.TorrentPassword = &password
		}
		if _, err := s.core.UpdateSettings(r.Context(), patch); err != nil {
			writeLegacy(w, true, err.Error())
			return
		}
		writeLegacy(w, false, "Настройки сохранены.")
	case "del":
		id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
		if err := s.core.DeleteTorrent(r.Context(), id); err != nil {
			writeLegacy(w, true, err.Error())
			return
		}
		writeLegacy(w, false, "Тема удалена.")
	case "clear_warnings":
		n, err := s.core.ClearWarnings(r.Context(), r.FormValue("tracker"))
		if err != nil {
			writeLegacy(w, true, err.Error())
			return
		}
		writeLegacy(w, false, "Ошибки очищены: "+strconv.Itoa(n))
	case "markNews":
		id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
		if err := s.core.MarkNewsRead(r.Context(), id); err != nil {
			writeLegacy(w, true, err.Error())
			return
		}
		writeLegacy(w, false, "")
	case "order":
		writeJSON(w, "ok", nil)
	case "item_data":
		id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
		item, err := s.core.GetTorrent(r.Context(), id)
		writeJSON(w, item, err)
	default:
		writeLegacy(w, true, "Legacy action is not implemented yet: "+action)
	}
}

func (s *Server) legacyIncludeGone(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusGone)
	_, _ = w.Write([]byte(`<div class="torrents-fs --error"><div>Этот Go UI больше не использует серверные include/*.php. Используйте /api/v1/*.</div></div>`))
}

func writeLegacy(w http.ResponseWriter, isError bool, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(legacyResponse{Error: isError, Msg: msg})
}

func parseLegacyBool(v string) bool {
	return v == "1" || v == "true" || v == "on" || v == "yes"
}

func stringPtr(v string) *string { return &v }
func boolPtr(v bool) *bool       { return &v }
func intPtr(v int) *int          { return &v }
