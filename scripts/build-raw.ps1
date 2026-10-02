param(
 [ValidateSet('windows-amd64','linux-amd64','linux-arm64')]
 [string[]]$Targets = @('windows-amd64','linux-amd64','linux-arm64'),
 [switch]$SkipWintun
)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$distRoot = Join-Path $projectRoot 'dist'
$previousGOOS = $env:GOOS
$previousGOARCH = $env:GOARCH
$previousCGO = $env:CGO_ENABLED
Push-Location $projectRoot
try {
 $env:CGO_ENABLED = '0'
 foreach ($target in $Targets) {
  $platform, $architecture = $target.Split('-')
  $env:GOOS = $platform
  $env:GOARCH = $architecture
  $outputDir = Join-Path $distRoot $target
  New-Item -ItemType Directory -Path $outputDir -Force | Out-Null
  $suffix = if ($platform -eq 'windows') { '.exe' } else { '' }
  foreach ($program in @('raw-client','raw-server')) {
   & go build -trimpath -ldflags '-s -w' -o (Join-Path $outputDir ($program + $suffix)) "./cmd/$program"
   if ($LASTEXITCODE -ne 0) { throw "Build failed: $target/$program" }
  }
  Copy-Item -LiteralPath (Join-Path $projectRoot 'LICENSE') -Destination $outputDir
  Copy-Item -LiteralPath (Join-Path $projectRoot 'docs/raw-experiment.md') -Destination (Join-Path $outputDir 'README.md')
  Write-Host "Built $target"
 }
 if ('windows-amd64' -in $Targets -and -not $SkipWintun) {
  $archivePath = Join-Path $distRoot 'wintun-0.14.1.zip'
  $expectedHash = '07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51'
  if (-not (Test-Path -LiteralPath $archivePath)) {
   Invoke-WebRequest -Uri 'https://www.wintun.net/builds/wintun-0.14.1.zip' -OutFile $archivePath
  }
  $actualHash = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
  if ($actualHash -ne $expectedHash) { throw 'Wintun archive hash does not match the official pinned release.' }
  $unpackPath = Join-Path $distRoot 'wintun-package'
  Expand-Archive -LiteralPath $archivePath -DestinationPath $unpackPath -Force
  $outputDir = Join-Path $distRoot 'windows-amd64'
  Copy-Item -LiteralPath (Join-Path $unpackPath 'wintun/bin/amd64/wintun.dll') -Destination $outputDir
  Copy-Item -LiteralPath (Join-Path $unpackPath 'wintun/LICENSE.txt') -Destination (Join-Path $outputDir 'WINTUN-LICENSE.txt')
  Write-Host 'Bundled official Wintun 0.14.1 (SHA256 verified); no driver was installed.'
 }
 foreach ($target in $Targets) {
  $outputDir = Join-Path $distRoot $target
  Compress-Archive -Path (Join-Path $outputDir '*') -DestinationPath (Join-Path $distRoot ("fturn-raw-$target.zip")) -Force
 }
} finally {
 $env:GOOS = $previousGOOS
 $env:GOARCH = $previousGOARCH
 $env:CGO_ENABLED = $previousCGO
 Pop-Location
}
