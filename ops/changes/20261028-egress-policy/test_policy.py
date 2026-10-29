"""Pure simulations only. No real nft/ip/systemctl, namespace or external network."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

HERE = Path(__file__).resolve().parent


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


guard_module = load("candidate_guard", HERE / "templates/egress_guard.py")
controller = load("candidate_controller", HERE / "templates/mihomo_control.py")
generator = load("candidate_generator", HERE / "generate_templates.py")
private_config = load("candidate_private_config", HERE / "prepare_private_config.py")


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

    def test_generated_templates_match_one_domain_source(self):
        for name, content in generator.render().items():
            self.assertEqual((HERE / "templates" / name).read_text(), content, name)

    def test_manual_bash_blocks_are_syntax_only_validated(self):
        import re
        import subprocess
        for name in ["DEPLOY.md", "ROLLBACK.md", "PHASE2-MANUAL.md"]:
            for index, block in enumerate(re.findall(r"```bash\n(.*?)```", (HERE / name).read_text(), re.S)):
                # -n only parses: no install, systemd, routing or network call runs.
                result = subprocess.run(["bash", "-n"], input=block, capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, f"{name} block {index}: {result.stderr}")

    def test_only_fake_prefix_enters_tun_and_no_dns_dependency(self):
        import yaml
        c = yaml.safe_load(generator.render()["mihomo.yaml.example"])
        self.assertEqual(c["tun"]["route-address"], ["198.18.0.0/16"])
        self.assertFalse(c["tun"]["auto-redirect"])
        self.assertFalse(c["tun"]["strict-route"])
        self.assertEqual(c["tun"]["dns-mode"], "disabled")
        self.assertEqual(c["tun"]["dns-hijack"], [])
        self.assertEqual(c["rules"][-1], "MATCH,REJECT")
        self.assertNotIn("proxy-groups", c)
        self.assertTrue(c["profile"]["store-fake-ip"])
        self.assertNotIn("mihomo.service", (HERE / "templates/dnsmasq-claude.service").read_text())
        self.assertNotIn("uid", guard_module.rules())

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

    def test_private_preparation_preserves_application_settings_without_installing(self):
        import yaml
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory); output = base / "private"
            source = base / "old-mihomo.yaml"
            source.write_text(yaml.safe_dump({"proxies": [{"name": "old", "type": "http", "server": "proxy.invalid", "port": 8022, "password": "simulated-only"}]}))
            app = base / "old-app.yaml"; app.write_text(yaml.safe_dump({"database": {"dbname": "simulated"}, "gateway": {"max_body_size": 12}}))
            private_config.prepare(source, app, output)
            self.assertFalse(yaml.safe_load((output / "application-phase1.yaml").read_text())["gateway"]["claude_egress"]["enabled"])
            second = yaml.safe_load((output / "application-phase2.yaml").read_text())
            self.assertEqual(second["gateway"]["max_body_size"], 12)
            self.assertEqual(second["database"]["dbname"], "simulated")
            self.assertEqual((output / "mihomo.yaml").stat().st_mode & 0o777, 0o600)
            self.assertIn("old", source.read_text())


if __name__ == "__main__":
    unittest.main()
