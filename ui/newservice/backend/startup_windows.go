package backend

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
)

type windowsStartup struct {
	executable string
	dataDir    string
}

func newWindowsStartup() *windowsStartup {
	exe, _ := os.Executable()
	dataDir, _ := userDataDirectory()
	return &windowsStartup{exe, dataDir}
}
func psLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func (s *windowsStartup) SetEnabled(enabled bool) error {
	if s.executable == "" || s.dataDir == "" {
		return fmt.Errorf("Не найден путь приложения для автозапуска.")
	}
	script := s.startupScript(enabled)

	units := utf16.Encode([]rune(script))
	data := make([]byte, len(units)*2)
	for i, v := range units {
		data[2*i] = byte(v)
		data[2*i+1] = byte(v >> 8)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := command(ctx, "powershell.exe", "-NoProfile", "-EncodedCommand", base64.StdEncoding.EncodeToString(data)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("Автозапуск: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (s *windowsStartup) startupScript(enabled bool) string {
	hash := sha256.Sum256([]byte(strings.ToLower(s.dataDir)))
	name := "FturnRaw-" + hex.EncodeToString(hash[:8])
	script := `$ErrorActionPreference='Stop'
 $exe=` + psLiteral(s.executable) + `
 $name=` + psLiteral(name) + `
 $sid=[Security.Principal.WindowsIdentity]::GetCurrent().User.Value
 $name=$name+'-'+$sid
 $description=` + psLiteral("fturn Raw user startup: "+s.dataDir) + `
 $tasks=@(Get-ScheduledTask -TaskPath '\' -ErrorAction Stop)
 $task=$tasks | Where-Object TaskName -EQ $name
 if($task){
  $owner=$task.Principal.UserId
  if($owner -notmatch '^S-1-'){ $owner=(New-Object Security.Principal.NTAccount($owner)).Translate([Security.Principal.SecurityIdentifier]).Value }
  if($task.Description -ne $description -or $owner -ne $sid){throw 'Startup task ownership mismatch'}
 }
 `
	if enabled {
		script += `$action=New-ScheduledTaskAction -Execute $exe -Argument '-startup' -WorkingDirectory ` + psLiteral(filepath.Dir(s.executable)) + `
 $trigger=New-ScheduledTaskTrigger -AtLogOn -User $sid
 $principal=New-ScheduledTaskPrincipal -UserId $sid -LogonType Interactive -RunLevel Highest
 $settings=New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit ([TimeSpan]::Zero) -MultipleInstances IgnoreNew
 Register-ScheduledTask -TaskName $name -TaskPath '\' -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Description $description -Force | Out-Null`
	} else {
		script += `if($task){Unregister-ScheduledTask -TaskName $name -TaskPath '\' -Confirm:$false}`
	}

	script += `
 foreach($legacy in $tasks){
  if($legacy.TaskName -eq $name -or $legacy.TaskName -notlike 'FturnRaw-*'){continue}
  if(@($legacy.Actions).Count -ne 1){continue}
  $oldExe=$legacy.Actions[0].Execute
  if($legacy.Description -ne ('fturn Raw startup: '+$oldExe)){continue}
  try{
   $owner=$legacy.Principal.UserId
   if($owner -notmatch '^S-1-'){$owner=(New-Object Security.Principal.NTAccount($owner)).Translate([Security.Principal.SecurityIdentifier]).Value}
  }catch{continue}
  if($owner -ne $sid){continue}
  $sha=[Security.Cryptography.SHA256]::Create()
  try{$hash=$sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($oldExe.ToLowerInvariant()))}finally{$sha.Dispose()}
  $oldName='FturnRaw-'+([BitConverter]::ToString($hash).Replace('-','').Substring(0,16).ToLowerInvariant())+'-'+$sid
  if($legacy.TaskName -eq $oldName){Unregister-ScheduledTask -TaskName $legacy.TaskName -TaskPath '\' -Confirm:$false}
 }`

	return script
}
