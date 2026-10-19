package sandbox

import (
	"strconv"
	"sync"
	"time"

	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsre"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodeproc"
	"github.com/Mizore66/faultline/internal/witnessresult"
)

// Port of src/sandbox.ts:83, :91 and :789.
var (
	dockerInfrastructureError     = jsre.MustCompile(`(?:cannot connect to the docker daemon|is the docker daemon running|error during connect|docker daemon is not running|error response from daemon|unable to find image|pull access denied|no such image)`, "i")
	compileOrSetupIncompatibility = jsre.MustCompile("(?:SyntaxError|TS\\d{4}\\s*:|error\\s+TS\\d{4}|failed to compile|error\\[E\\d{4}\\]|could not compile `|cargo:.*error|rustc.*error:|ModuleNotFoundError|ImportError:\\s*No module named)", "i")
	witnessSetupError             = jsre.MustCompile(`(?:command not found|not found|no such file|cannot find module|permission denied)`, "i")
)

// RunResult is SandboxCommandResult (src/sandbox.ts:203). Stdout and Stderr
// are JS strings (WTF-8); the native runner has already bounded and lossily
// decoded them. A nil ExitCode or Signal is JS null.
type RunResult struct {
	ExitCode                      *int
	Signal                        *string
	Stdout, Stderr                string
	TimedOut, OutputLimitExceeded bool
}

// Runner is SandboxCommandRunner.run (src/sandbox.ts:212) for a Docker
// plan. An error is a rejected promise: Execute reports its message as a
// RUNNER_FAILURE.
type Runner func(p Plan) (RunResult, error)

// Port of src/sandbox.ts:715 outputSize: the UTF-8 byte length of both.
func outputSize(stdout, stderr string) int {
	return len(jsstr.ToUTF8(stdout)) + len(jsstr.ToUTF8(stderr))
}

// TruncateOutput ports truncateOutput (src/sandbox.ts:719) on the UTF-8
// bytes of the two strings: byte prefixes from one budget, stdout first,
// each decoded with toString("utf8") (a cut code point becomes U+FFFD). The
// flag reports whether anything was dropped.
func TruncateOutput(stdout, stderr []byte, max int) (string, string, bool) {
	remaining := max
	take := func(b []byte) string {
		if remaining <= 0 {
			return ""
		}
		part := b[:min(len(b), remaining)]
		remaining -= len(part)
		return jsstr.DecodeUTF8(part)
	}
	out := take(stdout)
	errOut := take(stderr)
	return out, errOut, len(stdout)+len(stderr) > max
}

func intOrNull(p *int) jsjson.Value {
	if p == nil {
		return jsjson.MakeNull()
	}
	return jsjson.MakeNumber(float64(*p))
}

func stringOrNull(p *string) jsjson.Value {
	if p == nil {
		return jsjson.MakeNull()
	}
	return jsjson.MakeString(*p)
}

// Classify ports classifySandboxResult (src/sandbox.ts:735) with its
// default executor: NATIVE_DOCKER for a Docker plan, UNSAFE_LOCAL otherwise.
func Classify(p Plan, r RunResult) jsjson.Value {
	executor := "UNSAFE_LOCAL"
	if p.Value.Get("kind").Str() == "DOCKER_ISOLATED" {
		executor = "NATIVE_DOCKER"
	}
	return classify(p, r, executor)
}

// Port of src/sandbox.ts:735 classifySandboxResult. The result keeps the
// TS key order: the base fields, then verdict and reason.
func classify(p Plan, r RunResult, executor string) jsjson.Value {
	maxOutputBytes := int(p.Value.Get("limits", "maxOutputBytes").Num())
	exceeds := r.OutputLimitExceeded || outputSize(r.Stdout, r.Stderr) > maxOutputBytes
	stdout, stderr := r.Stdout, r.Stderr
	if exceeds {
		stdout, stderr, _ = TruncateOutput([]byte(jsstr.ToUTF8(r.Stdout)), []byte(jsstr.ToUTF8(r.Stderr)), maxOutputBytes)
	}
	kind := p.Value.Get("kind").Str()
	result := func(verdict, reason string) jsjson.Value {
		o := jsjson.NewObj()
		o.Set("kind", jsjson.MakeString(kind))
		o.Set("executor", jsjson.MakeString(executor))
		o.Set("exitCode", intOrNull(r.ExitCode))
		o.Set("signal", stringOrNull(r.Signal))
		o.Set("stdout", jsjson.MakeString(stdout))
		o.Set("stderr", jsjson.MakeString(stderr))
		o.Set("outputTruncated", jsjson.MakeBool(exceeds))
		o.Set("policyDigest", p.Value.Get("policyDigest"))
		o.Set("environmentPolicyDigest", p.Value.Get("environmentPolicyDigest"))
		o.Set("verdict", jsjson.MakeString(verdict))
		o.Set("reason", jsjson.MakeString(reason))
		return jsjson.MakeObject(o)
	}

	if kind == "UNSAFE_LOCAL" {
		return result("INAPPLICABLE", "UNSAFE_LOCAL_NOT_PROOF")
	}
	if r.TimedOut {
		return result("ERROR", "TIMEOUT")
	}
	if exceeds {
		return result("ERROR", "OUTPUT_LIMIT_EXCEEDED")
	}
	if r.ExitCode == nil {
		return result("ERROR", "RUNNER_FAILURE")
	}
	exitCode := *r.ExitCode
	// Docker reserves 125 for CLI/daemon failures before the container
	// command runs; its daemon/image failures can also arrive as exit 1.
	if exitCode == 125 || kind == "DOCKER_ISOLATED" && dockerInfrastructureError.Test(r.Stderr) {
		return result("ERROR", "SANDBOX_UNAVAILABLE")
	}
	if exitCode == 126 || exitCode == 127 {
		return result("ERROR", "WITNESS_SETUP_ERROR")
	}
	resolved := witnessresult.Resolve(r.Stdout)
	if !resolved.Ok {
		return result("ERROR", "HARNESS_ERROR")
	}
	if exitCode != 0 && resolved.Result == nil && witnessSetupError.Test(r.Stderr) {
		return result("ERROR", "WITNESS_SETUP_ERROR")
	}
	// Proof-bearing PASS/FAIL require exit-code consistency, so repository
	// stdout cannot forge PREDICATE_PASS after a failing run.
	if w := resolved.Result; w != nil {
		if !witnessresult.ConsistentWithExitCode(*w, exitCode) {
			return result("ERROR", "HARNESS_ERROR")
		}
		c := witnessresult.Classify(w.Outcome)
		return result(c.Verdict, c.Reason)
	}
	if compileOrSetupIncompatibility.Test(r.Stderr) || compileOrSetupIncompatibility.Test(r.Stdout) {
		return result("INAPPLICABLE", "INCOMPATIBLE_STATE")
	}
	if exitCode == 0 {
		return result("ERROR", "EXIT_ZERO_UNSTRUCTURED")
	}
	return result("ERROR", "EXIT_NONZERO_UNSTRUCTURED")
}

// Execute ports executeSandboxPlan (src/sandbox.ts:825) for a Docker plan:
// injected marks a caller-supplied runner (INJECTED_RUNNER) rather than
// NativeRunner (NATIVE_DOCKER). A runner error becomes a RUNNER_FAILURE
// whose stderr is the message, in the catch branch's key order.
func Execute(p Plan, r Runner, injected bool) jsjson.Value {
	executor := "NATIVE_DOCKER"
	if injected {
		executor = "INJECTED_RUNNER"
	}
	got, err := r(p)
	if err == nil {
		return classify(p, got, executor)
	}
	o := jsjson.NewObj()
	o.Set("kind", p.Value.Get("kind"))
	o.Set("executor", jsjson.MakeString(executor))
	o.Set("verdict", jsjson.MakeString("ERROR"))
	o.Set("reason", jsjson.MakeString("RUNNER_FAILURE"))
	o.Set("exitCode", jsjson.MakeNull())
	o.Set("signal", jsjson.MakeNull())
	o.Set("stdout", jsjson.MakeString(""))
	o.Set("stderr", jsjson.MakeString(err.Error()))
	o.Set("outputTruncated", jsjson.MakeBool(false))
	o.Set("policyDigest", p.Value.Get("policyDigest"))
	o.Set("environmentPolicyDigest", p.Value.Get("environmentPolicyDigest"))
	return jsjson.MakeObject(o)
}

// cleanupTimeout is the force-remove timer (src/sandbox.ts:912).
const cleanupTimeout = 5 * time.Second

// NativeRunner ports createNodeSandboxRunner (src/sandbox.ts:870) for a
// Docker plan. It runs `docker <arguments>` with argv (never a shell) in
// the source directory with fl's whole environment (the Docker client
// needs it; only the --env flags reach the container), stdio [ignore,
// pipe, pipe]. Every observed byte counts; up to maxOutputBytes of the two
// streams combined are captured, and the client is killed with SIGKILL as
// soon as more than that was observed. The timeout (timeoutMs) sets
// timedOut and kills it the same way. After a timeout, an overflow or a
// null exit code it force-removes the named container (docker rm --force)
// and appends any problem to stderr after a newline. It never fails.
func NativeRunner() Runner {
	return func(p Plan) (RunResult, error) {
		return runNative(p.Value.Get("arguments"), p.Value.Get("sourceDirectory").Str(),
			int(p.Value.Get("limits", "timeoutMs").Num()), int(p.Value.Get("limits", "maxOutputBytes").Num()),
			p.Value.Get("containerName").Str()), nil
	}
}

// Port of the run method of src/sandbox.ts:870 createNodeSandboxRunner.
func runNative(argv jsjson.Value, cwd string, timeoutMs, maxOutputBytes int, containerName string) RunResult {
	args := make([]string, 0, len(argv.Items()))
	for _, a := range argv.Items() {
		args = append(args, a.Str())
	}
	var (
		mu                 sync.Mutex
		stdout, stderr     []byte
		observed           int
		timedOut, overflow bool
		settled            bool
		child              *nodeproc.Child
		timer              *time.Timer
	)
	collect := func(chunk []byte, toStdout bool) {
		mu.Lock()
		defer mu.Unlock()
		observed += len(chunk)
		if remaining := max(0, maxOutputBytes-(len(stdout)+len(stderr))); remaining > 0 {
			captured := chunk[:min(len(chunk), remaining)]
			if toStdout {
				stdout = append(stdout, captured...)
			} else {
				stderr = append(stderr, captured...)
			}
		}
		if observed > maxOutputBytes && !overflow {
			overflow = true
			child.Kill("SIGKILL")
		}
	}
	finish := func(exitCode *int, signal *string, extraStderr string) RunResult {
		mu.Lock()
		settled = true
		if timer != nil {
			timer.Stop()
		}
		if extraStderr != "" {
			stderr = append(stderr, jsstr.ToUTF8(extraStderr)...)
		}
		forceRemove := timedOut || overflow || exitCode == nil
		mu.Unlock()
		if forceRemove {
			if detail, ok := forceRemoveContainer(containerName); ok {
				stderr = append(stderr, jsstr.ToUTF8("\n"+detail)...)
			}
		}
		mu.Lock()
		defer mu.Unlock()
		return RunResult{
			ExitCode:            exitCode,
			Signal:              signal,
			Stdout:              jsstr.DecodeUTF8(stdout),
			Stderr:              jsstr.DecodeUTF8(stderr),
			TimedOut:            timedOut,
			OutputLimitExceeded: overflow,
		}
	}

	// spawn throws synchronously for some failures; TS catches them.
	thrown := jsexc.Try(func() {
		// The listeners run on one goroutine, but the first chunk can
		// arrive before Spawn returns: hold the lock until child is set.
		mu.Lock()
		defer mu.Unlock()
		child = nodeproc.Spawn("docker", args, nodeproc.SpawnOptions{
			Cwd:      cwd,
			Stdout:   nodeproc.StdioPipe,
			Stderr:   nodeproc.StdioPipe,
			OnStdout: func(chunk []byte) { collect(chunk, true) },
			OnStderr: func(chunk []byte) { collect(chunk, false) },
		})
		timer = time.AfterFunc(time.Duration(timeoutMs)*time.Millisecond, func() {
			mu.Lock()
			defer mu.Unlock()
			if settled {
				return
			}
			timedOut = true
			child.Kill("SIGKILL")
		})
	})
	if thrown != nil {
		return finish(nil, nil, jsexc.Message(thrown))
	}
	exit := child.Wait()
	if exit.Err != nil {
		return finish(nil, nil, exit.Err.Error())
	}
	var signal *string
	if exit.Signal != "" {
		s := exit.Signal
		signal = &s
	}
	return finish(exit.Code, signal, "")
}

// forceRemoveContainer ports forceRemoveContainer (src/sandbox.ts:886):
// `docker rm --force <name>` with stdio [ignore, ignore, pipe], killed after
// 5 s. It reports the detail to append to stderr, or false when the removal
// succeeded.
func forceRemoveContainer(containerName string) (string, bool) {
	if containerName == "" {
		return "", false
	}
	var (
		mu            sync.Mutex
		cleanupStderr string
		cleanup       *nodeproc.Child
	)
	thrown := jsexc.Try(func() {
		mu.Lock()
		defer mu.Unlock()
		cleanup = nodeproc.Spawn("docker", []string{"rm", "--force", containerName}, nodeproc.SpawnOptions{
			Stdout: nodeproc.StdioIgnore,
			Stderr: nodeproc.StdioPipe,
			// Each chunk is decoded on its own, as TS's += does.
			OnStderr: func(chunk []byte) {
				mu.Lock()
				defer mu.Unlock()
				cleanupStderr += jsstr.DecodeUTF8(chunk)
			},
		})
	})
	if thrown != nil {
		return "FaultLine could not force-remove its named container: " + jsexc.Message(thrown), true
	}
	done := make(chan nodeproc.Exit, 1)
	go func() { done <- cleanup.Wait() }()
	timer := time.NewTimer(cleanupTimeout)
	defer timer.Stop()
	select {
	case exit := <-done:
		if exit.Err != nil {
			return "FaultLine could not force-remove its named container: " + exit.Err.Error(), true
		}
		if exit.Code != nil && *exit.Code == 0 {
			return "", false
		}
		code := "unknown"
		if exit.Code != nil {
			code = strconv.Itoa(*exit.Code)
		}
		mu.Lock()
		defer mu.Unlock()
		return "FaultLine attempted docker rm --force for its named container (exit " + code + "): " + jsstr.Trim(cleanupStderr), true
	case <-timer.C:
		cleanup.Kill("SIGKILL")
		return "FaultLine timed out while force-removing its named container.", true
	}
}
