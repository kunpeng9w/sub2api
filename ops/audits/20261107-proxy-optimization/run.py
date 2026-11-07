import os,subprocess
from pathlib import Path
probe=str(Path(__file__).with_name('probe.py'))
cases=[(2,None,1048576),(2,'256k',1048576),(2,'128k',1048576),(4,None,1048576),(2,'128k',1048576),(2,'256k',1048576),(2,None,1048576),(4,None,1048576),(0,None,65536),(2,None,65536)]
for i,(proxy,rate,size) in enumerate(cases,1):
 env=dict(os.environ,PROBE_BYTES=str(size))
 env.pop('PROBE_RATE',None)
 if rate:env['PROBE_RATE']=rate
 subprocess.run(['python3',probe,f'{proxy}:1.1:{i}'],env=env,check=True)
