#!/usr/bin/env python3
"""Render reviewable repository templates only; never reads installed configuration."""
import argparse
import json
from pathlib import Path

import yaml

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
DOMAINS = ROOT / "backend/internal/pkg/claudedomain/domains.txt"


def render():
    domains = DOMAINS.read_text().split()
    if not domains or len(domains) != len(set(domains)):
        raise ValueError("official domain list must be nonempty and unique")
    config = {
        "mixed-port": 7890, "allow-lan": False, "bind-address": "127.0.0.1",
        "mode": "rule", "log-level": "info", "ipv6": False,
        "interface-name": "eth0", "external-controller": "",
        "profile": {"store-selected": False, "store-fake-ip": True},
        "tun": {"enable": True, "device": "mihomo", "stack": "mixed",
                "auto-route": True, "auto-redirect": False, "auto-detect-interface": True,
                "strict-route": True, "mtu": 1500, "dns-hijack": [],
                "iproute2-table-index": 2022, "iproute2-rule-index": 9000,
                "route-address": ["198.18.0.0/16"]},
        "dns": {"enable": True, "listen": "127.0.0.1:1053", "ipv6": False,
                "enhanced-mode": "fake-ip", "fake-ip-range": "198.18.0.1/16",
                "use-hosts": False, "use-system-hosts": False,
                "fake-ip-filter-mode": "rule",
                "fake-ip-filter": [f"DOMAIN-SUFFIX,{d},fake-ip" for d in domains] + ["MATCH,real-ip"],
                "default-nameserver": ["100.100.2.136", "100.100.2.138"],
                "proxy-server-nameserver": ["100.100.2.136#eth0", "100.100.2.138#eth0"],
                "nameserver": ["https://1.1.1.1/dns-query#US-Proxy", "https://8.8.8.8/dns-query#US-Proxy"]},
        "proxies": [{"name": "US-Proxy", "type": "http", "server": "${PROXY_SERVER}",
                     "port": 8022, "username": "${PROXY_USERNAME}", "password": "${PROXY_PASSWORD}"}],
        "rules": [f"DOMAIN-SUFFIX,{d},US-Proxy" for d in domains] + ["MATCH,REJECT"],
    }
    dnsmasq = "\n".join([
        "# Candidate: manual deployment only. Independent of Mihomo lifecycle.",
        "port=53", "listen-address=127.0.0.1", "bind-interfaces", "no-resolv", "no-poll",
        "no-hosts", "cache-size=0", "server=100.100.2.136", "server=100.100.2.138",
        *[f"server=/{d}/127.0.0.1#1053" for d in domains], "",
    ])
    return {
        "mihomo.yaml.example": yaml.safe_dump(config, allow_unicode=True, sort_keys=False),
        "dnsmasq-claude.conf": dnsmasq,
        "official-domains.json": json.dumps(domains, indent=2) + "\n",
        "resolv.conf": "# Managed Claude domain policy; original resolver remains available for rollback.\nnameserver 127.0.0.1\noptions timeout:2 attempts:2\n",
        "application-phase1.yaml": "# Merge this section into a private copy of the existing config.\ngateway:\n  claude_egress:\n    enabled: false\n    proxy_url: http://127.0.0.1:7890\n",
        "application-phase2.yaml": "# Merge this section into a private copy of the existing config.\ngateway:\n  claude_egress:\n    enabled: true\n    proxy_url: http://127.0.0.1:7890\n",
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="compare only; writes nothing")
    args = parser.parse_args()
    for name, content in render().items():
        path = HERE / "templates" / name
        if args.check:
            if not path.exists() or path.read_text() != content:
                raise SystemExit(f"stale candidate template: {name}")
        else:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content)


if __name__ == "__main__":
    main()
