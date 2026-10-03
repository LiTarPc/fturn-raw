# Isolated regression checks: all network cmdlets below are mocked.
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
$tokens=$null;$errors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile((Join-Path $root 'ui/windows/routes.ps1'),[ref]$tokens,[ref]$errors)
if($errors.Count){throw ($errors | Out-String)}
foreach($fn in $ast.EndBlock.Statements | Where-Object {$_ -is [Management.Automation.Language.FunctionDefinitionAst]}){
 . ([scriptblock]::Create($fn.Extent.Text))
}
function Assert($condition,$message){if(-not $condition){throw $message}}
$testDir=Join-Path ([IO.Path]::GetTempPath()) ('fturn-route-test-'+[Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $testDir | Out-Null
$StateFile=Join-Path $testDir 'state.json'
$LegacyDirectory=$testDir
$OwnerPid=10;$ClientPid=20
$stateMutex=[Threading.Mutex]::new($false)
function Get-NetIPAddress($InterfaceIndex,$AddressFamily){[pscustomobject]@{IPAddress='10.77.0.2';InterfaceIndex=99}}
function Get-NetFirewallProfile($PolicyStore){[pscustomobject]@{Enabled=$true;AllowLocalFirewallRules='True'}}
function Get-NetAdapter {if($script:failAdapter){throw 'Injected adapter failure'};return $script:adapter}
function Get-Process($Id,$Name,$ErrorAction){if($Name){return $script:cores};return @($script:processes | Where-Object {$_.Id -eq $Id})}
function Stop-Process($Id,$ErrorAction){$script:stopped+=@($Id)}
function Start-Sleep($Seconds,$Milliseconds){$script:sleeps++;$script:failRemove=$false}
function Get-DnsClientNrptRule {return $script:nrpt.ToArray()}
function Add-DnsClientNrptRule($Namespace,$NameServers,$Comment,$DisplayName){
 $script:nrpt.Add([pscustomobject]@{Name='owned-nrpt';Namespace=@($Namespace);NameServers=$NameServers;Comment=$Comment;DisplayName=$DisplayName})
 if($script:failNRPT){throw 'Injected NRPT failure after creation'}
}
function Remove-DnsClientNrptRule($Name,[switch]$Force){
 if($script:failRemove){throw 'Injected NRPT removal failure'}
 foreach($r in @($script:nrpt.ToArray())){if($r.Name -eq $Name){[void]$script:nrpt.Remove($r)}}
}
function Clear-DnsClientCache {}
function New-NetFirewallRule($Name,$DisplayName,$Group,$Direction,$Action,$Enabled,$Profile,$RemoteAddress,$Protocol,$PolicyStore,$RemotePort,$LocalAddress){
 foreach($prefix in @($RemoteAddress)+@($LocalAddress)) {
  if($prefix -match '/0$'){throw 'Injected Windows validation: zero-length firewall prefix is invalid'}
 }
 $saved=Get-Content -LiteralPath $StateFile -Raw | ConvertFrom-Json
 Assert ($saved.firewallRules -contains $Name) 'Firewall rule was not journaled before creation'
 $script:rules.Add([pscustomobject]@{Name=$Name;Group=$Group;Direction=$Direction;Action=$Action;Enabled=$Enabled;Profile=$Profile;RemoteAddress=$RemoteAddress;Protocol=$Protocol;RemotePort=$RemotePort;LocalAddress=$LocalAddress})
 if($script:failFirewall -and $script:rules.Count -eq 3){throw 'Injected firewall failure after creation'}
}
function Get-NetFirewallRule($Name,$PolicyStore,$ErrorAction){return @($script:rules.ToArray() | Where-Object {-not $Name -or $_.Name -eq $Name})}
function Remove-NetFirewallRule {
 param([Parameter(ValueFromPipeline=$true)]$InputObject)
 process {if($script:failRemove -and $InputObject.Name -like '*-IPv6'){throw 'Injected IPv6 removal failure'};[void]$script:rules.Remove($InputObject)}
}
function Get-NetRoute($AddressFamily,$ErrorAction){return $script:routes.ToArray()}
function Remove-NetRoute {
 param([Parameter(ValueFromPipeline=$true)]$InputObject,[switch]$Confirm)
 process {[void]$script:routes.Remove($InputObject)}
}
function Set-DnsClientServerAddress($InterfaceIndex,$ServerAddresses,[switch]$ResetServerAddresses){if($script:failDNS){throw 'Injected DNS restore failure'};$script:dnsRestored=$true}
function Set-NetIPInterface($InterfaceIndex,$AddressFamily,$AutomaticMetric,$InterfaceMetric){$script:metricRestored=$true}
function Reset-Mocks {
 $script:rules=[Collections.Generic.List[object]]::new()
 $script:rules.Add([pscustomobject]@{Name='another-VPN';Group='other'})
 $script:nrpt=[Collections.Generic.List[object]]::new()
 $script:nrpt.Add([pscustomobject]@{Name='other-nrpt';Namespace=@('.example.invalid');Comment='other'})
 $script:failFirewall=$false;$script:failNRPT=$false
 $script:failDNS=$false;$script:dnsRestored=$false;$script:metricRestored=$false;$script:routes=[Collections.Generic.List[object]]::new()
 $script:failRemove=$false;$script:failAdapter=$false;$script:adapter=$null;$script:cores=@();$script:processes=@();$script:stopped=@();$script:sleeps=0
}
function New-TestState {return [ordered]@{version=3;interfaceIndex=99;interfaceGuid='fake';prefixes=@();privacyTag=$null;firewallRules=@();ownerPid=10;ownerStarted='1';clientPid=20;clientStarted='2';clientPath='fake.exe';dnsChanged=$false;metricChanged=$false}}
try {
 Reset-Mocks
 $state=New-TestState;Save-State $state;Install-Privacy $state
 Assert ($rules.Count -eq 4) 'Expected IPv6 and both DNS protocol rules'
 Assert ($rules[1].RemoteAddress.Count -eq 2 -and $rules[1].RemoteAddress[0] -eq '::/1' -and $rules[1].RemoteAddress[1] -eq '8000::/1' -and $rules[1].Protocol -eq 'Any') 'Incomplete IPv6 coverage'
 foreach($r in $rules.ToArray() | Select-Object -Skip 2){
  Assert ($r.LocalAddress.Count -eq 2 -and $r.LocalAddress[0] -eq '0.0.0.0-10.77.0.1' -and $r.LocalAddress[1] -eq '10.77.0.3-255.255.255.255') 'DNS block does not cover all sources outside Raw'
  Assert ($r.RemotePort -contains 53 -and $r.RemotePort -contains 853 -and $r.Direction -eq 'Outbound' -and $r.Action -eq 'Block') 'Incorrect DNS block'
 }
 Assert ($nrpt.Count -eq 2 -and $nrpt[1].Namespace[0] -eq '.' -and $nrpt[1].NameServers.Count -eq 2) 'DNS catch-all missing'
 # Simulate crash: TUN no longer exists, but privacy must still be removed.
 Remove-OwnedRoutesUnlocked
 Assert (-not(Test-Path -LiteralPath $StateFile)) 'Cleanup did not clear state'
 Assert ($rules.Count -eq 1 -and $rules[0].Name -eq 'another-VPN' -and $nrpt.Count -eq 1 -and $nrpt[0].Name -eq 'other-nrpt') 'Cleanup affected another VPN or left owned rules'
 foreach($failure in 'firewall','nrpt'){
  Reset-Mocks;$state=New-TestState;Save-State $state
  $script:failFirewall=$failure -eq 'firewall';$script:failNRPT=$failure -eq 'nrpt'
  $failed=$false
  try {Install-Privacy $state}catch{$failed=$true}
  Assert $failed 'Failure injection did not run'
  Remove-OwnedRoutesUnlocked
  Assert ($rules.Count -eq 1 -and $nrpt.Count -eq 1 -and -not(Test-Path -LiteralPath $StateFile)) 'Partial installation not rolled back'
 }
 Reset-Mocks;$state=New-TestState;Save-State $state
 $nrpt.Add([pscustomobject]@{Name='existing-catch-all';Namespace=@('.');Comment='VPN'})
 $failed=$false;try{Install-Privacy $state}catch{$failed=$true}
 Assert ($failed -and $rules.Count -eq 1 -and $nrpt.Count -eq 2) 'Conflicting DNS policy was overwritten'
 Remove-OwnedRoutesUnlocked
 Assert ($nrpt.Count -eq 2) 'Foreign catch-all was removed'

 # An abandoned own catch-all without a journal is safe to recover, even when
 # a GUI remains open in an error state. Unrelated rules must survive.
 Reset-Mocks;$state=New-TestState;Save-State $state;Install-Privacy $state
 Remove-Item -LiteralPath $StateFile
 Remove-OrphanedPrivacy
 Assert ($nrpt.Count -eq 1 -and $rules.Count -eq 1) 'Orphaned own rules were not recovered'
 # An arbitrary similarly named rule is not proof of ownership.
 $tag='FturnRaw-'+('a'*32)
 $nrpt.Add([pscustomobject]@{Name='lookalike';Namespace=@('.');Comment=$tag;DisplayName='other';NameServers=@('1.1.1.1','9.9.9.9')})
 $rules.Add([pscustomobject]@{Name='unknown';Group=$tag})
 Remove-OrphanedPrivacy
 Assert ($nrpt.Count -eq 2 -and $rules.Count -eq 2) 'Orphan cleanup removed a lookalike'
 # Even genuine own orphan rules cannot be swept while a core is running.
 Reset-Mocks;$state=New-TestState;Save-State $state;Install-Privacy $state
 $script:cores=@([pscustomobject]@{Id=20})
 Remove-OrphanedPrivacy
 Assert ($nrpt.Count -eq 2 -and $rules.Count -eq 4) 'Live core rules were swept'
 $script:cores=@();Remove-OwnedRoutesUnlocked
 # Adapter restoration failure must not leave persistent blocks installed.
 Reset-Mocks;$state=New-TestState;Save-State $state;Install-Privacy $state
 $script:failAdapter=$true
 $failed=$false;try{Remove-OwnedRoutesUnlocked}catch{$failed=$true}
 Assert ($failed -and (Test-Path -LiteralPath $StateFile) -and $nrpt.Count -eq 1 -and $rules.Count -eq 1) 'Adapter failure stranded privacy or discarded recovery journal'
 $script:failAdapter=$false;Remove-OwnedRoutesUnlocked
 # One privacy removal failure must not prevent the other rules being removed.
 Reset-Mocks;$state=New-TestState;Save-State $state;Install-Privacy $state
 $script:failRemove=$true
 $failed=$false;try{Remove-OwnedRoutesUnlocked}catch{$failed=$true}
 Assert ($failed -and $rules.Count -eq 2 -and (Test-Path -LiteralPath $StateFile)) 'Privacy cleanup stopped at first error or lost journal'
 $script:failRemove=$false;Remove-OwnedRoutesUnlocked

 # DNS restoration failure cannot strand routes or prevent metric restoration.
 Reset-Mocks;$state=New-TestState;$state.prefixes=@('0.0.0.0/1');$state.dnsChanged=$true;$state.metricChanged=$true;$state.previousDNS=@('192.0.2.53');$state.previousMetric=25;$state.previousAutomaticMetric='Enabled'
 $script:adapter=[pscustomobject]@{ifIndex=99;InterfaceGuid='fake'}
 $script:routes.Add([pscustomobject]@{InterfaceIndex=99;DestinationPrefix='0.0.0.0/1';NextHop='0.0.0.0';RouteMetric=5})
 $script:routes.Add([pscustomobject]@{InterfaceIndex=1;DestinationPrefix='0.0.0.0/1';NextHop='192.0.2.1';RouteMetric=5})
 Save-State $state;Install-Privacy $state;$script:failDNS=$true
 $failed=$false;try{Remove-OwnedRoutesUnlocked}catch{$failed=$true}
 Assert ($failed -and $rules.Count -eq 1 -and $nrpt.Count -eq 1 -and $routes.Count -eq 1 -and $routes[0].InterfaceIndex -eq 1 -and $metricRestored -and (Test-Path -LiteralPath $StateFile)) 'DNS failure prevented independent restoration or affected foreign routes'
 $script:failDNS=$false;Remove-OwnedRoutesUnlocked
 Assert ($dnsRestored -and -not(Test-Path -LiteralPath $StateFile)) 'DNS restoration retry failed'
 # Process death BEFORE the guard first reads its journal must still clean up.
 Reset-Mocks;$state=New-TestState;Save-State $state;Install-Privacy $state
 $script:failRemove=$true
 $output=@(Watch-Connection)
 Assert ($output[0] -eq 'READY' -and $sleeps -eq 1 -and -not(Test-Path -LiteralPath $StateFile) -and $rules.Count -eq 1 -and $nrpt.Count -eq 1) 'Initial-death guard race or cleanup retry regression'
 # PID reuse is not a live session and must not terminate another process.
 Reset-Mocks;$state=New-TestState;Save-State $state;Install-Privacy $state
 $script:processes=@([pscustomobject]@{Id=20;StartTime=[DateTime]::UtcNow;Path='fake.exe'})
 $null=Watch-Connection
 Assert ($stopped.Count -eq 0 -and -not(Test-Path -LiteralPath $StateFile)) 'Guard killed a reused PID'

 # Startup recovery also stops the exact recorded orphan when the old guard died.
 Reset-Mocks;$state=New-TestState;$when=[DateTime]::UtcNow;$state.clientStarted=$when.ToUniversalTime().Ticks.ToString()
 $script:processes=@([pscustomobject]@{Id=20;StartTime=$when;Path='fake.exe'})
 Save-State $state;Install-Privacy $state;Recover-NetworkUnlocked
 Assert ($stopped.Count -eq 1 -and $stopped[0] -eq 20 -and -not(Test-Path -LiteralPath $StateFile)) 'Startup left a recorded orphan core running'
 # Another live owner must not lose its routes when a second copy starts.
 Reset-Mocks;$state=New-TestState
 $when=[DateTime]::UtcNow;$state.ownerStarted=$when.ToUniversalTime().Ticks.ToString();$state.clientStarted=$state.ownerStarted
 $script:processes=@([pscustomobject]@{Id=10;StartTime=$when},[pscustomobject]@{Id=20;StartTime=$when})
 Save-State $state;Install-Privacy $state
 $OwnerPid=11
 $failed=$false;try{Remove-OwnedRoutesUnlocked}catch{$failed=$true}
 Assert ($failed -and $rules.Count -eq 4 -and (Test-Path -LiteralPath $StateFile)) 'Second owner removed a live connection'
 $script:processes=@();$OwnerPid=10;Remove-OwnedRoutesUnlocked
 # Recover reads a known old runtime journal as well as the shared journal.
 Reset-Mocks;$state=New-TestState;Save-State $state;Install-Privacy $state
 $old=Join-Path $testDir 'route-state.json';Move-Item -LiteralPath $StateFile -Destination $old
 Recover-NetworkUnlocked
 Assert (-not(Test-Path -LiteralPath $old) -and $rules.Count -eq 1 -and $nrpt.Count -eq 1 -and $StateFile -eq (Join-Path $testDir 'state.json')) 'Legacy recovery failed or changed shared state path'
 Write-Host 'PASS: orphan recovery, live-session ownership, initial crash race, retry, rollback, and foreign VPN preservation'

} finally {
 # Only fixed files inside our verified unique test directory are deleted.
 foreach($name in 'state.json','state.json.new','route-state.json','recovery.log'){
  $path=Join-Path $testDir $name
  if(Test-Path -LiteralPath $path){Remove-Item -LiteralPath $path -Force}
 }
 $stateMutex.Dispose()
 Remove-Item -LiteralPath $testDir -Force
}
