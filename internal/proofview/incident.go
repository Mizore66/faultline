package proofview

import (
	"strconv"
	"strings"

	"github.com/Mizore66/faultline/internal/demofixture"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
)

// Port of the pure interactive incident renderer in src/ui.ts (renderIncidentPage and its
// helpers). Strings are JS/WTF-8 strings. Every JS regexp used by the source is a non-Unicode
// regexp: \s is the JS WhiteSpace/LineTerminator set (jsstr.IsWhitespace), \w and \d are ASCII
// only, and /i folds ASCII letters only (a non-ASCII code point never folds onto ASCII). The
// patterns are therefore implemented by hand over UTF-16 code units, not with Go regexp/EqualFold.

func incidentLowerUnit(u uint16) uint16 {
	if u >= 'A' && u <= 'Z' {
		return u + ('a' - 'A')
	}
	return u
}

// incidentFoldAt reports whether units[i:] starts with the lowercase ASCII pattern, ignoring
// ASCII case.
func incidentFoldAt(units []uint16, i int, pattern string) bool {
	if i < 0 || i+len(pattern) > len(units) {
		return false
	}
	for k := 0; k < len(pattern); k++ {
		if incidentLowerUnit(units[i+k]) != uint16(pattern[k]) {
			return false
		}
	}
	return true
}

// incidentContainsFold is /pattern/i.test(s) for a literal lowercase ASCII pattern.
func incidentContainsFold(s, pattern string) bool {
	units := jsstr.ToUTF16(s)
	for i := 0; i+len(pattern) <= len(units); i++ {
		if incidentFoldAt(units, i, pattern) {
			return true
		}
	}
	return false
}

func incidentIsWord(u uint16) bool {
	return u >= 'a' && u <= 'z' || u >= 'A' && u <= 'Z' || u >= '0' && u <= '9' || u == '_'
}

// verdictClass is verdict.toLowerCase().replaceAll("_", "-").
func incidentVerdictClass(verdict string) string {
	return strings.ReplaceAll(strings.ToLower(verdict), "_", "-")
}

func incidentVerdictBadge(verdict string) string {
	label := verdict
	switch verdict {
	case "PASS":
		label = "WORKING"
	case "FAIL":
		label = "BROKEN"
	default:
		label = strings.ReplaceAll(verdict, "_", " ")
	}
	return `<span class="verdict ` + incidentVerdictClass(verdict) + `">` + EscapeHTML(label) + `</span>`
}

func incidentFriendlyReason(reasonCode string) string {
	if reasonCode == "WITNESS_SATISFIED" || reasonCode == "PREDICATE_PASS" {
		return "Passed the approved test"
	}
	if reasonCode == "WITNESS_ASSERTION_FAILED" || reasonCode == "PREDICATE_FAIL" {
		return "Failed the approved test"
	}
	return "Could not finish this check"
}

// incidentFriendlyCheckpointName: label.match(/^(\w+)\s+contribution/i)?.[1] gives
// `${session} version`, else label.replace(/\s+contribution boundary$/i, " version").
func incidentFriendlyCheckpointName(label string) string {
	units := jsstr.ToUTF16(label)
	n := 0
	for n < len(units) && incidentIsWord(units[n]) {
		n++
	}
	if n > 0 {
		k := n
		for k < len(units) && jsstr.IsWhitespace(units[k]) {
			k++
		}
		if k > n && incidentFoldAt(units, k, "contribution") {
			return jsstr.FromUTF16(units[:n]) + " version"
		}
	}
	const tail = "contribution boundary"
	start := len(units) - len(tail)
	if start > 0 && incidentFoldAt(units, start, tail) {
		i := start
		for i > 0 && jsstr.IsWhitespace(units[i-1]) {
			i--
		}
		if i < start {
			return jsstr.FromUTF16(units[:i]) + " version"
		}
	}
	return label
}

// incidentTurnNumber is label.match(/Turn\s+(\d+)/i)?.[1].
func incidentTurnNumber(label string) (string, bool) {
	units := jsstr.ToUTF16(label)
	for i := 0; i+4 <= len(units); i++ {
		if !incidentFoldAt(units, i, "turn") {
			continue
		}
		j := i + 4
		k := j
		for k < len(units) && jsstr.IsWhitespace(units[k]) {
			k++
		}
		if k == j {
			continue
		}
		d := k
		for d < len(units) && units[d] >= '0' && units[d] <= '9' {
			d++
		}
		if d == k {
			continue
		}
		return jsstr.FromUTF16(units[k:d]), true
	}
	return "", false
}

func incidentFriendlyTimelineName(label string) string {
	if incidentContainsFold(label, "last good") {
		return "Step 4 — last version that still worked"
	}
	if incidentContainsFold(label, "first bad") {
		return "Step 5 — first version that broke"
	}
	if incidentContainsFold(label, "repaired") {
		return "Step 7 — version after a repair"
	}
	if incidentContainsFold(label, "reintroduced") {
		return "Step 8 — bug came back later"
	}
	if turn, ok := incidentTurnNumber(label); ok {
		return "Step " + turn
	}
	return label
}

func incidentFriendlyStateCaption(label string) string {
	if incidentContainsFold(label, "last good") {
		return "Last good version"
	}
	if incidentContainsFold(label, "first bad") {
		return "First broken version"
	}
	if incidentContainsFold(label, "repair") {
		return "Repaired version"
	}
	return incidentFriendlyTimelineName(label)
}

func incidentFriendlyChangeName(name string) string {
	if strings.Contains(name, "settlement-display-default") {
		return "display default change"
	}
	if strings.Contains(name, "settlement-display-boundary") {
		return "settlement boundary change"
	}
	if strings.Contains(name, "partial") {
		return "incomplete change (could not apply)"
	}
	return name
}

func incidentFriendlyAttemptNote(note string) string {
	switch {
	case incidentContainsFold(note, "no implicated edits"):
		return "With no changes, the test still passes."
	case incidentContainsFold(note, "display default alone"):
		return "Changing only the display currency does not break the test."
	case incidentContainsFold(note, "boundary assignment alone"):
		return "Changing only the settlement side is not enough by itself."
	case incidentContainsFold(note, "partial patch"), incidentContainsFold(note, "unresolved"):
		return "This incomplete change could not be applied, so it does not count as proof."
	case incidentContainsFold(note, "reproduces the frozen witness failure"):
		return "Adding both changes to the working version makes the test fail."
	case incidentContainsFold(note, "restores the frozen witness pass"):
		return "Removing both changes from the broken version makes the test pass again."
	}
	return note
}

func incidentFriendlyClaimStatement(statement string) string {
	switch {
	case incidentContainsFold(statement, "passes before and fails after"):
		return "The approved test passed before Cedar step 5 and failed afterward."
	case incidentContainsFold(statement, "first stable pass-to-fail"):
		return "Cedar step 5 is the first clear place the test flips from working to broken."
	case incidentContainsFold(statement, "two-edit candidate is sufficient"):
		return "Two specific changes are enough to break the working version, and both are needed to keep the failure."
	case incidentContainsFold(statement, "settlement and display-currency"):
		return "It looks like settlement currency and display currency got mixed together at that point."
	case incidentContainsFold(statement, "whether an agent intended"):
		return "We do not know whether anyone meant to make that tradeoff — the tests do not show intent."
	case incidentContainsFold(statement, "cached replay does not establish"):
		return "This demo replay did not fully prove a working-to-broken change."
	case incidentContainsFold(statement, "no stable boundary is certified"):
		return "This demo could not certify exactly where the bug starts."
	case incidentContainsFold(statement, "cached counterfactual results are not a proof"):
		return "These demo checks are helpful, but they are not a full proof package."
	}
	return statement
}

func incidentRunMeta(run jsjson.Value) string {
	if run.Get("executionKind").Str() == "CACHED" {
		return "saved result"
	}
	return jsjson.FormatNumber(run.Get("durationMs").Num()) + " ms"
}

func incidentRunRow(name string, run jsjson.Value) string {
	return `<div class="run-row"><div><strong>` + EscapeHTML(name) + `</strong><small>` +
		EscapeHTML(incidentFriendlyReason(run.Get("reasonCode").Str())) +
		`</small></div><div class="run-meta">` + incidentVerdictBadge(run.Get("verdict").Str()) +
		`<small>` + incidentRunMeta(run) + `</small></div></div>`
}

func incidentTimelineRunRow(run jsjson.Value) string {
	return incidentRunRow(incidentFriendlyTimelineName(demofixture.StateLabel(run.Get("stateId").Str())), run)
}

func incidentContributionRunRow(run jsjson.Value) string {
	return incidentRunRow(incidentFriendlyCheckpointName(demofixture.StateLabel(run.Get("stateId").Str())), run)
}

func incidentEvidenceRow(kind, statement string) string {
	friendly := kind
	switch kind {
	case "EXECUTED":
		friendly = "Measured"
	case "DERIVED":
		friendly = "Confirmed"
	case "INFERRED":
		friendly = "Possible clue"
	case "UNKNOWN":
		friendly = "Not shown"
	}
	return `<div class="evidence-row"><span>` + EscapeHTML(friendly) + `</span><p>` +
		EscapeHTML(incidentFriendlyClaimStatement(statement)) + `</p></div>`
}

// incidentGroupedInteger is Number.prototype.toLocaleString() under the oracle's en-US locale for a
// positive integer binary64: the shortest-digit expansion with ASCII 3-digit grouping.
func incidentGroupedInteger(n float64) string {
	digits := strconv.FormatFloat(n, 'f', -1, 64)
	var out strings.Builder
	for i := 0; i < len(digits); i++ {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteByte(digits[i])
	}
	return out.String()
}

// RenderIncidentPage is the port of src/ui.ts renderIncidentPage. It is pure: it reads only the
// supplied DemoAnalysis-shaped value and returns the internal WTF-8 page. Input must satisfy
// DemoAnalysis field types, including ASCII verdict/outcome enums and positive integer changedLines.
// This function does not validate untrusted analysis before rendering.
func RenderIncidentPage(analysis jsjson.Value) string {
	prevention := analysis.Get("prevention")
	minimization := analysis.Get("minimization")
	metrics := analysis.Get("metrics")

	confirmed := false
	for _, item := range analysis.Get("transitions").Items() {
		if item.Get("kind").Str() == "PASS_TO_FAIL" && item.Get("stable").Bool() {
			confirmed = true
			break
		}
	}
	lastGood := demofixture.StateLabel(prevention.Get("lastGood", "stateId").Str())
	firstBad := demofixture.StateLabel(prevention.Get("firstBad", "stateId").Str())

	var contributionRows strings.Builder
	for i, run := range analysis.Get("contributionRuns").Items() {
		if i >= 3 {
			break
		}
		contributionRows.WriteString(incidentContributionRunRow(run))
	}
	var timelineRows strings.Builder
	for _, run := range analysis.Get("timelineRuns").Items() {
		timelineRows.WriteString(incidentTimelineRunRow(run))
	}
	var attemptRows strings.Builder
	for _, attempt := range minimization.Get("attempts").Items() {
		subset := attempt.Get("subset").Items()
		changes := "No changes"
		if len(subset) > 0 {
			names := make([]string, len(subset))
			for i, s := range subset {
				names[i] = incidentFriendlyChangeName(s.Str())
			}
			changes = strings.Join(names, " + ")
		}
		outcomeRaw := attempt.Get("outcome").Str()
		outcome := "COULD NOT CHECK"
		if outcomeRaw == "PASS" {
			outcome = "WORKING"
		} else if outcomeRaw == "FAIL" {
			outcome = "BROKEN"
		}
		attemptRows.WriteString(`<div class="attempt"><span class="mono">` + EscapeHTML(changes) +
			`</span><b class="attempt-` + strings.ToLower(outcomeRaw) + `">` + EscapeHTML(outcome) +
			`</b><small>` + EscapeHTML(incidentFriendlyAttemptNote(attempt.Get("note").Str())) + `</small></div>`)
	}
	var claimRows strings.Builder
	for _, claim := range analysis.Get("claims").Items() {
		claimRows.WriteString(incidentEvidenceRow(claim.Get("kind").Str(), claim.Get("statement").Str()))
	}
	candidates := minimization.Get("candidate").Items()
	candidateNames := make([]string, len(candidates))
	for i, c := range candidates {
		candidateNames[i] = incidentFriendlyChangeName(c.Str())
	}
	candidateLabel := strings.Join(candidateNames, " + ")
	lastGoodCaption := incidentFriendlyStateCaption(lastGood)
	firstBadCaption := incidentFriendlyStateCaption(firstBad)

	confirmStrong := "We could not confirm exactly where the bug starts."
	confirmSmall := "If the results are unclear, FaultLine will not guess."
	if confirmed {
		confirmStrong = "Confirmed: the bug first shows up between these two versions."
		confirmSmall = "Here, the same test reliably changes from working to broken."
	}
	grade := analysis.Get("grade", "value").Str()
	gradeSmall := "This grade shows how complete and repeatable the checks were."
	if grade == "A" {
		gradeSmall = "Strong: the important checks all passed."
	}
	num := func(v jsjson.Value) string { return jsjson.FormatNumber(v.Num()) }

	var b strings.Builder
	b.WriteString(incidentLiteral0)
	b.WriteString(EscapeHTML(analysis.Get("fixture", "title").Str()))
	b.WriteString(incidentLiteral2)
	b.WriteString(num(metrics.Get("sessions")))
	b.WriteString(incidentLiteral4)
	b.WriteString(num(metrics.Get("turns")))
	b.WriteString(incidentLiteral6)
	b.WriteString(num(metrics.Get("files")))
	b.WriteString(incidentLiteral8)
	b.WriteString(incidentGroupedInteger(metrics.Get("changedLines").Num()))
	b.WriteString(incidentLiteral10)
	b.WriteString(contributionRows.String())
	b.WriteString(incidentLiteral12)
	b.WriteString(timelineRows.String())
	b.WriteString(incidentLiteral14)
	b.WriteString(incidentVerdictBadge(prevention.Get("lastGood", "verdict").Str()))
	b.WriteString(incidentLiteral16)
	b.WriteString(EscapeHTML(lastGoodCaption))
	b.WriteString(incidentLiteral18)
	b.WriteString(incidentVerdictBadge(prevention.Get("firstBad", "verdict").Str()))
	b.WriteString(incidentLiteral20)
	b.WriteString(EscapeHTML(firstBadCaption))
	b.WriteString(incidentLiteral22)
	b.WriteString(confirmStrong)
	b.WriteString(incidentLiteral24)
	b.WriteString(confirmSmall)
	b.WriteString(incidentLiteral26)
	b.WriteString(num(minimization.Get("budget", "used")))
	b.WriteString(incidentLiteral28)
	b.WriteString(num(minimization.Get("budget", "max")))
	b.WriteString(incidentLiteral30)
	b.WriteString(attemptRows.String())
	b.WriteString(incidentLiteral32)
	b.WriteString(EscapeHTML(candidateLabel))
	b.WriteString(incidentLiteral34)
	b.WriteString(incidentVerdictBadge(minimization.Get("sufficiency", "verdict").Str()))
	b.WriteString(incidentLiteral36)
	b.WriteString(incidentVerdictBadge(minimization.Get("necessity", "verdict").Str()))
	b.WriteString(incidentLiteral38)
	b.WriteString(claimRows.String())
	b.WriteString(incidentLiteral40)
	b.WriteString(incidentVerdictBadge(prevention.Get("lastGood", "verdict").Str()))
	b.WriteString(incidentLiteral42)
	b.WriteString(incidentVerdictBadge(prevention.Get("firstBad", "verdict").Str()))
	b.WriteString(incidentLiteral44)
	b.WriteString(incidentVerdictBadge(prevention.Get("repaired", "verdict").Str()))
	b.WriteString(incidentLiteral46)
	b.WriteString(EscapeHTML(grade))
	b.WriteString(incidentLiteral48)
	b.WriteString(gradeSmall)
	b.WriteString(incidentLiteral50)
	return b.String()
}

// incidentInteraction configures the native-RERUN variant of the incident page.
type incidentInteraction struct {
	credential string
	enabled    bool
}

func incidentReplaceOnce(page, from, to string) string {
	if strings.Count(page, from) != 1 {
		panic("proofview: interactive incident literal count drift: " + from)
	}
	return strings.Replace(page, from, to, 1)
}

// renderInteractiveIncidentPage derives the nonce-bearing interactive page from the default
// page. RenderIncidentPage itself is unchanged.
func renderInteractiveIncidentPage(analysis jsjson.Value, in incidentInteraction) string {
	page := RenderIncidentPage(analysis)
	page = incidentReplaceOnce(page, "<script>", `<script nonce="`+EscapeHTML(in.credential)+`">`)
	if in.enabled {
		return incidentReplaceOnce(page, `fetch("/api/rerun",{method:"POST"})`,
			`fetch("/api/rerun",{method:"POST",headers:{"X-FaultLine-Native-RERUN":`+
				jsjson.Stringify(jsjson.MakeString(in.credential))+`}})`)
	}
	page = incidentReplaceOnce(page, `<button id="rerun" type="button">`, `<button id="rerun" type="button" disabled>`)
	page = incidentReplaceOnce(page, `<span class="action-status" id="action-status">uses the safe built-in example</span>`,
		`<span class="action-status" id="action-status">native RERUN requires startup --approve-native</span>`)
	const start = `const rerun=document.getElementById("rerun");`
	const end = "rerun.disabled=false}});\n"
	i := strings.Index(page, start)
	if i < 0 || strings.Count(page, start) != 1 {
		panic("proofview: interactive incident listener start drift")
	}
	j := strings.Index(page[i:], end)
	if j < 0 {
		panic("proofview: interactive incident listener end drift")
	}
	return page[:i] + page[i+j+len(end):]
}
