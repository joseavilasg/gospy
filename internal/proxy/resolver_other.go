//go:build !windows

package proxy

type ProcessInfo struct {
	PID         uint32
	Path        string
	Name        string
	DisplayName string
	IsSigned    *bool
	SignerName  string
	SignerReady bool
}

type ClientResolver struct{}

func NewClientResolver(proxyAddr string) *ClientResolver {
	return &ClientResolver{}
}

func (r *ClientResolver) Stop() {}

func (r *ClientResolver) OnUpdate(fn func(uint16, *ProcessInfo)) {}

func (r *ClientResolver) Resolve(remoteAddr string) *ProcessInfo {
	return nil
}

func (r *ClientResolver) GetAllProcesses() map[string]*ProcessInfo {
	return nil
}
