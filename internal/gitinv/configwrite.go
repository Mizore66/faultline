package gitinv

import (
	"errors"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/safedir"
)

func strObj(pairs ...string) *jsjson.Obj {
	o := jsjson.NewObj()
	for i := 0; i < len(pairs); i += 2 {
		o.Set(pairs[i], jsjson.MakeString(pairs[i+1]))
	}
	return o
}

func snapshotValue() jsjson.Value {
	o := jsjson.NewObj()
	o.Set("trackedFilesOnly", jsjson.MakeBool(false))
	return jsjson.MakeObject(o)
}

// DefaultConfig ports src/config.ts:38 defaultFaultLineConfig. Resolving
// can throw (jsexc) when process.cwd() fails.
func DefaultConfig(repo string) jsjson.Value {
	root := nodefs.Resolve(repo)
	cfg := jsjson.NewObj()
	cfg.Set("schemaVersion", jsjson.MakeString(FaultLineConfigSchemaVersion))
	cfg.Set("snapshot", snapshotValue())
	cfg.Set("stores", jsjson.MakeObject(strObj(
		"incidents", nodefs.Join(root, ".faultline", "incidents"),
		"witnesses", nodefs.Join(root, ".faultline", "witnesses"),
		"recordings", nodefs.Join(root, ".faultline", "recordings"),
	)))
	parsed, err := bundle.ParseValue(jsjson.MakeObject(cfg), FaultLineConfigSchema)
	if err != nil {
		panic(err)
	}
	return parsed
}

// WriteConfig ports src/config.ts:62 writeFaultLineConfig: parse, mkdir 0o700,
// trusted-path check, then a non-atomic 0o600 write.
func WriteConfig(repo string, cfg jsjson.Value) (string, error) {
	parsed, err := bundle.ParseValue(cfg, FaultLineConfigSchema)
	if err != nil {
		return "", err
	}
	path := FaultLineConfigPath(repo)
	directory := nodefs.Dirname(path)
	if err := nodefs.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	safeDir, err := safedir.ResolveTrustedSystemPath(directory)
	if err != nil {
		return "", err
	}
	nested, err := safedir.RelativeTrustedSystemPath(safeDir, path)
	if err != nil {
		return "", err
	}
	if nested == "" || strings.HasPrefix(nested, "..") || strings.Contains(nested, ":") {
		return "", errors.New("FaultLine config path escaped .faultline")
	}
	if err := nodefs.WriteFileUTF8(path, jsjson.StringifyIndent(parsed, "  ")+"\n", 0o600, false); err != nil {
		return "", err
	}
	return path, nil
}

// EnsureConfigOptions are the options of ensureFaultLineConfig; an empty
// RuntimeAlias or Image means absent.
type EnsureConfigOptions struct{ Repository, RuntimeAlias, Image string }

// EnsureConfigResult is the result of ensureFaultLineConfig; Status is
// CREATED, UPDATED or UNCHANGED.
type EnsureConfigResult struct {
	Path   string
	Config jsjson.Value
	Status string
}

func cloneObj(o *jsjson.Obj) *jsjson.Obj {
	c := jsjson.NewObj()
	for _, k := range o.Keys() {
		c.Set(k, o.Field(k))
	}
	return c
}

// EnsureConfig ports src/config.ts:84 ensureFaultLineConfig.
func EnsureConfig(o EnsureConfigOptions) (EnsureConfigResult, error) {
	existing, ok, err := ReadConfig(o.Repository)
	if err != nil {
		return EnsureConfigResult{}, err
	}
	base := existing
	if !ok {
		base = DefaultConfig(o.Repository)
	}
	merged := cloneObj(base.Obj())
	if o.Image != "" {
		merged.Set("image", jsjson.MakeString(o.Image))
	}
	if o.RuntimeAlias != "" && o.Image != "" {
		merged.Set("runtime", jsjson.MakeObject(strObj("alias", o.RuntimeAlias, "image", o.Image)))
	} else if o.RuntimeAlias != "" && base.Obj().Field("runtime").Kind() == jsjson.Object {
		rt := cloneObj(base.Obj().Field("runtime").Obj())
		rt.Set("alias", jsjson.MakeString(o.RuntimeAlias))
		merged.Set("runtime", jsjson.MakeObject(rt))
	}
	merged.Set("snapshot", snapshotValue())
	next, err := bundle.ParseValue(jsjson.MakeObject(merged), FaultLineConfigSchema)
	if err != nil {
		return EnsureConfigResult{}, err
	}
	path, err := WriteConfig(o.Repository, next)
	if err != nil {
		return EnsureConfigResult{}, err
	}
	status := "UPDATED"
	if !ok {
		status = "CREATED"
	} else if jsjson.Stringify(existing) == jsjson.Stringify(next) {
		status = "UNCHANGED"
	}
	return EnsureConfigResult{Path: path, Config: next, Status: status}, nil
}

// UpdateConfigImage ports src/config.ts:107 updateFaultLineConfigImage; an
// empty runtimeAlias means absent.
func UpdateConfigImage(repo, image, runtimeAlias string) (jsjson.Value, error) {
	base, ok, err := ReadConfig(repo)
	if err != nil {
		return jsjson.Value{}, err
	}
	if !ok {
		base = DefaultConfig(repo)
	}
	merged := cloneObj(base.Obj())
	merged.Set("image", jsjson.MakeString(image))
	if runtimeAlias != "" {
		merged.Set("runtime", jsjson.MakeObject(strObj("alias", runtimeAlias, "image", image)))
	}
	next, err := bundle.ParseValue(jsjson.MakeObject(merged), FaultLineConfigSchema)
	if err != nil {
		return jsjson.Value{}, err
	}
	if _, err := WriteConfig(repo, next); err != nil {
		return jsjson.Value{}, err
	}
	return next, nil
}
