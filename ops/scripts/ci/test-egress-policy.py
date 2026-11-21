"""Pure simulations only. No real nft/ip/systemctl, namespace or external network."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
import yaml

OPS = Path(__file__).resolve().parents[2]
GUARD_DIR = OPS / "scripts/egress-guard"


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


guard_module = load("egress_guard", GUARD_DIR / "egress_guard.py")
controller = load("mihomo_control", GUARD_DIR / "mihomo_control.py")
dns = load("mihomo_dns", OPS / "scripts/mihomo-dns/mihomo_dns.py")


class FakeKernel:
    def __init__(self):
        self.events = []
        self.route = False
        self.policy_rule = False
        self.fail = None
        self.rule_conflict = False
        self.table = {"nftables": [{"table": {"family": "inet", "name": guard_module.TABLE}}, {"rule": {"expr": [{"reject": None}]}}]}

    def __call__(self, args, *, input=None, optional=False):
        self.events.append((args, input))
        if args[:3] == ["/usr/sbin/ip", "route", "replace"]:
            if self.fail == "route": raise guard_module.GuardError("simulated route failure")
            self.route = True
            return ""
        if args[:3] == ["/usr/sbin/ip", "-j", "rule"]:
            if self.rule_conflict: return json.dumps([{"priority": 2000, "dst": "203.0.113.0/24", "table": 254}])
            return json.dumps([{"priority": 2000, "dst": "198.18.0.0", "dstlen": 16, "table": 2022}] if self.policy_rule else [])
        if args[:3] == ["/usr/sbin/ip", "rule", "add"]:
            if self.fail == "policy-rule": raise guard_module.GuardError("simulated policy-rule failure")
            self.policy_rule = True
            return ""
        if args[:3] == ["/usr/sbin/nft", "-f", "-"]:
            if self.fail == "nft": raise guard_module.GuardError("simulated nft failure")
            if "flush ruleset" in input: raise AssertionError("global firewall mutation forbidden")
            return ""
        if args[0] == "/usr/sbin/nft": return json.dumps(self.table)
        if args[0] == "/usr/sbin/ip": return "blackhole 198.18.0.0/16 metric 42760\n" if self.route else ""
        raise AssertionError("unexpected real-system command: " + repr(args))


class PolicySimulationTests(unittest.TestCase):
    def new_guard(self, directory, fake):
        guard = guard_module.Guard(directory, fake)
        Path(directory, "state.json").write_text(json.dumps({"version": 1, "phase": "confirmed"}))
        return guard

    def test_deployment_routes_only_official_domains_via_mihomo(self):
        domains = (OPS.parent / "backend/internal/pkg/claudedomain/domains.txt").read_text().split()
        config = yaml.safe_load((OPS / "config/mihomo/config.yaml.example").read_text())
        self.assertEqual(set(dns.DOMAINS), set(domains))
        self.assertEqual(config["rules"], [f"DOMAIN-SUFFIX,{d},US-Proxy" for d in domains] + ["MATCH,REJECT"])
        self.assertEqual(config["dns"]["fake-ip-filter"],
                         [f"DOMAIN-SUFFIX,{d},fake-ip" for d in domains] + ["MATCH,real-ip"])
        self.assertEqual(config["tun"]["route-address"], [guard_module.PREFIX])
        self.assertFalse(config["tun"]["auto-redirect"])
        self.assertFalse(config["tun"]["strict-route"])
        self.assertEqual(config["tun"]["dns-mode"], "disabled")
        self.assertEqual(config["tun"]["dns-hijack"], [])
        self.assertNotIn("proxy-groups", config)
        self.assertTrue(config["profile"]["store-fake-ip"])
        app = yaml.safe_load((OPS / "config/application.yaml.example").read_text())
        self.assertTrue(app["gateway"]["claude_egress"]["enabled"])
        self.assertEqual(app["gateway"]["claude_egress"]["proxy_url"],
                         f"http://{config['bind-address']}:{config['mixed-port']}")
        self.assertNotIn("uid", guard_module.rules())

    def test_deployed_units_preserve_ssh_and_independent_dns(self):
        units = OPS / "config/systemd"
        lines = (units / "mihomo.service").read_text().splitlines()
        start = next(i for i, line in enumerate(lines) if line.startswith("ExecStart="))
        self.assertLess(lines.index("ExecStartPre=/usr/local/libexec/mihomo-ssh-route add"), start)
        self.assertIn("ExecStopPost=/usr/local/libexec/mihomo-ssh-route del", lines)
        self.assertIn("ExecStartPre=/usr/local/libexec/egress_guard.py verify", lines)
        self.assertIn("ExecStart=/usr/local/libexec/egress_guard.py restore",
                      (units / "sub2api-egress.service").read_text().splitlines())
        self.assertNotIn("mihomo.service", (units / "dnsmasq-claude.service").read_text())

    def test_migration_keeps_locked_intent_and_installs_only_owned_table(self):
        with tempfile.TemporaryDirectory() as directory:
            fake = FakeKernel(); guard = self.new_guard(directory, fake)
            self.assertTrue(guard.arm(migrate=True)["consistent"])
            commands = [event[0] for event in fake.events]
            self.assertEqual(commands[0][:3], ["/usr/sbin/ip", "route", "replace"])
            self.assertIn("ip daddr 198.18.0.0/16", guard_module.rules())
            self.assertNotIn("ct state established", guard_module.rules())
            self.assertEqual(guard.read()["phase"], "confirmed")

    def test_route_and_rule_failures_never_unlock_or_stop_other_services(self):
        for failure in ["route", "policy-rule", "nft"]:
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as directory:
                fake = FakeKernel(); fake.fail = failure
                guard = self.new_guard(directory, fake)
                with self.assertRaises(guard_module.GuardError): guard.arm(migrate=True)
                self.assertEqual(guard.read()["phase"], "confirmed")
                self.assertNotIn("off", json.dumps(guard.read()))
                self.assertFalse(any("systemctl" in args[0] for args, _ in fake.events))

    def test_restore_after_simulated_reboot_recreates_guard(self):
        with tempfile.TemporaryDirectory() as directory:
            fake = FakeKernel(); guard = self.new_guard(directory, fake)
            guard.arm(migrate=True); fake.route = False; fake.policy_rule = False
            with self.assertRaises(guard_module.GuardError): guard.verify()
            self.assertTrue(guard.arm(guard.read()["phase"])["consistent"])

    def test_policy_rule_collision_preserves_lock_and_never_replaces_foreign_rule(self):
        with tempfile.TemporaryDirectory() as directory:
            fake = FakeKernel(); fake.rule_conflict = True
            guard = self.new_guard(directory, fake)
            with self.assertRaises(guard_module.GuardError): guard.arm(migrate=True)
            self.assertEqual(guard.read()["phase"], "confirmed")
            self.assertFalse(any(args[:3] == ["/usr/sbin/ip", "rule", "add"] for args, _ in fake.events))
            self.assertFalse(any(args[0] == "/usr/sbin/nft" for args, _ in fake.events))

    def test_policy_rule_supports_both_iproute2_json_formats(self):
        for rule in [
            {"priority": 2000, "dst": "198.18.0.0/16", "table": 2022},
            {"priority": 2000, "dst": "198.18.0.0", "dstlen": 16, "table": "2022"},
        ]:
            with self.subTest(rule=rule):
                def execute(args):
                    self.assertEqual(args, ["/usr/sbin/ip", "-j", "rule", "show"])
                    return json.dumps([rule])
                self.assertTrue(guard_module.Guard(execute=execute).policy_rule_present())

    def test_policy_rule_rejects_incorrect_or_ambiguous_destinations(self):
        correct = {"priority": 2000, "dst": "198.18.0.0", "dstlen": 16, "table": 2022}
        for rules in [
            [{**correct, "dstlen": 24}],
            [{**correct, "dstlen": "invalid"}],
            [{**correct, "dst": "203.0.113.0"}],
            [{**correct, "table": 254}],
            [{"priority": 2000, "dst": "198.18.0.0", "table": 2022}],
            [correct, correct],
        ]:
            with self.subTest(rules=rules):
                with self.assertRaises(guard_module.GuardError):
                    guard_module.Guard(execute=lambda args: json.dumps(rules)).policy_rule_present()

    def test_fingerprint_change_is_detected(self):
        with tempfile.TemporaryDirectory() as directory:
            fake = FakeKernel(); guard = self.new_guard(directory, fake); guard.arm(migrate=True)
            fake.table["nftables"][-1]["rule"]["expr"] = [{"accept": None}]
            with self.assertRaises(guard_module.GuardError): guard.verify()

    def test_stop_and_timeout_only_stop_mihomo(self):
        for command in ["stop", "expire-trial"]:
            events = []
            def run(args):
                events.append(args)
                return '{"phase":"confirmed","consistent":true}' if args[0] == controller.GUARD else "inactive\n"
            controller.control(command, run)
            self.assertIn(["/usr/bin/systemctl", "stop", "mihomo.service"], events)
            self.assertFalse(any("dnsmasq-claude.service" in args or "sub2api.service" in args for args in events))
            self.assertFalse(any("unlock" in args for args in events))

    def test_failed_guard_verification_never_starts_mihomo(self):
        events = []
        def run(args): events.append(args); raise RuntimeError("simulated broken guard")
        with self.assertRaises(RuntimeError): controller.control("start", run)
        self.assertEqual(len(events), 1)


if __name__ == "__main__":
    unittest.main()
