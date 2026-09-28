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

### FlareSolverr

Для трекеров с Cloudflare/JavaScript-проверками TorrentMonitor обращается к внешнему [FlareSolverr](https://github.com/FlareSolverr/FlareSolverr) через HTTP API. Для каждого трекера создаётся отдельная постоянная FlareSolverr-сессия, поэтому cookies после challenge и авторизации переиспользуются следующими проверками.

TorrentMonitor умеет автоматически выполнять описанный в YAML form-login через FlareSolverr. Ручной Chromium, CDP, графическая сессия и Weston больше не нужны.

Современный FlareSolverr возвращает HTML, cookies и User-Agent, но не бинарные загрузки. Поэтому страницы и login проходят через FlareSolverr, а файл `.torrent` скачивается TorrentMonitor напрямую с теми же cookies, User-Agent и proxy. Если FlareSolverr находится на другом хосте, для Cloudflare-защищённых download-endpoint важно обеспечить тот же внешний IP (например, общим proxy).

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
- [Пример системного сервиса](docs/torrentmonitor.service)
- [Репозиторий шаблонов](https://github.com/lifespirit/TorrentMonitor-templates)

## Основные переменные окружения

| Переменная | Назначение | Значение по умолчанию |
| --- | --- | --- |
| `TM_LISTEN` | Адрес HTTP-сервера | `:8080` |
| `TM_STORE` | `sqlite`, `json` или `memory` | `sqlite` |
| `TM_DATA_FILE` | Путь к базе/JSON | каталог данных пользователя |
| `TM_MONITOR_INTERVAL` | Начальный интервал планировщика | `15m` |
| `TM_FLARESOLVERR_URL` | API FlareSolverr | `http://127.0.0.1:8191/v1` |
| `TM_TEMPLATE_DIR` | Каталог внешних шаблонов | каталог данных пользователя |
| `TM_TEMPLATE_SOURCE_URL` | ZIP/YAML/JSON/каталог с шаблонами | пусто |

Большинство параметров после первого запуска настраивается и сохраняется через веб-интерфейс.

## Шаблоны

Встроенные шаблоны всегда доступны. Внешние YAML/JSON из `template_directory` загружаются поверх них: новый `id` дополняет registry, а совпадающий `id` переопределяет встроенный шаблон.

Рекомендуемый источник официального набора:

```text
https://github.com/lifespirit/TorrentMonitor-templates/archive/refs/heads/main.zip
```

## Лицензия

GNU General Public License v3.0. См. [LICENSE](LICENSE).
