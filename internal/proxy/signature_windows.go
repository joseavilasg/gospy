//go:build windows

package proxy

import (
	"time"

	"github.com/one-api/winutil/executable"
)

func verifyFile(filePath string) *SignatureResult {
	result := &SignatureResult{
		FilePath:   filePath,
		VerifiedAt: time.Now(),
	}

	signed, err := executable.VerifySignature(filePath)
	if err != nil {
		result.Error = err.Error()
		result.IsSigned = false
		return result
	}

	result.IsSigned = signed
	if signed {
		cert, err := executable.GetLeafCertificate(filePath)
		if err == nil {
			result.SignerName = cert.Subject.CommonName
		}
	}

	return result
}
