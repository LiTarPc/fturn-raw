param(
 [ValidateSet('Plan','Apply','Remove','Watch','Stats')][string]$Action,
 [ValidateSet('full','browser','tunnel')][string]$Mode='full',
 [string]$TUN='ftraw0',
 [string]$StateFile=(Join-Path $PSScriptRoot 'route-state.json'),
 [int]$OwnerPid=0,
 [int]$ClientPid=0
)
$ErrorActionPreference='Stop'
[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false)
$OutputEncoding=[Console]::OutputEncoding
function Save-State($state){
 $temp=$StateFile+'.new'
 $state | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $temp -Encoding UTF8
 Move-Item -LiteralPath $temp -Destination $StateFile -Force
}
function Get-PrivacyAddressFilters {
 # NetSecurity rejects zero-length prefixes on some Windows versions.
 # Two /1 networks cover IPv6 without using the rejected ::/0 spelling.
 return [ordered]@{IPv6=@('::/1','8000::/1');OutsideRaw=@('0.0.0.0-10.77.0.1','10.77.0.3-255.255.255.255')}
}
function Install-Privacy($state) {
 # Source-address filters also cover adapters which appear after connection.
 # Only DNS packets sourced from the TUN address may leave this computer.
 $rawIP=@(Get-NetIPAddress -InterfaceIndex $state.interfaceIndex -AddressFamily IPv4 | Where-Object {$_.IPAddress -eq '10.77.0.2'})
 if($rawIP.Count -ne 1){throw 'Raw adapter does not have the expected IPv4 address.'}
 $duplicates=@(Get-NetIPAddress -AddressFamily IPv4 | Where-Object {$_.IPAddress -eq '10.77.0.2' -and $_.InterfaceIndex -ne $state.interfaceIndex})
 if($duplicates.Count){throw 'Another adapter has the Raw IPv4 address; DNS protection cannot be installed.'}
 if(@(Get-NetFirewallProfile -PolicyStore ActiveStore | Where-Object {$_.Enabled -ne $true -or $_.AllowLocalFirewallRules -eq 'False'}).Count){throw 'Windows Firewall must be enabled for IPv6 and DNS protection.'}
 # A conflicting catch-all from another VPN is not overwritten.
 if(@(Get-DnsClientNrptRule | Where-Object {@($_.Namespace) -contains '.'}).Count){throw 'Another application has a catch-all DNS policy. Raw has not changed it.'}
 $state.privacyTag='FturnRaw-'+[Guid]::NewGuid().ToString('N')
 $state.firewallRules=@(($state.privacyTag+'-IPv6'),($state.privacyTag+'-DNS-UDP'),($state.privacyTag+'-DNS-TCP'))
 Save-State $state
 $filters=Get-PrivacyAddressFilters
 New-NetFirewallRule -Name $state.firewallRules[0] -DisplayName 'fturn Raw: block IPv6' -Group $state.privacyTag -Direction Outbound -Action Block -Enabled True -Profile Any -RemoteAddress $filters.IPv6 -Protocol Any -PolicyStore PersistentStore | Out-Null
 $outsideRaw=$filters.OutsideRaw
 foreach($i in 1,2){
  $protocol=if($i -eq 1){'UDP'}else{'TCP'}
  New-NetFirewallRule -Name $state.firewallRules[$i] -DisplayName ('fturn Raw: block DNS outside tunnel ('+$protocol+')') -Group $state.privacyTag -Direction Outbound -Action Block -Enabled True -Profile Any -Protocol $protocol -RemotePort 53,853 -LocalAddress $outsideRaw -PolicyStore PersistentStore | Out-Null
 }
 foreach($name in $state.firewallRules){
  $effective=@(Get-NetFirewallRule -PolicyStore ActiveStore -Name $name -ErrorAction SilentlyContinue | Where-Object {$_.Enabled -eq 'True' -and $_.Action -eq 'Block'})
  if($effective.Count -ne 1){throw 'Windows policy did not activate Raw leak protection.'}
 }
 Add-DnsClientNrptRule -Namespace '.' -NameServers '1.1.1.1','9.9.9.9' -Comment $state.privacyTag -DisplayName 'fturn Raw DNS' | Out-Null
 Clear-DnsClientCache
}
function Remove-Privacy($state) {
 if($state.privacyTag -and $state.privacyTag -match '^FturnRaw-[a-f0-9]{32}$'){
  foreach($rule in @(Get-DnsClientNrptRule | Where-Object {$_.Comment -eq $state.privacyTag})){
   Remove-DnsClientNrptRule -Name $rule.Name -Force
  }
  foreach($name in @($state.firewallRules)){
   if($name -like ($state.privacyTag+'-*')){
    Get-NetFirewallRule -PolicyStore PersistentStore -Name $name -ErrorAction SilentlyContinue | Where-Object {$_.Group -eq $state.privacyTag} | Remove-NetFirewallRule -ErrorAction Stop
   }
  }
  Clear-DnsClientCache
 }
}
function Remove-OwnedRoutesUnlocked {
 if(-not(Test-Path -LiteralPath $StateFile)){return}
 $state=Get-Content -LiteralPath $StateFile -Raw | ConvertFrom-Json
 $adapter=Get-NetAdapter -IncludeHidden | Where-Object {$_.ifIndex -eq $state.interfaceIndex -and $_.InterfaceGuid.ToString() -eq $state.interfaceGuid} | Select-Object -First 1
 if($adapter){
  foreach($prefix in $state.prefixes){
   Get-NetRoute -DestinationPrefix $prefix -InterfaceIndex $state.interfaceIndex -ErrorAction SilentlyContinue | Where-Object {$_.NextHop -eq '0.0.0.0' -and $_.RouteMetric -eq 5} | Remove-NetRoute -Confirm:$false -ErrorAction Stop
  }
  if($state.dnsChanged){
   if(@($state.previousDNS).Count -gt 0){Set-DnsClientServerAddress -InterfaceIndex $state.interfaceIndex -ServerAddresses @($state.previousDNS)}else{Set-DnsClientServerAddress -InterfaceIndex $state.interfaceIndex -ResetServerAddresses}
  }
  if($state.metricChanged){Set-NetIPInterface -InterfaceIndex $state.interfaceIndex -AddressFamily IPv4 -AutomaticMetric $state.previousAutomaticMetric -InterfaceMetric $state.previousMetric}
 }
 # Protection is independent of whether the TUN still exists after a crash.
 Remove-Privacy $state
 Remove-Item -LiteralPath $StateFile -Force
}
# One lock per state file prevents the UI and crash guard from racing cleanup.
$lockPath=[IO.Path]::GetFullPath($StateFile).ToLowerInvariant()
$lockHash=[Security.Cryptography.SHA256]::Create().ComputeHash([Text.Encoding]::UTF8.GetBytes($lockPath))
$lockName='Local\FturnRawRoutes_'+([BitConverter]::ToString($lockHash).Replace('-',''))
$stateMutex=[Threading.Mutex]::new($false,$lockName)
function Enter-StateLock {
 try {if(-not $stateMutex.WaitOne(45000)){throw 'Timed out waiting for route-state lock'}}catch [Threading.AbandonedMutexException]{}
}
function Remove-OwnedRoutes {
 Enter-StateLock
 try {Remove-OwnedRoutesUnlocked}finally{$stateMutex.ReleaseMutex()}
}
if($Action -eq 'Stats'){
 $s=Get-NetAdapterStatistics -Name $TUN
 [ordered]@{rx=[uint64]$s.ReceivedBytes;tx=[uint64]$s.SentBytes} | ConvertTo-Json -Compress
 exit
}
if($Action -eq 'Plan'){
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
if($Action -eq 'Watch'){
 # Started before Apply, so a crash during route/privacy installation is guarded.
 $deadline=[DateTime]::UtcNow.AddSeconds(45)
 $state=$null
 while([DateTime]::UtcNow -lt $deadline){
  if(-not(Get-Process -Id $OwnerPid -ErrorAction SilentlyContinue) -or -not(Get-Process -Id $ClientPid -ErrorAction SilentlyContinue)){exit}
  if(Test-Path -LiteralPath $StateFile){
   $candidate=Get-Content -LiteralPath $StateFile -Raw | ConvertFrom-Json
   if($candidate.ownerPid -eq $OwnerPid -and $candidate.clientPid -eq $ClientPid){$state=$candidate;break}
  }
  Start-Sleep -Milliseconds 200
 }
 if(-not $state){exit}
 while($true){
  $owner=Get-Process -Id $OwnerPid -ErrorAction SilentlyContinue
  if(-not $owner -or $owner.StartTime.ToUniversalTime().Ticks.ToString() -ne $state.ownerStarted){break}
  $client=Get-Process -Id $ClientPid -ErrorAction SilentlyContinue
  if(-not $client -or $client.StartTime.ToUniversalTime().Ticks.ToString() -ne $state.clientStarted){break}
  Start-Sleep -Seconds 2
 }
 Enter-StateLock
 try {
  if(Test-Path -LiteralPath $StateFile){
   $current=Get-Content -LiteralPath $StateFile -Raw | ConvertFrom-Json
   # An old guard must never remove routes of a new connection.
   if($current.clientStarted -eq $state.clientStarted -and $current.clientPid -eq $state.clientPid -and $current.ownerStarted -eq $state.ownerStarted){Remove-OwnedRoutesUnlocked}
  }
 }finally{$stateMutex.ReleaseMutex()}
 $client=Get-Process -Id $ClientPid -ErrorAction SilentlyContinue
 if($client -and $client.StartTime.ToUniversalTime().Ticks.ToString() -eq $state.clientStarted -and $client.Path -eq $state.clientPath){Stop-Process -Id $ClientPid -ErrorAction SilentlyContinue}
 exit
}
# Apply is called only after an authenticated Raw worker is ready.
Enter-StateLock
try {
Remove-OwnedRoutesUnlocked
$adapter=Get-NetAdapter -Name $TUN
$iface=Get-NetIPInterface -InterfaceIndex $adapter.ifIndex -AddressFamily IPv4
$dns=(Get-DnsClientServerAddress -InterfaceIndex $adapter.ifIndex -AddressFamily IPv4).ServerAddresses
$state=[ordered]@{version=2;privacyTag=$null;firewallRules=@();interfaceIndex=[int]$adapter.ifIndex;interfaceGuid=$adapter.InterfaceGuid.ToString();prefixes=@();previousDNS=@($dns);dnsChanged=$false;previousMetric=$iface.InterfaceMetric;previousAutomaticMetric=$iface.AutomaticMetric.ToString();metricChanged=$false;ownerPid=$OwnerPid;ownerStarted=(Get-Process -Id $OwnerPid).StartTime.ToUniversalTime().Ticks.ToString();clientPid=$ClientPid;clientStarted=(Get-Process -Id $ClientPid).StartTime.ToUniversalTime().Ticks.ToString();clientPath=(Get-Process -Id $ClientPid).Path}
Save-State $state
try {
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
 [ordered]@{mode=$Mode;interfaceIndex=[int]$adapter.ifIndex;routeCount=@($state.prefixes).Count} | ConvertTo-Json -Compress
} catch {
 try {Remove-OwnedRoutesUnlocked}catch{Write-Warning ("Rollback incomplete; state retained: "+$_.Exception.Message)}
 throw
}
} finally {$stateMutex.ReleaseMutex();$stateMutex.Dispose()}
