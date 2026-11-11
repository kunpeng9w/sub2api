import base64
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import threading
import time
import unittest
from unittest.mock import patch
from urllib.error import HTTPError
from urllib.request import Request, urlopen

import monitor


TOKEN = 'unit-test-only-token-with-32-characters'


def inventory():
    return monitor.normalize_inventory([
        {'id': 0, 'name': 'Direct', 'protocol': 'direct', 'accounts': [{'id': 1, 'name': 'Direct account'}]},
        {'id': 2, 'name': 'Shared proxy', 'protocol': 'socks5', 'host': 'private.proxy.invalid', 'port': 1080,
         'username': 'secret-user', 'password': 'secret-password', 'status': 'active', 'expires_at': None,
         'accounts': [{'id': 2, 'name': 'Account A'}, {'id': 3, 'name': 'Account B'}]},
    ], TOKEN)


def sample(kind='download', ok=True, speed=20):
    return {'checked_at': monitor.iso(time.time()), 'kind': kind, 'ok': ok, 'speed_mbps': speed if ok else None,
            'ttfb_ms': 100, 'duration_ms': 400, 'requested_bytes': 1000000, 'transferred_bytes': 1000000 if ok else 0,
            'error': '' if ok else 'timeout', 'http_code': 200 if ok else 0, 'curl_exit': 0 if ok else 28,
            'connect_ms': 30, 'tls_ms': 60}


class MonitorTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.store = monitor.Store(Path(self.directory.name) / 'state.sqlite3')
        self.rules = {**monitor.DEFAULT_RULES, 'repetitions': 1}
        self.store.save_rules(self.rules)
        self.routes = inventory()
        self.store.inventory(self.routes)

    def tearDown(self):
        self.directory.cleanup()

    def test_secret_boundary_and_association_snapshots(self):
        self.store.record(self.routes[1], self.rules, [sample(), sample('upload')])
        self.routes[1]['accounts'] = [{'id': 4, 'name': 'New account'}]
        self.store.inventory(self.routes)
        snapshot = self.store.snapshot(7, time.time())
        history = self.store.history(2, 7, time.time())
        self.assertEqual(snapshot['items'][1]['accounts'][0]['name'], 'New account')
        self.assertEqual(history['runs'][0]['route']['accounts'][0]['name'], 'Account A')
        self.assertEqual(history['runs'][0]['rules'], self.rules)
        payload = json.dumps([snapshot, history])
        for secret in ('secret-user', 'secret-password', 'private.proxy.invalid', 'username', 'password'):
            self.assertNotIn(secret, payload)
        self.assertNotIn(b'secret-password', Path(self.store.path).read_bytes())

    def test_configuration_change_does_not_reuse_old_measurement(self):
        self.store.record(self.routes[1], self.rules, [sample(), sample('upload')])
        self.routes[1]['revision'] = 'new-proxy-configuration'
        self.store.inventory(self.routes)
        self.assertEqual(self.store.snapshot(7, time.time())['items'][1]['status'], 'untested')
        self.assertEqual(self.store.history(2, 7, time.time())['runs'], [])

    def test_missing_direction_and_failures_are_not_successful(self):
        self.store.record(self.routes[0], self.rules, [sample(), sample('upload', ok=False)])
        route = self.store.snapshot(1, time.time())['items'][0]
        self.assertEqual(route['status'], 'degraded')
        self.assertEqual(route['success_rate'], 50)
        self.assertIsNone(route['upload_mbps'])
        self.store.record(self.routes[0], self.rules, [sample(ok=False), sample('upload', ok=False)])
        self.assertEqual(self.store.snapshot(1, time.time())['items'][0]['status'], 'failed')

    def test_empty_history_staleness_and_disabled_routes(self):
        route = self.store.snapshot(1, time.time())['items'][0]
        self.assertIsNone(route['success_rate'])
        self.assertTrue(all(bucket['status'] == 'untested' for bucket in route['timeline']))
        self.store.record(self.routes[0], self.rules, [sample(), sample('upload')])
        self.assertEqual(self.store.snapshot(1, time.time() + 4000)['items'][0]['status'], 'stale')
        self.store.save_rules({**self.rules, 'upload_bytes': 2000000})
        self.assertEqual(self.store.snapshot(1, time.time())['items'][0]['status'], 'stale')
        self.routes[0]['available'] = False
        self.store.inventory(self.routes)
        self.assertEqual(self.store.snapshot(1, time.time())['items'][0]['status'], 'disabled')

    def test_budget_is_atomic_persistent_and_resets_at_utc_midnight(self):
        threads = [threading.Thread(target=lambda: self.store.reserve(100, 500, 1700000000)) for _ in range(20)]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join()
        restored = monitor.Store(self.store.path)
        self.assertFalse(restored.reserve(1, 500, 1700000000))
        self.assertTrue(restored.reserve(500, 500, 1700086400))

    def test_bad_rules_are_rejected_without_overwriting_saved_rules(self):
        for change in ({'enabled': 1}, {'repetitions': True}, {'repetitions': 0}, {'upload_min_mbps': float('nan')},
                       {'address_family': 'garbage'}, {'connect_timeout_seconds': 60, 'request_timeout_seconds': 5},
                       {'endpoint': 'https://not-cloudflare.invalid'}, {'daily_budget_bytes': 0}):
            with self.subTest(change=change), self.assertRaises(ValueError):
                self.store.save_rules({**self.rules, **change})
        self.assertEqual(self.store.rules(), self.rules)

    def test_proxy_credentials_stay_in_stdin_and_direct_ignores_environment_proxy(self):
        completed = subprocess.CompletedProcess([], 0, json.dumps({'http_code': 200, 'size_upload': 1000000, 'time_total': 2, 'time_starttransfer': 1}), '')
        with patch('monitor.subprocess.run', return_value=completed) as run:
            result = monitor.probe(self.routes[1], 'upload', self.rules, Path('/unused/upload.bin'), 30)
        command = run.call_args.args[0]
        self.assertEqual(command[-1], 'https://speed.cloudflare.com/__up')
        self.assertNotIn('secret-password', ' '.join(command))
        self.assertIn('secret-password', run.call_args.kwargs['input'])
        self.assertIn('socks5h://', run.call_args.kwargs['input'])
        self.assertNotIn('--location', command)
        self.assertIn('--upload-file', command)
        self.assertNotIn('--data-binary', command)
        self.assertEqual(result['speed_mbps'], 4)
        self.assertEqual(monitor.curl_config(self.routes[0]), 'proxy = ""\nnoproxy = "*"\n')

    def test_http_or_partial_payload_cannot_be_reported_as_success(self):
        for raw in ({'http_code': 403, 'size_download': 1000000}, {'http_code': 200, 'size_download': 3}):
            completed = subprocess.CompletedProcess([], 0, json.dumps(raw), '')
            with patch('monitor.subprocess.run', return_value=completed):
                result = monitor.probe(self.routes[0], 'download', self.rules, None, 30)
            self.assertFalse(result['ok'])
            self.assertIsNone(result['speed_mbps'])

    def test_failed_inventory_sync_never_probes_old_credentials(self):
        service = monitor.Monitor(self.store, TOKEN)
        service.inventory = self.routes
        with patch('monitor.load_inventory', side_effect=ValueError('inventory unavailable')), patch('monitor.probe') as probe:
            service.run_round()
        probe.assert_not_called()
        self.assertTrue(service.snapshot(7)['inventory_error'])

    def test_disabled_proxy_is_visible_but_never_probed_and_budget_is_not_network_failure(self):
        self.routes[1]['available'] = False
        service = monitor.Monitor(self.store, TOKEN)
        with patch('monitor.load_inventory', return_value=self.routes), patch.object(self.store, 'reserve', return_value=False), patch('monitor.probe') as probe:
            service.run_round()
        probe.assert_not_called()
        result = service.snapshot(7)['items']
        self.assertEqual(result[0]['status'], 'paused')
        self.assertIsNone(result[0]['success_rate'])
        self.assertEqual(result[0]['sample_count'], 0)
        self.assertEqual(result[1]['status'], 'disabled')

    def test_private_api_requires_token_and_deduplicates_pending_rounds(self):
        service = monitor.Monitor(self.store, TOKEN)
        server = ThreadingHTTPServer(('127.0.0.1', 0), monitor.make_handler(service))
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        url = f'http://127.0.0.1:{server.server_port}'
        try:
            with self.assertRaises(HTTPError) as error:
                urlopen(url + '/snapshot', timeout=3)
            self.assertEqual(error.exception.code, 401)
            headers = {'Authorization': 'Bearer ' + TOKEN}
            with urlopen(Request(url + '/snapshot', headers=headers), timeout=3) as response:
                self.assertEqual(response.headers['Cache-Control'], 'no-store')
                self.assertNotIn('secret-password', response.read().decode())
            with urlopen(Request(url + '/run', method='POST', headers=headers), timeout=3) as response:
                self.assertEqual(response.status, 202)
            with self.assertRaises(HTTPError) as error:
                urlopen(Request(url + '/run', method='POST', headers=headers), timeout=3)
            self.assertEqual(error.exception.code, 409)
        finally:
            server.shutdown()
            server.server_close()
            thread.join()

    @unittest.skipUnless(shutil.which('curl'), 'curl is required for the loopback fixture')
    def test_real_curl_uses_selected_http_proxy_without_public_egress(self):
        seen = {}

        class Proxy(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_CONNECT(self):
                seen['target'] = self.path
                seen['auth'] = self.headers.get('Proxy-Authorization')
                self.send_response(407)
                self.end_headers()

        server = ThreadingHTTPServer(('127.0.0.1', 0), Proxy)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            route = {**self.routes[1], 'protocol': 'http', 'host': '127.0.0.1', 'port': server.server_port}
            result = monitor.probe(route, 'download', self.rules, None, 3)
            self.assertFalse(result['ok'])
            self.assertEqual(seen['target'], 'speed.cloudflare.com:443')
            self.assertEqual(seen['auth'], 'Basic ' + base64.b64encode(b'secret-user:secret-password').decode())
        finally:
            server.shutdown()
            server.server_close()
            thread.join()


@unittest.skipUnless(os.environ.get('NETWORK_STATUS_TEST_PG') == '1', 'Actions PostgreSQL fixture only')
class InventoryIntegrationTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        sql = '''
            CREATE TABLE proxies(id BIGINT PRIMARY KEY,name TEXT,protocol TEXT,host TEXT,port INTEGER,username TEXT,password TEXT,status TEXT,expires_at TIMESTAMPTZ,deleted_at TIMESTAMPTZ);
            CREATE TABLE accounts(id BIGINT PRIMARY KEY,name TEXT,proxy_id BIGINT,deleted_at TIMESTAMPTZ,credentials JSONB);
            INSERT INTO proxies VALUES
              (1,'Shared','http','proxy.invalid',8080,'user','password','active',NULL,NULL),
              (2,'Unused','http','unused.invalid',8080,'user','password','active',NULL,NULL),
              (3,'Expired','http','expired.invalid',8080,'user','password','active',NOW()-INTERVAL '1 day',NULL),
              (4,'Inactive','http','inactive.invalid',8080,'user','password','inactive',NULL,NULL);
            INSERT INTO accounts VALUES (1,'Direct',NULL,NULL,'{"token":"must-not-read"}'),(2,'A',1,NULL,'{}'),(3,'B',1,NULL,'{}'),(4,'Expired account',3,NULL,'{}'),(5,'Inactive account',4,NULL,'{}'),(6,'Deleted account',2,NOW(),'{}');
        '''
        subprocess.run(['psql', '-X', '-v', 'ON_ERROR_STOP=1', '-c', sql], check=True, capture_output=True)
        subprocess.run(['psql', '-X', '-v', 'ON_ERROR_STOP=1', '-f', str(Path(__file__).with_name('inventory.sql'))], check=True, capture_output=True)
        subprocess.run(['psql', '-X', '-v', 'ON_ERROR_STOP=1', '-c', "ALTER ROLE sub2api_network_reader PASSWORD 'ci-network-reader'"], check=True, capture_output=True)

    def test_view_covers_all_bound_proxies_and_reader_cannot_read_tokens_or_write(self):
        env = {**os.environ, 'PGUSER': 'sub2api_network_reader', 'PGPASSWORD': 'ci-network-reader'}
        with patch.dict(os.environ, env):
            rows = monitor.load_inventory(TOKEN)
        self.assertEqual([r['id'] for r in rows], [0, 1, 3, 4])
        self.assertEqual([a['name'] for a in rows[1]['accounts']], ['A', 'B'])
        self.assertEqual(rows[2]['unavailable_reason'], 'expired')
        self.assertEqual(rows[3]['unavailable_reason'], 'inactive')
        self.assertNotIn('must-not-read', json.dumps(rows))
        for query in ('SELECT credentials FROM accounts', "UPDATE network_status.inventory SET name='changed'"):
            result = subprocess.run(['psql', '-X', '-v', 'ON_ERROR_STOP=1', '-c', query], env=env, capture_output=True)
            self.assertNotEqual(result.returncode, 0)


if __name__ == '__main__':
    unittest.main()
