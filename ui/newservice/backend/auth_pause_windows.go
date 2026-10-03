package backend

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

func authRetryAt(dataDir string, now time.Time) int64 {
	f, err := os.Open(filepath.Join(coreStateDirectory(dataDir), "vk_auth_pause.json"))
	if err != nil {
		return 0
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1025))
	if err != nil || len(data) > 1024 {
		return 0
	}
	var v struct {
		Until int64 `json:"until"`
	}
	if json.Unmarshal(data, &v) != nil || v.Until <= now.Unix() || v.Until > now.Add(24*time.Hour).Unix() {
		return 0
	}
	return v.Until
}
func authPauseMessage(until int64) string {
	return fmt.Sprintf("VK ограничил попытки капчи. Пауза клиента до %s; срок ограничения VK может быть дольше.", time.Unix(until, 0).Format("15:04:05"))
}
