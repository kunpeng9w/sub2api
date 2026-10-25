#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
BASE=/home/pk/workspace/sub2api
LIB=/usr/local/lib/sub2api
# Historical official-binary bootstrap, not a custom-build deployment command.
[[ ${1:-} == --bootstrap-official ]] || { echo 'Fresh host only: install.sh --bootstrap-official' >&2; exit 1; }
[[ ! -f "$BASE/data/.installed" ]] || { echo 'Existing installation: refuse to overwrite its custom binary.' >&2; exit 1; }
[[ $EUID == 0 ]] || { echo 'Run this script with sudo.' >&2; exit 1; }
[[ -d "$BASE/release" && -d "$BASE/ops/scripts" && ! -L "$BASE" ]] || exit 1
trap 'echo "Installation stopped at line $LINENO; no data was removed. Inspect the error before retrying." >&2' ERR
exec 9>/run/lock/sub2api-install.lock
flock -n 9
cd "$BASE/release"
sha256sum --ignore-missing -c checksums.txt
[[ -f sub2api_0.2.18_linux_amd64.tar.gz ]]
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y postgresql-16 redis-server acl ca-certificates curl openssl python3-yaml
id sub2api >/dev/null 2>&1 || useradd --system --user-group --home-dir "$BASE/data" --shell /usr/sbin/nologin sub2api
install -d -m 0755 "$BASE/bin" "$LIB"
install -d -o sub2api -g sub2api -m 0700 "$BASE/data"
install -d -o root -g root -m 0700 "$BASE/backups"
install -d -o pk -g pk -m 0700 "$BASE/.operator"
chmod 0750 "$BASE"
setfacl -m u:sub2api:--x /home/pk /home/pk/workspace
setfacl -m u:sub2api:r-x "$BASE"
tar -xzf sub2api_0.2.18_linux_amd64.tar.gz -C "$BASE/bin" sub2api
chown root:root "$BASE/bin/sub2api"
chmod 0755 "$BASE/bin/sub2api"
"$BASE/bin/sub2api" -version
for file in provision.py backup.sh verify.sh observe.py api.py; do
  install -o root -g root -m 0755 "$BASE/ops/scripts/$file" "$LIB/$file"
done
python3 "$LIB/provision.py" secrets

# Dedicated cluster: preserve the package configuration and use a late override.
install -d -m 0755 /etc/postgresql/16/main/conf.d
install -o root -g root -m 0644 "$BASE/ops/config/postgresql/99-sub2api.conf" /etc/postgresql/16/main/conf.d/99-sub2api.conf
chmod 0644 /etc/postgresql/16/main/conf.d/99-sub2api.conf
runuser -u postgres -- test -r /etc/postgresql/16/main/conf.d/99-sub2api.conf
if [[ ! -f /etc/redis/redis.conf.pre-sub2api ]]; then
  cp -a /etc/redis/redis.conf /etc/redis/redis.conf.pre-sub2api
fi
python3 "$LIB/provision.py" redis
systemctl enable postgresql redis-server
systemctl restart postgresql@16-main redis-server
python3 "$LIB/provision.py" database

install -o root -g root -m 0644 "$BASE/ops/config/systemd/sub2api.service" /etc/systemd/system/sub2api.service
chmod 0644 /etc/systemd/system/sub2api.service
systemd-analyze verify /etc/systemd/system/sub2api.service
systemctl daemon-reload
systemctl enable sub2api
if [[ ! -f "$BASE/data/.installed" ]]; then
  [[ ! -e "$BASE/data/config.yaml" ]] || { echo 'Partial configuration exists; inspect before retrying.' >&2; exit 1; }
  systemctl start sub2api
  python3 "$LIB/provision.py" wizard
fi
# The wizard exits cleanly; Restart=on-failure needs an explicit restart here.
systemctl stop sub2api
python3 "$LIB/provision.py" config
systemctl reset-failed sub2api
systemctl start sub2api
python3 "$LIB/provision.py" ready
python3 "$LIB/provision.py" policy
systemctl restart sub2api
python3 "$LIB/provision.py" ready
runuser -u pk -- python3 "$LIB/api.py" bootstrap

install -o root -g root -m 0644 "$BASE/ops/config/systemd/sub2api-backup.service" /etc/systemd/system/sub2api-backup.service
install -o root -g root -m 0644 "$BASE/ops/config/systemd/sub2api-backup.timer" /etc/systemd/system/sub2api-backup.timer
chmod 0644 /etc/systemd/system/sub2api-backup.service /etc/systemd/system/sub2api-backup.timer
systemd-analyze verify /etc/systemd/system/sub2api-backup.service /etc/systemd/system/sub2api-backup.timer
systemctl daemon-reload
systemctl enable --now sub2api-backup.timer
"$LIB/verify.sh"
systemctl start sub2api-backup.service
"$LIB/backup.sh" --verify-latest
# Establish dependency-restart recovery without rebooting the server.
systemctl restart postgresql@16-main redis-server
systemctl restart sub2api
python3 "$LIB/provision.py" ready
runuser -u pk -- python3 "$LIB/api.py" check
systemctl reset-failed sub2api
echo 'BASE INSTALLATION COMPLETE. OAuth authorization and upstream acceptance are still pending.'
echo 'Admin credentials: /home/pk/workspace/sub2api/.operator/admin.json (pk only; do not paste into chat)'
echo 'SSH tunnel on your Mac: ssh -N -L 18080:127.0.0.1:8080 aly'
