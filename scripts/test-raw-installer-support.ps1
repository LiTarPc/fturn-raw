# Installer prerequisite and ownership regression checks with mocked OS/network APIs.
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
$tokens=$null;$errors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile((Join-Path $root 'installer/setup-support.ps1'),[ref]$tokens,[ref]$errors)
if($errors.Count){throw ($errors | Out-String)}
foreach($fn in $ast.EndBlock.Statements | Where-Object {$_ -is [Management.Automation.Language.FunctionDefinitionAst]}){
 . ([scriptblock]::Create($fn.Extent.Text))
}
function Assert($condition,$message){if(-not $condition){throw $message}}
$InstallDir='C:\Installer-Test'
$script:gui=@();$script:cores=@()
function Get-Process {return $script:gui}
function Get-CimInstance {return $script:cores}
Assert-ClientStopped
$script:gui=@([pscustomobject]@{Name='FturnRaw'})
$blocked=$false;try{Assert-ClientStopped}catch{$blocked=$true}
Assert $blocked 'A running GUI must block installation, without killing it'
$script:gui=@();$script:cores=@([pscustomobject]@{ExecutablePath='C:\Installer-Test\raw-client.exe'})
$blocked=$false;try{Assert-ClientStopped}catch{$blocked=$true}
Assert $blocked 'The installed running core must block uninstall'
$script:cores=@([pscustomobject]@{ExecutablePath='C:\Installer-Test\runtime\raw-client.exe'})
$blocked=$false;try{Assert-ClientStopped}catch{$blocked=$true}
Assert $blocked 'The nested running core must block uninstall'
$script:cores=@([pscustomobject]@{ExecutablePath='C:\Another-App\raw-client.exe'})
Assert-ClientStopped
$script:pv='0.0.0.0'
function Get-ItemProperty {return [pscustomobject]@{pv=$script:pv}}
Assert (-not(Test-WebView2)) 'Zero WebView version is absent'
$script:pv='invalid'
Assert (-not(Test-WebView2)) 'Invalid WebView version is absent'
$script:pv='123.0.2420.1'
Assert (Test-WebView2) 'Installed WebView version detected'
$script:downloads=0
function Invoke-WebRequest {param($Uri,$OutFile,[switch]$UseBasicParsing);$script:downloads++;[IO.File]::WriteAllText($OutFile,'test-bootstrapper')}
Install-WebView2
Assert ($script:downloads -eq 0) 'Existing WebView runtime must not be downloaded'
$script:pv='';$script:validSignature=$false;$script:executed=0
function Get-AuthenticodeSignature {
 param($LiteralPath)
 $status=if($script:validSignature){'Valid'}else{'NotSigned'}
 return [pscustomobject]@{Status=$status;SignerCertificate=[pscustomobject]@{Subject='CN=Microsoft Corporation, O=Microsoft Corporation, C=US'}}
}
function Start-Process {$script:executed++;$script:pv='123.0.2420.1';return [pscustomobject]@{ExitCode=0}}
$blocked=$false;try{Install-WebView2}catch{$blocked=$true}
Assert ($blocked -and $script:executed -eq 0) 'Unsigned bootstrapper must not be executed'
$script:validSignature=$true
Install-WebView2
Assert ($script:executed -eq 1 -and (Test-WebView2)) 'Signed Microsoft runtime installed and verified'
$script:removed=@()
function Get-ScheduledTask {
 @(
  [pscustomobject]@{TaskName='FturnRaw-owned';Description='fturn Raw user startup: C:\Data';Actions=@([pscustomobject]@{Execute='C:\Installer-Test\FturnRaw.exe'})},
  [pscustomobject]@{TaskName='FturnRaw-portable';Description='fturn Raw user startup: C:\Data';Actions=@([pscustomobject]@{Execute='C:\Portable\FturnRaw.exe'})},
  [pscustomobject]@{TaskName='FturnRaw-foreign';Description='foreign';Actions=@([pscustomobject]@{Execute='C:\Installer-Test\FturnRaw.exe'})},
  [pscustomobject]@{TaskName='OtherApp';Description='fturn Raw user startup: C:\Data';Actions=@([pscustomobject]@{Execute='C:\Installer-Test\FturnRaw.exe'})}
 )
}
function Unregister-ScheduledTask {param($TaskName,$TaskPath,[switch]$Confirm);$script:removed+=$TaskName}
Remove-InstalledStartupTasks
Assert ($script:removed.Count -eq 1 -and $script:removed[0] -eq 'FturnRaw-owned') 'Only this installed executable owns removed startup tasks'
Write-Host 'Installer prerequisite and ownership checks passed (no VPN or OS configuration changed).'
