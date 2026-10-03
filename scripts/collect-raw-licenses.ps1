param([Parameter(Mandatory=$true)][string]$OutputFile,[switch]$ServerOnly)
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
$modules=@()
$targets=if($ServerOnly){@(@{Dir=$root;Package='./cmd/raw-server'})}else{@(@{Dir=$root;Package='./cmd/raw-client'},@{Dir=(Join-Path $root 'ui/newservice');Package='.'})}
foreach($target in $targets){
 Push-Location $target.Dir
 try{
  $modules+=@(go list -deps -f '{{if .Module}}{{.Module.Path}}|{{.Module.Version}}|{{.Module.Dir}}{{end}}' $target.Package)
  if($LASTEXITCODE -ne 0){throw 'Cannot enumerate dependency licenses'}
 }finally{Pop-Location}
}
$parts=[Collections.Generic.List[string]]::new()
$scope=if($ServerOnly){'server core'}else{'Windows client and UI'}
$parts.Add("fturn Raw - dependency license notices. Generated from modules used by the $scope. Paths in the build machine are intentionally omitted.")
$goroot=go env GOROOT
if($LASTEXITCODE -ne 0){throw 'Cannot locate Go license'}
$parts.Add("`n===== Go standard library =====`n"+[IO.File]::ReadAllText((Join-Path $goroot 'LICENSE')))
foreach($entry in @($modules | Where-Object {$_} | Sort-Object -Unique)){
 $module,$version,$directory=$entry.Split('|',3)
 $files=@(Get-ChildItem -LiteralPath $directory -File | Where-Object {$_.Name -match '^(LICENSE|LICENCE|COPYING|NOTICE|COPYRIGHT)([._-].*)?$'})
 # fhttp retains the Go Authors BSD headers but omits the referenced LICENSE
 # from its module archive. Include the Go BSD text under its own module heading.
 if(-not $files.Count -and $module -eq 'github.com/bogdanfinn/fhttp'){
  $parts.Add("`n===== $module $version / Go Authors BSD license (source headers) =====`n"+[IO.File]::ReadAllText((Join-Path $goroot 'LICENSE')))
  continue
 }
 if(-not $files.Count){throw "Dependency license not found: $module $version"}
 foreach($file in $files){
  $parts.Add("`n===== $module $version / $($file.Name) =====`n"+[IO.File]::ReadAllText($file.FullName))
 }
}
[IO.File]::WriteAllText([IO.Path]::GetFullPath($OutputFile),($parts -join "`r`n"),[Text.UTF8Encoding]::new($false))
Write-Host ('Collected licenses for '+@($modules | Where-Object {$_} | Sort-Object -Unique).Count+' modules and Go standard library.')
