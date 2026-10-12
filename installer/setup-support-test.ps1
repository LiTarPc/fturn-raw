# Compile-time installer-test fixture. Never packaged in a production installer.
param(
 [Parameter(Mandatory=$true)][string]$SupportScript,
 [ValidateSet('Prepare','Uninstall')][string]$Action,
 [Parameter(Mandatory=$true)][string]$InstallDir,
 [string]$RecoveryScript
)
$ErrorActionPreference='Stop'
# Mock only OS/network operations. Execute the complete production script,
# including parameter binding and recovery-path initialization.
function Get-Process {return @()}
function Get-CimInstance {return @()}
function Get-ItemProperty {return [pscustomobject]@{pv='123.0.2420.1'}}
function Get-ScheduledTask {return @()}
function Unregister-ScheduledTask {throw 'Test attempted a real startup change'}
function Invoke-WebRequest {throw 'Test attempted a network download'}
function Start-Process {throw 'Test attempted a prerequisite install'}
if($RecoveryScript){
 & $SupportScript -Action $Action -InstallDir $InstallDir -RecoveryScript $RecoveryScript
}else{
 & $SupportScript -Action $Action -InstallDir $InstallDir
}
if(-not $?){exit 1}
Add-Content -LiteralPath (Join-Path $InstallDir 'installer-test-support.log') -Value $Action -Encoding Ascii
Write-Output 'Проверка подготовки завершена.'