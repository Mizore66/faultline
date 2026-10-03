package gitproof

import (
	"encoding/base64"
	"regexp"
	"strings"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
)

// isSafeRelativeArtifactPath ports git-proof-bundle.ts:67 (and witness-lock.ts:14 isSafeOverlayPath).
func isSafeRelativeArtifactPath(value string) bool {
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, `\`) || strings.Contains(value, "\x00") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func safePathValue(v jsjson.Value) bool { return isSafeRelativeArtifactPath(v.Str()) }

// safeGitRevision is /^(?!-)[^\0\r\n]{1,512}$/ counted in UTF-16 units.
func safeGitRevision(value string) bool {
	units := jsstr.ToUTF16(value)
	if len(units) < 1 || len(units) > 512 || units[0] == '-' {
		return false
	}
	for _, u := range units {
		if u == 0 || u == '\r' || u == '\n' {
			return false
		}
	}
	return true
}

var base64Shape = regexp.MustCompile(`^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$`)

// isCanonicalBase64 ports witness-lock.ts:20.
func isCanonicalBase64(value string) bool {
	if !base64Shape.MatchString(value) {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	return err == nil && base64.StdEncoding.EncodeToString(decoded) == value
}

func canonicalBase64Value(v jsjson.Value) bool { return isCanonicalBase64(v.Str()) }
