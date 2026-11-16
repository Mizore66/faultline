// Package guided ports src/guided-investigate.ts. This file is the guided
// CI-log intake (createGuidedDraft, guided-investigate.ts:197-263).
package guided

import (
	"errors"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/incident"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/nodeproc"
	"github.com/Mizore66/faultline/internal/runtimeimage"
	"github.com/Mizore66/faultline/internal/safegit"
	"github.com/Mizore66/faultline/internal/witness"
)

// IntakeOptions are createGuidedDraft's options. The pointer fields are the
// TS optional strings: nil is undefined, a pointer to "" is the empty string.
type IntakeOptions struct {
	Repository, CILogPath, WitnessStore, DraftStore string
	Command, From, To, IncidentID, RuntimeAlias     *string
}

// IntakeResult is { draft, draftPath }.
type IntakeResult struct {
	Draft     jsjson.Value
	DraftPath string
}

const (
	taskIntakeDefaultCommand = `node -e "console.log('Replace this command with the failing CI predicate after human review'); process.exit(1)"`
	taskIntakeFallback       = "CI reported a failure; review the attached log."
	// taskIntakeTimeoutSeconds is the reachable guided entry's timeout.
	taskIntakeTimeoutSeconds = 300
)

// intakeDeps are the seams for ordering and failure tests; the defaults call
// the real modules.
type intakeDeps struct {
	nowMillis      func() int64
	now            func() time.Time
	readLog        func(path string) (string, error)
	resolveRuntime func(alias string) (jsjson.Value, error)
	spawn          func(file string, args []string, o nodeproc.SpawnSyncOptions) nodeproc.Result
	createDraft    func(incident.DraftInput) (jsjson.Value, error)
	propose        func(store string, input jsjson.Value) (jsjson.Value, error)
	writeDraft     func(store string, draft jsjson.Value) (string, error)
}

func defaultIntakeDeps() intakeDeps {
	return intakeDeps{
		nowMillis: clock.NowMillis,
		now:       clock.Now,
		readLog:   nodefs.ReadText,
		resolveRuntime: func(alias string) (jsjson.Value, error) {
			return runtimeimage.ResolveCuratedRuntime(alias, runtimeimage.NodeRunner())
		},
		spawn:       nodeproc.SpawnSyncWith,
		createDraft: incident.CreateDraft,
		propose:     witness.Propose,
		writeDraft:  incident.WriteDraft,
	}
}

// CreateIntake is createGuidedDraft.
func CreateIntake(o IntakeOptions) (IntakeResult, error) {
	return createIntake(o, defaultIntakeDeps())
}

func createIntake(o IntakeOptions, d intakeDeps) (IntakeResult, error) {
	if (o.From == nil) != (o.To == nil) {
		return IntakeResult{}, errors.New("Guided investigate accepts --from and --to together, or neither for the conservative local HEAD-parent fallback.")
	}
	ciLog, err := d.readLog(nodefs.Resolve(o.CILogPath))
	if err != nil {
		return IntakeResult{}, err
	}
	command := taskIntakeDefaultCommand
	if o.Command != nil {
		if trimmed := jsstr.Trim(*o.Command); trimmed != "" {
			command = trimmed
		}
	}
	var draftID string
	if o.IncidentID != nil {
		draftID = *o.IncidentID
	} else {
		draftID = "guided-" + strconv.FormatInt(d.nowMillis(), 36)
	}
	input := incident.DraftInput{ID: draftID, Repository: o.Repository, Command: command}
	if o.RuntimeAlias != nil {
		resolved, err := d.resolveRuntime(*o.RuntimeAlias)
		if err != nil {
			return IntakeResult{}, err
		}
		input.Runtime = jsjson.MustObject("requested", resolved.Get("requested"), "image", resolved.Get("image"))
	}
	head, parents, err := taskLocalHead(d.spawn, o.Repository)
	if err != nil {
		return IntakeResult{}, err
	}
	input.CreatedAt = clock.ISO(d.now())
	if o.From != nil && o.To != nil && *o.From != "" && *o.To != "" {
		input.Range = jsjson.MustObject("ancestor", jsjson.MakeString(*o.From), "descendant", jsjson.MakeString(*o.To))
	} else {
		input.LocalHead = incident.LocalHeadFacts(head, parents)
	}

	preliminary, err := d.createDraft(input)
	if err != nil {
		return IntakeResult{}, err
	}
	rng := preliminary.Get("range")
	packet := jsjson.NewObj()
	packet.Set("symptom", jsjson.MakeString(deriveSymptom(ciLog)))
	packet.Set("ciLog", jsjson.MakeString(taskIntakeLogPrefix(ciLog)))
	packet.Set("repositoryLanguage", jsjson.MakeString("Unknown"))
	packet.Set("repositorySummary", jsjson.MakeString(jsexc.Concat("Guided CI-log intake for local range ",
		rng.Get("ancestor").Str(), " → ", rng.Get("descendant").Str(),
		". Review-only until human approval and freeze; nothing executed at intake.")))
	policy := jsjson.NewObj()
	policy.Set("network", jsjson.MakeString("disabled"))
	policy.Set("credentials", jsjson.MakeString("redacted"))
	policy.Set("timeoutSeconds", jsjson.MakeNumber(taskIntakeTimeoutSeconds))
	w := jsjson.NewObj()
	w.Set("behavior", jsjson.MakeString("The human-supplied command must exit successfully at the selected immutable Git states."))
	w.Set("command", jsjson.MakeString(command))
	w.Set("overlays", jsjson.MakeArray([]jsjson.Value{}))
	w.Set("policy", jsjson.MakeObject(policy))
	in := jsjson.NewObj()
	in.Set("proposalId", preliminary.Get("draftId"))
	in.Set("proposalOrigin", jsjson.MakeString("HUMAN"))
	in.Set("incidentPacket", jsjson.MakeObject(packet))
	in.Set("witness", jsjson.MakeObject(w))
	proposal, err := d.propose(o.WitnessStore, jsjson.MakeObject(in))
	if err != nil {
		return IntakeResult{}, err
	}

	binding := jsjson.NewObj()
	binding.Set("proposalId", proposal.Get("proposalId"))
	binding.Set("proposalDigest", proposal.Get("proposalDigest"))
	binding.Set("incidentPacketDigest", proposal.Get("incidentPacketDigest"))
	binding.Set("commandDigest", proposal.Get("witness", "commandDigest"))
	input.Proposal = jsjson.MakeObject(binding)
	draft, err := d.createDraft(input)
	if err != nil {
		return IntakeResult{}, err
	}
	path, err := d.writeDraft(o.DraftStore, draft)
	if err != nil {
		return IntakeResult{}, err
	}
	return IntakeResult{Draft: draft, DraftPath: path}, nil
}

// taskIntakeLogPrefix is ciLog.slice(0, 4_000) over UTF-16 units.
func taskIntakeLogPrefix(s string) string {
	units := jsstr.ToUTF16(s)
	if len(units) > 4000 {
		units = units[:4000]
	}
	return jsstr.FromUTF16(units)
}

// taskIntakeMatches is /fail|error|expected|assert/i. Without the u flag JS
// canonicalizes by uppercasing but never maps a non-ASCII unit to ASCII, so
// only ASCII letters fold.
func taskIntakeMatches(line string) bool {
	b := []byte(line)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	l := string(b)
	for _, w := range []string{"fail", "error", "expected", "assert"} {
		if strings.Contains(l, w) {
			return true
		}
	}
	return false
}

// deriveSymptom ports deriveSymptom (guided-investigate.ts:111).
func deriveSymptom(ciLog string) string {
	var lines []string
	for _, raw := range strings.Split(ciLog, "\n") {
		raw = strings.TrimSuffix(raw, "\r")
		if line := jsstr.Trim(raw); line != "" {
			lines = append(lines, line)
		}
	}
	pick := taskIntakeFallback
	if len(lines) > 0 {
		pick = lines[len(lines)-1]
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if taskIntakeMatches(lines[i]) {
			pick = lines[i]
			break
		}
	}
	units := jsstr.ToUTF16(pick)
	if len(units) > 500 {
		units = units[:500]
	}
	return jsstr.FromUTF16(units)
}

// taskLocalHead ports locallyObservedHead (guided-investigate.ts:117): the
// intake safe Git config, a minimal environment, default buffering and no
// timeout.
func taskLocalHead(spawn func(string, []string, nodeproc.SpawnSyncOptions) nodeproc.Result, repository string) (string, []string, error) {
	argv := append(safegit.Intake(), "-C", repository, "show", "-s", "--format=%H%n%P", "HEAD")
	path, _ := nodeproc.Getenv("PATH")
	env := []string{
		"PATH=" + path, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0",
		"GIT_NO_REPLACE_OBJECTS=1", "GIT_LFS_SKIP_SMUDGE=1", "GIT_ALLOW_PROTOCOL=none",
	}
	if runtime.GOOS == "windows" {
		for _, name := range []string{"SystemRoot", "ComSpec", "PATHEXT"} {
			if v, _ := nodeproc.Getenv(name); v != "" {
				env = append(env, name+"="+v)
			}
		}
	} else {
		for i := range env {
			env[i] = jsstr.ToUTF8(env[i])
		}
	}
	r := spawn("git", argv, nodeproc.SpawnSyncOptions{Env: env})
	if r.Err != nil || r.Status == nil || *r.Status != 0 {
		detail := jsstr.DecodeUTF8(r.Stderr)
		if r.Err != nil {
			detail = jsexc.Concat(detail, r.Err.Error())
		}
		detail = jsstr.Trim(detail)
		if detail == "" {
			detail = "supply --from and --to explicitly"
		}
		return "", nil, errors.New(jsexc.Concat("FaultLine could not read a local Git HEAD for guided intake: ", detail))
	}
	lines := strings.Split(strings.ReplaceAll(jsstr.DecodeUTF8(r.Stdout), "\r", ""), "\n")
	head := jsstr.Trim(lines[0])
	parentLine := ""
	if len(lines) > 1 {
		parentLine = jsstr.Trim(lines[1])
	}
	if head == "" {
		return "", nil, errors.New("FaultLine could not read a local Git HEAD for guided intake; supply --from and --to explicitly.")
	}
	return head, taskSplitWhitespace(parentLine), nil
}

// taskSplitWhitespace is `s.split(/\s+/).filter(Boolean)`.
func taskSplitWhitespace(s string) []string {
	units := jsstr.ToUTF16(s)
	parents := []string{}
	start := -1
	for i, u := range units {
		if jsstr.IsWhitespace(u) {
			if start >= 0 {
				parents = append(parents, jsstr.FromUTF16(units[start:i]))
				start = -1
			}
		} else if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		parents = append(parents, jsstr.FromUTF16(units[start:]))
	}
	return parents
}
