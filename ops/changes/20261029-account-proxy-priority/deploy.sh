#!/usr/bin/env bash
# Operator-run application-only deployment. --check never stops or installs anything.
set -euo pipefail
umask 077

APP_DIR=/home/pk/workspace/sub2api
RELEASE_DIR=/home/pk/workspace/sub2api-egress-delivery/5ed77ee58816d638350e26ff22f10918f8dff82a
EXPECTED_ARCHIVE=218abba00e736e3d4999bd060fbc16fb01c4700d4be16193fb5cd99a67887142
EXPECTED_BINARY=89d3c99845988494d44ede994911b288263b7b7b10e302ca78bfbc0ee2943a32
EXPECTED_PREVIOUS=1966de62f70ac92bb8840e6c886b7d4885cd88765cfdf13af2ed4e795e6300a8
GUARD=/usr/local/libexec/egress_guard.py

[[ $# -eq 1 && ( $1 == --check || $1 == --apply ) ]] || {
  echo "Usage: sudo bash $0 --check|--apply" >&2; exit 2;
}
[[ $EUID -eq 0 ]] || { echo 'Run with sudo bash.' >&2; exit 2; }

hash_file() { sha256sum -- "$1" | cut -d ' ' -f 1; }
health() {
  local body
  body=$(curl -fsS --max-time "${1:-5}" http://127.0.0.1:8080/health 2>/dev/null) || return 1
  python3 -c 'import json,sys
try:
    payload = json.load(sys.stdin)
except ValueError:
    sys.exit(1)
sys.exit(0 if isinstance(payload, dict) and payload.get("status") == "ok" else 1)
' <<<"$body"
}
[[ $(hash_file "$RELEASE_DIR/sub2api-linux-amd64.tar.gz") == "$EXPECTED_ARCHIVE" ]]
[[ $(hash_file "$RELEASE_DIR/sub2api") == "$EXPECTED_BINARY" ]]
[[ -f "$APP_DIR/bin/sub2api" && ! -L "$APP_DIR/bin/sub2api" ]]
systemctl is-active --quiet sub2api mihomo dnsmasq-claude sub2api-egress
[[ $(systemctl show sub2api -p WorkingDirectory --value) == "$APP_DIR" ]]
systemctl show sub2api -p ExecStart --value | grep -Fq "path=$APP_DIR/bin/sub2api ;"
"$GUARD" verify >/dev/null
health || { echo "Preflight failed: health endpoint is not ready; no changes made." >&2; exit 1; }
python3 - "$APP_DIR/data/config.yaml" <<'PY'
import sys
import yaml
try:
    with open(sys.argv[1]) as f:
        cfg = yaml.safe_load(f)
    assert cfg['gateway']['claude_egress']['enabled'] is True
except Exception:
    sys.exit('Cannot confirm gateway.claude_egress.enabled=true; no changes made.')
PY
current=$(hash_file "$APP_DIR/bin/sub2api")
pid=$(systemctl show sub2api -p MainPID --value)
[[ $pid =~ ^[1-9][0-9]*$ && $(hash_file "/proc/$pid/exe") == "$current" ]]
if [[ $current == "$EXPECTED_BINARY" ]]; then
  echo 'The verified candidate is already running; nothing to install.'
  exit 0
fi
[[ $current == "$EXPECTED_PREVIOUS" ]] || {
  echo 'Running version differs from the reviewed baseline; no changes made.' >&2; exit 1;
}
if [[ $1 == --check ]]; then
  echo 'Preflight passed. No files installed and no services stopped.'
  exit 0
fi

backup=$(mktemp -d "/root/sub2api-proxy-priority-$(TZ=Asia/Singapore date +%Y%m%d-%H%M%S).XXXXXX")
cp -p -- "$APP_DIR/bin/sub2api" "$backup/sub2api.previous"
cp -pL -- "$APP_DIR/data/config.yaml" "$backup/config.yaml"
(cd "$backup" && sha256sum sub2api.previous config.yaml > SHA256SUMS)
[[ $(hash_file "$backup/sub2api.previous") == "$EXPECTED_PREVIOUS" ]]
cat > "$backup/rollback.sh" <<'ROLLBACK'
#!/usr/bin/env bash
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo 'Run with sudo bash.' >&2; exit 2; }
backup=$(cd -- "$(dirname -- "$0")" && pwd -P)
app=/home/pk/workspace/sub2api
health() {
  local body
  body=$(curl -fsS --max-time "${1:-5}" http://127.0.0.1:8080/health 2>/dev/null) || return 1
  python3 -c 'import json,sys
try:
    payload = json.load(sys.stdin)
except ValueError:
    sys.exit(1)
sys.exit(0 if isinstance(payload, dict) and payload.get("status") == "ok" else 1)
' <<<"$body"
}
cd "$backup"
grep '  sub2api.previous$' SHA256SUMS | sha256sum -c -
stage=$(mktemp "$app/bin/.sub2api-rollback.XXXXXX")
trap 'rm -f -- "$stage"' EXIT
install -o root -g root -m 0755 sub2api.previous "$stage"
systemctl stop sub2api
mv -fT -- "$stage" "$app/bin/sub2api"
systemctl reset-failed sub2api
systemctl start sub2api
[[ $(sha256sum "$app/bin/sub2api" | cut -d ' ' -f 1) == $(sha256sum sub2api.previous | cut -d ' ' -f 1) ]]
for attempt in {1..30}; do
  if systemctl is-active --quiet sub2api && health 3; then
    echo 'Previous application restored and healthy. Account bindings and config were not restored or cleared.'
    exit 0
  fi
  sleep 1
done
echo 'Previous binary restored, but health failed. Inspect sub2api journal.' >&2
exit 1
ROLLBACK
chmod 0700 "$backup/rollback.sh"
bash -n "$backup/rollback.sh"
echo "Backup: $backup"
echo "Manual rollback: sudo bash $backup/rollback.sh"

stage=$(mktemp "$APP_DIR/bin/.sub2api-priority.XXXXXX")
needs_rollback=0
cleanup() {
  rc=$?
  trap - EXIT
  if (( rc != 0 && needs_rollback )); then
    echo 'Deployment failed; restoring the previous application.' >&2
    if ! bash "$backup/rollback.sh"; then
      echo "Automatic recovery failed. Run: sudo bash $backup/rollback.sh" >&2
    fi
  fi
  rm -f -- "$stage"
  exit "$rc"
}
trap cleanup EXIT
install -o root -g root -m 0755 "$RELEASE_DIR/sub2api" "$stage"
[[ $(hash_file "$stage") == "$EXPECTED_BINARY" ]]
# Recheck after preparation, before stopping the live service.
[[ $(hash_file "$APP_DIR/bin/sub2api") == "$EXPECTED_PREVIOUS" ]]
cmp -s -- "$APP_DIR/data/config.yaml" "$backup/config.yaml"
needs_rollback=1
systemctl stop sub2api
mv -fT -- "$stage" "$APP_DIR/bin/sub2api"
systemctl reset-failed sub2api
systemctl start sub2api
ready=0
echo "Waiting for Sub2API readiness (up to 30 checks)..."
for attempt in {1..30}; do
  if systemctl is-active --quiet sub2api && health; then ready=1; break; fi
  sleep 1
done
[[ $ready == 1 ]] || { echo "Application health did not become ready; deployment will be rolled back." >&2; exit 1; }
pid=$(systemctl show sub2api -p MainPID --value)
[[ $pid =~ ^[1-9][0-9]*$ && $(hash_file "/proc/$pid/exe") == "$EXPECTED_BINARY" ]]
cmp -s -- "$APP_DIR/data/config.yaml" "$backup/config.yaml"
"$GUARD" verify >/dev/null
needs_rollback=0
echo 'Deployment passed: commit 5ed77ee58816d638350e26ff22f10918f8dff82a, running binary verified, health=ok.'
echo "Backup and rollback script: $backup"
