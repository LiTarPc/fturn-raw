package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStartupSharedIdentityAndLegacyOwnership(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "fturn-raw")
	current := &windowsStartup{filepath.Join(t.TempDir(), "new folder", "FturnRaw.exe"), dataDir}
	previous := &windowsStartup{filepath.Join(t.TempDir(), "old folder", "FturnRaw.exe"), dataDir}
	hash := sha256.Sum256([]byte(strings.ToLower(dataDir)))
	identity := "FturnRaw-" + hex.EncodeToString(hash[:8])
	if !strings.Contains(current.startupScript(true), psLiteral(identity)) || !strings.Contains(previous.startupScript(true), psLiteral(identity)) {
		t.Fatal("startup identity changes with executable folder")
	}
	oldHash := sha256.Sum256([]byte(strings.ToLower(previous.executable)))
	legacyPrefix := "FturnRaw-" + hex.EncodeToString(oldHash[:8])
	for _, enabled := range []bool{true, false} {
		prelude := `$ErrorActionPreference='Stop'
 $sid=[Security.Principal.WindowsIdentity]::GetCurrent().User.Value
 $stable=` + psLiteral(identity) + `+'-'+$sid
 $legacyName=` + psLiteral(legacyPrefix) + `+'-'+$sid
 $script:deleted=@();$script:registered=$null
 $script:tasks=@(
  [pscustomobject]@{TaskName=$stable;Description=` + psLiteral("fturn Raw user startup: "+dataDir) + `;Principal=[pscustomobject]@{UserId=$sid};Actions=@()},
  [pscustomobject]@{TaskName=$legacyName;Description=` + psLiteral("fturn Raw startup: "+previous.executable) + `;Principal=[pscustomobject]@{UserId=$sid};Actions=@([pscustomobject]@{Execute=` + psLiteral(previous.executable) + `})},
  [pscustomobject]@{TaskName='FturnRaw-foreign';Description=` + psLiteral("fturn Raw startup: "+previous.executable) + `;Principal=[pscustomobject]@{UserId=$sid};Actions=@([pscustomobject]@{Execute=` + psLiteral(previous.executable) + `})})
 function Get-ScheduledTask{[CmdletBinding()]param($TaskPath) return $script:tasks}
 function New-ScheduledTaskAction{param($Execute,$Argument,$WorkingDirectory) return [pscustomobject]@{Execute=$Execute}}
 function New-ScheduledTaskTrigger{return [pscustomobject]@{}}
 function New-ScheduledTaskPrincipal{return [pscustomobject]@{}}
 function New-ScheduledTaskSettingsSet{return [pscustomobject]@{}}
 function Register-ScheduledTask{[CmdletBinding()]param($TaskName,$TaskPath,$Action,$Trigger,$Principal,$Settings,$Description,[switch]$Force) $script:registered=$Action.Execute}
 function Unregister-ScheduledTask{[CmdletBinding(SupportsShouldProcess=$true)]param($TaskName,$TaskPath) $script:deleted+= $TaskName}
 `
		assertions := `if($script:deleted -contains 'FturnRaw-foreign'){throw 'Foreign task removed'}
 if($script:deleted -notcontains $legacyName){throw 'Owned legacy task retained'}
 `
		if enabled {
			assertions += `if($script:registered -ne ` + psLiteral(current.executable) + `){throw 'New executable not selected'};if($script:deleted -contains $stable){throw 'Shared task removed'}`
		} else {
			assertions += `if($script:registered -ne $null){throw 'Disabled startup registered task'};if($script:deleted -notcontains $stable){throw 'Shared task not removed'}`
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		output, err := command(ctx, "powershell.exe", "-NoProfile", "-Command", prelude+current.startupScript(enabled)+"\n"+assertions).CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("mocked startup policy: %s (%v)", output, err)
		}
	}
}
