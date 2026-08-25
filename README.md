# TorrentMonitor Go scaffold

Каркас переписывания TorrentMonitor на Go.

Что уже есть:

- один Go HTTP daemon;
- embedded web panel через `go:embed`;
- новый JSON API `/api/v1/*`;
- совместимый legacy endpoint `/action.php` для части старых action;
- встроенный scheduler вместо cron;
- SQLite repository по умолчанию;
- рабочий CRUD в UI: добавить тему, добавить сериал, редактировать, удалить;
- редактирование учётных данных через новый API;
- режимы доступа к трекерам `native/chromium` и кнопка проверки логина;
- страница настроек и API `/api/v1/settings`;
- torrent-client слой: qBittorrent adapter, проверка соединения без добавления раздач и автоматическая отправка обновлений в подключённый клиент с сохранением hash, если клиент его вернул;
- глобальный post-update script hook из настроек с явными `TM_*` переменными окружения;
- site runner для первого шаблона `rutracker.org`: native HTTP или shared Chromium/CDP profile в зависимости от `access_mode`;
- persistent JSON repository как legacy/dev fallback;
- in-memory repository для dev/test режима;
- заготовки пакетов для site templates, torrent clients и notifications;
- декларативный YAML-шаблон RuTracker в новой схеме `schema_version: 1`;
- внешние шаблоны трекеров: загрузка из настраиваемого URL, ручное обновление и отдельный scheduler job автообновления.

## Запуск

```bash
go run ./cmd/torrentmonitor
# open http://127.0.0.1:8080
```

По умолчанию состояние сохраняется в:

```text
$XDG_DATA_HOME/torrentmonitor-go/torrentmonitor.sqlite3
# или
~/.local/share/torrentmonitor-go/torrentmonitor.sqlite3
```

Можно переопределить:

```bash
TM_LISTEN=:8080 \
TM_STORE=sqlite \
TM_DATA_FILE=/var/lib/torrentmonitor-go/torrentmonitor.sqlite3 \
go run ./cmd/torrentmonitor
```

Также доступны старый JSON-store и временный dev-режим без сохранения:

```bash
TM_STORE=json TM_DATA_FILE=/tmp/torrentmonitor-data.json go run ./cmd/torrentmonitor
TM_STORE=memory go run ./cmd/torrentmonitor
```

SQLite store использует CGO и системную `libsqlite3`. Если нужно собрать без CGO, используй `TM_STORE=json` или `TM_STORE=memory`.


## Авторизация web UI

Авторизация управляется в **Настройки → Основные**:

- чекбокс **Авторизация** включает защиту панели;
- поле **Новый пароль администратора** задаёт или меняет пароль;
- если пароль не задан, защита считается неактивной, чтобы не заблокировать первый запуск.

Можно задать пароль при первом запуске через переменную окружения:

```bash
TM_ADMIN_PASSWORD='strong-password' go run ./cmd/torrentmonitor
```

После входа создаётся HttpOnly session cookie `tm_session`. Защищены `/api/v1/*`, `/browser/session/*`, legacy `/action.php` и browser/CDP viewer. Публичны только `/`, `/assets/*`, `/api/v1/bootstrap` и endpoints входа.

API:

```bash
curl -X POST http://127.0.0.1:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"password":"strong-password"}'

curl -X POST http://127.0.0.1:8080/api/v1/auth/logout \
  -H 'Content-Type: application/json' \
  -d '{}'
```

## Проверка API

```bash
curl http://127.0.0.1:8080/api/v1/bootstrap
curl http://127.0.0.1:8080/api/v1/torrents
curl -X POST http://127.0.0.1:8080/api/v1/scheduler/run \
  -H 'Content-Type: application/json' \
  -d '{"job":"monitor"}'

Планировщик содержит два job-а:

- `monitor` — автоматический проход тем тем же backend-путём, что и кнопка **«Обновить сейчас»**, но без JS popup-уведомлений.
- `templates` — автообновление внешних шаблонов трекеров. Если интервал шаблонов равен `0` или источник не задан, job не делает сетевых изменений.

Старый placeholder `retry_temp` удалён.

curl -X PATCH http://127.0.0.1:8080/api/v1/credentials/1 \
  -H 'Content-Type: application/json' \
  -d '{"login":"user","password":"secret","passkey":"...","access_mode":"native"}'

# Проверить native-логин или открыть shared Chromium/CDP viewer для ручного логина, если access_mode=chromium.
curl -X POST http://127.0.0.1:8080/api/v1/credentials/1/check-login \
  -H 'Content-Type: application/json' \
  -d '{"open_browser":true}'

curl http://127.0.0.1:8080/api/v1/settings
curl -X PATCH http://127.0.0.1:8080/api/v1/settings \
  -H 'Content-Type: application/json' \
  -d '{"use_torrent":true,"torrent_client":"qBittorrent","torrent_address":"http://127.0.0.1:8081","http_timeout_seconds":20,"post_update_script":"/opt/torrentmonitor/post-update.sh","template_source_url":"https://example.org/torrentmonitor-trackers.zip","template_update_interval_minutes":1440}'

curl -X POST http://127.0.0.1:8080/api/v1/torrent-client/check \
  -H 'Content-Type: application/json' \
  -d '{}'

# Ручное обновление внешних шаблонов
curl -X POST http://127.0.0.1:8080/api/v1/templates/update \
  -H 'Content-Type: application/json' \
  -d '{}'

# Ручная проверка темы через HTTP-mode site runner.
curl -X POST http://127.0.0.1:8080/api/v1/torrents/1/check \
  -H 'Content-Type: application/json' \
  -d '{}'
```

Добавить тему через новый API:

```bash
curl -X POST http://127.0.0.1:8080/api/v1/torrents \
  -H 'Content-Type: application/json' \
  -d '{
    "kind":"theme",
    "url":"https://rutracker.org/forum/viewtopic.php?t=123456",
    "name":"Test release",
    "path":"/downloads",
    "update_header":true
  }'
```

## Внешние шаблоны трекеров

В **Настройки → Шаблоны трекеров** можно задать:

- `template_source_url` — адрес загрузки. Поддерживаются HTTP(S), `file://`, локальный `.zip`, одиночный `.yaml/.json` и локальный каталог. ZIP может содержать шаблоны в `trackers/*.yaml` или в любой другой вложенной папке.
- `template_update_interval_minutes` — отдельный интервал автообновления шаблонов. `0` отключает таймер.
- `template_directory` — локальный каталог, куда распаковываются шаблоны. По умолчанию это `~/.local/share/torrentmonitor-go/templates` или `$XDG_DATA_HOME/torrentmonitor-go/templates`.

При загрузке внешние шаблоны накладываются поверх встроенных. То есть шаблон с тем же `site`/`id` может переопределить встроенный `rutracker.org`.

Раздел **Учётные данные** теперь строится из registry шаблонов. Если есть встроенный или внешний шаблон трекера, для него автоматически появляется карточка логина. Если шаблон удалён или не загружен, трекер скрывается из этого меню, даже если старая строка credentials ещё осталась в БД.

Переменные окружения для стартовых defaults:

```bash
TM_TEMPLATE_SOURCE_URL=https://example.org/torrentmonitor-trackers.zip
TM_TEMPLATE_UPDATE_INTERVAL_MINUTES=1440
TM_TEMPLATE_DIR=/var/lib/torrentmonitor-go/templates
```

## Схема шаблонов трекеров v1

RuTracker уже описан декларативным YAML-шаблоном, а Go-код использует его как встроенный fallback. Внешний шаблон с тем же `id`/`site` переопределяет встроенный.

Минимальная структура forum-topic шаблона:

```yaml
schema_version: 1
id: rutracker.org
name: RuTracker
kind: forum_topic
mode: http

domains:
  - rutracker.org

defaults:
  access_mode: chromium
  timezone: Europe/Moscow
  page_timeout_seconds: 30

encoding:
  page: windows-1251
  form: windows-1251

urls:
  base: https://rutracker.org
  login: https://rutracker.org/forum/login.php
  auth_check: https://rutracker.org/forum/index.php
  topic: https://rutracker.org/forum/viewtopic.php?t={{ item.torrent_id }}
  download: https://rutracker.org/forum/dl.php?t={{ item.torrent_id }}

auth:
  check:
    method: GET
    url: https://rutracker.org/forum/index.php
    success:
      contains: profile.php?mode=viewprofile
  login_form:
    method: POST
    url: https://rutracker.org/forum/login.php
    form_encoding: windows-1251
    form:
      login_username: "{{ credentials.login }}"
      login_password: "{{ credentials.password }}"
      login: Вход

topic:
  ready:
    url:
      exact: https://rutracker.org/forum/viewtopic.php?t={{ item.torrent_id }}
    dom_contains:
      - dl.php?t={{ item.torrent_id }}
  title:
    selector: title
    cleanup:
      - trim_suffix: " :: RuTracker.org"
  updated_at:
    selector: body
    regex: "([0-9]{2}-[А-Яа-я]{3}-[0-9]{2} [0-9]{2}:[0-9]{2})"
    layout: "02-Jan-06 15:04"
    locale: ru

download:
  url: https://rutracker.org/forum/dl.php?t={{ item.torrent_id }}
  before:
    headers:
      Referer: https://rutracker.org/forum/viewtopic.php?t={{ item.torrent_id }}
    set_cookie:
      - name: bb_dl
        value: "{{ item.torrent_id }}"
        domain: rutracker.org
        path: /forum/
  validate:
    bencode_torrent: true
```

Важное правило готовности страницы: `topic.ready` должен описывать **положительные признаки ожидаемой страницы трекера**, например URL темы и ссылку `dl.php?t=...`. Заголовок страницы и строки Cloudflare не используются как признак успеха.

Внутри TorrentMonitor шаблон нормализуется в общий runner-формат. Один и тот же шаблон работает с `access_mode=native` и `access_mode=chromium`; режим доступа выбирает транспорт целиком.

## Следующие шаги

1. Добавить второй forum-topic шаблон, например `nnmclub.to` или `kinozal.tv`, чтобы проверить универсальность схемы.
2. Добавить JSON Schema для `schema_version: 1` во внешний репозиторий шаблонов.
3. Добавить CLI/endpoint проверки шаблона на fixture HTML.
4. Реализовать Transmission adapter.
5. Расширить BrowserBroker: дополнительные диагностические статусы интерактивных проверок.


### Глобальный скрипт после обновления

Скрипт задаётся один раз в **Настройки → Основные → Глобальный скрипт после обновления** или через API поле `post_update_script`. Старого поля `script` у отдельных тем больше нет: hook задаётся только глобально.

Hook запускается только когда раздача действительно обновилась. Порядок такой: проверка темы → скачивание `.torrent` → сохранение состояния темы → если подключён torrent-клиент (`use_torrent=true` и выбран `torrent_client`), отправка `.torrent` в этот клиент → сохранение hash, если клиент его вернул → запуск hook-а. Если torrent-клиент не подключён, этап отправки и сохранения hash просто пропускается, а hook всё равно запускается после сохранения состояния темы. Если клиент подключён, но отправка в него завершилась ошибкой, hook не запускается, а ошибка уходит в раздел **Ошибки**. Команда выполняется через `/bin/sh -c`, поэтому можно указать как путь к исполняемому скрипту, так и команду с аргументами.

Во время выполнения доступны переменные окружения:

```text
TM_TORRENT_DB_ID
TM_TORRENT_ID
TM_TORRENT_TRACKER
TM_TORRENT_NAME
TM_TORRENT_TITLE
TM_TORRENT_URL
TM_TORRENT_PATH
TM_TORRENT_SAVE_PATH
TM_SAVE_PATH
TM_TORRENT_HASH
TM_TORRENT_UPDATED_AT
TM_TORRENT_CLOSED
TM_TORRENT_UPDATED
TM_TORRENT_FILE
TM_TORRENT_FILE_SIZE
TM_CLIENT
TM_TORRENT_CLIENT
TM_TORRENT_CLIENT_ENABLED
TM_TORRENT_CLIENT_ADD_OK
TM_TORRENT_CLIENT_HASH
```

`TM_TORRENT_FILE` указывает на временный `.torrent` файл, который существует только на время работы hook-а. `TM_CLIENT` оставлен как короткий alias, новое имя — `TM_TORRENT_CLIENT`. `TM_TORRENT_CLIENT_ENABLED=false` означает, что этап отправки в torrent-клиент был пропущен, а `TM_TORRENT_CLIENT_ADD_OK=true` означает, что подключённый клиент принял `.torrent`. Если скрипт завершится с ошибкой или timeout 5 минут, ошибка записывается в раздел **Ошибки**, а тема помечается как проблемная.

### Tracker access mode: native / Chromium CDP

У каждого трекера в разделе **Учётные данные** есть режим доступа:

- `native` — весь pipeline идёт через обычные HTTP-запросы Go runner-а: login, auth-check, загрузка страницы темы и скачивание `.torrent`. После успешного form-login session cookie сохраняется внутри БД и не показывается в UI. Этот режим **не** открывает Chromium и не использует browser cookies.
- `chromium` — весь pipeline идёт через shared BrowserBroker/CDP: login/check через серверный Chromium, загрузка страницы темы через Chromium DOM и скачивание `.torrent` через Chromium download. Этот режим **не** делает native `POST /login.php` и не качает `/dl.php` через Go HTTP.

Это не challenge solver и не автоматический обход CAPTCHA. Приложение поднимает настоящий Chromium, показывает его screencast в панели и пересылает клики/клавиатуру обратно через Chrome DevTools Protocol.

BrowserBroker теперь использует **один общий persistent Chromium-профиль** для всех трекеров, а не отдельный профиль на сайт. По умолчанию это `.../browser/torrentmonitor`. Открытие новой проверки не убивает Chromium-процесс и не создаёт новый профиль; для каждого трекера переиспользуется вкладка/target внутри общего браузера. Это важно для Cloudflare и session cookies: login state сохраняется в одном месте и не теряется из-за profile lock или `Process.Kill()`.

Профиль по умолчанию создаётся тут:

```text
$XDG_DATA_HOME/torrentmonitor-go/browser/torrentmonitor
# или
~/.local/share/torrentmonitor-go/browser/torrentmonitor
```

Настраивается в веб-интерфейсе:

```text
Настройки → Chromium
  Режим браузера: Встроенный Chromium / Внешний Chromium
  Путь к Chromium
  Профиль Chromium
  Адрес CDP внешнего Chromium
```

Переменные окружения `TM_BROWSER_BINARY`, `TM_BROWSER_PROFILE`, `TM_BROWSER_PROFILE_BASE` и `TM_BROWSER_CONNECT_URL` оставлены как стартовые defaults для первой инициализации. После сохранения в UI используются значения из настроек.

Если выбран встроенный Chromium, BrowserBroker запускает Chromium в display/headful-режиме, без `--headless`. TorrentMonitor сам не поднимает display: перед запуском подготовь `DISPLAY` или `WAYLAND_DISPLAY` через обычную пользовательскую сессию, Xvfb, wolf, cage, weston, xpra или другой compositor/display provider. Если display не найден, создание browser-сессии завершится понятной ошибкой.

Можно не давать TorrentMonitor запускать Chromium вообще, а подключить его к уже запущенному браузеру через Chrome DevTools Protocol. Для этого выбери в настройках `Внешний Chromium` и укажи CDP endpoint, например `http://127.0.0.1:9222`:

```bash
chromium \
  --remote-debugging-address=127.0.0.1 \
  --remote-debugging-port=9222 \
  --user-data-dir="$HOME/.local/share/torrentmonitor-go/browser/torrentmonitor" \
  --no-first-run \
  --no-default-browser-check

# затем в TorrentMonitor:
# Настройки → Chromium → Внешний Chromium → http://127.0.0.1:9222
```

Внешний режим не ищет `TM_BROWSER_BINARY`, не проверяет `DISPLAY/WAYLAND_DISPLAY`, не создаёт процесс Chromium и не останавливает его. Он только создаёт/закрывает вкладки через `/json/new` и CDP. Поддержанные формы CDP endpoint: `http://127.0.0.1:9222`, `127.0.0.1:9222`, `:9222`, `9222`, а также browser websocket URL `ws://127.0.0.1:9222/devtools/browser/...`.

Если `native` получает 403/Cloudflare/challenge, он возвращает ошибку native-режима. Для таких трекеров надо переключить учётные данные на `chromium`. Неявного hybrid/fallback больше нет.

Ручное создание CDP browser session:

```bash
curl -X POST http://127.0.0.1:8080/api/v1/browser/sessions \
  -H 'Content-Type: application/json' \
  -d '{"tracker":"rutracker.org","url":"https://rutracker.org/forum/login.php"}'
```

Ответ содержит `viewer_url`, например:

```text
/browser/session/br_...
```

Browser viewer сейчас сделан без внешних JS-библиотек: картинка обновляется через `/frame`, ввод уходит через `/input`. В режиме `chromium` site runner читает страницу темы и скачивает `.torrent` через тот же shared BrowserBroker. Native HTTP с browser cookies больше не используется — такой режим появится только если будет явно добавлен отдельный `hybrid`.

### BrowserBroker debug

To inspect whether Cloudflare/login cookies are actually stored in the server Chromium profile, start TorrentMonitor with:

```bash
TM_BROWSER_DEBUG=1 \
TM_BROWSER_BINARY=/usr/bin/chromium \
TM_LISTEN=127.0.0.1:8080 \
go run ./cmd/torrentmonitor
```

After opening a browser session and pressing **Done** in `/browser/session/<id>`, the log prints the current URL, page title, cookie names, whether `cf_clearance` exists, and whether the current page still looks like Cloudflare or a login page. Cookie values are never logged.

You can also inspect the same information without cookie values:

```bash
curl http://127.0.0.1:8080/api/v1/browser/sessions/<id> | jq
```

## Error workflow and tracker pause

The web UI no longer has a News section. Runtime problems are surfaced in the Errors section instead of browser popups.

When Chromium access mode cannot render the expected tracker URL before timeout, the runner returns a typed interactive error. TorrentMonitor records an error entry with an "Open browser" action pointing to the live CDP browser session. While that warning is active, the scheduler skips the rest of the torrents for the same tracker to avoid hammering Cloudflare/login pages. Manual checks are still allowed; a successful browser session "Done" or a successful torrent check clears tracker warnings and unblocks the tracker.

Access modes remain strict:

- `native`: login, topic fetch, and torrent download all use Go HTTP.
- `chromium`: login/check, topic fetch, and torrent download all use the shared Chromium BrowserBroker.
- No implicit hybrid fallback is used.

## Notifications

TorrentMonitor can send Telegram notifications for successful updates and errors.

Settings are available in the web UI:

```text
Настройки → Уведомления
  Уведомления включены
  Отправлять ошибки
  Отправлять обновления
  Telegram
  Telegram bot token
  Telegram chat_id
  message_thread_id
  Без звука
  Использовать proxy
  Проверить Telegram
```

The bot token is stored in settings but is not returned by the JSON API. Leave the field empty to keep the existing token unchanged.

Environment defaults for first start:

```bash
TM_SEND_NOTIFICATIONS=1
TM_SEND_WARNING=1
TM_SEND_UPDATE=1
TM_TELEGRAM_ENABLED=1
TM_TELEGRAM_BOT_TOKEN='123456:ABC...'
TM_TELEGRAM_CHAT_ID='-1001234567890'
TM_TELEGRAM_THREAD_ID='284'        # optional
TM_TELEGRAM_SILENT=0              # optional
TM_TELEGRAM_USE_PROXY=0           # optional; use proxy_type/proxy_address
```

Telegram notifications are sent only when the global notification switch is enabled and the matching event type is enabled. Warning notifications can include an inline action button, for example “Открыть браузер”, if `server_address` is configured so a relative browser-session URL can be turned into an absolute URL.

Telegram API requests use the configured HTTP or SOCKS5 proxy when either the global proxy checkbox or the Telegram-specific proxy checkbox is enabled. The Telegram checkbox therefore forces proxy use for Telegram even when proxying is disabled globally. The “Проверить Telegram” action uses the current proxy checkboxes and proxy fields from the form, even before the settings are saved.

The web UI supports light, dark, and system theme modes. The browser stores the preference and applies it to both the application shell and the standalone login page. System mode follows operating-system theme changes without a reload.
