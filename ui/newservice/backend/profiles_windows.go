package backend

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

type SavedProfile struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Profile Profile `json:"profile"`
}
type profileBook struct {
	Version  int            `json:"version"`
	ActiveID string         `json:"activeId"`
	Profiles []SavedProfile `json:"profiles"`
}

func profileID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}
func validName(name string) bool {
	if strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 80 {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func (a *App) loadProfiles() {
	data, err := os.ReadFile(filepath.Join(a.dataDir, "profiles.json"))
	if err == nil {
		book, err := decodeProfileBook(data)
		if err != nil {
			a.storageErr = err
			a.state = "error"
			a.detail = err.Error()
			return
		}
		for _, p := range book.Profiles {
			if p.ID == book.ActiveID {
				a.profile = p.Profile
				break
			}
		}
		a.profiles = book.Profiles
		a.activeID = book.ActiveID
		return
	}
	if !os.IsNotExist(err) {
		a.storageErr = errors.New("Не удалось прочитать profiles.json.")
		a.state = "error"
		a.detail = a.storageErr.Error()
		return
	}
	data, err = os.ReadFile(filepath.Join(a.root, "connection.json"))
	if os.IsNotExist(err) {
		return
	}
	if err != nil || json.Unmarshal(data, &a.profile) != nil {
		a.storageErr = errors.New("Не удалось прочитать старый connection.json.")
		a.state = "error"
		a.detail = a.storageErr.Error()
		return
	}
	// A blank public example is not a saved connection.
	if a.profile.Server == "" {
		return
	}
	if key, err := validate(a.profile, a.root); err == nil {
		a.profile.Key = key
		a.profile.KeyFile = ""
	}
	id, err := profileID()
	if err != nil {
		a.storageErr = err
		return
	}
	a.activeID = id
	a.profiles = []SavedProfile{{ID: id, Name: a.profile.Server, Profile: a.profile}}
}
func (a *App) canChangeProfiles() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.storageErr != nil {
		return a.storageErr
	}
	if a.cancel != nil {
		return errors.New("Отключите Raw перед изменением или выбором профиля.")
	}
	return nil
}

// New entries may be saved while connected, provided the active profile stays intact.
func (a *App) canStoreProfiles() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.storageErr
}
func (a *App) canPersistProfiles(active string, selected Profile) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.storageErr != nil {
		return a.storageErr
	}
	if a.cancel != nil && (active != a.activeID || selected != a.profile) {
		return errors.New("Отключи Raw перед изменением или выбором активного профиля.")
	}
	return nil
}

// Caller holds op. Disk is replaced before changing in-memory state.
func (a *App) persistProfiles(entries []SavedProfile, active string) error {
	var selected Profile
	found := false
	for _, p := range entries {
		if p.ID == active {
			selected = p.Profile
			found = true
		}
	}
	if !found {
		return errors.New("Профиль не найден.")
	}
	if err := a.canPersistProfiles(active, selected); err != nil {
		return err
	}
	data, err := json.MarshalIndent(profileBook{1, active, entries}, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(a.dataDir, "profiles.json")
	temp, err := os.CreateTemp(a.dataDir, ".profiles-*.tmp")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	err = temp.Chmod(0600)
	if err == nil {
		_, err = temp.Write(data)
	}
	if err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	a.mu.Lock()
	a.profiles = entries
	a.activeID = active
	a.profile = selected
	a.mu.Unlock()
	a.publish()
	return nil
}
func (a *App) SaveNamedProfile(p Profile, name string, create bool) error {
	a.op.Lock()
	defer a.op.Unlock()
	return a.saveNamedProfile(p, name, create)
}
func (a *App) saveNamedProfile(p Profile, name string, create bool) error {
	check := a.canChangeProfiles
	if create {
		check = a.canStoreProfiles
	}
	if err := check(); err != nil {
		return err
	}
	p.Server = strings.TrimSpace(p.Server)
	p.VkLink = strings.TrimSpace(p.VkLink)
	key, err := validate(p, a.root)
	if err != nil {
		return err
	}
	p.Key = key
	p.KeyFile = ""
	entries := append([]SavedProfile{}, a.profiles...)
	active := a.activeID
	if create || len(entries) == 0 {
		if len(entries) >= 128 {
			return errors.New("Можно сохранить не более 128 профилей.")
		}
		if name == "" {
			name = p.Server
		}
		name = strings.TrimSpace(name)
		if !validName(name) {
			return errors.New("Введите имя профиля длиной до 80 символов.")
		}
		id, err := profileID()
		if err != nil {
			return err
		}
		entries = append(entries, SavedProfile{id, name, p})
		a.mu.Lock()
		running := a.cancel != nil
		a.mu.Unlock()
		if !running {
			active = id
		}
	} else {
		for i := range entries {
			if entries[i].ID == active {
				if name != "" {
					name = strings.TrimSpace(name)
					if !validName(name) {
						return errors.New("Введите имя профиля длиной до 80 символов.")
					}
					entries[i].Name = name
				}
				entries[i].Profile = p
			}
		}
	}
	return a.persistProfiles(entries, active)
}
func (a *App) SelectProfile(id string) error {
	a.op.Lock()
	defer a.op.Unlock()
	return a.persistProfiles(append([]SavedProfile{}, a.profiles...), id)
}
func (a *App) DeleteProfile(id string) error {
	a.op.Lock()
	defer a.op.Unlock()
	if err := a.canChangeProfiles(); err != nil {
		return err
	}
	if len(a.profiles) <= 1 {
		return errors.New("Последний профиль нельзя удалить. Сначала добавьте другой.")
	}
	entries := []SavedProfile{}
	found := false
	active := a.activeID
	for _, p := range a.profiles {
		if p.ID == id {
			found = true
		} else {
			entries = append(entries, p)
		}
	}
	if !found {
		return errors.New("Профиль не найден.")
	}
	if active == id {
		active = entries[0].ID
	}
	return a.persistProfiles(entries, active)
}

func decodeProfileBook(data []byte) (profileBook, error) {
	var book profileBook
	invalid := json.Unmarshal(data, &book) != nil || book.Version != 1 || len(book.Profiles) == 0 || len(book.Profiles) > 128
	seen := map[string]bool{}
	found := false
	for _, p := range book.Profiles {
		if p.ID == "" || seen[p.ID] || !validName(p.Name) {
			invalid = true
		}
		seen[p.ID] = true
		if p.ID == book.ActiveID {
			found = true
		}
	}
	if invalid || !found {
		return profileBook{}, errors.New("profiles.json повреждён. Исходный файл оставлен без изменений.")
	}
	return book, nil
}
