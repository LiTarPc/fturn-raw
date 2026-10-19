package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpdateVersionComparison(t *testing.T) {
	for _, tc := range []struct {
		next, current string
		want          bool
	}{
		{"v0.1.13", "0.1.12", true}, {"0.1.10", "v0.1.9", true}, {"1.0.0", "0.99.99", true},
		{"0.1.12", "0.1.12", false}, {"0.1.11", "0.1.12", false}, {"0.1.13", "development", false},
		{"0.1.13-rc1", "0.1.12", false}, {"../0.1.13", "0.1.12", false}, {"01.1.13", "0.1.12", false},
		{"18446744073709551616.1.1", "0.1.12", false},
	} {
		if got := newerRelease(tc.next, tc.current); got != tc.want {
			t.Errorf("%s > %s: %v", tc.next, tc.current, got)
		}
	}
}
func testUpdateServer(t *testing.T, payload []byte, manifest string, release appRelease) *updateService {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing user agent")
		}
		_ = json.NewEncoder(w).Encode(release)
	})
	mux.HandleFunc("/download/v0.1.13/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(manifest)) })
	mux.HandleFunc("/download/v0.1.13/"+installerName("v0.1.13"), func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) })
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return &updateService{client: server.Client(), endpoint: server.URL + "/latest", downloadBase: server.URL + "/download/"}
}
func testUpdateRelease(payload []byte) appRelease {
	return appRelease{Tag: "v0.1.13", Assets: []releaseAsset{{Name: installerName("v0.1.13"), Size: int64(len(payload)), State: "uploaded"}, {Name: "SHA256SUMS", Size: 200, State: "uploaded"}}}
}
func TestUpdateChecksWholeInstallerAndVerifiesDownloadedBytes(t *testing.T) {
	payload := []byte("test-only installer bytes; never executed")
	hash := sha256.Sum256(payload)
	manifest := hex.EncodeToString(hash[:]) + "  " + installerName("v0.1.13") + "\n"
	service := testUpdateServer(t, payload, manifest, testUpdateRelease(payload))
	info, err := service.check(context.Background(), "0.1.12")
	if err != nil || !info.Available || info.Version != "0.1.13" {
		t.Fatalf("check: %+v %v", info, err)
	}
	prepared, err := service.prepare(context.Background(), "0.1.12")
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.cleanup()
	data, err := os.ReadFile(prepared.path)
	if err != nil || string(data) != string(payload) || prepared.digest != hex.EncodeToString(hash[:]) {
		t.Fatal("downloaded bytes not verified")
	}
	if _, err = service.prepare(context.Background(), "0.1.13"); err == nil {
		t.Fatal("equal-version installation accepted")
	}
	if _, err = service.prepare(context.Background(), "0.1.14"); err == nil {
		t.Fatal("downgrade accepted")
	}
}
func TestUpdateRejectsCorruptTruncatedAndOversizedDownloads(t *testing.T) {
	original := []byte("expected installer")
	digest := sha256.Sum256(original)
	manifest := hex.EncodeToString(digest[:]) + "  " + installerName("v0.1.13") + "\n"
	for _, payload := range [][]byte{[]byte("corrupted content!"), original[:3], append(append([]byte{}, original...), 1)} {
		service := testUpdateServer(t, payload, manifest, testUpdateRelease(original))
		if prepared, err := service.prepare(context.Background(), "0.1.12"); err == nil {
			prepared.cleanup()
			t.Fatal("unverified installer accepted")
		}
	}
}
func TestUpdateRejectsIncompleteAndUnstableReleases(t *testing.T) {
	payload := []byte("test installer")
	for _, mutate := range []func(*appRelease){
		func(r *appRelease) { r.Draft = true }, func(r *appRelease) { r.Prerelease = true },
		func(r *appRelease) { r.Tag = "../../untrusted" }, func(r *appRelease) { r.Assets = r.Assets[:1] },
		func(r *appRelease) { r.Assets[0].Size = maxInstallerSize + 1 }, func(r *appRelease) { r.Assets[0].State = "new" },
		func(r *appRelease) { r.Assets = append(r.Assets, r.Assets[0]) },
	} {
		release := testUpdateRelease(payload)
		mutate(&release)
		service := testUpdateServer(t, payload, "", release)
		if _, err := service.check(context.Background(), "0.1.12"); err == nil {
			t.Fatalf("invalid release accepted: %+v", release)
		}
	}
}
func TestUpdateManifestRequiresOneExactChecksum(t *testing.T) {
	name := installerName("v0.1.13")
	hash := strings.Repeat("a", 64)
	for _, data := range []string{"", hash + "  other.exe", "xx  " + name, hash + "  " + name + "\n" + hash + "  " + name} {
		if _, err := manifestDigest([]byte(data), name); err == nil {
			t.Fatalf("bad manifest accepted: %q", data)
		}
	}
	if got, err := manifestDigest([]byte(hash+" *"+name+"\r\n"), name); err != nil || got != hash {
		t.Fatal("valid checksum rejected")
	}
}
func TestUpdateServiceReportsNetworkFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer server.Close()
	service := &updateService{client: server.Client(), endpoint: server.URL}
	if _, err := service.check(context.Background(), "0.1.12"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("HTTP failure: %v", err)
	}
}
func TestUpdatePreservesActiveSessionAndBlocksConnectAfterLaunch(t *testing.T) {
	a := newApp(t.TempDir())
	cancelled := false
	a.cancel = func() { cancelled = true }
	a.state = "connected"
	if err := a.InstallUpdate(); err == nil {
		t.Fatal("installation during active session accepted")
	}
	if cancelled || a.cancel == nil || a.state != "connected" {
		t.Fatal("active session changed")
	}
	a.installing = true
	if err := a.Connect(); err == nil {
		t.Fatal("connection during pending installer accepted")
	}
}
func TestUpdateInstallerWaitsForExitAndQuotesPaths(t *testing.T) {
	prepared := preparedUpdate{path: `C:\Temp\O'Brien\setup.exe`, digest: strings.Repeat("a", 64)}
	script := updateInstallerScript(prepared, `C:\Program Files\O'Brien Raw`, 1234)
	for _, want := range []string{"Get-Process -Id 1234", "$owner.WaitForExit()", "SHA256]::Create()", `'C:\Temp\O''Brien\setup.exe'`, `'/D=C:\Program Files\O''Brien Raw'`, "-WindowStyle Normal"} {
		if !strings.Contains(script, want) {
			t.Fatalf("missing %q", want)
		}
	}
	if strings.Index(script, "WaitForExit") > strings.Index(script, "Start-Process") {
		t.Fatal("installer starts before application exits")
	}
}
func TestVersionInfoDoesNotGuessCoreVersion(t *testing.T) {
	a := newApp(t.TempDir())
	info := a.GetVersionInfo()
	if info.AppVersion != Version || info.CoreVersion != "unknown" {
		t.Fatalf("incorrect version info: %+v", info)
	}
}

// Execute the real helper script with only process lookup/installer launch mocked.
// The checksum and file cleanup use Windows PowerShell itself; no installer runs.
func TestUpdateHelperRunsInWindowsPowerShell(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "O'Brien загрузка")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("test data, never executable")
	path := filepath.Join(dir, "setup.exe")
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(payload)
	prepared := preparedUpdate{path: path, digest: hex.EncodeToString(hash[:])}
	target := `C:\Program Files\O'Brien приложение`
	harness := `$script:steps=@()
function Get-Process {
 $owner=[pscustomobject]@{}
 $owner | Add-Member -MemberType ScriptMethod -Name WaitForExit -Value {$script:steps+='wait'} -PassThru
}
function Start-Process {
 param($FilePath,$ArgumentList,$WindowStyle,[switch]$Wait,[switch]$PassThru)
 if(($script:steps -join ',') -ne 'wait'){throw 'Installer started before parent exited'}
 if($FilePath -ne ` + powershellLiteral(path) + `){throw 'Wrong installer path'}
 if($ArgumentList -ne ` + powershellLiteral("/D="+target) + `){throw 'Wrong install target'}
 if($WindowStyle -ne 'Normal' -or !$Wait -or !$PassThru){throw 'Incorrect installer launch'}
 $script:steps+='installer'
 return [pscustomobject]@{ExitCode=0}
}
` + updateInstallerScript(prepared, target, 1234) + `
if(($script:steps -join ',') -ne 'wait,installer'){throw 'Installer was not called'}
if(Test-Path -LiteralPath ` + powershellLiteral(dir) + `){throw 'Successful download was not cleaned up'}
Write-Output 'helper passed'
`
	scriptPath := filepath.Join(root, "helper-test.ps1")
	if err := os.WriteFile(scriptPath, append([]byte{0xef, 0xbb, 0xbf}, []byte(harness)...), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := command(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", scriptPath).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "helper passed") {
		t.Fatalf("helper failed: %v\n%s", err, out)
	}
}
