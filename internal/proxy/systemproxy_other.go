//go:build !windows

package proxy

import "fmt"

type SavedProxy struct {
	Enabled  bool
	Server   string
	Override string
}

func GetSystemProxy() (*SavedProxy, error) {
	return &SavedProxy{}, nil
}

func SetSystemProxy(addr string) error {
	return fmt.Errorf("system proxy auto-configuration is only supported on Windows")
}

func RestoreSystemProxy(saved *SavedProxy) error {
	return nil
}

func SaveBackup(saved *SavedProxy, path string) error {
	return nil
}

func LoadBackup(path string) (*SavedProxy, error) {
	return nil, fmt.Errorf("system proxy is only supported on Windows")
}

func RemoveBackup(path string) error {
	return nil
}
