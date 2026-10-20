// Package doctor is the Go port of src/doctor.ts: the read-only local
// preflight for the Git + Docker path (`fl doctor`). A READY result only
// says that this narrow prerequisite was observed; it never attests to a
// witness, an image, an execution or a proof.
package doctor

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mizore66/faultline/internal/hostcmd"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsre"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/safegit"
)

// Port of src/doctor.ts:15-18.
const (
	SchemaVersion    = "faultline.doctor.v1"
	MinimumNodeMajor = 22
	CommandTimeout   = 5000 * time.Millisecond
	MaxOutputBytes   = 64 * 1024
)

// runtimeMarkers ports src/doctor.ts:135 runtimeMarkers.
var runtimeMarkers = []struct {
	kind, imageFamily string
	files             []string
}{
	{"NODE", "node", []string{"package.json", "pnpm-lock.yaml", "package-lock.json", "yarn.lock", "bun.lock", "bun.lockb"}},
	{"PYTHON", "python", []string{"pyproject.toml", "requirements.txt", "poetry.lock", "Pipfile", "setup.py"}},
	{"GO", "golang", []string{"go.mod"}},
}

// hardenedGitArgs ports src/doctor.ts:173 hardenedGitCommand's arguments.
func hardenedGitArgs(argv ...string) []string {
	return append(safegit.Doctor(), argv...)
}

// unavailableObservation ports src/doctor.ts:177.
func unavailableObservation(r hostcmd.Result) string {
	switch {
	case r.OutputLimitExceeded:
		return "The command exceeded FaultLine's " + strconv.Itoa(MaxOutputBytes) + "-byte diagnostic output limit."
	case r.TimedOut:
		return "The command timed out before returning a result."
	case r.ExitCode == nil:
		return "The command could not be started or did not return an exit code."
	}
	return "The command exited with status " + strconv.Itoa(*r.ExitCode) + "."
}

// outputLine ports src/doctor.ts:184: runs of CR/LF become one space, then
// trim(), then the first 160 UTF-16 units (which can cut a surrogate pair,
// as slice does); ok is false for an empty line (TS null).
func outputLine(value string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		if c := value[i]; c == '\r' || c == '\n' {
			for i+1 < len(value) && (value[i+1] == '\r' || value[i+1] == '\n') {
				i++
			}
			b.WriteByte(' ')
			continue
		}
		b.WriteByte(value[i])
	}
	line := jsstr.Trim(b.String())
	if line == "" {
		return "", false
	}
	if units := jsstr.ToUTF16(line); len(units) > 160 {
		return jsstr.FromUTF16(units[:160]), true
	}
	return line, true
}

// commandSucceeded ports src/doctor.ts:189.
func commandSucceeded(r hostcmd.Result) bool {
	return !r.TimedOut && !r.OutputLimitExceeded && r.ExitCode != nil && *r.ExitCode == 0
}

// diagnostic is a DoctorDiagnostic; a nil observation or remediation is null.
type diagnostic struct {
	id, status, summary      string
	observation, remediation *string
}

func ptr(s string) *string { return &s }

func optional(s string, ok bool) *string {
	if !ok {
		return nil
	}
	return &s
}

func nullable(s *string) jsjson.Value {
	if s == nil {
		return jsjson.MakeNull()
	}
	return jsjson.MakeString(*s)
}

func (d diagnostic) value() jsjson.Value {
	return jsjson.MustObject("id", jsjson.MakeString(d.id), "status", jsjson.MakeString(d.status),
		"summary", jsjson.MakeString(d.summary), "observation", nullable(d.observation), "remediation", nullable(d.remediation))
}

// gitDiagnostic ports src/doctor.ts:207.
func gitDiagnostic(r hostcmd.Result) diagnostic {
	version, ok := outputLine(r.Stdout)
	if !commandSucceeded(r) {
		return diagnostic{"git", "UNAVAILABLE", "Git is unavailable to FaultLine.", ptr(unavailableObservation(r)),
			ptr("Install Git and make it available on PATH, then run fl doctor again.")}
	}
	if !ok {
		return diagnostic{"git", "ACTION_REQUIRED", "Git returned no recognizable version output.", nil,
			ptr("Repair the Git installation or PATH entry, then run fl doctor again.")}
	}
	return diagnostic{"git", "READY", "Git is available.", ptr(version), nil}
}

// nodeVersion is /^v(\d+)(?:\.\d+){0,2}(?:\S*)?$/ (src/doctor.ts:247). jsre
// refuses the optional group around \S* (a quantifier over a body that can
// match the empty string); \S* alone accepts the same strings with the same
// first group: JS refuses the empty iteration of the group and falls back
// to skipping it, which leaves the same position \S* matching empty does.
var nodeVersion = jsre.MustCompile(`^v(\d+)(?:\.\d+){0,2}\S*$`, "")

// nodeDiagnostic ports src/doctor.ts:235.
func nodeDiagnostic(r hostcmd.Result) diagnostic {
	if !commandSucceeded(r) {
		return diagnostic{"node", "UNAVAILABLE", "Node.js is unavailable to FaultLine.", ptr(unavailableObservation(r)),
			ptr("Install Node.js " + strconv.Itoa(MinimumNodeMajor) + " or newer and make it available on PATH, then run fl doctor again.")}
	}
	version, ok := outputLine(r.Stdout)
	var major *string
	if ok {
		if m := nodeVersion.FindAll(version); len(m) > 0 {
			major = m[0].Groups[0]
		}
	}
	if major == nil {
		return diagnostic{"node", "ACTION_REQUIRED", "Node.js returned an unrecognizable version.", optional(version, ok),
			ptr("Use a supported Node.js " + strconv.Itoa(MinimumNodeMajor) + "+ installation, then run fl doctor again.")}
	}
	if jsjson.ToNumber(*major) < MinimumNodeMajor {
		return diagnostic{"node", "ACTION_REQUIRED", "Node.js " + version + " is below FaultLine's required major version.", ptr(version),
			ptr("Switch to Node.js " + strconv.Itoa(MinimumNodeMajor) + " or newer, then run fl doctor again.")}
	}
	return diagnostic{"node", "READY", "A supported Node.js runtime is available.", ptr(version), nil}
}

// dockerCliDiagnostic ports src/doctor.ts:276.
func dockerCliDiagnostic(r hostcmd.Result) diagnostic {
	version, ok := outputLine(r.Stdout)
	if !commandSucceeded(r) {
		return diagnostic{"docker-cli", "UNAVAILABLE", "The Docker CLI is unavailable to FaultLine.", ptr(unavailableObservation(r)),
			ptr("Install Docker Desktop or Docker Engine and make the docker command available on PATH.")}
	}
	if !ok {
		return diagnostic{"docker-cli", "ACTION_REQUIRED", "The Docker CLI returned no recognizable version output.", nil,
			ptr("Repair the Docker CLI installation, then run fl doctor again.")}
	}
	return diagnostic{"docker-cli", "READY", "The Docker CLI is available.", ptr(version), nil}
}

// dockerDaemonDiagnostic ports src/doctor.ts:304.
func dockerDaemonDiagnostic(r hostcmd.Result) diagnostic {
	version, ok := outputLine(r.Stdout)
	if !commandSucceeded(r) || !ok {
		observation := "Docker returned no server version."
		if !commandSucceeded(r) {
			observation = unavailableObservation(r)
		}
		return diagnostic{"docker-daemon", "UNAVAILABLE", "The Docker daemon is not reachable by FaultLine.", ptr(observation),
			ptr("Start Docker Desktop or Docker Engine and ensure the current user can access its daemon, then run fl doctor again.")}
	}
	return diagnostic{"docker-daemon", "READY", "The Docker daemon is reachable.", ptr(version), nil}
}

// inapplicableDiagnostic ports src/doctor.ts:324.
func inapplicableDiagnostic(id, summary, remediation string) diagnostic {
	return diagnostic{id, "INAPPLICABLE", summary, nil, ptr(remediation)}
}

// likelyRuntime is a LikelyRuntime; imageFamily "" is null.
type likelyRuntime struct {
	kind        string
	markers     []string
	imageFamily string
}

func (r likelyRuntime) value() jsjson.Value {
	markers := make([]jsjson.Value, len(r.markers))
	for i, m := range r.markers {
		markers[i] = jsjson.MakeString(m)
	}
	family := jsjson.MakeNull()
	if r.imageFamily != "" {
		family = jsjson.MakeString(r.imageFamily)
	}
	return jsjson.MustObject("kind", jsjson.MakeString(r.kind), "markers", jsjson.MakeArray(markers), "imageFamily", family)
}

// detectLikelyRuntime ports src/doctor.ts:345 detectLikelyRuntime with the
// default file probe (existsSync, which never throws).
func detectLikelyRuntime(repository string) likelyRuntime {
	type found struct {
		kind, imageFamily string
		markers           []string
	}
	var hits []found
	for _, candidate := range runtimeMarkers {
		var markers []string
		for _, file := range candidate.files {
			if nodefs.Exists(nodefs.Join(repository, file)) {
				markers = append(markers, file)
			}
		}
		if len(markers) > 0 {
			hits = append(hits, found{candidate.kind, candidate.imageFamily, markers})
		}
	}
	switch len(hits) {
	case 0:
		return likelyRuntime{kind: "UNKNOWN", markers: []string{}}
	case 1:
		return likelyRuntime{kind: hits[0].kind, markers: hits[0].markers, imageFamily: hits[0].imageFamily}
	}
	var markers []string
	for _, h := range hits {
		markers = append(markers, h.markers...)
	}
	return likelyRuntime{kind: "MULTIPLE", markers: markers}
}

// DetectLikelyRuntime is detectLikelyRuntime as its JSON value
// ({kind, markers, imageFamily}).
func DetectLikelyRuntime(repository string) jsjson.Value {
	return detectLikelyRuntime(repository).value()
}

// runtimeDiagnostic ports src/doctor.ts:368.
func runtimeDiagnostic(r likelyRuntime) diagnostic {
	switch r.kind {
	case "UNKNOWN":
		return inapplicableDiagnostic("runtime",
			"FaultLine could not infer a likely runtime family from conventional root markers.",
			"Choose an explicit digest-pinned image and witness command; automatic image selection is deliberately not attempted.")
	case "MULTIPLE":
		return diagnostic{"runtime", "INAPPLICABLE",
			"Multiple likely runtime families were detected; FaultLine will not choose an image automatically.",
			ptr(strings.Join(r.markers, ", ")), ptr("Choose an explicit digest-pinned image and witness command for the failing behavior.")}
	}
	return diagnostic{"runtime", "READY", "Likely " + r.kind + " repository detected from conventional root markers.",
		ptr(strings.Join(r.markers, ", ")),
		ptr("This is only a suggestion. Select and pin an image digest that matches the witness before investigating.")}
}

// preflightStatus ports src/doctor.ts:394: every check but runtime.
func preflightStatus(diagnostics []diagnostic) string {
	var statuses []string
	for _, d := range diagnostics {
		if d.id != "runtime" {
			statuses = append(statuses, d.status)
		}
	}
	all := true
	for _, s := range statuses {
		all = all && s == "READY"
	}
	switch {
	case all:
		return "READY"
	case contains(statuses, "UNAVAILABLE"):
		return "UNAVAILABLE"
	case contains(statuses, "ACTION_REQUIRED"):
		return "ACTION_REQUIRED"
	}
	return "INAPPLICABLE"
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// CliExitCode ports src/doctor.ts:411 doctorCliExitCode: 0 when the node
// diagnostic is READY, else 1.
func CliExitCode(report jsjson.Value) int {
	for _, d := range report.Get("diagnostics").Items() {
		if d.Get("id").Str() == "node" {
			if d.Get("status").Str() == "READY" {
				return 0
			}
			return 1
		}
	}
	return 1
}

// ProofReadyStatus is the report's dockerInvestigationPreflight.
func ProofReadyStatus(report jsjson.Value) string {
	return report.Get("dockerInvestigationPreflight").Str()
}

// Run ports src/doctor.ts:420 runFaultLineDoctor for an already resolved
// repository path (the CLI resolves --repo or the cwd first; resolve is
// applied again here, as in TS).
func Run(repo string, run Runner) jsjson.Value {
	repository := nodefs.Resolve(repo)

	// Promise.all: the three version checks start together, in this order.
	var gitResult, nodeResult, dockerCliResult hostcmd.Result
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); gitResult = runCommand(run, "git", hardenedGitArgs("--version")) }()
	go func() { defer wg.Done(); nodeResult = runCommand(run, "node", []string{"--version"}) }()
	go func() { defer wg.Done(); dockerCliResult = runCommand(run, "docker", []string{"--version"}) }()
	wg.Wait()
	git := gitDiagnostic(gitResult)
	node := nodeDiagnostic(nodeResult)
	dockerCli := dockerCliDiagnostic(dockerCliResult)

	var repositoryRoot *string
	var repositoryDiagnostic, worktreeDiagnostic diagnostic
	if git.status != "READY" {
		repositoryDiagnostic = inapplicableDiagnostic("repository",
			"Repository detection was not attempted because Git is unavailable.",
			"Make Git available, then run fl doctor again.")
		worktreeDiagnostic = inapplicableDiagnostic("worktree",
			"Worktree cleanliness was not checked because repository detection is unavailable.",
			"Make Git available and run fl doctor from a repository.")
	} else {
		repositoryResult := runCommand(run, "git", hardenedGitArgs("-C", repository, "rev-parse", "--show-toplevel"))
		reportedRoot, ok := outputLine(repositoryResult.Stdout)
		if !commandSucceeded(repositoryResult) || !ok {
			observation := "Git returned no repository root."
			if !commandSucceeded(repositoryResult) {
				observation = unavailableObservation(repositoryResult)
			}
			repositoryDiagnostic = diagnostic{"repository", "ACTION_REQUIRED",
				"No Git repository was detected at the requested directory or one of its parents.", ptr(observation),
				ptr("Run fl doctor with --repo pointing at a Git worktree, then run it again.")}
			worktreeDiagnostic = inapplicableDiagnostic("worktree",
				"Worktree cleanliness was not checked because no Git repository was detected.",
				"Run fl doctor from a Git worktree.")
		} else {
			root := nodefs.Resolve(reportedRoot)
			repositoryRoot = &root
			repositoryDiagnostic = diagnostic{"repository", "READY", "A Git repository was detected.", ptr(root), nil}
			statusResult := runCommand(run, "git", hardenedGitArgs("-C", root, "status", "--porcelain=v1", "--untracked-files=all", "-z"))
			if !commandSucceeded(statusResult) {
				worktreeDiagnostic = diagnostic{"worktree", "UNAVAILABLE", "FaultLine could not read the Git worktree status.",
					ptr(unavailableObservation(statusResult)), ptr("Fix Git access to this worktree, then run fl doctor again.")}
			} else {
				changes := 0
				for _, part := range strings.Split(statusResult.Stdout, "\x00") {
					if part != "" {
						changes++
					}
				}
				if changes == 0 {
					worktreeDiagnostic = diagnostic{"worktree", "READY", "The Git worktree is clean.",
						ptr("No tracked or untracked changes reported by Git."), nil}
				} else {
					plural := "s"
					if changes == 1 {
						plural = ""
					}
					worktreeDiagnostic = diagnostic{"worktree", "ACTION_REQUIRED",
						"The Git worktree has changes that would make a clean checkpoint ambiguous.",
						ptr(strconv.Itoa(changes) + " changed path" + plural + " reported by Git."),
						ptr("Commit or stash the changes, or model them explicitly before recording evidence.")}
				}
			}
		}
	}

	var dockerDaemon diagnostic
	if dockerCli.status != "READY" {
		dockerDaemon = inapplicableDiagnostic("docker-daemon",
			"Docker daemon reachability was not checked because the Docker CLI is unavailable.",
			"Install or repair the Docker CLI first, then run fl doctor again.")
	} else {
		dockerDaemon = dockerDaemonDiagnostic(runCommand(run, "docker", []string{"info", "--format", "{{.ServerVersion}}"}))
	}

	runtimeRoot := repository
	if repositoryRoot != nil {
		runtimeRoot = *repositoryRoot
	}
	runtime := detectLikelyRuntime(runtimeRoot)
	diagnostics := []diagnostic{git, repositoryDiagnostic, worktreeDiagnostic, node, dockerCli, dockerDaemon, runtimeDiagnostic(runtime)}
	items := make([]jsjson.Value, len(diagnostics))
	for i, d := range diagnostics {
		items[i] = d.value()
	}
	return jsjson.MustObject(
		"schemaVersion", jsjson.MakeString(SchemaVersion),
		"repository", jsjson.MakeString(repository),
		"repositoryRoot", nullable(repositoryRoot),
		"diagnostics", jsjson.MakeArray(items),
		"likelyRuntime", runtime.value(),
		"imageSelection", jsjson.MakeString("NOT_CHECKED"),
		"dockerInvestigationPreflight", jsjson.MakeString(preflightStatus(diagnostics)),
		"limitations", jsjson.MakeArray([]jsjson.Value{
			jsjson.MakeString("This is a local prerequisite check, not a proof of behavior or provenance."),
			jsjson.MakeString("FaultLine did not pull or inspect a container image, execute a witness, access the network, or write to the repository."),
			jsjson.MakeString("Likely runtime detection uses root-file markers only; choose and digest-pin the actual image explicitly."),
		}),
	)
}
