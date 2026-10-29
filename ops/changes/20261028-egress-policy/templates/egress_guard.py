#!/usr/bin/python3
"""Candidate v2 guard. Installed only by the operator during phase 2."""
import argparse
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import subprocess
import tempfile
import fcntl

STATE_DIR = Path("/var/lib/sub2api-egress")
TABLE = "sub2api_egress_guard"
PREFIX = "198.18.0.0/16"
TUN = "mihomo"
RULE_PRIORITY = 2000
ROUTE_TABLE = 2022


class GuardError(RuntimeError):
    pass


def run(args, *, input=None, optional=False):
    result = subprocess.run(args, input=input, capture_output=True, text=True)
    if result.returncode and not optional:
        raise GuardError(f"{args[0]} failed: {result.stderr.strip()}")
    return result.stdout if result.returncode == 0 else ""


def rules():
    return f'''table inet {TABLE} {{
    chain output {{
        type filter hook output priority -160; policy accept;
        ip daddr {PREFIX} oifname "{TUN}" counter accept
        ip daddr {PREFIX} counter reject with icmpx type admin-prohibited
    }}
}}
'''


def digest(document):
    def clean(value):
        if isinstance(value, dict):
            return {key: clean(item) for key, item in value.items() if key not in ("handle", "packets", "bytes")}
        if isinstance(value, list):
            return [clean(item) for item in value]
        return value
    semantic = [clean(entry) for entry in document["nftables"] if "metainfo" not in entry]
    return hashlib.sha256(json.dumps(semantic, sort_keys=True).encode()).hexdigest()


class Guard:
    def __init__(self, directory=STATE_DIR, execute=run):
        self.directory = Path(directory)
        self.path = self.directory / "state.json"
        self.execute = execute

    def read(self):
        if self.directory.is_symlink() or self.path.is_symlink():
            raise GuardError("guard state must not be a symlink")
        try:
            value = json.loads(self.path.read_text())
        except (OSError, ValueError) as exc:
            raise GuardError("guard state missing or damaged; refusing to assume unlocked") from exc
        if value.get("version") != 2 or value.get("mode") != "claude-only" or value.get("phase") not in ("trial", "confirmed") or value.get("prefix") != PREFIX or value.get("tun") != TUN:
            raise GuardError("guard state is not the approved Claude-only policy")
        return value

    def write(self, value):
        self.directory.mkdir(mode=0o700, parents=True, exist_ok=True)
        fd, temporary = tempfile.mkstemp(dir=self.directory, prefix=".state-")
        try:
            with os.fdopen(fd, "w") as output:
                json.dump(value, output, sort_keys=True)
                output.flush()
                os.fsync(output.fileno())
            os.replace(temporary, self.path)
            dfd = os.open(self.directory, os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(dfd)
            finally:
                os.close(dfd)
        finally:
            if os.path.exists(temporary):
                os.unlink(temporary)

    def policy_rule_present(self):
        document = json.loads(self.execute(["/usr/sbin/ip", "-j", "rule", "show"]))
        matching = [rule for rule in document if rule.get("priority") == RULE_PRIORITY]
        if not matching:
            return False
        if len(matching) != 1:
            raise GuardError("reserved policy-rule priority is already in use")
        rule = matching[0]
        destination = str(rule.get("dst", ""))
        # iproute2 versions emit either a CIDR string or separate dst/dstlen
        # fields. Compare networks so both represent the same owned rule.
        if "/" not in destination and "dstlen" in rule:
            destination += "/" + str(rule["dstlen"])
        try:
            correct_destination = ipaddress.ip_network(destination) == ipaddress.ip_network(PREFIX)
        except ValueError:
            correct_destination = False
        if not correct_destination or str(rule.get("table")) != str(ROUTE_TABLE):
            raise GuardError("reserved policy-rule priority is already in use")
        return True

    def arm(self, phase="confirmed", migrate=False):
        if migrate:
            # Migration requires a locked v1 state, not an implicit unlock.
            old = json.loads(self.path.read_text())
            if old.get("version") != 1 or old.get("phase") != "confirmed":
                raise GuardError("migration requires the backed-up, confirmed v1 state")
            value = {"version": 2, "mode": "claude-only", "prefix": PREFIX, "tun": TUN}
        else:
            value = self.read()
        value["phase"] = phase
        value.pop("fingerprint", None)
        # Persist locked intent before either kernel operation. A failure never
        # deletes the guard or converts the state to off.
        self.write(value)
        self.execute(["/usr/sbin/ip", "route", "replace", "blackhole", PREFIX, "metric", "42760"])
        # Precede Mihomo's main-table lookup, otherwise its persistent blackhole
        # could shadow the active TUN route. Missing TUN table falls to blackhole.
        if not self.policy_rule_present():
            self.execute(["/usr/sbin/ip", "rule", "add", "pref", str(RULE_PRIORITY), "to", PREFIX, "lookup", str(ROUTE_TABLE)])
        present = self.execute(["/usr/sbin/nft", "-j", "list", "table", "inet", TABLE], optional=True)
        prefix = f"delete table inet {TABLE}\n" if present else ""
        self.execute(["/usr/sbin/nft", "-f", "-"], input=prefix + rules())
        current = json.loads(self.execute(["/usr/sbin/nft", "-j", "list", "table", "inet", TABLE]))
        value["fingerprint"] = digest(current)
        self.write(value)
        return self.verify()

    def verify(self):
        value = self.read()
        actual = self.execute(["/usr/sbin/nft", "-j", "list", "table", "inet", TABLE])
        if not value.get("fingerprint") or digest(json.loads(actual)) != value["fingerprint"]:
            raise GuardError("actual guard differs from the recorded policy")
        routes = self.execute(["/usr/sbin/ip", "route", "show", "table", "main", "exact", PREFIX])
        if not any(line.startswith("blackhole " + PREFIX) and "metric 42760" in line for line in routes.splitlines()):
            raise GuardError("persistent reject route is missing")
        if not self.policy_rule_present():
            raise GuardError("targeted policy rule is missing")
        return {"phase": value["phase"], "mode": value["mode"], "consistent": True, "prefix": PREFIX}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["render", "migrate", "restore", "verify", "status"])
    args = parser.parse_args()
    if args.command == "render":
        print(rules(), end="")
        return
    if os.geteuid() != 0:
        raise SystemExit("root is required for installed guard state")
    if STATE_DIR.is_symlink():
        raise GuardError("guard directory must not be a symlink")
    STATE_DIR.mkdir(mode=0o700, parents=True, exist_ok=True)
    for path in [STATE_DIR, STATE_DIR / "state.json"]:
        if path.exists() and (path.stat().st_uid != 0 or path.stat().st_mode & 0o077):
            raise GuardError("guard state must be root-owned and private")
    with open(STATE_DIR / "control.lock", "a") as lock:
        os.chmod(lock.name, 0o600)
        fcntl.flock(lock, fcntl.LOCK_EX)
        guard = Guard()
        if args.command == "migrate":
            result = guard.arm(migrate=True)
        elif args.command == "restore":
            result = guard.arm(guard.read()["phase"])
        else:
            result = guard.verify()
        print(json.dumps(result, sort_keys=True))


if __name__ == "__main__":
    main()
