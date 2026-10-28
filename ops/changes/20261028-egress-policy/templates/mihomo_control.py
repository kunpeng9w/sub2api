#!/usr/bin/python3
"""Candidate controller: stop retains the Claude guard and independent DNS."""
import argparse
import json
import subprocess


GUARD = "/usr/local/libexec/egress_guard.py"


def execute(args):
    result = subprocess.run(args, capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError(f"{args[0]} failed: {result.stderr.strip()}")
    return result.stdout


def control(command, run=execute):
    # Never invoke uninstall, unlock, nft flush, DNS stop, or Sub2API stop.
    status = json.loads(run([GUARD, "verify"]))
    if command == "start":
        run(["/usr/bin/systemctl", "start", "mihomo.service"])
    elif command in ("stop", "expire-trial"):
        run(["/usr/bin/systemctl", "stop", "mihomo.service"])
    elif command == "confirm":
        run(["/usr/bin/systemctl", "is-active", "--quiet", "mihomo.service"])
    status["mihomo_state"] = run(["/usr/bin/systemctl", "show", "mihomo.service", "-p", "ActiveState", "--value"]).strip()
    return status


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["start", "confirm", "stop", "status", "expire-trial"])
    args = parser.parse_args()
    print(json.dumps(control(args.command), sort_keys=True))


if __name__ == "__main__":
    main()
