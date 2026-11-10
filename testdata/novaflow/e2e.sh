#!/bin/sh
# End-to-end test of `nova flow` against the configured instance and cell:
# install NovaFlow, round-trip a flow through push/get/backup/delete/restore,
# uninstall, and check that the flow survives a reinstall.
# Usage: testdata/novaflow/e2e.sh [workdir]
set -eu

dir=$(cd "$(dirname "$0")" && pwd)
work=${1:-$(mktemp -d)}
id="ci-flow-$$"

# temporary, see testdata/retry.sh: every nova call is run again if the connection drops
retry=$(cd "$(dirname "$0")/.." && pwd)/retry.sh
nova() { "$retry" nova "$@"; }

step() { printf '\n== %s\n' "$*"; }
fail() { echo "FAIL: $*" >&2; exit 1; }
has_flow() { nova flow list | awk 'NR > 1 { print $1 }' | grep -qx "$id"; }

step "status"
nova flow status

step "install"
nova flow install --yes --timeout 15m
nova flow status | tee "$work/status.txt"
grep -q '^Installed: *yes' "$work/status.txt" || fail "NovaFlow not installed"

step "push $id"
nova flow push "$dir/flow.json" --id "$id" --name "CI flow" --validate
has_flow || fail "$id missing in list"

step "get $id"
nova flow get "$id" -o "$work/got.json"
grep -q "\"id\": \"$id\"" "$work/got.json" || fail "get returned another flow"
grep -q '"revision"' "$work/got.json" || fail "get returned no revision"

step "push without --force must be rejected"
if nova flow push "$dir/flow.json" --id "$id" > "$work/push.txt" 2>&1; then
  fail "push replaced an existing flow without --force"
fi
grep '^Error' "$work/push.txt"

step "backup download"
nova flow backup download "$work/backup.tar.gz"
tar tzf "$work/backup.tar.gz" | tee "$work/backup.txt"
grep -qx "flows/$id.json" "$work/backup.txt" || fail "$id missing in backup"
grep -qx "manifest.json" "$work/backup.txt" || fail "manifest missing in backup"

step "delete $id"
nova flow delete "$id" --yes
if has_flow; then fail "$id still listed after delete"; fi

step "backup restore"
nova flow backup restore "$work/backup.tar.gz" --dry-run
nova flow backup restore "$work/backup.tar.gz" --yes
has_flow || fail "$id not restored"

step "uninstall"
nova flow uninstall --yes
nova flow status | tee "$work/status.txt"
grep -q '^Installed: *no' "$work/status.txt" || fail "NovaFlow still installed"

step "reinstall keeps the flows"
nova flow install --yes --timeout 15m
has_flow || fail "$id lost after reinstall"

step "cleanup"
nova flow delete "$id" --yes
echo "NovaFlow e2e passed"
