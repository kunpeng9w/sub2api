"""Run the pinned deploy recipe in temporary fixtures; never call real services."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

HERE = Path(__file__).resolve().parent
APP = '/home/pk/workspace/sub2api'
RELEASE = '/home/pk/workspace/sub2api-egress-delivery/742271e18840fdde7fab5c120ee827b122da080c'
OLD_SHA = '89d3c99845988494d44ede994911b288263b7b7b10e302ca78bfbc0ee2943a32'
NEW_SHA = '8de67bdabaa7fb63ccd138cbd4aab5c1c3c362498d99dec6b34dd9d6cc4e443e'
ARCHIVE_SHA = 'ffcec58454c62a81361776a3665c85ecdb164205eedb2a8510fbb77dec623e06'

MOCK = r'''#!/usr/bin/python3
import json, os, pathlib, shutil, sys
root = pathlib.Path(os.environ['DEPLOY_FIXTURE'])
app = root/'app'
state_file = root/'state.json'
state = json.loads(state_file.read_text())
name = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
def save(): state_file.write_text(json.dumps(state))
if name == 'systemctl':
    if args[0] == 'is-active': sys.exit(0 if state['active'] else 3)
    if args[0] == 'show':
        field = args[args.index('-p')+1]
        print({'WorkingDirectory': str(app), 'ExecStart': '{ path='+str(app/'bin/sub2api')+' ; }', 'MainPID': '4321' if state['active'] else '0'}[field])
        sys.exit(0)
    assert args[1:] == ['sub2api'], args
    state['events'].append(args[0]); save()
    if args[0] == 'stop': state['active'] = False; save(); sys.exit(0)
    if args[0] == 'reset-failed': sys.exit(0)
    assert args[0] == 'start', args
    if os.environ.get('SCENARIO') in ('start-failure', 'rollback-delay') and (app/'bin/sub2api').read_bytes() == b'candidate':
        sys.exit(1)
    state['active'] = True; save()
    shutil.copyfile(app/'bin/sub2api', root/'proc/4321/exe')
elif name == 'curl':
    scenario = os.environ.get('SCENARIO')
    candidate = (app/'bin/sub2api').read_bytes() == b'candidate'
    if scenario == 'preflight-unavailable' or not state['active'] or (scenario == 'health-failure' and candidate):
        print("curl: (7) Couldn't connect to server", file=sys.stderr)
        sys.exit(7)
    delayed = (scenario == 'startup-delay' and candidate) or (scenario == 'rollback-delay' and 'start' in state['events'] and not candidate)
    if delayed:
        state['delayed_probes'] = state.get('delayed_probes', 0) + 1; save()
        if state['delayed_probes'] <= 2:
            print("curl: (7) Couldn't connect to server", file=sys.stderr)
            sys.exit(7)
    if candidate and scenario in ('invalid-json', 'empty-json', 'unhealthy-json', 'non-object-json'):
        print({'invalid-json': '<html>not ready</html>', 'empty-json': '', 'unhealthy-json': '{"status":"starting"}', 'non-object-json': '[]'}[scenario])
    else:
        print('{"status":"ok"}')
elif name == 'install':
    shutil.copyfile(args[-2], args[-1]); pathlib.Path(args[-1]).chmod(0o755)
elif name == 'guard':
    assert args == ['verify'], args
elif name == 'sleep': pass
else: raise AssertionError(name)
'''


class DeployRecipeTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.app = self.root/'app'
        self.release = self.root/'release'
        for name in ['app/bin', 'app/data', 'release', 'commands', 'proc/4321', 'backups']:
            (self.root/name).mkdir(parents=True, exist_ok=True)
        (self.app/'bin/sub2api').write_bytes(b'previous')
        (self.root/'proc/4321/exe').write_bytes(b'previous')
        (self.app/'data/config.yaml').write_text('gateway:\n  claude_egress:\n    enabled: true\n')
        (self.release/'sub2api').write_bytes(b'candidate')
        (self.release/'sub2api-linux-amd64.tar.gz').write_bytes(b'archive')
        (self.root/'state.json').write_text(json.dumps({'active': True, 'events': []}))
        for name in ['systemctl', 'curl', 'install', 'guard', 'sleep']:
            path = self.root/'commands'/name
            path.write_text(MOCK)
            path.chmod(0o755)
        source = (HERE/'deploy.sh').read_text()
        # Transform only the fixture copy. The production recipe remains pinned.
        source = source.replace(RELEASE, str(self.release)).replace(APP, str(self.app))
        source = source.replace('/root/sub2api-egress-audit-', str(self.root/'backups/sub2api-egress-audit-'))
        source = source.replace('/proc/', str(self.root/'proc')+'/')
        source = source.replace('/usr/local/libexec/egress_guard.py', str(self.root/'commands/guard'))
        source = source.replace('[[ $EUID -eq 0 ]]', 'true')
        for old, data in [(OLD_SHA, b'previous'), (NEW_SHA, b'candidate'), (ARCHIVE_SHA, b'archive')]:
            source = source.replace(old, hashlib.sha256(data).hexdigest())
        self.script = self.root/'deploy.sh'
        self.script.write_text(source)
        self.env = os.environ.copy()
        self.env.update(DEPLOY_FIXTURE=str(self.root), PATH=str(self.root/'commands')+':'+self.env['PATH'])
        self.config = (self.app/'data/config.yaml').read_bytes()

    def run_recipe(self, mode='--apply', scenario=''):
        env = dict(self.env, SCENARIO=scenario)
        return subprocess.run(['bash', str(self.script), mode], env=env, text=True, capture_output=True)

    def state(self):
        return json.loads((self.root/'state.json').read_text())

    def assert_untouched(self):
        self.assertEqual(self.state()['events'], [])
        self.assertEqual(list((self.root/'backups').iterdir()), [])

    def test_preflight_never_stops_or_installs(self):
        result = self.run_recipe('--check')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_untouched()
        self.assertEqual((self.app/'bin/sub2api').read_bytes(), b'previous')

    def test_success_and_manual_binary_only_rollback(self):
        result = self.run_recipe()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.app/'bin/sub2api').read_bytes(), b'candidate')
        self.assertEqual((self.root/'proc/4321/exe').read_bytes(), b'candidate')
        backup = next((self.root/'backups').iterdir())
        self.assertEqual((backup/'sub2api.previous').read_bytes(), b'previous')
        # A later config edit must not be lost during a binary-only rollback.
        (self.app/'data/config.yaml').write_bytes(self.config+b'# later edit\n')
        result = subprocess.run(['bash', str(backup/'rollback.sh')], env=self.env, text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.app/'bin/sub2api').read_bytes(), b'previous')
        self.assertEqual((self.app/'data/config.yaml').read_bytes(), self.config+b'# later edit\n')

    def test_failed_start_restores_previous_application(self):
        result = self.run_recipe(scenario='start-failure')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.app/'bin/sub2api').read_bytes(), b'previous')
        self.assertTrue(self.state()['active'])
        self.assertEqual((self.app/'data/config.yaml').read_bytes(), self.config)

    def test_failed_health_restores_previous_application(self):
        result = self.run_recipe(scenario='health-failure')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.app/'bin/sub2api').read_bytes(), b'previous')
        self.assertTrue(self.state()['active'])

    def test_startup_delay_is_quiet_and_eventually_succeeds(self):
        result = self.run_recipe(scenario='startup-delay')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('Deployment passed', result.stdout)
        self.assertEqual(result.stderr, '')
        self.assertEqual(self.state()['delayed_probes'], 3)
        self.assertEqual(self.state()['events'], ['stop', 'reset-failed', 'start'])

    def test_rollback_delay_is_quiet_and_eventually_recovers(self):
        result = self.run_recipe(scenario='rollback-delay')
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn('Traceback', result.stderr)
        self.assertNotIn('curl:', result.stderr)
        self.assertIn('Previous application restored and healthy', result.stdout)
        self.assertEqual((self.app/'bin/sub2api').read_bytes(), b'previous')
        self.assertEqual(self.state()['delayed_probes'], 3)

    def test_invalid_health_never_becomes_a_success(self):
        for scenario in ('invalid-json', 'empty-json', 'unhealthy-json', 'non-object-json'):
            with self.subTest(scenario=scenario):
                result = self.run_recipe(scenario=scenario)
                self.assertNotEqual(result.returncode, 0)
                self.assertNotIn('Traceback', result.stderr)
                self.assertNotIn('Deployment passed', result.stdout)
                self.assertIn('Application health did not become ready', result.stderr)
                self.assertEqual((self.app/'bin/sub2api').read_bytes(), b'previous')
                self.assertTrue(self.state()['active'])

    def test_unavailable_preflight_reports_failure_without_traceback(self):
        result = self.run_recipe('--check', scenario='preflight-unavailable')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Preflight failed: health endpoint is not ready', result.stderr)
        self.assertNotIn('Traceback', result.stderr)
        self.assertNotIn('curl:', result.stderr)
        self.assert_untouched()

    def test_corrupt_candidate_never_stops_service(self):
        (self.release/'sub2api').write_bytes(b'corrupt')
        self.assertNotEqual(self.run_recipe().returncode, 0)
        self.assert_untouched()

    def test_corrupt_archive_never_stops_service(self):
        (self.release/'sub2api-linux-amd64.tar.gz').write_bytes(b'corrupt')
        self.assertNotEqual(self.run_recipe().returncode, 0)
        self.assert_untouched()

    def test_already_running_candidate_is_idempotent(self):
        result = self.run_recipe()
        self.assertEqual(result.returncode, 0, result.stderr)
        events = self.state()['events']
        backups = list((self.root/'backups').iterdir())
        for mode in ('--check', '--apply'):
            result = self.run_recipe(mode)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn('already running', result.stdout)
            self.assertEqual(self.state()['events'], events)
            self.assertEqual(list((self.root/'backups').iterdir()), backups)

    def test_unknown_baseline_never_stops_service(self):
        (self.app/'bin/sub2api').write_bytes(b'unknown')
        (self.root/'proc/4321/exe').write_bytes(b'unknown')
        self.assertNotEqual(self.run_recipe().returncode, 0)
        self.assert_untouched()

    def test_disk_running_mismatch_never_stops_service(self):
        (self.root/'proc/4321/exe').write_bytes(b'other-running-version')
        self.assertNotEqual(self.run_recipe().returncode, 0)
        self.assert_untouched()

    def test_disabled_claude_default_never_stops_service(self):
        (self.app/'data/config.yaml').write_text('gateway:\n  claude_egress:\n    enabled: false\n')
        self.assertNotEqual(self.run_recipe().returncode, 0)
        self.assert_untouched()

    def test_corrupt_backup_cannot_replace_candidate(self):
        self.assertEqual(self.run_recipe().returncode, 0)
        backup = next((self.root/'backups').iterdir())
        (backup/'sub2api.previous').write_bytes(b'corrupt')
        before = self.state()['events']
        result = subprocess.run(['bash', str(backup/'rollback.sh')], env=self.env, text=True, capture_output=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.app/'bin/sub2api').read_bytes(), b'candidate')
        self.assertEqual(self.state()['events'], before)


if __name__ == '__main__':
    unittest.main()
