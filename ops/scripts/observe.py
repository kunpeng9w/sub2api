#!/usr/bin/env python3
"""Collect 30 minutes of metadata only. No request bodies, tokens or responses."""
import datetime, json, pathlib, subprocess, sys, time
BASE=pathlib.Path('/home/pk/workspace/sub2api/.operator')

def sample():
    mem={line.split(':')[0]:int(line.split()[1]) for line in pathlib.Path('/proc/meminfo').read_text().splitlines()}
    vm={line.split()[0]:int(line.split()[1]) for line in pathlib.Path('/proc/vmstat').read_text().splitlines()}
    units={}
    for unit in ('sub2api','postgresql@16-main','redis-server'):
        raw=subprocess.check_output(['systemctl','show',unit,'-p','ActiveState','-p','NRestarts','-p','MemoryCurrent'],text=True)
        units[unit]=dict(line.split('=',1) for line in raw.splitlines())
    return {'time':datetime.datetime.now(datetime.timezone.utc).isoformat(),'available_kib':mem['MemAvailable'],'swap_used_kib':mem['SwapTotal']-mem['SwapFree'],'swap_in_pages':vm['pswpin'],'swap_out_pages':vm['pswpout'],'oom_kill':vm.get('oom_kill',0),'units':units}

def main():
    samples=[]
    started=time.monotonic()
    path=BASE/'resources.jsonl'
    with path.open('w') as out:
        path.chmod(0o600)
        while True:
            item=sample(); samples.append(item)
            out.write(json.dumps(item)+'\n'); out.flush()
            if time.monotonic()-started >= 1800: break
            time.sleep(15)
    last=samples[-20:]
    failures=[]
    if samples[-1]['oom_kill']>samples[0]['oom_kill']: failures.append('System OOM counter increased')
    if any(x['units']['sub2api']['NRestarts']!=samples[0]['units']['sub2api']['NRestarts'] for x in samples): failures.append('Application restarted during observation')
    if any(x['units'][unit]['ActiveState']!='active' for x in samples for unit in x['units']): failures.append('A service was not active')
    if min(x['available_kib'] for x in last)<102400: failures.append('Final 5-minute available memory fell below 100 MiB')
    active_intervals=sum(b['swap_in_pages']+b['swap_out_pages']>a['swap_in_pages']+a['swap_out_pages'] for a,b in zip(last,last[1:]))
    if active_intervals>=len(last)//2: failures.append('Swap activity persisted through final 5 minutes')
    result={'duration_seconds':round(time.monotonic()-started),'samples':len(samples),'passed':not failures,'failures':failures,'final_available_mib':round(samples[-1]['available_kib']/1024,1),'min_available_mib':round(min(x['available_kib'] for x in samples)/1024,1)}
    (BASE/'resource-result.json').write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps(result))
    return int(bool(failures))

if __name__=='__main__': sys.exit(main())
