"""Run only in GitHub Actions: isolated real-Nginx ingress verification."""
import http.client
import http.server
import json
from pathlib import Path
import re
import shutil
import ssl
import subprocess
import tempfile
import threading
import time

ROOT = Path(__file__).resolve().parents[3]


class Upstream(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.respond()

    def do_POST(self):
        self.rfile.read(int(self.headers.get('Content-Length', '0')))
        self.respond()

    def respond(self):
        body = json.dumps({
            'path': self.path,
            'real_ip': self.headers.get('X-Real-IP'),
            'forwarded_for': self.headers.get('X-Forwarded-For'),
            'upgrade': self.headers.get('Upgrade'),
            'connection': self.headers.get('Connection'),
        }).encode()
        self.send_response(401 if self.path.startswith('/api/v1/auth/login') else 200)
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


def main():
    nginx = shutil.which('nginx') or '/usr/sbin/nginx'
    backend = http.server.ThreadingHTTPServer(('127.0.0.1', 18081), Upstream)
    threading.Thread(target=backend.serve_forever, daemon=True).start()
    context = ssl._create_unverified_context()
    with tempfile.TemporaryDirectory(prefix='sub2api-nginx-') as directory:
        fixture = Path(directory)
        subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
                        '-keyout', str(fixture / 'key.pem'), '-out', str(fixture / 'cert.pem'),
                        '-days', '1', '-subj', '/CN=idontknow.site'],
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        site = (ROOT / 'ops/config/nginx/sub2api.conf').read_text()
        site = site.replace('listen 80;', 'listen 127.0.0.1:18080;')
        site = site.replace('listen 443 ssl;', 'listen 127.0.0.1:18443 ssl;')
        site = site.replace('http://127.0.0.1:8080', 'http://127.0.0.1:18081')
        site = re.sub(r'ssl_certificate .*?;', f'ssl_certificate {fixture}/cert.pem;', site)
        site = re.sub(r'ssl_certificate_key .*?;', f'ssl_certificate_key {fixture}/key.pem;', site)
        site = site.replace('/var/log/nginx/', f'{fixture}/')
        configuration = fixture / 'nginx.conf'
        configuration.write_text(
            f'pid {fixture}/nginx.pid;\nerror_log {fixture}/main.error.log;\n'
            f'events {{ worker_connections 128; }}\nhttp {{\n{site}\n}}\n')
        command = [nginx, '-p', str(fixture), '-c', str(configuration)]
        subprocess.run(command + ['-t'], check=True)
        process = subprocess.Popen(command + ['-g', 'daemon off;'])

        def request(path, expected, method='GET', headers=None, source='127.0.0.1'):
            connection = http.client.HTTPSConnection(
                '127.0.0.1', 18443, context=context, timeout=3,
                source_address=(source, 0))
            connection.request(method, path, body=b'' if method == 'POST' else None,
                               headers={'Host': 'idontknow.site', **(headers or {})})
            response = connection.getresponse()
            status, body = response.status, response.read()
            connection.close()
            assert status == expected, (path, expected, status, body[:200])
            print(f'PASS {source} {method} {path}: {status}', flush=True)
            return json.loads(body) if status in (200, 401) else None

        try:
            for _ in range(40):
                if (fixture / 'nginx.pid').exists():
                    break
                if process.poll() is not None:
                    raise RuntimeError('Nginx exited during startup')
                time.sleep(0.1)
            first = request('/api/v1/auth/login', 401, 'POST')
            assert first['real_ip'] == first['forwarded_for'] == '127.0.0.1'
            for path in ['/api/v1/auth/login', '/api/v1/auth/login?retry=1',
                         '/api/v1/auth/login/2fa', '/api/v1/auth/login/',
                         '/api/v1/auth/login/2fa/']:
                request(path, 429, 'POST')
            request('/api/v1/auth/login', 429, 'POST', headers={
                'X-Real-IP': '198.51.100.1', 'X-Forwarded-For': '198.51.100.2',
                'CF-Connecting-IP': '198.51.100.3'})
            request('/api/v1/auth/login', 401, 'POST', source='127.0.0.2')
            for _ in range(4):
                for path in ['/', '/health', '/api/v1/auth/me', '/api/v1/auth/refresh',
                             '/v1/responses', '/v1/messages']:
                    request(path, 200)
            ws = request('/v1/responses', 200, headers={
                'Upgrade': 'websocket', 'Connection': 'Upgrade'})
            assert ws['upgrade'] == 'websocket' and ws['connection'].lower() == 'upgrade'
            # This is the real 3r/m interval, with a margin for integer rate precision.
            time.sleep(20.5)
            request('/api/v1/auth/login', 401, 'POST')
            request('/api/v1/auth/login', 429, 'POST')
            errors = (fixture / 'sub2api.error.log').read_text()
            assert 'limiting requests' in errors and 'sub2api_login' in errors
            print('PASS configuration syntax, no burst, shared TOTP budget, recovery, '
                  'independent IPs, spoof rejection, other routes and Upgrade forwarding')
        finally:
            process.terminate()
            process.wait(timeout=5)
            backend.shutdown()


if __name__ == '__main__':
    main()
