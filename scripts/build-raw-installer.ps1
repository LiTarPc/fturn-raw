param(
 [string]$Version='0.1.1',
 [switch]$SkipBuild,
 [switch]$TestMode,
 [string]$OutputFile
)
$ErrorActionPreference='Stop'
if($Version -notmatch '^\d+\.\d+\.\d+$'){throw 'Version must be major.minor.patch'}
$projectRoot=Split-Path -Parent $PSScriptRoot
$productVersion=(Get-Content -LiteralPath (Join-Path $projectRoot 'ui/newservice/wails.json') -Raw | ConvertFrom-Json).info.productVersion
if($Version -ne $productVersion){throw 'Installer and Wails product versions must match'}
if(-not $SkipBuild){& (Join-Path $PSScriptRoot 'build-raw-ui.ps1')}
$payload=Join-Path $projectRoot 'dist/windows-ui-newservice'
$config=Get-Content -LiteralPath (Join-Path $payload 'connection.json') -Raw | ConvertFrom-Json
if($config.Server -or $config.VkLink -or $config.Key -or $config.KeyFile){throw 'Installer configuration must contain no personal data'}
$compiler=Get-Command makensis.exe -ErrorAction SilentlyContinue
if($compiler){$compiler=$compiler.Source}else{$compiler=Join-Path ${env:ProgramFiles(x86)} 'NSIS/makensis.exe'}
if(-not(Test-Path -LiteralPath $compiler)){throw 'Install NSIS 3 and add makensis.exe to PATH'}
if(-not $OutputFile){$OutputFile=Join-Path $projectRoot "dist/fturn-raw-$Version-windows-x64-setup.exe"}
$OutputFile=[IO.Path]::GetFullPath($OutputFile)
$defines=@('/V3',"/DVERSION=$Version","/DPAYLOAD_DIR=$payload","/DOUTPUT_FILE=$OutputFile")
if($TestMode){$defines+='/DTEST_MODE'}
Push-Location (Join-Path $projectRoot 'installer')
try{
 & $compiler @defines 'fturn-raw.nsi'
 if($LASTEXITCODE -ne 0){throw 'NSIS compilation failed'}
}finally{Pop-Location}
Get-FileHash -LiteralPath $OutputFile -Algorithm SHA256
