# Native grammar check for the real source-port filter. Run on the isolated
# Windows CI runner as administrator. Every created rule is DISABLED.
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
$identity=[Security.Principal.WindowsIdentity]::GetCurrent()
$principal=[Security.Principal.WindowsPrincipal]::new($identity)
if(-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)){throw 'Native firewall check requires an administrator CI runner.'}
$tokens=$null;$errors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile((Join-Path $root 'ui/windows/routes.ps1'),[ref]$tokens,[ref]$errors)
if($errors.Count){throw ($errors | Out-String)}
$fn=@($ast.EndBlock.Statements | Where-Object {$_ -is [Management.Automation.Language.FunctionDefinitionAst] -and $_.Name -eq 'Get-DNSBlockedSourcePorts'})
if($fn.Count -ne 1){throw 'Actual DNS range generator missing'}
. ([scriptblock]::Create($fn[0].Extent.Text))
foreach($port in 1024,53000,65535){
 $name='FturnRawValidation-'+[Guid]::NewGuid().ToString('N')
 $expected=@(Get-DNSBlockedSourcePorts $port)
 try {
  New-NetFirewallRule -Name $name -DisplayName $name -Group 'FturnRawValidation' -Enabled False -Direction Outbound -Action Block -Profile Any -Protocol UDP -RemotePort 53,853 -LocalAddress '0.0.0.0-10.77.0.1','10.77.0.3-255.255.255.255' -LocalPort $expected -PolicyStore PersistentStore | Out-Null
  $rule=Get-NetFirewallRule -Name $name -PolicyStore PersistentStore
  if($rule.Enabled -ne 'False' -or $rule.Group -ne 'FturnRawValidation'){throw 'Validation rule identity mismatch'}
  $filter=$rule | Get-NetFirewallPortFilter
  if((@($filter.LocalPort | Sort-Object) -join ',') -ne (@($expected | Sort-Object) -join ',')){throw 'Windows changed the source-port filter'}
 }finally{
  Get-NetFirewallRule -Name $name -PolicyStore PersistentStore -ErrorAction SilentlyContinue | Where-Object {$_.Group -eq 'FturnRawValidation' -and $_.Enabled -eq 'False'} | Remove-NetFirewallRule
 }
}
Write-Host 'Windows accepted the actual DNS source-port filters, including boundary ports; inactive validation rules removed.'
