#!/usr/bin/env python3
"""Independent Cloudflare monitor. Python stdlib + curl + a read-only psql view.

No Sub2API imports, account tokens, database writes, or proxy fallback.
The HTTP API is private; Sub2API authenticates all browser access.
"""

import argparse
from contextlib import contextmanager
import datetime
import hashlib
import hmac
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import math
import os
from pathlib import Path
import signal
import sqlite3
import statistics
import subprocess
import tempfile
import threading
import time
from urllib.parse import parse_qs, urlsplit


DEFAULT_RULES = {
    'enabled': False,
    'interval_seconds': 1800,
    'download_bytes': 1000000,
    'upload_bytes': 1000000,
    'repetitions': 3,
    'connect_timeout_seconds': 10,
    'request_timeout_seconds': 30,
    'round_timeout_seconds': 600,
    'daily_budget_bytes': 500000000,
    'download_min_mbps': 10,
    'upload_min_mbps': 3,
    'min_success_ratio': 1,
    'address_family': 'ipv4',
    'http_version': 'http1.1',
}
INVENTORY_SQL = "SELECT COALESCE(json_agg(row_to_json(r) ORDER BY r.id), '[]'::json) FROM network_status.inventory r"


def validate_rules(value):
    if not isinstance(value, dict) or set(value) != set(DEFAULT_RULES):
        raise ValueError('Supply exactly the documented rule fields')
    result = dict(value)
    if type(value['enabled']) is not bool:
        raise ValueError('enabled must be boolean')
    bounds = {
        'interval_seconds': (300, 86400),
        'download_bytes': (1000, 100000000),
        'upload_bytes': (1000, 100000000),
        'repetitions': (1, 5),
        'connect_timeout_seconds': (1, 60),
        'request_timeout_seconds': (1, 120),
        'round_timeout_seconds': (30, 3600),
        'daily_budget_bytes': (1000000, 10000000000),
    }
    for key, (low, high) in bounds.items():
        if type(value[key]) is not int or not low <= value[key] <= high:
            raise ValueError(f'{key} must be an integer between {low} and {high}')
    for key, high in [('download_min_mbps', 100000), ('upload_min_mbps', 100000), ('min_success_ratio', 1)]:
        number = value[key]
        if type(number) not in (int, float) or not math.isfinite(number) or not 0 <= number <= high:
            raise ValueError(f'Invalid {key}')
    if value['connect_timeout_seconds'] > value['request_timeout_seconds']:
        raise ValueError('Connection timeout cannot exceed request timeout')
    if value['address_family'] not in ('ipv4', 'ipv6', 'auto'):
        raise ValueError('Invalid address_family')
    if value['http_version'] not in ('http1.1', 'http2'):
        raise ValueError('Invalid http_version')
    return result


def version(rules):
    return hashlib.sha256(json.dumps(rules, sort_keys=True).encode()).hexdigest()[:16]


def iso(timestamp):
    return datetime.datetime.fromtimestamp(timestamp, datetime.timezone.utc).isoformat() if timestamp is not None else None


def day_key(now):
    return datetime.datetime.fromtimestamp(now, datetime.timezone.utc).date().isoformat()


def number(value, default=0):
    return value if type(value) in (float, int) and math.isfinite(value) and value >= 0 else default


def public_route(route):
    # Never serialize the inventory object: it includes proxy credentials.
    return {key: route[key] for key in ('id', 'name', 'protocol', 'accounts', 'available', 'unavailable_reason', 'revision')}


def normalize_inventory(rows, token):
    if not isinstance(rows, list) or not rows or len(rows) > 10000:
        raise ValueError('Invalid inventory')
    result, seen = [], set()
    for source in rows:
        route = dict(source)
        route_id = route.get('id')
        if type(route_id) is not int or route_id < 0 or route_id in seen:
            raise ValueError('Invalid inventory identity')
        seen.add(route_id)
        if not isinstance(route.get('name'), str) or not isinstance(route.get('accounts'), list):
            raise ValueError('Invalid inventory metadata')
        for account in route['accounts']:
            if not isinstance(account, dict) or type(account.get('id')) is not int or not isinstance(account.get('name'), str):
                raise ValueError('Invalid account relationship')
        route['accounts'] = [{'id': a['id'], 'name': a['name']} for a in route['accounts']]
        reason = ''
        if route_id == 0:
            route['protocol'] = 'direct'
        else:
            if route.get('status') != 'active':
                reason = 'inactive'
            if route.get('expires_at') is not None and number(route['expires_at']) <= time.time():
                reason = 'expired'
            if route.get('deleted'):
                reason = 'deleted'
            if route.get('protocol') not in ('http', 'https', 'socks5', 'socks5h'):
                reason = 'invalid_config'
            if type(route.get('port')) is not int or not 1 <= route['port'] <= 65535:
                reason = 'invalid_config'
            for key in ('host', 'username', 'password'):
                value = route.get(key) or ''
                if not isinstance(value, str) or any(ord(c) < 32 or ord(c) == 127 for c in value):
                    reason = 'invalid_config'
                route[key] = value
            if not route['host'] or any(c in route['host'] for c in '/@?#\\ "'):
                reason = 'invalid_config'
        route['available'], route['unavailable_reason'] = not reason, reason
        # HMAC prevents an exported revision from becoming a password oracle.
        connection = {k: route.get(k) for k in ('id', 'protocol', 'host', 'port', 'username', 'password', 'status', 'expires_at', 'deleted')}
        route['revision'] = hmac.new(token.encode(), json.dumps(connection, sort_keys=True).encode(), hashlib.sha256).hexdigest()[:24]
        result.append(route)
    if 0 not in seen:
        raise ValueError('Inventory must include the direct route')
    return sorted(result, key=lambda r: r['id'])


def load_inventory(token):
    env = dict(os.environ)
    env['PGOPTIONS'] = '-c default_transaction_read_only=on -c statement_timeout=5000'
    try:
        output = subprocess.run(
            ['psql', '-X', '-At', '--no-password', '-v', 'ON_ERROR_STOP=1', '-c', INVENTORY_SQL],
            env=env, capture_output=True, text=True, timeout=10, check=True,
        ).stdout
        if len(output) > 8 * 1024 * 1024:
            raise ValueError('Inventory too large')
        return normalize_inventory(json.loads(output), token)
    except (OSError, subprocess.SubprocessError, ValueError, TypeError, KeyError):
        # psql errors can contain connection information. Never expose them.
        raise ValueError('Inventory unavailable; check the read-only database connection and inventory view') from None


def config_quote(value):
    return '"' + str(value).replace('\\', '\\\\').replace('"', '\\"') + '"'


def curl_config(route):
    if route['id'] == 0:
        return 'proxy = ""\nnoproxy = "*"\n'
    protocol = 'socks5h' if route['protocol'] == 'socks5' else route['protocol']
    host = route['host']
    if ':' in host and not host.startswith('['):
        host = '[' + host + ']'
    endpoint = f"{protocol}://{host}:{route['port']}"
    config = 'proxy = ' + config_quote(endpoint) + '\nnoproxy = ""\n'
    if route['username'] or route['password']:
        config += 'proxy-user = ' + config_quote(route['username'] + ':' + route['password']) + '\n'
    return config


def probe(route, kind, rules, payload, remaining):
    size = rules[kind + '_bytes']
    timeout = min(rules['request_timeout_seconds'], remaining)
    started = time.time()
    command = [
        'curl', '-q', '--config', '-', '--silent', '--show-error',
        '--proto', '=https', '--connect-timeout', str(min(rules['connect_timeout_seconds'], timeout)),
        '--max-time', str(timeout), '--output', os.devnull, '--write-out', '%{json}',
        '--max-filesize', str(size if kind == 'download' else 1000000), '--' + rules['http_version'],
    ]
    if rules['address_family'] != 'auto':
        command.append('-4' if rules['address_family'] == 'ipv4' else '-6')
    if kind == 'download':
        command.append(f'https://speed.cloudflare.com/__down?bytes={size}')
    else:
        command += ['-H', 'Expect:', '-H', 'Content-Type: application/octet-stream',
                    '--request', 'POST', '--upload-file', str(payload), 'https://speed.cloudflare.com/__up']
    raw, code = {}, 1
    try:
        # Credentials use stdin, never argv, logs, result files or shell commands.
        completed = subprocess.run(command, input=curl_config(route), capture_output=True, text=True, timeout=timeout + 3)
        code = completed.returncode
        raw = json.loads(completed.stdout)
        if not isinstance(raw, dict):
            raw = {}
    except subprocess.TimeoutExpired:
        code = 28
    except (OSError, ValueError):
        pass
    http_code = int(number(raw.get('http_code')))
    actual = number(raw.get('size_' + kind))
    ok = code == 0 and http_code == 200 and actual == size
    error = ''
    if not ok:
        error = {5: 'dns', 6: 'dns', 7: 'connect', 28: 'timeout', 35: 'tls', 60: 'tls', 97: 'connect'}.get(code, 'transport')
        if code == 0:
            error = 'http' if http_code != 200 else 'size'
    duration = number(raw.get('time_total'), time.time() - started)
    # Payload / total request duration is intentionally an end-to-end rate;
    # it includes connection/TLS/response waiting and is NOT peak bandwidth.
    return {
        'checked_at': iso(started), 'kind': kind, 'requested_bytes': size,
        'transferred_bytes': actual, 'ok': ok, 'error': error,
        'http_code': http_code, 'curl_exit': code,
        'speed_mbps': round(actual * 8 / duration / 1000000, 3) if ok and duration > 0 else None,
        'duration_ms': round(duration * 1000, 1),
        'connect_ms': round(number(raw.get('time_connect')) * 1000, 1),
        'tls_ms': round(number(raw.get('time_appconnect')) * 1000, 1),
        'ttfb_ms': round(number(raw.get('time_starttransfer')) * 1000, 1),
    }


class Store:
    def __init__(self, path, initial_rules=None):
        self.path = str(path)
        Path(path).parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        with self.connect() as db:
            db.execute('PRAGMA journal_mode=WAL')
            db.executescript('''
                CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
                CREATE TABLE IF NOT EXISTS routes (id INTEGER PRIMARY KEY, revision TEXT NOT NULL, metadata TEXT NOT NULL);
                CREATE TABLE IF NOT EXISTS runs (
                    id INTEGER PRIMARY KEY, route_id INTEGER NOT NULL, revision TEXT NOT NULL,
                    checked_at REAL NOT NULL, status TEXT NOT NULL,
                    download_mbps REAL, upload_mbps REAL, latency_ms REAL,
                    successes INTEGER NOT NULL, total INTEGER NOT NULL, rule_version TEXT NOT NULL,
                    details TEXT NOT NULL
                );
                CREATE INDEX IF NOT EXISTS runs_route_time ON runs(route_id, revision, checked_at);
                CREATE TABLE IF NOT EXISTS budgets (day TEXT PRIMARY KEY, bytes INTEGER NOT NULL);
            ''')
            db.execute('INSERT OR IGNORE INTO settings VALUES (?, ?)', ('rules', json.dumps(validate_rules(initial_rules or DEFAULT_RULES))))
        os.chmod(self.path, 0o600)

    @contextmanager
    def connect(self):
        db = sqlite3.connect(self.path, timeout=10)
        db.row_factory = sqlite3.Row
        try:
            with db:
                yield db
        finally:
            db.close()

    def rules(self):
        with self.connect() as db:
            return json.loads(db.execute("SELECT value FROM settings WHERE key='rules'").fetchone()[0])

    def save_rules(self, value):
        rules = validate_rules(value)
        with self.connect() as db:
            db.execute("UPDATE settings SET value=? WHERE key='rules'", (json.dumps(rules),))
        return rules

    def inventory(self, routes):
        with self.connect() as db:
            db.execute('DELETE FROM routes')
            db.executemany('INSERT INTO routes VALUES (?, ?, ?)', [(r['id'], r['revision'], json.dumps(public_route(r))) for r in routes])

    def reserve(self, amount, limit, now):
        with self.connect() as db:
            db.execute('BEGIN IMMEDIATE')
            key = day_key(now)
            db.execute('INSERT OR IGNORE INTO budgets VALUES (?, 0)', (key,))
            used = db.execute('SELECT bytes FROM budgets WHERE day=?', (key,)).fetchone()[0]
            if used + amount > limit:
                return False
            db.execute('UPDATE budgets SET bytes=bytes+? WHERE day=?', (amount, key))
            return True

    def record(self, route, rules, samples, skipped=''):
        good = [s for s in samples if s['ok']]
        down = [s['speed_mbps'] for s in good if s['kind'] == 'download' and s['speed_mbps'] is not None]
        up = [s['speed_mbps'] for s in good if s['kind'] == 'upload' and s['speed_mbps'] is not None]
        download, upload = statistics.median(down) if down else None, statistics.median(up) if up else None
        expected = rules['repetitions'] * 2
        status = 'failed'
        if skipped and not samples:
            status = 'paused'
        elif good:
            status = 'normal'
            if (skipped or len(good) / expected < rules['min_success_ratio'] or download is None or upload is None
                    or download < rules['download_min_mbps'] or upload < rules['upload_min_mbps']):
                status = 'degraded'
        details = {'route': public_route(route), 'rules': rules, 'samples': samples, 'skipped': skipped}
        latency = statistics.median([s['ttfb_ms'] for s in good]) if good else None
        with self.connect() as db:
            db.execute('INSERT INTO runs(route_id,revision,checked_at,status,download_mbps,upload_mbps,latency_ms,successes,total,rule_version,details) VALUES(?,?,?,?,?,?,?,?,?,?,?)',
                       (route['id'], route['revision'], time.time(), status, download, upload, latency, len(good), len(samples), version(rules), json.dumps(details)))

    def prune(self, now):
        with self.connect() as db:
            db.execute('DELETE FROM runs WHERE checked_at<?', (now - 31 * 86400,))
            db.execute('DELETE FROM budgets WHERE day<?', (day_key(now - 32 * 86400),))

    def snapshot(self, days, now):
        rules = self.rules()
        since = now - days * 86400
        width = days * 86400 / 28
        items = []
        with self.connect() as db:
            for row in db.execute('SELECT * FROM routes ORDER BY id'):
                route = json.loads(row['metadata'])
                latest = db.execute('SELECT * FROM runs WHERE route_id=? AND revision=? ORDER BY id DESC LIMIT 1', (row['id'], row['revision'])).fetchone()
                stats = db.execute('SELECT SUM(successes), SUM(total) FROM runs WHERE route_id=? AND revision=? AND checked_at>=?', (row['id'], row['revision'], since)).fetchone()
                buckets = db.execute('''SELECT CAST((checked_at-?)/? AS INTEGER) AS bucket,
                    MAX(CASE status WHEN 'failed' THEN 3 WHEN 'degraded' THEN 2 WHEN 'paused' THEN 1 ELSE 0 END) AS severity,
                    COUNT(*) AS count FROM runs WHERE route_id=? AND revision=? AND checked_at>=? AND checked_at<=? GROUP BY bucket''',
                                     (since, width, row['id'], row['revision'], since, now)).fetchall()
                timeline = [{'status': 'untested', 'count': 0, 'from': iso(since + i * width), 'to': iso(since + (i + 1) * width)} for i in range(28)]
                for bucket in buckets:
                    index = min(27, max(0, bucket['bucket']))
                    timeline[index].update(status=['normal', 'paused', 'degraded', 'failed'][bucket['severity']], count=bucket['count'])
                status = latest['status'] if latest else 'untested'
                if latest and (now - latest['checked_at'] > max(rules['interval_seconds'] * 2, 600) or latest['rule_version'] != version(rules)):
                    status = 'stale'
                if not route['available']:
                    status = 'disabled'
                route.update(
                    status=status, checked_at=iso(latest['checked_at']) if latest else None,
                    download_mbps=latest['download_mbps'] if latest else None,
                    upload_mbps=latest['upload_mbps'] if latest else None,
                    latency_ms=latest['latency_ms'] if latest else None,
                    success_rate=round(stats[0] / stats[1] * 100, 2) if stats[1] else None,
                    sample_count=stats[1] or 0, timeline=timeline,
                )
                items.append(route)
            budget = db.execute('SELECT bytes FROM budgets WHERE day=?', (day_key(now),)).fetchone()
        return {'items': items, 'rules': rules, 'rule_version': version(rules), 'budget_used_bytes': budget[0] if budget else 0}

    def history(self, route_id, days, now):
        with self.connect() as db:
            route = db.execute('SELECT metadata,revision FROM routes WHERE id=?', (route_id,)).fetchone()
            if not route:
                return None
            rows = db.execute('SELECT id,checked_at,status,rule_version,details FROM runs WHERE route_id=? AND revision=? AND checked_at>=? ORDER BY id DESC LIMIT 20',
                              (route_id, route['revision'], now - days * 86400)).fetchall()
        return {'route': json.loads(route['metadata']), 'runs': [dict(id=r['id'], checked_at=iso(r['checked_at']), status=r['status'], rule_version=r['rule_version'], **json.loads(r['details'])) for r in rows]}


class Monitor:
    def __init__(self, store, token):
        self.store, self.token = store, token
        self.lock = threading.RLock()
        self.wake = threading.Event()
        self.stop = threading.Event()
        self.inventory = []
        self.running = False
        self.pending = False
        self.inventory_error = ''
        self.worker_error = ''
        self.last_sync = None
        self.last_started = None
        self.next_run = time.time() if store.rules()['enabled'] else None

    def request_run(self):
        with self.lock:
            if self.running or self.pending:
                return False, 'A detection round is already queued or running'
            if self.last_started is not None and time.time() - self.last_started < 60:
                return False, 'Wait at least 60 seconds between detection rounds'
            self.pending = True
            self.wake.set()
            return True, ''

    def save_rules(self, rules):
        with self.lock:
            result = self.store.save_rules(rules)
            self.next_run = time.time() + result['interval_seconds'] if result['enabled'] else None
            self.wake.set()
            return result

    def refresh_inventory(self):
        try:
            inventory = load_inventory(self.token)
            self.store.inventory(inventory)
            with self.lock:
                self.inventory, self.inventory_error, self.last_sync = inventory, '', time.time()
            return True
        except ValueError as error:
            with self.lock:
                self.inventory_error = str(error)
            return False

    def snapshot(self, days):
        with self.lock:
            result = self.store.snapshot(days, time.time())
            result.update(running=self.running or self.pending, next_run_at=iso(self.next_run), last_sync_at=iso(self.last_sync), inventory_error=self.inventory_error, worker_error=self.worker_error)
            return result

    def run_round(self):
        # Re-read inventory before every round. A failed sync must never probe
        # old credentials or silently substitute a direct connection.
        if not self.refresh_inventory():
            return
        rules = self.store.rules()
        deadline = time.monotonic() + rules['round_timeout_seconds']
        with tempfile.TemporaryDirectory(prefix='network-status-') as temporary:
            payload = Path(temporary) / 'upload.bin'
            with payload.open('wb') as stream:
                remaining = rules['upload_bytes']
                while remaining:
                    chunk = os.urandom(min(65536, remaining))
                    stream.write(chunk)
                    remaining -= len(chunk)
            for route in self.inventory:
                if not route['available'] or self.stop.is_set():
                    continue
                samples, skipped = [], ''
                for _ in range(rules['repetitions']):
                    for kind in ('download', 'upload'):
                        remaining = deadline - time.monotonic()
                        if remaining < 1 or self.stop.is_set():
                            skipped = 'deadline'
                            break
                        if not self.store.reserve(rules[kind + '_bytes'], rules['daily_budget_bytes'], time.time()):
                            skipped = 'budget'
                            break
                        samples.append(probe(route, kind, rules, payload, remaining))
                    if skipped:
                        break
                self.store.record(route, rules, samples, skipped)

    def loop(self):
        next_sync = 0
        next_prune = 0
        while not self.stop.is_set():
            self.wake.clear()
            try:
                now = time.time()
                if now >= next_prune:
                    self.store.prune(now)
                    next_prune = now + 86400
                if now >= next_sync:
                    self.refresh_inventory()
                    next_sync = now + 60
                with self.lock:
                    due = self.pending or (self.next_run is not None and now >= self.next_run)
                    if due:
                        self.pending, self.running, self.last_started = False, True, now
                if due:
                    try:
                        self.run_round()
                        self.worker_error = ''
                    except Exception:
                        # Keep the scheduler alive without recording secrets.
                        self.worker_error = 'Detection round failed; inspect service dependencies and storage'
                    finally:
                        with self.lock:
                            self.running = False
                            rules = self.store.rules()
                            self.next_run = time.time() + rules['interval_seconds'] if rules['enabled'] else None
                        self.store.prune(time.time())
            except Exception:
                with self.lock:
                    self.worker_error = 'Monitor storage or scheduler unavailable'
            self.wake.wait(1)


def make_handler(monitor):
    class Handler(BaseHTTPRequestHandler):
        server_version = 'NetworkStatus'

        def log_message(self, *args):
            pass  # Neither bearer tokens nor proxy errors belong in access logs.

        def send_json(self, status, data=None, message='success'):
            payload = json.dumps({'code': 0 if status < 400 else status, 'message': message, 'data': data}, allow_nan=False).encode()
            self.send_response(status)
            self.send_header('Content-Type', 'application/json; charset=utf-8')
            self.send_header('Cache-Control', 'no-store')
            self.send_header('Content-Length', str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

        def dispatch(self):
            self.connection.settimeout(10)
            expected = 'Bearer ' + monitor.token
            if not hmac.compare_digest(self.headers.get('Authorization', '').encode(), expected.encode()):
                self.send_json(401, message='Unauthorized')
                return
            parsed = urlsplit(self.path)
            query = parse_qs(parsed.query)
            try:
                days = int(query.get('days', ['7'])[0])
                if days not in (1, 7, 30):
                    raise ValueError('days must be 1, 7 or 30')
                if self.command == 'GET' and parsed.path == '/snapshot':
                    self.send_json(200, monitor.snapshot(days))
                elif self.command == 'GET' and parsed.path == '/rules':
                    self.send_json(200, monitor.store.rules())
                elif self.command == 'GET' and parsed.path == '/history':
                    route_id = int(query.get('id', ['-1'])[0])
                    if not 0 <= route_id <= 9223372036854775807:
                        raise ValueError('Invalid route ID')
                    result = monitor.store.history(route_id, days, time.time())
                    self.send_json(200 if result else 404, result, 'success' if result else 'Route not found')
                elif self.command == 'PUT' and parsed.path == '/rules':
                    length = int(self.headers.get('Content-Length', '0'))
                    if not 0 < length <= 16384:
                        raise ValueError('Invalid request length')
                    data = json.loads(self.rfile.read(length))
                    self.send_json(200, monitor.save_rules(data))
                elif self.command == 'POST' and parsed.path == '/run':
                    queued, error = monitor.request_run()
                    self.send_json(202 if queued else 409, {'queued': queued}, error or 'accepted')
                else:
                    self.send_json(404, message='Not found')
            except (ValueError, TypeError, OverflowError):
                self.send_json(400, message='Invalid request or rules; check documented field types and bounds')
            except Exception:
                self.send_json(503, message='Monitor storage unavailable')

        do_GET = dispatch
        do_PUT = dispatch
        do_POST = dispatch

    return Handler


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--host', default='127.0.0.1')
    parser.add_argument('--port', type=int, default=8091)
    parser.add_argument('--database', type=Path, default=Path('/var/lib/sub2api-network/status.sqlite3'))
    parser.add_argument('--rules', type=Path, help='Initial rules for a new state database')
    args = parser.parse_args()
    token = os.environ.get('NETWORK_STATUS_TOKEN', '').strip()
    if len(token) < 32 or not token.isascii():
        parser.error('NETWORK_STATUS_TOKEN must contain at least 32 ASCII characters')
    os.umask(0o077)
    initial_rules = json.loads(args.rules.read_text()) if args.rules else None
    monitor = Monitor(Store(args.database, initial_rules), token)
    server = ThreadingHTTPServer((args.host, args.port), make_handler(monitor))
    server.daemon_threads = True
    worker = threading.Thread(target=monitor.loop, name='network-status-worker', daemon=True)
    worker.start()

    def shutdown(*_):
        monitor.stop.set()
        monitor.wake.set()
        threading.Thread(target=server.shutdown, daemon=True).start()

    signal.signal(signal.SIGTERM, shutdown)
    signal.signal(signal.SIGINT, shutdown)
    try:
        server.serve_forever()
    finally:
        monitor.stop.set()
        monitor.wake.set()
        worker.join(timeout=125)
        server.server_close()


if __name__ == '__main__':
    main()
