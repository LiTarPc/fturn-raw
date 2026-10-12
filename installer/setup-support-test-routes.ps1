# No routes, DNS, firewall or tasks are changed by this test fixture.
param([string]$Action,[string]$LegacyDirectory)
$ErrorActionPreference='Stop'
if($Action -ne 'Recover'){throw 'Unexpected recovery action'}
Add-Content -LiteralPath (Join-Path (Split-Path -Parent $LegacyDirectory) 'installer-test-recovery.log') -Value $Action -Encoding Ascii