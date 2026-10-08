#!/usr/bin/env bash
#
# Обновление приложения на сервере: git pull -> сборка -> перезапуск сервиса.
# Запускать из-под пользователя, у которого есть sudo на systemctl.
#
set -euo pipefail

# Папка, где лежит сам скрипт (и исходники) — не привязываемся к абсолютному пути.
cd "$(dirname "$(readlink -f "$0")")"

SERVICE="financialanalyzer"
BINARY="financialanalyzer"
MODULE="github.com/VxVxN/financialanalyzer"
if [ -z "${GO:-}" ]; then
  if [ -x /usr/local/go/bin/go ]; then
    GO=/usr/local/go/bin/go        # прод: Go установлен вручную сюда
  elif command -v go >/dev/null 2>&1; then
    GO="$(command -v go)"          # локально: Go из PATH (например, snap)
  else
    echo "!! Не найден Go: нет /usr/local/go/bin/go и команды go в PATH" >&2
    exit 1
  fi
fi

echo "==> Обновляю код (git pull)"
git pull --ff-only

echo "==> Собираю новый бинарник"
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
"$GO" build -ldflags "-s -w -X ${MODULE}/internal/version.Version=${VERSION} -X ${MODULE}/internal/version.Commit=${COMMIT} -X ${MODULE}/internal/version.Date=${DATE}" \
  -o "${BINARY}.new" ./cmd/plot

# Бэкап базы делается отдельно на стороне PostgreSQL (pg_dump / pg_basebackup),
# а не здесь при выкладке бинарника.

echo "==> Останавливаю сервис"
sudo systemctl stop "$SERVICE"

echo "==> Подменяю бинарник"
mv -f "${BINARY}.new" "$BINARY"

echo "==> Запускаю сервис"
sudo systemctl start "$SERVICE"

# Небольшая пауза и проверка, что сервис реально поднялся.
sleep 1
if systemctl is-active --quiet "$SERVICE"; then
  echo "==> Готово: сервис работает"
  systemctl status "$SERVICE" --no-pager || true
else
  echo "!! Сервис не поднялся — смотри логи:" >&2
  journalctl -u "$SERVICE" -e --no-pager | tail -n 30 >&2
  exit 1
fi
