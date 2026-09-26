package gitproof

import (
	"math"
	"regexp"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// Port of src/sandbox.ts:14-55.
const (
	sandboxPolicyVersion       = "faultline.sandbox.v2"
	legacySandboxPolicyVersion = "faultline.sandbox.v1"
	sandboxSourceTarget        = "/workspace/src"
	legacySandboxSourceTarget  = "/workspace"
	environmentPolicyVersion   = "faultline.sandbox-environment.v1"
)

type namedLimit struct {
	name    string
	maximum float64
}

// maxSandboxLimits keeps Object.entries(MAX_SANDBOX_LIMITS) order.
var maxSandboxLimits = []namedLimit{
	{"timeoutMs", 300_000},
	{"maxOutputBytes", 8_388_608},
	{"cpuCount", 4},
	{"memoryBytes", 2_147_483_648},
	{"pidsLimit", 512},
	{"tmpfsBytes", 536_870_912},
}

// deterministicEnvironment is DETERMINISTIC_ENVIRONMENT in literal order.
func deterministicEnvironment() jsjson.Value {
	o := jsjson.NewObj()
	for _, kv := range [][2]string{{"CI", "1"}, {"HOME", "/tmp"}, {"LANG", "C.UTF-8"}, {"LC_ALL", "C.UTF-8"}, {"SOURCE_DATE_EPOCH", "0"}, {"TZ", "UTC"}} {
		o.Set(kv[0], jsjson.MakeString(kv[1]))
	}
	return jsjson.MakeObject(o)
}

var (
	environmentName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	imageReference  = regexp.MustCompile(digestPinnedImageRe)
	sandboxDigest   = regexp.MustCompile(sha256DigestSource)
)

// testString is RegExp.prototype.test, which coerces non-strings; for these
// patterns no coerced non-string can match.
func testString(re *regexp.Regexp, v jsjson.Value) bool {
	return v.Kind() == jsjson.String && re.MatchString(v.Str())
}

// dockerPolicyPayload ports src/sandbox.ts:387.
func dockerPolicyPayload(image, witnessDigest string, limits jsjson.Value, environmentPolicyDigest jsjson.Value, schemaVersion, sourceTarget string) jsjson.Value {
	source := jsjson.NewObj()
	source.Set("target", jsjson.MakeString(sourceTarget))
	source.Set("readOnly", jsjson.MakeBool(true))
	o := jsjson.NewObj()
	o.Set("schemaVersion", jsjson.MakeString(schemaVersion))
	o.Set("kind", jsjson.MakeString("DOCKER_ISOLATED"))
	o.Set("image", jsjson.MakeString(image))
	o.Set("witnessDigest", jsjson.MakeString(witnessDigest))
	o.Set("source", jsjson.MakeObject(source))
	o.Set("network", jsjson.MakeString("none"))
	o.Set("rootFilesystem", jsjson.MakeString("read-only"))
	o.Set("user", jsjson.MakeString("65534:65534"))
	o.Set("capDrop", jsjson.MakeString("ALL"))
	o.Set("noNewPrivileges", jsjson.MakeBool(true))
	o.Set("pull", jsjson.MakeString("never"))
	o.Set("entrypoint", jsjson.MakeString("/bin/sh"))
	o.Set("limits", limits)
	o.Set("environmentPolicyDigest", environmentPolicyDigest)
	return jsjson.MakeObject(o)
}

// unsafeLocalPolicyPayload ports src/sandbox.ts:426.
func unsafeLocalPolicyPayload(witnessDigest string, limits jsjson.Value, environmentPolicyDigest jsjson.Value, schemaVersion string) jsjson.Value {
	o := jsjson.NewObj()
	o.Set("schemaVersion", jsjson.MakeString(schemaVersion))
	o.Set("kind", jsjson.MakeString("UNSAFE_LOCAL"))
	o.Set("witnessDigest", jsjson.MakeString(witnessDigest))
	o.Set("warning", jsjson.MakeString("No container isolation. Never use as proof."))
	o.Set("limits", limits)
	o.Set("environmentPolicyDigest", environmentPolicyDigest)
	return jsjson.MakeObject(o)
}

func digestOf(v jsjson.Value) string { return bundle.Must(canonical.DigestJSON(v)) }

// isInteger is Number.isInteger.
func isInteger(v jsjson.Value) bool {
	return v.Kind() == jsjson.Number && !math.IsInf(v.Num(), 0) && v.Num() == math.Trunc(v.Num())
}

// ValidateSandboxPlanAudit ports validateSandboxPlanAudit (src/sandbox.ts:615).
func ValidateSandboxPlanAudit(audit jsjson.Value) []string {
	errs := []string{}
	add := func(s string) { errs = append(errs, s) }
	if !testString(sandboxDigest, audit.Get("witnessDigest")) {
		add("witness digest is invalid")
	}
	if !testString(sandboxDigest, audit.Get("commandDigest")) {
		add("command digest is invalid")
	}
	if !testString(sandboxDigest, audit.Get("environmentPolicyDigest")) {
		add("environment policy digest is invalid")
	}
	if !testString(sandboxDigest, audit.Get("policyDigest")) {
		add("sandbox policy digest is invalid")
	}
	env := audit.Get("environment")
	expectedFixed := canonical.SortLocale([]string{"CI", "HOME", "LANG", "LC_ALL", "SOURCE_DATE_EPOCH", "TZ"})
	fixed := canonical.SortLocale(bundle.Strings(env.Get("fixedKeys")))
	if jsjson.Stringify(stringList(fixed)) != jsjson.Stringify(stringList(expectedFixed)) {
		add("fixed environment keys do not match the deterministic policy")
	}
	allowedKeys := bundle.Strings(env.Get("allowedKeys"))
	redactedKeys := bundle.Strings(env.Get("redactedKeys"))
	passed := env.Get("passed").Items()
	allowed := map[string]bool{}
	for _, k := range allowedKeys {
		allowed[k] = true
	}
	passedNames := make([]string, len(passed))
	for i, entry := range passed {
		passedNames[i] = entry.Get("key").Str()
	}
	if len(allowed) != len(allowedKeys) || bundle.DistinctCount(passedNames) != len(passed) || bundle.DistinctCount(redactedKeys) != len(redactedKeys) {
		add("sandbox environment audit contains duplicate names")
	}
	for _, entry := range passed {
		if !testString(environmentName, entry.Get("key")) || !testString(sandboxDigest, entry.Get("valueDigest")) {
			add("sandbox environment audit contains an invalid passed value")
			break
		}
		if !allowed[entry.Get("key").Str()] {
			add("sandbox environment audit passes a value outside its allowlist")
			break
		}
	}
	for _, key := range append(append([]string{}, allowedKeys...), redactedKeys...) {
		if !environmentName.MatchString(key) {
			add("sandbox environment audit contains an invalid environment name")
			break
		}
	}
	policy := jsjson.NewObj()
	policy.Set("schemaVersion", jsjson.MakeString(environmentPolicyVersion))
	policy.Set("fixed", deterministicEnvironment())
	policy.Set("allowedKeys", env.Get("allowedKeys"))
	policy.Set("passed", env.Get("passed"))
	environmentPolicyDigest := audit.Get("environmentPolicyDigest")
	if environmentPolicyDigest.Str() != digestOf(jsjson.MakeObject(policy)) {
		add("environment policy digest does not match the serializable audit facts")
	}
	runtime := audit.Get("runtime")
	limits := runtime.Get("limits")
	for _, limit := range maxSandboxLimits {
		value := limits.Get(limit.name)
		if !isInteger(value) || value.Num() <= 0 || value.Num() > limit.maximum {
			add("sandbox limit " + limit.name + " is outside the allowed policy range")
		}
	}
	witnessDigest := audit.Get("witnessDigest").Str()
	policyDigest := audit.Get("policyDigest").Str()
	if audit.Get("kind").Str() == "DOCKER_ISOLATED" {
		image := runtime.Get("image")
		if image.Kind() != jsjson.String || image.Str() == "" || !imageReference.MatchString(image.Str()) {
			add("Docker image is not digest-pinned")
		}
		if runtime.Get("entrypoint").Str() != "/bin/sh" || runtime.Get("entrypoint").Kind() != jsjson.String ||
			runtime.Get("network").Kind() != jsjson.String || runtime.Get("network").Str() != "none" ||
			!runtime.Get("rootFilesystemReadOnly").Bool() ||
			runtime.Get("user").Kind() != jsjson.String || runtime.Get("user").Str() != "65534:65534" ||
			!runtime.Get("capDropAll").Bool() || !runtime.Get("noNewPrivileges").Bool() ||
			runtime.Get("pull").Kind() != jsjson.String || runtime.Get("pull").Str() != "never" {
			add("Docker runtime policy is not locked down")
		}
		imageText := image.Str() // runtime.image ?? "" (null gives "")
		current := dockerPolicyPayload(imageText, witnessDigest, limits, environmentPolicyDigest, sandboxPolicyVersion, sandboxSourceTarget)
		legacy := dockerPolicyPayload(imageText, witnessDigest, limits, environmentPolicyDigest, legacySandboxPolicyVersion, legacySandboxSourceTarget)
		if policyDigest != digestOf(current) && policyDigest != digestOf(legacy) {
			add("Docker policy digest does not match the serializable audit facts")
		}
	} else if runtime.Get("image").Kind() != jsjson.Null || runtime.Get("entrypoint").Kind() != jsjson.Null || runtime.Get("network").Kind() != jsjson.Null ||
		runtime.Get("rootFilesystemReadOnly").Bool() || runtime.Get("user").Kind() != jsjson.Null || runtime.Get("capDropAll").Bool() ||
		runtime.Get("noNewPrivileges").Bool() || runtime.Get("pull").Kind() != jsjson.Null {
		add("unsafe-local runtime audit contradicts its declared mode")
	} else {
		current := unsafeLocalPolicyPayload(witnessDigest, limits, environmentPolicyDigest, sandboxPolicyVersion)
		legacy := unsafeLocalPolicyPayload(witnessDigest, limits, environmentPolicyDigest, legacySandboxPolicyVersion)
		if policyDigest != digestOf(current) && policyDigest != digestOf(legacy) {
			add("unsafe-local policy digest does not match the serializable audit facts")
		}
	}
	return errs
}
