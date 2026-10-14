package gitinv

import (
	"errors"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	s "github.com/Mizore66/faultline/internal/schema"
)

const sha256DigestSource = `^sha256:[a-f0-9]{64}$`

var (
	// src/runtime-mapping-file.ts:5-9. The digest regex has no message.
	digestSchema            = s.String(s.Regex(s.Pattern(sha256DigestSource), ""))
	digestPinnedImageSchema = s.String(s.Regex(DigestPinnedImage, "expected a digest-pinned image reference"))
)

// MappingEntry is one fingerprint -> image pair of a runtime mapping.
type MappingEntry struct{ Fingerprint, Image string }

// lookup is `mapping[digest]`; ok is false for undefined.
func lookup(mapping []MappingEntry, digest string) (string, bool) {
	for _, e := range mapping {
		if e.Fingerprint == digest {
			return e.Image, true
		}
	}
	return "", false
}

// LoadRuntimeMapping ports src/runtime-mapping-file.ts:67
// loadRuntimeMappingDocument. The entries keep Object.entries order.
func LoadRuntimeMapping(doc jsjson.Value) ([]MappingEntry, error) {
	if doc.Kind() != jsjson.Object {
		return nil, errors.New("runtime mapping must be a JSON object")
	}
	payload := doc
	if m := doc.Obj().Field("mapping"); m.Kind() == jsjson.Object {
		payload = m
	}
	var mapping []MappingEntry
	for _, digest := range payload.Obj().Keys() {
		switch digest {
		case "schemaVersion", "generatedAt", "source", "provenanceDigest":
			continue
		}
		image := payload.Obj().Field(digest)
		if image.Kind() != jsjson.String {
			return nil, errors.New(jsexc.Concat("runtime mapping entry for ", digest, " must be a digest-pinned image string"))
		}
		if _, err := bundle.ParseValue(jsjson.MakeString(digest), digestSchema); err != nil {
			return nil, err
		}
		if _, err := bundle.ParseValue(image, digestPinnedImageSchema); err != nil {
			return nil, err
		}
		mapping = append(mapping, MappingEntry{digest, image.Str()})
	}
	return mapping, nil
}
