#!/usr/bin/python3
"""Persistent, UID-scoped egress guard. Only an explicit controller stop unlocks."""
import contextlib
import fcntl
import hashlib
import json
import os
from pathlib import Path
import pwd
import subprocess
import sys
import tempfile

STATE_DIR = Path('/var/lib/sub2api-egress')
STATE = STATE_DIR / 'state.json'
LOCK = Path('/run/lock/sub2api-egress.lock')
NFT = '/usr/sbin/nft'
SYSTEMCTL = '/usr/bin/systemctl'
TABLE = 'sub2api_egress_guard'
TUN = 'mihomo'
USER = 'sub2api'


class GuardError(RuntimeError):
    pass


def run(args, *, input=None):
    result = subprocess.run(args, input=input, text=True, capture_output=True)
    if result.returncode:
        raise GuardError(f'{args[0]} failed: {result.stderr.strip()}')
    return result.stdout


@contextlib.contextmanager
def exclusive(path=LOCK):
    fd = os.open(path, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600)
    try:
        if os.fstat(fd).st_uid != 0:
            raise GuardError('Control lock must be owned by root')
        fcntl.flock(fd, fcntl.LOCK_EX)
        yield
    finally:
        os.close(fd)


def rules(uid):
    # Run before Mihomo's OUTPUT redirect at priority -150. Do not allow all
    # established connections: a pre-existing direct connection must be blocked.
    return f'''table inet {TABLE} {{
    chain output {{
        type filter hook output priority -160; policy accept;
        meta skuid {uid} jump protected
    }}
    chain protected {{
        oifname "lo" ip daddr 127.0.0.0/8 counter accept
        oifname "lo" ip6 daddr ::1 counter accept
        oifname "{TUN}" counter accept
        counter reject with icmpx type admin-prohibited
    }}
}}
'''


def canonical_digest(document):
    def clean(value):
        if isinstance(value, dict):
            return {k: clean(v) for k, v in value.items()
                    if k not in ('handle', 'packets', 'bytes')}
        if isinstance(value, list):
            return [clean(v) for v in value]
        return value
    entries = [clean(v) for v in document['nftables'] if 'metainfo' not in v]
    return hashlib.sha256(json.dumps(entries, sort_keys=True).encode()).hexdigest()


class Guard:
    def __init__(self, directory=STATE_DIR, executor=run, uid=None):
        self.directory = Path(directory)
        self.state_path = self.directory / 'state.json'
        self.execute = executor
        self.uid = pwd.getpwnam(USER).pw_uid if uid is None else uid

    def read(self):
        try:
            metadata = self.directory.lstat()
            file_metadata = self.state_path.lstat()
            if (self.directory.is_symlink() or self.state_path.is_symlink()
                    or metadata.st_uid != 0 or metadata.st_mode & 0o077
                    or file_metadata.st_uid != 0 or file_metadata.st_mode & 0o077):
                raise GuardError('State must be root-owned and private')
            state = json.loads(self.state_path.read_text())
        except (OSError, ValueError) as exc:
            raise GuardError('Missing or unreadable guard state; refusing to assume unlocked') from exc
        if (state.get('version') != 1 or state.get('phase') not in ('off', 'trial', 'confirmed')
                or state.get('uid') != self.uid or state.get('tun') != TUN):
            raise GuardError('Invalid state or changed service UID; operator repair required')
        return state

    def write(self, state):
        fd, temporary = tempfile.mkstemp(prefix='.state-', dir=self.directory)
        try:
            with os.fdopen(fd, 'w') as output:
                json.dump(state, output, sort_keys=True)
                output.write('\n')
                output.flush()
                os.fsync(output.fileno())
            os.replace(temporary, self.state_path)
            directory_fd = os.open(self.directory, os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(directory_fd)
            finally:
                os.close(directory_fd)
        finally:
            if os.path.exists(temporary):
                os.unlink(temporary)

    def initialize(self):
        if self.state_path.exists() or self.state_path.is_symlink():
            raise GuardError('State already exists; initialization never resets a lock')
        self.directory.mkdir(mode=0o700, parents=False, exist_ok=True)
        metadata = self.directory.lstat()
        if self.directory.is_symlink() or metadata.st_uid != 0 or metadata.st_mode & 0o077:
            raise GuardError('State directory must be root-owned and mode 0700')
        if self.exists():
            raise GuardError('Rules already exist; refusing unlocked initialization')
        self.write({'version': 1, 'phase': 'off', 'uid': self.uid, 'tun': TUN,
                    'rules_digest': None})

    def exists(self):
        document = json.loads(self.execute([NFT, '-j', 'list', 'tables']))
        return any(entry.get('table', {}).get('family') == 'inet'
                   and entry.get('table', {}).get('name') == TABLE
                   for entry in document['nftables'])

    def digest(self):
        if not self.exists():
            return None
        return canonical_digest(json.loads(self.execute([NFT, '-j', '-s', 'list', 'table', 'inet', TABLE])))

    def install(self):
        prefix = f'delete table inet {TABLE}\n' if self.exists() else ''
        # One nft transaction: replacing our own table never creates an open gap.
        self.execute([NFT, '-f', '-'], input=prefix + rules(self.uid))
        digest = self.digest()
        if digest is None:
            raise GuardError('Firewall transaction completed but guard table is absent')
        return digest

    def verify(self):
        state = self.read()
        actual = self.digest()
        if state['phase'] == 'off':
            if actual is not None:
                raise GuardError('State says off but guard rules remain; explicit stop required')
        elif not state.get('rules_digest') or actual != state['rules_digest']:
            raise GuardError('Protection requested but firewall rules are missing or changed')
        return state

    def arm(self, phase='trial'):
        state = self.read()
        if phase not in ('trial', 'confirmed'):
            raise GuardError('Invalid locked phase')
        state.update(phase=phase, rules_digest=None)
        self.write(state)  # Persist intent first; recovery must never assume off.
        state['rules_digest'] = self.install()
        self.write(state)
        self.verify()

    def restore(self):
        state = self.read()
        if state['phase'] == 'off':
            self.verify()  # Do not silently remove unexpected rules.
            return
        if not state.get('rules_digest') or self.digest() != state['rules_digest']:
            state['rules_digest'] = self.install()
            self.write(state)
        self.verify()

    def confirm(self):
        state = self.verify()
        if state['phase'] == 'off':
            raise GuardError('Nothing is locked; run start first')
        state['phase'] = 'confirmed'
        self.write(state)

    def unlock(self):
        state = self.read()
        if self.exists():
            self.execute([NFT, '-f', '-'], input=f'delete table inet {TABLE}\n')
        try:
            self.write({**state, 'phase': 'off', 'rules_digest': None})
        except Exception:
            # write() may have replaced state.json before its directory fsync
            # failed. Recover from the saved intent, never the possibly-off file.
            if state['phase'] != 'off':
                state['rules_digest'] = self.install()
                self.write(state)
                self.verify()
            raise
        self.verify()

    def status(self):
        state = self.read()
        actual = self.digest()
        return {'phase': state['phase'], 'uid': self.uid, 'tun': TUN,
                'rules_present': actual is not None,
                'consistent': (actual is None if state['phase'] == 'off'
                               else bool(state.get('rules_digest')) and actual == state['rules_digest'])}


def main():
    if os.geteuid() != 0:
        raise GuardError('Run with sudo')
    action = sys.argv[1] if len(sys.argv) == 2 else ''
    if action not in ('initialize', 'restore', 'verify', 'status'):
        raise GuardError('Usage: egress_guard.py {initialize|restore|verify|status}')
    with exclusive():
        guard = Guard()
        if action == 'status':
            status = guard.status()
            print(json.dumps(status, sort_keys=True))
            return 0 if status['consistent'] else 1
        getattr(guard, action)()
    return 0


if __name__ == '__main__':
    os.umask(0o077)
    try:
        sys.exit(main())
    except (GuardError, OSError, ValueError) as exc:
        print(f'egress guard: {exc}', file=sys.stderr)
        sys.exit(1)
