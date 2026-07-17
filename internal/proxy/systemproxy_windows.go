//go:build windows

package proxy

import (
	"encoding/json"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const (
	internetOptionSettingsChanged = 39
	internetOptionRefresh         = 37
)

var (
	modwininet            = syscall.NewLazyDLL("wininet.dll")
	procInternetSetOption = modwininet.NewProc("InternetSetOptionW")
)

type SavedProxy struct {
	Enabled  bool
	Server   string
	Override string
}

func GetSystemProxy() (*SavedProxy, error) {
	key, err := registry.OpenKey(
		registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		registry.QUERY_VALUE,
	)
	if err != nil {
		return nil, fmt.Errorf("open registry: %w", err)
	}
	defer key.Close()

	enableVal, _, err := key.GetIntegerValue("ProxyEnable")
	if err != nil {
		return nil, fmt.Errorf("read ProxyEnable: %w", err)
	}

	server, _, _ := key.GetStringValue("ProxyServer")
	override, _, _ := key.GetStringValue("ProxyOverride")

	return &SavedProxy{
		Enabled:  enableVal == 1,
		Server:   server,
		Override: override,
	}, nil
}

func SetSystemProxy(addr string) error {
	key, err := registry.OpenKey(
		registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		registry.SET_VALUE,
	)
	if err != nil {
		return fmt.Errorf("open registry: %w", err)
	}
	defer key.Close()

	if err := key.SetStringValue("ProxyServer", addr); err != nil {
		return fmt.Errorf("set ProxyServer: %w", err)
	}

	if err := key.SetDWordValue("ProxyEnable", 1); err != nil {
		return fmt.Errorf("set ProxyEnable: %w", err)
	}

	if err := key.SetStringValue("ProxyOverride", "<local>;127.*"); err != nil {
		return fmt.Errorf("set ProxyOverride: %w", err)
	}

	notifySystemSettingsChanged()
	return nil
}

func RestoreSystemProxy(saved *SavedProxy) error {
	key, err := registry.OpenKey(
		registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		registry.SET_VALUE,
	)
	if err != nil {
		return fmt.Errorf("open registry: %w", err)
	}
	defer key.Close()

	enableVal := uint32(0)
	if saved.Enabled {
		enableVal = 1
		if err := key.SetStringValue("ProxyServer", saved.Server); err != nil {
			return fmt.Errorf("restore ProxyServer: %w", err)
		}
		if err := key.SetStringValue("ProxyOverride", saved.Override); err != nil {
			return fmt.Errorf("restore ProxyOverride: %w", err)
		}
	}

	if err := key.SetDWordValue("ProxyEnable", enableVal); err != nil {
		return fmt.Errorf("restore ProxyEnable: %w", err)
	}

	notifySystemSettingsChanged()
	return nil
}

func notifySystemSettingsChanged() {
	procInternetSetOption.Call(0, uintptr(internetOptionSettingsChanged), 0, 0)
	procInternetSetOption.Call(0, uintptr(internetOptionRefresh), 0, 0)
}

func SaveBackup(saved *SavedProxy, path string) error {
	data, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal backup: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write backup: %w", err)
	}
	return nil
}

func LoadBackup(path string) (*SavedProxy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read backup: %w", err)
	}
	var saved SavedProxy
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, fmt.Errorf("unmarshal backup: %w", err)
	}
	return &saved, nil
}

func RemoveBackup(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove backup: %w", err)
	}
	return nil
}
