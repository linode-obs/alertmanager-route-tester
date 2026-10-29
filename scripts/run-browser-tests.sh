#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

# Ports stay separate from the development server and Go integration tests.
APP_PORT=${ART_BROWSER_PORT:-18081}
AM_PORT=${ART_BROWSER_AM_PORT:-19095}
TEMP=$(mktemp -d)
AM_PID=""
APP_PID=""
cleanup() {
    if [[ -n "$APP_PID" ]]; then kill "$APP_PID" 2>/dev/null || true; wait "$APP_PID" 2>/dev/null || true; fi
    if [[ -n "$AM_PID" ]]; then kill "$AM_PID" 2>/dev/null || true; wait "$AM_PID" 2>/dev/null || true; fi
    rm -rf "$TEMP"
}
trap cleanup EXIT
for port in "$APP_PORT" "$AM_PORT"; do
    if lsof -tiTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
        echo "Browser test port $port is already in use" >&2
        exit 1
    fi
done
printf 'alertmanager-route-tester:\n  server:\n    listen: 127.0.0.1:%s\nalertmanagers:\n  local:\n    url: http://127.0.0.1:%s\n  second:\n    url: http://127.0.0.1:%s\n' "$APP_PORT" "$AM_PORT" "$AM_PORT" > "$TEMP/config.yaml"
./bin/alertmanager/alertmanager --config.file=testdata/alertmanager.yml --web.listen-address="127.0.0.1:$AM_PORT" --cluster.listen-address= --storage.path="$TEMP/data" > "$TEMP/alertmanager.log" 2>&1 &
AM_PID=$!
./bin/alertmanager-route-tester -config "$TEMP/config.yaml" > "$TEMP/app.log" 2>&1 &
APP_PID=$!
for i in {1..100}; do
    if curl -fsS "http://127.0.0.1:$APP_PORT/healthz" >/dev/null 2>&1 && curl -fsS "http://127.0.0.1:$AM_PORT/api/v2/status" >/dev/null 2>&1; then break; fi
    if ! kill -0 "$APP_PID" 2>/dev/null || ! kill -0 "$AM_PID" 2>/dev/null; then
        echo "A browser test service stopped before readiness. Logs: $TEMP" >&2
        exit 1
    fi
    sleep 0.1
done
ART_BROWSER_URL="http://127.0.0.1:$APP_PORT" bash scripts/test-browser.sh "${1:-all}"
