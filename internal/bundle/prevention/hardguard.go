package prevention

import (
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// HardGuardDigestsFromStrings ports hardGuardDigestsFromStrings
// (src/prevention-from-artifacts.ts:543): each value's "sha256:" digest
// (a lone surrogate hashes as U+FFFD), first occurrences kept as
// `new Set` keeps them, sorted with localeCompare. No src/ caller.
func HardGuardDigestsFromStrings(values []string) []string {
	seen := map[string]bool{}
	digests := []string{}
	for _, value := range values {
		digest := "sha256:" + canonical.SHA256Hex(value)
		if !seen[digest] {
			seen[digest] = true
			digests = append(digests, digest)
		}
	}
	return canonical.SortLocale(digests)
}

// WriteHardGuardDigestSidecar ports writeHardGuardDigestSidecar
// (src/prevention-from-artifacts.ts:547): `JSON.stringify({digests}, null,
// 2)` plus "\n" written to <directory>/hard-guard-digests.json (flag "w",
// the default mode), then the "sha256:" digest of the file's bytes read
// back. Errors are Node's. No src/ caller.
func WriteHardGuardDigestSidecar(directory string, digests []string) (string, error) {
	path := nodefs.Join(directory, "hard-guard-digests.json")
	items := make([]jsjson.Value, len(digests))
	for i, d := range digests {
		items[i] = jsjson.MakeString(d)
	}
	text := jsjson.StringifyIndent(jsjson.MustObject("digests", jsjson.MakeArray(items)), "  ") + "\n"
	if err := nodefs.WriteFileUTF8(path, text, 0o666, false); err != nil {
		return "", err
	}
	b, err := nodefs.ReadBytes(path)
	if err != nil {
		return "", err // coverage gate: race only (the file was just written)
	}
	return "sha256:" + canonical.SHA256HexBytes(b), nil
}
