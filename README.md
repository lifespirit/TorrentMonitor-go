# TorrentMonitor Go

TorrentMonitor Go следит за форумными торрент-раздачами, обнаруживает обновления и отправляет новый `.torrent` в qBittorrent. Трекеры описываются декларативными YAML-шаблонами, поэтому поддержку нового сайта можно добавить без изменения Go-кода.

## Возможности

- периодическая и ручная проверка тем;
- автоматическая проверка сразу после добавления темы;
- загрузка обновлённого `.torrent` и отправка в qBittorrent;
- отдельный путь сохранения для каждой темы;
- сохранение info-hash и безопасная обработка уже существующей раздачи;
- встроенные шаблоны RuTracker, NNM-Club и Tapochek.net;
- подключение и автоматическое обновление внешних шаблонов;
- уведомления Telegram;
- HTTP/SOCKS-прокси для Native HTTP и отдельных интеграций;
- общий скрипт после обновления;
- SQLite, JSON и временное in-memory хранилище;
- светлая, тёмная и системная тема интерфейса.

## Два режима доступа к трекерам

Режим выбирается отдельно для каждого трекера в разделе **Учётные данные**.

### Native HTTP

TorrentMonitor самостоятельно выполняет HTTP-запросы, авторизацию и загрузку `.torrent`.

Преимущества:

- минимальное потребление памяти и CPU;
- подходит для роутеров, одноплатных компьютеров и слабых ARM-устройств;
- не требует графической сессии;
- поддерживает глобальный User-Agent и прокси;
- сохраняет session cookie в базе.

Native HTTP следует использовать всегда, когда трекер не требует JavaScript, Cloudflare или интерактивную CAPTCHA.

### Chromium

TorrentMonitor подключается к Chromium через Chrome DevTools Protocol и использует постоянный браузерный профиль. Страницы темы и загрузка могут выполняться в той же авторизованной сессии.

В веб-интерфейсе доступен просмотр интерактивной браузерной сессии. В ней можно вручную:

- войти на трекер;
- пройти CAPTCHA;
- пройти Cloudflare или другую JavaScript-проверку;
- подтвердить дополнительные запросы сайта.

После успешной авторизации профиль сохраняется и повторно используется планировщиком. Chromium можно запустить самим TorrentMonitor в существующей графической сессии либо отдельным сервисом внутри headless Weston.

## Быстрый запуск

```bash
git clone --recurse-submodules https://github.com/lifespirit/TorrentMonitor-go.git
cd TorrentMonitor-go
CGO_ENABLED=1 go run ./cmd/torrentmonitor
```

По умолчанию интерфейс слушает `:8080`, а база создаётся в `~/.local/share/torrentmonitor-go/torrentmonitor.sqlite3`.

Откройте `http://127.0.0.1:8080`.

## Документация

- [Сборка и установка](docs/BUILD_INSTALL.md)
- [Chromium в headless Weston](docs/CHROMIUM_WESTON.md)
- [Пример системного сервиса](docs/torrentmonitor.service)
- [Репозиторий шаблонов](https://github.com/lifespirit/TorrentMonitor-templates)

## Основные переменные окружения

| Переменная | Назначение | Значение по умолчанию |
| --- | --- | --- |
| `TM_LISTEN` | Адрес HTTP-сервера | `:8080` |
| `TM_STORE` | `sqlite`, `json` или `memory` | `sqlite` |
| `TM_DATA_FILE` | Путь к базе/JSON | каталог данных пользователя |
| `TM_MONITOR_INTERVAL` | Начальный интервал планировщика | `15m` |
| `TM_TEMPLATE_DIR` | Каталог внешних шаблонов | каталог данных пользователя |
| `TM_TEMPLATE_SOURCE_URL` | ZIP/YAML/JSON/каталог с шаблонами | пусто |
| `TM_BROWSER_CONNECT_URL` | DevTools URL внешнего Chromium | пусто |
| `TM_BROWSER_BINARY` | Путь к Chromium | автоопределение |
| `TM_BROWSER_PROFILE_BASE` | Базовый каталог профилей | каталог данных пользователя |
| `TM_BROWSER_DEBUG` | Дополнительные логи BrowserBroker | `false` |

Большинство параметров после первого запуска настраивается и сохраняется через веб-интерфейс.

## Шаблоны

Встроенные шаблоны всегда доступны. Внешние YAML/JSON из `template_directory` загружаются поверх них: новый `id` дополняет registry, а совпадающий `id` переопределяет встроенный шаблон.

Рекомендуемый источник официального набора:

```text
https://github.com/lifespirit/TorrentMonitor-templates/archive/refs/heads/main.zip
```

## Лицензия

GNU General Public License v3.0. См. [LICENSE](LICENSE).
