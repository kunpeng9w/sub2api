#!/usr/bin/env python3
"""Read-only production journal and kernel counters; retain only sanitized metrics."""
import collections, datetime, json, math, re, subprocess, time
from pathlib import Path
OUT=Path(__file__).parent
START='2026-11-07 07:45:00'
END=datetime.datetime.now().astimezone().strftime('%Y-%m-%d %H:%M:%S')
fields=['elapsed_ms','request_write_total_ms','wait_after_request_written_ms','request_body_bytes','response_read_max_gap_ms','tcp_connect_ms','tls_handshake_ms','pool_queue_wait_ms']
rows=[]; bad=0
p=subprocess.Popen(['sudo','-n','journalctl','-u','sub2api','--since',START,'--until',END,'-g','upstream_egress_completed','-o','cat','--no-pager'],stdout=subprocess.PIPE,text=True)
for line in p.stdout:
 i=line.find('{')
 if i<0: continue
 try: d=json.loads(line[i:])
 except ValueError: bad+=1; continue
 if '\tupstream_egress_completed\t' not in line[:i]: continue
 row={k:d[k] for k in fields+['proxy_id','protocol','outcome','connection_reused'] if k in d}
 row['time']=line.split('\t',1)[0]; rows.append(row)
if p.wait(): raise SystemExit('journal read failed')
def stats(v):
 v=sorted(x for x in v if isinstance(x,(int,float)))
 if not v:return None
 return {'n':len(v),'mean':round(sum(v)/len(v),3),'p50':v[math.ceil(len(v)*.5)-1],'p95':v[math.ceil(len(v)*.95)-1],'max':v[-1]}
groups=collections.defaultdict(list)
for r in rows: groups[(r['time'][:13],r.get('proxy_id'),r.get('protocol'))].append(r)
summary=[]
for (hour,proxy,protocol),rr in sorted(groups.items(),key=lambda kv:str(kv[0])):
 summary.append({'hour':hour,'proxy_id':proxy,'protocol':protocol,'attempts':len(rr),'outcomes':dict(collections.Counter(r.get('outcome','unknown') for r in rr)),'metrics':{k:stats([r.get(k) for r in rr]) for k in fields},'write_gt_5s':sum(r.get('request_write_total_ms',0)>5000 for r in rr),'write_gt_10s':sum(r.get('request_write_total_ms',0)>10000 for r in rr)})
# ss addresses are used only as in-memory socket keys and never saved.
pid=subprocess.check_output(['systemctl','show','sub2api','-p','MainPID','--value'],text=True).strip()
def snapshot():
 raw=subprocess.check_output(['sudo','-n','ss','-tinpH'],text=True).splitlines(); out={}
 for i,l in enumerate(raw):
  if 'pid='+pid+',' not in l or i+1>=len(raw):continue
  tok=l.split(); info=raw[i+1]; d={'send_q':int(tok[2])}
  for k in ['bytes_sent','bytes_acked','bytes_retrans','rtt','cwnd','notsent']:
   m=re.search(r'\b'+k+r':([^\s]+)',info)
   if m:d[k]=float(m[1].split('/')[0]) if k=='rtt' else int(m[1])
  out[(tok[3],tok[4])]=d
 return out
def counters():
 return json.loads(subprocess.check_output(['ip','-j','-s','link','show','dev','eth0'],text=True))[0]['stats64']
a=snapshot(); c0=counters(); t0=time.monotonic(); time.sleep(10); b=snapshot(); c1=counters(); dt=time.monotonic()-t0
sockets=[]
for key in a.keys()&b.keys():
 x,y=a[key],b[key]; delta={k:y.get(k,0)-x.get(k,0) for k in ['bytes_sent','bytes_acked','bytes_retrans']}
 sockets.append({'delta':delta,'start':x,'end':y})
result={'start':START,'end':END,'timezone':'Asia/Singapore','parse_failures':bad,'rows':len(rows),'hourly':summary,'socket_window_seconds':round(dt,3),'persistent_sockets':sockets,'new_socket_count':len(b.keys()-a.keys()),'closed_socket_count':len(a.keys()-b.keys()),'interface_delta':{direction:{k:c1[direction][k]-v for k,v in c0[direction].items()} for direction in ['rx','tx']}}
(OUT/'events.json').write_text(json.dumps(rows,ensure_ascii=False,indent=2)+'\n')
(OUT/'summary.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
for s in summary:
 print(json.dumps({**{k:v for k,v in s.items() if k!='metrics'},'write_ms':s['metrics']['request_write_total_ms'],'wait_ms':s['metrics']['wait_after_request_written_ms'],'body_bytes':s['metrics']['request_body_bytes']},ensure_ascii=False))
print(json.dumps({k:v for k,v in result.items() if k not in ['hourly']},ensure_ascii=False))
