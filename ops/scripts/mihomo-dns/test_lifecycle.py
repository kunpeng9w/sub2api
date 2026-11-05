#!/usr/bin/python3
"""CI-only unit and real systemd/dnsmasq lifecycle verification."""
import importlib.util
import os
from pathlib import Path
import signal
import socket
import struct
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

SOURCE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('mihomo_dns', SOURCE / 'mihomo_dns.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


def query(port, domain='api.anthropic.com'):
    identifier = os.urandom(2)
    question = b''.join(bytes([len(label)]) + label.encode('ascii')
                        for label in domain.split('.')) + b'\x00\x00\x01\x00\x01'
    message = identifier + struct.pack('!HHHHH', 0x0100, 1, 0, 0, 0) + question
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as client:
        client.settimeout(0.3)
        client.sendto(message, ('127.0.0.1', port))
        answer, _ = client.recvfrom(4096)
    if answer[:2] != identifier or len(answer) < 16 or not struct.unpack('!H', answer[6:8])[0]:
        raise ValueError('No DNS answer')
    return socket.inet_ntoa(answer[-4:])


def serve(port, address):
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as server:
        server.bind(('127.0.0.1', port))
        while True:
            request, peer = server.recvfrom(4096)
            # dnsmasq adds EDNS metadata to forwarded queries. Echo only the
            # question, not that additional section, before our A answer.
            end = 12
            while request[end]:
                end += request[end] + 1
            question = request[12:end + 5]
            answer = (request[:2] + struct.pack('!HHHHH', 0x8180, 1, 1, 0, 0)
                      + question + b'\xc0\x0c' + struct.pack('!HHIH', 1, 1, 0, 4)
                      + socket.inet_aton(address))
            server.sendto(answer, peer)


class ControllerTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.runtime = Path(temporary.name) / 'runtime'
        self.events = []
        self.state = 'inactive'
        self.ready = False
        self.resolver_pid = 123
        self.controller = module.Controller(self.runtime, self.run_command, lambda: self.ready)

    def run_command(self, args):
        self.events.append(args)
        if 'ActiveState' in args:
            return f'ActiveState={self.state}\nMainPID={456 if self.state == "active" else 0}'
        if '--value' in args:
            return str(self.resolver_pid)
        return ''

    def test_start_stop_clears_overrides_and_reloads_without_restarting_dnsmasq(self):
        self.ready = True
        self.assertEqual(self.controller.change('on'), 'mihomo')
        content = (self.runtime / 'servers.conf').read_text()
        for domain in module.DOMAINS:
            self.assertIn(f'server=/{domain}/127.0.0.1#1053\n', content)
        self.assertEqual(self.runtime.stat().st_mode & 0o777, 0o755)
        self.assertEqual((self.runtime / 'servers.conf').stat().st_mode & 0o777, 0o644)
        self.assertEqual(self.controller.change('off'), 'ordinary')
        self.assertEqual((self.runtime / 'servers.conf').read_text(), '')
        self.assertEqual(sum('kill' in event for event in self.events), 2)
        self.assertFalse(any('restart' in event or 'stop' in event for event in self.events))

    def test_boot_and_resolver_restart_reconstruct_current_state_without_signalling(self):
        for state, ready, expected in [('inactive', True, 'ordinary'), ('active', False, 'mihomo'),
                                       ('active', True, 'mihomo'), ('activating', True, 'ordinary'),
                                       ('deactivating', True, 'ordinary'), ('failed', True, 'ordinary')]:
            self.state, self.ready = state, ready
            self.assertEqual(self.controller.change('prepare'), expected)
        self.assertFalse(any('kill' in event for event in self.events))

    def test_not_ready_clears_stale_forwarding_and_fails_start(self):
        self.ready = True
        self.controller.change('on')
        self.ready = False
        with patch.object(module.time, 'monotonic', side_effect=[0, 9]):
            with self.assertRaisesRegex(RuntimeError, 'not ready'):
                self.controller.change('on')
        self.assertEqual((self.runtime / 'servers.conf').read_text(), '')

    def test_mihomo_umask_keeps_runtime_readable_after_privilege_drop(self):
        previous = os.umask(0o077)
        try:
            self.controller.change('off')
        finally:
            os.umask(previous)
        self.assertEqual(self.runtime.stat().st_mode & 0o777, 0o755)
        self.assertEqual((self.runtime / 'servers.conf').stat().st_mode & 0o777, 0o644)

    def test_no_resolver_process_needs_no_signal(self):
        self.resolver_pid = 0
        self.controller.change('off')
        self.assertFalse(any('kill' in event for event in self.events))


class SystemdIntegrationTests(unittest.TestCase):
    """Use distinct temporary units and local fake DNS; never installed services."""
    def command(self, *args, check=True):
        result = subprocess.run(args, text=True, capture_output=True, timeout=25)
        if check and result.returncode:
            self.fail(f'{args}: {result.stdout}\n{result.stderr}')
        return result

    def setUp(self):
        if os.geteuid() != 0 or not Path('/run/systemd/system').exists():
            self.fail('Run this CI integration suite as root under systemd')
        temporary = tempfile.TemporaryDirectory(prefix='mihomo-dns-ci-')
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.root.chmod(0o755)
        self.resolver = 'sub2api-dns-fixture-resolver.service'
        self.mihomo = 'sub2api-dns-fixture-mihomo.service'
        self.unit_paths = []
        self.addCleanup(self.cleanup_units)
        self.ports = []
        # Reserve distinct local UDP ports before releasing them to fixtures.
        reservations = []
        for _ in range(3):
            server = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
            server.bind(('127.0.0.1', 0))
            self.ports.append(server.getsockname()[1])
            reservations.append(server)
        for server in reservations:
            server.close()
        self.normal_port, self.fake_port, self.resolver_port = self.ports
        self.runtime = self.root / 'runtime'
        helper = (SOURCE / 'mihomo_dns.py').read_text()
        helper = helper.replace("Path('/run/mihomo-dns')", f'Path({str(self.runtime)!r})')
        helper = helper.replace("'mihomo.service'", repr(self.mihomo))
        helper = helper.replace("'dnsmasq-claude.service'", repr(self.resolver))
        helper = helper.replace("('127.0.0.1', 1053)", f"('127.0.0.1', {self.fake_port})")
        (self.root / 'helper.py').write_text(helper)
        normal = subprocess.Popen([sys.executable, str(SOURCE / 'test_lifecycle.py'),
                                   'serve', str(self.normal_port), '203.0.113.8'])
        self.addCleanup(normal.wait, 5)
        self.addCleanup(normal.terminate)
        config = (SOURCE / 'dnsmasq-claude.conf').read_text()
        config = config.replace('port=53\n', f'port={self.resolver_port}\n')
        config = config.replace('server=100.100.2.136', f'server=127.0.0.1#{self.normal_port}')
        config = config.replace('server=100.100.2.138\n', '')
        config = config.replace('/run/mihomo-dns/servers.conf', str(self.runtime / 'servers.conf'))
        (self.root / 'dnsmasq.conf').write_text(config)
        self.write_unit(self.resolver, '[Service]\nType=simple\n'
                        f'ExecStart=/usr/sbin/dnsmasq --keep-in-foreground --conf-file={self.root}/dnsmasq.conf --pid-file=\n'
                        + self.dropin('dnsmasq-lifecycle.conf'))
        self.write_mihomo()
        self.command('systemctl', 'daemon-reload')

    def dropin(self, name):
        return (SOURCE / name).read_text().replace('/usr/local/libexec/mihomo_dns.py',
                    f'{sys.executable} {self.root}/helper.py').replace('dnsmasq-claude.service', self.resolver)

    def write_unit(self, name, text):
        path = Path('/run/systemd/system') / name
        if path not in self.unit_paths:
            if path.exists():
                self.fail('Fixture name already occupied')
            self.unit_paths.append(path)
        path.write_text(text)

    def write_mihomo(self, failing=False):
        command = '/bin/false' if failing else (f'{sys.executable} {SOURCE}/test_lifecycle.py '
                                               f'serve {self.fake_port} 198.18.0.4')
        self.write_unit(self.mihomo, f'[Service]\nType=simple\nUMask=0077\nExecStart={command}\n'
                        + self.dropin('mihomo-lifecycle.conf'))

    def cleanup_units(self):
        subprocess.run(['systemctl', 'stop', self.mihomo, self.resolver], capture_output=True, timeout=25)
        logs = subprocess.run(['journalctl', '-u', self.mihomo, '-u', self.resolver,
                               '--no-pager', '-n', '60'], capture_output=True, text=True)
        print('\nFixture service journal:\n' + logs.stdout)
        for path in self.unit_paths:
            path.unlink(missing_ok=True)
        subprocess.run(['systemctl', 'daemon-reload'], capture_output=True, timeout=25)
        subprocess.run(['systemctl', 'reset-failed', self.mihomo, self.resolver], capture_output=True)

    def answer(self, expected, domain='api.anthropic.com'):
        deadline = time.monotonic() + 6
        actual = None
        while time.monotonic() < deadline:
            try:
                actual = query(self.resolver_port, domain)
                if actual == expected:
                    return
            except (OSError, ValueError):
                pass
            time.sleep(0.1)
        self.fail(f'Expected {expected}, got {actual}')

    def resolver_pid(self):
        return self.command('systemctl', 'show', self.resolver, '-p', 'MainPID', '--value').stdout.strip()

    def test_resolver_restart_during_dns_timeout_recovers_without_mihomo_restart(self):
        self.command('systemctl', 'start', self.mihomo)
        self.answer('198.18.0.4')
        mihomo_pid = self.command('systemctl', 'show', self.mihomo,
                                  '-p', 'MainPID', '--value').stdout.strip()
        # Pause only the fixture's DNS worker. systemd still considers the
        # successfully started service active, as during a temporary stall.
        self.command('systemctl', 'kill', '--kill-whom=main', '--signal=STOP', self.mihomo)
        try:
            with self.assertRaises(TimeoutError):
                query(self.fake_port)
            self.command('systemctl', 'restart', self.resolver)
            self.answer('203.0.113.8', 'example.com')
            # A resolver restart must not silently route Claude to the ordinary
            # upstream while Mihomo is still active but temporarily unavailable.
            with self.assertRaises(TimeoutError):
                query(self.resolver_port)
            resolver_pid = self.resolver_pid()
        finally:
            self.command('systemctl', 'kill', '--kill-whom=main', '--signal=CONT', self.mihomo)
        self.answer('198.18.0.4')
        self.assertEqual(self.command('systemctl', 'show', self.mihomo,
                                     '-p', 'MainPID', '--value').stdout.strip(), mihomo_pid)
        self.assertEqual(self.resolver_pid(), resolver_pid)
        self.command('systemctl', 'stop', self.mihomo)
        self.answer('203.0.113.8')

    def test_real_lifecycle_start_stop_restart_crash_and_failed_start(self):
        self.command('systemctl', 'start', self.resolver)
        self.answer('203.0.113.8')
        initial_pid = self.resolver_pid()
        self.command('systemctl', 'start', self.mihomo)
        self.answer('198.18.0.4')
        self.command('systemctl', 'restart', self.mihomo)
        self.answer('198.18.0.4')
        self.assertEqual(self.resolver_pid(), initial_pid)
        self.command('systemctl', 'restart', self.resolver)
        self.answer('198.18.0.4')
        self.command('systemctl', 'stop', self.mihomo)
        self.answer('203.0.113.8')
        self.command('systemctl', 'restart', self.resolver)
        self.answer('203.0.113.8')
        self.command('systemctl', 'start', self.mihomo)
        self.answer('198.18.0.4')
        pid = self.resolver_pid()
        self.command('systemctl', 'kill', '--signal=KILL', self.mihomo)
        self.answer('203.0.113.8')
        self.assertEqual(self.resolver_pid(), pid)
        self.command('systemctl', 'reset-failed', self.mihomo)
        self.write_mihomo(failing=True)
        self.command('systemctl', 'daemon-reload')
        self.assertNotEqual(self.command('systemctl', 'start', self.mihomo, check=False).returncode, 0)
        self.answer('203.0.113.8')
        self.assertEqual((self.runtime / 'servers.conf').read_text(), '')


if __name__ == '__main__':
    if len(sys.argv) == 4 and sys.argv[1] == 'serve':
        serve(int(sys.argv[2]), sys.argv[3])
    else:
        unittest.main()
