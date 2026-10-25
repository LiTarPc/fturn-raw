#!/usr/bin/env python3
"""Package the four explicit Linux/Android client manifests for a core release."""
import hashlib
import json
from pathlib import Path
import re
import struct
import zipfile

ROOT = Path(__file__).resolve().parent.parent
VERSION = (ROOT / 'internal/buildinfo/core-version.txt').read_text().strip()
assert re.fullmatch(r'\d+\.\d+\.\d+(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?', VERSION), 'Invalid client core version'
DIST = ROOT / 'dist'
TARGETS = {
    'linux-amd64': ('raw-client', 62, 2),
    'linux-arm64': ('raw-client', 183, 2),
    'android-arm64-vpnservice': ('arm64-v8a/libfturnraw.so', 183, 3),
    'android-arm64-termux': ('raw-client', 183, 3),
}
checksums = []
for target, (binary_name, machine, executable_type) in TARGETS.items():
    folder = DIST / 'platform-clients' / target
    files = {
        binary_name: folder / binary_name,
        'THIRD-PARTY-NOTICES.txt': folder / 'THIRD-PARTY-NOTICES.txt',
        'LICENSE': ROOT / 'LICENSE',
        'README.md': ROOT / 'docs/raw-platform-clients.md',
        'raw-versions.md': ROOT / 'docs/raw-versions.md',
    }
    header = files[binary_name].read_bytes()[:20]
    assert header[:6] == b'\x7fELF\x02\x01', f'Expected ELF64 little-endian: {target}'
    assert struct.unpack_from('<HH', header, 16) == (executable_type, machine), f'ELF type/CPU mismatch: {target}'
    archive = DIST / f'fturn-raw-client-{VERSION}-{target}.zip'
    with zipfile.ZipFile(archive, 'w', compression=zipfile.ZIP_DEFLATED, compresslevel=9) as output:
        for name, path in files.items():
            entry = zipfile.ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
            entry.create_system = 3
            mode = 0o100755 if name == binary_name else 0o100644
            entry.external_attr = mode << 16
            entry.compress_type = zipfile.ZIP_DEFLATED
            output.writestr(entry, path.read_bytes())
    with zipfile.ZipFile(archive) as output:
        assert set(output.namelist()) == set(files), f'Unexpected files: {target}'
        assert output.testzip() is None, f'Corrupt archive: {target}'
        for name, path in files.items():
            assert hashlib.sha256(output.read(name)).digest() == hashlib.sha256(path.read_bytes()).digest()
    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    checksums.append(f'{digest}  {archive.name}')
    print(f'PASS: {archive.name}: explicit files, ELF architecture, executable permissions, CRC and binary hashes')
(DIST / 'CLIENT-SHA256SUMS').write_text('\n'.join(checksums) + '\n', encoding='ascii')
