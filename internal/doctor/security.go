package doctor

import (
	"encoding/base64"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jscase"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsre"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/nodeproc"
	"github.com/Mizore66/faultline/internal/safegit"
	"github.com/Mizore66/faultline/internal/turnsnap"
)

// SecuritySchemaVersion ports src/doctor.ts:546 DOCTOR_SECURITY_SCHEMA_VERSION.
const SecuritySchemaVersion = "faultline.doctor-security.v1"

// The refusal patterns src/doctor.ts:705 and :733 test the caught messages
// against.
var (
	overlayRefusal = jsre.MustCompile(`unsafe|escap`, "i")
	secretRefusal  = jsre.MustCompile(`secret material|high-entropy`, "i")
)

// securityResult is the { status, stdout, stderr } securityGit returns;
// status nil is JS null.
type securityResult struct {
	status         *int
	stdout, stderr string
}

// securityGit ports src/doctor.ts:579 securityGit: spawnSync("git", argv,
// { encoding: "utf8", shell: false, windowsHide: true, env }) with the
// environment stripped to PATH, GIT_CONFIG_NOSYSTEM, GIT_CONFIG_GLOBAL and
// GIT_TERMINAL_PROMPT (plus a non-empty SystemRoot and ComSpec on
// Windows), no timeout and the default maxBuffer. No call site passes
// options.env.
func securityGit(repository string, args []string, hardened bool) securityResult {
	var argv []string
	if hardened {
		argv = safegit.Doctor()
	}
	argv = append(argv, "-C", repository)
	argv = append(argv, args...)
	path, _ := nodeproc.Getenv("PATH")
	global := "/dev/null"
	if runtime.GOOS == "windows" {
		global = "NUL"
	}
	env := []string{"PATH=" + path, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + global, "GIT_TERMINAL_PROMPT=0"}
	if runtime.GOOS == "windows" {
		for _, name := range []string{"SystemRoot", "ComSpec"} {
			if v, _ := nodeproc.Getenv(name); v != "" {
				env = append(env, name+"="+v)
			}
		}
	} else {
		// Node encodes the env object's JS strings as UTF-8.
		for i := range env {
			env[i] = jsstr.ToUTF8(env[i])
		}
	}
	r := nodeproc.SpawnSyncWith("git", argv, nodeproc.SpawnSyncOptions{Env: env})
	return securityResult{status: r.Status, stdout: jsstr.DecodeUTF8(r.Stdout), stderr: jsstr.DecodeUTF8(r.Stderr)}
}

// statusText is `${status ?? "null"}`.
func statusText(status *int) string {
	if status == nil {
		return "null"
	}
	return strconv.Itoa(*status)
}

// slice200 is s.slice(0, 200), in UTF-16 units (it can cut a surrogate
// pair, as slice does).
func slice200(s string) string {
	if units := jsstr.ToUTF16(s); len(units) > 200 {
		return jsstr.FromUTF16(units[:200])
	}
	return s
}

// orEmpty is `value || "<empty>"`.
func orEmpty(value string) string {
	if value == "" {
		return "<empty>"
	}
	return value
}

func securityCheck(id string, pass bool, passSummary, failSummary string, observation jsjson.Value) jsjson.Value {
	status, summary := "FAIL", failSummary
	if pass {
		status, summary = "PASS", passSummary
	}
	o := jsjson.NewObj()
	o.Set("id", jsjson.MakeString(id))
	o.Set("status", jsjson.MakeString(status))
	o.Set("summary", jsjson.MakeString(summary))
	o.Set("observation", observation)
	return jsjson.MakeObject(o)
}

// stringOrNull is `observation || null`.
func stringOrNull(s string) jsjson.Value {
	if s == "" {
		return jsjson.MakeNull()
	}
	return jsjson.MakeString(s)
}

// plantedSecretLine is the fake credential line the secret check writes. It
// is assembled at run time from pieces so that no source or committed file
// holds the credential-shaped string.
func plantedSecretLine() string {
	var b strings.Builder
	for c := 'a'; c <= 'z'; c++ {
		b.WriteRune(c)
	}
	for d := 0; d <= 5; d++ {
		b.WriteString(strconv.Itoa(d))
	}
	return "token = " + strings.Join([]string{"sk", "proj", b.String()}, "-") + "\n"
}

// writeText is writeFileSync(path, text, "utf8"): flag "w", mode 0o666.
func writeText(path, text string) {
	jsexc.Must(struct{}{}, nodefs.WriteFileUTF8(path, text, 0o666, false))
}

// RunSecurity ports src/doctor.ts:617 runFaultLineSecurityDoctor: a live
// self-test that spawns Git against a throwaway fixture holding a malicious
// hook and hostile filter config, and checks that FaultLine's hardened
// overrides neutralize them; it also probes the overlay path traversal
// refusal and the turn-snapshot secret refusal. A failure to create or
// fill the fixture is returned as the error the CLI prints.
func RunSecurity() (report jsjson.Value, err error) {
	fixtureRepository, err := nodefs.Mkdtemp(nodefs.Join(nodefs.Tmpdir(), "faultline-doctor-security-"))
	if err != nil {
		return jsjson.Value{}, err
	}
	var checks []jsjson.Value
	err = jsexc.Try(func() { checks = runSecurityChecks(fixtureRepository) })
	// finally: rmSync(fixtureRepository, { recursive: true, force: true });
	// an error from it replaces the try block's.
	if rmErr := nodefs.RemoveAll(fixtureRepository); rmErr != nil {
		return jsjson.Value{}, rmErr
	}
	if err != nil {
		return jsjson.Value{}, err
	}

	status := "SECURE"
	for _, check := range checks {
		if check.Get("status").Str() != "PASS" {
			status = "INSECURE"
		}
	}
	next := "fl doctor --security"
	if status == "SECURE" {
		next = "fl doctor --proof-ready"
	}
	o := jsjson.NewObj()
	o.Set("schemaVersion", jsjson.MakeString(SecuritySchemaVersion))
	o.Set("fixtureRepository", jsjson.MakeString(fixtureRepository))
	o.Set("checks", jsjson.MakeArray(checks))
	o.Set("status", jsjson.MakeString(status))
	o.Set("next", jsjson.MakeString(next))
	return jsjson.MakeObject(o), nil
}

// runSecurityChecks is the try block of src/doctor.ts:617
// runFaultLineSecurityDoctor; fixture write failures are thrown.
func runSecurityChecks(fixtureRepository string) []jsjson.Value {
	hooksDir := nodefs.Join(fixtureRepository, "malicious-hooks")
	const markerRelative = "HOOK_EXECUTED.marker"
	var checks []jsjson.Value

	jsexc.Must(struct{}{}, nodefs.MkdirAll(hooksDir, 0o700))
	hookPath := nodefs.Join(hooksDir, "pre-commit")
	// Relative marker so the same POSIX hook body works under Git for Windows' sh.
	writeText(hookPath, "#!/bin/sh\nprintf 'hooked\\n' > "+markerRelative+"\n")
	if runtime.GOOS != "windows" {
		if err := os.Chmod(jsstr.ToUTF8(hookPath), 0o755); err != nil {
			jsexc.Throw(&nodefs.Error{Code: nodefs.ErrnoCode(err), Syscall: "chmod", Path: jsstr.ToUTF8(hookPath)})
		}
	}

	securityGit(fixtureRepository, []string{"init"}, false)
	securityGit(fixtureRepository, []string{"config", "user.email", "faultline-security@example.invalid"}, false)
	securityGit(fixtureRepository, []string{"config", "user.name", "FaultLine Security Doctor"}, false)
	securityGit(fixtureRepository, []string{"config", "core.hooksPath", hooksDir}, false)
	securityGit(fixtureRepository, []string{"config", "filter.lfs.process", "malicious-lfs-filter"}, false)
	securityGit(fixtureRepository, []string{"config", "filter.lfs.smudge", "malicious-lfs-smudge"}, false)
	securityGit(fixtureRepository, []string{"config", "filter.lfs.required", "true"}, false)
	writeText(nodefs.Join(fixtureRepository, "tracked.txt"), "security-doctor\n")
	securityGit(fixtureRepository, []string{"add", "tracked.txt"}, false)

	// Hardened commit must not execute the fixture's malicious pre-commit hook.
	hardenedCommit := securityGit(fixtureRepository, []string{"commit", "-m", "security-doctor"}, true)
	hookExecuted := nodefs.Exists(nodefs.Join(fixtureRepository, markerRelative))
	hookObservation := "commit exit=" + statusText(hardenedCommit.status) + "; marker absent"
	if hookExecuted {
		hookObservation = "Marker written at " + markerRelative
	}
	hooks := securityCheck("hooks-neutralization", !hookExecuted,
		"Hardened Git overrides prevented the malicious pre-commit hook from executing.",
		"Malicious pre-commit hook executed despite hardened overrides.",
		jsjson.MakeString(hookObservation))
	// The summary follows the marker alone; PASS also needs the commit to
	// have succeeded.
	if hardenedCommit.status == nil || *hardenedCommit.status != 0 {
		hooks.Obj().Set("status", jsjson.MakeString("FAIL"))
	}
	checks = append(checks, hooks)

	protocolProbe := securityGit(fixtureRepository, []string{"ls-remote", "https://example.invalid/faultline-security-probe.git"}, true)
	protocolBlocked := protocolProbe.status == nil || *protocolProbe.status != 0
	checks = append(checks, securityCheck("protocol-allow-never", protocolBlocked,
		"protocol.allow=never blocked a remote HTTPS Git probe.",
		"Remote HTTPS Git probe unexpectedly succeeded under hardened overrides.",
		jsjson.MakeString(jsexc.Concat("exit=", statusText(protocolProbe.status), "; stderr=", slice200(jsstr.Trim(protocolProbe.stderr))))))

	filterProcess := securityGit(fixtureRepository, []string{"config", "--get", "filter.lfs.process"}, true)
	filterSmudge := securityGit(fixtureRepository, []string{"config", "--get", "filter.lfs.smudge"}, true)
	// With -c overrides, `git config --get` still reads the stored repo value.
	// Prove neutralization by running a status under hardened config that must
	// not invoke the configured LFS process (empty override + required=false).
	statusHardened := securityGit(fixtureRepository, []string{"status", "--porcelain=v1", "-z"}, true)
	filtersNeutral := statusHardened.status != nil && *statusHardened.status == 0 &&
		!strings.Contains(jscase.Lower(statusHardened.stderr), "malicious-lfs")
	checks = append(checks, securityCheck("path-filters", filtersNeutral,
		"Hardened overrides neutralize repository filter.lfs process/smudge settings during Git inspection.",
		"Hardened Git inspection still appears to honor hostile filter.lfs settings.",
		jsjson.MakeString(jsexc.Join([]string{
			jsexc.Concat("stored process=", orEmpty(jsstr.Trim(filterProcess.stdout))),
			jsexc.Concat("stored smudge=", orEmpty(jsstr.Trim(filterSmudge.stdout))),
			"status exit=" + statusText(statusHardened.status),
		}, "; "))))

	// Overlay path traversal: materializeFrozenOverlays must refuse ../escape
	// even if a hostile FrozenWitness bypasses proposal-schema checks.
	overlayBytes := []byte("export const hostile = true;\n")
	overlay := jsjson.NewObj()
	overlay.Set("path", jsjson.MakeString("../escape.mjs"))
	overlay.Set("bytesBase64", jsjson.MakeString(base64.StdEncoding.EncodeToString(overlayBytes)))
	overlay.Set("bytesDigest", jsjson.MakeString("sha256:"+canonical.SHA256HexBytes(overlayBytes)))
	witness := jsjson.NewObj()
	witness.Set("overlays", jsjson.MakeArray([]jsjson.Value{jsjson.MakeObject(overlay)}))
	proposal := jsjson.NewObj()
	proposal.Set("witness", jsjson.MakeObject(witness))
	hostileOverlayWitness := jsjson.NewObj()
	hostileOverlayWitness.Set("proposal", jsjson.MakeObject(proposal))
	overlayRefused := false
	overlayObservation := ""
	if err := caught(func() error {
		_, err := gitinv.MaterializeFrozenOverlays(fixtureRepository, jsjson.MakeObject(hostileOverlayWitness), nil)
		return err
	}); err == nil {
		overlayObservation = "materializeFrozenOverlays unexpectedly accepted ../escape.mjs"
	} else {
		message := jsexc.Message(err)
		overlayRefused = overlayRefusal.Test(message)
		overlayObservation = slice200(message)
	}
	checks = append(checks, securityCheck("overlay-path-traversal", overlayRefused,
		"materializeFrozenOverlays refused an overlay path that attempts ../escape.",
		"Overlay path traversal was not refused.",
		stringOrNull(overlayObservation)))

	// Secret-shaped blob: turn-snapshot planning/capture must reject before write.
	writeText(nodefs.Join(fixtureRepository, "leaked-secret.txt"), plantedSecretLine())
	secretRefused := false
	secretObservation := ""
	if err := caught(func() error {
		_, _, err := turnsnap.Capture(fixtureRepository, turnsnap.CaptureOptions{Sleep: func(time.Duration) {}})
		return err
	}); err == nil {
		secretObservation = "captureTurnTreeSnapshot unexpectedly accepted a secret-shaped blob"
	} else {
		message := jsexc.Message(err)
		secretRefused = secretRefusal.Test(message)
		secretObservation = slice200(message)
	}
	checks = append(checks, securityCheck("secret-shaped-blob", secretRefused,
		"Turn-tree snapshot refused a secret-shaped blob before acceptance.",
		"Secret-shaped blob was not refused by turn-tree snapshot capture.",
		stringOrNull(secretObservation)))
	return checks
}

// caught runs fn inside a JS try: a returned error and a thrown exception
// both reach the catch.
func caught(fn func() error) error {
	var err error
	if thrown := jsexc.Try(func() { err = fn() }); thrown != nil {
		return thrown
	}
	return err
}

// SecurityExitCode ports src/doctor.ts:757 doctorSecurityExitCode.
func SecurityExitCode(r jsjson.Value) int {
	if r.Get("status").Str() == "SECURE" {
		return 0
	}
	return 1
}
