#!/usr/bin/env python3
"""Verify the release source snapshot before testing or building."""
import hashlib,json
from pathlib import Path
root=Path(__file__).resolve().parent.parent
m=json.loads((root/'SOURCE.json').read_text(encoding='utf-8'))
assert m['coreVersion']==m['serverVersion']=='1.1.0'
for f in m['files']:
 p=(root/f['path']).resolve()
 assert p.is_relative_to(root) and p.is_file(),f['path']
 assert hashlib.sha256(p.read_bytes()).hexdigest()==f['sha256'],f['path']
digest=hashlib.sha256(json.dumps(m['files'],sort_keys=True,separators=(',',':')).encode()).hexdigest()
assert digest==m['sourceSnapshotSha256']
for kind in ['core','server']:
 assert (root/f'internal/buildinfo/{kind}-version.txt').read_text().strip()=='1.1.0'
print(f"Verified {len(m['files'])} pinned source files: {digest}")
