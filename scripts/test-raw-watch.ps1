# Real process-death integration check. Networking remains entirely mocked.
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
$tokens=$null;$errors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile((Join-Path $root 'ui/windows/routes.ps1'),[ref]$tokens,[ref]$errors)
if($errors.Count){throw ($errors | Out-String)}
$functions=@($ast.EndBlock.Statements | Where-Object {$_ -is [Management.Automation.Language.FunctionDefinitionAst]} | ForEach-Object {$_.Extent.Text}) -join "`r`n"
$testDir=Join-Path ([IO.Path]::GetTempPath()) ('fturn-watch-test-'+[Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $testDir | Out-Null
$fixture=Join-Path $testDir 'fixture.ps1'
$mock=Join-Path $testDir 'guard.ps1'
$stateFile=Join-Path $testDir 'state.json'
$tag='FturnRaw-'+[Guid]::NewGuid().ToString('N')
[IO.File]::WriteAllText($fixture,'Start-Sleep -Seconds 120')
$mockText=Get-Content -LiteralPath (Join-Path $PSScriptRoot 'fixtures/route-watch-mocks.ps1') -Raw
[IO.File]::WriteAllText($mock,$mockText+"`r`n"+$functions+"`r`nWatch-Connection")
$processes=[Collections.Generic.List[object]]::new()
try {
 foreach($initialDeath in @($false,$true)){
  $owner=Start-Process powershell.exe -ArgumentList '-NoProfile','-File',('"'+$fixture+'"') -WindowStyle Hidden -PassThru
  $client=Start-Process powershell.exe -ArgumentList '-NoProfile','-File',('"'+$fixture+'"') -WindowStyle Hidden -PassThru
  $processes.Add($owner);$processes.Add($client)
  $deadline=[DateTime]::UtcNow.AddSeconds(10)
  do {
   $actualClient=Get-Process -Id $client.Id
   $actualOwner=Get-Process -Id $owner.Id
   if($actualClient.Path -and $actualOwner.Path){break}
   Start-Sleep -Milliseconds 100
  }while([DateTime]::UtcNow -lt $deadline)
  if(-not $actualClient.Path){throw 'Fixture process did not finish loading its module'}
  $state=[ordered]@{version=3;interfaceIndex=99;interfaceGuid='mock';prefixes=@();privacyTag=$tag;firewallRules=@(($tag+'-IPv6'),($tag+'-DNS-UDP'),($tag+'-DNS-TCP'));ownerPid=$owner.Id;ownerStarted=$actualOwner.StartTime.ToUniversalTime().Ticks.ToString();clientPid=$client.Id;clientStarted=$actualClient.StartTime.ToUniversalTime().Ticks.ToString();clientPath=$actualClient.Path}
  $state | ConvertTo-Json | Set-Content -LiteralPath $stateFile -Encoding UTF8
  if($initialDeath){$owner.Kill();$owner.WaitForExit()}
  $arguments=@('-NoProfile','-ExecutionPolicy','Bypass','-File',('"'+$mock+'"'),'-StateFile',('"'+$stateFile+'"'),'-OwnerPid',$owner.Id,'-ClientPid',$client.Id,'-Tag',$tag)
  $guard=Start-Process powershell.exe -ArgumentList $arguments -WindowStyle Hidden -PassThru -RedirectStandardOutput ($stateFile+'.out') -RedirectStandardError ($stateFile+'.err')
  $null=$guard.Handle
  $processes.Add($guard)
  $deadline=[DateTime]::UtcNow.AddSeconds(10)
  while([DateTime]::UtcNow -lt $deadline){
   if(Test-Path -LiteralPath ($stateFile+'.out')){if((Get-Content -LiteralPath ($stateFile+'.out') -Raw) -match 'READY'){break}}
   if($guard.HasExited){throw 'Guard exited before acknowledgement'}
   Start-Sleep -Milliseconds 100
  }
  if((Get-Content -LiteralPath ($stateFile+'.out') -Raw) -notmatch 'READY'){throw 'Guard readiness timed out'}
  if(-not $initialDeath){$owner.Kill();$owner.WaitForExit()}
  if(-not $guard.WaitForExit(15000)){throw 'Guard did not recover after forced owner termination'}
  if($guard.ExitCode -ne 0){throw ('Guard failed, exit code '+$guard.ExitCode+': '+(Get-Content -LiteralPath ($stateFile+'.err') -Raw))}
  if(Test-Path -LiteralPath $stateFile){throw 'Crash recovery left its journal'}
  if(-not $client.WaitForExit(5000)){throw ('Orphaned exact client process was not stopped; recorded '+$state.clientPath+' / '+$state.clientStarted+'; actual '+(Get-Process -Id $client.Id).Path+' / '+(Get-Process -Id $client.Id).StartTime.ToUniversalTime().Ticks.ToString())}
  $removed=@(Get-Content -LiteralPath ($stateFile+'.removed'))
  if($removed.Count -ne 4 -or $removed[0] -ne 'fixture-nrpt'){throw 'Guard did not remove all owned privacy rules'}
  foreach($name in @('state.json.removed','state.json.out','state.json.err','recovery.log')){$path=Join-Path $testDir $name;if(Test-Path -LiteralPath $path){Remove-Item -LiteralPath $path -Force}}
 }
 Write-Host 'PASS: forced owner termination, orphaned core termination, and death before first journal read (network mocked)'
}finally{
 foreach($process in $processes){if(-not $process.HasExited){$process.Kill();$process.WaitForExit()};$process.Dispose()}
 foreach($name in @('fixture.ps1','guard.ps1','state.json','state.json.removed','state.json.out','state.json.err','recovery.log')){
  $path=Join-Path $testDir $name
  if(Test-Path -LiteralPath $path){Remove-Item -LiteralPath $path -Force}
 }
 Remove-Item -LiteralPath $testDir -Force
}
