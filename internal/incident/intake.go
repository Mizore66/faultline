package incident

// Port of src/incident-intake.ts: read-only local Git observations that
// help a human start an incident. Nothing here fetches, queries a forge,
// parses a CI log or selects a range; the candidates are a starting point
// for review.

import (
	"errors"
	"io/fs"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/hostcmd"
	"github.com/Mizore66/faultline/internal/jscase"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsre"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/nodeproc"
	"github.com/Mizore66/faultline/internal/safegit"
)

// Port of src/incident-intake.ts:11-14.
const (
	IntakeSchemaVersion   = "faultline.incident-intake.v1"
	IntakeCommandTimeout  = 5_000 * time.Millisecond
	IntakeMaxOutputBytes  = 64 * 1024
	intakeMaxCandidates   = 2
	intakeLocalUpstream   = "LOCAL_UPSTREAM_MERGE_BASE"
	intakeHeadParent      = "HEAD_PARENT"
	intakeMaxIdentityWalk = 256
)

// gitObjectID is src/incident-intake.ts:16 GIT_OBJECT_ID.
var gitObjectID = jsre.MustCompile(`^[a-f0-9]{40}(?:[a-f0-9]{24})?$`, "")

// lineBreak is the /[\r\n]/ of src/incident-intake.ts:317 singleLine.
var lineBreak = jsre.MustCompile(`[\r\n]`, "")

// SafeRefName reports whether s matches src/incident-intake.ts:17 SAFE_REF_NAME,
// /^refs\/(?!.*(?:\.\.|@\{|[~^:?*\[\\\s]|\/{2}|\/$|\.$))[A-Za-z0-9!#$%&'()+,./;=@_-]+$/,
// written out because of its lookahead. After "refs/" the rest must be one
// or more characters of the final class, which holds no line terminator,
// whitespace or any of ~^:?*[\ , so the lookahead's `.*` can reach every
// position and its first, third and fifth alternatives need only checking
// for "..", "@{" and "//", and for a trailing "/" or ".".
func SafeRefName(s string) bool {
	rest, ok := strings.CutPrefix(s, "refs/")
	if !ok || rest == "" {
		return false
	}
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.IndexByte("!#$%&'()+,./;=@_-", c) >= 0) {
			return false
		}
	}
	return !strings.Contains(rest, "..") && !strings.Contains(rest, "@{") && !strings.Contains(rest, "//") &&
		!strings.HasSuffix(rest, "/") && !strings.HasSuffix(rest, ".")
}

// IntakeError is src/incident-intake.ts:26 IncidentIntakeError: Code is one
// of UNSAFE_REPOSITORY_PATH, NOT_A_GIT_WORKTREE, GIT_UNAVAILABLE,
// INVALID_GIT_RESPONSE and HEAD_UNAVAILABLE.
type IntakeError struct{ Code, Message string }

func (e *IntakeError) Error() string { return e.Message }

func intakeError(code string, parts ...string) *IntakeError {
	return &IntakeError{Code: code, Message: jsexc.Concat(parts...)}
}

// IntakeRunner is IncidentIntakeGitRunner's run (src/incident-intake.ts:52)
// for FaultLine's fixed command: git with args, a 5,000 ms timeout and a
// 65,536-byte output cap. A runner that throws (jsexc) is a command that
// produced nothing and has a null exit code.
type IntakeRunner func(args []string) hostcmd.Result

// intakeGitEnvironment ports src/incident-intake.ts:114
// hardenedGitEnvironment: gitinv's hardened set with GIT_PROTOCOL_FROM_USER=0
// right after GIT_ALLOW_PROTOCOL (before the Windows variables).
func intakeGitEnvironment() []string {
	env := gitinv.HardenedGitEnvironment(gitinv.HardenedOptions{})
	at := slices.Index(env, "GIT_ALLOW_PROTOCOL=none") + 1
	return slices.Insert(env, at, "GIT_PROTOCOL_FROM_USER=0")
}

// NodeIntakeRunner ports src/incident-intake.ts:148
// createNodeIncidentIntakeRunner. Each argv carries its own -C, and the TS
// factory takes no argument, so repo is not used: Task 4 passes the
// requested directory for symmetry with the call site.
//
// spawn("git", args, { shell: false, windowsHide: true, stdio: ["ignore",
// "pipe", "pipe"], env: hardenedGitEnvironment() }) in the inherited cwd.
// Unlike hostcmd.Run (the doctor and Docker runners), stdout and stderr are
// kept as bytes, capped on their combined byte length, and decoded once
// when the command settles; a spawn that throws or an 'error' event settles
// with a null exit code and only the bytes already read, with no message.
// The cap and the timer both SIGKILL the child.
func NodeIntakeRunner(repo string) IntakeRunner {
	_ = repo
	return func(args []string) hostcmd.Result {
		return runIntakeCommand("git", args, intakeGitEnvironment(), IntakeCommandTimeout, IntakeMaxOutputBytes)
	}
}

// runIntakeCommand is the promise body of createNodeIncidentIntakeRunner's
// run (src/incident-intake.ts:150-213).
func runIntakeCommand(file string, args, env []string, timeout time.Duration, maxOutputBytes int) hostcmd.Result {
	var (
		mu             sync.Mutex
		settled        bool
		timedOut       bool
		overflow       bool
		stdout, stderr []byte
		child          *nodeproc.Child
		timer          *time.Timer
		result         hostcmd.Result
	)
	finish := func(exitCode *int) {
		if settled {
			return
		}
		settled = true
		if timer != nil {
			timer.Stop() // clearTimeout: a callback waiting on mu sees settled
		}
		result = hostcmd.Result{
			ExitCode:            exitCode,
			Stdout:              jsstr.DecodeUTF8(stdout),
			Stderr:              jsstr.DecodeUTF8(stderr),
			TimedOut:            timedOut,
			OutputLimitExceeded: overflow,
		}
	}
	appendOutput := func(toStdout bool, chunk []byte) {
		mu.Lock()
		defer mu.Unlock()
		remaining := maxOutputBytes - (len(stdout) + len(stderr))
		if remaining <= 0 {
			overflow = true
			child.Kill("SIGKILL")
			return
		}
		retained := chunk[:min(len(chunk), remaining)]
		if toStdout {
			stdout = append(stdout, retained...)
		} else {
			stderr = append(stderr, retained...)
		}
		if len(chunk) > remaining {
			overflow = true
			child.Kill("SIGKILL")
		}
	}

	thrown := jsexc.Try(func() {
		// The first chunk can arrive before Spawn returns: hold mu until
		// child and the timer are set, as Node runs the listeners only
		// after this tick.
		mu.Lock()
		defer mu.Unlock()
		child = nodeproc.Spawn(file, args, nodeproc.SpawnOptions{
			Env:      env,
			Stdout:   nodeproc.StdioPipe,
			Stderr:   nodeproc.StdioPipe,
			OnStdout: func(chunk []byte) { appendOutput(true, chunk) },
			OnStderr: func(chunk []byte) { appendOutput(false, chunk) },
		})
		timer = time.AfterFunc(timeout, func() {
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
		mu.Lock()
		defer mu.Unlock()
		finish(nil)
		return result
	}
	exit := child.Wait()
	mu.Lock()
	defer mu.Unlock()
	if exit.Err != nil {
		finish(nil) // 'error'
		return result
	}
	finish(exit.Code) // 'close'
	return result
}

// fixedGitCommand ports src/incident-intake.ts:134 fixedGitCommand's
// arguments: DOCTOR_SAFE_GIT_CONFIG, -C repository, then args.
func fixedGitCommand(repository string, args ...string) []string {
	return append(append(safegit.Doctor(), "-C", repository), args...)
}

// resolveRealDirectory ports src/incident-intake.ts:227
// resolveRealDirectory: a terminal symlink or a non-directory is refused,
// then the path is realpath'd (macOS /var -> /private/var).
func resolveRealDirectory(path, label string) (string, error) {
	unavailable := func(err error) error {
		var fe *nodefs.Error
		if errors.As(err, &fe) && fe.Code == "ENOENT" {
			return intakeError("UNSAFE_REPOSITORY_PATH", label, " does not exist.")
		}
		return intakeError("UNSAFE_REPOSITORY_PATH", label, " is unavailable.")
	}
	stat, err := nodefs.Lstat(path)
	if err != nil {
		return "", unavailable(err)
	}
	if stat.Mode()&fs.ModeSymlink != 0 || !stat.IsDir() {
		return "", intakeError("UNSAFE_REPOSITORY_PATH", label, " must be a real directory, not a symlink or file.")
	}
	real, err := nodefs.RealpathSync(path)
	if err != nil {
		return "", unavailable(err)
	}
	return real, nil
}

// comparablePath is isInsideDirectory's comparable
// (src/incident-intake.ts:247): on Windows, \\?\UNC\ becomes \\ (the
// prefix matched case-insensitively), \\?\ is dropped, / becomes \, and
// the result is lower-cased for en-US. Elsewhere the path is unchanged.
func comparablePath(value string) string {
	if runtime.GOOS != "windows" {
		return value
	}
	if len(value) >= 8 && value[:4] == `\\?\` && strings.EqualFold(value[4:7], "UNC") && value[7] == '\\' {
		value = `\\` + value[8:]
	}
	value = strings.TrimPrefix(value, `\\?\`)
	return jscase.Lower(strings.ReplaceAll(value, "/", `\`))
}

// isInsideDirectory ports src/incident-intake.ts:241 isInsideDirectory:
// path.relative first, then a dev/ino walk up from child (at most 256
// levels), skipped when parent's ino is 0.
func isInsideDirectory(child, parent string) bool {
	fromParent := nodefs.Relative(comparablePath(parent), comparablePath(child))
	up := "../"
	if runtime.GOOS == "windows" {
		up = `..\`
	}
	if fromParent == "" || !strings.HasPrefix(fromParent, up) && fromParent != ".." && !nodefs.IsAbsolute(fromParent) {
		return true
	}
	inside := false
	jsexc.Try(func() { // a path that cannot be inspected establishes nothing
		parentStat := jsexc.Must(nodefs.Stat(parent))
		parentDev, parentIno, _ := nodefs.StatIdentity(parentStat)
		if parentIno == 0 {
			return
		}
		current := child
		for depth := 0; depth < intakeMaxIdentityWalk; depth++ {
			dev, ino, _ := nodefs.StatIdentity(jsexc.Must(nodefs.Stat(current)))
			if dev == parentDev && ino == parentIno {
				inside = true
				return
			}
			next := nodefs.Dirname(current)
			if next == current {
				return
			}
			current = next
		}
	})
	return inside
}

// commandOutcome is CommandOutcome (src/incident-intake.ts:109).
type commandOutcome struct {
	result hostcmd.Result
	usable bool
}

// normalizeRunnerResult ports src/incident-intake.ts:285
// normalizeRunnerResult. A Go runner's strings and exit code are always
// well-typed; the byte cap is checked again on the UTF-8 lengths.
func normalizeRunnerResult(value hostcmd.Result) commandOutcome {
	value.OutputLimitExceeded = value.OutputLimitExceeded || len(jsstr.ToUTF8(value.Stdout))+len(jsstr.ToUTF8(value.Stderr)) > IntakeMaxOutputBytes
	return commandOutcome{
		result: value,
		usable: value.ExitCode != nil && *value.ExitCode == 0 && !value.TimedOut && !value.OutputLimitExceeded,
	}
}

// runCommand ports src/incident-intake.ts:304 runCommand.
func runCommand(run IntakeRunner, args []string) commandOutcome {
	var value hostcmd.Result
	if thrown := jsexc.Try(func() { value = run(args) }); thrown != nil {
		return commandOutcome{}
	}
	return normalizeRunnerResult(value)
}

func (o commandOutcome) unavailable() bool {
	return o.result.TimedOut || o.result.OutputLimitExceeded || o.result.ExitCode == nil
}

// singleLine ports src/incident-intake.ts:315 singleLine; ok is false for
// null.
func singleLine(value string) (string, bool) {
	normalized := jsstr.Trim(value)
	return normalized, normalized != "" && !lineBreak.Test(normalized)
}

// requiredGitText ports src/incident-intake.ts:320 requiredGitText.
func requiredGitText(run IntakeRunner, repository string, args []string, code, operation string) (string, error) {
	outcome := runCommand(run, fixedGitCommand(repository, args...))
	if !outcome.usable {
		if outcome.unavailable() {
			code = "GIT_UNAVAILABLE"
		}
		return "", intakeError(code, "FaultLine could not safely inspect local Git state while ", operation, ".")
	}
	value, ok := singleLine(outcome.result.Stdout)
	if !ok {
		return "", intakeError("INVALID_GIT_RESPONSE", "Git returned an invalid response while ", operation, ".")
	}
	return value, nil
}

// optionalText is optionalGitText's result (src/incident-intake.ts:346).
type optionalText struct {
	value       string
	present     bool // value is not null
	unavailable bool
}

// optionalGitText ports src/incident-intake.ts:342 optionalGitText.
func optionalGitText(run IntakeRunner, repository string, args ...string) optionalText {
	outcome := runCommand(run, fixedGitCommand(repository, args...))
	if !outcome.usable {
		return optionalText{unavailable: outcome.unavailable()}
	}
	value, ok := singleLine(outcome.result.Stdout)
	return optionalText{value: value, present: ok}
}

// resolveRequiredCommit ports src/incident-intake.ts:361
// resolveRequiredCommit with assertObjectId (354).
func resolveRequiredCommit(run IntakeRunner, repository, revision, operation, code string) (string, error) {
	commit, err := requiredGitText(run, repository, []string{"rev-parse", "--verify", "--end-of-options", revision + "^{commit}"}, code, operation)
	if err != nil {
		return "", err
	}
	if !gitObjectID.Test(commit) {
		return "", intakeError("INVALID_GIT_RESPONSE", "Git returned an invalid commit id while ", operation, ".")
	}
	return commit, nil
}

// isVerifiedAncestor ports src/incident-intake.ts:372 isVerifiedAncestor.
func isVerifiedAncestor(run IntakeRunner, repository, ancestor, descendant string) bool {
	return runCommand(run, fixedGitCommand(repository, "merge-base", "--is-ancestor", ancestor, descendant)).usable
}

// candidate is an IncidentRangeCandidate (src/incident-intake.ts:58).
type candidate struct {
	from, to string
	value    jsjson.Value
}

func observation(source, status, reason string) jsjson.Value {
	return jsjson.MustObject("source", jsjson.MakeString(source), "status", jsjson.MakeString(status), "reason", jsjson.MakeString(reason))
}

func rangeCandidate(id, from, to, confidence, reason string, evidence jsjson.Value) *candidate {
	return &candidate{from: from, to: to, value: jsjson.MustObject(
		"id", jsjson.MakeString(id),
		"from", jsjson.MakeString(from),
		"to", jsjson.MakeString(to),
		"confidence", jsjson.MakeString(confidence),
		"reason", jsjson.MakeString(reason),
		"requiresHumanApproval", jsjson.MakeBool(true),
		"evidence", evidence,
	)}
}

// inspectLocalUpstream ports src/incident-intake.ts:382
// inspectLocalUpstream.
func inspectLocalUpstream(run IntakeRunner, repository, head string) (*candidate, jsjson.Value) {
	notAvailable := func(reason string) (*candidate, jsjson.Value) {
		return nil, observation(intakeLocalUpstream, "NOT_AVAILABLE", reason)
	}
	upstreamOutput := optionalGitText(run, repository, "rev-parse", "--symbolic-full-name", "@{upstream}")
	if !upstreamOutput.present {
		if upstreamOutput.unavailable {
			return notAvailable("FaultLine could not safely read a local upstream reference; no upstream range was proposed.")
		}
		return notAvailable("No resolvable local upstream reference was configured for the current HEAD; no upstream range was proposed.")
	}
	upstreamRef := upstreamOutput.value
	if !SafeRefName(upstreamRef) {
		return notAvailable("Git returned an unsafe local upstream reference; FaultLine refused to use it.")
	}

	upstreamCommitOutput := optionalGitText(run, repository, "rev-parse", "--verify", "--end-of-options", upstreamRef+"^{commit}")
	if !upstreamCommitOutput.present || !gitObjectID.Test(upstreamCommitOutput.value) {
		return notAvailable("The locally configured upstream could not be resolved to a safe local commit; no upstream range was proposed.")
	}
	upstreamCommit := upstreamCommitOutput.value

	mergeBaseOutput := optionalGitText(run, repository, "merge-base", head, upstreamCommit)
	if !mergeBaseOutput.present || !gitObjectID.Test(mergeBaseOutput.value) {
		return notAvailable("FaultLine could not resolve a safe local merge-base for the configured upstream; no upstream range was proposed.")
	}
	mergeBase := mergeBaseOutput.value
	if mergeBase == head {
		return nil, observation(intakeLocalUpstream, "NO_RANGE", "The local upstream merge-base is the current HEAD, so it does not bound any newer commits.")
	}
	if !isVerifiedAncestor(run, repository, mergeBase, head) {
		return notAvailable("FaultLine could not verify the local merge-base as an ancestor of HEAD; no upstream range was proposed.")
	}

	return rangeCandidate(intakeLocalUpstream, mergeBase, head, "MEDIUM",
			jsexc.Concat("This range starts at the locally resolved merge-base with ", upstreamRef, ". FaultLine did not contact that remote; review that potentially stale local ref before freezing a witness."),
			jsjson.MustObject(
				"source", jsjson.MakeString(intakeLocalUpstream),
				"upstreamRef", jsjson.MakeString(upstreamRef),
				"upstreamCommit", jsjson.MakeString(upstreamCommit),
				"mergeBase", jsjson.MakeString(mergeBase),
				"remoteContacted", jsjson.MakeBool(false),
			)),
		observation(intakeLocalUpstream, "CANDIDATE_AVAILABLE",
			jsexc.Concat("A locally resolved upstream merge-base candidate was found from ", upstreamRef, "; it still requires human review."))
}

// inspectHeadParent ports src/incident-intake.ts:488 inspectHeadParent.
func inspectHeadParent(run IntakeRunner, repository, head string, existing []*candidate) (*candidate, jsjson.Value) {
	// HEAD^ is the first parent; HEAD^{} would only peel a tag.
	parentOutput := optionalGitText(run, repository, "rev-parse", "--verify", "--end-of-options", "HEAD^")
	if !parentOutput.present || !gitObjectID.Test(parentOutput.value) {
		if parentOutput.unavailable {
			return nil, observation(intakeHeadParent, "NOT_AVAILABLE", "FaultLine could not safely read HEAD's first parent; no parent fallback was proposed.")
		}
		return nil, observation(intakeHeadParent, "NO_RANGE", "The current HEAD has no resolvable first parent, so no parent fallback was proposed.")
	}
	parent := parentOutput.value
	if !isVerifiedAncestor(run, repository, parent, head) {
		return nil, observation(intakeHeadParent, "NOT_AVAILABLE", "FaultLine could not verify HEAD's first parent as an ancestor; no parent fallback was proposed.")
	}
	for _, c := range existing {
		if c.from == parent && c.to == head {
			return nil, observation(intakeHeadParent, "DUPLICATE_RANGE", "HEAD's parent fallback is identical to an already listed local-upstream candidate.")
		}
	}
	return rangeCandidate(intakeHeadParent, parent, head, "LOW",
			"This is only the immediately preceding local commit. It is a conservative fallback, not evidence that the regression began there.",
			jsjson.MustObject("source", jsjson.MakeString(intakeHeadParent), "parent", jsjson.MakeString(parent))),
		observation(intakeHeadParent, "CANDIDATE_AVAILABLE", "A one-commit parent fallback was found; it still requires human review.")
}

// SuggestRanges ports src/incident-intake.ts:554 suggestIncidentRanges for
// options.repository = repo: at most two locally provable ranges, never a
// choice between them. A nil run is the default NodeIntakeRunner. The
// error is an *IntakeError, or whatever else TS would throw (a lost cwd
// while resolving repo).
func SuggestRanges(repo string, run IntakeRunner) (out jsjson.Value, err error) {
	if thrown := jsexc.Try(func() { out, err = suggestRanges(repo, run) }); thrown != nil {
		return jsjson.Value{}, thrown
	}
	return out, err
}

func suggestRanges(repo string, run IntakeRunner) (jsjson.Value, error) {
	repository, err := resolveRealDirectory(nodefs.Resolve(repo), "Requested repository directory")
	if err != nil {
		return jsjson.Value{}, err
	}
	if run == nil {
		run = NodeIntakeRunner(repo)
	}

	insideWorkTree, err := requiredGitText(run, repository, []string{"rev-parse", "--is-inside-work-tree"}, "NOT_A_GIT_WORKTREE",
		"checking whether the requested directory is a Git worktree")
	if err != nil {
		return jsjson.Value{}, err
	}
	if insideWorkTree != "true" {
		return jsjson.Value{}, intakeError("NOT_A_GIT_WORKTREE", "The requested directory is not inside a non-bare Git worktree.")
	}

	reportedRoot, err := requiredGitText(run, repository, []string{"rev-parse", "--show-toplevel"}, "NOT_A_GIT_WORKTREE", "resolving the Git worktree root")
	if err != nil {
		return jsjson.Value{}, err
	}
	if !nodefs.IsAbsolute(reportedRoot) {
		return jsjson.Value{}, intakeError("INVALID_GIT_RESPONSE", "Git did not return an absolute worktree root.")
	}
	repositoryRoot, err := resolveRealDirectory(nodefs.Resolve(reportedRoot), "Git worktree root")
	if err != nil {
		return jsjson.Value{}, err
	}
	if !isInsideDirectory(repository, repositoryRoot) {
		return jsjson.Value{}, intakeError("UNSAFE_REPOSITORY_PATH", "The requested directory falls outside Git's reported worktree root.")
	}

	head, err := resolveRequiredCommit(run, repositoryRoot, "HEAD", "resolving current HEAD", "HEAD_UNAVAILABLE")
	if err != nil {
		return jsjson.Value{}, err
	}
	var candidates []*candidate
	upstream, upstreamObservation := inspectLocalUpstream(run, repositoryRoot, head)
	if upstream != nil {
		candidates = append(candidates, upstream)
	}
	parent, parentObservation := inspectHeadParent(run, repositoryRoot, head, candidates)
	if parent != nil {
		candidates = append(candidates, parent)
	}

	items := []jsjson.Value{}
	for _, c := range candidates[:min(len(candidates), intakeMaxCandidates)] {
		items = append(items, c.value)
	}
	return jsjson.MustObject(
		"schemaVersion", jsjson.MakeString(IntakeSchemaVersion),
		"repository", jsjson.MakeString(repository),
		"repositoryRoot", jsjson.MakeString(repositoryRoot),
		"head", jsjson.MakeString(head),
		"automaticSelection", jsjson.MakeString("NONE"),
		"selectionReason", jsjson.MakeString("FaultLine found local Git candidates only. A human must review and explicitly choose --from and --to before any witness is frozen or investigation runs."),
		"candidates", jsjson.MakeArray(items),
		"observations", jsjson.MakeArray([]jsjson.Value{upstreamObservation, parentObservation}),
		"limitations", jsjson.MakeArray([]jsjson.Value{
			jsjson.MakeString("FaultLine inspected only local Git metadata and did not contact a remote, fetch, inspect a pull request, parse CI, or execute a witness."),
			jsjson.MakeString("A local upstream ref can be stale or unrelated to the failure. A parent fallback covers only one commit and is not a diagnosis."),
			jsjson.MakeString("No suggested range is selected automatically; a human must review the exact commits and freeze an approved witness separately."),
		}),
	), nil
}
