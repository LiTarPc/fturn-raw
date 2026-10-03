param(
 [ValidateSet('Plan','Apply','Remove','Recover','Watch','Stats')][string]$Action,
 [ValidateSet('full','browser','tunnel')][string]$Mode='full',
 [string]$TUN='ftraw0',
 [string]$StateFile=(Join-Path $env:LOCALAPPDATA 'fturn-raw/network/route-state.json'),
 [string]$LegacyDirectory=$PSScriptRoot,
 [int]$OwnerPid=0,
 [int]$ClientPid=0
)
$ErrorActionPreference='Stop'
[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false)
$OutputEncoding=[Console]::OutputEncoding
function Save-State($state){
 $directory=Split-Path -Parent $StateFile
 if(-not(Test-Path -LiteralPath $directory)){New-Item -ItemType Directory -Path $directory -Force | Out-Null}
 $temp=$StateFile+'.new'
 $json=$state | ConvertTo-Json -Depth 6
 [IO.File]::WriteAllText($temp,$json,[Text.UTF8Encoding]::new($false))
 if(Test-Path -LiteralPath $StateFile){[IO.File]::Replace($temp,$StateFile,[NullString]::Value)}else{[IO.File]::Move($temp,$StateFile)}
}
function Read-State {
 if(Test-Path -LiteralPath $StateFile){return Get-Content -LiteralPath $StateFile -Raw -Encoding UTF8 | ConvertFrom-Json}
 return $null
}
function Test-ProcessIdentity($processId,$started){
 if(-not $processId -or -not $started){return $false}
 $process=Get-Process -Id $processId -ErrorAction SilentlyContinue
 if(-not $process){return $false}
 # If Windows denies inspection, keep the session rather than deleting its rules.
 try {return $process.StartTime.ToUniversalTime().Ticks.ToString() -eq $started}catch{return $true}
}
function Test-SessionAlive($state){
 return (Test-ProcessIdentity $state.ownerPid $state.ownerStarted) -and (Test-ProcessIdentity $state.clientPid $state.clientStarted)
}
function Test-SameSession($a,$b){
 return $a -and $b -and $a.ownerPid -eq $b.ownerPid -and $a.ownerStarted -eq $b.ownerStarted -and $a.clientPid -eq $b.clientPid -and $a.clientStarted -eq $b.clientStarted
}
function Get-PrivacyAddressFilters {
 return [ordered]@{IPv6=@('::/1','8000::/1');OutsideRaw=@('0.0.0.0-10.77.0.1','10.77.0.3-255.255.255.255')}
}
function Install-Privacy($state) {
 $rawIP=@(Get-NetIPAddress -InterfaceIndex $state.interfaceIndex -AddressFamily IPv4 | Where-Object {$_.IPAddress -eq '10.77.0.2'})
 if($rawIP.Count -ne 1){throw 'Raw adapter does not have the expected IPv4 address.'}
 $duplicates=@(Get-NetIPAddress -AddressFamily IPv4 | Where-Object {$_.IPAddress -eq '10.77.0.2' -and $_.InterfaceIndex -ne $state.interfaceIndex})
 if($duplicates.Count){throw 'Another adapter has the Raw IPv4 address; DNS protection cannot be installed.'}
 if(@(Get-NetFirewallProfile -PolicyStore ActiveStore | Where-Object {$_.Enabled -ne $true -or $_.AllowLocalFirewallRules -eq 'False'}).Count){throw 'Windows Firewall must be enabled for IPv6 and DNS protection.'}
 # Recover removes abandoned Raw rules before the core starts. A real foreign
 # catch-all remains a conflict: duplicate root policies are not safe to merge.
 if(@(Get-DnsClientNrptRule | Where-Object {@($_.Namespace) -contains '.'}).Count){throw 'An active DNS catch-all policy conflicts with Raw. It was preserved; Raw rolled back its changes.'}
 $state.privacyTag='FturnRaw-'+[Guid]::NewGuid().ToString('N')
 $state.firewallRules=@(($state.privacyTag+'-IPv6'),($state.privacyTag+'-DNS-UDP'),($state.privacyTag+'-DNS-TCP'))
 Save-State $state
 $filters=Get-PrivacyAddressFilters
 New-NetFirewallRule -Name $state.firewallRules[0] -DisplayName 'fturn Raw: block IPv6' -Group $state.privacyTag -Direction Outbound -Action Block -Enabled True -Profile Any -RemoteAddress $filters.IPv6 -Protocol Any -PolicyStore PersistentStore | Out-Null
 foreach($i in 1,2){
  $protocol=if($i -eq 1){'UDP'}else{'TCP'}
  New-NetFirewallRule -Name $state.firewallRules[$i] -DisplayName ('fturn Raw: block DNS outside tunnel ('+$protocol+')') -Group $state.privacyTag -Direction Outbound -Action Block -Enabled True -Profile Any -Protocol $protocol -RemotePort 53,853 -LocalAddress $filters.OutsideRaw -PolicyStore PersistentStore | Out-Null
 }
 foreach($name in $state.firewallRules){
  $effective=@(Get-NetFirewallRule -PolicyStore ActiveStore -Name $name -ErrorAction SilentlyContinue | Where-Object {$_.Enabled -eq 'True' -and $_.Action -eq 'Block'})
  if($effective.Count -ne 1){throw 'Windows policy did not activate Raw leak protection.'}
 }
 Add-DnsClientNrptRule -Namespace '.' -NameServers '1.1.1.1','9.9.9.9' -Comment $state.privacyTag -DisplayName 'fturn Raw DNS' | Out-Null
 Clear-DnsClientCache
}
function Remove-Privacy($state) {
 $failures=[Collections.Generic.List[string]]::new()
 if($state.privacyTag -and $state.privacyTag -match '^FturnRaw-[a-f0-9]{32}$'){
  try {
   foreach($rule in @(Get-DnsClientNrptRule | Where-Object {$_.Comment -eq $state.privacyTag})){
    try {Remove-DnsClientNrptRule -Name $rule.Name -Force}catch{$failures.Add($_.Exception.Message)}
   }
  }catch{$failures.Add($_.Exception.Message)}
  foreach($name in @($state.firewallRules)){
   if($name -in @(($state.privacyTag+'-IPv6'),($state.privacyTag+'-DNS-UDP'),($state.privacyTag+'-DNS-TCP'))){
    try {Get-NetFirewallRule -PolicyStore PersistentStore -ErrorAction Stop | Where-Object {$_.Name -eq $name -and $_.Group -eq $state.privacyTag} | Remove-NetFirewallRule -ErrorAction Stop}catch{$failures.Add($_.Exception.Message)}
   }
  }
  try {Clear-DnsClientCache}catch{$failures.Add($_.Exception.Message)}
 }
 if($failures.Count){throw ($failures -join '; ')}
}
function Stop-OrphanedCore($state){
 if(-not $state.clientPid -or (Test-ProcessIdentity $state.ownerPid $state.ownerStarted)){return}
 $client=Get-Process -Id $state.clientPid -ErrorAction SilentlyContinue
 if(-not $client){return}
 # Inspection failures preserve the journal; they are never permission to kill.
 $started=$client.StartTime.ToUniversalTime().Ticks.ToString()
 if($started -ne $state.clientStarted){return}
 if(-not $client.Path -or $client.Path -ne $state.clientPath){throw 'Cannot verify the orphaned Raw core executable; journal retained.'}
 Stop-Process -Id $state.clientPid -ErrorAction Stop
}
function Remove-OwnedRoutesUnlocked {
 $state=Read-State
 if(-not $state){return}
 if((Test-SessionAlive $state) -and $state.ownerPid -ne $OwnerPid){throw 'Another Raw session is still active. Its network rules were preserved.'}
 $failures=[Collections.Generic.List[string]]::new()
 # Persistent DNS/IPv6 protection must be released even if another recovery step fails.
 try {Stop-OrphanedCore $state}catch{$failures.Add($_.Exception.Message)}
 try {Remove-Privacy $state}catch{$failures.Add($_.Exception.Message)}
 try {
  $adapter=Get-NetAdapter -IncludeHidden | Where-Object {$_.ifIndex -eq $state.interfaceIndex -and $_.InterfaceGuid.ToString() -eq $state.interfaceGuid} | Select-Object -First 1
  if($adapter){
   foreach($prefix in $state.prefixes){
    try {Get-NetRoute -AddressFamily IPv4 -ErrorAction Stop | Where-Object {$_.InterfaceIndex -eq $state.interfaceIndex -and $_.DestinationPrefix -eq $prefix -and $_.NextHop -eq '0.0.0.0' -and $_.RouteMetric -eq 5} | Remove-NetRoute -Confirm:$false -ErrorAction Stop}catch{$failures.Add($_.Exception.Message)}
   }
   if($state.dnsChanged){
    try {
     if(@($state.previousDNS).Count -gt 0){Set-DnsClientServerAddress -InterfaceIndex $state.interfaceIndex -ServerAddresses @($state.previousDNS)}else{Set-DnsClientServerAddress -InterfaceIndex $state.interfaceIndex -ResetServerAddresses}
    }catch{$failures.Add($_.Exception.Message)}
   }
   if($state.metricChanged){
    try {Set-NetIPInterface -InterfaceIndex $state.interfaceIndex -AddressFamily IPv4 -AutomaticMetric $state.previousAutomaticMetric -InterfaceMetric $state.previousMetric}catch{$failures.Add($_.Exception.Message)}
   }
  }
 }catch{$failures.Add($_.Exception.Message)}
 if($failures.Count){throw ('Network recovery incomplete; journal retained: '+($failures -join '; '))}
 Remove-Item -LiteralPath $StateFile -Force
}
function Remove-OrphanedPrivacy {
 # Old builds kept journals next to the EXE; moving that folder can lose them.
 # Never sweep even an owned tag while any Raw core could be using it.
 if(@(Get-Process -Name raw-client -ErrorAction SilentlyContinue).Count){return}
 $failures=[Collections.Generic.List[string]]::new()
 foreach($rule in @(Get-DnsClientNrptRule)){
  if($rule.Comment -notmatch '^FturnRaw-[a-f0-9]{32}$' -or $rule.DisplayName -ne 'fturn Raw DNS' -or @($rule.Namespace).Count -ne 1 -or $rule.Namespace[0] -ne '.'){continue}
  $servers=@($rule.NameServers | ForEach-Object {$_.ToString()})
  if($servers.Count -ne 2 -or $servers -notcontains '1.1.1.1' -or $servers -notcontains '9.9.9.9'){continue}
  try {Remove-DnsClientNrptRule -Name $rule.Name -Force}catch{$failures.Add($_.Exception.Message)}
 }
 foreach($rule in @(Get-NetFirewallRule -PolicyStore PersistentStore -ErrorAction Stop)){
  if($rule.Group -notmatch '^FturnRaw-[a-f0-9]{32}$'){continue}
  if($rule.Name -notin @(($rule.Group+'-IPv6'),($rule.Group+'-DNS-UDP'),($rule.Group+'-DNS-TCP'))){continue}
  try {$rule | Remove-NetFirewallRule -ErrorAction Stop}catch{$failures.Add($_.Exception.Message)}
 }
 try {Clear-DnsClientCache}catch{$failures.Add($_.Exception.Message)}
 if($failures.Count){throw ('Orphan recovery incomplete: '+($failures -join '; '))}
}
function Recover-NetworkUnlocked {
 $currentFile=$StateFile
 $paths=@($currentFile,(Join-Path $LegacyDirectory 'route-state.json'))
 if((Split-Path -Leaf $LegacyDirectory) -eq 'runtime'){$paths+=Join-Path (Split-Path -Parent $LegacyDirectory) 'route-state.json'}
 $failures=[Collections.Generic.List[string]]::new()
 try {
  foreach($path in $paths | Select-Object -Unique){
   $script:StateFile=$path
   try {
    $state=Read-State
    if($state -and (Test-SessionAlive $state)){throw 'Another Raw session is still active. Its network rules were preserved.'}
    Remove-OwnedRoutesUnlocked
   }catch{$failures.Add($_.Exception.Message)}
  }
 }finally{$script:StateFile=$currentFile}
 try {Remove-OrphanedPrivacy}catch{$failures.Add($_.Exception.Message)}
 if($failures.Count){throw ($failures -join '; ')}
}
# All new portable/installed copies coordinate through the same mutex and journal.
$stateMutex=[Threading.Mutex]::new($false,'Local\FturnRawRoutes-v3')
function Enter-StateLock {
 try {if(-not $stateMutex.WaitOne(45000)){throw 'Timed out waiting for route-state lock'}}catch [Threading.AbandonedMutexException]{}
}
function Remove-OwnedRoutes {
 Enter-StateLock
 try {Remove-OwnedRoutesUnlocked}finally{$stateMutex.ReleaseMutex()}
}
function Write-RecoveryFailure($message){
 # The UI may already be dead, closing its stdout pipe. Recovery must not
 # depend on a console or pipe still having a reader.
 try {
  $log=Join-Path (Split-Path -Parent $StateFile) 'recovery.log'
  if((Test-Path -LiteralPath $log) -and (Get-Item -LiteralPath $log).Length -gt 131072){[IO.File]::WriteAllText($log,'')}
  [IO.File]::AppendAllText($log,([DateTime]::UtcNow.ToString('o')+' '+$message+[Environment]::NewLine),[Text.UTF8Encoding]::new($false))
 }catch{}
}
function Watch-Connection {
 # UI must receive this acknowledgement before launching Apply.
 Write-Output 'READY'
 $deadline=[DateTime]::UtcNow.AddSeconds(45)
 $state=$null
 while([DateTime]::UtcNow -lt $deadline){
  Enter-StateLock
  try {
   $candidate=Read-State
   if($candidate -and $candidate.ownerPid -eq $OwnerPid -and $candidate.clientPid -eq $ClientPid){$state=$candidate}
   # Read the journal before checking death: Apply may have crashed after saving it.
   if(-not $state -and (-not(Get-Process -Id $OwnerPid -ErrorAction SilentlyContinue) -or -not(Get-Process -Id $ClientPid -ErrorAction SilentlyContinue))){return}
  }finally{$stateMutex.ReleaseMutex()}
  if($state){break}
  Start-Sleep -Milliseconds 200
 }
 if(-not $state){return}
 while(Test-SessionAlive $state){
  Enter-StateLock
  try {if(-not(Test-SameSession (Read-State) $state)){return}}finally{$stateMutex.ReleaseMutex()}
  Start-Sleep -Seconds 2
 }
 while($true){
  Enter-StateLock
  try {
   if(-not(Test-SameSession (Read-State) $state)){return}
   Remove-OwnedRoutesUnlocked
   return
  }catch{Write-RecoveryFailure $_.Exception.Message}finally{$stateMutex.ReleaseMutex()}
  # A transient cmdlet failure must not terminate the only crash guard.
  Start-Sleep -Seconds 2
 }
}
if($Action -eq 'Recover'){
 Enter-StateLock
 try {Recover-NetworkUnlocked}finally{$stateMutex.ReleaseMutex();$stateMutex.Dispose()}
 exit
}
if($Action -eq 'Stats'){
 $s=Get-NetAdapterStatistics -Name $TUN
 [ordered]@{rx=[uint64]$s.ReceivedBytes;tx=[uint64]$s.SentBytes} | ConvertTo-Json -Compress
 exit
}
if($Action -eq 'Plan'){
 Enter-StateLock
 try {Recover-NetworkUnlocked}finally{$stateMutex.ReleaseMutex()}

 # Use the existing connection as the underlay, including an already active VPN.
 $selected=Find-NetRoute -RemoteIPAddress 1.0.0.1
 $route=$selected | Where-Object {$_.DestinationPrefix} | Select-Object -First 1
 if(-not $route){throw 'Не найдено текущее IPv4-подключение.'}
 $adapter=Get-NetAdapter -IncludeHidden | Where-Object {$_.ifIndex -eq $route.InterfaceIndex} | Select-Object -First 1
 if(-not $adapter -or $adapter.Status -ne 'Up' -or $adapter.Name -eq $TUN){throw 'Не найден внешний адаптер для Raw. Восстановите предыдущее подключение.'}
 [ordered]@{controlInterface=[int]$route.InterfaceIndex;adapter=$adapter.Name;gateway=$route.NextHop} | ConvertTo-Json -Compress
 exit
}
if($Action -eq 'Remove'){Remove-OwnedRoutes;exit}
if($Action -eq 'Watch'){Watch-Connection;exit}
# Apply is called only after an authenticated Raw worker is ready.
Enter-StateLock
try {
Remove-OwnedRoutesUnlocked
if(-not(Get-Process -Id $OwnerPid -ErrorAction SilentlyContinue) -or -not(Get-Process -Id $ClientPid -ErrorAction SilentlyContinue)){throw 'Raw owner or core exited before Apply.'}
$adapter=Get-NetAdapter -Name $TUN
$iface=Get-NetIPInterface -InterfaceIndex $adapter.ifIndex -AddressFamily IPv4
$dns=(Get-DnsClientServerAddress -InterfaceIndex $adapter.ifIndex -AddressFamily IPv4).ServerAddresses
$state=[ordered]@{version=3;privacyTag=$null;firewallRules=@();interfaceIndex=[int]$adapter.ifIndex;interfaceGuid=$adapter.InterfaceGuid.ToString();prefixes=@();previousDNS=@($dns);dnsChanged=$false;previousMetric=$iface.InterfaceMetric;previousAutomaticMetric=$iface.AutomaticMetric.ToString();metricChanged=$false;ownerPid=$OwnerPid;ownerStarted=(Get-Process -Id $OwnerPid).StartTime.ToUniversalTime().Ticks.ToString();clientPid=$ClientPid;clientStarted=(Get-Process -Id $ClientPid).StartTime.ToUniversalTime().Ticks.ToString();clientPath=(Get-Process -Id $ClientPid).Path}
Save-State $state
try {
 if(-not(Test-SessionAlive $state)){throw 'Raw owner or core exited during Apply.'}
 $prefixes=@()
 if($Mode -eq 'full'){
  Install-Privacy $state
  # Override the existing VPN's broad routes without deleting or changing them.
  $bits=1
  foreach($route in Get-NetRoute -AddressFamily IPv4){
   if($route.InterfaceIndex -eq $adapter.ifIndex){continue}
   $length=[int]($route.DestinationPrefix.Split('/')[1])
   if($length -gt 0 -and $length -le 7 -and [int]($route.DestinationPrefix.Split('.')[0]) -lt 224){$bits=[Math]::Max($bits,$length+1)}
  }
  $count=[int][Math]::Pow(2,$bits)
  $step=[uint64][Math]::Pow(2,32-$bits)
  for($i=0;$i -lt $count;$i++){
   $value=[uint64]$i*$step
   if(($value -shr 24) -ge 224){continue}
   $prefixes+=('{0}.{1}.{2}.{3}/{4}' -f (($value -shr 24) -band 255),(($value -shr 16) -band 255),(($value -shr 8) -band 255),($value -band 255),$bits)
  }
  # Explicit DNS routes override more-specific routes kept by the underlay VPN.
  $prefixes+=@('1.1.1.1/32','9.9.9.9/32')
  $state.metricChanged=$true;Save-State $state
  Set-NetIPInterface -InterfaceIndex $adapter.ifIndex -AddressFamily IPv4 -AutomaticMetric Disabled -InterfaceMetric 1
  $state.dnsChanged=$true;Save-State $state
  Set-DnsClientServerAddress -InterfaceIndex $adapter.ifIndex -ServerAddresses '1.1.1.1','9.9.9.9'
 } elseif($Mode -eq 'browser'){
  $prefixes=@(Resolve-DnsName browserleaks.com -Type A | Where-Object {$_.IPAddress -match '^\d+\.'} | ForEach-Object {$_.IPAddress+'/32'} | Select-Object -Unique)
 }
 foreach($prefix in $prefixes){
  $existing=Get-NetRoute -DestinationPrefix $prefix -InterfaceIndex $adapter.ifIndex -ErrorAction SilentlyContinue
  if(-not $existing){
   # Record first, so rollback can recover if the process exits during the cmdlet.
   $state.prefixes+=@($prefix);Save-State $state
   New-NetRoute -DestinationPrefix $prefix -InterfaceIndex $adapter.ifIndex -NextHop '0.0.0.0' -RouteMetric 5 -PolicyStore ActiveStore | Out-Null
  }
 }
 if(-not(Test-SessionAlive $state)){throw 'Raw owner or core exited during Apply.'}
 [ordered]@{mode=$Mode;interfaceIndex=[int]$adapter.ifIndex;routeCount=@($state.prefixes).Count} | ConvertTo-Json -Compress
} catch {
 try {Remove-OwnedRoutesUnlocked}catch{Write-Warning ("Rollback incomplete; state retained: "+$_.Exception.Message)}
 throw
}
} finally {$stateMutex.ReleaseMutex();$stateMutex.Dispose()}
