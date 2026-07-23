#!/bin/sh
set -eu

INSTALL_DIR="${INSTALL_DIR:-/opt/serverbot}"
GO_VERSION="${GO_VERSION:-1.24.5}"
UNIT_NAME="server-bot"
ASSUME_YES=0
DO_UNINSTALL=0
DO_PURGE=0

usage() {
    cat <<'EOF'
Использование:
  sudo sh install.sh                                 интерактивная установка
  sudo TOKEN="1:abc" USER_ID=123 sh install.sh --yes без вопросов
  sudo sh install.sh --uninstall                     удалить сервис (config останется)
  sudo sh install.sh --uninstall --purge             удалить всё, включая config

Переменные окружения: TOKEN, USER_ID, INSTALL_DIR (по умолч. /opt/serverbot),
GO_VERSION (версия скачиваемого Go, по умолч. 1.24.5).
EOF
    exit 0
}

for arg in "$@"; do
    case "$arg" in
        --yes|-y)        ASSUME_YES=1 ;;
        --uninstall)     DO_UNINSTALL=1 ;;
        --purge)         DO_PURGE=1 ;;
        --help|-h)       usage ;;
        *)
            echo "Неизвестный аргумент: $arg (см. --help)" >&2
            exit 2
            ;;
    esac
done

if [ -t 1 ]; then
    C_OK='\033[32m'; C_WARN='\033[33m'; C_ERR='\033[31m'; C_OFF='\033[0m'
else
    C_OK=''; C_WARN=''; C_ERR=''; C_OFF=''
fi
log()  { printf '%s[+]%s %s\n'  "$C_OK"   "$C_OFF" "$*"; }
warn() { printf '%s[!]%s %s\n'  "$C_WARN" "$C_OFF" "$*" >&2; }
die()  { printf '%s[x]%s %s\n'  "$C_ERR"  "$C_OFF" "$*" >&2; exit 1; }

[ "$(id -u)" = "0" ] || die "Запустите от root: sudo sh $0 $*"

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)

OS_ID="unknown"; OS_LIKE=""
if [ -r /etc/os-release ]; then
    OS_ID=$(  sed -n 's/^ID=\(.*\)$/\1/p'       /etc/os-release | tr -d '"' | head -n1)
    OS_LIKE=$(sed -n 's/^ID_LIKE=\(.*\)$/\1/p'  /etc/os-release | tr -d '"' | head -n1)
fi
[ -n "$OS_ID" ] || OS_ID="unknown"

PM="none"
for c in apt-get dnf yum pacman apk; do
    if command -v "$c" >/dev/null 2>&1; then PM="$c"; break; fi
done
if [ "$PM" = "none" ]; then
    case " $OS_ID $OS_LIKE " in
        *debian*|*ubuntu*) PM="apt-get" ;;
        *rhel*|*fedora*|*centos*) PM="dnf" ;;
        *arch*) PM="pacman" ;;
        *alpine*) PM="apk" ;;
    esac
fi

PID1=$(cat /proc/1/comm 2>/dev/null || echo "unknown")
INIT="none"
if [ "$PID1" = "systemd" ] && command -v systemctl >/dev/null 2>&1; then
    INIT="systemd"
elif command -v rc-service >/dev/null 2>&1 || [ "$PID1" = "init" -a "$OS_ID" = "alpine" ]; then
    INIT="openrc"
fi

MACHINE=$(uname -m)
case "$MACHINE" in
    x86_64|amd64)   GOARCH="amd64" ;;
    aarch64|arm64)  GOARCH="arm64" ;;
    armv7l|armv6l)  GOARCH="armv6l" ;;
    i686|i386)      GOARCH="386" ;;
    *)              die "Неподдерживаемая архитектура: $MACHINE" ;;
esac

log "ОС: $OS_ID (семейство: ${OS_LIKE:-—}), PM: $PM, init: $INIT (pid1: $PID1), arch: $GOARCH"

if [ "$DO_UNINSTALL" = "1" ]; then
    log "Удаление $UNIT_NAME..."
    if [ "$INIT" = "systemd" ]; then
        systemctl disable --now "$UNIT_NAME" 2>/dev/null || true
        rm -f "/etc/systemd/system/$UNIT_NAME.service"
        systemctl daemon-reload 2>/dev/null || true
        log "systemd unit удалён"
    elif [ "$INIT" = "openrc" ]; then
        rc-service "$UNIT_NAME" stop 2>/dev/null || true
        rc-update del "$UNIT_NAME" default 2>/dev/null || true
        rm -f "/etc/init.d/$UNIT_NAME"
        log "OpenRC-сервис удалён"
    else
        pkill -f "$INSTALL_DIR/server-bot" 2>/dev/null || true
        warn "init-система не опознана — процесс остановлен по имени"
    fi
    if [ "$DO_PURGE" = "1" ]; then
        rm -rf "$INSTALL_DIR"
        log "Каталог $INSTALL_DIR удалён полностью"
    else
        rm -f "$INSTALL_DIR/server-bot"
        log "Бинарник удалён; config.yaml/state.json оставлены в $INSTALL_DIR"
    fi
    log "Готово."
    exit 0
fi

pm_install() {
    log "Устанавливаю пакет: $1 (через $PM)"
    case "$PM" in
        apt-get) DEBIAN_FRONTEND=noninteractive apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y "$1" ;;
        dnf)     dnf install -y "$1" ;;
        yum)     yum install -y "$1" ;;
        pacman)  pacman -Sy --noconfirm "$1" ;;
        apk)     apk add "$1" ;;
        *)       return 1 ;;
    esac
}

go_major_minor() {
    RAW=$("$1" version 2>/dev/null | awk '{print $3}' | sed 's/^go//')
    MAJ=$(printf '%s' "$RAW" | cut -d. -f1)
    MIN=$(printf '%s' "$RAW" | cut -d. -f2)
    case "$MAJ$MIN" in (*[!0-9]*|"") echo "0 0" ;; (*) echo "$MAJ $MIN" ;; esac
}

go_expected_sha256() {
    case "$1" in
        go1.24.5.linux-amd64.tar.gz)
            echo "10ad9e86233e74c0f6590fe5426895de6bf388964210eac34a6d83f38918ecdc"; return 0 ;;
        go1.24.5.linux-arm64.tar.gz)
            echo "0df02e6aeb3d3c06c95ff201d575907c736d6c62cfa4b6934c11203f1d600ffa"; return 0 ;;
    esac
    JSON=$(mktemp)
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL --retry 2 -o "$JSON" "https://go.dev/dl/?mode=json&include=all" 2>/dev/null \
        || curl -fsSL --retry 2 -o "$JSON" "https://go.dev/dl/?mode=json" 2>/dev/null || true
    else
        wget -q -O "$JSON" "https://go.dev/dl/?mode=json&include=all" 2>/dev/null \
        || wget -q -O "$JSON" "https://go.dev/dl/?mode=json" 2>/dev/null || true
    fi
    tr -d ' \n' < "$JSON" | grep -o "\"filename\":\"$1\"[^}]*" \
        | sed -n 's/.*"sha256":"\([a-f0-9]*\)".*/\1/p' | head -n1
    rm -f "$JSON"
}

install_go() {
    if [ -x /usr/local/go/bin/go ]; then
        set -- $(go_major_minor /usr/local/go/bin/go)
        if [ "$1" -gt 1 ] || { [ "$1" = "1" ] && [ "$2" -ge 24 ]; }; then
            GO_BIN=/usr/local/go/bin/go
            log "Найден Go в /usr/local/go ($(/usr/local/go/bin/go version | awk '{print $3}'))"
            return 0
        fi
    fi

    command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1 || pm_install curl \
        || die "Нужен curl или wget для скачивания Go — установите вручную"

    TARBALL="go${GO_VERSION}.linux-${GOARCH}.tar.gz"
    URL="https://go.dev/dl/${TARBALL}"
    TMP_DL=$(mktemp -d)
    trap 'rm -rf "$TMP_DL"' EXIT

    log "Скачиваю $URL"
    if command -v curl >/dev/null 2>&1; then
        curl -fSL --retry 3 -o "$TMP_DL/$TARBALL" "$URL" || die "Не удалось скачать $URL"
    else
        wget -q -O "$TMP_DL/$TARBALL" "$URL" || die "Не удалось скачать $URL"
    fi

    if command -v sha256sum >/dev/null 2>&1; then
        EXPECT=$(go_expected_sha256 "$TARBALL")
        if [ -n "$EXPECT" ]; then
            ACTUAL=$(sha256sum "$TMP_DL/$TARBALL" | awk '{print $1}')
            [ "$EXPECT" = "$ACTUAL" ] || die "SHA256 не сошёлся ($ACTUAL != $EXPECT) — загрузка прервана"
            log "SHA256 совпал"
        else
            warn "Эталонный SHA256 получить не удалось — проверка пропущена"
        fi
    else
        warn "sha256sum не найден — проверка целостности пропущена"
    fi

    log "Устанавливаю Go $GO_VERSION в /usr/local/go"
    rm -rf /usr/local/go
    tar -C /usr/local -xzf "$TMP_DL/$TARBALL" || die "Не удалось распаковать $TARBALL"
    rm -rf "$TMP_DL"; trap - EXIT

    printf 'export PATH=$PATH:/usr/local/go/bin\n' > /etc/profile.d/golang.sh 2>/dev/null || true

    GO_BIN=/usr/local/go/bin/go
    [ -x "$GO_BIN" ] && "$GO_BIN" version >/dev/null 2>&1 || die "Go установлен, но не запускается"
    log "Установлен $($GO_BIN version | awk '{print $3}')"
}

ensure_go() {
    GO_BIN=""
    if command -v go >/dev/null 2>&1; then
        GO_BIN=$(command -v go)
    elif [ -x /usr/local/go/bin/go ]; then
        GO_BIN=/usr/local/go/bin/go
    fi

    if [ -n "$GO_BIN" ]; then
        set -- $(go_major_minor "$GO_BIN")
        if [ "$1" -gt 1 ] || { [ "$1" = "1" ] && [ "$2" -ge 24 ]; }; then
            BUILD_ENV=""
            log "Найден $("$GO_BIN" version | awk '{print $3}') — сборка напрямую"
            return 0
        fi
        if [ "$1" = "1" ] && [ "$2" -ge 21 ]; then
            BUILD_ENV="GOTOOLCHAIN=auto"
            warn "Go 1.$2 < 1.24 — нужный тулчейн будет скачан автоматически (GOTOOLCHAIN=auto, нужна сеть)"
            return 0
        fi
        warn "Go 1.$2 слишком старый — ставлю свежий из официального архива"
    else
        warn "Go не найден — устанавливаю с go.dev"
    fi
    install_go
    BUILD_ENV=""
}

BIN_SRC=""

if [ -f "$SCRIPT_DIR/server-bot" ] && head -c 4 "$SCRIPT_DIR/server-bot" 2>/dev/null | grep -q 'ELF'; then
    BIN_SRC="$SCRIPT_DIR/server-bot"
    log "Найден готовый бинарник: $BIN_SRC"
else
    [ -f "$SCRIPT_DIR/go.mod" ] || die "Нет ни готового ./server-bot, ни исходников (go.mod) рядом со скриптом. Соберите бинарник (README) или запустите установщик из каталога проекта."

    ensure_go

    log "Сборка бинарника (CGO_ENABLED=0, статический)..."
    ( cd "$SCRIPT_DIR" && env CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" ${BUILD_ENV:-} "$GO_BIN" build -ldflags="-s -w" -o server-bot ./cmd/bot ) \
        || die "Сборка не удалась (см. вывод выше)"
    BIN_SRC="$SCRIPT_DIR/server-bot"
    log "Собрано: $BIN_SRC"
fi

if [ "$INIT" = "systemd" ]; then
    systemctl stop "$UNIT_NAME" 2>/dev/null || true
elif [ "$INIT" = "openrc" ]; then
    rc-service "$UNIT_NAME" stop 2>/dev/null || true
fi

mkdir -p "$INSTALL_DIR"
cp "$BIN_SRC" "$INSTALL_DIR/server-bot"
chmod 755 "$INSTALL_DIR/server-bot"
log "Бинарник установлен: $INSTALL_DIR/server-bot"

CONFIG="$INSTALL_DIR/config.yaml"
TOKEN="${TOKEN:-}"
USER_ID="${USER_ID:-}"

valid_token() { printf '%s' "$1" | grep -qE '^[0-9]{5,12}:[A-Za-z0-9_-]{30,60}$'; }
valid_uid()   { printf '%s' "$1" | grep -qE '^[0-9]{1,12}$'; }

config_has_real_token() {
    grep -qE '^token:[[:space:]]*"[0-9]{5,12}:[A-Za-z0-9_-]{30,60}"' "$1" 2>/dev/null
}

NEED_CONFIG=1
if [ -f "$CONFIG" ]; then
    if [ -n "$TOKEN" ] && [ -n "$USER_ID" ]; then
        log "config.yaml существует, но токен/ID заданы явно — перезаписываю (бэкап: config.yaml.bak)"
        cp "$CONFIG" "$CONFIG.bak"
    elif config_has_real_token "$CONFIG"; then
        log "config.yaml уже существует и заполнен — не трогаю"
        NEED_CONFIG=0
    else
        warn "config.yaml существует, но токен в нём незаполнен/невалиден — пересоздаю (бэкап: config.yaml.bak)"
        cp "$CONFIG" "$CONFIG.bak"
    fi
fi

if [ "$NEED_CONFIG" = "1" ]; then
    if [ -n "$TOKEN" ] && ! valid_token "$TOKEN"; then
        die "Токен не соответствует формату Telegram (123456789:AA…, ~46 символов): '$TOKEN'"
    fi
    if [ -n "$USER_ID" ] && ! valid_uid "$USER_ID"; then
        die "Telegram ID должен быть числом: '$USER_ID'"
    fi

    while [ -z "$TOKEN" ] && [ "$ASSUME_YES" != "1" ] && [ -t 0 ]; do
        printf 'Введите токен бота (от @BotFather, формат 123456789:AA…): '
        read -r TOKEN
        if ! valid_token "$TOKEN"; then
            warn "Это не похоже на токен @BotFather (нужны цифры:длинный-ключ, ~46 символов). Попробуйте ещё раз."
            TOKEN=""
        fi
    done
    while [ -z "$USER_ID" ] && [ "$ASSUME_YES" != "1" ] && [ -t 0 ]; do
        printf 'Введите ваш Telegram user ID (число, узнать: @userinfobot): '
        read -r USER_ID
        if ! valid_uid "$USER_ID"; then
            warn "ID должен быть числом. Попробуйте ещё раз."
            USER_ID=""
        fi
    done

    if [ -n "$TOKEN" ] && [ -n "$USER_ID" ]; then
        if [ -f "$SCRIPT_DIR/config.example.yaml" ]; then
            awk -v tok="$TOKEN" -v uid="$USER_ID" '
                /^token:[ \t]/                { print "token: \"" tok "\""; next }
                /^allowed_users:/             { inau=1; print; next }
                inau && /^[ \t]*-[ \t]*[0-9]+/ { if (!done) { print "  - " uid; done=1 } ; next }
                /^[a-z_]+:/                   { inau=0 }
                { print }
            ' "$SCRIPT_DIR/config.example.yaml" > "$CONFIG" \
                || die "Не удалось создать $CONFIG"
        else
            cat > "$CONFIG" <<EOF
token: "$TOKEN"
allowed_users:
  - $USER_ID
EOF
        fi
        if ! config_has_real_token "$CONFIG"; then
            die "Подстановка токена в $CONFIG не сработала — проверьте файл вручную"
        fi
        log "config.yaml создан (токен и ID подставлены)"
    else
        if [ -f "$SCRIPT_DIR/config.example.yaml" ]; then
            cp "$SCRIPT_DIR/config.example.yaml" "$CONFIG"
        else
            printf 'token: "ЗАМЕНИТЕ_НА_ТОКЕН"\nallowed_users:\n  - 0\n' > "$CONFIG"
        fi
        warn "Токен/ID не заданы — бот НЕ будет запущен. Отредактируйте $CONFIG"
        warn "и запустите вручную: systemctl restart $UNIT_NAME"
    fi
    chmod 600 "$CONFIG"
fi

CONFIG_READY=1
if ! config_has_real_token "$CONFIG"; then
    CONFIG_READY=0
fi

if [ "$INIT" = "systemd" ]; then
    if [ -f "$SCRIPT_DIR/server-bot.service" ]; then
        sed "s|/opt/serverbot|$INSTALL_DIR|g" "$SCRIPT_DIR/server-bot.service" \
            > "/etc/systemd/system/$UNIT_NAME.service"
    else
        cat > "/etc/systemd/system/$UNIT_NAME.service" <<EOF
[Unit]
Description=Steward — бот управления виртуалкой (Telegram)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/server-bot -config $CONFIG
Restart=on-failure
RestartSec=5
MemoryMax=100M
CPUQuota=25%
NoNewPrivileges=true
LimitNOFILE=1024

[Install]
WantedBy=multi-user.target
EOF
    fi
    systemctl daemon-reload
    if [ "$CONFIG_READY" = "1" ]; then
        if systemctl is-active --quiet "$UNIT_NAME" 2>/dev/null; then
            systemctl restart "$UNIT_NAME"
            log "systemd: сервис $UNIT_NAME перезапущен (обновлён)"
        else
            systemctl enable --now "$UNIT_NAME"
            log "systemd: сервис $UNIT_NAME включён и запущен"
        fi
    else
        systemctl enable "$UNIT_NAME" 2>/dev/null || true
        warn "systemd: сервис прописан, но НЕ запущен — сначала заполните $CONFIG"
    fi
elif [ "$INIT" = "openrc" ]; then
    cat > "/etc/init.d/$UNIT_NAME" <<EOF
#!/sbin/openrc-run
name="$UNIT_NAME"
description="Steward — Telegram-бот управления сервером"
command="$INSTALL_DIR/server-bot"
command_args="-config $CONFIG"
command_background="yes"
pidfile="/run/$UNIT_NAME.pid"
directory="$INSTALL_DIR"
depend() {
    need net
    after firewall
}
EOF
    chmod 755 "/etc/init.d/$UNIT_NAME"
    rc-update add "$UNIT_NAME" default
    if [ "$CONFIG_READY" = "1" ]; then
        rc-service "$UNIT_NAME" restart
        log "OpenRC: сервис $UNIT_NAME добавлен в default и запущен"
    else
        warn "OpenRC: сервис прописан, но НЕ запущен — сначала заполните $CONFIG"
    fi
else
    warn "init-система не опознана (pid1: $PID1). Автозапуск не настроен."
    warn "Запустите вручную:  nohup $INSTALL_DIR/server-bot -config $CONFIG >>$INSTALL_DIR/nohup.log 2>&1 &"
fi

echo
log "Установка завершена."
echo "  Бинарник:  $INSTALL_DIR/server-bot"
echo "  Конфиг:    $CONFIG"
if [ "$CONFIG_READY" != "1" ]; then
    echo
    warn "В конфиге нет настоящего токена — бот не запущен."
    warn "Отредактируйте $CONFIG (token и allowed_users), затем:"
    echo "       systemctl restart $UNIT_NAME   (или rc-service $UNIT_NAME restart)"
fi
if [ "$INIT" = "systemd" ]; then
    echo "  Статус:    systemctl status $UNIT_NAME"
    echo "  Логи:      journalctl -u $UNIT_NAME -f   (и $INSTALL_DIR/bot.log)"
elif [ "$INIT" = "openrc" ]; then
    echo "  Статус:    rc-service $UNIT_NAME status"
    echo "  Логи:      $INSTALL_DIR/bot.log"
fi
echo
echo "  Дальше: напишите боту /start или /menu в Telegram."
echo "  Чистый сервер: разделы «🛡 Fail2ban» и «🔥 Firewall» предложат"
echo "  установить и настроить защиту одной кнопкой."
