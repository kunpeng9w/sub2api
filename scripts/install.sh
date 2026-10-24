#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
BASE=/home/pk/workspace/sub2api
LIB=/usr/local/lib/sub2api
[[ $EUID == 0 ]] || { echo 'Run this script with sudo.' >&2; exit 1; }
[[ -d "$BASE/release" && -d "$BASE/scripts" && ! -L "$BASE" ]] || exit 1
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
  install -o root -g root -m 0755 "$BASE/scripts/$file" "$LIB/$file"
done
python3 "$LIB/provision.py" secrets

# Dedicated cluster: preserve the package configuration and use a late override.
install -d -m 0755 /etc/postgresql/16/main/conf.d
cat > /etc/postgresql/16/main/conf.d/99-sub2api.conf <<'EOF'
listen_addresses = '127.0.0.1'
max_connections = 30
shared_buffers = 64MB
work_mem = 2MB
maintenance_work_mem = 32MB
EOF
chmod 0644 /etc/postgresql/16/main/conf.d/99-sub2api.conf
runuser -u postgres -- test -r /etc/postgresql/16/main/conf.d/99-sub2api.conf
if [[ ! -f /etc/redis/redis.conf.pre-sub2api ]]; then
  cp -a /etc/redis/redis.conf /etc/redis/redis.conf.pre-sub2api
fi
python3 "$LIB/provision.py" redis
systemctl enable postgresql redis-server
systemctl restart postgresql@16-main redis-server
python3 "$LIB/provision.py" database

cat > /etc/systemd/system/sub2api.service <<'EOF'
[Unit]
Description=Sub2API v0.2.18 personal gateway
After=network-online.target postgresql@16-main.service redis-server.service
Wants=network-online.target postgresql@16-main.service redis-server.service
StartLimitIntervalSec=120
StartLimitBurst=5

[Service]
Type=simple
User=sub2api
Group=sub2api
WorkingDirectory=/home/pk/workspace/sub2api
ExecStart=/home/pk/workspace/sub2api/bin/sub2api
Environment=DATA_DIR=/home/pk/workspace/sub2api/data
Environment=SERVER_HOST=127.0.0.1
Environment=SERVER_PORT=8080
Environment=RUN_MODE=simple
Environment=TZ=Asia/Shanghai
Environment=GOMEMLIMIT=192MiB
Environment=LOG_OUTPUT_TO_FILE=false
Environment=LOG_OUTPUT_TO_STDOUT=true
Environment=DATABASE_MAX_OPEN_CONNS=10
Environment=DATABASE_MAX_IDLE_CONNS=2
Environment=REDIS_POOL_SIZE=16
Environment=REDIS_MIN_IDLE_CONNS=2
Restart=on-failure
RestartSec=5
TimeoutStopSec=60
UMask=0077
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=read-only
PrivateTmp=true
ReadWritePaths=/home/pk/workspace/sub2api/data
StandardOutput=journal
StandardError=journal
SyslogIdentifier=sub2api
LogRateLimitIntervalSec=30s
LogRateLimitBurst=300

[Install]
WantedBy=multi-user.target
EOF
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

cat > /etc/systemd/system/sub2api-backup.service <<'EOF'
[Unit]
Description=Consistent Sub2API database and configuration backup
After=postgresql@16-main.service
[Service]
Type=oneshot
ExecStart=/usr/local/lib/sub2api/backup.sh
UMask=0077
Nice=10
IOSchedulingClass=idle
EOF
cat > /etc/systemd/system/sub2api-backup.timer <<'EOF'
[Unit]
Description=Daily Sub2API backup at 03:30 Asia/Shanghai
[Timer]
OnCalendar=*-*-* 03:30:00 Asia/Shanghai
Persistent=true
AccuracySec=1min
[Install]
WantedBy=timers.target
EOF
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
