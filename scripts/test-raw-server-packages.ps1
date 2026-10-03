param(
 [ValidateSet('linux-amd64','linux-arm64','windows-amd64')][string[]]$Targets=@('linux-amd64','linux-arm64','windows-amd64'),
 [string]$Version='0.1.1'
)
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
Add-Type -AssemblyName System.IO.Compression.FileSystem
function Assert($condition,$message){if(-not $condition){throw $message}}
$hostOS=if([Environment]::OSVersion.Platform -eq 'Win32NT'){'windows'}else{'linux'}
$hostArch=switch([Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()){'X64'{'amd64'};'Arm64'{'arm64'};default{''}}
foreach($target in $Targets){
 $platform,$arch=$target.Split('-')
 $binary=if($platform -eq 'windows'){'raw-server.exe'}else{'raw-server'}
 $path=Join-Path $root "dist/fturn-raw-server-$Version-$target.zip"
 $zip=[IO.Compression.ZipFile]::OpenRead($path)
 try{
  $expected=@($binary,'LICENSE','README.md','THIRD-PARTY-NOTICES.txt')
  if($platform -eq 'windows'){$expected+=@('wintun.dll','WINTUN-LICENSE.txt')}
  Assert (-not(Compare-Object ($expected | Sort-Object) (@($zip.Entries.FullName) | Sort-Object))) "Unexpected archive contents: $target"
  $stream=$zip.GetEntry($binary).Open();$memory=[IO.MemoryStream]::new()
  try{$stream.CopyTo($memory);$bytes=$memory.ToArray()}finally{$stream.Dispose();$memory.Dispose()}
  if($platform -eq 'linux'){
   Assert ($bytes[0] -eq 127 -and [Text.Encoding]::ASCII.GetString($bytes,1,3) -eq 'ELF' -and $bytes[4] -eq 2 -and $bytes[5] -eq 1) 'Expected ELF64 little-endian'
   $machine=[BitConverter]::ToUInt16($bytes,18)
   $wanted=if($arch -eq 'amd64'){62}else{183}
   Assert ($machine -eq $wanted) "ELF CPU mismatch: $target"
  }else{
   Assert ([Text.Encoding]::ASCII.GetString($bytes,0,2) -eq 'MZ') 'Expected Windows PE'
   $pe=[BitConverter]::ToInt32($bytes,60)
   Assert ([Text.Encoding]::ASCII.GetString($bytes,$pe,4) -eq "PE`0`0" -and [BitConverter]::ToUInt16($bytes,$pe+4) -eq 34404) 'Expected Windows amd64 PE'
   $dll=$zip.GetEntry('wintun.dll');Assert ($dll.Length -gt 0) 'Wintun missing'
  }
 }finally{$zip.Dispose()}
 $manifest=Get-Content -LiteralPath (Join-Path $root "dist/SERVER-SHA256SUMS-$target") -Raw
 Assert ($manifest.Trim() -eq (((Get-FileHash -LiteralPath $path).Hash.ToLowerInvariant())+'  '+[IO.Path]::GetFileName($path))) "Checksum mismatch: $target"
 if($platform -eq $hostOS -and $arch -eq $hostArch){
  $exe=Join-Path $root ("dist/server-$target/$binary")
  $info=[Diagnostics.ProcessStartInfo]::new()
  $info.FileName=$exe;$info.Arguments='-help';$info.UseShellExecute=$false;$info.CreateNoWindow=$true
  $info.RedirectStandardOutput=$true;$info.RedirectStandardError=$true
  $process=[Diagnostics.Process]::Start($info)
  try{$output=$process.StandardOutput.ReadToEnd()+$process.StandardError.ReadToEnd();$process.WaitForExit();$exitCode=$process.ExitCode}finally{$process.Dispose()}
  Assert ($exitCode -eq 0 -and $output -match 'obf-key-file' -and $output -match 'client-ip') "Native server help failed: $target"
 }
 Write-Host "PASS: $target package, CPU, SHA256 and native help when supported"
}
