package runtimeimage

import (
	"errors"
	"io/fs"
	"runtime"
	"strings"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsre"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// Port of src/runtime.ts:48-49.
const (
	ProjectRuntimeImageMaxContextFiles = 10_000
	ProjectRuntimeImageMaxContextBytes = 256 * 1024 * 1024
)

// Port of src/runtime.ts:56 SHA256_DIGEST.
var sha256Digest = jsre.MustCompile(`^sha256:[a-f0-9]{64}$`, "")

// Port of the regexes in src/runtime.ts:497 validateProjectImageTag.
// `[\0\r\n\s@]` is written without \0 (jsre has no \0 escape); NUL is
// checked separately. \s already covers \r and \n.
var (
	projectTagUnsafe     = jsre.MustCompile(`[\r\n\s@]`, "")
	projectTagRepository = jsre.MustCompile(`^[a-z0-9][a-z0-9._:/-]*$`, "")
	projectTagTag        = jsre.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`, "")
)

// PreparationError ports src/runtime.ts:147
// ProjectRuntimeImagePreparationError; Code is its .code.
type PreparationError struct{ Code, Message string }

func (e *PreparationError) Error() string { return e.Message }

func preparationError(code string, parts ...string) *PreparationError {
	return &PreparationError{Code: code, Message: jsexc.Concat(parts...)}
}

// Port of src/runtime.ts:340 projectImageInputError.
func projectImageInputError(parts ...string) *PreparationError {
	return preparationError("PROJECT_IMAGE_INVALID_INPUT", parts...)
}

// ProjectBuildInput ports src/runtime.ts:191
// ProjectRuntimeImageBuildRequest. Network must be "none" or "default".
type ProjectBuildInput struct {
	ContextDirectory, Dockerfile, ImageTag, Network string
}

// normalizedBuild ports src/runtime.ts:334 NormalizedProjectRuntimeImageBuild.
type normalizedBuild struct {
	contextDirectory, dockerfile, imageTag, network string
}

// Port of src/runtime.ts:344 assertSafeProjectBuildPath.
func assertSafeProjectBuildPath(label, value string) error {
	if value == "" || strings.ContainsAny(value, "\x00\r\n") {
		return projectImageInputError("Project image ", label, " must be non-empty and cannot contain a NUL or line break.")
	}
	return nil
}

// Port of src/runtime.ts:350 isDescendantOrSame.
func isDescendantOrSame(directory, candidate string) bool {
	sep := "/"
	if runtime.GOOS == "windows" {
		sep = "\\"
	}
	fromDirectory := nodefs.Relative(directory, candidate)
	return fromDirectory == "" ||
		(!strings.HasPrefix(fromDirectory, ".."+sep) && fromDirectory != ".." && !nodefs.IsAbsolute(fromDirectory))
}

// errorDetail is `error instanceof Error ? `: ${error.message}` : ""`: every
// error the Go ports raise stands for a JS Error.
func errorDetail(err error) string { return ": " + jsexc.Message(err) }

// Port of src/runtime.ts:358 normalizeProjectBuildPaths.
func normalizeProjectBuildPaths(in ProjectBuildInput) (normalizedBuild, error) {
	if err := assertSafeProjectBuildPath("contextDirectory", in.ContextDirectory); err != nil {
		return normalizedBuild{}, err
	}
	if err := assertSafeProjectBuildPath("dockerfile", in.Dockerfile); err != nil {
		return normalizedBuild{}, err
	}
	if !nodefs.IsAbsolute(in.ContextDirectory) {
		return normalizedBuild{}, projectImageInputError("Project image contextDirectory must be an absolute path.")
	}

	var contextDirectory, dockerfile string
	resolvePaths := func() error {
		var err error
		if contextDirectory, err = nodefs.RealpathSync(nodefs.Resolve(in.ContextDirectory)); err != nil {
			return err
		}
		info, err := nodefs.Stat(contextDirectory)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return projectImageInputError("Project image contextDirectory is not a directory: ", in.ContextDirectory)
		}
		if contextDirectory == nodefs.ParseRoot(contextDirectory) {
			return projectImageInputError("Project image contextDirectory cannot be a filesystem root.")
		}
		// A relative Dockerfile is always relative to the reviewed build
		// context, rather than the caller's process cwd.
		if dockerfile, err = nodefs.RealpathSync(nodefs.Resolve(contextDirectory, in.Dockerfile)); err != nil {
			return err
		}
		if info, err = nodefs.Stat(dockerfile); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return projectImageInputError("Project image dockerfile is not a regular file: ", in.Dockerfile)
		}
		return nil
	}
	var err error
	if thrown := jsexc.Try(func() { err = resolvePaths() }); thrown != nil {
		err = thrown
	}
	if err != nil {
		var pe *PreparationError
		if errors.As(err, &pe) {
			return normalizedBuild{}, err
		}
		return normalizedBuild{}, projectImageInputError("FaultLine could not resolve the project image build paths", errorDetail(err))
	}

	if !isDescendantOrSame(contextDirectory, dockerfile) {
		return normalizedBuild{}, projectImageInputError("Project image dockerfile must resolve inside the declared contextDirectory.")
	}
	tag, err := validateProjectImageTag(in.ImageTag)
	if err != nil {
		return normalizedBuild{}, err
	}
	return normalizedBuild{contextDirectory: contextDirectory, dockerfile: dockerfile, imageTag: tag, network: in.Network}, nil
}

// Port of src/runtime.ts:497 validateProjectImageTag. The lastIndexOf and
// slice calls use byte offsets: they only locate ASCII '/' and ':', byte and
// UTF-16 offsets order the same way, and the colon is the last character in
// both or neither. Both slices are then checked against ASCII-only regexes,
// so every accepted tag (the value the curated.go helpers index) is ASCII.
func validateProjectImageTag(imageTag string) (string, error) {
	if imageTag == "" || strings.IndexByte(imageTag, 0) >= 0 || projectTagUnsafe.Test(imageTag) {
		return "", projectImageInputError("Project imageTag must be a non-empty, tag-based Docker reference without whitespace or @.")
	}
	lastSlash := strings.LastIndexByte(imageTag, '/')
	lastColon := strings.LastIndexByte(imageTag, ':')
	if lastColon <= lastSlash || lastColon == len(imageTag)-1 {
		return "", projectImageInputError("Project imageTag must include an explicit non-latest tag (for example local/faultline:deps-20260717).")
	}
	repository := imageTag[:lastColon]
	tag := imageTag[lastColon+1:]
	if !projectTagRepository.Test(repository) || !projectTagTag.Test(tag) {
		return "", projectImageInputError("Project imageTag is not a safe Docker repository:tag reference.")
	}
	if strings.ToLower(tag) == "latest" {
		return "", projectImageInputError("Project imageTag cannot use the mutable latest tag; choose an explicit setup tag.")
	}
	return imageTag, nil
}

// Port of src/runtime.ts:588 assertProjectBuildNetwork.
func assertProjectBuildNetwork(network string) error {
	if network != "none" && network != "default" {
		return projectImageInputError("Project image network must be either none or default.")
	}
	return nil
}

// contextFingerprint ports src/runtime.ts:395 ProjectBuildContextFingerprint.
type contextFingerprint struct {
	contextDigest, dockerfileDigest string
	fileCount                       int
	totalBytes                      int64
}

// Port of src/runtime.ts:409 fingerprintProjectBuildContext: a depth-first,
// pre-order walk of the context, each directory in localeCompare order with
// subdirectories entered at their sorted position. Links and special files
// are refused, and the file count and byte total are capped. The context
// root itself is not lstat'ed.
func fingerprintProjectBuildContext(n normalizedBuild) (contextFingerprint, error) {
	var files []jsjson.Value
	var totalBytes int64

	var visit func(directory, relativeDirectory string) error
	visit = func(directory, relativeDirectory string) error {
		var entries []string
		var err error
		if thrown := jsexc.Try(func() {
			var names []string
			if names, err = nodefs.ReadDirNames(directory); err == nil {
				entries = canonical.SortLocale(names)
			}
		}); thrown != nil {
			err = thrown
		}
		if err != nil {
			return projectImageInputError("FaultLine could not enumerate the project image build context", errorDetail(err))
		}
		for _, name := range entries {
			child := nodefs.Resolve(directory, name)
			relativePath := name
			if relativeDirectory != "" {
				relativePath = jsexc.Concat(relativeDirectory, "/", name)
			}
			details, err := nodefs.Lstat(child)
			if err != nil {
				return projectImageInputError("FaultLine could not inspect build-context path ", relativePath, errorDetail(err))
			}
			if details.Mode()&fs.ModeSymlink != 0 {
				return projectImageInputError("Project image build context contains a symbolic link (", relativePath, "); use a self-contained, regular-file context so the reviewed digest is unambiguous.")
			}
			if details.IsDir() {
				if err := visit(child, relativePath); err != nil {
					return err
				}
				continue
			}
			if !details.Mode().IsRegular() {
				return projectImageInputError("Project image build context contains a non-regular file (", relativePath, "); use a self-contained regular-file context.")
			}
			if len(files) >= ProjectRuntimeImageMaxContextFiles {
				return projectImageInputError("Project image build context exceeds 10000 files; use a smaller reviewed context.")
			}
			totalBytes += details.Size()
			if totalBytes > ProjectRuntimeImageMaxContextBytes {
				return projectImageInputError("Project image build context exceeds 268435456 bytes; use a smaller reviewed context.")
			}
			bytes, err := nodefs.ReadBytes(child)
			if err != nil {
				return projectImageInputError("FaultLine could not read build-context file ", relativePath, errorDetail(err))
			}
			file := jsjson.NewObj()
			file.Set("path", jsjson.MakeString(relativePath))
			file.Set("bytes", jsjson.MakeNumber(float64(len(bytes))))
			file.Set("mode", jsjson.MakeNumber(float64(details.Mode().Perm())))
			file.Set("digest", jsjson.MakeString("sha256:"+canonical.SHA256HexBytes(bytes)))
			files = append(files, jsjson.MakeObject(file))
		}
		return nil
	}

	if err := visit(n.contextDirectory, ""); err != nil {
		return contextFingerprint{}, err
	}
	dockerfileBytes, err := nodefs.ReadBytes(n.dockerfile)
	if err != nil {
		return contextFingerprint{}, projectImageInputError("FaultLine could not read the reviewed Dockerfile", errorDetail(err))
	}
	if files == nil {
		files = []jsjson.Value{}
	}
	body := jsjson.NewObj()
	body.Set("schema", jsjson.MakeString("faultline.project-build-context.v1"))
	body.Set("files", jsjson.MakeArray(files))
	contextDigest, err := canonical.DigestJSON(jsjson.MakeObject(body))
	if err != nil {
		return contextFingerprint{}, err
	}
	return contextFingerprint{
		contextDigest:    contextDigest,
		dockerfileDigest: "sha256:" + canonical.SHA256HexBytes(dockerfileBytes),
		fileCount:        len(files),
		totalBytes:       totalBytes,
	}, nil
}

// Port of src/runtime.ts:482 projectBuildPlanDigest.
func projectBuildPlanDigest(n normalizedBuild, f contextFingerprint) (string, error) {
	o := jsjson.NewObj()
	o.Set("schema", jsjson.MakeString("faultline.project-build-plan.v1"))
	o.Set("contextDirectory", jsjson.MakeString(n.contextDirectory))
	o.Set("dockerfile", jsjson.MakeString(n.dockerfile))
	o.Set("imageTag", jsjson.MakeString(n.imageTag))
	o.Set("network", jsjson.MakeString(n.network))
	o.Set("contextDigest", jsjson.MakeString(f.contextDigest))
	o.Set("dockerfileDigest", jsjson.MakeString(f.dockerfileDigest))
	return canonical.DigestJSON(jsjson.MakeObject(o))
}
