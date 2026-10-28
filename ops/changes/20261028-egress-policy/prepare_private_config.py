#!/usr/bin/env python3
"""Operator-only preparation. Writes private candidates; does not install or restart."""
import argparse
import copy
import json
import os
from pathlib import Path

import yaml

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]


def private_write(path, value):
    data = yaml.safe_dump(value, allow_unicode=True, sort_keys=False)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "w") as output:
        output.write(data)
        output.flush()
        os.fsync(output.fileno())


def prepare(mihomo_source, app_source, output_dir):
    output_dir = Path(output_dir).resolve()
    if output_dir.is_relative_to(ROOT.resolve()):
        raise ValueError("private candidates must be outside the repository")
    output_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    if output_dir.stat().st_mode & 0o077 or output_dir.stat().st_uid != os.geteuid():
        raise ValueError("candidate directory must be private and owned by the operator")
    old = yaml.safe_load(Path(mihomo_source).read_text())
    nodes = old.get("proxies", [])
    if len(nodes) != 1 or nodes[0].get("type") != "http":
        raise ValueError("expected the existing single HTTP proxy; inspect manually before proceeding")
    domains = json.loads((HERE / "templates/official-domains.json").read_text())
    host = str(nodes[0].get("server", "")).lower().rstrip(".")
    if not host or any(host == domain or host.endswith("." + domain) for domain in domains):
        raise ValueError("proxy endpoint must not recurse through the official-domain policy")
    candidate = yaml.safe_load((HERE / "templates/mihomo.yaml.example").read_text())
    node = copy.deepcopy(nodes[0])
    node["name"] = "US-Proxy"
    candidate["proxies"] = [node]
    private_write(output_dir / "mihomo.yaml", candidate)
    app = yaml.safe_load(Path(app_source).read_text())
    for phase, enabled in [(1, False), (2, True)]:
        value = copy.deepcopy(app)
        value.setdefault("gateway", {})["claude_egress"] = {"enabled": enabled, "proxy_url": "http://127.0.0.1:7890"}
        private_write(output_dir / f"application-phase{phase}.yaml", value)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mihomo-source", required=True)
    parser.add_argument("--app-source", required=True)
    parser.add_argument("--output-dir", required=True)
    args = parser.parse_args()
    if os.geteuid() != 0:
        raise SystemExit("run manually as root to keep production credentials private")
    prepare(args.mihomo_source, args.app_source, args.output_dir)
    print("Private candidates prepared; no files installed and no services changed.")


if __name__ == "__main__":
    main()
