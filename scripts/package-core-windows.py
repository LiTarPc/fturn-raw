#!/usr/bin/env python3
import hashlib,struct,zipfile
from pathlib import Path
root=Path(__file__).resolve().parent.parent
folder=root/'dist/core-windows'
version = (root/'internal/buildinfo/core-version.txt').read_text().strip()
files={'raw-client.exe':folder/'raw-client.exe','wintun.dll':folder/'wintun.dll','WINTUN-LICENSE.txt':folder/'WINTUN-LICENSE.txt','THIRD-PARTY-NOTICES.txt':folder/'THIRD-PARTY-NOTICES.txt','LICENSE':root/'LICENSE','README.md':root/f'docs/raw-core-{version}.md'}
for name in ['raw-client.exe','wintun.dll']:
 data=files[name].read_bytes();assert data[:2]==b'MZ',name
 offset=struct.unpack_from('<I',data,0x3c)[0]
 assert data[offset:offset+4]==b'PE\0\0' and struct.unpack_from('<H',data,offset+4)[0]==0x8664,name
archive=root/f'dist/fturn-raw-client-{version}-windows-amd64.zip'
with zipfile.ZipFile(archive,'w',compression=zipfile.ZIP_DEFLATED,compresslevel=9) as z:
 for name,p in sorted(files.items()):
  entry=zipfile.ZipInfo(name,(1980,1,1,0,0,0));entry.create_system=3;entry.external_attr=0o100644<<16;entry.compress_type=zipfile.ZIP_DEFLATED
  z.writestr(entry,p.read_bytes())
with zipfile.ZipFile(archive) as z:
 assert set(z.namelist())==set(files) and z.testzip() is None
 for name,p in files.items():assert z.read(name)==p.read_bytes()
(root/'dist/CLIENT-SHA256SUMS-windows-amd64').write_text(hashlib.sha256(archive.read_bytes()).hexdigest()+'  '+archive.name+'\n',encoding='ascii')
print('Verified Windows amd64 executable, matching Wintun DLL, licenses and archive:',archive.name)
