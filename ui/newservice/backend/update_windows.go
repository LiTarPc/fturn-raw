package backend

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

const releaseRepository = "LiTarPc/fturn-raw"
const maxInstallerSize = 256 << 20

var releaseVersionRE = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type UpdateInfo struct {
	CurrentVersion string `json:"currentVersion"`
	Version        string `json:"version"`
	Available      bool   `json:"available"`
}
type releaseAsset struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	State string `json:"state"`
}
type appRelease struct {
	Tag        string         `json:"tag_name"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	Assets     []releaseAsset `json:"assets"`
}
type updateService struct {
	client                 *http.Client
	endpoint, downloadBase string
}
type preparedUpdate struct {
	path, digest string
}

func newUpdateService() *updateService {
	return &updateService{
		client: &http.Client{Timeout: 3 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 || req.URL.Scheme != "https" {
				return errors.New("Недопустимое перенаправление обновления")
			}
			return nil
		}},
		endpoint:     "https://api.github.com/repos/" + releaseRepository + "/releases/latest",
		downloadBase: "https://github.com/" + releaseRepository + "/releases/download/",
	}
}
func parseReleaseVersion(v string) ([3]uint64, bool) {
	var parts [3]uint64
	match := releaseVersionRE.FindStringSubmatch(v)
	if match == nil {
		return parts, false
	}
	for i := range parts {
		n, err := strconv.ParseUint(match[i+1], 10, 64)
		if err != nil {
			return parts, false
		}
		parts[i] = n
	}
	return parts, true
}
func newerRelease(candidate, current string) bool {
	next, ok := parseReleaseVersion(candidate)
	if !ok {
		return false
	}
	previous, ok := parseReleaseVersion(current)
	if !ok {
		return false
	}
	for i := range next {
		if next[i] != previous[i] {
			return next[i] > previous[i]
		}
	}
	return false
}
func (s *updateService) get(ctx context.Context, address string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "FturnRaw-Updater")
	req.Header.Set("Accept", "application/octet-stream")
	if address == s.endpoint {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	response, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Не удалось связаться с сервером обновлений: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("Сервер обновлений вернул HTTP %d. Повторите проверку позже.", response.StatusCode)
	}
	return response, nil
}
func installerName(tag string) string {
	return "fturn-raw-" + strings.TrimPrefix(tag, "v") + "-windows-x64-setup.exe"
}
func (s *updateService) latest(ctx context.Context) (appRelease, error) {
	response, err := s.get(ctx, s.endpoint)
	if err != nil {
		return appRelease{}, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return appRelease{}, err
	}
	var release appRelease
	if len(data) > 1<<20 || json.Unmarshal(data, &release) != nil {
		return release, errors.New("Некорректный ответ сервера обновлений")
	}
	if _, ok := parseReleaseVersion(release.Tag); !ok || release.Draft || release.Prerelease {
		return release, errors.New("Не найден стабильный выпуск приложения")
	}
	return release, nil
}
func (s *updateService) check(ctx context.Context, current string) (UpdateInfo, error) {
	release, err := s.latest(ctx)
	if err != nil {
		return UpdateInfo{}, err
	}
	info := UpdateInfo{CurrentVersion: current, Version: strings.TrimPrefix(release.Tag, "v"), Available: newerRelease(release.Tag, current)}
	if info.Available {
		if _, err = releaseInstallerSize(release); err != nil {
			return UpdateInfo{}, err
		}
	}
	return info, nil
}
func releaseInstallerSize(release appRelease) (int64, error) {
	var size int64
	installerCount, manifestCount := 0, 0
	for _, asset := range release.Assets {
		if asset.State != "uploaded" {
			continue
		}
		if asset.Name == installerName(release.Tag) {
			installerCount++
			size = asset.Size
		}
		if asset.Name == "SHA256SUMS" && asset.Size > 0 && asset.Size <= 1<<20 {
			manifestCount++
		}
	}
	if installerCount != 1 || manifestCount != 1 || size <= 0 || size > maxInstallerSize {
		return 0, errors.New("В выпуске пока нет полного установщика Windows и контрольных сумм. Повторите проверку позже.")
	}
	return size, nil
}
func manifestDigest(data []byte, name string) (string, error) {
	digest := ""
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		value, err := hex.DecodeString(fields[0])
		if err != nil || len(value) != sha256.Size || digest != "" {
			return "", errors.New("Некорректная контрольная сумма установщика")
		}
		digest = strings.ToLower(fields[0])
	}
	if digest == "" {
		return "", errors.New("В выпуске отсутствует контрольная сумма установщика")
	}
	return digest, nil
}
func (p preparedUpdate) cleanup() { _ = os.Remove(p.path); _ = os.Remove(filepath.Dir(p.path)) }
func (s *updateService) prepare(ctx context.Context, current string) (preparedUpdate, error) {
	release, err := s.latest(ctx)
	if err != nil {
		return preparedUpdate{}, err
	}
	if !newerRelease(release.Tag, current) {
		return preparedUpdate{}, errors.New("Более новая версия приложения не найдена")
	}
	size, err := releaseInstallerSize(release)
	if err != nil {
		return preparedUpdate{}, err
	}
	base := s.downloadBase + url.PathEscape(release.Tag) + "/"
	response, err := s.get(ctx, base+"SHA256SUMS")
	if err != nil {
		return preparedUpdate{}, err
	}
	manifest, readErr := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	response.Body.Close()
	if readErr != nil {
		return preparedUpdate{}, readErr
	}
	if len(manifest) > 1<<20 {
		return preparedUpdate{}, errors.New("Слишком большой файл контрольных сумм")
	}
	name := installerName(release.Tag)
	digest, err := manifestDigest(manifest, name)
	if err != nil {
		return preparedUpdate{}, err
	}
	dir, err := os.MkdirTemp("", "fturn-raw-update-")
	if err != nil {
		return preparedUpdate{}, err
	}
	prepared := preparedUpdate{path: filepath.Join(dir, name), digest: digest}
	success := false
	defer func() {
		if !success {
			prepared.cleanup()
		}
	}()
	response, err = s.get(ctx, base+name)
	if err != nil {
		return preparedUpdate{}, err
	}
	defer response.Body.Close()
	file, err := os.OpenFile(prepared.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return preparedUpdate{}, err
	}
	hash := sha256.New()
	count, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, size+1))
	closeErr := file.Close()
	if err = errors.Join(copyErr, closeErr); err != nil {
		return preparedUpdate{}, err
	}
	if count != size || hex.EncodeToString(hash.Sum(nil)) != digest {
		return preparedUpdate{}, errors.New("Проверка установщика не пройдена. Обновление отменено; повторите загрузку.")
	}
	success = true
	return prepared, nil
}
func powershellLiteral(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
func updateInstallerScript(prepared preparedUpdate, root string, pid int) string {
	return fmt.Sprintf(`$ErrorActionPreference='Stop'
$owner=Get-Process -Id %d -ErrorAction SilentlyContinue
if($owner){$owner.WaitForExit()}
$installer=%s
$stream=[IO.File]::OpenRead($installer)
$sha=[Security.Cryptography.SHA256]::Create()
try{$actual=[BitConverter]::ToString($sha.ComputeHash($stream)).Replace('-','')}finally{$stream.Dispose();$sha.Dispose()}
if($actual -ine %s){throw 'Installer checksum changed'}
$process=Start-Process -FilePath $installer -ArgumentList %s -WindowStyle Normal -Wait -PassThru
if($process.ExitCode -eq 0){Remove-Item -LiteralPath $installer;Remove-Item -LiteralPath %s}
`, pid, powershellLiteral(prepared.path), powershellLiteral(prepared.digest), powershellLiteral("/D="+root), powershellLiteral(filepath.Dir(prepared.path)))
}
func launchUpdateInstaller(prepared preparedUpdate, root string) error {
	units := utf16.Encode([]rune(updateInstallerScript(prepared, root, os.Getpid())))
	data := make([]byte, len(units)*2)
	for i, v := range units {
		binary.LittleEndian.PutUint16(data[i*2:], v)
	}
	cmd := command(context.Background(), "powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(data))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
func (a *App) CheckForUpdates() (UpdateInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return a.updates.check(ctx, Version)
}
func (a *App) InstallUpdate() error {
	a.op.Lock()
	defer a.op.Unlock()
	a.mu.Lock()
	running := a.cancel != nil || connectionRunning(a.state)
	installing := a.installing
	a.mu.Unlock()
	if running {
		return errors.New("Отключите соединение перед обновлением приложения")
	}
	if installing {
		return errors.New("Установка обновления уже запущена")
	}
	if a.ctx == nil || a.desktop == nil || a.desktop.host == nil {
		return errors.New("Обновление доступно в приложении Windows")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 3*time.Minute)
	defer cancel()
	prepared, err := a.updates.prepare(ctx, Version)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		prepared.cleanup()
		return err
	}
	if err = launchUpdateInstaller(prepared, a.root); err != nil {
		prepared.cleanup()
		return fmt.Errorf("Не удалось запустить установщик: %w", err)
	}
	a.mu.Lock()
	a.installing = true
	a.mu.Unlock()
	go a.desktop.exit()
	return nil
}
