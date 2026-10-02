import json,pathlib,subprocess,sys,time,datetime,os
name=sys.argv[1];argv=sys.argv[2:]
out=pathlib.Path('/Users/ostape/kee-route-manager/reports/fixes/20261002/evidence');out.mkdir(parents=True,exist_ok=True)
started=datetime.datetime.now(datetime.timezone.utc).isoformat();timer=time.monotonic()
with (out/(name+'.log')).open('wb') as f:
 p=subprocess.run(argv,stdout=f,stderr=subprocess.STDOUT,cwd='/Users/ostape/kee-route-manager')
receipt={'argv':argv,'cwd':os.getcwd(),'started_at':started,'exit':p.returncode,'elapsed_seconds':round(time.monotonic()-timer,3),'output':name+'.log'}
(out/(name+'.json')).write_text(json.dumps(receipt,indent=2)+'\n')
print(json.dumps(receipt))
sys.exit(p.returncode)
