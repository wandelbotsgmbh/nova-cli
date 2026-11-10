#!/bin/sh
# End-to-end test of `nova api` against the targeted instance and its cell: reads,
# search and schema on the instance's spec, a write round trip through the
# collision store, --dry-run, HTTP errors, the refusal of WebSocket streams, and
# an installed app reached through its path on the instance.
# Usage: [NOVA_API_E2E_APP=<app>] testdata/api/e2e.sh [workdir]
# The app defaults to the first app of the cell. With NOVA_API_E2E_APP, the app
# must be one created by `nova app create` (python_app template): its own API is
# called too (openapi.json, NOVAx' GET /programs listing start_here).
set -eu

work=${1:-$(mktemp -d)}
key="nova-api-e2e-$$"
collider="/api/v2/cells/{cell}/store/collision/colliders/$key"

# temporary, see testdata/retry.sh: every nova call is run again if the connection drops
retry=$(cd "$(dirname "$0")/.." && pwd)/retry.sh
nova() { "$retry" nova "$@"; }

step() { printf '\n== %s\n' "$*"; }
fail() { echo "FAIL: $*" >&2; exit 1; }

step "GET cells"
nova api /api/v2/cells | tee "$work/cells.json"
grep -q '"cell"' "$work/cells.json" || fail "cell missing in /api/v2/cells"

step "GET a controller of the cell"
nova api '/api/v2/cells/{cell}/controllers' | tee "$work/controllers.json"
ctrl=$(tr -d '[]" \n' < "$work/controllers.json" | cut -d, -f1)
[ -n "$ctrl" ] || fail "no controller in the cell"
nova api "/api/v2/cells/{cell}/controllers/$ctrl" | tee "$work/controller.json"
grep -q '"kind"' "$work/controller.json" || fail "controller without configuration.kind"

step "--include prints the status line"
nova api /api/v2/version --include | tee "$work/include.txt"
head -n 1 "$work/include.txt" | grep -q ' 200 OK' || fail "no status line"

step "search and schema"
nova api search robot controller --limit 3 | tee "$work/search.json"
grep -q '"operationId": "getRobotController"' "$work/search.json" || fail "search misses getRobotController"
nova api schema storeCollider > "$work/schema.json"
grep -q '"requestBody"' "$work/schema.json" || fail "schema without request body"

step "--dry-run sends nothing"
nova api "$collider" -X PUT -F 'shape[shape_type]=sphere' -F 'shape[radius]=10' --dry-run | tee "$work/dry.json"
grep -q '"operationId": "storeCollider"' "$work/dry.json" || fail "dry run names no operation"
if nova api "$collider" > /dev/null 2>&1; then fail "--dry-run stored $key"; fi

step "PUT, GET, DELETE $key"
nova api "$collider" -X PUT -F 'shape[shape_type]=sphere' -F 'shape[radius]=10'
nova api "$collider" | tee "$work/collider.json"
grep -q '"radius":10' "$work/collider.json" || fail "stored collider not read back"
nova api "$collider" -X DELETE

step "HTTP 404 prints the body and exits 1"
set +e
nova api "$collider" > "$work/404.json" 2> "$work/404.txt"
code=$?
set -e
cat "$work/404.txt"
[ "$code" -eq 1 ] || fail "exit $code for a deleted collider, want 1"
grep -q 'HTTP 404' "$work/404.txt" || fail "no HTTP 404 on stderr"

step "WebSocket streams are refused with exit 3"
set +e
nova api "/api/v2/cells/{cell}/controllers/$ctrl/state-stream" > "$work/stream.out" 2> "$work/stream.txt"
code=$?
set -e
cat "$work/stream.txt"
[ "$code" -eq 3 ] || fail "exit $code for a stream, want 3"
[ ! -s "$work/stream.out" ] || fail "stdout not empty for a refused stream"
grep -q '"code":"STREAM_NOT_SUPPORTED"' "$work/stream.txt" || fail "no typed error on stderr"

step "reach an installed app"
app=${NOVA_API_E2E_APP:-}
if [ -z "$app" ]; then
  nova api '/api/v2/cells/{cell}/apps' | tee "$work/apps.json"
  app=$(tr -d '[]" \n' < "$work/apps.json" | cut -d, -f1)
fi
[ -n "$app" ] || fail "no app installed in the cell"
tries=0
until nova api "/{cell}/$app/" --include > "$work/app.txt" 2> "$work/app.err"; do
  tries=$((tries + 1))
  [ "$tries" -lt 30 ] || { cat "$work/app.err"; fail "app $app not reachable at /{cell}/$app/"; }
  echo "app $app not ready yet: $(tail -n 1 "$work/app.err")"
  sleep 10
done
head -n 1 "$work/app.txt"
head -n 1 "$work/app.txt" | grep -q ' 200 OK' || fail "app $app answered $(head -n 1 "$work/app.txt")"

if [ -n "${NOVA_API_E2E_APP:-}" ]; then
  step "call the API of app $app"
  nova api "/{cell}/$app/openapi.json" > "$work/app-openapi.json"
  grep -q '"/programs"' "$work/app-openapi.json" || fail "app $app has no /programs in its openapi.json"
  nova api "/{cell}/$app/programs" | tee "$work/programs.json"
  echo
  grep -q '"start_here"' "$work/programs.json" || fail "app $app doesn't list the program start_here"
  nova api "/{cell}/$app/programs/start_here" | tee "$work/program.json"
  echo
  grep -q '"start_here"' "$work/program.json" || fail "GET /programs/start_here returned another program"
fi

step "ok"
