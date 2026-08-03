//go:build !windows

package browser

// DetectDefault is a no-op on non-Windows platforms, where there is no
// registry-based default browser detection and the proxy is not configured
// through the Windows system proxy settings.
func DetectDefault() (Type, string, error) {
	return Unknown, "", nil
}
