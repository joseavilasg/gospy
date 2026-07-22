//go:build !windows

package proxy

import "time"

func verifyFile(filePath string) *SignatureResult {
	return &SignatureResult{
		FilePath:   filePath,
		IsSigned:   false,
		Error:      "signature verification not supported on this platform",
		VerifiedAt: time.Now(),
	}
}
