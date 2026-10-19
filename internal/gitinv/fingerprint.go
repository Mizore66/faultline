package gitinv

import (
	"errors"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// Port of src/environment-fingerprint.ts:8-11.
const (
	EnvironmentFingerprintVersion  = "faultline.environment-fingerprint.v2"
	EnvironmentChangedProofMessage = "Environment changed between the passing and failing states. Supply a runtime mapping for each fingerprint before proof can continue."
)

// EnvironmentDescriptorFiles ports src/environment-fingerprint.ts:18.
var EnvironmentDescriptorFiles = []string{
	"package.json",
	"package-lock.json",
	"pnpm-lock.yaml",
	"yarn.lock",
	"bun.lock",
	"bun.lockb",
	"requirements.txt",
	"pyproject.toml",
	"poetry.lock",
	"uv.lock",
	"Pipfile.lock",
	"go.sum",
	"go.mod",
	"Cargo.toml",
	"Cargo.lock",
	"rust-toolchain.toml",
	".nvmrc",
	".node-version",
	".python-version",
	".tool-versions",
	"Dockerfile",
	"Dockerfile.faultline",
	"docker-compose.yml",
	"docker-compose.yaml",
	"compose.yml",
	"compose.yaml",
}

// Homogeneity values (src/environment-fingerprint.ts:59).
const (
	Homogeneous   = "HOMOGENEOUS"
	Heterogeneous = "HETEROGENEOUS"
	Empty         = "EMPTY"
)

// ComputeFingerprint ports src/environment-fingerprint.ts:71
// computeEnvironmentFingerprint. Resolving can throw (jsexc) when
// process.cwd() fails; file errors are returned.
func ComputeFingerprint(dir string) (jsjson.Value, error) {
	root := nodefs.Resolve(dir)
	digests := map[string]string{}
	var found []string
	for _, name := range EnvironmentDescriptorFiles {
		absolute := nodefs.Join(root, name)
		if !nodefs.Exists(absolute) {
			continue
		}
		stat, err := nodefs.Lstat(absolute)
		if err != nil {
			return jsjson.Value{}, err
		}
		if !stat.Mode().IsRegular() {
			continue
		}
		relativePath := strings.ReplaceAll(nodefs.Relative(root, absolute), `\`, "/")
		data, err := nodefs.ReadBytes(absolute)
		if err != nil {
			return jsjson.Value{}, err
		}
		if _, dup := digests[relativePath]; !dup {
			found = append(found, relativePath)
		}
		digests[relativePath] = "sha256:" + canonical.SHA256HexBytes(data)
	}
	files := jsjson.NewObj()
	for _, name := range canonical.SortLocale(found) {
		files.Set(name, jsjson.MakeString(digests[name]))
	}
	unsigned := jsjson.NewObj()
	unsigned.Set("schemaVersion", jsjson.MakeString(EnvironmentFingerprintVersion))
	unsigned.Set("files", jsjson.MakeObject(files))
	digest, err := canonical.DigestJSON(jsjson.MakeObject(unsigned))
	if err != nil {
		return jsjson.Value{}, err
	}
	signed := jsjson.NewObj()
	signed.Set("schemaVersion", jsjson.MakeString(EnvironmentFingerprintVersion))
	signed.Set("files", jsjson.MakeObject(files))
	signed.Set("digest", jsjson.MakeString(digest))
	return bundle.ParseValue(jsjson.MakeObject(signed), gitproof.EnvironmentFingerprintSchema)
}

// Homogeneity ports src/environment-fingerprint.ts:84 environmentHomogeneity
// over the fingerprints' digests.
func Homogeneity(digests []string) string {
	switch bundle.DistinctCount(digests) {
	case 0:
		return Empty
	case 1:
		return Homogeneous
	}
	return Heterogeneous
}

// DistinctFingerprintDigests ports src/environment-fingerprint.ts:100
// distinctFingerprintDigests: the unique digests in localeCompare order.
func DistinctFingerprintDigests(digests []string) []string {
	seen := map[string]bool{}
	var unique []string
	for _, d := range digests {
		if !seen[d] {
			jsexc.SetAdd(seen, d)
			unique = append(unique, d)
		}
	}
	return canonical.SortLocale(unique)
}

// MissingRuntimeMappingDigests ports src/environment-fingerprint.ts:106
// missingRuntimeMappingDigests.
func MissingRuntimeMappingDigests(digests []string, mapping []MappingEntry) []string {
	var missing []string
	for _, digest := range DistinctFingerprintDigests(digests) {
		if image, ok := lookup(mapping, digest); !ok || image == "" {
			missing = append(missing, digest)
		}
	}
	return missing
}

// ResolveRuntimeImageForFingerprint ports src/environment-fingerprint.ts:124
// resolveRuntimeImageForFingerprint. defaultImage "" is TS undefined or "".
func ResolveRuntimeImageForFingerprint(digest, homogeneity string, mapping []MappingEntry, defaultImage string) (string, error) {
	if mapped, ok := lookup(mapping, digest); ok && mapped != "" {
		return parseImage(mapped)
	}
	if homogeneity == Heterogeneous {
		return "", errors.New(EnvironmentChangedProofMessage)
	}
	if defaultImage == "" {
		return "", errors.New("No digest-pinned runtime image is available for this environment fingerprint.")
	}
	return parseImage(defaultImage)
}

func parseImage(image string) (string, error) {
	v, err := bundle.ParseValue(jsjson.MakeString(image), digestPinnedImageSchema)
	if err != nil {
		return "", err
	}
	return v.Str(), nil
}

// BuildEffectiveRuntimeMapping ports src/environment-fingerprint.ts:147
// buildEffectiveRuntimeMapping, in distinct-digest order.
func BuildEffectiveRuntimeMapping(digests []string, mapping []MappingEntry, defaultImage, homogeneity string) ([]MappingEntry, error) {
	var effective []MappingEntry
	for _, digest := range DistinctFingerprintDigests(digests) {
		image, err := ResolveRuntimeImageForFingerprint(digest, homogeneity, mapping, defaultImage)
		if err != nil {
			return nil, err
		}
		effective = append(effective, MappingEntry{digest, image})
	}
	return effective, nil
}
