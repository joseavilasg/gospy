package proxy

// ProcessInfo describes the client process that owns a proxied connection. On
// Windows it comes from the system TCP table + Authenticode verification; on
// Linux it comes from /proc and the signature fields stay zero (no Authenticode
// equivalent exists for ELF binaries).
type ProcessInfo struct {
	PID         uint32
	Path        string
	Name        string
	DisplayName string
	IsSigned    *bool
	SignerName  string
	SignerReady bool
}
