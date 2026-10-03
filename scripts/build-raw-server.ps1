param(
 [ValidateSet('linux-amd64','linux-arm64','windows-amd64')][string[]]$Targets=@('linux-amd64','linux-arm64','windows-amd64'),
 [string]$Version='0.1.0'
)
$ErrorActionPreference='Stop'
if($Version -notmatch '^\d+\.\d+\.\d+$'){throw 'Version must be major.minor.patch'}
$root=Split-Path -Parent $PSScriptRoot
$dist=Join-Path $root 'dist'
$oldOS=$env:GOOS;$oldArch=$env:GOARCH;$oldCGO=$env:CGO_ENABLED
$checks=@()
Push-Location $root
try{
 $env:CGO_ENABLED='0'
 foreach($target in $Targets){
  $platform,$arch=$target.Split('-');$env:GOOS=$platform;$env:GOARCH=$arch
  $out=Join-Path $dist ('server-'+$target)
  New-Item -ItemType Directory -Path $out -Force | Out-Null
  $binary=if($platform -eq 'windows'){'raw-server.exe'}else{'raw-server'}
  go build -buildvcs=false -trimpath -ldflags '-s -w' -o (Join-Path $out $binary) ./cmd/raw-server
  if($LASTEXITCODE -ne 0){throw "Server build failed: $target"}
  Copy-Item -LiteralPath (Join-Path $root 'LICENSE') -Destination $out
  Copy-Item -LiteralPath (Join-Path $root 'docs/raw-server.md') -Destination (Join-Path $out 'README.md')
  & (Join-Path $PSScriptRoot 'collect-raw-licenses.ps1') -ServerOnly -OutputFile (Join-Path $out 'THIRD-PARTY-NOTICES.txt')
  $files=@($binary,'LICENSE','README.md','THIRD-PARTY-NOTICES.txt')
  if($platform -eq 'windows'){
   & (Join-Path $PSScriptRoot 'bundle-raw-wintun.ps1') -OutputDir $out
   $files+=@('wintun.dll','WINTUN-LICENSE.txt')
  }
  $archive=Join-Path $dist "fturn-raw-server-$Version-$target.zip"
  $paths=$files | ForEach-Object {Join-Path $out $_}
  Compress-Archive -LiteralPath $paths -DestinationPath $archive -Force
  $line=((Get-FileHash -LiteralPath $archive).Hash.ToLowerInvariant())+'  '+[IO.Path]::GetFileName($archive)
  $checks+=$line
  $line | Set-Content -LiteralPath (Join-Path $dist "SERVER-SHA256SUMS-$target") -Encoding ascii
  Write-Host "Built server package: $target"
 }
 $checks | Set-Content -LiteralPath (Join-Path $dist 'SERVER-SHA256SUMS') -Encoding ascii
}finally{
 $env:GOOS=$oldOS;$env:GOARCH=$oldArch;$env:CGO_ENABLED=$oldCGO;Pop-Location
}
