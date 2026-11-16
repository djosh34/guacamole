#!/usr/bin/env bash
# Start, stop or check the test Chromium with the DevTools protocol (CDP) on
# 127.0.0.1:$CDP_PORT. It runs on the host, outside compose.
#
#   scripts/chromium.sh start            # headless (default)
#   scripts/chromium.sh start --headful  # real window under Xvfb
#   scripts/chromium.sh status
#   scripts/chromium.sh stop
#
# Env: CHROME (binary), CDP_PORT (default 47922), STATE_DIR (default /tmp/guactest-chromium)
set -euo pipefail

CHROME="${CHROME:-$(ls -d "$HOME"/.cache/ms-playwright/chromium-*/chrome-linux*/chrome 2>/dev/null | sort -V | tail -1)}"
CDP_PORT="${CDP_PORT:-47922}"
STATE_DIR="${STATE_DIR:-/tmp/guactest-chromium}"
PROFILE="$STATE_DIR/profile"
PIDFILE="$STATE_DIR/chrome.pid"
XVFB_PIDFILE="$STATE_DIR/xvfb.pid"
LOG="$STATE_DIR/chrome.log"
DISPLAY_NUM="${DISPLAY_NUM:-:47}"

alive() { [ -f "$1" ] && kill -0 "$(cat "$1")" 2>/dev/null; }

case "${1:-}" in
  start)
    if alive "$PIDFILE"; then echo "already running, pid $(cat "$PIDFILE")"; exit 0; fi
    [ -x "$CHROME" ] || { echo "Chromium not found; set CHROME=/path/to/chrome" >&2; exit 1; }
    mkdir -p "$PROFILE/Default"
    # Turn off the password manager. After a password form is submitted,
    # Chromium's "Save password?" prompt makes the tab drop all trusted input
    # (CDP Input.* key and mouse events never reach the page), headless or not.
    python3 -I - "$PROFILE/Default/Preferences" <<'PY'
import json, os, sys
path = sys.argv[1]
prefs = json.load(open(path)) if os.path.exists(path) else {}
prefs["credentials_enable_service"] = False
prefs.setdefault("profile", {}).update(password_manager_enabled=False, password_manager_leak_detection=False)
json.dump(prefs, open(path, "w"))
PY
    args=(
      --remote-debugging-address=127.0.0.1
      --remote-debugging-port="$CDP_PORT"
      --user-data-dir="$PROFILE"
      --no-first-run --no-default-browser-check
      --disable-gpu
      --window-size=1280,800
      about:blank
    )
    if [ "${2:-}" = "--headful" ]; then
      setsid Xvfb "$DISPLAY_NUM" -screen 0 1280x800x24 -nolisten tcp >"$STATE_DIR/xvfb.log" 2>&1 &
      echo $! > "$XVFB_PIDFILE"
      sleep 1
      DISPLAY="$DISPLAY_NUM" setsid "$CHROME" "${args[@]}" >"$LOG" 2>&1 < /dev/null &
    else
      setsid "$CHROME" --headless=new "${args[@]}" >"$LOG" 2>&1 < /dev/null &
    fi
    echo $! > "$PIDFILE"
    for _ in $(seq 1 50); do
      if curl -fs "http://127.0.0.1:$CDP_PORT/json/version" >/dev/null; then
        echo "Chromium pid $(cat "$PIDFILE"), CDP at http://127.0.0.1:$CDP_PORT"
        curl -s "http://127.0.0.1:$CDP_PORT/json/version"
        exit 0
      fi
      sleep 0.2
    done
    echo "Chromium did not open the CDP port; see $LOG" >&2
    exit 1
    ;;
  stop)
    alive "$PIDFILE" && kill "$(cat "$PIDFILE")" && echo "stopped Chromium"
    alive "$XVFB_PIDFILE" && kill "$(cat "$XVFB_PIDFILE")" && echo "stopped Xvfb"
    rm -f "$PIDFILE" "$XVFB_PIDFILE"
    ;;
  status)
    if alive "$PIDFILE"; then
      echo "running, pid $(cat "$PIDFILE")"
      curl -s "http://127.0.0.1:$CDP_PORT/json/version"
    else
      echo "not running"; exit 1
    fi
    ;;
  *)
    echo "usage: $0 start [--headful] | stop | status" >&2; exit 2
    ;;
esac
