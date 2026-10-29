// Package turnproof is the port of src/turn-proof-bundle.ts.
package turnproof

import (
	"strings"

	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsstr"
)

// Port of src/turn-proof-bundle.ts:59-67.
const (
	BundleSchemaVersion    = "faultline.turn-proof-bundle.v1"
	SourceSchemaVersion    = "faultline.turn-proof-source.v1"
	MaxSourceArtifactBytes = 128 * 1024 * 1024
	MaxHashCatalogBytes    = 4 * 1024 * 1024
	MaxArtifacts           = 2048
	MaxTotalBytes          = 512 * 1024 * 1024
)

// IsSafeRelativeArtifactPath ports turn-proof-bundle.ts:72.
func IsSafeRelativeArtifactPath(value string) bool {
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

// Catalog is the Map<string, string> parseHashCatalog returns: Keys in
// first-insertion order, Digests by artifact (a duplicate's last digest).
type Catalog struct {
	Keys    []string
	Digests map[string]string
}

// catalogLine is /^([a-f0-9]{64})  (.+)$/ where JS `.` excludes \r, U+2028
// and U+2029 (\n cannot occur after the split). It follows
// gitproof.parseCatalogLine.
func catalogLine(line string) (digest, artifact string, ok bool) {
	if len(line) < 67 || line[64:66] != "  " {
		return "", "", false
	}
	for i := 0; i < 64; i++ {
		c := line[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return "", "", false
		}
	}
	artifact = line[66:]
	if strings.ContainsAny(artifact, "\r  ") {
		return "", "", false
	}
	return line[:64], artifact, true
}

// ParseHashCatalog ports turn-proof-bundle.ts:390 parseHashCatalog (no entry cap).
func ParseHashCatalog(hashes []byte, errs *[]string) Catalog {
	c := Catalog{Digests: map[string]string{}}
	text := jsstr.DecodeUTF8(hashes)
	if !strings.HasSuffix(text, "\n") {
		*errs = append(*errs, "hashes.txt must end with a trailing newline")
	}
	for _, line := range strings.Split(jsstr.TrimEnd(text), "\n") {
		digest, artifact, ok := catalogLine(line)
		if !ok {
			*errs = append(*errs, jsexc.Concat("hashes.txt contains a malformed line: ", line))
			continue
		}
		if !IsSafeRelativeArtifactPath(artifact) {
			*errs = append(*errs, jsexc.Concat("hashes.txt contains an unsafe artifact path: ", line))
			continue
		}
		if _, dup := c.Digests[artifact]; dup {
			*errs = append(*errs, jsexc.Concat("hashes.txt declares duplicate artifact: ", artifact))
		} else {
			c.Keys = append(c.Keys, artifact)
		}
		jsexc.MapSet(c.Digests, artifact, digest)
	}
	return c
}
