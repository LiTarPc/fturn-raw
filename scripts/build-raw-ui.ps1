param([switch]$SkipGoBuild)
$ErrorActionPreference='Stop'
$projectRoot=Split-Path -Parent $PSScriptRoot
$oldArch=$env:GOARCH
$oldCGO=$env:CGO_ENABLED
Push-Location $projectRoot
try {
 if(-not $SkipGoBuild){& (Join-Path $PSScriptRoot 'build-raw.ps1') -Targets windows-amd64}
 & powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'test-raw-routes.ps1')
 if($LASTEXITCODE -ne 0){throw 'Route/privacy regression checks failed'}
 $env:GOARCH='amd64';$env:CGO_ENABLED='0'
 Push-Location (Join-Path $projectRoot 'ui/newservice')
 try {
  go test ./backend
  if($LASTEXITCODE -ne 0){throw 'UI backend tests failed'}
  wails build -platform windows/amd64 -clean
  if($LASTEXITCODE -ne 0){throw 'Wails UI build failed'}
 }finally{Pop-Location}
 $outputDir=Join-Path $projectRoot 'dist/windows-ui-newservice'
 New-Item -ItemType Directory -Path $outputDir -Force | Out-Null
 Copy-Item -LiteralPath (Join-Path $projectRoot 'ui/newservice/build/bin/FturnRaw.exe'),(Join-Path $projectRoot 'dist/windows-amd64/raw-client.exe'),(Join-Path $projectRoot 'dist/windows-amd64/wintun.dll'),(Join-Path $projectRoot 'dist/windows-amd64/WINTUN-LICENSE.txt'),(Join-Path $projectRoot 'LICENSE'),(Join-Path $projectRoot 'ui/windows/routes.ps1') -Destination $outputDir
 Copy-Item -LiteralPath (Join-Path $projectRoot 'ui/newservice/LICENSE') -Destination (Join-Path $outputDir 'UI-LICENSE.txt')
 Copy-Item -LiteralPath (Join-Path $projectRoot 'ui/newservice/ATTRIBUTION.md'),(Join-Path $projectRoot 'ui/newservice/SYSTRAY-LICENSE.txt') -Destination $outputDir
 Copy-Item -LiteralPath (Join-Path $projectRoot 'ui/windows/connection.example.json') -Destination (Join-Path $outputDir 'connection.json')
 Copy-Item -LiteralPath (Join-Path $projectRoot 'docs/raw-windows-ui.md') -Destination (Join-Path $outputDir 'README.md')
 & (Join-Path $PSScriptRoot 'collect-raw-licenses.ps1') -OutputFile (Join-Path $outputDir 'THIRD-PARTY-NOTICES.txt')
 # Explicit file list: a previously used output folder may contain imported private keys.
 $bundleFiles=@('FturnRaw.exe','raw-client.exe','wintun.dll','WINTUN-LICENSE.txt','LICENSE','UI-LICENSE.txt','ATTRIBUTION.md','SYSTRAY-LICENSE.txt','routes.ps1','connection.json','README.md','THIRD-PARTY-NOTICES.txt') | ForEach-Object {Join-Path $outputDir $_}
 Compress-Archive -LiteralPath $bundleFiles -DestinationPath (Join-Path $projectRoot 'dist/fturn-raw-windows-ui-newservice.zip') -Force
 Write-Host 'Built Newservice-based Windows UI; public package has no private key or call link.'
}finally{$env:GOARCH=$oldArch;$env:CGO_ENABLED=$oldCGO;Pop-Location}
