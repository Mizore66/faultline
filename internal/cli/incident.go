package cli

import (
	"errors"
	"io/fs"
	"math"
	"runtime"
	"strings"

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

// incidentUsage is the refusal of src/cli-app.ts:577.
const incidentUsage = "Usage: fl incident suggest|start|status|continue ..."

// incidentCommand ports src/cli-app.ts:563 incidentCommand.
func incidentCommand(e *env, args []string) error {
	switch arg(args, 0) {
	case "suggest":
		return incidentSuggestCommand(e, args)
	case "status":
		return incidentStatusCommand(e, args)
	case "continue":
		return incidentContinueCommand(e, args)
	case "start":
		return incidentStartCommand(e, args)
	}
	return fail("%s", incidentUsage)
}

// incidentSuggestCommand ports the suggest branch of src/cli-app.ts:564-574.
func incidentSuggestCommand(e *env, args []string) error {
	repo, err := requiredOption(args, "--repo")
	if err != nil {
		return err
	}
	suggestions, err := incident.SuggestRanges(repo, incident.NodeIntakeRunner(repo))
	if err != nil {
		return err
	}
	none := len(suggestions.Get("candidates").Items()) == 0
	status := "RANGE_SUGGESTIONS_READY"
	next := "Review the local candidates. When one applies, pass its exact from/to commits to fl incident start; FaultLine will still require human witness approval and freeze before execution."
	if none {
		status = "NO_REVIEWABLE_RANGE_SUGGESTIONS"
		next = "Supply an explicit reviewed --from <commit> and --to <commit> when you start the incident. FaultLine intentionally did not guess a range."
	}
	// { status, ...suggestions, next }
	o := jsjson.NewObj()
	o.Set("status", jsjson.MakeString(status))
	for _, k := range suggestions.Obj().Keys() {
		o.Set(k, suggestions.Obj().Field(k))
	}
	o.Set("next", stringList(next))
	e.printJSON(jsjson.MakeObject(o))
	return nil
}

// commandInput is incidentCommandInput's result (src/cli-app.ts:214).
type commandInput struct {
	command string
	file    string // "" for an inline command
}

// incidentCommandInput ports src/cli-app.ts:214: a reviewable command from
// --command, or from a regular UTF-8 text file (--command-file) without
// asking the caller's shell to re-quote nested command syntax. The command
// bytes, not the file path, become the immutable witness input.
func incidentCommandInput(args []string) (commandInput, error) {
	inline, hasInline := option(args, "--command")
	commandFile, hasFile := option(args, "--command-file")
	if hasInline == hasFile {
		return commandInput{}, fail("Incident intake requires exactly one of --command <failing-command> or --command-file <utf8-file>.")
	}
	if hasInline {
		if inline == "" || strings.HasPrefix(inline, "--") {
			return commandInput{}, fail("Missing required option: --command")
		}
		return commandInput{command: inline}, nil
	}
	if commandFile == "" || strings.HasPrefix(commandFile, "--") {
		return commandInput{}, fail("Missing required option: --command-file")
	}
	file := nodefs.Resolve(commandFile)
	bytes, err := readCommandFile(file)
	if err != nil {
		return commandInput{}, errors.New(jsexc.Concat("Unable to read --command-file ", file, ": ", jsexc.Message(err)))
	}
	if len(bytes) > 32_000 {
		return commandInput{}, fail("--command-file exceeds the 32 KB witness-command limit.")
	}
	command := jsstr.DecodeUTF8(bytes)
	if jsstr.ToUTF8(command) != string(bytes) {
		return commandInput{}, fail("--command-file must contain valid UTF-8 without an ambiguous byte encoding.")
	}
	return commandInput{command: command, file: file}, nil
}

// readCommandFile is the try body of src/cli-app.ts:227-232: lstat, refuse
// a symlink or non-file, then readFileSync.
func readCommandFile(file string) ([]byte, error) {
	info, err := nodefs.Lstat(file)
	if err != nil {
		return nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("must be a real regular file, not a symbolic link or directory")
	}
	return nodefs.ReadBytes(file)
}

// locallyObservedHead ports src/cli-app.ts:402: read only the local HEAD and
// its parents, never infer a remote or PR base. spawnSync("git", [...], {
// encoding: "utf8", shell: false, windowsHide: true, env }) with the
// default maxBuffer and no timeout.
func locallyObservedHead(repository string) (head string, parents []string, err error) {
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
		// Node encodes the env object's JS strings as UTF-8.
		for i := range env {
			env[i] = jsstr.ToUTF8(env[i])
		}
	}
	r := nodeproc.SpawnSyncWith("git", argv, nodeproc.SpawnSyncOptions{Env: env})
	if r.Err != nil || r.Status == nil || *r.Status != 0 {
		detail := jsstr.DecodeUTF8(r.Stderr)
		if r.Err != nil {
			detail = jsexc.Concat(detail, r.Err.Error())
		}
		detail = jsstr.Trim(detail)
		if detail == "" {
			detail = "run fl doctor or supply --from and --to explicitly"
		}
		return "", nil, errors.New(jsexc.Concat("FaultLine could not read a local Git HEAD for incident intake: ", detail))
	}
	lines := strings.Split(strings.ReplaceAll(jsstr.DecodeUTF8(r.Stdout), "\r", ""), "\n")
	head = jsstr.Trim(lines[0])
	parentLine := ""
	if len(lines) > 1 {
		parentLine = jsstr.Trim(lines[1])
	}
	if head == "" {
		return "", nil, fail("FaultLine could not read a local Git HEAD for incident intake; supply --from and --to explicitly.")
	}
	return head, splitJSWhitespace(parentLine), nil
}

// splitJSWhitespace is `s.split(/\s+/).filter(Boolean)`.
func splitJSWhitespace(s string) []string {
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

// generatedIncidentID ports src/cli-app.ts:436: "incident-", the first 17
// digits of the ISO time, "-" and the first 12 characters of a UUID.
func generatedIncidentID() string {
	var digits strings.Builder
	for _, c := range []byte(clock.ISO(clock.Now())) {
		if c >= '0' && c <= '9' && digits.Len() < 17 {
			digits.WriteByte(c)
		}
	}
	return jsexc.Concat("incident-", digits.String(), "-", clock.NewUUID()[:12])
}

// parseTimeoutSeconds is `Number(option(args, "--timeout-seconds") ??
// "300")` checked with Number.isInteger and the 1-3600 bounds
// (src/cli-app.ts:599-602).
func parseTimeoutSeconds(args []string) (float64, error) {
	timeout := jsjson.ToNumber(optionOr(args, "--timeout-seconds", "300"))
	if math.IsNaN(timeout) || math.IsInf(timeout, 0) || timeout != math.Trunc(timeout) || timeout < 1 || timeout > 3_600 {
		return 0, fail("--timeout-seconds must be an integer from 1 through 3600.")
	}
	return timeout, nil
}

// incidentStartCommand ports the start branch of src/cli-app.ts:580-692.
func incidentStartCommand(e *env, args []string) error {
	repoOption, err := requiredOption(args, "--repo")
	if err != nil {
		return err
	}
	repository := nodefs.Resolve(repoOption)
	input, err := incidentCommandInput(args)
	if err != nil {
		return err
	}
	command := input.command
	from, hasFrom := option(args, "--from")
	to, hasTo := option(args, "--to")
	if hasFrom != hasTo {
		return fail("Incident intake accepts --from and --to together, or neither for the conservative local HEAD-parent fallback.")
	}
	draftID, ok := option(args, "--id")
	if !ok {
		draftID = generatedIncidentID()
	}
	requestedRuntime, hasRuntime := option(args, "--runtime")
	requestedImage, hasImage := option(args, "--image")
	if hasRuntime && hasImage {
		return fail("Incident intake accepts either --runtime <node|python|go> or --image <digest-pinned-image>, not both.")
	}
	timeoutSeconds, err := parseTimeoutSeconds(args)
	if err != nil {
		return err
	}
	var runtimeValue jsjson.Value // undefined: no runtime
	switch {
	case hasRuntime:
		resolution, err := runtimeimage.ResolveCuratedRuntime(requestedRuntime, runtimeRunner())
		if err != nil {
			return err
		}
		runtimeValue = jsjson.MustObject("requested", resolution.Get("requested"), "image", resolution.Get("image"))
	case hasImage:
		runtimeValue = jsjson.MustObject("requested", jsjson.MakeString("explicit"), "image", jsjson.MakeString(requestedImage))
	}
	// Even an explicit bracket must belong to a local Git worktree. The
	// observed facts are used only for the no-range fallback; they never
	// make FaultLine select a remote base.
	head, parents, err := locallyObservedHead(repository)
	if err != nil {
		return err
	}
	draftInput := incident.DraftInput{ID: draftID, Repository: repository, Command: command, Runtime: runtimeValue}
	if hasFrom && hasTo {
		draftInput.Range = jsjson.MustObject("ancestor", jsjson.MakeString(from), "descendant", jsjson.MakeString(to))
	} else {
		draftInput.LocalHead = incident.LocalHeadFacts(head, parents)
	}
	// The preliminary draft resolves the range so the proposal's blinded
	// packet can describe the exact locally selected bracket; it is never
	// persisted. Its createdAt is the one clock read TS's draftInput makes.
	preliminary, err := incident.CreateDraft(draftInput)
	if err != nil {
		return err
	}
	draftInput.CreatedAt = preliminary.Get("createdAt").Str()
	store := nodefs.Resolve(optionOr(args, "--store", nodefs.Join(repository, ".faultline", "witnesses")))
	rangeValue := preliminary.Get("range")
	summary := jsexc.Concat("Review-only local Git range ", rangeValue.Get("ancestor").Str(), " to ", rangeValue.Get("descendant").Str(),
		" (", rangeValue.Get("source").Str(), "). No remote base was selected.")
	if image := preliminary.Get("runtime"); image.Kind() != jsjson.Undefined {
		summary = jsexc.Concat(summary, " The selected local runtime resolves to ", image.Get("image").Str(), ".")
	}
	proposalInput := jsjson.MustObject(
		"proposalId", preliminary.Get("draftId"),
		"proposalOrigin", jsjson.MakeString("HUMAN"),
		"incidentPacket", jsjson.MustObject(
			"symptom", jsjson.MakeString(optionOr(args, "--symptom", "A human-reported command is failing and requires a reviewed, frozen witness before localization.")),
			"ciLog", jsjson.MakeString("The exact user-supplied command is stored in the reviewable witness, not executed by incident intake."),
			"repositoryLanguage", jsjson.MakeString(optionOr(args, "--language", "Unknown")),
			"repositorySummary", jsjson.MakeString(summary),
		),
		"witness", jsjson.MustObject(
			"behavior", jsjson.MakeString(optionOr(args, "--behavior", "The human-supplied command must exit successfully at the selected immutable Git states.")),
			"command", jsjson.MakeString(command),
			"overlays", jsjson.MakeArray([]jsjson.Value{}),
			"policy", jsjson.MustObject(
				"network", jsjson.MakeString("disabled"),
				"credentials", jsjson.MakeString("redacted"),
				"timeoutSeconds", jsjson.MakeNumber(timeoutSeconds),
			),
		),
	)
	proposal, err := witness.Propose(store, proposalInput)
	if err != nil {
		return err
	}
	draftInput.Proposal = jsjson.MustObject(
		"proposalId", proposal.Get("proposalId"),
		"proposalDigest", proposal.Get("proposalDigest"),
		"incidentPacketDigest", proposal.Get("incidentPacketDigest"),
		"commandDigest", proposal.Get("witness", "commandDigest"),
	)
	draft, err := incident.CreateDraft(draftInput)
	if err != nil {
		return err
	}
	draftStore := nodefs.Resolve(optionOr(args, "--draft-store", witness.DefaultIncidentDraftStore(repository)))
	path, err := incident.WriteDraft(draftStore, draft)
	if err != nil {
		return err
	}

	commandInputValue := jsjson.MustObject("kind", jsjson.MakeString("INLINE"))
	if input.file != "" {
		commandInputValue = jsjson.MustObject("kind", jsjson.MakeString("FILE"), "path", jsjson.MakeString(input.file))
	}
	draftRuntime := draft.Get("runtime")
	runtimeOrNull := draftRuntime
	if draftRuntime.Kind() == jsjson.Undefined {
		runtimeOrNull = jsjson.MakeNull()
	}
	proposalID := proposal.Get("proposalId").Str()
	continueNext := jsexc.Concat("Resolve a local curated runtime with fl runtime resolve <node|python|go>, then after human freeze provide a retained --expect-digest and explicit digest-pinned image to fl incident continue ", proposalID, ".")
	if draftRuntime.Kind() != jsjson.Undefined {
		continueNext = jsexc.Concat("After human freeze, run fl incident continue ", proposalID, " --expect-digest <retained-frozen-digest> to reuse the reviewed range and selected image ", draftRuntime.Get("image").Str(), ".")
	}
	e.printJSON(obj(
		"status", "DRAFT_REQUIRES_HUMAN_REVIEW",
		"draft", obj(
			"path", path,
			"digest", draft.Get("draftDigest"),
			"range", draft.Get("range"),
			"commandDigest", draft.Get("commandDigest"),
			"commandInput", commandInputValue,
			"runtime", runtimeOrNull,
			"review", draft.Get("review"),
		),
		"witnessProposal", obj(
			"store", store,
			"proposalId", proposal.Get("proposalId"),
			"proposalDigest", proposal.Get("proposalDigest"),
			"incidentPacketDigest", proposal.Get("incidentPacketDigest"),
			"commandDigest", proposal.Get("witness", "commandDigest"),
			"overlays", jsjson.MakeNumber(float64(len(proposal.Get("witness", "overlays").Items()))),
			"state", "NOT_APPROVED_NOT_FROZEN",
		),
		"next", stringList(
			jsexc.Concat("fl witness review ", proposalID, " --store ", store, " --draft-store ", draftStore, " (starts the local human review workbench and prints its URL; approval and freeze are separate explicit clicks)"),
			continueNext,
			"Proof-grade replay additionally requires a Docker daemon; FaultLine will not treat local debug as proof.",
		),
		"limitations", stringList(
			"Incident intake did not execute the command, pull an image, query a remote branch, approve a witness, or freeze a witness.",
			"The local default is only the exactly observed single-parent HEAD range; merge and root HEADs require an explicit range.",
			"A local debug result is not proof-grade. Only completed Docker-isolated replay can produce a portable proof package.",
		),
	))
	return nil
}
