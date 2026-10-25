#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
BASE=/home/pk/workspace/sub2api
[[ $EUID == 0 ]] || exit 1
exec 9>/run/lock/sub2api-backup.lock
flock -n 9
if [[ ${1:-} == --verify-latest ]]; then
  latest=$(find "$BASE/backups" -maxdepth 1 -type f -name 'sub2api-*.tar.gz' -printf '%f\n' | sort | tail -n 1)
  [[ -n "$latest" ]]
  temp=$(mktemp -d)
  restoredb="sub2api_restore_$(date +%s)"
  cleanup() {
    runuser -u postgres -- dropdb --if-exists "$restoredb"
    rm -rf "$temp"
  }
  trap cleanup EXIT
  tar -xzf "$BASE/backups/$latest" -C "$temp"
  runuser -u postgres -- createdb -O sub2api "$restoredb"
  # pg_restore consumes stdin so postgres cannot read private backup directories.
  runuser -u postgres -- pg_restore --exit-on-error --no-owner --role=sub2api -d "$restoredb" < "$temp/database.dump"
  runuser -u postgres -- psql -X -v ON_ERROR_STOP=1 -d "$restoredb" -c 'SELECT count(*) AS users FROM users; SELECT count(*) AS accounts FROM accounts; SELECT count(*) AS api_keys FROM api_keys; SELECT count(*) AS account_groups FROM account_groups;'
  runuser -u postgres -- psql -X -v ON_ERROR_STOP=1 -d "$restoredb" -c 'SET ROLE sub2api; SELECT count(*) AS readable_accounts FROM accounts;'
  python3 - "$temp" "$restoredb" <<'PY'
import json,pathlib,subprocess,sys,yaml
root=pathlib.Path(sys.argv[1])
cfg=yaml.safe_load((root/'data/config.yaml').read_text())
s=json.loads((root/'native-secrets.json').read_text())
assert cfg['jwt']['secret']==s['jwt'], 'JWT secret mismatch in backup'
assert cfg['totp']['encryption_key']==s['totp'], 'TOTP key mismatch in backup'
assert cfg['database']['password']==s['database'], 'Database credential mismatch in backup'
assert cfg['redis']['password']==s['redis'], 'Redis credential mismatch in backup'
assert (root/'data/.installed').exists()
keyfile=root/'operator/test-key.json'
if keyfile.exists():
    key=json.loads(keyfile.read_text())
    # Capture privately; neither the restored key nor the archived key is printed.
    restored=subprocess.check_output(['runuser','-u','postgres','--','psql','-X','-At','-v','ON_ERROR_STOP=1','-d',sys.argv[2],'-c','SELECT key FROM api_keys WHERE id='+str(int(key['id']))],text=True).strip()
    assert restored==key['key'], 'Restored downstream key does not match archived operator key'
print('PASS: restored downstream key and all fixed configuration secrets match the archived private files.')
PY
  echo 'PASS: latest backup restored to temporary database; config and fixed keys verified.'
  exit 0
fi
[[ -f "$BASE/data/.installed" ]]
temp=$(mktemp -d "$BASE/backups/.pending.XXXXXX")
was_active=0
cleanup() {
  if [[ $was_active == 1 ]]; then systemctl start sub2api || true; fi
  rm -rf "$temp"
}
trap cleanup EXIT
if systemctl is-active --quiet sub2api; then
  was_active=1
  systemctl stop sub2api
fi
runuser -u postgres -- pg_dump -Fc sub2api > "$temp/database.dump"
cp -a "$BASE/data" "$temp/data"
install -d -m 0700 "$temp/operator"
for file in admin.json test-user.json test-key.json; do
  [[ ! -f "$BASE/.operator/$file" ]] || cp "$BASE/.operator/$file" "$temp/operator/"
done
cp /etc/sub2api-native-secrets.json "$temp/native-secrets.json"
install -d "$temp/systemd" "$temp/config" "$temp/scripts"
cp /etc/systemd/system/sub2api*.service /etc/systemd/system/sub2api*.timer "$temp/systemd/"
cp /etc/postgresql/16/main/conf.d/99-sub2api.conf "$temp/config/postgresql.conf"
cp /etc/redis/redis.conf "$temp/config/redis.conf"
if [[ -d /etc/nginx ]]; then cp -a /etc/nginx "$temp/config/nginx"; fi
if [[ -d /etc/letsencrypt ]]; then cp -a /etc/letsencrypt "$temp/letsencrypt"; fi
cp /usr/local/lib/sub2api/* "$temp/scripts/"
cp "$BASE/release/checksums.txt" "$temp/checksums.txt"
printf '%s\n' 'v0.2.18 linux_amd64' > "$temp/VERSION"
if [[ $was_active == 1 ]]; then
  systemctl start sub2api
  python3 /usr/local/lib/sub2api/provision.py ready
  was_active=0
fi
stamp=$(TZ=Asia/Shanghai date +%Y%m%dT%H%M%S%z)
archive="$BASE/backups/sub2api-$stamp.tar.gz"
tar -czf "$archive.partial" -C "$temp" .
mv "$archive.partial" "$archive"
# Export a pk-readable copy for retrieval to the local Mac; it contains secrets.
install -o pk -g pk -m 0600 "$archive" "$BASE/.operator/latest-backup.tar.gz"
python3 - "$BASE/backups" <<'PY'
import pathlib,sys
files=sorted(pathlib.Path(sys.argv[1]).glob('sub2api-*.tar.gz'))
for path in files[:-3]: path.unlink()
PY
echo "Backup complete: $(basename "$archive") (service briefly paused for consistency)"
