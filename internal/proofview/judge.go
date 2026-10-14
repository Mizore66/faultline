package proofview

import (
	"errors"
	"io/fs"
	"strconv"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/evidence"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// Port of src/proof-roots.ts:7-15.
const (
	RecordedSelfIncidentRoot   = "sha256:f6a391b3407731d766bd19510c4e4172ad44f28771f1d034030fc56513625b75"
	JudgeCommitProofSampleRoot = "sha256:f85c446dfd5ab92222b10a314e79209a8a7dc10ee69af9d2deaa04aceafeb7d9"
)

// Port of src/proof-roots.ts:18 commitProofPackageIdentityNotice; nil is
// undefined.
func commitProofPackageIdentityNotice(rootDigest string) *string {
	var notice string
	switch rootDigest {
	case JudgeCommitProofSampleRoot:
		notice = "Sample package (live-git demo); historical self-incident is a separate package."
	case RecordedSelfIncidentRoot:
		notice = "Historical self-incident package; the disposable judge sample root is a separate package."
	default:
		return nil
	}
	return &notice
}

// View is VerifiedGitProofView (src/git-proof-view.ts:40) without its
// attachments member: judge-proof and commit-proof-preview pass no
// attachment options, and loadVerifiedIncidentAttachments with empty options
// returns { minimization: null, repair: null, prevention: null } without
// reading anything, so every attachment is the constant null here and the
// panels render their null branch only.
type View struct {
	RootDigest         string
	CheckedFiles       int
	ExternalRootStatus string
	Manifest           jsjson.Value
	Investigation      jsjson.Value
	FrozenWitness      jsjson.Value
}

// Port of src/git-proof-view.ts:50 readJson.
func readJSON(path, label string) jsjson.Value {
	v, err := bundle.ParseJSONFile(path)
	if err != nil {
		jsexc.Throw(errors.New(jsexc.Concat("Unable to read verified ", label, ": ", jsexc.Message(err))))
	}
	return v
}

type presentationArtifacts struct{ investigation, frozenWitness jsjson.Value }

// Port of src/git-proof-view.ts:68 readProofPresentationArtifacts: a schema
// failure throws the ZodError, as Schema.parse does.
func readProofPresentationArtifacts(root string, manifest jsjson.Value) presentationArtifacts {
	return presentationArtifacts{
		investigation: bundle.Must(bundle.ParseValue(readJSON(nodefs.Join(root, manifest.Get("artifacts", "investigation").Str()), "investigation artifact"), gitproof.GitInvestigationResultSchema)),
		frozenWitness: bundle.Must(bundle.ParseValue(readJSON(nodefs.Join(root, manifest.Get("artifacts", "frozenWitness").Str()), "frozen witness artifact"), gitproof.FrozenWitnessSchema)),
	}
}

// Port of src/git-proof-view.ts:79 sameProofPresentationArtifacts.
func sameProofPresentationArtifacts(left, right presentationArtifacts) bool {
	return bundle.SameCanonical(left.investigation, right.investigation) && bundle.SameCanonical(left.frozenWitness, right.frozenWitness)
}

// Port of src/git-proof-view.ts:89 loadVerifiedGitProofView (with no
// attachment options). A nil expectedRoot is undefined.
func LoadVerifiedGitProofView(directory string, expectedRoot *string) (view View, err error) {
	err = jsexc.Try(func() { view = loadVerifiedGitProofView(directory, expectedRoot) })
	return view, err
}

func verifyBundle(root string, expectedRoot *string) (bundle.Result, jsjson.Value) {
	if expectedRoot == nil {
		return gitproof.VerifyWithManifest(root, "", false)
	}
	return gitproof.VerifyWithManifest(root, *expectedRoot, true)
}

func loadVerifiedGitProofView(directory string, expectedRoot *string) View {
	root := nodefs.Resolve(directory)
	verification, manifest := verifyBundle(root, expectedRoot)
	if !verification.Valid || manifest.Kind() == jsjson.Undefined || verification.RootDigest == nil {
		detail := "no valid root digest was produced"
		if len(verification.Errors) > 0 {
			detail = jsexc.Join(verification.Errors, "; ")
		}
		jsexc.Throw(errors.New(jsexc.Concat("Refusing to render an invalid Git proof bundle: ", detail)))
	}
	rootDigest := *verification.RootDigest

	// Read twice around verifier passes and keep only matching snapshots
	// (see the comment at src/git-proof-view.ts:104).
	initialArtifacts := readProofPresentationArtifacts(root, manifest)
	recheck, recheckManifest := verifyBundle(root, &rootDigest)
	if !recheck.Valid || recheckManifest.Kind() == jsjson.Undefined || recheck.RootDigest == nil || *recheck.RootDigest != rootDigest {
		detail := "the bundle changed while its artifacts were being read"
		if len(recheck.Errors) > 0 {
			detail = jsexc.Join(recheck.Errors, "; ")
		}
		jsexc.Throw(errors.New(jsexc.Concat("Refusing to render a Git proof bundle that changed during verification: ", detail)))
	}
	recheckedArtifacts := readProofPresentationArtifacts(root, recheckManifest)
	finalCheck, finalManifest := verifyBundle(root, &rootDigest)
	if !finalCheck.Valid || finalManifest.Kind() == jsjson.Undefined || finalCheck.RootDigest == nil || *finalCheck.RootDigest != rootDigest ||
		!bundle.SameCanonical(manifest, recheckManifest) ||
		!bundle.SameCanonical(manifest, finalManifest) ||
		!sameProofPresentationArtifacts(initialArtifacts, recheckedArtifacts) {
		detail := "the bundle presentation artifacts changed during verification"
		if len(finalCheck.Errors) > 0 {
			detail = jsexc.Join(finalCheck.Errors, "; ")
		}
		jsexc.Throw(errors.New(jsexc.Concat("Refusing to render a Git proof bundle that changed during verification: ", detail)))
	}
	return View{
		RootDigest:         rootDigest,
		CheckedFiles:       finalCheck.CheckedFiles,
		ExternalRootStatus: verification.ExternalRootStatus,
		Manifest:           finalManifest,
		Investigation:      recheckedArtifacts.investigation,
		FrozenWitness:      recheckedArtifacts.frozenWitness,
	}
}

// Port of src/git-proof-view.ts:140 shortDigest (UTF-16 slices).
func shortDigest(value string) string {
	units := jsstr.ToUTF16(value)
	if len(units) <= 22 {
		return value
	}
	return jsstr.FromUTF16(units[:15]) + "..." + jsstr.FromUTF16(units[len(units)-8:])
}

// Port of src/git-proof-view.ts:144 shortCommit (a Git object id: ASCII).
func shortCommit(value string) string {
	units := jsstr.ToUTF16(value)
	return jsstr.FromUTF16(units[:min(12, len(units))])
}

func underscoresToSpaces(s string) string { return strings.ReplaceAll(s, "_", " ") }

// Port of src/git-proof-view.ts:148 verdictBadge. Verdicts are schema enums
// (ASCII), so strings.ToLower is toLowerCase.
func verdictBadge(verdict string) string {
	return `<span class="verdict ` + EscapeHTML(strings.ReplaceAll(strings.ToLower(verdict), "_", "-")) + `">` + EscapeHTML(verdict) + `</span>`
}

// Port of src/git-proof-view.ts:152 externalRootCopy.
func externalRootCopy(status string) string {
	switch status {
	case "MATCH":
		return "MATCH - the supplied external root matches this verified package."
	case "NOT_PROVIDED":
		return "NOT PROVIDED - the package is self-consistent, but no externally retained root was checked."
	case "MISMATCH":
		return "MISMATCH - this state is never rendered because the bundle loader rejects it."
	}
	return "undefined" // the switch has no default: `${undefined}`
}

func count(v jsjson.Value) string { return strconv.Itoa(len(v.Items())) }

func number(v jsjson.Value) string { return jsjson.FormatNumber(v.Num()) }

// Port of src/git-proof-view.ts:163 transitionRows.
func transitionRows(transitions []jsjson.Value) string {
	if len(transitions) == 0 {
		return "<tr><td colspan=\"5\">No stable transition was recorded.</td></tr>"
	}
	var b strings.Builder
	for _, transition := range transitions {
		before, after := transition.Get("before"), transition.Get("after")
		b.WriteString(`<tr>
    <td>` + EscapeHTML(underscoresToSpaces(transition.Get("kind").Str())) + `</td>
    <td><code title="` + EscapeHTML(before.Get("commit").Str()) + `">` + EscapeHTML(shortCommit(before.Get("commit").Str())) + `</code> ` + verdictBadge(before.Get("verdict").Str()) + `</td>
    <td>&rarr;</td>
    <td><code title="` + EscapeHTML(after.Get("commit").Str()) + `">` + EscapeHTML(shortCommit(after.Get("commit").Str())) + `</code> ` + verdictBadge(after.Get("verdict").Str()) + `</td>
    <td>` + count(before.Get("executionIds")) + ` + ` + count(after.Get("executionIds")) + ` distinct Docker executions</td>
  </tr>`)
	}
	return b.String()
}

// Port of src/git-proof-view.ts:176 stableStateRows.
func stableStateRows(view View) string {
	states := view.Investigation.Get("stableStates").Items()
	if len(states) == 0 {
		return "<tr><td colspan=\"4\">No stable state was recorded.</td></tr>"
	}
	var b strings.Builder
	for _, state := range states {
		b.WriteString(`<tr>
    <td>` + number(state.Get("stateIndex")) + `</td>
    <td><code title="` + EscapeHTML(state.Get("commit").Str()) + `">` + EscapeHTML(shortCommit(state.Get("commit").Str())) + `</code></td>
    <td>` + verdictBadge(state.Get("verdict").Str()) + `</td>
    <td>` + count(state.Get("executionIds")) + ` distinct Docker executions</td>
  </tr>`)
	}
	return b.String()
}

// Port of src/git-proof-view.ts:188 sandboxRows.
func sandboxRows(view View) string {
	runs := view.Investigation.Get("runs").Items()
	var representatives []jsjson.Value // a Map: first run per key, in insertion order
	seen := map[string]bool{}
	for _, run := range runs {
		key := jsexc.Concat(run.Get("sandbox", "kind").Str(), ":", run.Get("result", "executor").Str(), ":", run.Get("sandbox", "policyDigest").Str())
		if !seen[key] {
			jsexc.SetAdd(seen, key)
			representatives = append(representatives, run)
		}
	}
	if len(representatives) == 0 {
		return "<tr><td colspan=\"5\">No sandbox execution facts were recorded.</td></tr>"
	}
	var b strings.Builder
	for _, run := range representatives {
		policyDigest := run.Get("sandbox", "policyDigest").Str()
		coveredRuns, truncatedRuns := 0, 0
		for _, candidate := range runs {
			if candidate.Get("sandbox", "policyDigest").Str() == policyDigest {
				coveredRuns++
				if candidate.Get("result", "outputTruncated").Bool() {
					truncatedRuns++
				}
			}
		}
		runtime := run.Get("sandbox", "runtime")
		pick := func(field, null, prefix string) string {
			if runtime.Get(field).Kind() == jsjson.Null {
				return null
			}
			return prefix + runtime.Get(field).Str()
		}
		flag := func(field, yes, no string) string {
			if runtime.Get(field).Bool() {
				return yes
			}
			return no
		}
		runtimeEvidence := jsexc.Join([]string{
			pick("image", "image not attested", "image "),
			pick("network", "network not attested", "network "),
			flag("rootFilesystemReadOnly", "read-only root", "writable root"),
			pick("user", "user not attested", "user "),
			flag("capDropAll", "all capabilities dropped", "capability policy incomplete"),
			flag("noNewPrivileges", "no-new-privileges", "privilege policy incomplete"),
			pick("pull", "pull policy not attested", "pull "),
		}, " | ")
		truncated := ""
		if truncatedRuns != 0 {
			truncated = "; " + strconv.Itoa(truncatedRuns) + " output-truncated"
		}
		b.WriteString(`<tr>
      <td>` + EscapeHTML(underscoresToSpaces(run.Get("sandbox", "kind").Str())) + `</td>
      <td>` + EscapeHTML(underscoresToSpaces(run.Get("result", "executor").Str())) + `</td>
      <td><code title="` + EscapeHTML(policyDigest) + `">` + EscapeHTML(shortDigest(policyDigest)) + `</code></td>
      <td><span class="policy-text" title="` + EscapeHTML(runtimeEvidence) + `">` + EscapeHTML(runtimeEvidence) + `</span></td>
      <td>` + strconv.Itoa(coveredRuns) + ` runs` + truncated + `</td>
    </tr>`)
	}
	return b.String()
}

// Port of src/git-proof-view.ts:225 lifecyclePanel.
func lifecyclePanel(view View) string {
	lifecycle := view.Manifest.Get("lifecycle")
	status := lifecycle.Get("status").Str()
	if status == "UNBOUND" {
		return `<article class="card panel"><h3>Lifecycle binding</h3><p class="empty-state"><strong>UNBOUND</strong><br>` + EscapeHTML(lifecycle.Get("limitation").Str()) + `</p><small>FaultLine does not infer private model reasoning or native Codex interception from an unbound package.</small></article>`
	}
	bindings := lifecycle.Get("checkpointBindings").Items()
	covered := map[float64]bool{} // new Set(stateIndex numbers)
	for _, binding := range bindings {
		jsexc.SetAdd(covered, binding.Get("stateIndex").Num())
	}
	coverage := strconv.Itoa(len(covered)) + " of " + count(view.Investigation.Get("states")) + " investigated Git states"
	var bindingStatus, bindingScope string
	switch status {
	case "FULLY_BOUND":
		bindingStatus = "FULLY BOUND"
		bindingScope = "Every investigated state has an observed checkpoint (" + coverage + ")."
	case "PARTIALLY_BOUND":
		bindingStatus = "PARTIALLY BOUND"
		bindingScope = "Only " + coverage + " have observed checkpoints; the remaining replayed states are not lifecycle-bound."
	default:
		bindingStatus = "LEGACY BOUND"
		bindingScope = "This package predates explicit lifecycle coverage labels. It lists " + coverage + ", but its old BOUND label does not assert complete coverage."
	}
	var rows strings.Builder
	for _, binding := range bindings {
		rows.WriteString(`<tr>
    <td>` + number(binding.Get("stateIndex")) + `</td>
    <td>` + number(binding.Get("sequence")) + `</td>
    <td><code title="` + EscapeHTML(binding.Get("checkpointDigest").Str()) + `">` + EscapeHTML(shortDigest(binding.Get("checkpointDigest").Str())) + `</code></td>
  </tr>`)
	}
	return `<article class="card panel"><h3>Lifecycle binding</h3>
    <p><strong>` + bindingStatus + `</strong> via observed ` + EscapeHTML(underscoresToSpaces(lifecycle.Get("transport").Str())) + ` events. ` + EscapeHTML(bindingScope) + ` This records checkpoints, not private model reasoning or native interception.</p>
    <div class="proof"><span>Ledger digest</span><code title="` + EscapeHTML(lifecycle.Get("ledgerDigest").Str()) + `">` + EscapeHTML(shortDigest(lifecycle.Get("ledgerDigest").Str())) + `</code></div>
    <div class="proof"><span>Ledger head</span><code title="` + EscapeHTML(lifecycle.Get("headHash").Str()) + `">` + EscapeHTML(shortDigest(lifecycle.Get("headHash").Str())) + `</code></div>
    <div class="table-wrap"><table class="data-table"><thead><tr><th>Git state</th><th>Ledger sequence</th><th>Checkpoint digest</th></tr></thead><tbody>` + rows.String() + `</tbody></table></div>
  </article>`
}

// Port of src/git-proof-view.ts:256 recoveryPanel.
func recoveryPanel(transitions []jsjson.Value) string {
	var recoveries []jsjson.Value
	for _, transition := range transitions {
		if transition.Get("kind").Str() == "FAIL_TO_PASS" {
			recoveries = append(recoveries, transition)
		}
	}
	if len(recoveries) == 0 {
		return `<article class="card panel"><h3>Recovery evidence</h3><p class="empty-state"><strong>Not verified</strong><br>No later stable FAIL_TO_PASS transition is attached to this package.</p><small>A verified boundary does not establish a repair or prevention. Attach separate recovery and prevention evidence to make either claim.</small></article>`
	}
	var rows strings.Builder
	for _, transition := range recoveries {
		before, after := transition.Get("before"), transition.Get("after")
		rows.WriteString(`<div class="proof"><span><code title="` + EscapeHTML(before.Get("commit").Str()) + `">` + EscapeHTML(shortCommit(before.Get("commit").Str())) + `</code> ` + verdictBadge(before.Get("verdict").Str()) + `</span><span>&rarr;</span><span><code title="` + EscapeHTML(after.Get("commit").Str()) + `">` + EscapeHTML(shortCommit(after.Get("commit").Str())) + `</code> ` + verdictBadge(after.Get("verdict").Str()) + `</span></div>`)
	}
	return `<article class="card panel"><h3>Recorded recovery</h3><p><strong>Recorded stable recovery</strong><small>Each displayed state has three distinct Docker executions under the same frozen witness. This establishes recovery only, not prevention.</small></p>` + rows.String() + `</article>`
}

// Port of src/git-proof-view.ts:265 minimizationPanel: its null branch only
// (no attachment is ever supplied, see View).
const minimizationPanel = `<article class="card panel"><h3>Counterfactual minimization</h3><p class="empty-state"><strong>Not attached</strong><br>No minimization record was supplied to this read-only view.</p><small>A stable Git boundary does not by itself establish a 1-minimal set, agent intent, or a unique semantic root cause.</small></article>`

// Port of src/git-proof-view.ts:291 repairPanel: its null branch only.
const repairPanel = `<article class="card panel"><h3>Evidence-bounded repair packet</h3><p>FaultLine can hand a repair workflow the frozen witness, verified boundary, and evidence digests without choosing a model culprit.</p><p class="empty-state"><strong>Repair not attached</strong><br>No repair artifact was supplied to this view.</p><small>No repair claim is rendered from a boundary-only proof package.</small></article>`

// Port of src/git-proof-view.ts:307 preventionGuidancePanel: the branch with
// neither a prevention package nor a repair attachment.
const preventionGuidancePanel = `<article class="card panel"><h3>Prevention guidance</h3><p class="empty-state"><strong>Not attached</strong><br>No evidence-cited prevention guidance or prevention-proof package was supplied.</p><small>Recovery evidence and a stable boundary do not establish prevention. Attach a grounded <code>faultline.prevention-proof.v1</code> package via <code>fl prevention write --from-bundle</code>.</small></article>`

// Port of src/git-proof-view.ts:345 attachmentSummary with both
// attachments null.
const attachmentSummary = "It distinguishes an observed boundary from downstream artifacts that were not supplied."

func ptr(s string) *string { return &s }

// Port of src/git-proof-view.ts:337 renderGitProofIncidentPage.
func RenderGitProofIncidentPage(view View) string {
	manifest, investigation, frozenWitness := view.Manifest, view.Investigation, view.FrozenWitness
	transitions := investigation.Get("transitions").Items()
	var firstRegression *jsjson.Value
	for i := range transitions {
		if transitions[i].Get("kind").Str() == "PASS_TO_FAIL" {
			firstRegression = &transitions[i]
			break
		}
	}
	approval := frozenWitness.Get("approval")
	states := len(investigation.Get("states").Items())
	runs := len(investigation.Get("runs").Items())
	executionSummary := strconv.Itoa(runs) + " recorded Docker-isolated executions across " + strconv.Itoa(states) + " Git states (" + number(investigation.Get("executionsPerState")) + " per state)."
	omitted := max(0, states-len(investigation.Get("stableStates").Items()))
	var stableStateSummary string
	if omitted == 0 {
		stableStateSummary = executionSummary + " All recorded states reached a stable pass/fail verdict."
	} else {
		did, is := "states did", "are"
		if omitted == 1 {
			did, is = "state did", "is"
		}
		stableStateSummary = executionSummary + " " + strconv.Itoa(omitted) + " recorded " + did + " not reach a stable pass/fail verdict and " + is + " omitted from this table."
	}
	proofStatus := "NOT CERTIFIED AS PROOF"
	if investigation.Get("proof", "isProof").Bool() {
		proofStatus = "VERIFIED PROOF"
	}
	evidenceGrade := investigation.Get("proof", "evidenceGrade").Str()
	evidenceLabel := investigation.Get("proof", "evidenceLabel").Str()

	ideaBoundary := "No stable PASS→FAIL transition is recorded in this package."
	if firstRegression != nil {
		ideaBoundary = jsexc.Concat("First stable PASS→FAIL at ", shortCommit(firstRegression.Get("after", "commit").Str()), " under a human-frozen witness; package root ", shortDigest(view.RootDigest), ".")
	}
	firstHeading := "No stable pass-to-fail transition"
	firstDetail := "FaultLine refuses to name a first bad state without a stable bracket."
	if firstRegression != nil {
		firstHeading = "First stable pass-to-fail transition"
		firstDetail = shortCommit(firstRegression.Get("before", "commit").Str()) + " -> " + shortCommit(firstRegression.Get("after", "commit").Str())
	}
	return renderIncidentPageDocumentStart("verified Git proof") + "\n" +
		renderIncidentPageProductChrome(chromeOptions{
			command:       "fl serve --bundle / fl judge-proof",
			packageBanner: commitProofPackageIdentityNotice(view.RootDigest),
			ideaBeat:      ptr("Portable offline-verifiable evidence for one frozen predicate — another engineer verifies without re-running repository code."),
			notice:        ptr(readonlyNotice),
		}) + `
<section class="hero"><div><div class="eyebrow">FAULTLINE · COMMIT_PROOF</div><h1>Where this frozen<br>predicate <em>first failed.</em></h1><p class="lede">` + EscapeHTML(ideaBoundary) + ` Grade <code>` + EscapeHTML(evidenceGrade) + `</code> (` + EscapeHTML(evidenceLabel) + `). When certified: <code>` + evidence.CommitPathEvidenceGrade + `</code>. Not turn-path (<code>` + evidence.TurnPathEvidenceGradeExperimental + `</code>). ` + EscapeHTML(attachmentSummary) + `</p><span class="readonly-badge">READ-ONLY REVIEW</span></div><aside class="card metric-card" aria-label="Verified proof summary"><div class="metric"><b>` + strconv.Itoa(view.CheckedFiles) + `</b><small>declared files checked</small></div><div class="metric"><b>` + strconv.Itoa(runs) + `</b><small>recorded executions</small></div><div class="metric"><b>` + strconv.Itoa(states) + `</b><small>Git states</small></div><div class="metric"><b>` + strconv.Itoa(len(transitions)) + `</b><small>stable transitions</small></div></aside></section>
<section class="section" id="break"><div class="section-head"><span class="number">01</span><h2>BREAK → FIND</h2></div><div class="grid"><article class="card panel witness"><h3>Frozen witness</h3><p><strong>Human-reviewed executable predicate</strong></p><span class="pill">human-approved before freeze</span><span class="pill">network disabled</span><span class="pill">read-only review</span><p class="hash">` + EscapeHTML(frozenWitness.Get("witnessDigest").Str()) + `</p><small>Behavior text, command text, overlay bytes, incident-packet contents, and reviewer identity are intentionally omitted from this shareable view.</small></article><article class="card panel"><h3>Package verification</h3><p><strong>` + EscapeHTML(proofStatus) + `</strong></p><div class="proof"><span>Evidence grade</span><code>` + EscapeHTML(evidenceGrade) + `</code></div><div class="proof"><span>Evidence label</span><small>` + EscapeHTML(evidenceLabel) + `</small></div><div class="proof proof-stack"><span>Turn-path contrast</span><small><code>` + evidence.TurnPathEvidenceGradeExperimental + `</code> — ` + EscapeHTML(evidence.TurnPathEvidenceLabelExperimental) + ` (not this page)</small></div><div class="proof"><span>Bundle root</span><code title="` + EscapeHTML(view.RootDigest) + `">` + EscapeHTML(shortDigest(view.RootDigest)) + `</code></div><div class="proof"><span>External root</span><small>` + EscapeHTML(externalRootCopy(view.ExternalRootStatus)) + `</small></div><div class="proof"><span>Human approval</span><small>` + EscapeHTML(approval.Get("approvedAt").Str()) + ` recorded before freeze</small></div></article></div></section>
<section class="section" id="find"><div class="section-head"><span class="number">02</span><h2>FIND</h2></div><div class="grid"><article class="card panel"><h3>Stable Git states</h3><div class="table-wrap"><table class="data-table"><thead><tr><th>Index</th><th>Commit</th><th>Verdict</th><th>Evidence</th></tr></thead><tbody>` + stableStateRows(view) + `</tbody></table></div><small>` + EscapeHTML(stableStateSummary) + `</small></article><article class="card panel"><h3>Stable transitions</h3><p>Three reruns on either side prevent a single noisy result from becoming attribution.</p><div class="table-wrap"><table class="data-table transitions-table"><thead><tr><th>Transition</th><th>Before</th><th></th><th>After</th><th>Evidence</th></tr></thead><tbody>` + transitionRows(transitions) + `</tbody></table></div><p><strong>` + firstHeading + `</strong></p><small>` + firstDetail + `</small></article></div></section>
<section class="section" id="prove"><div class="section-head"><span class="number">03</span><h2>PROVE</h2></div><div class="grid"><article class="card panel"><h3>Sandbox evidence</h3><p>Recorded execution trust: <strong>` + EscapeHTML(underscoresToSpaces(investigation.Get("proof", "executionTrust").Str())) + `</strong>.</p><div class="table-wrap"><table class="data-table sandbox-table"><thead><tr><th>Kind</th><th>Executor</th><th>Policy</th><th>Runtime constraints</th><th>Coverage</th></tr></thead><tbody>` + sandboxRows(view) + `</tbody></table></div><small>Policy and command values are represented by digests; raw output and overlay bytes are intentionally omitted.</small></article>` + minimizationPanel + `</div></section>
<section class="section" id="fix"><div class="section-head"><span class="number">04</span><h2>FIX</h2></div><div class="grid">` + repairPanel + lifecyclePanel(view) + `</div></section>
<section class="section" id="prevent"><div class="section-head"><span class="number">05</span><h2>PREVENT</h2></div><div class="grid">` + recoveryPanel(transitions) + preventionGuidancePanel + `<article class="card panel"><h3>Portable proof bundle</h3><div class="proof"><span>Integrity scope</span><small>` + EscapeHTML(manifest.Get("integrityScope").Str()) + `</small></div><div class="proof"><span>Bundle root</span><code title="` + EscapeHTML(view.RootDigest) + `">` + EscapeHTML(shortDigest(view.RootDigest)) + `</code></div><div class="proof-command"><span>Verified command</span><pre class="command-snippet"><code>fl verify &lt;bundle&gt; --expect-root ` + EscapeHTML(view.RootDigest) + `</code></pre></div><details><summary>Integrity boundary</summary><pre>Verifies the complete declared file set against an externally retained root. Separately supplied attachments are re-verified and linked before rendering, but are not retroactively included in this Git proof root.</pre></details></article></div></section>
` + renderIncidentPageDocumentEnd("FaultLine reports verified predicate-specific evidence, states when minimization or repair artifacts are absent, and does not claim private model reasoning, a unique semantic cause, or native Codex interception.", nil)
}

const readonlyNotice = "Read-only verified bundle: FaultLine checked its complete declared file set before rendering. This page does not execute repository code or rerun the witness."

// Port of src/judge-proof.ts:12-16: docs/samples/self-incident-commit-proof
// under the workspace (the cwd).
func DefaultSelfIncidentSampleDirectory() string {
	return nodefs.Resolve(nodefs.Join("docs", "samples", "self-incident-commit-proof"))
}

// Port of src/judge-proof.ts:18 defaultCommitProofPreviewPath.
func DefaultCommitProofPreviewPath() string {
	return nodefs.Resolve("docs", "self-incident-proof-preview.html")
}

// Port of src/judge-proof.ts:22 assertSelfIncidentSamplePresent.
func assertSelfIncidentSamplePresent(directory string) {
	root := nodefs.Resolve(directory)
	if !nodefs.Exists(nodefs.Join(root, "manifest.json")) {
		jsexc.Throw(errors.New(jsexc.Join([]string{
			jsexc.Concat("Missing COMMIT_PROOF sample at ", root, "."),
			"Copy a verified Git proof bundle into that directory (must include manifest.json; no extra files like README.md),",
			"then open with: pnpm fl judge-proof --expect-root " + JudgeCommitProofSampleRoot,
			"Or regenerate with Docker: pnpm fl demo live-git --export-only and copy the printed bundle path.",
			"See docs/samples/COMMIT_PROOF_SAMPLE.md. Historical self-incident root remains",
			RecordedSelfIncidentRoot,
		}, " ")))
	}
}

// Port of src/judge-proof.ts:39 loadSelfIncidentProofView: the CLI always
// passes the directory; a nil expectedRoot defaults to the sample root.
func LoadSelfIncidentProofView(directory string, expectedRoot *string) (view View, err error) {
	err = jsexc.Try(func() { view = loadSelfIncidentProofView(directory, expectedRoot) })
	return view, err
}

func loadSelfIncidentProofView(directory string, expectedRoot *string) View {
	directory = nodefs.Resolve(directory)
	assertSelfIncidentSamplePresent(directory)
	root := JudgeCommitProofSampleRoot
	if expectedRoot != nil {
		root = *expectedRoot
	}
	return loadVerifiedGitProofView(directory, &root)
}

// Preview is writeCommitProofPreview's result.
type Preview struct {
	Path       string
	Bytes      int
	RootDigest string
}

// Port of src/judge-proof.ts:55 writeCommitProofPreview (the CLI always
// passes the directory and the output file).
func WriteCommitProofPreview(directory string, expectedRoot *string, output string) (p Preview, err error) {
	err = jsexc.Try(func() {
		view := loadSelfIncidentProofView(directory, expectedRoot)
		// String.prototype.replace with a string pattern: first occurrence only.
		html := strings.Replace(RenderGitProofIncidentPage(view), "</title>",
			"</title>\n<meta name=\"faultline-preview\" content=\"static-commit-proof-snapshot\" />", 1)
		page := strings.Replace(html,
			"FaultLine checked its complete declared file set before rendering. This page does not execute repository code or rerun the witness.",
			"STATIC SNAPSHOT of a recorded COMMIT_PROOF package. Opened from disk HTML — not a live Docker run. Verify the retained root before treating this as authoritative evidence.", 1)
		out := nodefs.Resolve(output)
		jsexc.Must(0, nodefs.MkdirAll(nodefs.Dirname(out), 0o777))
		jsexc.Must(0, nodefs.WriteFileUTF8(out, page, fs.FileMode(0o644), false))
		// Buffer.byteLength(page, "utf8"): lone surrogates count as U+FFFD.
		p = Preview{Path: out, Bytes: len(jsstr.ToUTF8(page)), RootDigest: view.RootDigest}
	})
	return p, err
}
