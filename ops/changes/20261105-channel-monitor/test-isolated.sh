#!/usr/bin/env bash
# CI only: allow loopback/Docker fixtures and reject every new public connection
# from the test user. Dependency downloads happen before this wrapper.
set -euo pipefail
if [[ "${GITHUB_ACTIONS:-}" != true ]]; then
  echo 'This verification runs only on GitHub Actions.' >&2
  exit 1
fi
chain="MONITOR_TEST_$$"
uid=$(id -u)
cleanup() {
  sudo iptables -D OUTPUT -m owner --uid-owner "$uid" -j "$chain" 2>/dev/null || true
  sudo iptables -F "$chain" 2>/dev/null || true
  sudo iptables -X "$chain" 2>/dev/null || true
  sudo ip6tables -D OUTPUT -m owner --uid-owner "$uid" -j "$chain" 2>/dev/null || true
  sudo ip6tables -F "$chain" 2>/dev/null || true
  sudo ip6tables -X "$chain" 2>/dev/null || true
}
trap cleanup EXIT
sudo iptables -N "$chain"
sudo iptables -A "$chain" -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN
sudo iptables -A "$chain" -d 127.0.0.0/8 -j RETURN
sudo iptables -A "$chain" -d 172.16.0.0/12 -j RETURN
sudo iptables -A "$chain" -j REJECT
sudo iptables -I OUTPUT 1 -m owner --uid-owner "$uid" -j "$chain"
sudo ip6tables -N "$chain"
sudo ip6tables -A "$chain" -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN
sudo ip6tables -A "$chain" -d ::1/128 -j RETURN
sudo ip6tables -A "$chain" -j REJECT
sudo ip6tables -I OUTPUT 1 -m owner --uid-owner "$uid" -j "$chain"
"$@"
