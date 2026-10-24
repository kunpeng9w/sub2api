#!/usr/bin/python3
"""Run the installer against temporary paths and fake guard/systemctl commands.

Only the test copy has its absolute deployment paths and root check rewritten.
No installed files, services, or firewall rules are accessed by this harness.
"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


SOURCE = Path(__file__).resolve().parent
COMMAND = '''#!/usr/bin/python3
import json, os, subprocess, sys
from pathlib import Path
name = Path(sys.argv[0]).name
args = sys.argv[1:]
with open(os.environ['TEST_EVENTS'], 'a') as f:
    f.write(json.dumps([name, *args]) + '\\n')
failure = os.environ.get('TEST_FAIL')
if name == 'systemctl':
    if args[0] == 'is-active':
        sys.exit(0 if args[-1] == 'mihomo.service' else 3)
    sys.exit(1 if failure == args[0] else 0)
if name == 'sync':
    sys.exit(1 if failure == 'sync' else 0)
if name == 'install':
    if failure == 'dependency' and args[-1].endswith('.service.d/egress-guard.conf'):
        sys.exit(1)
    # Preserve the real install behavior except for root ownership requirements.
    args = [a for a in args if a not in ('-o', '-g', 'root')]
    sys.exit(subprocess.run(['/usr/bin/install', *args]).returncode)
raise AssertionError(name)
'''
GUARD = '''#!/usr/bin/python3
import contextlib, json, os, sys
from pathlib import Path
state_path = Path(os.environ['TEST_STATE'])
exclusive = contextlib.nullcontext
class GuardError(RuntimeError):
    pass
class Guard:
    def arm(self, phase):
        with open(os.environ['TEST_EVENTS'], 'a') as f:
            f.write(json.dumps(['arm', phase]) + '\\n')
        state_path.write_text(json.dumps({'phase': phase}))
        if os.environ.get('TEST_FAIL') == 'arm':
            raise GuardError('injected nft failure after persisting intent')
    def verify(self):
        return json.loads(state_path.read_text())
if __name__ == '__main__':
    assert sys.argv[1:] == ['initialize']
    state_path.write_text(json.dumps({'phase': 'off'}))
'''


class InstallTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix='egress-install-test-')
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.source = self.root / 'candidate'
        self.source.mkdir()
        self.events = self.root / 'events.jsonl'
        self.events.touch()
        paths = ('/usr/local/libexec', '/usr/local/sbin', '/usr/local/bin',
                 '/var/lib/sub2api-egress', '/var/backups', '/run/lock',
                 '/etc/systemd/system')
        self.mapping = {p: self.root / 'deployment' / p.lstrip('/') for p in paths}
        for path in self.mapping.values():
            path.mkdir(parents=True)
        self.units = self.mapping['/etc/systemd/system']
        self.state = self.mapping['/var/lib/sub2api-egress'] / 'state.json'
        script = (SOURCE / 'install.sh').read_text()
        root_check = "[[ $EUID == 0 ]] || { echo 'Run with sudo.' >&2; exit 1; }"
        self.assertIn(root_check, script)
        script = script.replace(root_check, ': # Root ownership is simulated by the test harness.')
        for original, replacement in self.mapping.items():
            script = script.replace(original, str(replacement))
        (self.source / 'install.sh').write_text(script)
        for name in ('sub2api-egress.service', 'mihomo-trial-rollback.service',
                     'mihomo-trial-rollback.timer', 'guard-dependency.conf'):
            shutil.copyfile(SOURCE / name, self.source / name)
        (self.source / 'egress_guard.py').write_text(GUARD)
        (self.source / 'mihomo_control.py').write_text('')
        (self.source / 'mihomo-ctl').write_text('#!/bin/sh\nexit 0\n')
        mock_bin = self.root / 'mock-bin'
        mock_bin.mkdir()
        for name in ('systemctl', 'install', 'sync'):
            path = mock_bin / name
            path.write_text(COMMAND)
            path.chmod(0o755)
        for parent, name in (('/usr/local/bin', 'mihomo'), ('/usr/local/libexec', 'mihomo-check')):
            path = self.mapping[parent] / name
            path.write_text('#!/bin/sh\nexit 0\n')
            path.chmod(0o755)
        self.env = {**os.environ, 'PATH': str(mock_bin) + ':/usr/bin:/bin',
                    'TEST_EVENTS': str(self.events), 'TEST_STATE': str(self.state),
                    'PYTHONDONTWRITEBYTECODE': '1'}

    def run_install(self, failure=''):
        result = subprocess.run(['/bin/bash', str(self.source / 'install.sh'), '--adopt-confirmed'],
                                env={**self.env, 'TEST_FAIL': failure},
                                text=True, capture_output=True, timeout=10)
        self.assertEqual(result.returncode == 0, not failure, result.stdout + result.stderr)
        return [json.loads(line) for line in self.events.read_text().splitlines()]

    def assert_boot_dependencies(self):
        self.assertIn('egress_guard.py restore', (self.units / 'sub2api-egress.service').read_text())
        for name in ('sub2api', 'mihomo'):
            content = (self.units / (name + '.service.d') / 'egress-guard.conf').read_text()
            self.assertIn('Requires=sub2api-egress.service', content)
            self.assertIn('After=sub2api-egress.service', content)
            self.assertIn('egress_guard.py verify', content)

    def test_nft_failure_retains_boot_dependencies_and_locked_intent(self):
        events = self.run_install('arm')
        self.assert_boot_dependencies()
        self.assertEqual(json.loads(self.state.read_text())['phase'], 'confirmed')
        self.assertIn(['systemctl', 'stop', 'sub2api.service'], events)
        self.assertLess(events.index(['sync', '-f', str(self.units)]),
                        events.index(['arm', 'confirmed']))

    def test_dependency_install_failure_never_arms(self):
        events = self.run_install('dependency')
        self.assertNotIn(['arm', 'confirmed'], events)
        self.assertEqual(json.loads(self.state.read_text())['phase'], 'off')

    def test_sync_failure_never_arms(self):
        events = self.run_install('sync')
        self.assertNotIn(['arm', 'confirmed'], events)

    def test_reload_failure_never_arms(self):
        events = self.run_install('daemon-reload')
        self.assertNotIn(['arm', 'confirmed'], events)

    def test_activation_failure_retains_boot_dependencies_and_lock(self):
        events = self.run_install('enable')
        self.assert_boot_dependencies()
        self.assertEqual(json.loads(self.state.read_text())['phase'], 'confirmed')
        self.assertIn(['systemctl', 'stop', 'sub2api.service'], events)

    def test_success_keeps_live_services_running(self):
        events = self.run_install()
        self.assert_boot_dependencies()
        self.assertLess(events.index(['systemctl', 'daemon-reload']),
                        events.index(['arm', 'confirmed']))
        self.assertIn(['systemctl', 'enable', '--now', 'sub2api-egress.service'], events)
        self.assertFalse(any(e[:2] in (['systemctl', 'stop'], ['systemctl', 'restart']) for e in events))


if __name__ == '__main__':
    unittest.main()
