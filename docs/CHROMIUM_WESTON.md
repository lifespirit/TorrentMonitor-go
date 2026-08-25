# Chromium в headless Weston

Этот вариант нужен серверу без обычной графической сессии, если хотя бы один трекер работает в режиме **Chromium**. Weston создаёт виртуальную Wayland-сессию, Chromium хранит постоянный профиль, а TorrentMonitor подключается к нему по Chrome DevTools Protocol.

Native HTTP не требует Weston или Chromium.

## Установка

Arch Linux:

```bash
sudo pacman -S --needed weston chromium
```

Убедитесь, что пользователь `torrentmonitor` и его домашний каталог созданы по инструкции [BUILD_INSTALL.md](BUILD_INSTALL.md).

## Сервис

```bash
sudo install -Dm0644 docs/torrentmonitor-browser.service \
  /etc/systemd/system/torrentmonitor-browser.service
sudo systemctl daemon-reload
sudo systemctl enable --now torrentmonitor-browser.service
```

Проверка:

```bash
systemctl status torrentmonitor-browser.service
journalctl -u torrentmonitor-browser.service -f
curl http://127.0.0.1:9222/json/version
```

DevTools должен слушать только loopback. Не публикуйте порт `9222`: доступ к нему даёт управление браузером и его авторизованными сессиями.

## Подключение TorrentMonitor

В настройках Chromium выберите внешний браузер и укажите:

```text
http://127.0.0.1:9222
```

Либо выполните `sudo systemctl edit torrentmonitor.service`:

```ini
[Unit]
After=torrentmonitor-browser.service

[Service]
Environment=TM_BROWSER_CONNECT_URL=http://127.0.0.1:9222
```

Затем перезапустите TorrentMonitor. Сохранённый через веб-интерфейс адрес имеет приоритет над начальным значением из окружения.

## CAPTCHA и ручная авторизация

1. В разделе **Учётные данные** выберите режим `Chromium`.
2. Нажмите **Проверить логин**.
3. Откройте интерактивную сессию.
4. Выполните вход и пройдите CAPTCHA/Cloudflare.
5. Повторите проверку логина.

Профиль хранится в `/var/lib/torrentmonitor/.local/share/torrentmonitor-go/browser/torrentmonitor` и сохраняется между перезапусками.

## Отладка

```bash
journalctl -u torrentmonitor-browser.service -b --no-pager
coredumpctl list chromium
coredumpctl info chromium
coredumpctl debug chromium
```

Для подробного Wayland-протокола временно запустите Chromium через `/usr/bin/env WAYLAND_DEBUG=client`. Это создаёт большой объём журналов и не должно использоваться постоянно.
