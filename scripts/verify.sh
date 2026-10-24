#!/usr/bin/env bash
set -Eeuo pipefail
for unit in sub2api postgresql@16-main redis-server; do
  systemctl is-active "$unit"
  systemctl is-enabled "$unit" || [[ "$unit" == postgresql@16-main ]]
done
curl --fail --silent http://127.0.0.1:8080/health
python3 - <<'PY'
import subprocess
lines=subprocess.check_output(['ss','-H','-ltn'],text=True).splitlines()
for port in ('8080','5432','6379'):
    matches=[x.split()[3] for x in lines if x.split()[3].endswith(':'+port)]
    assert matches, 'Port not listening: '+port
    assert all(x in ('127.0.0.1:'+port,'[::1]:'+port) for x in matches), 'Public listener: '+port
print('\nPASS: application/database/Redis listeners are loopback only.')
PY
runuser -u sub2api -- test -r /home/pk/workspace/sub2api/bin/sub2api
runuser -u sub2api -- test -w /home/pk/workspace/sub2api/data
runuser -u pk -- python3 /usr/local/lib/sub2api/api.py check
[[ $(runuser -u postgres -- psql -X -At -d sub2api -c "SELECT value FROM settings WHERE key='registration_enabled'") == false ]]
systemctl show sub2api -p NRestarts -p MemoryCurrent -p ActiveState
systemctl list-timers sub2api-backup.timer --no-pager
free -m
