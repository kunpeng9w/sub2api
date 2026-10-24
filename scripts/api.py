#!/usr/bin/env python3
"""Operator API tasks. Read credentials from private files; output metadata only."""
import argparse, json, os, pathlib, secrets, sys, time, urllib.error, urllib.request

BASE = pathlib.Path('/home/pk/workspace/sub2api')
PRIVATE = BASE / '.operator'
URL = 'http://127.0.0.1:8080'

def save(name, obj):
    path = PRIVATE / name
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, 'w') as out:
        json.dump(obj, out)

def request(path, data=None, token=None, method=None):
    headers = {'Content-Type':'application/json'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    req = urllib.request.Request(URL + path, data=None if data is None else json.dumps(data).encode(), headers=headers, method=method)
    with urllib.request.urlopen(req, timeout=180) as response:
        result = json.load(response)
    if result.get('code', 0) != 0:
        raise RuntimeError('API operation failed: ' + path)
    return result.get('data', result)

def login(credentials):
    return request('/api/v1/auth/login', {k:credentials[k] for k in ('email','password')})['access_token']

def bootstrap():
    admin = json.loads((PRIVATE / 'admin.json').read_text())
    token = login(admin)
    if request('/api/v1/admin/compliance', token=token)['required']:
        print('PENDING: owner must read and complete the project acknowledgement in the Web UI. No agreement was accepted automatically.')
        check()
        return
    groups = request('/api/v1/groups/available', token=token)
    group = next((g for g in groups if g.get('platform') == 'openai' and g.get('status') == 'active'), None)
    if group is None:
        raise RuntimeError('No active OpenAI default group exists')
    path = PRIVATE / 'test-user.json'
    user = json.loads(path.read_text()) if path.exists() else {'email':'phase1-' + secrets.token_hex(4) + '@sub2api.local', 'password':secrets.token_urlsafe(24)}
    if 'id' not in user:
        result = request('/api/v1/admin/users', {**user, 'role':'user', 'concurrency':1, 'allowed_groups':[group['id']], 'notes':'Phase 1 single-concurrency acceptance user'}, token)
        user.update(id=result['id'], group_id=group['id'])
        save('test-user.json', user)
    request('/api/v1/admin/users/' + str(user['id']), {'concurrency':1, 'allowed_groups':[group['id']]}, token, 'PUT')
    user_token = login(user)
    if not (PRIVATE / 'test-key.json').exists():
        key = request('/api/v1/keys', {'name':'phase1-single-account', 'group_id':group['id']}, user_token)
        save('test-key.json', key)
    check()
    print('Test user and downstream key ready. OAuth account authorization is pending.')

def check():
    token = login(json.loads((PRIVATE / 'admin.json').read_text()))
    profile = request('/api/v1/user/profile', token=token)
    assert profile['concurrency'] == 1, 'Admin concurrency must be 1'
    gated = request('/api/v1/admin/compliance', token=token)['required']
    if not gated:
        settings = request('/api/v1/admin/settings', token=token)
        assert settings['registration_enabled'] is False, 'Public registration must be disabled'
    if (PRIVATE / 'test-user.json').exists():
        user = json.loads((PRIVATE / 'test-user.json').read_text())
        profile = request('/api/v1/user/profile', token=login(user))
        assert profile['concurrency'] == 1
    try:
        request('/v1/models', token='invalid-phase1-key')
    except urllib.error.HTTPError as exc:
        assert exc.code in (401, 403), 'Invalid key was not rejected by authentication'
    else:
        raise RuntimeError('Invalid API key unexpectedly accepted')
    print('PASS: admin login, concurrency=1, invalid key rejected. Project acknowledgement pending=' + str(gated))

def inventory():
    token = login(json.loads((PRIVATE / 'admin.json').read_text()))
    result = request('/api/v1/admin/accounts?platform=openai&page_size=20', token=token)
    items = result.get('items', []) if isinstance(result,dict) else result
    print(json.dumps([{'id':x['id'],'platform':x.get('platform'),'type':x.get('type'),'status':x.get('status'),'concurrency':x.get('concurrency'),'group_ids':x.get('group_ids')} for x in items]))

def call(model, stream=False, cancel=False):
    key = json.loads((PRIVATE / 'test-key.json').read_text())['key']
    text = 'Count slowly from one to fifty.' if cancel else 'Reply with exactly: OK'
    payload = {'model':model, 'instructions':'You are a concise assistant.', 'input':[{'role':'user','content':[{'type':'input_text','text':text}]}], 'stream':stream, 'store':False}
    req = urllib.request.Request(URL + '/v1/responses', data=json.dumps(payload).encode(), headers={'Content-Type':'application/json','Authorization':'Bearer ' + key})
    start = time.monotonic()
    with urllib.request.urlopen(req, timeout=180) as response:
        if stream:
            saw_delta = False
            completed = False
            for line in response:
                if line.startswith(b'data: '):
                    raw = line[6:].strip()
                    if raw == b'[DONE]':
                        continue
                    event = json.loads(raw)
                    if event.get('type') == 'error' or event.get('type') == 'response.failed':
                        raise RuntimeError('Upstream returned a streaming failure')
                    if event.get('type') == 'response.output_text.delta':
                        saw_delta = True
                        if cancel:
                            break
                    if event.get('type') == 'response.completed':
                        completed = True
            assert saw_delta and (completed or cancel), 'Stream did not produce successful text events'
        else:
            result = json.load(response)
            assert result.get('status') == 'completed', 'Response not completed'
            assert any(item.get('content') for item in result.get('output',[])), 'No output content'
    print(json.dumps({'test':'cancel' if cancel else 'stream' if stream else 'ordinary','passed':True,'seconds':round(time.monotonic()-start,2)}))

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('mode', choices=['bootstrap','check','inventory','accept'])
    parser.add_argument('--model')
    args = parser.parse_args()
    if args.mode == 'bootstrap': bootstrap()
    elif args.mode == 'check': check()
    elif args.mode == 'inventory': inventory()
    else:
        if not args.model: parser.error('--model is required; choose an account-supported model')
        for _ in range(10): call(args.model)
        call(args.model, stream=True)
        call(args.model, stream=True, cancel=True)
        call(args.model)

if __name__ == '__main__':
    try: main()
    except urllib.error.HTTPError as exc:
        print('API HTTP status ' + str(exc.code) + '; body suppressed to protect credentials.', file=sys.stderr)
        sys.exit(1)
    except Exception as exc:
        print(type(exc).__name__ + ': ' + str(exc), file=sys.stderr)
        sys.exit(1)
