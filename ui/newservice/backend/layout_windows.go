package backend

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func runtimeDirectory(root string) string {
	nested := filepath.Join(root, "runtime")
	if info, err := os.Stat(filepath.Join(nested, "raw-client.exe")); err == nil && info.Mode().IsRegular() {
		return nested
	}
	return root
}
func coreStateDirectory(dataDir string) string { return filepath.Join(dataDir, "core") }

// Preserve the installation's identity when moving mutable state away from binaries.
func migrateCoreState(root, dataDir string) error {
	dest := coreStateDirectory(dataDir)
	for _, name := range []string{"client_config.json", "vk_persona.json", "vk_auth_pause.json"} {
		target := filepath.Join(dest, name)
		exists, err := destinationExists(target)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		for _, sourceDir := range []string{root, filepath.Join(root, "runtime")} {
			source := filepath.Join(sourceDir, name)
			info, err := os.Lstat(source)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
				return fmt.Errorf("Unsupported legacy state: %s", name)
			}
			data, err := os.ReadFile(source)
			if err != nil {
				return err
			}
			data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
			if !json.Valid(data) {
				return fmt.Errorf("Invalid legacy state: %s", name)
			}
			if err = os.MkdirAll(dest, 0700); err != nil {
				return err
			}
			if err = createDataFile(target, data); err != nil {
				return err
			}
			break
		}
	}
	return nil
}

// Archive exact legacy filenames only after user-data migration. Never remove a
// file unless its complete, verified backup exists in AppData. Unknown files stay.
func archiveLegacyData(root, dataDir string) error {
	if runtimeDirectory(root) == root || strings.EqualFold(filepath.Clean(root), filepath.Clean(dataDir)) {
		return nil
	}
	archive := filepath.Join(dataDir, "legacy-install")
	for _, name := range []string{"connection.json", "profiles.json", "app-settings.json", "client_config.json", "vk_persona.json", "vk_auth_pause.json"} {
		source := filepath.Join(root, name)
		info, err := os.Lstat(source)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
			continue
		}
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		if !json.Valid(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})) {
			continue
		}
		if err = os.MkdirAll(archive, 0700); err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		target := filepath.Join(archive, fmt.Sprintf("%s-%x.bak", name, digest[:8]))
		if err = createDataFile(target, data); err != nil {
			return err
		}
		backup, err := os.ReadFile(target)
		if err != nil {
			return err
		}
		current, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		if !bytes.Equal(backup, data) || !bytes.Equal(current, data) {
			return fmt.Errorf("Legacy backup verification failed: %s", name)
		}
		if err = os.Remove(source); err != nil {
			return err
		}
	}
	return nil
}
