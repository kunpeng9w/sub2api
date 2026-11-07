#!/usr/bin/env python3
"""Bounded synthetic upload probes; no upstream credentials or user content."""
import subprocess,json,time,os,tempfile,threading,re,sys,datetime
from urllib.parse import quote
from pathlib import Path
OUT=Path(__file__).parent
rows=json.loads(subprocess.check_output(['sudo','-n','-u','postgres','psql','-X','-d','sub2api','-Atc',"SELECT coalesce(json_agg(p),'[]') FROM (SELECT id,protocol,host,port,username,password FROM proxies WHERE deleted_at IS NULL AND status='active') p"],text=True))
proxies={r['id']:r for r in rows}
def pressure():
 out={}
 for kind in ('memory','io','cpu'):
  for line in Path('/proc/pressure/'+kind).read_text().splitlines():
   k=line.split()[0]; out[kind+'_'+k]=int(re.search(r'total=(\d+)',line)[1])
 for kind in ('memory','io','cpu'):
  for line in Path('/sys/fs/cgroup/system.slice/sub2api.service/'+kind+'.pressure').read_text().splitlines():
   k=line.split()[0]; out['service_'+kind+'_'+k]=int(re.search(r'total=(\d+)',line)[1])
 return out
def counters():
 vm=dict(line.split() for line in Path('/proc/vmstat').read_text().splitlines())
 cg=dict(line.split() for line in Path('/sys/fs/cgroup/system.slice/sub2api.service/memory.stat').read_text().splitlines())
 return {'swapin_bytes':int(vm['pswpin'])*os.sysconf('SC_PAGE_SIZE'),'swapout_bytes':int(vm['pswpout'])*os.sysconf('SC_PAGE_SIZE'),'service_pgmajfault':int(cg['pgmajfault'])}
fd,path=tempfile.mkstemp(prefix='synthetic-upload-')
os.write(fd,b'x'*int(os.environ.get('PROBE_BYTES','1048576')));os.close(fd)
def probe(pid,version,roundno):
 p=proxies.get(pid); config=''
 if p:
  scheme='socks5h' if p['protocol'] in ('socks5','socks5h') else p['protocol']
  auth=(quote(p.get('username') or '',safe='')+':'+quote(p.get('password') or '',safe='')+'@') if p.get('username') else ''
  url=f"{scheme}://{auth}{p['host']}:{p['port']}"
  config='proxy = '+json.dumps(url)+'\nnoproxy = ""\n'
 else: config='proxy = ""\n'
 start=time.monotonic(); before=pressure(); counts_before=counters(); events=[]; samples=[]
 cmd=['curl','--config','-','--http'+version,'--connect-timeout','10','--max-time','35','--silent','--show-error','--verbose','-H','Expect:','-H','Content-Type: application/octet-stream','--data-binary','@'+path,'--output','/dev/null','--write-out','%{json}',os.environ.get('PROBE_URL','https://speed.cloudflare.com/__up')]
 if os.environ.get('PROBE_RATE'):cmd += ['--limit-rate',os.environ['PROBE_RATE']]
 env={k:v for k,v in os.environ.items() if k.lower() not in ('http_proxy','https_proxy','all_proxy','no_proxy')}
 proc=subprocess.Popen(cmd,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,env=env)
 def stderr():
  for line in proc.stderr:
   l=line.lower()
   if any(x in l for x in ('upload completely sent','completely uploaded','connected to','ssl connection using','< http/','curl:')):
    # Never retain raw verbose output: it can contain proxy authentication.
    event=('upload_done' if 'upload' in l else 'tls_done' if 'ssl connection' in l else 'connected' if 'connected to' in l else 'http_status' if '< http/' in l else 'curl_error')
    events.append({'t':round(time.monotonic()-start,4),'event':event})
 t=threading.Thread(target=stderr);t.start();proc.stdin.write(config);proc.stdin.close()
 while proc.poll() is None:
  raw=subprocess.check_output(['ss','-tinpH'],text=True)
  lines=raw.splitlines()
  for i,l in enumerate(lines):
   if f'pid={proc.pid},' not in l: continue
   info=lines[i+1] if i+1<len(lines) else ''
   tokens=l.split(); d={'t':round(time.monotonic()-start,3),'send_q':int(tokens[2])}
   for key in ('bytes_sent','bytes_acked','bytes_retrans','notsent','cwnd','rtt','retrans','rwnd_limited','sndbuf_limited','delivery_rate'):
    m=re.search(r'\b'+key+r':([^\s]+)',info)
    if m:d[key]=m[1]
   samples.append(d)
  time.sleep(.5)
 stdout=proc.stdout.read();t.join();after=pressure()
 try: m=json.loads(stdout)
 except ValueError:m={}
 keep=('http_code','http_version','size_upload','size_download','time_connect','time_appconnect','time_pretransfer','time_starttransfer','time_total','speed_upload','speed_download','exitcode','num_connects')
 result={'time':datetime.datetime.now(datetime.timezone(datetime.timedelta(hours=8))).isoformat(),'target':os.environ.get('PROBE_URL','https://speed.cloudflare.com/__up'),'rate_limit':os.environ.get('PROBE_RATE'),'proxy_id':pid,'requested_http':version,'round':roundno,'curl':{k:m[k] for k in keep if k in m},'returncode':proc.returncode,'events':events,'tcp':samples,'counter_delta':{k:v-counts_before[k] for k,v in counters().items()},'psi_stall_seconds':{k:round((after[k]-before[k])/1e6,4) for k in before}}
 with (OUT/'results.jsonl').open('a') as f:f.write(json.dumps(result)+'\n')
 summary={k:v for k,v in result.items() if k!='tcp'}
 summary['max_retrans_bytes']=max([int(s.get('bytes_retrans',0)) for s in samples] or [0]);summary['max_send_q']=max([s['send_q'] for s in samples] or [0]);print(json.dumps(summary),flush=True)
try:
 for arg in sys.argv[1:] or ['0:2:1','1:2:1','2:2:1','4:2:1']:
  pid,v,r=arg.split(':');probe(int(pid),v,int(r))
finally:os.unlink(path)
