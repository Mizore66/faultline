// Package sandbox ports src/sandbox.ts: the Docker-isolated execution plan,
// the native runner that executes it, and the fail-closed classification of
// what the runner observed.
//
// Requests are the JSON form of SandboxPlanRequest (src/sandbox.ts:107) with
// the TypeScript types: strings where TS has strings, an object of strings
// for environment, an array of strings for allowedEnvironment. A field of
// another type is read as absent or as failing its pattern, where TS would
// sometimes throw a TypeError instead; FaultLine's callers build requests
// from schema-checked values, so no CLI path supplies one.
package sandbox

import (
	"errors"
	"math"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsre"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/nodeproc"
)

// Port of src/sandbox.ts:29 DEFAULT_SANDBOX_LIMITS, in key order.
var defaultSandboxLimits = []float64{120_000, 1_048_576, 1, 536_870_912, 128, 67_108_864}

// Port of src/sandbox.ts:56-59 and :270.
var (
	secretEnvironmentName = jsre.MustCompile(`(api[_-]?key|auth|bearer|cookie|credential|passwd|password|private[_-]?key|secret|session|token)`, "i")
	environmentName       = jsre.MustCompile(`^[A-Z_][A-Z0-9_]*$`, "")
	imageReference        = jsre.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[a-f0-9]{64}$`, "")
	witnessDigest         = jsre.MustCompile(`^sha256:[a-f0-9]{64}$`, "")
	unsafeMountCharacter  = jsre.MustCompile(`[\r\n,]`, "")
)

// Plan is a DockerSandboxPlan (src/sandbox.ts:177), its keys in the order
// createDockerSandboxPlan builds them. Like the TS object it holds the raw
// argv and environment values: persist AuditPlan, never the plan.
type Plan struct{ Value jsjson.Value }

// deterministicValue is `key in DETERMINISTIC_ENVIRONMENT` and its value, for a name that
// passed ENVIRONMENT_NAME (no Object.prototype property does).
func deterministicValue(key string) (string, bool) {
	v := gitproof.DeterministicEnvironment().Get(key)
	return v.Str(), v.Kind() == jsjson.String
}

func stringOf(v jsjson.Value) string {
	if v.Kind() != jsjson.String {
		return ""
	}
	return v.Str()
}

// Port of src/sandbox.ts:230 uniqueSorted.
func uniqueSorted(values []string) []string {
	seen := map[string]bool{}
	var unique []string
	for _, v := range values {
		if !seen[v] {
			jsexc.SetAdd(seen, v)
			unique = append(unique, v)
		}
	}
	return canonical.SortLocale(unique)
}

// Port of src/sandbox.ts:234 assertEnvironmentName.
func assertEnvironmentName(name string) error {
	if !environmentName.Test(name) {
		return errors.New(jsexc.Concat("Sandbox environment name is invalid: ", name))
	}
	return nil
}

// Port of src/sandbox.ts:244 normalizeAllowlist.
func normalizeAllowlist(allowed jsjson.Value) ([]string, error) {
	var names []string
	for _, item := range allowed.Items() {
		names = append(names, stringOf(item))
	}
	normalized := uniqueSorted(names)
	for _, name := range normalized {
		if err := assertEnvironmentName(name); err != nil {
			return nil, err
		}
		if _, fixed := deterministicValue(name); fixed {
			return nil, errors.New("Sandbox environment " + name + " is fixed by the deterministic policy and must not be allowlisted.")
		}
		if secretEnvironmentName.Test(name) {
			return nil, errors.New("Sandbox refuses secret-like environment name even when allowlisted: " + name)
		}
	}
	return normalized, nil
}

// Port of src/sandbox.ts:258 resolveSourceDirectory.
func resolveSourceDirectory(sourceDirectory string) (string, error) {
	if sourceDirectory == "" || !nodefs.IsAbsolute(sourceDirectory) {
		return "", errors.New("Sandbox sourceDirectory must be an absolute path.")
	}
	absolute := nodefs.Resolve(sourceDirectory)
	if strings.IndexByte(absolute, 0) >= 0 { // realpathSync's validatePath
		return "", nodeproc.InvalidArgValue("The argument 'path'", absolute, "must be a string, Uint8Array, or URL without null bytes")
	}
	resolved, err := nodefs.RealpathSync(absolute)
	if err != nil {
		return "", err
	}
	info, err := nodefs.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New(jsexc.Concat("Sandbox sourceDirectory is not a directory: ", sourceDirectory))
	}
	// parse(resolved).root === resolved: a resolved path is its own root
	// exactly when dirname leaves it unchanged.
	if nodefs.Dirname(resolved) == resolved {
		return "", errors.New("Sandbox refuses to mount a filesystem root, even read-only.")
	}
	if unsafeMountCharacter.Test(resolved) {
		return "", errors.New("Sandbox sourceDirectory contains a character unsafe for Docker --mount.")
	}
	return resolved, nil
}

// Port of src/sandbox.ts:276 validateImage.
func validateImage(image string) (string, error) {
	if image == "" || !imageReference.Test(image) {
		return "", errors.New("Sandbox image must be an immutable digest-pinned reference (for example registry.example/faultline@sha256:<64-hex-digest>).")
	}
	return image, nil
}

// Port of src/sandbox.ts:290 resolveLimits (with :283 strictLimit). The
// result is a limits object in DEFAULT_SANDBOX_LIMITS key order.
func resolveLimits(overrides jsjson.Value) (jsjson.Value, error) {
	limits := jsjson.NewObj()
	for i, limit := range gitproof.MaxSandboxLimits() {
		value := jsjson.MakeNumber(defaultSandboxLimits[i])
		if overrides.Kind() == jsjson.Object {
			if v, ok := overrides.Obj().Get(limit.Name); ok {
				value = v
			}
		}
		if !isInteger(value) || value.Num() <= 0 || value.Num() > limit.Maximum {
			return jsjson.Value{}, errors.New("Sandbox limit " + limit.Name + " must be a positive integer no greater than " + jsjson.FormatNumber(limit.Maximum) + ".")
		}
		limits.Set(limit.Name, value)
	}
	return jsjson.MakeObject(limits), nil
}

// isInteger is Number.isInteger.
func isInteger(v jsjson.Value) bool {
	if v.Kind() != jsjson.Number {
		return false
	}
	n := v.Num()
	return !math.IsInf(n, 0) && n == math.Trunc(n)
}

// Port of src/sandbox.ts:302 validateWitness.
func validateWitness(witness jsjson.Value) (digest, command string, err error) {
	digest, command = stringOf(witness.Get("digest")), stringOf(witness.Get("command"))
	if witness.Get("digest").Kind() != jsjson.String || !witnessDigest.Test(digest) {
		return "", "", errors.New("Sandbox requires a frozen witness with a sha256 digest.")
	}
	if jsstr.Trim(command) == "" || strings.IndexByte(command, 0) >= 0 {
		return "", "", errors.New("Sandbox witness command must be non-empty and cannot contain a NUL byte.")
	}
	return digest, command, nil
}

// builtEnvironment is BuiltEnvironment (src/sandbox.ts:312).
type builtEnvironment struct {
	values       jsjson.Value
	policy       jsjson.Value
	redactedKeys []string
}

// Port of src/sandbox.ts:318 buildEnvironment.
func buildEnvironment(supplied jsjson.Value, allowedKeys []string) (builtEnvironment, error) {
	values := gitproof.DeterministicEnvironment().Obj()
	var passed []jsjson.Value
	var redacted []string
	allowed := map[string]bool{}
	for _, k := range allowedKeys {
		allowed[k] = true
	}
	var keys []string
	if supplied.Kind() == jsjson.Object {
		keys = supplied.Obj().Keys()
	}
	// Object.entries(...).sort by key: the sort sees only the keys.
	for _, key := range canonical.SortLocale(keys) {
		if err := assertEnvironmentName(key); err != nil {
			return builtEnvironment{}, err
		}
		value := supplied.Obj().Field(key)
		if secretEnvironmentName.Test(key) {
			redacted = append(redacted, key)
			continue
		}
		if fixed, ok := deterministicValue(key); ok {
			if value.Kind() != jsjson.String || value.Str() != fixed {
				return builtEnvironment{}, errors.New("Sandbox refuses to override deterministic environment " + key + ".")
			}
			continue
		}
		if !allowed[key] {
			redacted = append(redacted, key)
			continue
		}
		values.Set(key, value)
		entry := jsjson.NewObj()
		entry.Set("key", jsjson.MakeString(key))
		entry.Set("valueDigest", jsjson.MakeString("sha256:"+canonical.SHA256Hex(stringOf(value))))
		passed = append(passed, jsjson.MakeObject(entry))
	}
	policy := jsjson.NewObj()
	policy.Set("schemaVersion", jsjson.MakeString(gitproof.EnvironmentPolicyVersion))
	policy.Set("fixed", gitproof.DeterministicEnvironment())
	policy.Set("allowedKeys", stringArray(allowedKeys))
	policy.Set("passed", jsjson.MakeArray(append([]jsjson.Value{}, passed...)))
	return builtEnvironment{
		values:       jsjson.MakeObject(values),
		policy:       jsjson.MakeObject(policy),
		redactedKeys: uniqueSorted(redacted),
	}, nil
}

func stringArray(values []string) jsjson.Value {
	items := make([]jsjson.Value, len(values))
	for i, v := range values {
		items[i] = jsjson.MakeString(v)
	}
	return jsjson.MakeArray(items)
}

// Port of src/sandbox.ts:361 environmentArguments.
func environmentArguments(environment jsjson.Value) []string {
	var args []string
	for _, key := range canonical.SortLocale(environment.Obj().Keys()) {
		args = append(args, "--env", key+"="+environment.Obj().Field(key).Str())
	}
	return args
}

// Port of src/sandbox.ts:367 createAudit, for a Docker plan's runtime.
func createAudit(digest, command string, environment builtEnvironment, environmentPolicyDigest, policyDigest, image string, limits jsjson.Value) jsjson.Value {
	env := jsjson.NewObj()
	env.Set("fixedKeys", stringArray(canonical.SortLocale(gitproof.DeterministicEnvironment().Obj().Keys())))
	env.Set("allowedKeys", environment.policy.Get("allowedKeys"))
	env.Set("passed", environment.policy.Get("passed"))
	env.Set("redactedKeys", stringArray(environment.redactedKeys))
	runtime := jsjson.NewObj()
	runtime.Set("image", jsjson.MakeString(image))
	runtime.Set("entrypoint", jsjson.MakeString("/bin/sh"))
	runtime.Set("network", jsjson.MakeString("none"))
	runtime.Set("rootFilesystemReadOnly", jsjson.MakeBool(true))
	runtime.Set("user", jsjson.MakeString("65534:65534"))
	runtime.Set("capDropAll", jsjson.MakeBool(true))
	runtime.Set("noNewPrivileges", jsjson.MakeBool(true))
	runtime.Set("pull", jsjson.MakeString("never"))
	runtime.Set("limits", limits)
	audit := jsjson.NewObj()
	audit.Set("kind", jsjson.MakeString("DOCKER_ISOLATED"))
	audit.Set("witnessDigest", jsjson.MakeString(digest))
	audit.Set("commandDigest", jsjson.MakeString("sha256:"+canonical.SHA256Hex(command)))
	audit.Set("environmentPolicyDigest", jsjson.MakeString(environmentPolicyDigest))
	audit.Set("policyDigest", jsjson.MakeString(policyDigest))
	audit.Set("environment", jsjson.MakeObject(env))
	audit.Set("runtime", jsjson.MakeObject(runtime))
	return jsjson.MakeObject(audit)
}

func digestOf(v jsjson.Value) string { return jsexc.Must(canonical.DigestJSON(v)) }

// Port of src/sandbox.ts:455 createDockerSandboxPlan.
func createDockerPlan(request jsjson.Value) (Plan, error) {
	digest, command, err := validateWitness(request.Get("witness"))
	if err != nil {
		return Plan{}, err
	}
	sourceDirectory, err := resolveSourceDirectory(stringOf(request.Get("sourceDirectory")))
	if err != nil {
		return Plan{}, err
	}
	image, err := validateImage(stringOf(request.Get("image")))
	if err != nil {
		return Plan{}, err
	}
	limits, err := resolveLimits(request.Get("limits"))
	if err != nil {
		return Plan{}, err
	}
	allowedKeys, err := normalizeAllowlist(request.Get("allowedEnvironment"))
	if err != nil {
		return Plan{}, err
	}
	environment, err := buildEnvironment(request.Get("environment"), allowedKeys)
	if err != nil {
		return Plan{}, err
	}
	environmentPolicyDigest := digestOf(environment.policy)
	// Port of src/sandbox.ts:417 dockerPolicyDigest.
	policyDigest := digestOf(gitproof.DockerPolicyPayload(image, digest, limits, jsjson.MakeString(environmentPolicyDigest),
		gitproof.SandboxPolicyVersion, gitproof.SandboxSourceTarget))
	containerName := "faultline-" + strings.ReplaceAll(clock.NewUUID(), "-", "")
	limit := func(name string) string { return jsjson.FormatNumber(limits.Get(name).Num()) }
	fsize := limit("maxOutputBytes")
	args := []string{
		"run",
		"--rm",
		"--name",
		containerName,
		"--init",
		"--pull=never",
		"--network",
		"none",
		"--ipc",
		"none",
		"--read-only",
		"--user",
		"65534:65534",
		"--cap-drop",
		"ALL",
		"--security-opt",
		"no-new-privileges:true",
		"--ulimit",
		"fsize=" + fsize + ":" + fsize,
		"--pids-limit",
		limit("pidsLimit"),
		"--memory",
		limit("memoryBytes"),
		"--memory-swap",
		limit("memoryBytes"),
		"--cpus",
		limit("cpuCount"),
		"--tmpfs",
		"/tmp:rw,noexec,nosuid,nodev,size=" + limit("tmpfsBytes"),
		"--mount",
		"type=bind,src=" + sourceDirectory + ",dst=" + gitproof.SandboxSourceTarget + ",readonly",
		"--workdir",
		gitproof.SandboxSourceTarget,
	}
	args = append(args, environmentArguments(environment.values)...)
	args = append(args, "--entrypoint", "/bin/sh", image, "-lc", command)
	plan := jsjson.NewObj()
	plan.Set("kind", jsjson.MakeString("DOCKER_ISOLATED"))
	plan.Set("executable", jsjson.MakeString("docker"))
	plan.Set("arguments", stringArray(args))
	plan.Set("sourceDirectory", jsjson.MakeString(sourceDirectory))
	plan.Set("image", jsjson.MakeString(image))
	plan.Set("containerName", jsjson.MakeString(containerName))
	plan.Set("limits", limits)
	plan.Set("environment", environment.values)
	plan.Set("environmentPolicy", environment.policy)
	plan.Set("environmentPolicyDigest", jsjson.MakeString(environmentPolicyDigest))
	plan.Set("policyDigest", jsjson.MakeString(policyDigest))
	plan.Set("audit", createAudit(digest, command, environment, environmentPolicyDigest, policyDigest, image, limits))
	return Plan{jsjson.MakeObject(plan)}, nil
}

// CreatePlan ports createSandboxPlan (src/sandbox.ts:588). Its error is the
// TS Error's message (fs errors in Node's format). UNSAFE_LOCAL plans are
// refused here exactly as in TS; createUnsafeLocalSandboxPlan, reachable
// only through createDevUnsafeLocalSandboxPlan, is not ported.
func CreatePlan(request jsjson.Value) (Plan, error) {
	if m := request.Get("mode"); m.Kind() == jsjson.String && m.Str() == "UNSAFE_LOCAL" {
		return Plan{}, errors.New("UNSAFE_LOCAL execution is not available in the shipped FaultLine runtime.")
	}
	if a := request.Get("allowUnsafeLocal"); a.Kind() == jsjson.Bool && a.Bool() {
		return Plan{}, errors.New("allowUnsafeLocal is only valid together with mode: UNSAFE_LOCAL.")
	}
	return createDockerPlan(request)
}

// AuditPlan ports auditSandboxPlan (src/sandbox.ts:610): the serializable,
// value-redacted form, safe for proof bundles.
func AuditPlan(p Plan) jsjson.Value { return p.Value.Get("audit") }
