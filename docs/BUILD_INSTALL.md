# Сборка и установка TorrentMonitor Go

## Требования

Для SQLite-сборки нужны Go 1.22+, C-компилятор, `pkg-config`, SQLite 3 с заголовками и Git. Chromium и Weston нужны только для браузерного режима.

Arch Linux:

```bash
sudo pacman -S --needed go gcc pkgconf sqlite git
```

Debian/Ubuntu:

```bash
sudo apt install golang-go build-essential pkg-config libsqlite3-dev git
```

Для Chromium-режима дополнительно установите `chromium` и `weston`.

## Получение исходников

```bash
git clone --recurse-submodules https://github.com/lifespirit/TorrentMonitor-go.git
cd TorrentMonitor-go
```

Для существующего клона:

```bash
git submodule update --init --recursive
```

Submodule нужен для разработки шаблонов. Встроенные шаблоны уже входят в бинарник.

## Сборка с SQLite

```bash
CGO_ENABLED=1 go build \
  -trimpath \
  -ldflags='-s -w' \
  -o torrentmonitor-go \
  ./cmd/torrentmonitor
```

SQLite требует CGO. Сборка с `CGO_ENABLED=0` может использовать только `TM_STORE=json` или `TM_STORE=memory`.

## Слабые ARM-устройства

Предпочтительно собирать непосредственно на целевом устройстве: это автоматически использует правильную ABI и установленную SQLite.

```bash
GOMAXPROCS=2 CGO_ENABLED=1 go build \
  -trimpath \
  -ldflags='-s -w' \
  -o torrentmonitor-go \
  ./cmd/torrentmonitor
```

На устройстве с небольшим объёмом RAM перед сборкой полезно включить swap/zram и остановить Chromium. После установки Go и компилятор можно удалить. Для минимальной нагрузки используйте режим **Native HTTP**.

### Кросс-компиляция

SQLite-сборке нужен ARM-кросс-компилятор и ARM-версия SQLite в sysroot. Поэтому рекомендуются нативная сборка на ARM, ARM-контейнер/chroot или CI runner нужной архитектуры.

Без SQLite возможна простая статическая сборка:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build \
  -trimpath -ldflags='-s -w' \
  -o torrentmonitor-go-arm64 \
  ./cmd/torrentmonitor
```

Запускайте её с `TM_STORE=json`.

## Системный пользователь и каталоги

```bash
sudo install -Dm0755 torrentmonitor-go /usr/bin/torrentmonitor-go
```

Создайте `/etc/sysusers.d/torrentmonitor.conf`:

```ini
u torrentmonitor - "TorrentMonitor Go service" /var/lib/torrentmonitor /usr/bin/nologin
```

Создайте `/etc/tmpfiles.d/torrentmonitor.conf`:

```ini
d /var/lib/torrentmonitor 0750 torrentmonitor torrentmonitor -
```

Примените:

```bash
sudo systemd-sysusers /etc/sysusers.d/torrentmonitor.conf
sudo systemd-tmpfiles --create /etc/tmpfiles.d/torrentmonitor.conf
```

## systemd

```bash
sudo install -Dm0644 docs/torrentmonitor.service \
  /etc/systemd/system/torrentmonitor.service
sudo systemctl daemon-reload
sudo systemctl enable --now torrentmonitor.service
```

Проверка:

```bash
systemctl status torrentmonitor.service
journalctl -u torrentmonitor.service -f
```

База по умолчанию: `/var/lib/torrentmonitor/.local/share/torrentmonitor-go/torrentmonitor.sqlite3`.

Для явной конфигурации выполните `sudo systemctl edit torrentmonitor.service`:

```ini
[Service]
Environment=TM_LISTEN=127.0.0.1:8080
Environment=TM_DATA_FILE=/var/lib/torrentmonitor/torrentmonitor.sqlite3
Environment=TM_TEMPLATE_DIR=/var/lib/torrentmonitor/templates
```

После изменений:

```bash
sudo systemctl daemon-reload
sudo systemctl restart torrentmonitor.service
```

## Обновление

```bash
sudo systemctl stop torrentmonitor.service
sudo install -m0755 torrentmonitor-go /usr/bin/torrentmonitor-go
sudo systemctl start torrentmonitor.service
```

Перед крупным обновлением остановите сервис и сохраните базу вместе с файлами `-wal` и `-shm`.
