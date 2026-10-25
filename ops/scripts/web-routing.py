#!/usr/bin/python3
"""Keep public Web replies on eth0 instead of the host's TUN route."""
import json,subprocess,sys
rules=json.loads(subprocess.check_output(['/usr/sbin/ip','-j','rule','show'],text=True))
for priority,port in ((101,80),(102,443)):
    entries=[r for r in rules if r.get('priority')==priority]
    matching=any(str(r.get('table')) in ('main','254') and r.get('ipproto')=='tcp' and str(r.get('sport'))==str(port) for r in entries)
    if sys.argv[1]=='start':
        if entries and not matching: raise SystemExit('Conflicting IP rule priority '+str(priority))
        if not matching:
            subprocess.run(['/usr/sbin/ip','rule','add','priority',str(priority),'ipproto','tcp','sport',str(port),'lookup','main'],check=True)
    elif matching:
        subprocess.run(['/usr/sbin/ip','rule','del','priority',str(priority),'ipproto','tcp','sport',str(port),'lookup','main'],check=True)
