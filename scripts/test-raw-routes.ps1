# Isolated regression checks: all network cmdlets below are mocked.
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
$tokens=$null;$errors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile((Join-Path $root 'ui/windows/routes.ps1'),[ref]$tokens,[ref]$errors)
if($errors.Count){throw ($errors | Out-String)}
foreach($fn in $ast.EndBlock.Statements | Where-Object {$_ -is [Management.Automation.Language.FunctionDefinitionAst]}){
 if($fn.Name -in @('Get-PrivacyAddressFilters','Save-State','Install-Privacy','Remove-Privacy','Remove-OwnedRoutesUnlocked')){. ([scriptblock]::Create($fn.Extent.Text))}
}
function Assert($condition,$message){if(-not $condition){throw $message}}
$testDir=Join-Path ([IO.Path]::GetTempPath()) ('fturn-route-test-'+[Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $testDir | Out-Null
$StateFile=Join-Path $testDir 'state.json'
function Get-NetIPAddress($InterfaceIndex,$AddressFamily){[pscustomobject]@{IPAddress='10.77.0.2';InterfaceIndex=99}}
function Get-NetFirewallProfile($PolicyStore){[pscustomobject]@{Enabled=$true;AllowLocalFirewallRules='True'}}
function Get-NetAdapter {return $null}
function Get-DnsClientNrptRule {return $script:nrpt.ToArray()}
function Add-DnsClientNrptRule($Namespace,$NameServers,$Comment,$DisplayName){
 $script:nrpt.Add([pscustomobject]@{Name='owned-nrpt';Namespace=@($Namespace);NameServers=$NameServers;Comment=$Comment})
 if($script:failNRPT){throw 'Injected NRPT failure after creation'}
}
function Remove-DnsClientNrptRule($Name,[switch]$Force){
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
function Get-NetFirewallRule($Name,$PolicyStore,$ErrorAction){return @($script:rules.ToArray() | Where-Object {$_.Name -eq $Name})}
function Remove-NetFirewallRule {
 param([Parameter(ValueFromPipeline=$true)]$InputObject)
 process {[void]$script:rules.Remove($InputObject)}
}
function Reset-Mocks {
 $script:rules=[Collections.Generic.List[object]]::new()
 $script:rules.Add([pscustomobject]@{Name='another-VPN';Group='other'})
 $script:nrpt=[Collections.Generic.List[object]]::new()
 $script:nrpt.Add([pscustomobject]@{Name='other-nrpt';Namespace=@('.example.invalid');Comment='other'})
 $script:failFirewall=$false;$script:failNRPT=$false
}
function New-TestState {return [ordered]@{version=2;interfaceIndex=99;interfaceGuid='fake';prefixes=@();privacyTag=$null;firewallRules=@()}}
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
 Write-Host 'PASS: privacy coverage, crash cleanup, partial rollback, and foreign VPN preservation'
} finally {
 # Only fixed files inside our verified unique test directory are deleted.
 foreach($name in 'state.json','state.json.new'){
  $path=Join-Path $testDir $name
  if(Test-Path -LiteralPath $path){Remove-Item -LiteralPath $path -Force}
 }
 Remove-Item -LiteralPath $testDir -Force
}
