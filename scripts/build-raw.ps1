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
  & (Join-Path $PSScriptRoot 'bundle-raw-wintun.ps1') -OutputDir (Join-Path $distRoot 'windows-amd64')
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
