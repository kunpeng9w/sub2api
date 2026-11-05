#!/usr/bin/env bash
# CI only: isolate the test process group, never the Actions runner's own sockets.
# Dependencies are downloaded before this wrapper; fixtures use loopback/Docker.
set -euo pipefail
if [[ "${GITHUB_ACTIONS:-}" != true ]]; then
  echo 'This verification runs only on GitHub Actions.' >&2
  exit 1
fi
chain="MONITOR_TEST_$$"
test_uid=$(id -u)
test_group="sub2api-ci-$$"
sudo groupadd "$test_group"
test_gid=$(getent group "$test_group" | cut -d: -f3)
cleanup() {
  # Stop any orphaned test children before lifting their network restriction.
  sudo pkill -G "$test_gid" 2>/dev/null || true
  sudo iptables -D OUTPUT -m owner --gid-owner "$test_gid" -j "$chain" 2>/dev/null || true
  sudo iptables -F "$chain" 2>/dev/null || true
  sudo iptables -X "$chain" 2>/dev/null || true
  sudo ip6tables -D OUTPUT -m owner --gid-owner "$test_gid" -j "$chain" 2>/dev/null || true
  sudo ip6tables -F "$chain" 2>/dev/null || true
  sudo ip6tables -X "$chain" 2>/dev/null || true
  sudo groupdel "$test_group" 2>/dev/null || true
}
trap cleanup EXIT
sudo iptables -N "$chain"
sudo iptables -A "$chain" -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN
sudo iptables -A "$chain" -d 127.0.0.0/8 -j RETURN
sudo iptables -A "$chain" -d 172.16.0.0/12 -j RETURN
sudo iptables -A "$chain" -j REJECT
sudo iptables -I OUTPUT 1 -m owner --gid-owner "$test_gid" -j "$chain"
sudo ip6tables -N "$chain"
sudo ip6tables -A "$chain" -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN
sudo ip6tables -A "$chain" -d ::1/128 -j RETURN
sudo ip6tables -A "$chain" -j REJECT
sudo ip6tables -I OUTPUT 1 -m owner --gid-owner "$test_gid" -j "$chain"
# Preserve the runner UID, workspace/cache ownership and Docker supplementary group.
# Only the effective group changes, so CI cancellation and artifact upload keep working.
sudo --preserve-env -u "#$test_uid" -g "$test_group" -- env PATH="$PATH" "$@"
