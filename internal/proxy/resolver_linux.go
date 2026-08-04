//go:build linux

package proxy

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ClientResolver resolves the process that owns a proxied connection by reading
// the kernel connection table (/proc/net/tcp[6]) and linking each client socket
// inode back to its PID through /proc/<pid>/fd. Linux has no Authenticode
// equivalent, so the signature fields on the returned ProcessInfo stay zero.
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
	r := &ClientResolver{
		portToInfo: make(map[uint16]*ProcessInfo),
		pathCache:  make(map[uint32]*ProcessInfo),
		proxyPID:   uint32(os.Getpid()),
		proxyPort:  parsePort(proxyAddr),
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
	port := parsePort(remoteAddr)
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
	owners := scanInodeOwners()
	rows := readTCPTable()

	r.mu.Lock()
	defer r.mu.Unlock()

	for port, inode := range clientInodes(rows, r.proxyPort) {
		if _, exists := r.portToInfo[port]; exists {
			continue
		}
		pid, ok := owners[inode]
		if !ok || pid == 0 || pid == r.proxyPID {
			continue
		}
		info := r.resolvePIDLocked(pid)
		if info != nil {
			r.portToInfo[port] = info
			if r.onUpdate != nil {
				go r.onUpdate(port, info)
			}
		}
	}
}

func (r *ClientResolver) resolveFromTable(port uint16) *ProcessInfo {
	inode, found := lookupClientInode(readTCPTable(), r.proxyPort, port)
	if !found {
		return nil
	}
	pid := inodeOwnerPID(inode)
	if pid == 0 || pid == r.proxyPID {
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

	info := buildProcessInfo(pid)
	if info == nil {
		return nil
	}

	r.mu.Lock()
	r.pathCache[pid] = info
	r.mu.Unlock()
	return info
}

func (r *ClientResolver) resolvePIDLocked(pid uint32) *ProcessInfo {
	if cached, ok := r.pathCache[pid]; ok {
		return cached
	}
	info := buildProcessInfo(pid)
	if info == nil {
		return nil
	}
	r.pathCache[pid] = info
	return info
}

func readTCPTable() []tcpRow {
	var rows []tcpRow
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		rows = append(rows, parseTCPTable(data)...)
	}
	return rows
}

func lookupClientInode(rows []tcpRow, proxyPort, clientPort uint16) (uint64, bool) {
	for _, row := range rows {
		if row.state == establishedState && row.localPort == clientPort && row.remotePort == proxyPort {
			return row.inode, true
		}
	}
	return 0, false
}

func buildProcessInfo(pid uint32) *ProcessInfo {
	procDir := filepath.Join("/proc", strconv.FormatUint(uint64(pid), 10))

	name, err := os.ReadFile(filepath.Join(procDir, "comm"))
	if err != nil {
		return nil
	}

	path, err := os.Readlink(filepath.Join(procDir, "exe"))
	if err != nil || path == "" {
		return nil
	}

	nameStr := strings.TrimSpace(string(name))
	return &ProcessInfo{
		PID:         pid,
		Path:        path,
		Name:        nameStr,
		DisplayName: nameStr,
	}
}

// scanInodeOwners maps every socket inode visible in the kernel to the PID that
// holds it by scanning /proc/<pid>/fd symlinks for "socket:[<inode>]".
func scanInodeOwners() map[uint64]uint32 {
	owners := make(map[uint64]uint32)
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return owners
	}
	for _, e := range entries {
		pid, err := strconv.ParseUint(e.Name(), 10, 32)
		if err != nil {
			continue
		}
		fdDir := filepath.Join("/proc", e.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			if !strings.HasPrefix(target, "socket:[") || !strings.HasSuffix(target, "]") {
				continue
			}
			inode, err := strconv.ParseUint(target[len("socket:["):len(target)-1], 10, 64)
			if err != nil {
				continue
			}
			if _, ok := owners[inode]; !ok {
				owners[inode] = uint32(pid)
			}
		}
	}
	return owners
}

// inodeOwnerPID finds the PID holding a specific socket inode. Used on the
// on-demand resolveFromTable path instead of a full scan.
func inodeOwnerPID(inode uint64) uint32 {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	for _, e := range entries {
		pid, err := strconv.ParseUint(e.Name(), 10, 32)
		if err != nil {
			continue
		}
		fdDir := filepath.Join("/proc", e.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			if target == "socket:["+strconv.FormatUint(inode, 10)+"]" {
				return uint32(pid)
			}
		}
	}
	return 0
}
