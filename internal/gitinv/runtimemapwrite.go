package gitinv

import (
	"errors"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// RuntimeMappingFileSchemaVersion ports src/runtime-mapping-file.ts:11.
const RuntimeMappingFileSchemaVersion = "faultline.runtime-mapping.v1"

// RuntimeMappingWrite holds the options of writeRuntimeMappingFile; an empty
// GeneratedAt or Source means absent.
type RuntimeMappingWrite struct {
	Pairs                           []MappingEntry
	OutputPath, GeneratedAt, Source string
}

// WrittenRuntimeMapping ports src/runtime-mapping-file.ts:18.
type WrittenRuntimeMapping struct {
	Path             string
	Mapping          *jsjson.Obj
	ProvenanceDigest string
	GeneratedAt      string
}

// WriteRuntimeMapping ports src/runtime-mapping-file.ts:30 writeRuntimeMappingFile.
func WriteRuntimeMapping(o RuntimeMappingWrite) (WrittenRuntimeMapping, error) {
	if len(o.Pairs) == 0 {
		return WrittenRuntimeMapping{}, errors.New("runtime mapping write requires at least one --fingerprint / --image pair")
	}
	mapping := jsjson.NewObj()
	for _, pair := range o.Pairs {
		fp, err := bundle.ParseValue(jsjson.MakeString(pair.Fingerprint), digestSchema)
		if err != nil {
			return WrittenRuntimeMapping{}, err
		}
		im, err := bundle.ParseValue(jsjson.MakeString(pair.Image), digestPinnedImageSchema)
		if err != nil {
			return WrittenRuntimeMapping{}, err
		}
		if prev, ok := mapping.Get(fp.Str()); ok && prev.Str() != im.Str() {
			return WrittenRuntimeMapping{}, errors.New(jsexc.Concat("conflicting runtime mapping for ", fp.Str()))
		}
		mapping.Set(fp.Str(), im)
	}
	generatedAt := o.GeneratedAt
	if generatedAt == "" {
		generatedAt = clock.ISO(clock.Now())
	}
	source := o.Source
	if source == "" {
		source = "fl runtime mapping write"
	}
	unsigned := jsjson.NewObj()
	unsigned.Set("schemaVersion", jsjson.MakeString(RuntimeMappingFileSchemaVersion))
	unsigned.Set("generatedAt", jsjson.MakeString(generatedAt))
	unsigned.Set("source", jsjson.MakeString(source))
	unsigned.Set("mapping", jsjson.MakeObject(mapping))
	digest, err := canonical.DigestJSON(jsjson.MakeObject(unsigned))
	if err != nil {
		return WrittenRuntimeMapping{}, err
	}
	document := cloneObj(unsigned)
	document.Set("provenanceDigest", jsjson.MakeString(digest))
	path := nodefs.Resolve(o.OutputPath)
	if err := nodefs.MkdirAll(nodefs.Dirname(path), 0o777); err != nil {
		return WrittenRuntimeMapping{}, err
	}
	if err := nodefs.WriteFileUTF8(path, jsjson.StringifyIndent(jsjson.MakeObject(document), "  ")+"\n", 0o666, false); err != nil {
		return WrittenRuntimeMapping{}, err
	}
	return WrittenRuntimeMapping{Path: path, Mapping: mapping, ProvenanceDigest: digest, GeneratedAt: generatedAt}, nil
}
