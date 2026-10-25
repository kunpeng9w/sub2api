#!/bin/bash
# Install only when explicitly invoked; default does not change the machine.
set -Eeuo pipefail
[[ $EUID == 0 ]] || { echo 'Run with sudo.' >&2; exit 1; }
case "${1:-}" in
  --adopt-confirmed|--unlocked) mode=$1 ;;
  *) echo 'Usage: sudo bash install.sh {--adopt-confirmed|--unlocked}' >&2; exit 2 ;;
esac
source_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
exec 9>/run/lock/mihomo-ctl.lock
flock -x 9

if systemctl is-active --quiet mihomo-rollback.timer; then
  echo 'Legacy 180-second trial is pending; resolve it before installation.' >&2
  exit 1
fi
if [[ $mode == --adopt-confirmed ]]; then
  systemctl is-active --quiet mihomo.service
  systemctl is-enabled --quiet mihomo.service
  /usr/local/libexec/mihomo-check
  /usr/local/bin/mihomo -t -d /var/lib/mihomo -f /etc/mihomo/config.yaml
else
  if systemctl is-active --quiet mihomo.service; then
    echo 'Unlocked installation requires Mihomo to be stopped first.' >&2
    exit 1
  fi
fi

backup_dir=$(mktemp -d /var/backups/sub2api-egress.XXXXXXXX)
chmod 700 "$backup_dir"
for target in \
  /usr/local/sbin/mihomo-ctl \
  /usr/local/libexec/egress_guard.py \
  /usr/local/libexec/mihomo_control.py \
  /etc/systemd/system/sub2api-egress.service \
  /etc/systemd/system/mihomo-trial-rollback.service \
  /etc/systemd/system/mihomo-trial-rollback.timer \
  /etc/systemd/system/sub2api.service.d/egress-guard.conf \
  /etc/systemd/system/mihomo.service.d/egress-guard.conf \
  /var/lib/sub2api-egress/state.json; do
  if [[ -e $target ]]; then
    cp --parents -a -- "$target" "$backup_dir/"
  fi
done
protection_requested=0
on_error() {
  if [[ $protection_requested == 1 ]]; then
    systemctl stop sub2api.service || true
  fi
  echo "Installation failed. No automatic unlock. Backup: $backup_dir" >&2
}
trap on_error ERR
install -o root -g root -m 0755 "$source_dir/egress_guard.py" /usr/local/libexec/egress_guard.py
install -o root -g root -m 0755 "$source_dir/mihomo_control.py" /usr/local/libexec/mihomo_control.py
install -o root -g root -m 0755 "$source_dir/mihomo-ctl" /usr/local/sbin/mihomo-ctl
install -d -o root -g root -m 0700 /var/lib/sub2api-egress
if [[ ! -e /var/lib/sub2api-egress/state.json ]]; then
  /usr/local/libexec/egress_guard.py initialize
fi

# Make boot recovery durable before persisting any new locked intent. If arm
# subsequently fails, the next boot must restore protection or refuse startup.
for unit in sub2api-egress.service mihomo-trial-rollback.service mihomo-trial-rollback.timer; do
  install -o root -g root -m 0644 "$source_dir/$unit" "/etc/systemd/system/$unit"
done
for unit in sub2api mihomo; do
  install -d -o root -g root -m 0755 "/etc/systemd/system/$unit.service.d"
  install -o root -g root -m 0644 "$source_dir/guard-dependency.conf" "/etc/systemd/system/$unit.service.d/egress-guard.conf"
done
sync -f /etc/systemd/system
systemctl daemon-reload

# Adopt the already confirmed proxy without restarting either live service.
if [[ $mode == --adopt-confirmed ]]; then
  protection_requested=1
  /usr/bin/python3 - <<'PY'
import sys
sys.path.insert(0, '/usr/local/libexec')
from egress_guard import Guard, exclusive
with exclusive():
    Guard().arm('confirmed')
PY
else
  /usr/bin/python3 - <<'PY'
import sys
sys.path.insert(0, '/usr/local/libexec')
from egress_guard import Guard, GuardError, exclusive
with exclusive():
    g = Guard()
    if g.verify()['phase'] != 'off':
        raise GuardError('Existing lock must be explicitly released with mihomo-ctl stop')
PY
fi

systemctl enable --now sub2api-egress.service
flock -u 9
/usr/local/sbin/mihomo-ctl status
echo "Installed. Backup: $backup_dir"
