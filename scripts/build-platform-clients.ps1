param([string]$OutputDir = 'dist/platform-clients')
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
if (-not [IO.Path]::IsPathRooted($OutputDir)) { $OutputDir = Join-Path $root $OutputDir }
$version = (Get-Content (Join-Path $root 'internal/buildinfo/core-version.txt') -Raw).Trim()
$oldGOOS, $oldGOARCH, $oldCGO = $env:GOOS, $env:GOARCH, $env:CGO_ENABLED
Push-Location $root
try {
    $env:CGO_ENABLED = '0'
    foreach ($arch in @('amd64', 'arm64')) {
        $env:GOOS = 'linux'; $env:GOARCH = $arch
        $target = Join-Path $OutputDir "linux-$arch/raw-client"
        New-Item -ItemType Directory -Force (Split-Path -Parent $target) | Out-Null
        & go build -trimpath -buildvcs=false -ldflags "-s -w -X github.com/samosvalishe/free-turn-proxy/internal/buildinfo.Version=$version" -o $target ./cmd/raw-client
        if ($LASTEXITCODE -ne 0) { throw "Linux $arch client build failed" }
        & (Join-Path $PSScriptRoot 'collect-raw-licenses.ps1') -ClientOnly -OutputFile (Join-Path (Split-Path -Parent $target) 'THIRD-PARTY-NOTICES.txt')
    }
    $env:GOOS = 'android'; $env:GOARCH = 'arm64'
    foreach ($variant in @('vpnservice', 'termux')) {
        $file = if ($variant -eq 'vpnservice') { 'arm64-v8a/libfturnraw.so' } else { 'raw-client' }
        $target = Join-Path $OutputDir "android-arm64-$variant/$file"
        New-Item -ItemType Directory -Force (Split-Path -Parent $target) | Out-Null
        $buildArgs = @('build', '-trimpath', '-buildvcs=false', '-buildmode=pie', '-ldflags', "-checklinkname=0 -s -w -X github.com/samosvalishe/free-turn-proxy/internal/buildinfo.Version=$version", '-o', $target)
        if ($variant -eq 'termux') { $buildArgs += @('-tags', 'fturntermux') }
        & go @buildArgs ./cmd/raw-client
        if ($LASTEXITCODE -ne 0) { throw "Android $variant client build failed" }
        & (Join-Path $PSScriptRoot 'collect-raw-licenses.ps1') -ClientOnly -OutputFile (Join-Path $OutputDir "android-arm64-$variant/THIRD-PARTY-NOTICES.txt")
    }
} finally {
    $env:GOOS, $env:GOARCH, $env:CGO_ENABLED = $oldGOOS, $oldGOARCH, $oldCGO
    Pop-Location
}
