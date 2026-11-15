#!/usr/bin/env python3
"""Merge explicit package manifests and hash every core release attachment."""
from pathlib import Path
import hashlib,re,shutil
root=Path(__file__).resolve().parent.parent
out=root/'release';out.mkdir(exist_ok=True)
for prefix in ['CLIENT','SERVER']:
 records={}
 for p in list(out.glob(prefix+'-SHA256SUMS*')):
  for line in p.read_text(encoding='ascii').splitlines():
   digest,name=line.split('  ',1)
   assert re.fullmatch('[a-f0-9]{64}',digest) and Path(name).name==name
   assert hashlib.sha256((out/name).read_bytes()).hexdigest()==digest,name
   assert name not in records or records[name]==digest
   records[name]=digest
  if p.name!=prefix+'-SHA256SUMS':p.unlink()
 assert len(records)==(5 if prefix=='CLIENT' else 3),(prefix,len(records))
 (out/(prefix+'-SHA256SUMS')).write_text(''.join(f'{records[n]}  {n}\n' for n in sorted(records)),encoding='ascii')
for source,dest in [('scripts/quick-start.sh','quick-start.sh'),('docs/QUICK-START.md','QUICK-START.md'),('SOURCE.json','SOURCE.json')]:
 shutil.copyfile(root/source,out/dest)
expected={f'fturn-raw-client-1.1.0-{t}.zip' for t in ['windows-amd64','linux-amd64','linux-arm64','android-arm64-termux','android-arm64-vpnservice']}
expected|={f'fturn-raw-server-1.1.0-{t}.zip' for t in ['linux-amd64','linux-arm64','windows-amd64']}
expected|={'libfturnraw.so','CLIENT-SHA256SUMS','SERVER-SHA256SUMS','quick-start.sh','QUICK-START.md','SOURCE.json'}
assert {p.name for p in out.iterdir() if p.is_file()}==expected
(out/'SHA256SUMS').write_text(''.join(hashlib.sha256((out/n).read_bytes()).hexdigest()+'  '+n+'\n' for n in sorted(expected)),encoding='ascii')
print('Verified',len(expected),'attachments; global SHA256SUMS includes every attachment.')
