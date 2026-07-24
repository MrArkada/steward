# Steward

Telegram-бот для управления Linux-сервером. Написан на Go, работает через
long polling — веб-сервер, открытый порт и nginx ему не нужны, только
исходящие HTTPS-запросы к API Telegram.

Главная фишка — минимальное потребление ресурсов:
в простое бот ест меньше 20 МБ RAM, метрики читает напрямую из /proc
и /sys, не запуская top/free/df.

На чистом сервере (Debian/Ubuntu) при первом запуске сам ставит
и настраивает fail2ban и UFW, потом присылает отчёт в Telegram.

Что умеет

- Статус: CPU/RAM/swap/диски/uptime/load/сеть, топ процессов, live-режим
  с автообновлением, kill процесса с подтверждением.
- Fail2ban: установка, jail'ы, забаненные IP с GeoIP, бан/разбан, whitelist.
- Firewall (UFW): установка, базовая настройка, правила. SSH-порт берётся
  из sshd_config до включения файрвола — доступ не отрежется.
- Сервисы: автообнаружение популярных юнитов, start/stop/restart, логи
  (длинные присылает файлом), watchdog с кнопкой перезапуска в алерте.
- Диск: маунты, топ-10 тяжёлых каталогов, очистка apt / journald / старых
  логов / /tmp / docker с отчётом об освобождённом месте.
- Оптимизация: BBR, пресеты sysctl (веб/БД/VPN), swap-файл, лимит journald,
  автоочистка по расписанию.
- Безопасность: SSH hardening по шагам с проверкой sshd -t, последние
  входы, топ атакующих IP, контроль открытых портов, unattended-upgrades.
- Обновления системы: проверка, полное обновление, флаг reboot-required.
- Доступ: смена паролей (сообщение с паролем самоудаляется через минуту),
  SSH-ключи, пользователи, sudo.
- Питание: reboot/poweroff с двойным подтверждением, счётчиком активных
  SSH-сессий и кулдауном 10 минут, отложенная перезагрузка.
- Настройки: пороги алертов, тихие часы, ежедневный дайджест, whitelist.

Фоновые алерты: превышение CPU/RAM/диска/load, успешный SSH-вход (шлётся
даже в тихие часы), падение отслеживаемых сервисов, новый открытый порт,
дайджест. Антифлуд: повторный алерт того же типа не плодит сообщения,
а увеличивает счётчик в уже отправленном.

Чужие пользователи молча игнорируются (whitelist по Telegram ID). Всё
деструктивное — через «Ты уверен?» → «Подтвердить». Действия пишутся
в audit.log, пароли и токены никуда не логируются.

Требования

- Debian/Ubuntu — полная поддержка. CentOS/RHEL/Fedora, Arch, Alpine —
  частичная: функции, завязанные на apt/UFW, ответят «не поддерживается
  на этой ОС», бот не упадёт.
- root (бот управляет сервисами, пакетами, файрволом, пользователями).
- systemd или OpenRC.
- Для сборки: Go 1.24+.

Установка

    sudo sh install.sh

или без вопросов:

    sudo TOKEN="123:ABC..." USER_ID=123456789 sh install.sh --yes

Установщик сам определит ОС, пакетный менеджер и init-систему, возьмёт
готовый ./server-bot рядом со скриптом или соберёт его из исходников
(при отсутствии Go скачает его с go.dev в /usr/local/go с проверкой
SHA256), установит файлы в /opt/serverbot, создаст config.yaml,
зарегистрирует и запустит сервис.

Удаление:

    sudo sh install.sh --uninstall          # config останется
    sudo sh install.sh --uninstall --purge  # полностью

Ручная установка

    mkdir -p /opt/serverbot
    cp server-bot /opt/serverbot/
    cp config.example.yaml /opt/serverbot/config.yaml
    nano /opt/serverbot/config.yaml   # вставить токен и свой Telegram ID
    chmod 600 /opt/serverbot/config.yaml
    cp server-bot.service /etc/systemd/system/
    systemctl daemon-reload
    systemctl enable --now server-bot

Токен — у @BotFather (/newbot). Свой Telegram ID — у @userinfobot;
первый ID в allowed_users — суперадмин.

Сборка

    go build -ldflags="-s -w" -o server-bot ./cmd/bot

Статический бинарник под Linux с любой машины:

    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o server-bot ./cmd/bot

Структура

    cmd/bot/main.go        точка входа, graceful shutdown
    internal/config        config.yaml: загрузка, валидация, сохранение
    internal/metrics       парсинг /proc и /sys, кэш с TTL 4 сек
    internal/bot           gotgbot: диспетчер, long polling
    internal/handlers      обработчики кнопок (файл на раздел меню)
    internal/alerts        фоновые мониторинги и антифлуд-рассылка
    internal/sysutil       обёртки над systemctl/apt/ufw (context timeout)
    internal/security      whitelist, аудит-лог, генератор паролей
    internal/storage       state.json (атомарная запись)
    internal/detect        определение ОС, пакетного менеджера, init
    internal/geoip         GeoIP-кэш поверх ip-api.com
    internal/logging       лог с ротацией (10 МБ × 3 файла)

Лицензия

MIT
