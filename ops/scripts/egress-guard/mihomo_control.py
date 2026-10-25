#!/usr/bin/python3
"""Mihomo lifecycle control; only the public stop action releases the guard."""
import json
import os
from pathlib import Path
import subprocess
import sys

from egress_guard import Guard, GuardError, exclusive, SYSTEMCTL

CONTROL_LOCK = Path('/run/lock/mihomo-ctl.lock')
TIMER = 'mihomo-trial-rollback.timer'
ROLLBACK = 'mihomo-trial-rollback.service'


def command(args, *, optional=False):
    result = subprocess.run(args, text=True, capture_output=True)
    if result.returncode and not optional:
        raise GuardError(f'{" ".join(args)} failed: {result.stderr.strip()}')
    return result


class Controller:
    def __init__(self, guard=None, executor=command):
        self.guard = guard or Guard()
        self.execute = executor

    def systemctl(self, *args, optional=False):
        return self.execute([SYSTEMCTL, *args], optional=optional)

    def guarded(self, action, *args):
        with exclusive():
            return getattr(self.guard, action)(*args)

    def active(self, unit):
        return self.systemctl('is-active', '--quiet', unit, optional=True).returncode == 0

    def trial_pending(self):
        result = self.systemctl('show', TIMER, '-p', 'ActiveState', '-p', 'SubState')
        state = dict(line.split('=', 1) for line in result.stdout.splitlines() if '=' in line)
        # A one-shot timer remains active/elapsed after its rollback completes.
        # Waiting or running timers still belong to an unfinished trial.
        return (state.get('ActiveState') in ('active', 'activating')
                and state.get('SubState') != 'elapsed')

    def check(self):
        self.execute(['/usr/local/libexec/mihomo-check'])
        self.execute(['/usr/local/bin/mihomo', '-t', '-d', '/var/lib/mihomo',
                      '-f', '/etc/mihomo/config.yaml'])

    def cancel_timer(self):
        self.systemctl('stop', TIMER, ROLLBACK)

    def fail_closed(self, reason):
        # A requested lock without valid rules must not leave Sub2API running.
        stopped = self.systemctl('stop', 'sub2api.service', optional=True)
        suffix = '; Sub2API stopped' if stopped.returncode == 0 else '; FAILED TO STOP SUB2API'
        raise GuardError(str(reason) + suffix)

    def start(self):
        self.check()
        if self.trial_pending():
            raise GuardError('A trial is pending; confirm or stop it first')
        try:
            self.guarded('arm', 'trial')
        except Exception as exc:
            self.fail_closed(exc)
        try:
            self.cancel_timer()
            self.systemctl('start', 'sub2api-egress.service')
            self.systemctl('reset-failed', 'mihomo.service')
            self.systemctl('start', TIMER)
            self.systemctl('restart', 'mihomo.service')
            self.guarded('verify')
            if not self.active('mihomo.service'):
                raise GuardError('Mihomo did not become active')
        except Exception as exc:
            self.systemctl('stop', TIMER, 'mihomo.service', optional=True)
            # Persisted lock and firewall survive all activation failures.
            try:
                self.guarded('verify')
            except Exception as guard_error:
                self.fail_closed(f'{exc}; protection verification also failed: {guard_error}')
            raise
        print('Trial started; Sub2API is locked. Confirm within 180 seconds.')
        print('Timeout stops Mihomo but keeps the lock. Only mihomo-ctl stop unlocks.')

    def confirm(self):
        try:
            state = self.guarded('verify')
        except Exception as exc:
            self.fail_closed(exc)
        if state['phase'] == 'off' or not self.active('mihomo.service'):
            raise GuardError('A locked, running Mihomo is required; run start first')
        self.cancel_timer()
        self.systemctl('enable', 'mihomo.service')
        self.guarded('confirm')
        print('Confirmed. Sub2API remains locked across crashes and reboots.')

    def stop(self):
        self.cancel_timer()
        self.systemctl('disable', '--now', 'mihomo.service')
        result = self.systemctl('show', 'mihomo.service', '-p', 'ActiveState', '-p', 'MainPID')
        state = dict(line.split('=', 1) for line in result.stdout.splitlines() if '=' in line)
        if state.get('ActiveState') not in ('inactive', 'failed') or state.get('MainPID') != '0':
            raise GuardError('Mihomo is not fully stopped; guard remains locked')
        self.guarded('unlock')
        print('Mihomo stopped. Sub2API egress protection explicitly released.')

    def expire_trial(self):
        if self.guarded('read')['phase'] == 'trial':
            self.systemctl('stop', 'mihomo.service')
            print('Trial expired. Mihomo stopped; Sub2API protection remains locked.')

    def status(self):
        status = self.guarded('status')
        status['mihomo_active'] = self.active('mihomo.service')
        status['trial_timer_active'] = self.active(TIMER)
        print(json.dumps(status, sort_keys=True))
        return 0 if status['consistent'] else 1


def main():
    if os.geteuid() != 0:
        raise GuardError('Run with sudo')
    action = sys.argv[1] if len(sys.argv) == 2 else ''
    if action not in ('check', 'start', 'confirm', 'stop', 'status', 'expire-trial'):
        raise GuardError('Usage: mihomo-ctl {check|start|confirm|stop|status}')
    # Rollback acquires the same control lock as confirm/stop, preventing races.
    with exclusive(CONTROL_LOCK):
        controller = Controller()
        # Do not hold the guard lock while starting a unit that verifies it.
        # The control lock still serializes all controller state transitions.
        return getattr(controller, action.replace('-', '_'))() or 0


if __name__ == '__main__':
    os.umask(0o077)
    try:
        sys.exit(main())
    except (GuardError, OSError, ValueError) as exc:
        print(f'mihomo-ctl: {exc}', file=sys.stderr)
        sys.exit(1)
