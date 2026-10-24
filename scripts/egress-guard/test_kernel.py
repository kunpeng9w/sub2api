#!/usr/bin/python3
"""Real packet tests, always inside a new network namespace; no host mutations."""
import json
import os
from pathlib import Path
import socket
import stat
import subprocess
import sys
import tempfile
from unittest.mock import patch

from egress_guard import Guard, GuardError, NFT, TABLE, run

SERVER = r'''
import socket,threading,time
def serve(host,port,family,udp=False):
    s=socket.socket(family,socket.SOCK_DGRAM if udp else socket.SOCK_STREAM)
    s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
    if family==socket.AF_INET6:s.setsockopt(socket.IPPROTO_IPV6,socket.IPV6_V6ONLY,1)
    s.bind((host,port))
    if udp:
        while True:
            b,a=s.recvfrom(100);s.sendto(b,a)
    s.listen()
    def echo(c):
        with c:
            while True:
                b=c.recv(100)
                if not b:break
                c.sendall(b)
    while True:
        c,_=s.accept();threading.Thread(target=echo,args=(c,),daemon=True).start()
for h,f in [('0.0.0.0',socket.AF_INET),('::',socket.AF_INET6)]:
    for p in (18080,18081,45867):
        threading.Thread(target=serve,args=(h,p,f),daemon=True).start()
    threading.Thread(target=serve,args=(h,18080,f,True),daemon=True).start()
time.sleep(.2)
print('ready',flush=True)
while True:time.sleep(60)
'''
CLIENT = r'''
import socket,sys
family=socket.AF_INET6 if ':' in sys.argv[1] else socket.AF_INET
udp=sys.argv[3]=='udp'
s=socket.socket(family,socket.SOCK_DGRAM if udp else socket.SOCK_STREAM)
s.settimeout(1)
try:
    s.connect((sys.argv[1],int(sys.argv[2])))
    s.send(b'probe');assert s.recv(100)==b'probe'
    print('connected',flush=True)
    if sys.argv[3]=='persistent':
        for line in sys.stdin:
            try:
                s.send(b'again');assert s.recv(100)==b'again'
                print('connected',flush=True)
            except OSError:
                print('blocked',flush=True);break
except OSError:
    print('blocked',flush=True);sys.exit(3)
'''


def server(*prefix):
    p = subprocess.Popen([*prefix, sys.executable, '-u', '-c', SERVER], stdout=subprocess.PIPE,
                         stderr=subprocess.DEVNULL, text=True)
    if p.stdout.readline().strip() != 'ready':
        raise RuntimeError('Test echo server failed to start')
    return p


def probe(address, expected, port=18080, uid=999, protocol='tcp'):
    result = subprocess.run([sys.executable, '-c', CLIENT, address, str(port), protocol],
                            user=uid, group=988 if uid == 999 else 0, extra_groups=[],
                            text=True, capture_output=True, timeout=4)
    actual = result.stdout.strip()
    assert actual == expected, (address, protocol, expected, actual, result.stderr)


def inside(parent_namespace):
    assert os.geteuid() == 0
    assert os.readlink('/proc/self/ns/net') != parent_namespace, 'Refusing host network tests'
    children = []
    results = []
    ip = lambda *args: run(['/usr/sbin/ip', *args])
    try:
        ip('link', 'set', 'lo', 'up')
        remote = server('/usr/bin/unshare', '--net')
        children.append(remote)
        ip('link', 'add', 'eth0', 'type', 'veth', 'peer', 'name', 'test-peer')
        ip('link', 'set', 'test-peer', 'netns', str(remote.pid))
        ip('addr', 'add', '192.0.2.1/24', 'dev', 'eth0')
        ip('-6', 'addr', 'add', '2001:db8:100::1/64', 'dev', 'eth0', 'nodad')
        ip('link', 'set', 'eth0', 'up')
        for args in [('link','set','lo','up'),('addr','add','192.0.2.2/24','dev','test-peer'),
                     ('-6','addr','add','2001:db8:100::2/64','dev','test-peer','nodad'),
                     ('link','set','test-peer','up')]:
            run(['/usr/bin/nsenter', '-t', str(remote.pid), '-n', '/usr/sbin/ip', *args])
        local = server()
        children.append(local)
        probe('192.0.2.2', 'connected')
        probe('2001:db8:100::2', 'connected')
        probe('192.0.2.2', 'connected', protocol='udp')
        results.append('baseline direct IPv4/IPv6 TCP and UDP reachable')
        persistent = subprocess.Popen([sys.executable, '-u', '-c', CLIENT, '192.0.2.2', '18080', 'persistent'],
                                      user=999, group=988, extra_groups=[], stdin=subprocess.PIPE,
                                      stdout=subprocess.PIPE, text=True)
        children.append(persistent)
        assert persistent.stdout.readline().strip() == 'connected'
        with tempfile.TemporaryDirectory(prefix='egress-kernel-') as temporary:
            guard = Guard(directory=Path(temporary) / 'state', uid=999)
            guard.initialize()
            guard.arm()
            probe('192.0.2.2', 'blocked')
            probe('2001:db8:100::2', 'blocked')
            probe('192.0.2.2', 'blocked', protocol='udp')
            probe('2001:db8:100::2', 'blocked', protocol='udp')
            persistent.stdin.write('next\n'); persistent.stdin.flush()
            assert persistent.stdout.readline().strip() == 'blocked'
            results.append('locked: new and established direct TCP, IPv4/IPv6 UDP blocked')
            probe('127.0.0.1', 'connected', port=18081)
            probe('::1', 'connected', port=18081)
            probe('192.0.2.2', 'connected', uid=0)
            results.append('loopback and unrelated root sockets unaffected')
            guard.confirm()
            # Simulate current Mihomo auto-redirect, after our filter hook.
            ip('link', 'add', 'mihomo', 'type', 'dummy')
            ip('addr', 'add', '198.18.0.1/30', 'dev', 'mihomo')
            ip('link', 'set', 'mihomo', 'up')
            ip('route', 'add', '192.0.2.2/32', 'dev', 'mihomo', 'src', '198.18.0.1')
            run([NFT, '-f', '-'], input='''table inet test_tun {
chain output { type nat hook output priority -150; policy accept;
oifname "mihomo" meta nfproto ipv4 meta l4proto tcp redirect to :45867
}
}
''')
            probe('192.0.2.2', 'connected')
            guard.verify()  # Counters changing must not invalidate the fingerprint.
            results.append('locked: simulated Mihomo OUTPUT auto-redirect still works')
            ip('link', 'del', 'mihomo')
            probe('192.0.2.2', 'blocked')
            guard.verify()
            results.append('TUN removed: direct route restored but Sub2API still blocked')
            # Simulate volatile rules lost across reboot. Boot restore reinstalls them.
            run([NFT, '-f', '-'], input=f'delete table inet {TABLE}\n')
            try:
                guard.verify()
                raise AssertionError('Missing rules were not detected')
            except GuardError:
                pass
            restored = Guard(directory=guard.directory, uid=999)
            restored.restore()
            probe('192.0.2.2', 'blocked')
            assert restored.read()['phase'] == 'confirmed'
            results.append('persisted confirmed state restores lost firewall and blocks direct')
            # Semantic alteration must be detected, and restore must repair it.
            run([NFT, 'flush', 'chain', 'inet', TABLE, 'protected'])
            try:
                restored.verify()
                raise AssertionError('Altered rules were not detected')
            except GuardError:
                pass
            restored.restore()
            probe('192.0.2.2', 'blocked')
            results.append('modified rules detected and restore repairs the guard')
            restored.unlock()
            probe('192.0.2.2', 'connected')
            probe('2001:db8:100::2', 'connected')
            assert 'test_tun' in run([NFT, 'list', 'tables'])
            results.append('explicit release restores direct access; other nft tables preserved')
            # The durable lock must survive an nft failure before rules exist.
            with patch.object(restored, 'install', side_effect=GuardError('injected nft failure')):
                try:
                    restored.arm()
                    raise AssertionError('Installation failure was swallowed')
                except GuardError:
                    pass
            assert restored.read()['phase'] == 'trial'
            try:
                restored.verify()
                raise AssertionError('Partial arm was treated as protected')
            except GuardError:
                pass
            restored.restore()
            probe('192.0.2.2', 'blocked')
            try:
                restored.initialize()
                raise AssertionError('Initialization reset an existing lock')
            except GuardError:
                pass
            results.append('failed nft install preserves locked intent; restore repairs it; initialize cannot reset it')
            saved = restored.read()
            restored.state_path.write_text('{}')
            try:
                restored.restore()
                raise AssertionError('Corrupt state was treated as off')
            except GuardError:
                pass
            probe('192.0.2.2', 'blocked')
            restored.write(saved)
            # If persisting off fails after rule removal, re-establish protection.
            real_write = restored.write
            failed = False
            def fail_once(state):
                nonlocal failed
                if state['phase'] == 'off' and not failed:
                    failed = True
                    raise OSError('injected disk error')
                real_write(state)
            with patch.object(restored, 'write', side_effect=fail_once):
                try:
                    restored.unlock()
                    raise AssertionError('Failed state commit was swallowed')
                except OSError:
                    pass
            restored.verify()
            probe('192.0.2.2', 'blocked')
            results.append('corrupt state refuses restore; failed unlock state commit reinstalls firewall')
            # A directory fsync failure happens after the off state has replaced
            # the locked state. Recovery must not trust that new off state.
            saved_phase = restored.read()['phase']
            real_fsync = os.fsync
            directory_failure = False
            def fail_directory_once(fd):
                nonlocal directory_failure
                if stat.S_ISDIR(os.fstat(fd).st_mode) and not directory_failure:
                    directory_failure = True
                    assert restored.read()['phase'] == 'off'
                    raise OSError('injected directory fsync failure after rename')
                real_fsync(fd)
            with patch('egress_guard.os.fsync', side_effect=fail_directory_once):
                try:
                    restored.unlock()
                    raise AssertionError('Post-rename fsync failure was swallowed')
                except OSError as exc:
                    assert 'directory fsync' in str(exc)
            assert directory_failure
            assert restored.verify()['phase'] == saved_phase
            probe('192.0.2.2', 'blocked')
            results.append('post-rename unlock failure restores saved locked state and firewall')
            # Check the repaired state is sufficient for the next boot, when
            # volatile rules are absent, rather than only fixing current packets.
            run([NFT, '-f', '-'], input=f'delete table inet {TABLE}\n')
            rebooted = Guard(directory=restored.directory, uid=999)
            rebooted.restore()
            assert rebooted.verify()['phase'] == saved_phase
            probe('192.0.2.2', 'blocked')
            results.append('recovered unlock state restores protection after simulated rule loss at reboot')
            restored.unlock()
        print(json.dumps({'passed': True, 'scope': 'isolated network namespace; synthetic echo servers',
                          'checks': results}, ensure_ascii=False, indent=2))
    finally:
        for child in reversed(children):
            if child.poll() is None:
                child.terminate()
            try:
                child.wait(timeout=3)
            except subprocess.TimeoutExpired:
                child.kill(); child.wait()


if __name__ == '__main__':
    if len(sys.argv) == 3 and sys.argv[1] == '--inside':
        inside(sys.argv[2])
    else:
        namespace = os.readlink('/proc/self/ns/net')
        subprocess.run(['/usr/bin/sudo', '-n', '/usr/bin/unshare', '--net', sys.executable,
                        str(Path(__file__).resolve()), '--inside', namespace], check=True)
