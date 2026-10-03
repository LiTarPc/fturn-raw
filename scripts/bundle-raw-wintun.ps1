param([Parameter(Mandatory=$true)][string]$OutputDir)
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
$dist=Join-Path $root 'dist'
New-Item -ItemType Directory -Path $dist,$OutputDir -Force | Out-Null
$archive=Join-Path $dist 'wintun-0.14.1.zip'
$expected='07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51'
if(-not(Test-Path -LiteralPath $archive)){
 Invoke-WebRequest -Uri 'https://www.wintun.net/builds/wintun-0.14.1.zip' -OutFile $archive
}
if((Get-FileHash -LiteralPath $archive).Hash.ToLowerInvariant() -ne $expected){throw 'Wintun archive hash does not match the official pinned release'}
$unpack=Join-Path $dist 'wintun-package'
Expand-Archive -LiteralPath $archive -DestinationPath $unpack -Force
Copy-Item -LiteralPath (Join-Path $unpack 'wintun/bin/amd64/wintun.dll') -Destination $OutputDir
Copy-Item -LiteralPath (Join-Path $unpack 'wintun/LICENSE.txt') -Destination (Join-Path $OutputDir 'WINTUN-LICENSE.txt')
Write-Host 'Bundled official Wintun 0.14.1 (SHA256 verified); no driver was installed.'
