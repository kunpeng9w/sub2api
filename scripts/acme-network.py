#!/usr/bin/python3
"""Resolve ACME on eth0 and bypass the TUN only for ACME service addresses."""
import ipaddress,json,pathlib,re,subprocess,time

def run(*cmd):
    return subprocess.check_output(cmd,text=True,timeout=20).strip()

def ip(*args):
    return json.loads(run('/usr/sbin/ip','-j','-N',*args))

link='eth0'
domains=run('resolvectl','domain',link).split(':',1)[1].split()
if '~letsencrypt.org' not in domains:
    subprocess.run(['resolvectl','domain',link,*domains,'~letsencrypt.org'],check=True)
addresses=set()
for host in ('acme-v02.api.letsencrypt.org','acme-staging-v02.api.letsencrypt.org'):
    for attempt in range(3):
        try:
            raw=run('resolvectl','query','--cache=no','-4','-i',link,host)
            break
        except (subprocess.CalledProcessError,subprocess.TimeoutExpired):
            if attempt==2: raise
            time.sleep(1)
    addresses.update(str(ipaddress.IPv4Address(x)) for x in re.findall(r'\b(?:\d{1,3}\.){3}\d{1,3}\b',raw))
assert addresses,'No IPv4 ACME addresses resolved'
default=next(r for r in ip('route','show','table','main','default') if r.get('dev')==link)
state=pathlib.Path('/var/lib/sub2api-acme-network/routes.json')
state.parent.mkdir(mode=0o700,parents=True,exist_ok=True)
owned=set(json.loads(state.read_text())) if state.exists() else set()
for address in owned-addresses:
    existing=ip('route','show','table','main',address+'/32')
    if any(str(r.get('protocol'))=='99' and r.get('dev')==link for r in existing):
        subprocess.run(['/usr/sbin/ip','route','del',address+'/32','proto','99'],check=True)
new_owned=set()
for address in sorted(addresses):
    existing=ip('route','show','table','main',address+'/32')
    if existing and address not in owned:
        continue
    subprocess.run(['/usr/sbin/ip','route','replace',address+'/32','via',default['gateway'],'dev',link,'proto','99'],check=True)
    new_owned.add(address)
state.write_text(json.dumps(sorted(new_owned)))
state.chmod(0o600)
print('ACME DNS and direct routes ready for '+str(len(addresses))+' addresses.')
