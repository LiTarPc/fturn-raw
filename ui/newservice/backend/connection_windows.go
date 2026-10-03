package backend

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var readyRE = regexp.MustCompile(`RAW stream (\d+) ready`)
var retryRE = regexp.MustCompile(`RAW stream (\d+):.*retrying`)

func command(ctx context.Context, path string, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, path, args...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return c
}
func (a *App) routes(ctx context.Context, action string, pid int, mode string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	c := command(ctx, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(runtimeDirectory(a.root), "routes.ps1"), "-Action", action, "-Mode", mode, "-OwnerPid", strconv.Itoa(os.Getpid()), "-ClientPid", strconv.Itoa(pid))
	c.Dir = runtimeDirectory(a.root)
	out, err := c.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %s (%w)", action, strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}
func (a *App) setState(g int, state, detail string) {
	a.mu.Lock()
	if a.generation == g {
		a.clock.transition(state, time.Now())
		a.state = state
		a.detail = detail
	}
	a.mu.Unlock()
	a.publish()
}
func (a *App) Connect() error {
	a.op.Lock()
	defer a.op.Unlock()
	a.mu.Lock()
	if a.cancel != nil {
		a.mu.Unlock()
		return nil
	}
	p := a.profile
	storageErr := a.storageErr
	a.mu.Unlock()
	if storageErr != nil {
		return storageErr
	}
	if until := authRetryAt(a.dataDir, time.Now()); until > 0 {
		return errors.New(authPauseMessage(until))
	}
	key, err := validate(p, a.root)
	if err != nil {
		return err
	}
	for _, name := range []string{"raw-client.exe", "wintun.dll", "routes.ps1"} {
		if _, err = os.Stat(filepath.Join(runtimeDirectory(a.root), name)); err != nil {
			return fmt.Errorf("Р СЏРґРѕРј СЃ РїСЂРёР»РѕР¶РµРЅРёРµРј РЅСѓР¶РµРЅ %s.", name)
		}
	}
	bypassPath, err := a.bypass.prepare(p.RouteMode)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.generation++
	g := a.generation
	a.cancel = cancel
	a.bypassActive = bypassPath != ""
	a.clock.transition("connecting", time.Now())
	a.state = "connecting"
	a.detail = "РџРѕР»СѓС‡Р°РµРј РґРѕСЃС‚СѓРї Рє VK Рё СЃРѕР·РґР°С‘Рј С‚СѓРЅРЅРµР»СЊ"
	a.ready = map[string]bool{}
	a.rx = 0
	a.tx = 0
	a.mu.Unlock()
	a.publish()
	go a.run(ctx, g, p, key, bypassPath)
	return nil
}
func (a *App) run(ctx context.Context, g int, p Profile, key, bypassPath string) {
	if bypassPath != "" {
		defer os.Remove(bypassPath)
	}
	var runErr error
	defer func() {
		a.op.Lock()
		defer a.op.Unlock()
		a.mu.Lock()
		current := a.generation == g
		cancel := a.cancel
		a.mu.Unlock()
		if !current {
			return
		}
		if cancel != nil {
			cancel()
		}
		if _, err := a.routes(context.Background(), "Remove", 0, p.RouteMode); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("РќРµ СѓРґР°Р»РѕСЃСЊ РІРѕСЃСЃС‚Р°РЅРѕРІРёС‚СЊ СЃРµС‚РµРІС‹Рµ РЅР°СЃС‚СЂРѕР№РєРё: %w", err))
		}
		a.mu.Lock()
		a.cmd = nil
		a.cancel = nil
		a.bypassActive = false
		a.ready = map[string]bool{}
		a.mu.Unlock()
		if runErr != nil {
			a.appendLog(runErr.Error())
			a.setState(g, "error", runErr.Error())
		} else {
			a.setState(g, "idle", "Р“РѕС‚РѕРІ Рє РїРѕРґРєР»СЋС‡РµРЅРёСЋ")
		}
	}()
	planData, err := a.routes(ctx, "Plan", 0, p.RouteMode)
	if err != nil {
		runErr = err
		return
	}
	var plan struct {
		ControlInterface int    `json:"controlInterface"`
		Adapter          string `json:"adapter"`
	}
	if err = json.Unmarshal([]byte(planData), &plan); err != nil || plan.ControlInterface <= 0 {
		runErr = fmt.Errorf("РќРµ СѓРґР°Р»РѕСЃСЊ РІС‹Р±СЂР°С‚СЊ РІРЅРµС€РЅРµРµ СЃРѕРµРґРёРЅРµРЅРёРµ: %s", planData)
		return
	}
	a.mu.Lock()
	if a.generation != g {
		a.mu.Unlock()
		return
	}
	a.underlay = plan.Adapter
	a.mu.Unlock()
	a.appendLog("Р’РЅРµС€РЅРµРµ СЃРѕРµРґРёРЅРµРЅРёРµ: " + plan.Adapter + ". TCP Рє VK TURN.")
	a.publish()
	args := rawClientArgs(p, key, plan.ControlInterface, a.dataDir, bypassPath)
	c := command(ctx, filepath.Join(runtimeDirectory(a.root), "raw-client.exe"), args...)
	c.Dir = runtimeDirectory(a.root)
	pipe, err := c.StdoutPipe()
	if err != nil {
		runErr = err
		return
	}
	c.Stderr = c.Stdout
	a.op.Lock()
	a.mu.Lock()
	valid := a.generation == g
	a.mu.Unlock()
	if !valid || ctx.Err() != nil {
		a.op.Unlock()
		return
	}
	err = c.Start()
	if err == nil {
		a.mu.Lock()
		a.cmd = c
		a.mu.Unlock()
	}
	a.op.Unlock()
	if err != nil {
		runErr = err
		return
	}
	readySignal := make(chan struct{})
	var once sync.Once
	var timedOut bool
	var timerMu sync.Mutex
	timer := time.AfterFunc(140*time.Second, func() {
		select {
		case <-readySignal:
			return
		default:
			timerMu.Lock()
			timedOut = true
			timerMu.Unlock()
			a.mu.Lock()
			cancel := a.cancel
			valid := a.generation == g
			a.mu.Unlock()
			if valid && cancel != nil {
				cancel()
			}
		}
	})
	defer timer.Stop()
	applied := false
	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 8192), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		a.appendLog(line)
		if strings.Contains(line, "CAPTCHA_RATE_LIMIT") {
			if until := authRetryAt(a.dataDir, time.Now()); until > 0 {
				a.setState(g, a.GetSnapshot().State, authPauseMessage(until))
			}
		}
		if match := readyRE.FindStringSubmatch(line); match != nil {
			a.mu.Lock()
			valid := a.generation == g
			if valid {
				a.ready[match[1]] = true
			}
			a.mu.Unlock()
			if !valid || ctx.Err() != nil {
				break
			}
			if !applied {
				a.op.Lock()
				a.mu.Lock()
				valid = a.generation == g
				a.mu.Unlock()
				if valid && ctx.Err() == nil {
					guard := command(context.Background(), "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(runtimeDirectory(a.root), "routes.ps1"), "-Action", "Watch", "-Mode", p.RouteMode, "-OwnerPid", strconv.Itoa(os.Getpid()), "-ClientPid", strconv.Itoa(c.Process.Pid))
					guard.Dir = runtimeDirectory(a.root)
					err = guard.Start()
					if err == nil {
						go func() { _ = guard.Wait() }()
						_, err = a.routes(ctx, "Apply", c.Process.Pid, p.RouteMode)
					}
				}
				a.op.Unlock()
				if err != nil {
					runErr = err
					_ = c.Process.Kill()
					break
				}
				if !valid || ctx.Err() != nil {
					break
				}
				applied = true
				if p.RouteMode == "full" {
					a.appendLog("Р—Р°С‰РёС‚Р° РІРєР»СЋС‡РµРЅР°: IPv6 Р·Р°Р±Р»РѕРєРёСЂРѕРІР°РЅ, DNS РІРЅРµ Raw Р·Р°РїСЂРµС‰С‘РЅ.")
				}
				once.Do(func() { close(readySignal) })
				go a.stats(ctx, g)
			}
			label := "IPv4 Рё DNS С‡РµСЂРµР· Raw; IPv6 Р·Р°Р±Р»РѕРєРёСЂРѕРІР°РЅ"
			if bypassPath != "" {
				label = "IPv4 С‡РµСЂРµР· Raw СЃ РѕР±С…РѕРґРѕРј; DNS С‡РµСЂРµР· Raw; IPv6 Р·Р°Р±Р»РѕРєРёСЂРѕРІР°РЅ"
			}
			if p.RouteMode == "tunnel" {
				label = "РЎРѕРµРґРёРЅРµРЅРёРµ СЃ СЃРµСЂРІРµСЂРѕРј; РјР°СЂС€СЂСѓС‚С‹ РёРЅС‚РµСЂРЅРµС‚Р° РЅРµ РёР·РјРµРЅРµРЅС‹"
			}
			a.setState(g, "connected", label+" В· TCP В· MTU "+strconv.Itoa(p.Mtu))
		} else if match := retryRE.FindStringSubmatch(line); match != nil {
			a.mu.Lock()
			delete(a.ready, match[1])
			count := len(a.ready)
			a.mu.Unlock()
			if applied && count == 0 {
				a.setState(g, "reconnecting", "РЎРІСЏР·СЊ РїРѕС‚РµСЂСЏРЅР°, РїРѕРІС‚РѕСЂСЏРµРј РїРѕРґРєР»СЋС‡РµРЅРёРµ. РњРѕР¶РЅРѕ РѕС‚РєР»СЋС‡РёС‚СЊ Raw.")
			}
		}
	}
	waitErr := c.Wait()
	if ctx.Err() == nil && waitErr != nil {
		if until := authRetryAt(a.dataDir, time.Now()); until > 0 {
			waitErr = errors.New(authPauseMessage(until))
		}
	}
	if runErr == nil && ctx.Err() == nil {
		runErr = waitErr
	}
	timerMu.Lock()
	timeout := timedOut
	timerMu.Unlock()
	if timeout {
		runErr = errors.New("РўР°Р№РјР°СѓС‚: TCP Рє VK TURN РЅРµРґРѕСЃС‚СѓРїРµРЅ С‡РµСЂРµР· РІС‹Р±СЂР°РЅРЅРѕРµ СЃРѕРµРґРёРЅРµРЅРёРµ.")
	}
	if runErr != nil && ctx.Err() == nil {
		if until := authRetryAt(a.dataDir, time.Now()); until > 0 {
			runErr = errors.New(authPauseMessage(until))
		}
	}
	if scanErr := scanner.Err(); runErr == nil && scanErr != nil && ctx.Err() == nil {
		runErr = scanErr
	}
}
func (a *App) stats(ctx context.Context, g int) {
	timer := time.NewTicker(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			data, err := a.routes(ctx, "Stats", 0, "full")
			if err != nil {
				continue
			}
			var s struct {
				RX uint64 `json:"rx"`
				TX uint64 `json:"tx"`
			}
			if json.Unmarshal([]byte(data), &s) == nil {
				a.mu.Lock()
				if a.generation == g {
					a.rx = s.RX
					a.tx = s.TX
				}
				a.mu.Unlock()
				a.publish()
			}
		}
	}
}
func (a *App) Disconnect() error {
	a.mu.Lock()
	cancel := a.cancel
	if cancel != nil {
		cancel()
	}
	a.mu.Unlock()
	a.op.Lock()
	defer a.op.Unlock()
	a.mu.Lock()
	a.generation++
	a.clock.transition("stopping", time.Now())
	a.state = "stopping"
	a.detail = "Р’РѕСЃСЃС‚Р°РЅР°РІР»РёРІР°РµРј РїСЂРµР¶РЅРµРµ СЃРѕРµРґРёРЅРµРЅРёРµ"
	c := a.cmd
	a.cmd = nil
	a.cancel = nil
	a.bypassActive = false
	a.ready = map[string]bool{}
	a.mu.Unlock()
	a.publish()
	if c != nil && c.Process != nil {
		_ = c.Process.Kill()
	}
	_, err := a.routes(context.Background(), "Remove", 0, "full")
	a.mu.Lock()
	a.state = "idle"
	a.detail = "Р“РѕС‚РѕРІ Рє РїРѕРґРєР»СЋС‡РµРЅРёСЋ"
	if err != nil {
		a.state = "error"
		a.detail = "РќРµ СѓРґР°Р»РѕСЃСЊ РІРѕСЃСЃС‚Р°РЅРѕРІРёС‚СЊ РјР°СЂС€СЂСѓС‚С‹: " + err.Error()
	}
	a.mu.Unlock()
	a.publish()
	return err
}

func rawClientArgs(p Profile, key string, controlInterface int, dataDir, bypassPath string) []string {
	p = profileDefaults(p)
	args := []string{"-state-dir", coreStateDirectory(dataDir), "-peer", p.Server, "-links", p.VkLink, "-n", strconv.Itoa(p.Streams), "-streams-per-cred", strconv.Itoa(p.StreamsPerCred), "-transport", "tcp", "-tun", "ftraw0", "-raw-address", "10.77.0.2/24", "-raw-mtu", strconv.Itoa(p.Mtu), "-obf-profile", "rtpopus2", "-obf-key", key, "-control-interface", strconv.Itoa(controlInterface)}
	if bypassPath != "" {
		args = append(args, "-bypass-file", bypassPath)
	}
	return args
}
