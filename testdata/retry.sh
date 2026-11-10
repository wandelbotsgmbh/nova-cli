#!/usr/bin/env bash
# Temporary: runs a command again when it failed because the instance dropped or
# refused the connection. The shared ingress controller of the cloud instances is
# OOM-killed under load (developer-portal/applications!1403), which cuts off requests
# in flight. Remove this wrapper once that is fixed.
# Usage: testdata/retry.sh <command> [args...]
set -uo pipefail

attempts=5
pattern='GOAWAY|unexpected EOF|: EOF$|connection refused|connection reset|server closed the connection'
err=$(mktemp)
trap 'rm -f "$err"' EXIT

for i in $(seq 1 "$attempts"); do
  "$@" 2> >(tee "$err" >&2)
  rc=$?
  wait # for tee, so $err is complete
  if [ "$rc" -eq 0 ] || [ "$i" -eq "$attempts" ] || ! grep -Eq "$pattern" "$err"; then
    exit "$rc"
  fi
  echo "retry: the instance dropped the connection (attempt $i/$attempts), running '$*' again in $((i * 5))s" >&2
  sleep $((i * 5))
done
