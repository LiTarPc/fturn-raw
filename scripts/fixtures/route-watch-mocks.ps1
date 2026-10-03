param($StateFile,[int]$OwnerPid,[int]$ClientPid,$Tag)
$ErrorActionPreference='Stop'
$stateMutex=[Threading.Mutex]::new($false)
function Get-NetAdapter {return $null}
function Get-DnsClientNrptRule {return [pscustomobject]@{Name='fixture-nrpt';Comment=$Tag}}
function Remove-DnsClientNrptRule($Name,[switch]$Force){Add-Content -LiteralPath ($StateFile+'.removed') -Value $Name}
function Get-NetFirewallRule($Name,$PolicyStore,$ErrorAction){return @('IPv6','DNS-UDP','DNS-TCP' | ForEach-Object {[pscustomobject]@{Name=($Tag+'-'+$_);Group=$Tag}})}
function Remove-NetFirewallRule {
 param([Parameter(ValueFromPipeline=$true)]$InputObject)
 process {Add-Content -LiteralPath ($StateFile+'.removed') -Value $InputObject.Name}
}
function Clear-DnsClientCache {}
