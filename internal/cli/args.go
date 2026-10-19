package cli

import (
	"errors"
	"slices"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/safedir"
)

// hasFlag ports src/cli-app.ts:140.
func hasFlag(args []string, flag string) bool { return slices.Contains(args, flag) }

// option ports src/cli-app.ts:144: the element after the first occurrence of
// flag. ok is false when the flag is absent or last (TS `undefined`).
func option(args []string, flag string) (value string, ok bool) {
	i := slices.Index(args, flag)
	if i < 0 || i+1 >= len(args) {
		return "", false
	}
	return args[i+1], true
}

// requiredOption ports src/cli-app.ts:149: an absent, empty or "--"-prefixed
// value is missing.
func requiredOption(args []string, flag string) (string, error) {
	value, ok := option(args, flag)
	if !ok || value == "" || strings.HasPrefix(value, "--") {
		return "", fail("Missing required option: %s", flag)
	}
	return value, nil
}

// witnessStore ports src/cli-app.ts:252. Resolving can throw (jsexc) when
// process.cwd() fails.
func witnessStore(args []string) string {
	store, ok := option(args, "--store")
	if !ok {
		store = ".faultline/witnesses"
	}
	return nodefs.Resolve(store)
}

// readJSONInput ports src/cli-app.ts:244: every failure, resolve's included,
// becomes "Unable to read JSON input <file>: <message>".
func readJSONInput(file string) (jsjson.Value, error) {
	var v jsjson.Value
	if err := jsexc.Try(func() { v = jsexc.Must(bundle.ParseJSONFile(nodefs.Resolve(file))) }); err != nil {
		return jsjson.Value{}, errors.New(jsexc.Concat("Unable to read JSON input ", file, ": ", jsexc.Message(err)))
	}
	return v, nil
}

// requireImageOrConfig ports src/cli-app.ts:161: `--image` overrides the
// image persisted in .faultline/config.json (resolveConfiguredImage,
// src/config.ts:122).
func requireImageOrConfig(repository string, args []string) (string, error) {
	image, ok := option(args, "--image")
	if !ok {
		var err error
		if image, ok, err = gitinv.ResolveConfiguredImage(repository); err != nil {
			return "", err
		}
	}
	if !ok {
		return "", fail("Missing required image: pass --image <digest-pinned-image> or run fl init / fl runtime prepare so `.faultline/config.json` stores one.")
	}
	return image, nil
}

// realDirectorySegment is resolveSafeDirectorySegment !== null, with its
// lstat error thrown as in TS.
func realDirectorySegment(path string) bool {
	_, ok, err := safedir.ResolveSafeDirectorySegment(path)
	if err != nil {
		jsexc.Throw(err)
	}
	return ok
}

// safeManagedFileOutput ports src/cli-app.ts:265: keep write-once CLI
// artifacts inside a managed, non-symlink FaultLine root.
func safeManagedFileOutput(outputFile, managedRoot, label string) (string, error) {
	root := nodefs.Resolve(managedRoot)
	output := nodefs.Resolve(outputFile)
	nested := jsexc.Must(safedir.RelativeTrustedSystemPath(root, output))
	if nested == "" || strings.HasPrefix(nested, "..") || nodefs.IsAbsolute(nested) {
		return "", fail("%s output must be a file beneath %s", label, root)
	}
	parts := strings.FieldsFunc(nested, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) < 1 {
		return "", fail("%s output must have a file name.", label)
	}
	current := root
	if nodefs.Exists(current) {
		if !realDirectorySegment(current) {
			return "", fail("%s root must be a real directory: %s", label, root)
		}
	}
	for _, part := range parts[:len(parts)-1] {
		current = nodefs.Join(current, part)
		if !nodefs.Exists(current) {
			continue
		}
		if !realDirectorySegment(current) {
			return "", fail("%s output cannot traverse a symbolic link or non-directory: %s", label, current)
		}
	}
	if nodefs.Exists(output) {
		return "", fail("%s output already exists and will not be replaced: %s", label, output)
	}
	return output, nil
}
