#!/usr/bin/env python3
"""Check source/operations boundaries without touching installed services."""
import ast
import json
from pathlib import Path
import re
import subprocess

import yaml

ROOT = Path(__file__).resolve().parents[2]
OPS = ROOT / 'ops'


def git(*args):
    return subprocess.check_output(['git', '-C', str(ROOT), *args], text=True)


def require(condition, message):
    if not condition:
        raise SystemExit(message)


def main():
    tracked = git('ls-files', '-z').split('\0')[:-1]
    state = json.loads((OPS / 'deployed-build.json').read_text())
    require(git('rev-parse', '--is-shallow-repository').strip() == 'false',
            'Use a complete checkout (fetch-depth: 0) to verify provenance.')
    subprocess.run(['git', '-C', str(ROOT), 'merge-base', '--is-ancestor',
                    state['upstream']['commit'], 'HEAD'], check=True)
    subprocess.run(['git', '-C', str(ROOT), 'merge-base', '--is-ancestor',
                    'c104f7ec58f8618d4c220b997300ca7cc1a989ed', 'HEAD'], check=True)
    runtime_prefixes = ('bin/', 'data/', 'backups/', '.operator/', 'release/',
                        'scripts/', 'validation/logs/', 'ops/validation/logs/',
                        'ops/scripts/ubuntu-packages/', 'ops/audits/')
    for name in tracked:
        require(not name.startswith(runtime_prefixes), f'Runtime file tracked: {name}')
    ignored_examples = [
        'bin/sub2api', 'data/config.yaml', '.operator/admin.json',
        'backups/database.dump', 'release/archive.tar.gz',
        'scripts/ubuntu-packages/package.deb', 'validation/logs/sub2api.log',
        'ops/validation/logs/sub2api.log', 'ops/scripts/ubuntu-packages/package.deb',
        'ops/scripts/__pycache__/module.pyc', 'frontend/node_modules/test/index.js',
        'backend/internal/web/dist/index.html', 'ops/config/private.key',
        'ops/config/mihomo/config.local.yaml', '.env', 'ops/audits/README.md',
    ]
    for name in ignored_examples:
        result = subprocess.run(['git', '-C', str(ROOT), 'check-ignore', '--no-index',
                                 '-q', name])
        require(result.returncode == 0, f'Runtime/secret path must be ignored: {name}')
    ops_files = [ROOT / name for name in tracked if name.startswith('ops/')]
    for path in ops_files:
        require(path.exists(), f'Missing operations source: {path.relative_to(ROOT)}')
        if path.is_symlink():
            require(path.resolve().is_relative_to(OPS), f'Link outside operations: {path}')
        text = path.read_text()
        if path.suffix == '.py':
            ast.parse(text, filename=str(path))
        elif path.suffix == '.sh' or text.startswith('#!/bin/sh') or text.startswith('#!/bin/bash'):
            subprocess.run(['bash', '-n', str(path)], check=True)
        if path.suffix == '.json':
            json.loads(text)
        if path.suffix in ('.yaml', '.yml') or path.name.endswith('.yaml.example'):
            yaml.safe_load(text)
        if path.is_relative_to(OPS / 'config'):
            require(not re.search(r'-----BEGIN [^-\n]*PRIVATE KEY-----', text),
                    f'Private key in template: {path}')
            require(not re.search(r'\b(?:sk-[A-Za-z0-9_-]{16,}|gh[pousr]_[A-Za-z0-9]{20,})\b', text),
                    f'Possible real token in template: {path}')
    app = yaml.safe_load((OPS / 'config/application.yaml.example').read_text())
    proxy = yaml.safe_load((OPS / 'config/mihomo/config.yaml.example').read_text())
    placeholders = [app['database']['password'], app['redis']['password'],
                    app['jwt']['secret'], app['totp']['encryption_key']]
    for node in proxy.get('proxies', []):
        placeholders += [node['server'], node['username'], node['password']]
    require(all(isinstance(v, str) and re.fullmatch(r'\$\{[A-Z_]+\}', v)
                for v in placeholders), 'Live credentials must be explicit placeholders.')
    redis = (OPS / 'config/redis/redis.conf.example').read_text()
    require('requirepass ${REDIS_PASSWORD}' in redis, 'Redis template must redact password.')
    nginx = (OPS / 'config/nginx/sub2api.conf').read_text()
    require('proxy_set_header Upgrade $http_upgrade;' in nginx
            and 'proxy_set_header Connection $sub2api_connection_upgrade;' in nginx,
            'Nginx template lost the deployed WebSocket forwarding configuration.')
    # Check relative links in the maintained entry points (dated evidence is archival).
    docs = ['ops/README.md', 'ops/validation/README.md',
            'ops/changes/20261107-upgrade-v0.2.20/README.md',
            'ops/scripts/egress-guard/README.md']
    for name in docs:
        path = ROOT / name
        for target in re.findall(r'\[[^\]]*\]\(([^)]+)\)', path.read_text()):
            if '://' in target or target.startswith('#'):
                continue
            require((path.parent / target.split('#', 1)[0]).exists(),
                    f'Broken documentation link: {name}: {target}')
    print(f'PASS: full history, runtime exclusions, {len(ops_files)} operations files, '
          'sanitized templates, shell/Python syntax and documentation links.')


if __name__ == '__main__':
    main()
