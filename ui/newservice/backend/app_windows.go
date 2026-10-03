package backend

import (
	"context"
	"errors"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type Profile struct {
	Server    string
	VkLink    string
	Key       string
	KeyFile   string `json:",omitempty"`
	Mtu       int
	Streams   int
	RouteMode string
}
type Snapshot struct {
	BypassActive   bool           `json:"bypassActive"`
	Bypass         BypassSnapshot `json:"bypass"`
	Profiles       []SavedProfile `json:"profiles"`
	ActiveID       string         `json:"activeId"`
	Profile        Profile        `json:"profile"`
	State          string         `json:"state"`
	Detail         string         `json:"detail"`
	Underlay       string         `json:"underlay"`
	Ready          int            `json:"ready"`
	Logs           []string       `json:"logs"`
	RX             uint64         `json:"rx"`
	TX             uint64         `json:"tx"`
	ElapsedSeconds int64          `json:"elapsedSeconds"`
	Settings       AppSettings    `json:"settings"`
	TrayReady      bool           `json:"trayReady"`
	DataDir        string         `json:"dataDir"`
}
type App struct {
	mu                      sync.Mutex
	op                      sync.Mutex
	root                    string
	dataDir                 string
	ctx                     context.Context
	profile                 Profile
	profiles                []SavedProfile
	activeID                string
	storageErr              error
	state, detail, underlay string
	logs                    []string
	ready                   map[string]bool
	cancel                  context.CancelFunc
	cmd                     *exec.Cmd
	generation              int
	rx, tx                  uint64
	clock                   connectionClock
	bypassActive            bool
	settings                *settingsService
	bypass                  *bypassService
	desktop                 *desktopController
	startupDone             chan struct{}
}

func NewApp() *App {
	path, exeErr := os.Executable()
	dataDir, dataErr := userDataDirectory()
	return newAppAt(filepath.Dir(path), dataDir, errors.Join(exeErr, dataErr))
}

// Existing unit tests use one isolated directory; production supplies a separate user-data path.
func newApp(root string) *App                  { return newAppWithData(root, root) }
func newAppWithData(root, dataDir string) *App { return newAppAt(root, dataDir, nil) }
func newAppAt(root, dataDir string, initErr error) *App {
	a := &App{root: root, dataDir: dataDir, profile: Profile{KeyFile: "raw.key", Mtu: 1420, Streams: 10, RouteMode: "full"}, state: "idle", detail: "Готов к подключению", ready: map[string]bool{}, logs: []string{}}
	if initErr == nil {
		initErr = migrateUserData(root, dataDir)
	}
	if initErr == nil {
		a.loadProfiles()
	} else {
		a.storageErr = initErr
		a.state = "error"
		a.detail = initErr.Error()
	}
	if initErr == nil {
		a.settings = newSettingsService(filepath.Join(dataDir, "app-settings.json"), newWindowsStartup())
	} else {
		a.settings = &settingsService{path: filepath.Join(dataDir, "app-settings.json"), value: AppSettings{MinimizeToTray: true}, loadErr: initErr, startup: newWindowsStartup(), write: atomicSettingsWrite}
	}
	a.bypass = newBypassService(dataDir)
	if initErr != nil {
		a.bypass.loadErr = initErr
	}
	a.startupDone = make(chan struct{})
	a.desktop = &desktopController{tray: &nativeTray{}, snapshot: a.GetSnapshot, settings: a.settings.get, log: a.appendLog, toggle: func() {
		var err error
		if connectionRunning(a.GetSnapshot().State) {
			err = a.Disconnect()
		} else {
			err = a.Connect()
		}
		if err != nil {
			a.appendLog(err.Error())
			a.desktop.show()
			a.publish()
		}
	}}
	return a
}
func (a *App) GetSnapshot() Snapshot {
	a.mu.Lock()
	snap := Snapshot{Profile: a.profile, Profiles: append([]SavedProfile{}, a.profiles...), ActiveID: a.activeID, State: a.state, Detail: a.detail, Underlay: a.underlay, Ready: len(a.ready), Logs: append([]string{}, a.logs...), RX: a.rx, TX: a.tx, ElapsedSeconds: a.clock.seconds(time.Now()), DataDir: a.dataDir}
	a.mu.Unlock()
	a.mu.Lock()
	snap.BypassActive = a.bypassActive
	a.mu.Unlock()
	snap.Settings = a.settings.get()
	snap.Bypass = a.bypass.snapshot()
	snap.TrayReady = a.desktop != nil && a.desktop.ready.Load()
	return snap
}
func (a *App) publish() {
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx != nil {
		runtime.EventsEmit(ctx, "snapshot", a.GetSnapshot())
	}
}
func (a *App) SaveProfile(p Profile) error {
	a.op.Lock()
	defer a.op.Unlock()
	return a.saveNamedProfile(p, "", false)
}
