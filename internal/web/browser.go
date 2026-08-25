package web

import (
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"torrentmonitor-go/internal/browserbroker"
)

func (s *Server) browserSessionPage(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/browser/session/")
	id = cleanBrowserSessionID(id)
	if id == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = browserViewerTemplate.Execute(w, map[string]string{"ID": id})
}

func (s *Server) apiBrowserSessions(w http.ResponseWriter, r *http.Request) {
	if s.browser == nil {
		writeError(w, http.StatusServiceUnavailable, "browser broker is not configured")
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, map[string]any{"items": s.browser.List()}, nil)
		return
	}
	var req browserbroker.OpenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.browser.Open(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSONStatus(w, http.StatusCreated, result)
}

func (s *Server) apiBrowserSessionByID(w http.ResponseWriter, r *http.Request) {
	if s.browser == nil {
		writeError(w, http.StatusServiceUnavailable, "browser broker is not configured")
		return
	}
	id, action := browserPathIDAction(r.URL.Path)
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing browser session id")
		return
	}
	if r.Method == http.MethodGet && action == "" {
		info, err := s.browser.Get(id)
		writeBrowserJSON(w, info, err)
		return
	}
	if r.Method == http.MethodGet && action == "frame" {
		data, typ, err := s.browser.Frame(id)
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, browserbroker.ErrSessionNotFound) {
				status = http.StatusNotFound
			}
			writeError(w, status, err.Error())
			return
		}
		w.Header().Set("Content-Type", typ)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
		return
	}
	if r.Method == http.MethodPost && action == "input" {
		var ev browserbroker.InputEvent
		if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		err := s.browser.Input(r.Context(), id, ev)
		writeBrowserJSON(w, map[string]any{"ok": err == nil}, err)
		return
	}
	if r.Method == http.MethodPost && action == "navigate" {
		var req struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		err := s.browser.Navigate(r.Context(), id, req.URL)
		writeBrowserJSON(w, map[string]any{"ok": err == nil}, err)
		return
	}
	if r.Method == http.MethodPost && action == "done" {
		info, err := s.browser.Done(r.Context(), id)
		message := "Сессия помечена как проверенная. Повтори проверку логина или темы."
		if err == nil {
			if info.LooksLikeCloudflare {
				message = "Cloudflare/challenge ещё активен. Сессия оставлена открытой; продолжи проверку в этом окне и нажми «Готово» позже."
			} else if info.LooksLikeLoginPage {
				message = "Cloudflare, похоже, пройден, но сайт всё ещё показывает страницу входа. Войди на сайт и нажми «Готово» снова."
			} else {
				if strings.TrimSpace(info.Tracker) != "" {
					if _, clearErr := s.core.ClearWarnings(r.Context(), info.Tracker); clearErr != nil {
						s.logger.Warn("failed to clear tracker warnings after browser session done", "tracker", info.Tracker, "error", clearErr)
					}
				}
				if info.HasCloudflareClearance {
					message = "Сессия помечена как проверенная. В профиле найден cf_clearance; ошибки трекера очищены. Повтори проверку темы."
				} else if len(info.CookieNames) > 0 {
					message = "Сессия помечена как проверенная. Cookies в профиле есть; ошибки трекера очищены. Повтори проверку темы."
				} else {
					message = "Сессия помечена как проверенная, но cookies в профиле не обнаружены. Ошибки трекера очищены; повтори проверку темы."
				}
			}
		}
		writeBrowserJSON(w, map[string]any{"ok": err == nil, "session": info, "message": message}, err)
		return
	}
	if r.Method == http.MethodDelete && action == "" {
		err := s.browser.Close(id)
		writeBrowserJSON(w, map[string]any{"ok": err == nil}, err)
		return
	}
	writeError(w, http.StatusNotFound, "unknown browser session action")
}

func writeBrowserJSON(w http.ResponseWriter, v any, err error) {
	if err != nil {
		if errors.Is(err, browserbroker.ErrSessionNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, v, nil)
}

func browserPathIDAction(path string) (string, string) {
	raw := strings.TrimPrefix(path, "/api/v1/browser/sessions/")
	parts := strings.Split(strings.Trim(raw, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", ""
	}
	id := cleanBrowserSessionID(parts[0])
	if len(parts) == 1 {
		return id, ""
	}
	return id, parts[1]
}

func cleanBrowserSessionID(id string) string {
	id = strings.Trim(id, "/")
	if decoded, err := url.PathUnescape(id); err == nil {
		id = decoded
	}
	id = strings.TrimSpace(id)
	id = strings.Trim(id, "\"'")
	return id
}

var browserViewerTemplate = template.Must(template.New("browser-viewer").Parse(`<!doctype html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>TorrentMonitor Browser Session</title>
<style>
  :root { color-scheme: dark; font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background:#10141d; color:#edf2ff; }
  body { margin:0; min-height:100vh; display:flex; flex-direction:column; }
  header { display:flex; gap:12px; align-items:center; flex-wrap:wrap; padding:12px 16px; border-bottom:1px solid #263246; background:#151b27; }
  header strong { margin-right:auto; }
  button { border:1px solid #46566f; background:#202a3b; color:#edf2ff; border-radius:8px; padding:8px 12px; cursor:pointer; }
  button:hover { background:#2b3850; }
  #address { flex:1 1 520px; min-width:220px; border:1px solid #46566f; background:#0f1520; color:#edf2ff; border-radius:8px; padding:8px 10px; }
  #status { color:#9fb0ca; font-size:14px; }
  main { flex:1; display:grid; place-items:center; overflow:auto; padding:12px; }
  #screen { background:#000; max-width:100%; height:auto; outline:none; border:1px solid #263246; box-shadow:0 16px 50px rgba(0,0,0,.35); }
  .hint { color:#9fb0ca; padding:8px 16px; font-size:13px; border-top:1px solid #263246; background:#151b27; }
</style>
</head>
<body>
<header>
  <strong>Browser session {{.ID}}</strong>
  <span id="status">подключение…</span>
  <button id="done" type="button">Продолжить</button>
  <button id="close" type="button">Закрыть вкладку</button>
  <input id="address" type="text" aria-label="Адрес" placeholder="https://chromewebstore.google.com/">
  <button id="navigate" type="button">Перейти</button>
</header>
<main>
  <img id="screen" tabindex="0" alt="Chromium screencast">
</main>
<div class="hint">Клики, колесо и клавиатура отправляются в серверный Chromium через CDP. Если кадр не появился сразу — подожди несколько секунд, пока Chromium стартует.</div>
<script>
const id = {{printf "%q" .ID}};
const img = document.getElementById('screen');
const statusEl = document.getElementById('status');
const addressEl = document.getElementById('address');
let viewportW = 0;
let viewportH = 0;

async function refreshInfo() {
  try {
    const r = await fetch('/api/v1/browser/sessions/' + encodeURIComponent(id), {cache:'no-store'});
    const j = await r.json();
    if (!r.ok) throw new Error(j.message || r.statusText);
    viewportW = Number(j.width || viewportW || 0);
    viewportH = Number(j.height || viewportH || 0);
    const cookieStatus = j.has_cloudflare_clearance ? ' · cf_clearance' : (j.cookie_names && j.cookie_names.length ? ' · cookies: ' + j.cookie_names.join(',') : '');
    statusEl.textContent = (j.status || 'unknown') + ' · viewport ' + (viewportW || '?') + 'x' + (viewportH || '?') + cookieStatus;
    const currentURL = j.current_url || j.url || '';
    if (document.activeElement !== addressEl && currentURL) addressEl.value = currentURL;
    document.getElementById('done').hidden = j.tracker === 'torrentmonitor-extensions';
  } catch (e) {
    statusEl.textContent = e.message;
  }
}

function refreshFrame() {
  img.src = '/api/v1/browser/sessions/' + encodeURIComponent(id) + '/frame?t=' + Date.now();
}
img.addEventListener('load', () => { statusEl.textContent = 'кадр получен'; });
img.addEventListener('error', () => { /* frame may be not ready yet */ });

async function send(ev) {
  try {
    await fetch('/api/v1/browser/sessions/' + encodeURIComponent(id) + '/input', {
      method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify(ev)
    });
  } catch (_) {}
}
function clamp(v, min, max) { return Math.max(min, Math.min(max, v)); }
function coords(e) {
  const r = img.getBoundingClientRect();
  const srcW = viewportW || img.naturalWidth || r.width || 1;
  const srcH = viewportH || img.naturalHeight || r.height || 1;
  const relX = r.width ? (e.clientX - r.left) / r.width : 0;
  const relY = r.height ? (e.clientY - r.top) / r.height : 0;
  return {
    x: clamp(relX * srcW, 0, Math.max(0, srcW - 1)),
    y: clamp(relY * srcH, 0, Math.max(0, srcH - 1))
  };
}
img.addEventListener('mousedown', e => {
  img.focus();
  const p = coords(e);
  send({kind:'mouse', type:'mouseMoved', x:p.x, y:p.y, button:'none'});
  send({kind:'mouse', type:'mousePressed', x:p.x, y:p.y, button:'left', click_count:1});
  e.preventDefault();
});
img.addEventListener('mouseup', e => {
  const p = coords(e);
  send({kind:'mouse', type:'mouseReleased', x:p.x, y:p.y, button:'left', click_count:1});
  e.preventDefault();
});
img.addEventListener('mousemove', e => {
  const p = coords(e);
  send({kind:'mouse', type:'mouseMoved', x:p.x, y:p.y, button:e.buttons ? 'left' : 'none'});
});
img.addEventListener('wheel', e => { const p=coords(e); send({kind:'mouse', type:'mouseWheel', x:p.x, y:p.y, delta_x:e.deltaX, delta_y:e.deltaY}); e.preventDefault(); }, {passive:false});
function cdpModifiers(e) {
  let m = 0;
  if (e.altKey) m |= 1;
  if (e.ctrlKey) m |= 2;
  if (e.metaKey) m |= 4;
  if (e.shiftKey) m |= 8;
  return m;
}
function keyVK(e) {
  return e.keyCode || e.which || 0;
}
function keyPayload(e, type) {
  const vk = keyVK(e);
  const key = e.key || '';
  const code = e.code || '';
  const printable = key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey;
  const enterText = key === 'Enter' ? '\r' : '';
  let unmodified = '';
  if (printable) {
    unmodified = key;
  } else if (enterText) {
    unmodified = enterText;
  }
  return {
    kind: 'keyboard',
    type,
    key,
    code,
    modifiers: cdpModifiers(e),
    windows_virtual_key_code: vk,
    native_virtual_key_code: vk,
    location: e.location || 0,
    auto_repeat: !!e.repeat,
    is_keypad: (e.location || 0) === 3,
    text: type === 'char' ? (printable ? key : enterText) : (enterText || ''),
    unmodified_text: unmodified
  };
}
function ownKeyboardEvent(e) {
  return document.activeElement === img;
}
document.addEventListener('keydown', e => {
  if (!ownKeyboardEvent(e)) return;
  const printable = e.key && e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey;
  send(keyPayload(e, 'rawKeyDown'));
  if (printable) {
    send(keyPayload(e, 'char'));
  }
  statusEl.textContent = 'key ' + (e.ctrlKey ? 'Ctrl+' : '') + (e.shiftKey ? 'Shift+' : '') + (e.altKey ? 'Alt+' : '') + (e.metaKey ? 'Meta+' : '') + (e.key || e.code);
  e.preventDefault();
  e.stopPropagation();
}, true);
document.addEventListener('keyup', e => {
  if (!ownKeyboardEvent(e)) return;
  send(keyPayload(e, 'keyUp'));
  e.preventDefault();
  e.stopPropagation();
}, true);
document.addEventListener('paste', e => {
  if (!ownKeyboardEvent(e)) return;
  const text = (e.clipboardData || window.clipboardData)?.getData('text') || '';
  if (text) send({kind:'keyboard', type:'insertText', text});
  e.preventDefault();
  e.stopPropagation();
}, true);
async function navigate() {
  const url = addressEl.value.trim();
  if (!url) return;
  const r = await fetch('/api/v1/browser/sessions/' + encodeURIComponent(id) + '/navigate', {
    method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({url})
  });
  const j = await r.json();
  if (!r.ok) alert(j.message || r.statusText);
}
document.getElementById('navigate').addEventListener('click', navigate);
addressEl.addEventListener('keydown', e => {
  if (e.key === 'Enter') { e.preventDefault(); navigate(); }
});
document.getElementById('done').addEventListener('click', async () => {
  const r = await fetch('/api/v1/browser/sessions/' + encodeURIComponent(id) + '/done', {method:'POST'});
  const j = await r.json();
  alert(j.message || 'Готово');
  if (r.ok && j.session && j.session.status === 'user_done') window.close();
});
document.getElementById('close').addEventListener('click', async () => {
  await fetch('/api/v1/browser/sessions/' + encodeURIComponent(id), {method:'DELETE'});
  window.close();
});
refreshInfo(); refreshFrame();
setInterval(refreshInfo, 3000);
setInterval(refreshFrame, 450);
</script>
</body>
</html>`))
