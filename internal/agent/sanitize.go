package agent

import (
	"regexp"
	"strconv"
	"strings"
)

// sensitiveWords are header-name tokens that always mark a header as sensitive.
// The lowercased header name is split on '-', '_', '.' and space, and each token
// is compared exactly - so "key" matches X-Api-Key and X-Customer-Key but never
// Monkey-Header or X-Keycloak-Realm (no substring false positives).
var sensitiveWords = []string{
	"auth", "authorization", "authenticate", "bearer",
	"cookie", "cookie2", "credential", "csrf",
	"key", "password", "passwd", "secret",
	"session", "sid", "sig", "signature", "token", "apikey", "pwd",
}

func isSensitiveHeader(name string) bool {
	ln := strings.ToLower(name)
	for _, w := range strings.FieldsFunc(ln, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == ' '
	}) {
		for _, s := range sensitiveWords {
			if w == s {
				return true
			}
		}
	}
	return false
}

// jwtRe matches a JWT compact serialization (3 segments for JWS, up to 5 for
// JWE), optionally prefixed with "Bearer ". Header payloads start with the
// base64url of '{"', i.e. "eyJ".
var jwtRe = regexp.MustCompile(`(?i)^\s*(bearer\s+)?eyj[a-z0-9_-]+(\.[a-z0-9_-]+){2,4}\s*$`)

// opaqueBearerRe matches an opaque (non-JWT) Bearer token, which has no header
// structure to verify - a leading "Bearer " with a token of at least 8 chars.
var opaqueBearerRe = regexp.MustCompile(`(?i)^\s*bearer\s+\S{8,}\s*$`)

func valueLooksSensitive(v string) bool {
	return jwtRe.MatchString(v) || opaqueBearerRe.MatchString(v)
}

func redactValue(v string) string {
	return "<redacted len=" + strconv.Itoa(len(v)) + ">"
}

// SanitizeHeaders returns a copy of h with sensitive values replaced by a
// "<redacted len=N>" placeholder. A header whose NAME carries a sensitive token
// has every value redacted unconditionally; a name-clean header is redacted per
// value only when one of its values looks like a JWT or a Bearer token, so a
// benign value in a mixed multi-value header stays readable. The input map is
// never mutated.
func SanitizeHeaders(h map[string][]string) map[string][]string {
	out := make(map[string][]string, len(h))
	for k, v := range h {
		if isSensitiveHeader(k) {
			redacted := make([]string, len(v))
			for i, val := range v {
				redacted[i] = redactValue(val)
			}
			out[k] = redacted
			continue
		}
		if !anyValueLooksSensitive(v) {
			out[k] = append([]string(nil), v...)
			continue
		}
		redacted := make([]string, len(v))
		for i, val := range v {
			if valueLooksSensitive(val) {
				redacted[i] = redactValue(val)
			} else {
				redacted[i] = val
			}
		}
		out[k] = redacted
	}
	return out
}

func anyValueLooksSensitive(vs []string) bool {
	for _, v := range vs {
		if valueLooksSensitive(v) {
			return true
		}
	}
	return false
}
