param([switch]$SkipGoBuild)
$ErrorActionPreference='Stop'
$projectRoot=Split-Path -Parent $PSScriptRoot
$productVersion=(Get-Content -LiteralPath (Join-Path $projectRoot 'ui/newservice/wails.json') -Raw | ConvertFrom-Json).info.productVersion
$coreVersion=([IO.File]::ReadAllText((Join-Path $projectRoot 'internal/buildinfo/core-version.txt'))).Trim()
if($coreVersion -notmatch '^\d+\.\d+\.\d+(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$'){throw 'Raw core version must be major.minor.patch'}
$oldArch=$env:GOARCH;$oldCGO=$env:CGO_ENABLED;$oldOS=$env:GOOS
Push-Location $projectRoot
try {
 $env:GOARCH='amd64';$env:CGO_ENABLED='0';$env:GOOS='windows'
 if(-not $SkipGoBuild){
  $coreDir=Join-Path $projectRoot 'dist/windows-amd64'
  New-Item -ItemType Directory -Path $coreDir -Force | Out-Null
  go build -trimpath -ldflags ("-s -w -X github.com/samosvalishe/free-turn-proxy/internal/buildinfo.Version="+$coreVersion) -o (Join-Path $coreDir 'raw-client.exe') ./cmd/raw-client
  if($LASTEXITCODE -ne 0){throw 'Raw client build failed'}
  & (Join-Path $PSScriptRoot 'bundle-raw-wintun.ps1') -OutputDir $coreDir
 }
 & powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'test-raw-routes.ps1')
 if($LASTEXITCODE -ne 0){throw 'Route/privacy regression checks failed'}
 & powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'test-raw-watch.ps1')
 if($LASTEXITCODE -ne 0){throw 'Crash guard integration checks failed'}
 Push-Location (Join-Path $projectRoot 'ui/newservice')
 try {
  Push-Location frontend
  try {
   npm ci
   if($LASTEXITCODE -ne 0){throw 'UI dependencies failed'}
   npm run lint
   if($LASTEXITCODE -ne 0){throw 'UI lint failed'}
   npm test
   if($LASTEXITCODE -ne 0){throw 'UI frontend tests failed'}
   npm run build
   if($LASTEXITCODE -ne 0){throw 'UI frontend build failed'}
  }finally{Pop-Location}
  go test . ./backend
  if($LASTEXITCODE -ne 0){throw 'UI process/backend tests failed'}
  wails build -platform windows/amd64 -clean -ldflags ("-X fturnrawui/backend.Version="+$productVersion)
  if($LASTEXITCODE -ne 0){throw 'Wails UI build failed'}
 }finally{Pop-Location}
 $outputDir=Join-Path $projectRoot 'dist/windows-ui-clean'
 foreach($dir in @('','runtime','licenses','docs')){New-Item -ItemType Directory -Path (Join-Path $outputDir $dir) -Force | Out-Null}
 # Explicit manifest prevents packaging personal data from a previously used folder.
 $files=[ordered]@{
  'FturnRaw.exe'='ui/newservice/build/bin/FturnRaw.exe'
  'runtime/raw-client.exe'='dist/windows-amd64/raw-client.exe'
  'runtime/wintun.dll'='dist/windows-amd64/wintun.dll'
  'runtime/routes.ps1'='ui/windows/routes.ps1'
  'licenses/WINTUN-LICENSE.txt'='dist/windows-amd64/WINTUN-LICENSE.txt'
  'licenses/LICENSE'='LICENSE'
  'licenses/UI-LICENSE.txt'='ui/newservice/LICENSE'
  'licenses/ATTRIBUTION.md'='ui/newservice/ATTRIBUTION.md'
  'licenses/SYSTRAY-LICENSE.txt'='ui/newservice/SYSTRAY-LICENSE.txt'
  'docs/README.md'='docs/raw-windows-ui.md'
 }
 foreach($entry in $files.GetEnumerator()){Copy-Item -LiteralPath (Join-Path $projectRoot $entry.Value) -Destination (Join-Path $outputDir $entry.Key)}
 & (Join-Path $PSScriptRoot 'collect-raw-licenses.ps1') -OutputFile (Join-Path $outputDir 'licenses/THIRD-PARTY-NOTICES.txt')
 Add-Type -AssemblyName System.IO.Compression
 $archive=Join-Path $projectRoot 'dist/fturn-raw-windows-ui-newservice.zip'
 $stream=[IO.File]::Open($archive,[IO.FileMode]::Create)
 $zip=New-Object IO.Compression.ZipArchive($stream,[IO.Compression.ZipArchiveMode]::Create)
 try{
  foreach($name in @($files.Keys)+@('licenses/THIRD-PARTY-NOTICES.txt')){
   $entry=$zip.CreateEntry($name,[IO.Compression.CompressionLevel]::Optimal)
   $input=[IO.File]::OpenRead((Join-Path $outputDir $name));$output=$entry.Open()
   try{$input.CopyTo($output)}finally{$input.Dispose();$output.Dispose()}
  }
 }finally{$zip.Dispose();$stream.Dispose()}
 Write-Host 'Built clean Windows UI layout; public package has no private key or call link.'
}finally{$env:GOARCH=$oldArch;$env:CGO_ENABLED=$oldCGO;$env:GOOS=$oldOS;Pop-Location}
