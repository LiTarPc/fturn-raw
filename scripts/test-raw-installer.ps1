param([switch]$SkipCompile)
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
$expectedVersion=(Get-Content -LiteralPath (Join-Path $root 'ui/newservice/wails.json') -Raw | ConvertFrom-Json).info.productVersion
$testRoot=Join-Path $root 'dist/installer-smoke-clean'
$target=Join-Path $testRoot 'installed'
$setup=Join-Path $testRoot 'setup-test.exe'
New-Item -ItemType Directory -Path $testRoot -Force | Out-Null
if(-not $SkipCompile){& (Join-Path $PSScriptRoot 'build-raw-installer.ps1') -SkipBuild -TestMode -OutputFile $setup}
function Assert($condition,$message){if(-not $condition){throw $message}}
# This separately compiled test variant uses HKCU and unique shortcuts, no elevation,
# no WebView installation, no network changes, and never launches the application.
$appData=Join-Path $env:LOCALAPPDATA 'fturn-raw'
$before=@{}
foreach($name in @('profiles.json','app-settings.json')){
 $path=Join-Path $appData $name
 $before[$name]=if(Test-Path -LiteralPath $path){(Get-FileHash -LiteralPath $path).Hash}else{''}
}
function Install-Test {
 $process=Start-Process -FilePath $setup -ArgumentList '/S',("/D="+$target) -WindowStyle Hidden -Wait -PassThru
 Assert ($process.ExitCode -eq 0) 'NSIS silent install failed'
}
Install-Test
$source=Join-Path $root 'dist/windows-ui-clean'
foreach($file in @('FturnRaw.exe','runtime/raw-client.exe','runtime/wintun.dll','runtime/routes.ps1','licenses/LICENSE','docs/README.md')){
 Assert ((Get-FileHash -LiteralPath (Join-Path $source $file)).Hash -eq (Get-FileHash -LiteralPath (Join-Path $target $file)).Hash) "Payload hash mismatch: $file"
}
$personal=Join-Path $target 'connection.json'
[IO.File]::WriteAllText($personal,'{"legacy":"preserve-during-upgrade"}')
$userFile=Join-Path $target 'user-notes.txt'
[IO.File]::WriteAllText($userFile,'preserve-during-uninstall')
# Simulate owned files left by the former flat installer; preserve user files.
foreach($name in @('raw-client.exe','wintun.dll','routes.ps1','README.md','LICENSE','setup-support.ps1')){[IO.File]::WriteAllText((Join-Path $target $name),'legacy owned file')}
Install-Test
foreach($name in @('raw-client.exe','wintun.dll','routes.ps1','README.md','LICENSE','setup-support.ps1')){Assert (-not(Test-Path -LiteralPath (Join-Path $target $name))) ('Upgrade retained flat file: '+$name)}
Assert ((Get-Content -LiteralPath $personal -Raw) -eq '{"legacy":"preserve-during-upgrade"}') 'Upgrade overwrote legacy settings'
$reg='HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\FturnRawInstallerTest'
Assert ((Get-ItemProperty -LiteralPath $reg).DisplayVersion -eq $expectedVersion) 'Uninstall registration missing'
$uninstaller=Join-Path $target 'Uninstall.exe'
$process=Start-Process -FilePath $uninstaller -ArgumentList '/S',("_?="+$target) -WindowStyle Hidden -Wait -PassThru
Assert ($process.ExitCode -eq 0) 'NSIS uninstall failed'
Assert (-not(Test-Path -LiteralPath (Join-Path $target 'FturnRaw.exe'))) 'Uninstall retained executable'
Assert (-not(Test-Path -LiteralPath $reg)) 'Uninstall retained registry entry'
Assert (Test-Path -LiteralPath $personal) 'Uninstall deleted legacy settings'
Assert (Test-Path -LiteralPath $userFile) 'Uninstall deleted an unrelated file'
foreach($name in $before.Keys){
 $path=Join-Path $appData $name
 $after=if(Test-Path -LiteralPath $path){(Get-FileHash -LiteralPath $path).Hash}else{''}
 Assert ($before[$name] -eq $after) "AppData changed: $name"
}
Write-Host 'NSIS install, upgrade, uninstall, payload hashes, and AppData preservation passed.'
