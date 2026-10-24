#!/usr/bin/python3
"""Control-state fault tests; systemctl is mocked and no live services change."""
import contextlib
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import Mock, patch

import egress_guard as eg
import mihomo_control as mc


class ControlTests(unittest.TestCase):
    def setUp(self):
        self.guard = Mock()
        self.guard.verify.return_value = {'phase': 'trial'}
        self.guard.read.return_value = {'phase': 'trial'}
        self.events = []
        self.fail_on = None
        self.running = True
        self.timer = False
        self.timer_substate = 'dead'

        def execute(args, optional=False):
            self.events.append(args)
            if self.fail_on and self.fail_on in args:
                if optional:
                    return subprocess.CompletedProcess(args, 1, '', 'injected')
                raise eg.GuardError('injected command failure')
            if 'is-active' in args:
                active = self.running if args[-1] == 'mihomo.service' else self.timer
                return subprocess.CompletedProcess(args, 0 if active else 3, '', '')
            if 'show' in args and mc.TIMER in args:
                output = ('ActiveState=' + ('active' if self.timer else 'inactive')
                          + '\nSubState=' + self.timer_substate + '\n')
            else:
                output = 'ActiveState=inactive\nMainPID=0\n' if 'show' in args else ''
            return subprocess.CompletedProcess(args, 0, output, '')

        self.control = mc.Controller(self.guard, execute)
        self.lock_patch = patch.object(mc, 'exclusive', side_effect=lambda *a: contextlib.nullcontext())
        self.lock_patch.start()
        self.output_patch = contextlib.redirect_stdout(io.StringIO())
        self.output_patch.__enter__()

    def tearDown(self):
        self.output_patch.__exit__(None, None, None)
        self.lock_patch.stop()

    def test_start_arms_before_restarting_mihomo(self):
        self.guard.arm.side_effect = lambda *a: self.events.append(['ARM'])
        self.control.start()
        arm = self.events.index(['ARM'])
        restart = next(i for i, a in enumerate(self.events) if 'restart' in a)
        self.assertLess(arm, restart)
        self.guard.unlock.assert_not_called()

    def test_start_failure_never_unlocks(self):
        self.fail_on = 'restart'
        with self.assertRaises(eg.GuardError):
            self.control.start()
        self.guard.arm.assert_called_once_with('trial')
        self.guard.unlock.assert_not_called()

    def test_elapsed_timer_allows_retry_without_unlock(self):
        self.timer = True
        self.timer_substate = 'elapsed'
        self.control.start()
        self.guard.arm.assert_called_once_with('trial')
        self.guard.unlock.assert_not_called()
        stop = self.events.index([mc.SYSTEMCTL, 'stop', mc.TIMER, mc.ROLLBACK])
        start = self.events.index([mc.SYSTEMCTL, 'start', mc.TIMER])
        self.assertLess(stop, start)

    def test_unfinished_trial_cannot_be_replaced(self):
        self.timer = True
        for substate in ('waiting', 'running'):
            with self.subTest(substate=substate):
                self.timer_substate = substate
                with self.assertRaisesRegex(eg.GuardError, 'trial is pending'):
                    self.control.start()
                self.guard.arm.assert_not_called()
                self.guard.unlock.assert_not_called()

    def test_timer_query_failure_does_not_arm(self):
        self.fail_on = 'show'
        with self.assertRaises(eg.GuardError):
            self.control.start()
        self.guard.arm.assert_not_called()

    def test_arm_failure_stops_sub2api(self):
        self.guard.arm.side_effect = eg.GuardError('nft failed')
        with self.assertRaisesRegex(eg.GuardError, 'Sub2API stopped'):
            self.control.start()
        self.assertTrue(any('stop' in a and 'sub2api.service' in a for a in self.events))
        self.assertFalse(any('restart' in a for a in self.events))

    def test_lost_rules_during_activation_stop_sub2api(self):
        self.guard.verify.side_effect = eg.GuardError('missing rules')
        with self.assertRaisesRegex(eg.GuardError, 'Sub2API stopped'):
            self.control.start()
        self.assertTrue(any('sub2api.service' in a for a in self.events))
        self.guard.unlock.assert_not_called()

    def test_confirm_verifies_rules_and_never_unlocks(self):
        self.control.confirm()
        self.guard.verify.assert_called_once()
        self.guard.confirm.assert_called_once()
        self.guard.unlock.assert_not_called()

    def test_confirm_missing_rules_stops_sub2api(self):
        self.guard.verify.side_effect = eg.GuardError('missing rules')
        with self.assertRaises(eg.GuardError):
            self.control.confirm()
        self.guard.confirm.assert_not_called()
        self.assertTrue(any('sub2api.service' in a for a in self.events))

    def test_timeout_only_stops_mihomo(self):
        self.control.expire_trial()
        self.assertTrue(any('stop' in a and 'mihomo.service' in a for a in self.events))
        self.guard.unlock.assert_not_called()

    def test_stale_timeout_cannot_stop_confirmed_service(self):
        self.guard.read.return_value = {'phase': 'confirmed'}
        self.control.expire_trial()
        self.assertEqual(self.events, [])

    def test_explicit_stop_unlocks_after_full_stop(self):
        self.guard.unlock.side_effect = lambda: self.events.append(['UNLOCK'])
        self.control.stop()
        self.assertEqual(self.events[-1], ['UNLOCK'])
        self.assertTrue(any('show' in a for a in self.events[:-1]))

    def test_stop_failure_retains_lock(self):
        self.fail_on = 'disable'
        with self.assertRaises(eg.GuardError):
            self.control.stop()
        self.guard.unlock.assert_not_called()

    def test_still_running_service_cannot_unlock(self):
        execute = self.control.execute
        def still_running(args, optional=False):
            if 'show' in args:
                return subprocess.CompletedProcess(args, 0, 'ActiveState=active\nMainPID=12\n', '')
            return execute(args, optional=optional)
        self.control.execute = still_running
        with self.assertRaises(eg.GuardError):
            self.control.stop()
        self.guard.unlock.assert_not_called()


class FingerprintTests(unittest.TestCase):
    def test_counter_values_and_handles_are_ignored(self):
        a = {'nftables': [{'metainfo': {'version':'a'}},
                         {'rule': {'handle':1, 'expr':[{'counter':{'packets':1,'bytes':2}}, {'accept':None}]}}]}
        b = {'nftables': [{'metainfo': {'version':'b'}},
                         {'rule': {'handle':7, 'expr':[{'counter':{'packets':9,'bytes':100}}, {'accept':None}]}}]}
        self.assertEqual(eg.canonical_digest(a), eg.canonical_digest(b))
        b['nftables'][1]['rule']['expr'][-1] = {'drop':None}
        self.assertNotEqual(eg.canonical_digest(a), eg.canonical_digest(b))


if __name__ == '__main__':
    unittest.main()
