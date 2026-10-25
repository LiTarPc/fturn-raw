package backend

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

const uiLogMaxBytes int64 = 5 * 1024 * 1024
const uiLogBackups = 3

// The caller serializes rotation and append with App.logMu.
func appendRotatingLog(path, entry string, limit int64, backups int) error {
	if int64(len(entry)) > limit {
		suffix := " [truncated]\n"
		cut := int(limit) - len(suffix)
		if cut < 0 {
			return errors.New("log size limit too small")
		}
		for cut > 0 && entry[cut]&0xc0 == 0x80 {
			cut--
		}
		entry = entry[:cut] + suffix
	}
	info, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if info != nil && info.Size() > 0 && info.Size()+int64(len(entry)) > limit {
		for i := backups; i >= 1; i-- {
			destination := path + "." + strconv.Itoa(i)
			if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("rotate log: %w", err)
			}
			source := path
			if i > 1 {
				source = path + "." + strconv.Itoa(i-1)
			}
			if err := os.Rename(source, destination); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("rotate log: %w", err)
			}
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.WriteString(entry)
	return errors.Join(writeErr, f.Close())
}
