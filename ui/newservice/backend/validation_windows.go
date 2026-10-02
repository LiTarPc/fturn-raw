package backend

import (
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func validateFields(p Profile) error {
	host, port, err := net.SplitHostPort(p.Server)
	if err != nil || net.ParseIP(host) == nil || net.ParseIP(host).To4() == nil {
		return errors.New("Укажите IPv4:порт сервера.")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return errors.New("Недопустимый порт сервера.")
	}
	u, err := url.Parse(p.VkLink)
	if err != nil || u.Scheme != "https" || (u.Host != "vk.ru" && u.Host != "vk.com") || !strings.HasPrefix(u.Path, "/call/join/") || len(strings.TrimPrefix(u.Path, "/call/join/")) == 0 {
		return errors.New("Укажите HTTPS-ссылку VK-звонка.")
	}
	if p.Mtu < 576 || p.Mtu > 1500 {
		return errors.New("MTU должен быть от 576 до 1500 и совпадать с сервером.")
	}
	if p.Streams < 1 || p.Streams > 64 {
		return errors.New("Количество потоков должно быть от 1 до 64.")
	}
	if p.RouteMode != "full" && p.RouteMode != "tunnel" {
		return errors.New("Неизвестный режим трафика.")
	}
	return nil
}
func validate(p Profile, root string) (string, error) {
	if err := validateFields(p); err != nil {
		return "", err
	}
	key := strings.TrimSpace(p.Key)
	if key == "" {
		path := strings.TrimSpace(p.KeyFile)
		if path == "" {
			return "", errors.New("Введите Raw key: 64 шестнадцатеричных символа.")
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", errors.New("Не удалось прочитать старый файл ключа. Введите Raw key вручную.")
		}
		key = strings.TrimSpace(string(data))
	}
	decoded, err := hex.DecodeString(key)
	if err != nil || len(decoded) != 32 {
		return "", errors.New("Raw key должен содержать 64 шестнадцатеричных символа.")
	}
	return hex.EncodeToString(decoded), nil
}
