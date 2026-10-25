#!/usr/bin/env python3
"""Privileged deployment helpers; never print credentials or HTTP error bodies."""
import json, os, pathlib, pwd, secrets, subprocess, sys, time, urllib.request
import yaml

BASE = pathlib.Path('/home/pk/workspace/sub2api')
SECRET = pathlib.Path('/etc/sub2api-native-secrets.json')

def write_private(path, data, uid=0, gid=0):
    path = pathlib.Path(path)
    temp = path.with_name(path.name + '.new')
    fd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, 'w') as out:
        out.write(data)
    os.chown(temp, uid, gid)
    os.replace(temp, path)

def sql(statement, database='postgres'):
    result = subprocess.run(['runuser', '-u', 'postgres', '--', 'psql', '-X', '-q', '-v', 'ON_ERROR_STOP=1', '-d', database], input=statement, text=True, capture_output=True)
    if result.returncode:
        raise RuntimeError('PostgreSQL provisioning failed; server-side statement output suppressed to protect secrets')
    return result.stdout

def http(path, payload=None):
    req = urllib.request.Request('http://127.0.0.1:8080' + path, data=None if payload is None else json.dumps(payload).encode(), headers={'Content-Type':'application/json'})
    with urllib.request.urlopen(req, timeout=180) as response:
        return json.load(response)

def wait(path):
    for _ in range(120):
        try:
            result = http(path)
            return result
        except Exception:
            time.sleep(1)
    raise RuntimeError('Sub2API did not become ready within 120 seconds; inspect journalctl -u sub2api')

def main():
    mode = sys.argv[1]
    if mode == 'secrets':
        if not SECRET.exists():
            write_private(SECRET, json.dumps({name:secrets.token_hex(32) for name in ['database', 'redis', 'jwt', 'totp']}))
        target = BASE / '.operator/admin.json'
        if not target.exists():
            user = pwd.getpwnam('pk')
            write_private(target, json.dumps({'email':'admin-' + secrets.token_hex(4) + '@sub2api.local', 'password':secrets.token_urlsafe(24)}), user.pw_uid, user.pw_gid)
        return
    s = json.loads(SECRET.read_text())
    if mode == 'redis':
        path = pathlib.Path('/etc/redis/redis.conf')
        # Rewrite managed options; preserve every unrelated package option.
        options = {'bind':'127.0.0.1 ::1', 'protected-mode':'yes', 'requirepass':s['redis'], 'maxmemory':'64mb', 'maxmemory-policy':'noeviction', 'appendonly':'yes', 'appendfsync':'everysec'}
        lines = [line for line in path.read_text().splitlines() if not line.strip() or line.lstrip().startswith('#') or line.split()[0] not in options]
        lines.extend(key + ' ' + value for key,value in options.items())
        redis = pwd.getpwnam('redis')
        write_private(path, '\n'.join(lines) + '\n', redis.pw_uid, redis.pw_gid)
    elif mode == 'database':
        sql("SELECT 'CREATE ROLE sub2api LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE' WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname='sub2api')\\gexec\nALTER ROLE sub2api PASSWORD '" + s['database'] + "';\nSELECT 'CREATE DATABASE sub2api OWNER sub2api' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname='sub2api')\\gexec\n")
    elif mode == 'wizard':
        state = wait('/setup/status')
        if not state['data']['needs_setup']:
            raise RuntimeError('Setup unexpectedly already completed; refusing to replace configuration')
        admin = json.loads((BASE / '.operator/admin.json').read_text())
        payload = {'database':{'host':'127.0.0.1', 'port':5432, 'user':'sub2api', 'password':s['database'], 'dbname':'sub2api', 'sslmode':'disable'}, 'redis':{'host':'127.0.0.1', 'port':6379, 'password':s['redis'], 'db':0}, 'admin':admin, 'server':{'host':'127.0.0.1', 'port':8080, 'mode':'release'}}
        result = http('/setup/install', payload)
        if result.get('code') != 0:
            raise RuntimeError('Wizard installation rejected; response suppressed')
        print('Official setup wizard backend completed (credentials not printed).')
    elif mode == 'config':
        path = BASE / 'data/config.yaml'
        cfg = yaml.safe_load(path.read_text())
        cfg.update(run_mode='simple', timezone='Asia/Shanghai')
        cfg['server'].update(host='127.0.0.1', port=8080, mode='release', max_request_body_size=16777216)
        cfg['database'].update(max_open_conns=10, max_idle_conns=2)
        cfg['redis'].update(pool_size=16, min_idle_conns=2)
        cfg['jwt']['secret'] = s['jwt']
        cfg['totp'] = {'encryption_key':s['totp']}
        cfg.setdefault('default', {})['user_concurrency'] = 1
        cfg.setdefault('gateway', {}).update(max_body_size=16777216, text_max_body_size=16777216, max_idle_conns=16, max_idle_conns_per_host=4, max_conns_per_host=16)
        cfg.setdefault('log', {}).update(output_to_file=False, output_to_stdout=True)
        user = pwd.getpwnam('sub2api')
        write_private(path, yaml.safe_dump(cfg, sort_keys=False), user.pw_uid, user.pw_gid)
    elif mode == 'ready':
        wait('/health')
        print('Application /health passed.')
    elif mode == 'policy':
        # Apply only after default settings initialization, then restart to clear caches.
        sql("INSERT INTO settings (key,value,updated_at) VALUES ('registration_enabled','false',now()) ON CONFLICT (key) DO UPDATE SET value='false',updated_at=now(); UPDATE users SET concurrency=1 WHERE role='admin';", 'sub2api')
    else:
        raise RuntimeError('Unknown mode')

if __name__ == '__main__':
    try:
        main()
    except Exception as exc:
        # Error text from urllib and our own exceptions does not contain payloads.
        print(type(exc).__name__ + ': ' + str(exc), file=sys.stderr)
        sys.exit(1)
