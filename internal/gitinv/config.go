// Package gitinv ports the Git investigation support code: image
// configuration, runtime mappings and environment fingerprints.
package gitinv

import (
	"errors"
	"os"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	s "github.com/Mizore66/faultline/internal/schema"
)

// Port of src/config.ts:6-7.
const (
	FaultLineConfigSchemaVersion = "faultline.config.v1"
	FaultLineConfigFilename      = "config.json"
)

const digestPinnedImageSource = `^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[a-f0-9]{64}$`

// DigestPinnedImage ports src/config.ts:9 DIGEST_PINNED_IMAGE.
var DigestPinnedImage = s.Pattern(digestPinnedImageSource)

// isTagFreeDigestImage ports src/config.ts:11. The pattern admits ASCII
// only, so byte and UTF-16 indexes agree.
func isTagFreeDigestImage(value string) bool {
	if !DigestPinnedImage(value) {
		return false
	}
	repository := value[:strings.IndexByte(value, '@')]
	return strings.LastIndexByte(repository, ':') <= strings.LastIndexByte(repository, '/')
}

func tagFreeImage(message string) s.Schema {
	return s.Refine(s.String(), func(v jsjson.Value) bool { return isTagFreeDigestImage(v.Str()) }, message)
}

// FaultLineConfigSchema ports src/config.ts:17.
var FaultLineConfigSchema = s.Object(
	s.F("schemaVersion", s.LiteralString(FaultLineConfigSchemaVersion)),
	s.F("image", s.Optional(tagFreeImage("image must be a tag-free digest-pinned reference"))),
	s.F("runtime", s.Optional(s.Object(
		s.F("alias", s.Enum("node", "python", "go")),
		s.F("image", tagFreeImage("runtime.image must be a tag-free digest-pinned reference")),
	).Strict())),
	s.F("snapshot", s.Object(
		s.F("trackedFilesOnly", s.Boolean()),
	).Strict()),
	s.F("stores", s.Object(
		s.F("incidents", s.String(s.MinLength(1))),
		s.F("witnesses", s.String(s.MinLength(1))),
		s.F("recordings", s.String(s.MinLength(1))),
	).Strict()),
).Strict()

// FaultLineConfigPath ports src/config.ts:54 faultLineConfigPath. Resolving
// can throw (jsexc) when process.cwd() fails.
func FaultLineConfigPath(repository string) string {
	return nodefs.Join(nodefs.Resolve(repository), ".faultline", FaultLineConfigFilename)
}

// ReadConfig ports src/config.ts:58 readFaultLineConfig. TS's null is
// (zero, false, nil). Failures keep their Node or zod error types (read
// their messages with jsexc.Message).
func ReadConfig(repo string) (jsjson.Value, bool, error) {
	path := FaultLineConfigPath(repo)
	if !nodefs.Exists(path) {
		return jsjson.Value{}, false, nil
	}
	stat, err := nodefs.Lstat(path)
	if err != nil {
		return jsjson.Value{}, false, err
	}
	if stat.Mode()&os.ModeSymlink != 0 || !stat.Mode().IsRegular() {
		return jsjson.Value{}, false, errors.New(jsexc.Concat("FaultLine config must be a regular non-symlink file: ", path))
	}
	parsed, err := bundle.ParseFile(path, FaultLineConfigSchema)
	if err != nil {
		return jsjson.Value{}, false, err
	}
	return parsed, true, nil
}

// ResolveConfiguredImage ports the config half of src/config.ts:122
// resolveConfiguredImage: the image persisted in the project config. A
// `--image` option overrides it before this is called.
func ResolveConfiguredImage(repo string) (string, bool, error) {
	config, ok, err := ReadConfig(repo)
	if err != nil || !ok {
		return "", false, err
	}
	image := config.Get("image")
	if image.Kind() != jsjson.String {
		return "", false, nil
	}
	return image.Str(), true, nil
}
