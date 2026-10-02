param([Parameter(Mandatory=$true)][string]$Destination)
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
$Destination=[IO.Path]::GetFullPath($Destination)
if(Test-Path -LiteralPath $Destination){throw 'Export destination must not exist'}
$allowedFiles=@('go.mod','go.sum','LICENSE','.gitignore','.gitattributes','ui/windows/routes.ps1','ui/windows/connection.example.json','scripts/build-raw.ps1','scripts/build-raw-ui.ps1','scripts/build-raw-installer.ps1','scripts/export-raw-source.ps1','scripts/test-raw-routes.ps1','scripts/test-raw-installer.ps1','scripts/test-raw-installer-support.ps1')
$allowedDirs=@('cmd/','internal/','third_party/','ui/newservice/','installer/','scripts/debian/')
Push-Location $root
try{
 $files=@(git ls-files)
 if($LASTEXITCODE -ne 0){throw 'Cannot enumerate tracked source'}
 New-Item -ItemType Directory -Path $Destination | Out-Null
 foreach($file in $files){
  $allowed=$file -in $allowedFiles -or ($file -like 'docs/raw-*.md') -or $file -eq 'scripts/ci/raw-windows.yml'
  foreach($prefix in $allowedDirs){if($file.StartsWith($prefix)){$allowed=$true}}
  if(-not $allowed){continue}
  $target=Join-Path $Destination $file
  New-Item -ItemType Directory -Path (Split-Path -Parent $target) -Force | Out-Null
  Copy-Item -LiteralPath (Join-Path $root $file) -Destination $target
 }
 Copy-Item -LiteralPath (Join-Path $root 'docs/raw-public-readme.md') -Destination (Join-Path $Destination 'README.md')
 $workflow=Join-Path $Destination '.github/workflows'
 New-Item -ItemType Directory -Path $workflow -Force | Out-Null
 Copy-Item -LiteralPath (Join-Path $root 'scripts/ci/raw-windows.yml') -Destination (Join-Path $workflow 'windows.yml')
 Write-Host "Exported clean Raw source to $Destination"
}finally{Pop-Location}
