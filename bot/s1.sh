#!/usr/bin/env bash
set -euo pipefail

# === Paths ===
ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="$ROOT_DIR/bin"
RUN_DIR="$ROOT_DIR/.run"
LOG_DIR="$ROOT_DIR/logs"

SERVER_NAME="whserver"
BOT_NAME="whbot"

SERVER_SRC="./cmd/whserver"
BOT_SRC="./cmd/whbot"

SERVER_BIN="$BIN_DIR/$SERVER_NAME"
BOT_BIN="$BIN_DIR/$BOT_NAME"

SERVER_PID="$RUN_DIR/$SERVER_NAME.pid"
BOT_PID="$RUN_DIR/$BOT_NAME.pid"

SERVER_LOG="$LOG_DIR/$SERVER_NAME.log"
BOT_LOG="$LOG_DIR/$BOT_NAME.log"

# === Helpers ===
ensure_dirs() {
  mkdir -p "$BIN_DIR" "$RUN_DIR" "$LOG_DIR"
}

is_running() {
  # $1 = pidfile, $2 = expected binary name
  [[ -f "$1" ]] || return 1
  local pid; pid="$(cat "$1" 2>/dev/null || true)"
  [[ -n "${pid:-}" ]] || return 1
  if ps -p "$pid" -o cmd= >/dev/null 2>&1; then
    ps -p "$pid" -o cmd= | grep -q "$2" && return 0 || return 1
  fi
  return 1
}

stop_one() {
  # $1 = pidfile, $2 = human name
  if is_running "$1" "$2"; then
    local pid; pid="$(cat "$1")"
    echo "→ Parando $2 (pid=$pid)…"
    kill -TERM "$pid" 2>/dev/null || true
    # espera suave hasta 6s
    for _ in {1..12}; do
      sleep 0.5
      if ! ps -p "$pid" >/dev/null 2>&1; then
        break
      fi
    done
    if ps -p "$pid" >/dev/null 2>&1; then
      echo "⚠ $2 no cerró a tiempo; enviando SIGKILL"
      kill -KILL "$pid" 2>/dev/null || true
    fi
    rm -f "$1"
    echo "✔ $2 detenido."
  else
    rm -f "$1" 2>/dev/null || true
    echo "ℹ $2 no estaba corriendo."
  fi
}

start_server() {
  echo "→ Iniciando $SERVER_NAME…"
  nohup "$SERVER_BIN" >> "$SERVER_LOG" 2>&1 &
  echo $! > "$SERVER_PID"
  echo "✔ $SERVER_NAME pid=$(cat "$SERVER_PID") (log: $SERVER_LOG)"
}

start_bot() {
  echo "→ Iniciando $BOT_NAME…"
  # El binario ya fue compilado con CGO habilitado
  nohup "$BOT_BIN" >> "$BOT_LOG" 2>&1 &
  echo $! > "$BOT_PID"
  echo "✔ $BOT_NAME pid=$(cat "$BOT_PID") (log: $BOT_LOG)"
}

build_all() {
  echo "→ Compilando…"
  ensure_dirs
  # Server
  (cd "$ROOT_DIR" && go build -o "$SERVER_BIN" "$SERVER_SRC")
  # Bot (con CGO, como en tus pruebas)
  (cd "$ROOT_DIR" && CGO_ENABLED=1 go build -o "$BOT_BIN" "$BOT_SRC")
  echo "✔ Build OK."
}

start_all() {
  ensure_dirs
  # Logs con cabecera de sesión
  {
    echo ""
    echo "===== $(date '+%F %T') :: START $SERVER_NAME ====="
  } >> "$SERVER_LOG"
  {
    echo ""
    echo "===== $(date '+%F %T') :: START $BOT_NAME ====="
  } >> "$BOT_LOG"

  start_server
  # Pequeña espera para que el server escuche el :9000
  sleep 0.7
  start_bot
}

stop_all() {
  stop_one "$BOT_PID" "$BOT_NAME"
  stop_one "$SERVER_PID" "$SERVER_NAME"
}

status_all() {
  if is_running "$SERVER_PID" "$SERVER_NAME"; then
    echo "✔ $SERVER_NAME corriendo (pid=$(cat "$SERVER_PID"))"
  else
    echo "✖ $SERVER_NAME detenido"
  fi
  if is_running "$BOT_PID" "$BOT_NAME"; then
    echo "✔ $BOT_NAME corriendo (pid=$(cat "$BOT_PID"))"
  else
    echo "✖ $BOT_NAME detenido"
  fi
}

logs_follow() {
  echo "Mostrando logs (Ctrl+C para salir)…"
  touch "$SERVER_LOG" "$BOT_LOG"
  tail -n 100 -f "$SERVER_LOG" "$BOT_LOG"
}

default_restart_smart() {
  ensure_dirs
  # Si ya está corriendo, parar limpio; luego build y start
  if is_running "$SERVER_PID" "$SERVER_NAME" || is_running "$BOT_PID" "$BOT_NAME"; then
    echo "↻ Reiniciando limpio…"
    stop_all
  else
    echo "↻ No había procesos corriendo; arranque limpio…"
  end_if=true
  fi
  build_all
  start_all
  status_all
}

# === CLI ===
cmd="${1:-restart}"
case "$cmd" in
  start)
    build_all
    start_all
    ;;
  stop)
    stop_all
    ;;
  restart)
    default_restart_smart
    ;;
  status)
    status_all
    ;;
  logs)
    logs_follow
    ;;
  *)
    echo "Uso: $0 {start|stop|restart|status|logs}"
    echo "Sin argumentos: restart"
    default_restart_smart
    ;;
esac
