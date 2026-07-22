//go:build windows

package proxy

import (
	"net"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/one-api/winutil/network"
	"github.com/one-api/winutil/system"
)

var versionLikeRe = regexp.MustCompile(`^\d+(\.\d+)+$`)

var pathPatterns = []struct {
	prefix string
	levels int
}{
	{"AppData\\Local\\", 1},
	{"AppData\\Roaming\\", 1},
	{"Program Files (x86)\\", 1},
	{"Program Files\\", 1},
}

var genericDirs = map[string]bool{
	"bin": true, "lib": true, "app": true, "application": true,
	"contents": true, "macos": true, "plugins": true, "appmain": true,
	"resources": true, "out": true, "dist": true, "build": true,
	"packed": true, "unpacked": true, "node_modules": true,
	"win": true, "linux": true, "app.asar.unpacked": true, "app.asar": true,
}

func processDisplayName(exePath string) (name, displayName string) {
	base := filepath.Base(exePath)
	normPath := strings.ReplaceAll(exePath, "/", "\\")

	for _, pat := range pathPatterns {
		idx := strings.Index(normPath, pat.prefix)
		if idx < 0 {
			continue
		}
		rest := normPath[idx+len(pat.prefix):]
		parts := strings.SplitN(rest, "\\", pat.levels+1)
		if len(parts) >= pat.levels+1 {
			appName := parts[pat.levels-1]
			if appName != "" && !versionLikeRe.MatchString(appName) {
				return base + " (" + appName + ")", appName
			}
		}
	}

	dir := filepath.Dir(exePath)
	for i := 0; i < 6; i++ {
		dirName := filepath.Base(dir)
		if dirName == "." || dirName == string(filepath.Separator) {
			break
		}
		if !versionLikeRe.MatchString(dirName) && !genericDirs[strings.ToLower(dirName)] {
			return base + " (" + dirName + ")", dirName
		}
		dir = filepath.Dir(dir)
	}

	return base, ""
}

type ProcessInfo struct {
	PID         uint32
	Path        string
	Name        string
	DisplayName string
	IsSigned    *bool
	SignerName  string
	SignerReady bool
}

type ClientResolver struct {
	mu         sync.RWMutex
	portToInfo map[uint16]*ProcessInfo
	pathCache  map[uint32]*ProcessInfo
	proxyPID   uint32
	proxyPort  uint16
	stopCh     chan struct{}
	onUpdate   func(port uint16, info *ProcessInfo)
}

func NewClientResolver(proxyAddr string) *ClientResolver {
	_, portStr, _ := net.SplitHostPort(proxyAddr)
	var proxyPort uint16
	for _, c := range portStr {
		if c >= '0' && c <= '9' {
			proxyPort = proxyPort*10 + uint16(c-'0')
		}
	}

	r := &ClientResolver{
		portToInfo: make(map[uint16]*ProcessInfo),
		pathCache:  make(map[uint32]*ProcessInfo),
		proxyPID:   uint32(syscall.Getpid()),
		proxyPort:  proxyPort,
		stopCh:     make(chan struct{}),
	}
	r.refresh()
	go r.refreshLoop()
	return r
}

func (r *ClientResolver) Stop() {
	close(r.stopCh)
}

func (r *ClientResolver) OnUpdate(fn func(uint16, *ProcessInfo)) {
	r.onUpdate = fn
}

func (r *ClientResolver) Resolve(remoteAddr string) *ProcessInfo {
	_, portStr, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return nil
	}
	var port uint16
	for _, c := range portStr {
		if c >= '0' && c <= '9' {
			port = port*10 + uint16(c-'0')
		}
	}
	if port == 0 {
		return nil
	}

	r.mu.RLock()
	info, ok := r.portToInfo[port]
	r.mu.RUnlock()
	if ok {
		return info
	}

	info = r.resolveFromTable(port)
	if info != nil {
		r.mu.Lock()
		r.portToInfo[port] = info
		r.mu.Unlock()
	}
	return info
}

func (r *ClientResolver) GetAllProcesses() map[string]*ProcessInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make(map[string]*ProcessInfo)
	for _, info := range r.portToInfo {
		if info.Name != "" {
			if existing, ok := result[info.Name]; !ok || existing.PID != info.PID {
				result[info.Name] = info
			}
		}
	}
	return result
}

func (r *ClientResolver) resolveFromTable(port uint16) *ProcessInfo {
	pid, err := network.GetPortOwner(port, "TCP")
	if err != nil || pid == 0 || pid == r.proxyPID {
		return nil
	}
	return r.resolvePID(pid)
}

func (r *ClientResolver) resolvePID(pid uint32) *ProcessInfo {
	r.mu.RLock()
	cached, ok := r.pathCache[pid]
	r.mu.RUnlock()
	if ok {
		return cached
	}

	path, err := system.GetExecutablePathByPID(pid)
	if err != nil || path == "" {
		return nil
	}

	name, displayName := processDisplayName(path)
	info := &ProcessInfo{
		PID:         pid,
		Path:        path,
		Name:        name,
		DisplayName: displayName,
	}

	r.mu.Lock()
	r.pathCache[pid] = info
	r.mu.Unlock()

	return info
}

func (r *ClientResolver) refreshLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.refresh()
		case <-r.stopCh:
			return
		}
	}
}

func (r *ClientResolver) refresh() {
	rows, err := network.GetActiveSockets()
	if err != nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, row := range rows {
		if row.State != "ESTABLISHED" {
			continue
		}
		if row.PID == 0 || row.PID == r.proxyPID {
			continue
		}

		var clientPort uint16
		if row.LocalPort == r.proxyPort {
			clientPort = row.RemotePort
		} else if row.RemotePort == r.proxyPort {
			clientPort = row.LocalPort
		} else {
			continue
		}

		if clientPort == 0 {
			continue
		}

		if _, exists := r.portToInfo[clientPort]; exists {
			continue
		}

		info := r.resolvePIDLocked(row.PID)
		if info != nil {
			r.portToInfo[clientPort] = info
			if r.onUpdate != nil {
				go r.onUpdate(clientPort, info)
			}
		}
	}
}

func (r *ClientResolver) resolvePIDLocked(pid uint32) *ProcessInfo {
	if cached, ok := r.pathCache[pid]; ok {
		return cached
	}

	path, err := system.GetExecutablePathByPID(pid)
	if err != nil || path == "" {
		return nil
	}

	name, displayName := processDisplayName(path)
	info := &ProcessInfo{
		PID:         pid,
		Path:        path,
		Name:        name,
		DisplayName: displayName,
	}

	r.pathCache[pid] = info
	return info
}
