param(
 [ValidateSet('Prepare','Uninstall')][string]$Action,
 [Parameter(Mandatory=$true)][string]$InstallDir
)
$ErrorActionPreference='Stop'
function Assert-ClientStopped {
 if(@(Get-Process -Name FturnRaw -ErrorAction SilentlyContinue).Count){
  throw 'Close fturn Raw using Exit in the tray menu before installing or uninstalling.'
 }
 $core=Join-Path $InstallDir 'raw-client.exe'
 foreach($process in @(Get-CimInstance Win32_Process -Filter "Name='raw-client.exe'")){
  if($process.ExecutablePath -and $process.ExecutablePath -ieq $core){
   throw 'The installed Raw core is still running. Exit the client first.'
  }
 }
}
function Test-WebView2 {
 $keys=@(
  'HKLM:\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}',
  'HKCU:\Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}'
 )
 foreach($key in $keys){
  $version=(Get-ItemProperty -LiteralPath $key -Name pv -ErrorAction SilentlyContinue).pv
  $parsed=$null
  if([version]::TryParse([string]$version,[ref]$parsed) -and $parsed -gt [version]'0.0.0.0'){return $true}
 }
 return $false
}
function Install-WebView2 {
 if(Test-WebView2){return}
 Write-Output 'Downloading Microsoft WebView2 Runtime...'
 $bootstrap=Join-Path ([IO.Path]::GetTempPath()) ('fturn-webview2-'+[guid]::NewGuid().ToString('N')+'.exe')
 try {
  [Net.ServicePointManager]::SecurityProtocol=[Net.SecurityProtocolType]::Tls12
  Invoke-WebRequest -UseBasicParsing -Uri 'https://go.microsoft.com/fwlink/p/?LinkId=2124703' -OutFile $bootstrap
  $signature=Get-AuthenticodeSignature -LiteralPath $bootstrap
  if($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Subject -notmatch '(?:^|,\s*)O=Microsoft Corporation(?:,|$)'){
   throw 'WebView2 bootstrapper does not have a valid Microsoft signature.'
  }
  $process=Start-Process -FilePath $bootstrap -ArgumentList '/silent','/install' -WindowStyle Hidden -Wait -PassThru
  if(-not(Test-WebView2)){throw ('WebView2 installation failed, exit code '+$process.ExitCode)}
 }finally{Remove-Item -LiteralPath $bootstrap -Force -ErrorAction SilentlyContinue}
}
function Remove-InstalledStartupTasks {
 $exe=Join-Path $InstallDir 'FturnRaw.exe'
 # A newer portable copy can own the shared AppData task. Only this executable is removed.
 foreach($task in @(Get-ScheduledTask -TaskPath '\' -ErrorAction Stop)){
  if($task.TaskName -notlike 'FturnRaw-*' -or @($task.Actions).Count -ne 1){continue}
  if($task.Actions[0].Execute -ine $exe){continue}
  if($task.Description -notlike 'fturn Raw user startup: *' -and $task.Description -ne ('fturn Raw startup: '+$exe)){continue}
  Unregister-ScheduledTask -TaskName $task.TaskName -TaskPath '\' -Confirm:$false
 }
}
Assert-ClientStopped
if($Action -eq 'Prepare'){
 Install-WebView2
}elseif($Action -eq 'Uninstall'){
 $state=Join-Path $InstallDir 'route-state.json'
 if(Test-Path -LiteralPath $state){
  & (Join-Path $InstallDir 'routes.ps1') -Action Remove -StateFile $state
 }
 Remove-InstalledStartupTasks
}else{throw 'An installer action is required.'}
