const state = {
  page: "torrents",
  sort: "name",
  dir: "asc",
  filter: "",
  torrents: [],
  torrentPage: 1,
  torrentPageSize: 10,
  warnings: [],
  credentials: [],
  scheduler: {jobs: []},
  settings: null,
  templates: null,
  bootstrap: null,
  editingItem: null,
}

const $ = (sel) => document.querySelector(sel)
const pageContent = () => $("#page-content")

async function api(path, options = {}) {
  const res = await fetch(path, {
    headers: {"Content-Type": "application/json", ...(options.headers || {})},
    ...options,
  })
  const data = await res.json().catch(() => ({}))
  if (res.status === 401 && path !== "/api/v1/auth/login") {
    state.bootstrap = {auth: {enabled: true, authenticated: false}, counters: {warnings: 0}, version: {app: "", database: ""}}
    showLogin(data.message || "Требуется вход в TorrentMonitor")
    throw new Error(data.message || "Требуется вход в TorrentMonitor")
  }
  if (!res.ok) throw new Error(data.message || `HTTP ${res.status}`)
  return data
}

async function init() {
  await loadBootstrap()
  bindEvents()
  $("#loader").hidden = true
  if (requiresLogin()) showLogin()
  else {
    showApplication()
    await showPage("torrents")
  }
}

async function loadBootstrap() {
  state.bootstrap = await api("/api/v1/bootstrap")
  document.querySelector('[data-bind="version"]').textContent = `${state.bootstrap.version.app} / ${state.bootstrap.version.database}`
  const warningsBadge = $("#warnings-count")
  const warningsCount = Number(state.bootstrap.counters.warnings) || 0
  warningsBadge.textContent = warningsCount
  warningsBadge.hidden = warningsCount === 0
  const logout = $("#logout")
  if (logout) logout.hidden = !(state.bootstrap.auth && state.bootstrap.auth.enabled && state.bootstrap.auth.authenticated)
}

function requiresLogin() {
  return !!(state.bootstrap && state.bootstrap.auth && state.bootstrap.auth.enabled && !state.bootstrap.auth.authenticated)
}

function showLogin(message = "") {
  state.page = "login"
	$("#app").hidden = true
	$("#login-view").hidden = false
	$("#login-password").value = ""
	if (message) showToast(message, "info")
  setTimeout(() => $("#login-password")?.focus(), 0)
}

function showApplication() {
	$("#login-view").hidden = true
	$("#app").hidden = false
}

async function submitLogin(e) {
  e.preventDefault()
  try {
    await api("/api/v1/auth/login", {method: "POST", body: JSON.stringify({password: $("#login-password").value})})
    await loadBootstrap()
	showApplication()
    await showPage("torrents")
  } catch (ex) {
	showToast(ex.message, "error")
  }
}

function bindEvents() {
	$("#login-form").addEventListener("submit", submitLogin)
  document.querySelectorAll(".nav-item").forEach((btn) => {
    btn.addEventListener("click", async () => {
      await showPage(btn.dataset.page)
    })
  })

  $("#filter").addEventListener("input", debounce(async (e) => {
    state.filter = e.target.value
    state.torrentPage = 1
    if (state.page === "torrents") await loadTorrents()
  }, 200))

  $("#sort-name").addEventListener("click", async () => setSort("name"))
  $("#sort-date").addEventListener("click", async () => setSort("date"))
  $("#add-theme").addEventListener("click", () => openItemModal("theme"))
  $("#add-series").addEventListener("click", () => openItemModal("series"))
  $("#run-monitor").addEventListener("click", async () => {
    await api("/api/v1/scheduler/run", {method: "POST", body: JSON.stringify({job: "monitor"})})
    await showPage("scheduler")
  })
  $("#logout")?.addEventListener("click", async () => {
    await api("/api/v1/auth/logout", {method: "POST", body: "{}"}).catch(() => null)
    await loadBootstrap()
    showLogin("Вы вышли из TorrentMonitor")
  })

  $("#modal-close").addEventListener("click", closeItemModal)
  $("#modal-cancel").addEventListener("click", closeItemModal)
  $("#modal-backdrop").addEventListener("click", (e) => {
    if (e.target.id === "modal-backdrop") closeItemModal()
  })
  $("#item-form").addEventListener("submit", submitItemForm)
}

async function setSort(sort) {
  if (state.sort === sort) state.dir = state.dir === "asc" ? "desc" : "asc"
  state.sort = sort
  state.torrentPage = 1
  document.querySelectorAll(".top-bar__sort .btn").forEach((x) => x.classList.remove("--active"))
  $(`#sort-${sort}`).classList.add("--active")
  if (state.page !== "torrents") await showPage("torrents")
  else await loadTorrents()
}

async function showPage(page) {
  if (requiresLogin() && page !== "login") { showLogin(); return }
  state.page = page
  setActiveNav(page)
  setToolbarVisibility(page)

  pageContent().replaceChildren()
  pageContent().innerHTML = `<div class="card c-muted">Загрузка…</div>`

  try {
    if (page === "torrents") await loadTorrents()
    else if (page === "warnings") await loadWarnings()
    else if (page === "credentials") await loadCredentials()
    else if (page === "templates") await loadTemplates()
    else if (page === "settings") await loadSettings()
    else if (page === "scheduler") await loadScheduler()
    else pageContent().innerHTML = `<div class="card c-danger">Неизвестная страница</div>`
  } catch (err) {
    pageContent().innerHTML = `<div class="card c-danger">${escapeHtml(err.message)}</div>`
  }
}

function setActiveNav(page) {
  document.querySelectorAll(".nav-item").forEach((btn) => {
    btn.classList.toggle("--active", btn.dataset.page === page)
  })
}

function setToolbarVisibility(page) {
  $("#torrents-toolbar").hidden = page !== "torrents"
}

async function loadTorrents() {
  const qs = new URLSearchParams({sort: state.sort, dir: state.dir, filter: state.filter})
  const data = await api(`/api/v1/torrents?${qs}`)
  state.torrents = data.items || []
  const totalPages = Math.max(1, Math.ceil(state.torrents.length / state.torrentPageSize))
  state.torrentPage = Math.min(state.torrentPage, totalPages)
  renderTorrents()
}

function renderTorrents() {
  const el = pageContent()
  if (!state.torrents.length) {
    el.innerHTML = `<div class="card c-muted">Нет тем для мониторинга</div>`
    return
  }
  const totalPages = Math.ceil(state.torrents.length / state.torrentPageSize)
  const offset = (state.torrentPage - 1) * state.torrentPageSize
  const pageItems = state.torrents.slice(offset, offset + state.torrentPageSize)
  el.innerHTML = pageItems.map(renderTorrent).join("") + renderTorrentPagination(totalPages)
  el.querySelectorAll("[data-edit]").forEach((btn) => {
    btn.addEventListener("click", async () => openEditModal(btn.dataset.edit))
  })
  el.querySelectorAll("[data-delete]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      if (!confirm("Правда удалить?")) return
      await api(`/api/v1/torrents/${btn.dataset.delete}`, {method: "DELETE"})
      await loadBootstrap()
      await loadTorrents()
    })
  })
  el.querySelectorAll("[data-check]").forEach((btn) => {
    btn.addEventListener("click", async () => checkTorrentItem(btn.dataset.check))
  })
  el.querySelectorAll("[data-torrent-page]").forEach((btn) => {
    btn.addEventListener("click", () => {
      state.torrentPage = Number(btn.dataset.torrentPage)
      renderTorrents()
      window.scrollTo({top: 0, behavior: "smooth"})
    })
  })
}

function renderTorrentPagination(totalPages) {
  if (totalPages <= 1) return ""
  return `
    <nav class="pagination" aria-label="Страницы раздач">
      <button class="btn" type="button" data-torrent-page="${state.torrentPage - 1}" ${state.torrentPage === 1 ? "disabled" : ""}>Назад</button>
      <span class="pagination__status">Страница ${state.torrentPage} из ${totalPages}</span>
      <button class="btn" type="button" data-torrent-page="${state.torrentPage + 1}" ${state.torrentPage === totalPages ? "disabled" : ""}>Вперёд</button>
    </nav>`
}

function renderTorrent(item) {
  const url = item.url || "#"
  const date = item.updated_at ? new Date(item.updated_at).toLocaleString("ru-RU") : "—"
  const quality = item.quality ? `<div class="tm-item__quality">${escapeHtml(item.quality)}</div>` : `<div></div>`
  const err = item.has_error ? `<span class="c-danger">Есть ошибки</span>` : ""
  const closed = item.closed ? `<span class="c-danger">Тема закрыта</span>` : ""
  const paused = item.paused ? `<span class="c-muted">Пауза</span>` : ""
  const hash = item.hash ? `<span class="c-muted">Hash: ${escapeHtml(item.hash.slice(0, 12))}</span>` : ""
  return `
    <div class="tm-item">
      <div class="tracker-icon" title="${escapeHtml(item.tracker)}">${escapeHtml(item.tracker.slice(0, 2))}</div>
      <div>
        <div class="tm-item__title"><a href="${escapeAttr(url)}" target="_blank">${escapeHtml(item.name)}</a></div>
        <div class="tm-item__meta">${escapeHtml(item.tracker)} · ${escapeHtml(item.type)} · ${escapeHtml(item.path || "")}</div>
        <div class="tm-item__meta">${err} ${closed} ${paused} ${hash}</div>
      </div>
      ${quality}
      <div class="tm-item__date">${date}</div>
      <div class="tm-item__actions">
        <button class="icon-btn" type="button" data-check="${item.id}">Проверить</button>
        <button class="icon-btn" type="button" data-edit="${item.id}">Изменить</button>
        <button class="icon-btn icon-btn--danger" type="button" data-delete="${item.id}">Удалить</button>
      </div>
    </div>`
}


async function checkTorrentItem(id) {
  const btn = pageContent().querySelector(`[data-check="${cssEscape(id)}"]`)
  if (btn) {
    btn.disabled = true
    btn.textContent = "Проверяю…"
  }
  try {
    const result = await api(`/api/v1/torrents/${id}/check`, {method: "POST", body: "{}"})
    await loadBootstrap()
    await loadTorrents()
	showToast(torrentCheckMessage(result), result.updated ? "success" : "info", 6500)
  } catch (err) {
    await loadBootstrap()
	showToast(err.message, "error", 8000)
  } finally {
    if (btn) {
      btn.disabled = false
      btn.textContent = "Проверить"
    }
  }
}

function torrentCheckMessage(result) {
  const updatedAt = result.updated_at ? new Date(result.updated_at).toLocaleString("ru-RU") : "—"
	return `${result.message || "Проверка завершена."}\n${result.title || "Без названия"}\nДата: ${updatedAt}; обновление: ${result.updated ? "да" : "нет"}; .torrent: ${Number(result.torrent_size || 0)} байт`
}

function showToast(message, type = "info", timeout = 4500) {
	const region = $("#toast-region")
	const toast = document.createElement("div")
	toast.className = `toast toast--${type}`
	toast.innerHTML = `<div class="toast__message">${escapeHtml(String(message || "")).replaceAll("\n", "<br>")}</div><button type="button" class="toast__close" aria-label="Закрыть">×</button>`
	const close = () => {
		toast.classList.add("toast--leaving")
		setTimeout(() => toast.remove(), 180)
	}
	toast.querySelector("button").addEventListener("click", close)
	region.append(toast)
	setTimeout(close, timeout)
}

async function loadWarnings() {
  const data = await api("/api/v1/warnings")
  state.warnings = data.items || []
  pageContent().innerHTML = `
    <div class="section-actions">
      <button class="btn" type="button" id="clear-warnings">Очистить ошибки</button>
    </div>
    ${state.warnings.map(renderWarning).join("") || `<div class="card c-muted">Ошибок нет</div>`}`
  $("#clear-warnings")?.addEventListener("click", async () => {
    await api("/api/v1/warnings", {method: "DELETE"})
    await loadBootstrap()
    await loadWarnings()
  })
}

function renderWarning(w) {
  const action = w.action_url ? `
    <div class="card-actions">
      <a class="btn btn--primary" href="${escapeAttr(w.action_url)}" target="_blank" rel="noopener noreferrer">${escapeHtml(w.action_label || "Открыть")}</a>
      ${w.tracker ? `<span class="c-muted">Пока ошибка активна, автоматический опрос ${escapeHtml(w.tracker)} пропускается.</span>` : ""}
    </div>` : ""
  const torrent = w.torrent_id ? ` · torrent #${escapeHtml(String(w.torrent_id))}` : ""
  return `
    <div class="card warning-card">
      <div class="card-title c-danger">${escapeHtml(w.where || w.tracker || "system")}${torrent}</div>
      <div>${escapeHtml(w.reason)}</div>
      <div class="c-muted">${new Date(w.time).toLocaleString("ru-RU")}</div>
      ${action}
    </div>`
}

async function loadCredentials() {
  const data = await api("/api/v1/credentials")
  state.credentials = data.items || []
  pageContent().innerHTML = state.credentials.map(renderCredential).join("") || `<div class="card c-muted">Учётные данные не заведены</div>`
  pageContent().querySelectorAll("[data-save-credential]").forEach((btn) => {
    btn.addEventListener("click", async () => saveCredential(btn.dataset.saveCredential))
  })
  pageContent().querySelectorAll("[data-check-credential]").forEach((btn) => {
    btn.addEventListener("click", async () => checkCredentialLogin(btn.dataset.checkCredential))
  })
}

function renderCredential(c) {
  const accessMode = c.access_mode || "native"
  return `
    <div class="card credential-card" data-credential="${c.id}">
      <div class="card-title">${escapeHtml(c.tracker)}</div>
      <div class="credential-grid">
        <label>
          <span>Режим доступа</span>
          <select data-field="access_mode">
            ${option("native", "Native HTTP", accessMode)}
            ${option("chromium", "Chromium", accessMode)}
          </select>
        </label>
        <label>
          <span>Логин</span>
          <input type="text" value="${escapeAttr(c.login || "")}" placeholder="логин" data-field="login">
        </label>
        <label>
          <span>Пароль</span>
          <input type="password" value="" placeholder="оставить пустым, чтобы не менять" data-field="password">
        </label>
        <label>
          <span>Passkey</span>
          <input type="text" value="${escapeAttr(c.passkey || "")}" placeholder="passkey" data-field="passkey">
        </label>
        <label class="check-row form-field--wide">
          <input type="checkbox" data-field="use_proxy" ${c.use_proxy ? "checked" : ""}>
          <span>Использовать proxy из настроек для этого трекера</span>
        </label>
        <div class="form-field form-field--wide c-muted">
          Native использует HTTP-запросы и может сохранять session cookie внутри базы. Chromium открывает реальный браузерный профиль для ручного прохождения CAPTCHA/Cloudflare. Proxy-переключатель трекера принудительно включает proxy из общих настроек для Native HTTP-запросов этого трекера.
        </div>
      </div>
      <div class="credential-footer">
        <div class="c-muted">${escapeHtml(c.type)} · обязательный: ${c.necessarily ? "да" : "нет"}</div>
        <div class="credential-actions">
          <button class="btn" type="button" data-check-credential="${c.id}">Проверить логин</button>
          <button class="btn" type="button" data-save-credential="${c.id}">Сохранить</button>
        </div>
      </div>
    </div>`
}

async function saveCredential(id) {
  const card = pageContent().querySelector(`[data-credential="${cssEscape(id)}"]`)
  const login = card.querySelector('[data-field="login"]').value
  const password = card.querySelector('[data-field="password"]').value
  const passkey = card.querySelector('[data-field="passkey"]').value
  const access_mode = card.querySelector('[data-field="access_mode"]').value
  const use_proxy = card.querySelector('[data-field="use_proxy"]').checked
  const body = {login, passkey, access_mode, use_proxy}
  if (password !== "") body.password = password
  await api(`/api/v1/credentials/${id}`, {method: "PATCH", body: JSON.stringify(body)})
  card.querySelector('[data-field="password"]').value = ""
	showToast("Учётные данные сохранены", "success")
}

async function checkCredentialLogin(id) {
  const card = pageContent().querySelector(`[data-credential="${cssEscape(id)}"]`)
  await saveCredential(id)
  const mode = card.querySelector('[data-field="access_mode"]').value
  const btn = card.querySelector(`[data-check-credential="${cssEscape(id)}"]`)
  btn.disabled = true
  btn.textContent = mode === "chromium" ? "Открываю…" : "Проверяю…"
  try {
    const result = await api(`/api/v1/credentials/${id}/check-login`, {
      method: "POST",
      body: JSON.stringify({open_browser: mode === "chromium", access_mode: mode}),
    })
    const extra = []
    if (result.login_url) extra.push(`URL: ${result.login_url}`)
    if (result.profile_path) extra.push(`Профиль: ${result.profile_path}`)
    if (result.viewer_url) extra.push(`Viewer: ${location.origin}${result.viewer_url}`)
	showToast([result.message || "Проверка завершена.", ...extra].join("\n"), "success", 7000)
    if (result.viewer_url) {
      window.open(result.viewer_url, "tm-browser-" + (result.browser_session_id || id), "noopener,noreferrer")
    }
  } catch (err) {
    await loadBootstrap()
	showToast(err.message, "error", 8000)
  } finally {
    btn.disabled = false
    btn.textContent = "Проверить логин"
  }
}



async function loadSettings() {
  state.settings = await api("/api/v1/settings")
  pageContent().innerHTML = renderSettings(state.settings)
  $("#settings-form").addEventListener("submit", saveSettings)
  $("#torrent-client-check").addEventListener("click", checkTorrentClientConnection)
  $("#browser-extensions")?.addEventListener("click", openBrowserExtensions)
  $("#telegram-check")?.addEventListener("click", checkTelegramNotification)
  const browserMode = $("#set-browser-mode")
  if (browserMode) {
    browserMode.addEventListener("change", updateBrowserSettingsVisibility)
    updateBrowserSettingsVisibility()
  }
	$("#set-auth")?.addEventListener("change", updateAuthSettingsVisibility)
	$("#set-notification-method")?.addEventListener("change", updateNotificationSettingsVisibility)
	$("#set-ui-theme")?.addEventListener("change", (event) => {
		window.TMTheme.setPreference(event.target.value)
		showToast("Тема интерфейса изменена", "success")
	})
	updateAuthSettingsVisibility()
	updateNotificationSettingsVisibility()
}

async function openBrowserExtensions() {
  const btn = $("#browser-extensions")
  btn.disabled = true
  btn.textContent = "Открываю…"
  try {
    const result = await api("/api/v1/browser/sessions", {
      method: "POST",
      body: JSON.stringify({tracker: "torrentmonitor-extensions", url: "chrome://extensions/"}),
    })
    if (!result.viewer_url) throw new Error("Chromium не вернул адрес окна")
    window.open(result.viewer_url, "tm-browser-extensions", "noopener,noreferrer")
    showToast("Открыта страница расширений сервисного Chromium", "success")
  } catch (err) {
    showToast(err.message, "error", 8000)
  } finally {
    btn.disabled = false
    btn.textContent = "Расширения Chromium"
  }
}

async function loadTemplates() {
  state.settings = await api("/api/v1/settings")
  state.templates = await api("/api/v1/templates")
  pageContent().innerHTML = renderTemplatesPage(state.settings, state.templates)
  $("#templates-form").addEventListener("submit", saveTemplateSettings)
  $("#templates-update-now").addEventListener("click", updateTemplatesNow)
}

function renderSettings(s) {
  return `
    <form id="settings-form" class="settings-form">
      <div class="card settings-section">
        <div class="card-title">Основные</div>
        <div class="settings-grid">
		  <label class="form-field">
			<span>Тема интерфейса</span>
			<select id="set-ui-theme">
			  ${option("system", "Как в системе", window.TMTheme.getPreference())}
			  ${option("light", "Светлая", window.TMTheme.getPreference())}
			  ${option("dark", "Тёмная", window.TMTheme.getPreference())}
			</select>
		  </label>
          ${renderCheck("set-auth", "Авторизация", s.auth)}
          <label class="form-field">
            <span>Новый пароль администратора</span>
			<input id="set-admin-password" type="password" value="" placeholder="${s.auth_password_set ? "оставить пустым, чтобы не менять" : "задайте пароль"}" ${s.auth ? "" : "disabled"}>
          </label>
          <div class="form-field c-muted">Пароль: ${s.auth_password_set ? "задан" : "не задан; авторизация не активна"}</div>
          ${renderCheck("set-debug", "Отладка Chromium", s.debug)}
          ${renderCheck("set-auto-update", "Автообновление системы", s.auto_update)}
          <label class="form-field">
            <span>HTTP timeout, сек</span>
            <input id="set-http-timeout" type="number" min="1" value="${escapeAttr(s.http_timeout_seconds || 15)}">
          </label>
          <label class="form-field">
            <span>Интервал обновления, минут</span>
            <input id="set-monitor-interval" type="number" min="1" value="${escapeAttr(s.monitor_interval_minutes || 15)}">
          </label>
          <label class="form-field form-field--wide">
            <span>Глобальный скрипт после обновления</span>
            <input id="set-post-update-script" type="text" value="${escapeAttr(s.post_update_script || "")}" placeholder="/path/to/script.sh или команда">
          </label>
          <label class="form-field form-field--wide">
            <span>User-Agent для Native HTTP</span>
            <input id="set-user-agent" type="text" value="${escapeAttr(s.user_agent || "")}">
          </label>
        </div>
      </div>

      <div class="card settings-section">
        <div class="card-title">Proxy</div>
        <div class="settings-grid">
          ${renderCheck("set-proxy", "Использовать proxy", s.proxy)}
          <label class="form-field">
            <span>Тип proxy</span>
            <select id="set-proxy-type">
              ${option("", "—", s.proxy_type)}
              ${option("SOCKS5", "SOCKS5", s.proxy_type)}
              ${option("HTTP", "HTTP", s.proxy_type)}
            </select>
          </label>
          <label class="form-field form-field--wide">
            <span>Адрес proxy</span>
            <input id="set-proxy-address" type="text" value="${escapeAttr(s.proxy_address || "")}" placeholder="127.0.0.1:9050">
          </label>
        </div>
      </div>


      <div class="card settings-section">
        <div class="card-title">Chromium</div>
        <div class="settings-grid">
          <label class="form-field">
            <span>Режим браузера</span>
            <select id="set-browser-mode">
              ${option("embedded", "Встроенный Chromium", s.browser_mode || "embedded")}
              ${option("external", "Внешний Chromium", s.browser_mode || "embedded")}
            </select>
          </label>
          <label class="form-field form-field--wide" data-browser-field="embedded">
            <span>Путь к Chromium</span>
            <input id="set-browser-binary" type="text" value="${escapeAttr(s.browser_binary || "")}" placeholder="/usr/bin/chromium">
          </label>
          <label class="form-field form-field--wide" data-browser-field="embedded">
            <span>Профиль Chromium</span>
            <input id="set-browser-profile" type="text" value="${escapeAttr(s.browser_profile || "")}" placeholder="~/.local/share/torrentmonitor-go/browser/torrentmonitor">
          </label>
          <label class="form-field form-field--wide" data-browser-field="external">
            <span>Адрес CDP внешнего Chromium</span>
            <input id="set-browser-connect-url" type="text" value="${escapeAttr(s.browser_connect_url || "")}" placeholder="http://127.0.0.1:9222 или ws://127.0.0.1:9222/devtools/browser/...">
          </label>
        </div>
        <div class="settings-actions settings-actions--test">
          <button id="browser-extensions" class="btn" type="button">Расширения Chromium</button>
          <span class="c-muted">Открывает общий профиль сервисного браузера. Установленные расширения будут доступны всем Chromium-проверкам.</span>
        </div>
        <div class="c-muted small-note">Для внешнего режима запусти Chromium самостоятельно с <code>--remote-debugging-port=9222</code>. TorrentMonitor будет только подключаться и создавать вкладки.</div>
      </div>

      <div class="card settings-section">
        <div class="card-title">Torrent-клиент</div>
        <div class="settings-grid">
          ${renderCheck("set-use-torrent", "Добавлять в torrent-клиент", s.use_torrent)}
          <label class="form-field">
            <span>Клиент</span>
            <select id="set-torrent-client">
              ${option("", "—", s.torrent_client)}
              ${option("Transmission", "Transmission", s.torrent_client)}
              ${option("qBittorrent", "qBittorrent", s.torrent_client)}
              ${option("Deluge", "Deluge", s.torrent_client)}
            </select>
          </label>
          <label class="form-field">
            <span>Адрес</span>
            <input id="set-torrent-address" type="text" value="${escapeAttr(s.torrent_address || "")}" placeholder="127.0.0.1:9091">
          </label>
          <label class="form-field">
            <span>Логин</span>
            <input id="set-torrent-login" type="text" value="${escapeAttr(s.torrent_login || "")}">
          </label>
          <label class="form-field">
            <span>Пароль</span>
            <input id="set-torrent-password" type="password" value="" placeholder="оставить пустым, чтобы не менять">
          </label>
          <label class="form-field form-field--wide">
            <span>Путь по умолчанию</span>
            <input id="set-path-to-download" type="text" value="${escapeAttr(s.path_to_download || "")}" placeholder="/downloads">
          </label>
          <label class="form-field form-field--wide">
            <span>Server address для отдачи .torrent</span>
            <input id="set-server-address" type="text" value="${escapeAttr(s.server_address || "")}" placeholder="http://host:8080/">
          </label>
          ${renderCheck("set-delete-old-files", "Удалять старые файлы", s.delete_old_files)}
          ${renderCheck("set-delete-distribution", "Удалять старую раздачу", s.delete_distribution)}
        </div>
        <div class="torrent-client-test">
          <div class="settings-actions settings-actions--test">
            <button id="torrent-client-check" class="btn" type="button">Проверка соединения</button>
            <span id="torrent-client-check-status" class="c-muted"></span>
          </div>
        </div>
      </div>

      <div class="card settings-section">
        <div class="card-title">Уведомления</div>
        <div class="settings-grid">
          ${renderCheck("set-send", "Уведомления включены", s.send)}
          ${renderCheck("set-send-warning", "Отправлять ошибки", s.send_warning)}
          ${renderCheck("set-send-update", "Отправлять обновления", s.send_update)}
		  <label class="form-field">
			<span>Способ отправки</span>
			<select id="set-notification-method">
			  ${option("none", "Не использовать", s.telegram_enabled ? "telegram" : "none")}
			  ${option("telegram", "Telegram", s.telegram_enabled ? "telegram" : "none")}
			</select>
		  </label>
		  <div class="settings-subsection form-field--wide" data-notification-fields="telegram">
			<div class="settings-grid">
          <label class="form-field">
            <span>Telegram bot token</span>
            <input id="set-telegram-bot-token" type="password" value="" placeholder="${s.telegram_bot_token_set ? "оставить пустым, чтобы не менять" : "123456:ABC..."}">
          </label>
          <label class="form-field">
            <span>Telegram chat_id</span>
            <input id="set-telegram-chat-id" type="text" value="${escapeAttr(s.telegram_chat_id || "")}" placeholder="-1001234567890">
          </label>
          <label class="form-field">
            <span>message_thread_id</span>
            <input id="set-telegram-thread-id" type="text" value="${escapeAttr(s.telegram_thread_id || "")}" placeholder="необязательно">
          </label>
          ${renderCheck("set-telegram-silent", "Без звука", s.telegram_silent)}
          ${renderCheck("set-telegram-use-proxy", "Использовать proxy", s.telegram_use_proxy)}
			</div>
			<div class="settings-actions settings-actions--test">
			  <button id="telegram-check" class="btn" type="button">Проверить Telegram</button>
			</div>
		  </div>
        </div>
      </div>

      <div class="settings-actions">
        <button class="btn btn--primary" type="submit">Сохранить настройки</button>
      </div>
    </form>`
}


function renderTemplatesPage(s, templatesStatus = null) {
  return `
    <form id="templates-form" class="settings-form">
      <div class="card settings-section">
        <div class="card-title">Источник шаблонов</div>
        <div class="settings-grid">
          <label class="form-field form-field--wide">
            <span>Адрес загрузки шаблонов</span>
            <input id="set-template-source-url" type="text" value="${escapeAttr(s.template_source_url || "")}" placeholder="https://example.org/torrentmonitor-trackers.zip или file:///path/templates.zip">
          </label>
          <label class="form-field">
            <span>Интервал автообновления, минут</span>
            <input id="set-template-update-interval" type="number" min="0" value="${escapeAttr(s.template_update_interval_minutes ?? 1440)}">
          </label>
          <label class="form-field form-field--wide">
            <span>Каталог шаблонов</span>
            <input id="set-template-directory" type="text" value="${escapeAttr(s.template_directory || "")}" placeholder="~/.local/share/torrentmonitor-go/templates">
          </label>
        </div>
        <div class="settings-actions">
          <button class="btn btn--primary" type="submit">Сохранить настройки шаблонов</button>
          <button id="templates-update-now" class="btn" type="button">Обновить шаблоны сейчас</button>
          <span id="templates-update-status" class="c-muted"></span>
        </div>
        <div class="c-muted small-note">Если интервал равен 0, автоматическое обновление шаблонов отключено. Источник может быть HTTP(S), file://, локальный .zip, .yaml/.json или каталог.</div>
      </div>

      <div class="card settings-section">
        <div class="card-title">Загруженные шаблоны</div>
        <div id="templates-status-list">${renderTemplateStatus(templatesStatus)}</div>
      </div>
    </form>`
}

async function saveTemplateSettings(e) {
  e.preventDefault()
  const body = {
    template_source_url: $("#set-template-source-url").value,
    template_update_interval_minutes: Number($("#set-template-update-interval").value || 0),
    template_directory: $("#set-template-directory").value,
  }
  const status = $("#templates-update-status")
  status.textContent = "Сохраняю…"
  status.className = "c-muted"
  try {
    state.settings = await api("/api/v1/settings", {method: "PATCH", body: JSON.stringify(body)})
    state.templates = await api("/api/v1/templates")
    const list = $("#templates-status-list")
    if (list) list.innerHTML = renderTemplateStatus(state.templates)
    status.textContent = "Настройки шаблонов сохранены"
    status.className = "c-success"
    await loadBootstrap()
    setTimeout(() => { status.textContent = ""; status.className = "c-muted" }, 2000)
  } catch (err) {
    status.textContent = err.message
    status.className = "c-danger"
  }
}


function renderTemplateStatus(status) {
  const templates = (status && status.templates) || []
  const loaded = status ? Number(status.loaded || templates.length || 0) : 0
  const directory = status && status.directory ? status.directory : "—"
  const source = status && status.source_url ? status.source_url : "—"
  const interval = status ? formatInterval(Number(status.update_interval_minutes || 0) * 60) : "—"
  const rows = templates.map(t => {
    const label = t.name ? `${t.name} (${t.id})` : t.id
    const domains = (t.domains || []).join(", ") || "—"
    const sourceText = t.source || "—"
    return `<div class="template-row">
      <div><strong>${escapeHtml(label)}</strong><div class="c-muted">${escapeHtml(domains)}</div></div>
      <div class="c-muted">${escapeHtml(t.kind || "—")}</div>
      <div class="c-muted">${escapeHtml(t.default_access_mode || "native")}</div>
      <div class="c-muted" title="${escapeAttr(sourceText)}">${escapeHtml(shortPath(sourceText))}</div>
    </div>`
  }).join("")
  return `
    <div class="template-status">
      <div class="c-muted">Загружено шаблонов: ${loaded}. Каталог: ${escapeHtml(directory)}. Источник: ${escapeHtml(source)}. Автообновление: ${escapeHtml(interval)}.</div>
      <div class="template-list">
        <div class="template-row template-row--head"><div>Шаблон</div><div>Тип</div><div>Режим</div><div>Источник</div></div>
        ${rows || `<div class="c-muted">Шаблоны не загружены</div>`}
      </div>
    </div>`
}

function shortPath(value) {
  if (!value) return "—"
  if (value === "built-in") return "built-in"
  const text = String(value)
  if (text.length <= 48) return text
  return "…" + text.slice(-47)
}

function updateBrowserSettingsVisibility() {
  const modeEl = $("#set-browser-mode")
  if (!modeEl) return
  const mode = modeEl.value || "embedded"
  document.querySelectorAll("[data-browser-field]").forEach(el => {
    const show = el.getAttribute("data-browser-field") === mode
    el.hidden = !show
  })
}

function updateAuthSettingsVisibility() {
	const enabled = $("#set-auth")?.checked ?? false
	const password = $("#set-admin-password")
	if (password) {
		password.disabled = !enabled
		if (!enabled) password.value = ""
	}
}

function updateNotificationSettingsVisibility() {
	const method = $("#set-notification-method")?.value || "none"
	document.querySelectorAll("[data-notification-fields]").forEach(section => {
		section.hidden = section.dataset.notificationFields !== method
	})
}

function renderCheck(id, label, checked) {
  return `
    <label class="form-check settings-check">
      <input id="${id}" type="checkbox" ${checked ? "checked" : ""}>
      <span>${escapeHtml(label)}</span>
    </label>`
}

function option(value, label, current) {
  return `<option value="${escapeAttr(value)}" ${value === (current || "") ? "selected" : ""}>${escapeHtml(label)}</option>`
}

async function saveSettings(e) {
  e.preventDefault()
	const body = {
		auth: $("#set-auth").checked,
		debug: $("#set-debug").checked,
    auto_update: $("#set-auto-update").checked,
    http_timeout_seconds: Number($("#set-http-timeout").value || 15),
    monitor_interval_minutes: Number($("#set-monitor-interval").value || 15),
    post_update_script: $("#set-post-update-script").value,
    browser_mode: $("#set-browser-mode").value,
    browser_binary: $("#set-browser-binary").value,
    browser_profile: $("#set-browser-profile").value,
    browser_connect_url: $("#set-browser-connect-url").value,
    user_agent: $("#set-user-agent").value,
    proxy: $("#set-proxy").checked,
    proxy_type: $("#set-proxy-type").value,
    proxy_address: $("#set-proxy-address").value,
    use_torrent: $("#set-use-torrent").checked,
    torrent_client: $("#set-torrent-client").value,
    torrent_address: $("#set-torrent-address").value,
    torrent_login: $("#set-torrent-login").value,
    path_to_download: $("#set-path-to-download").value,
    server_address: $("#set-server-address").value,
    delete_old_files: $("#set-delete-old-files").checked,
    delete_distribution: $("#set-delete-distribution").checked,
    send: $("#set-send").checked,
    send_warning: $("#set-send-warning").checked,
    send_update: $("#set-send-update").checked,
	telegram_enabled: $("#set-notification-method").value === "telegram",
    telegram_chat_id: $("#set-telegram-chat-id").value,
    telegram_thread_id: $("#set-telegram-thread-id").value,
    telegram_silent: $("#set-telegram-silent").checked,
    telegram_use_proxy: $("#set-telegram-use-proxy").checked,
  }
  const adminPassword = $("#set-admin-password")?.value || ""
  if (adminPassword !== "") body.admin_password = adminPassword
  const password = $("#set-torrent-password").value
  if (password !== "") body.torrent_password = password
  const telegramToken = $("#set-telegram-bot-token").value
  if (telegramToken !== "") body.telegram_bot_token = telegramToken
  state.settings = await api("/api/v1/settings", {method: "PATCH", body: JSON.stringify(body)})
  if ($("#set-admin-password")) $("#set-admin-password").value = ""
  $("#set-torrent-password").value = ""
  $("#set-telegram-bot-token").value = ""
	showToast("Настройки сохранены", "success")
  await loadBootstrap()
  if (requiresLogin()) { showLogin("Авторизация включена. Войдите с новым паролем."); return }
}


async function checkTelegramNotification() {
	const button = $("#telegram-check")
	button.disabled = true
	button.textContent = "Отправляю…"
  const body = {
	telegram_enabled: $("#set-notification-method").value === "telegram",
    telegram_chat_id: $("#set-telegram-chat-id").value,
    telegram_thread_id: $("#set-telegram-thread-id").value,
    telegram_silent: $("#set-telegram-silent").checked,
    telegram_use_proxy: $("#set-telegram-use-proxy").checked,
    proxy: $("#set-proxy").checked,
    proxy_type: $("#set-proxy-type").value,
    proxy_address: $("#set-proxy-address").value,
  }
  const token = $("#set-telegram-bot-token").value
  if (token !== "") body.telegram_bot_token = token
  try {
    const data = await api("/api/v1/notifications/test", {method: "POST", body: JSON.stringify(body)})
	showToast(data.message || "Telegram-уведомление отправлено", "success")
  } catch (err) {
	showToast(err.message, "error", 8000)
	} finally {
		button.disabled = false
		button.textContent = "Проверить Telegram"
  }
}

async function checkTorrentClientConnection() {
  const status = $("#torrent-client-check-status")
  status.textContent = "Проверяю…"
  status.className = "c-muted"
  const body = {
    torrent_client: $("#set-torrent-client").value,
    torrent_address: $("#set-torrent-address").value,
    torrent_login: $("#set-torrent-login").value,
    http_timeout_seconds: Number($("#set-http-timeout").value || 15),
  }
  const adminPassword = $("#set-admin-password")?.value || ""
  if (adminPassword !== "") body.admin_password = adminPassword
  const password = $("#set-torrent-password").value
  if (password !== "") body.torrent_password = password
  try {
    const data = await api("/api/v1/torrent-client/check", {
      method: "POST",
      body: JSON.stringify(body),
    })
    status.textContent = data.message || "Соединение установлено"
    status.className = "c-success"
  } catch (err) {
    status.textContent = err.message
    status.className = "c-danger"
  }
}

async function updateTemplatesNow() {
  const status = $("#templates-update-status")
  status.textContent = "Обновляю…"
  status.className = "c-muted"
  try {
    const data = await api("/api/v1/templates/update", {method: "POST", body: JSON.stringify({})})
    status.textContent = data.message || `Загружено шаблонов: ${data.loaded || 0}`
    status.className = "c-success"
    state.templates = await api("/api/v1/templates")
    const list = $("#templates-status-list")
    if (list) list.innerHTML = renderTemplateStatus(state.templates)
    await loadBootstrap()
  } catch (err) {
    status.textContent = err.message
    status.className = "c-danger"
  }
}

function schedulerJobTitle(name) {
  if (name === "monitor") return "Автоматическое обновление"
  if (name === "templates") return "Обновление шаблонов"
  return name
}

function schedulerJobDescription(name) {
  if (name === "monitor") return "Тот же проход, что и кнопка “Обновить сейчас”, только без JS-уведомлений."
  if (name === "templates") return "Скачивает внешний набор шаблонов и перезагружает registry трекеров."
  return ""
}

async function loadScheduler() {
  const data = await api("/api/v1/scheduler/status")
  state.scheduler = data
  pageContent().innerHTML = (data.jobs || []).map(j => `
    <div class="card">
      <div class="card-title">${escapeHtml(schedulerJobTitle(j.name))} ${j.running ? `<span class="c-success">выполняется</span>` : ""}</div>
      <div class="c-muted">${escapeHtml(schedulerJobDescription(j.name))}</div>
      <div class="c-muted">Интервал: ${formatInterval(j.interval_seconds)}</div>
      <div class="c-muted">Последний старт: ${j.last_start ? new Date(j.last_start).toLocaleString("ru-RU") : "—"}</div>
      <div class="c-muted">Последнее завершение: ${j.last_finish ? new Date(j.last_finish).toLocaleString("ru-RU") : "—"}</div>
      <div class="c-muted">Следующий запуск: ${j.next_run ? new Date(j.next_run).toLocaleString("ru-RU") : "—"}</div>
      ${j.last_error ? `<div class="c-danger">${escapeHtml(j.last_error)}</div>` : ""}
      <div class="settings-actions"><button class="btn" type="button" data-run-job="${escapeAttr(j.name)}" ${j.running ? "disabled" : ""}>Запустить сейчас</button></div>
    </div>`).join("") || `<div class="card c-muted">Заданий нет</div>`
  pageContent().querySelectorAll("[data-run-job]").forEach(btn => {
    btn.addEventListener("click", async () => {
      btn.disabled = true
      await api("/api/v1/scheduler/run", {method: "POST", body: JSON.stringify({job: btn.dataset.runJob})})
      setTimeout(loadScheduler, 500)
    })
  })
}

function formatInterval(seconds) {
  const n = Number(seconds || 0)
  if (!n) return "—"
  if (n % 86400 === 0) return `${n / 86400} д.`
  if (n % 3600 === 0) return `${n / 3600} ч.`
  if (n % 60 === 0) return `${n / 60} мин.`
  return `${n} сек.`
}

function openItemModal(mode) {
  state.editingItem = null
  $("#form-mode").value = mode
  $("#form-id").value = ""
  setFormError("")
  $("#item-form").reset()
  $("#theme-update-header").checked = true

  $("#theme-fields").hidden = mode !== "theme"
  $("#series-fields").hidden = mode !== "series"
  $("#edit-fields").hidden = true
  $("#modal-title").textContent = mode === "series" ? "Добавить сериал" : "Добавить тему"
  $("#modal-subtitle").textContent = mode === "series" ? "RSS-мониторинг новых серий" : "Форумная тема или раздача"
  $("#modal-backdrop").hidden = false
  focusFirstVisibleInput()
}

async function openEditModal(id) {
  setFormError("")
  const item = await api(`/api/v1/torrents/${id}`)
  state.editingItem = item
  $("#form-mode").value = "edit"
  $("#form-id").value = item.id
  $("#theme-fields").hidden = true
  $("#series-fields").hidden = true
  $("#edit-fields").hidden = false
  $("#modal-title").textContent = "Изменить тему"
  $("#modal-subtitle").textContent = `${item.tracker} · ${item.type}`
  $("#edit-name").value = item.name || ""
  $("#edit-torrent-id").value = item.torrent_id || ""
  $("#edit-path").value = item.path || ""
  $("#edit-auto-update").checked = !!item.auto_update
  $("#edit-paused").checked = !!item.paused
  $("#edit-reset").checked = false
  $("#modal-backdrop").hidden = false
  focusFirstVisibleInput()
}

function closeItemModal() {
  $("#modal-backdrop").hidden = true
  setFormError("")
}

async function submitItemForm(e) {
  e.preventDefault()
  setFormError("")
  const mode = $("#form-mode").value
  try {
    if (mode === "theme") {
      await api("/api/v1/torrents", {
        method: "POST",
        body: JSON.stringify({
          kind: "theme",
          url: $("#theme-url").value,
          name: $("#theme-name").value,
          path: $("#theme-path").value,
          update_header: $("#theme-update-header").checked,
        }),
      })
    } else if (mode === "series") {
      await api("/api/v1/torrents", {
        method: "POST",
        body: JSON.stringify({
          kind: "series",
          tracker: $("#series-tracker").value,
          name: $("#series-name").value,
          path: $("#series-path").value,
          hd: Number($("#series-hd").value),
        }),
      })
    } else if (mode === "edit") {
      const id = $("#form-id").value
      await api(`/api/v1/torrents/${id}`, {
        method: "PATCH",
        body: JSON.stringify({
          name: $("#edit-name").value,
          path: $("#edit-path").value,
          torrent_id: $("#edit-torrent-id").value,
          auto_update: $("#edit-auto-update").checked,
          paused: $("#edit-paused").checked,
          reset_timestamp: $("#edit-reset").checked,
        }),
      })
    }
    closeItemModal()
    await loadBootstrap()
    await showPage("torrents")
  } catch (err) {
    setFormError(err.message)
  }
}

function setFormError(msg) {
  const el = $("#form-error")
  el.textContent = msg
  el.hidden = !msg
}

function focusFirstVisibleInput() {
  setTimeout(() => {
    const modal = $("#modal-backdrop")
    const input = Array.from(modal.querySelectorAll('input:not([type="hidden"]), select'))
      .find((el) => !el.closest('[hidden]'))
    if (input) input.focus()
  }, 0)
}

function flash(el, msg) {
  const old = el.querySelector(".flash")
  if (old) old.remove()
  const node = document.createElement("div")
  node.className = "flash c-success"
  node.textContent = msg
  el.appendChild(node)
  setTimeout(() => node.remove(), 2000)
}

function debounce(fn, ms) {
  let t
  return (...args) => { clearTimeout(t); t = setTimeout(() => fn(...args), ms) }
}
function escapeHtml(s) { return String(s ?? "").replace(/[&<>'"]/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;","'":"&#39;",'"':"&quot;"}[c])) }
function escapeAttr(s) { return escapeHtml(s) }
function cssEscape(s) {
  if (window.CSS && CSS.escape) return CSS.escape(String(s))
  return String(s).replace(/[^a-zA-Z0-9_-]/g, "\\$&")
}

init().catch((err) => {
  $("#loader").textContent = `Ошибка: ${err.message}`
})
