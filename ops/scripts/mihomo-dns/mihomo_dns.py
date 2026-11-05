#!/usr/bin/python3
"""Switch only Claude DNS forwarding with the Mihomo service lifecycle."""
import argparse
import fcntl
import os
from pathlib import Path
import socket
import struct
import subprocess
import tempfile
import time

RUNTIME = Path('/run/mihomo-dns')
MIHOMO = 'mihomo.service'
RESOLVER = 'dnsmasq-claude.service'
DNS_ADDRESS = ('127.0.0.1', 1053)
DOMAINS = ('anthropic.com', 'claude.ai', 'claude.com')


def execute(args):
    return subprocess.check_output(args, text=True).strip()


def dns_ready():
    """Ask for a local Fake-IP answer; readiness needs no public DNS access."""
    identifier = os.urandom(2)
    question = b'\x03api\x09anthropic\x03com\x00\x00\x01\x00\x01'
    query = identifier + struct.pack('!HHHHH', 0x0100, 1, 0, 0, 0) + question
    try:
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as client:
            client.settimeout(0.2)
            client.connect(DNS_ADDRESS)
            client.send(query)
            answer = client.recv(4096)
        if len(answer) < 12 or answer[:2] != identifier:
            return False
        flags, questions, answers = struct.unpack('!HHH', answer[2:8])
        return bool(flags & 0x8000) and not flags & 0x020F and questions == 1 and answers > 0
    except OSError:
        return False


class Controller:
    def __init__(self, directory=RUNTIME, run=execute, ready=dns_ready):
        self.directory = Path(directory)
        self.run = run
        self.ready = ready

    def prepare_directory(self):
        self.directory.mkdir(mode=0o755, parents=True, exist_ok=True)
        info = self.directory.lstat()
        if self.directory.is_symlink() or info.st_uid != 0 or info.st_mode & 0o022:
            raise RuntimeError('DNS runtime directory must be root-owned and not writable by others')
        # Mihomo's UMask=0077 must not prevent dnsmasq's unprivileged worker
        # from re-reading servers.conf after it drops root.
        self.directory.chmod(0o755)

    def running(self):
        properties = dict(line.split('=', 1) for line in self.run([
            '/usr/bin/systemctl', 'show', MIHOMO, '-p', 'ActiveState', '-p', 'MainPID',
        ]).splitlines() if '=' in line)
        return properties.get('ActiveState') == 'active' and int(properties.get('MainPID', '0')) > 0

    def publish(self, enabled, reload):
        content = ''.join(f'server=/{domain}/{DNS_ADDRESS[0]}#{DNS_ADDRESS[1]}\n'
                          for domain in DOMAINS) if enabled else ''
        fd, temporary = tempfile.mkstemp(prefix='.servers-', dir=self.directory)
        try:
            with os.fdopen(fd, 'w') as output:
                os.fchmod(output.fileno(), 0o644)
                output.write(content)
                output.flush()
                os.fsync(output.fileno())
            os.replace(temporary, self.directory / 'servers.conf')
        finally:
            if os.path.exists(temporary):
                os.unlink(temporary)
        if reload:
            pid = int(self.run(['/usr/bin/systemctl', 'show', RESOLVER, '-p', 'MainPID', '--value']))
            if pid > 0:
                self.run(['/usr/bin/systemctl', 'kill', '--kill-whom=main', '--signal=HUP', RESOLVER])
        return 'mihomo' if enabled else 'ordinary'

    def change(self, command):
        # systemd orders Mihomo after dnsmasq; prepare never waits for a start job.
        # The flock also serializes dnsmasq restart preparation with on/off hooks.
        self.prepare_directory()
        fd = os.open(self.directory / 'control.lock', os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'a') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            if command == 'prepare':
                # active means ExecStartPost=on already passed the readiness
                # check. A transient DNS timeout must not revoke forwarding:
                # no new on hook runs when that same process recovers.
                return self.publish(self.running(), reload=False)
            if command == 'off':
                return self.publish(False, reload=True)
            deadline = time.monotonic() + 8
            while not self.ready():
                if time.monotonic() >= deadline:
                    self.publish(False, reload=True)
                    raise RuntimeError('Mihomo DNS not ready; ordinary DNS retained')
                time.sleep(0.1)
            return self.publish(True, reload=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=('prepare', 'on', 'off'))
    args = parser.parse_args()
    if os.geteuid() != 0:
        parser.error('root is required')
    print('Claude DNS upstream: ' + Controller().change(args.command))


if __name__ == '__main__':
    main()
