package backend

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
)

// Personal data is independent of the directory containing executables and route state.
func userDataDirectory() (string, error) {
	path, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		return "", fmt.Errorf("Не удалось найти LocalAppData: %w", err)
	}
	return filepath.Join(path, "fturn-raw"), nil
}
func migrateUserData(root, dataDir string) error {
	if dataDir == "" {
		return errors.New("Папка данных приложения не определена.")
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("Папка данных приложения: %w", err)
	}
	if strings.EqualFold(filepath.Clean(root), filepath.Clean(dataDir)) {
		return nil
	}
	if err := migrateProfiles(root, dataDir); err != nil {
		return err
	}
	return migrateSettings(root, dataDir)
}
func destinationExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}
func migrateProfiles(root, dataDir string) error {
	target := filepath.Join(dataDir, "profiles.json")
	exists, err := destinationExists(target)
	if err != nil || exists {
		return err
	}
	source := filepath.Join(root, "profiles.json")
	data, err := os.ReadFile(source)
	var book profileBook
	if os.IsNotExist(err) {
		source = filepath.Join(root, "connection.json")
		data, err = os.ReadFile(source)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		var p Profile
		if json.Unmarshal(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}), &p) != nil {
			return errors.New("Не удалось перенести connection.json: файл повреждён.")
		}
		if p.Server == "" {
			return nil
		}
		id, err := profileID()
		if err != nil {
			return err
		}
		book = profileBook{1, id, []SavedProfile{{ID: id, Name: p.Server, Profile: p}}}
	} else {
		if err != nil {
			return err
		}
		book, err = decodeProfileBook(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}))
		if err != nil {
			return err
		}
	}
	for i := range book.Profiles {
		key, err := validate(book.Profiles[i].Profile, root)
		if err != nil {
			return fmt.Errorf("Не удалось перенести профиль %q: %w", book.Profiles[i].Name, err)
		}
		book.Profiles[i].Profile.Key = key
		book.Profiles[i].Profile.KeyFile = ""
	}
	encoded, err := json.MarshalIndent(book, "", "  ")
	if err != nil {
		return err
	}
	return createDataFile(target, encoded)
}
func migrateSettings(root, dataDir string) error {
	target := filepath.Join(dataDir, "app-settings.json")
	exists, err := destinationExists(target)
	if err != nil || exists {
		return err
	}
	data, err := os.ReadFile(filepath.Join(root, "app-settings.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if _, err = decodeAppSettings(data); err != nil {
		return err
	}
	return createDataFile(target, data)
}

// Write the whole file before publication, and never replace an existing destination.
func createDataFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".migration-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = f.Chmod(0600)
	if err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	from, err := windows.UTF16PtrFromString(f.Name())
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	err = windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) || errors.Is(err, windows.ERROR_FILE_EXISTS) {
		return nil
	}
	return err
}
