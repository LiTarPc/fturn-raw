$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
$out=Join-Path $root 'dist/core-windows'
New-Item -ItemType Directory -Path $out -Force | Out-Null
$env:GOOS='windows';$env:GOARCH='amd64';$env:CGO_ENABLED='0'
Push-Location $root
try {
 go build -mod=readonly -buildvcs=false -trimpath -ldflags '-checklinkname=0 -s -w -X github.com/samosvalishe/free-turn-proxy/internal/buildinfo.Version=1.1.0' -o (Join-Path $out 'raw-client.exe') ./cmd/raw-client
 if($LASTEXITCODE -ne 0){throw 'Windows core build failed'}
 & (Join-Path $PSScriptRoot 'bundle-raw-wintun.ps1') -OutputDir $out
 & (Join-Path $PSScriptRoot 'collect-raw-licenses.ps1') -ClientOnly -OutputFile (Join-Path $out 'THIRD-PARTY-NOTICES.txt')
 if((& (Join-Path $out 'raw-client.exe') --version) -ne 'FturnRaw core 1.1.0'){throw 'Core version mismatch'}
 python scripts/package-core-windows.py
 if($LASTEXITCODE -ne 0){throw 'Core packaging failed'}
} finally {Pop-Location}
