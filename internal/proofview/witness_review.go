package proofview

import (
	"encoding/base64"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodeproc"
)

// Port of the humanized witness review presentation
// (src/witness-review-presentation.ts) and the review page markup
// (src/witness-review-server.ts renderReviewPage/renderFailurePage). The text
// and HTML renderers consume the same private projection.

type humanizedOverlay struct {
	path       string
	utf8       bool
	byteLength int
	text       string
	base64     string
}

type rangeCommitLine struct{ short, subject, author, relativeAge string }

type humanizedRange struct {
	summary           string
	lines             []rangeCommitLine
	source            string
	hasSource         bool
	unavailableReason string
}

type witnessReviewPageState struct {
	Approved *jsjson.Value // approval record: approvedBy, approvalDigest
	Frozen   *jsjson.Value // frozen record: frozenDigest
	Notice   *string
	NowMs    *float64 // test clock; nil means the current time
}

type witnessReviewView struct {
	symptom, command, behavior string
	overlays                   []humanizedOverlay
	timeout                    string // "30s"
	rng                        humanizedRange
	evidence                   [][2]string // label, digest in display order
	lifecycle                  string
}

// humanizeOverlay decodes overlay bytes as UTF-8 only when decoding and
// re-encoding reproduces them and no NUL is present.
func humanizeOverlay(path, bytesBase64 string) humanizedOverlay {
	bytes, err := base64.StdEncoding.DecodeString(bytesBase64)
	if err != nil {
		bytes, _ = base64.RawStdEncoding.DecodeString(strings.TrimRight(bytesBase64, "="))
	}
	if utf8.Valid(bytes) && !strings.Contains(string(bytes), "\x00") {
		return humanizedOverlay{path: path, utf8: true, byteLength: len(bytes), text: string(bytes)}
	}
	return humanizedOverlay{path: path, byteLength: len(bytes), base64: bytesBase64}
}

// splitLines is split(/\r?\n/).
func splitLines(s string) []string {
	parts := strings.Split(s, "\n")
	for i := 0; i < len(parts)-1; i++ {
		parts[i] = strings.TrimSuffix(parts[i], "\r")
	}
	return parts
}

func shortRevision(value string) string {
	if isHex(value, 40) || isHex(value, 64) {
		return value[:12]
	}
	units := jsstr.ToUTF16(value)
	if len(units) > 24 {
		return jsstr.FromUTF16(units[:21]) + "…"
	}
	return value
}

func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// parseISODateMs is Date.parse for the ISO 8601 forms Git %aI and stored
// timestamps produce: YYYY-MM-DD, optionally THH:MM[:SS[.fff...]] with an
// optional Z or +/-HH:MM offset. A date-time without an offset is local time.
func parseISODateMs(s string) (float64, bool) {
	p := 0
	num := func(n int) (int, bool) {
		if p+n > len(s) {
			return 0, false
		}
		v := 0
		for i := 0; i < n; i++ {
			c := s[p+i]
			if c < '0' || c > '9' {
				return 0, false
			}
			v = v*10 + int(c-'0')
		}
		p += n
		return v, true
	}
	lit := func(c byte) bool {
		if p < len(s) && s[p] == c {
			p++
			return true
		}
		return false
	}
	year, ok := num(4)
	if !ok {
		return 0, false
	}
	month, day := 1, 1
	if lit('-') {
		if month, ok = num(2); !ok {
			return 0, false
		}
		if lit('-') {
			if day, ok = num(2); !ok {
				return 0, false
			}
		}
	}
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return 0, false
	}
	hour, minute, second, milli := 0, 0, 0, 0
	dateOnly := p == len(s)
	hasTime, hasOffset := false, false
	offsetMin := 0
	if !dateOnly {
		if !lit('T') {
			return 0, false
		}
		hasTime = true
		if hour, ok = num(2); !ok || !lit(':') {
			return 0, false
		}
		if minute, ok = num(2); !ok {
			return 0, false
		}
		if lit(':') {
			if second, ok = num(2); !ok {
				return 0, false
			}
			if lit('.') {
				start := p
				for p < len(s) && s[p] >= '0' && s[p] <= '9' {
					p++
				}
				if p == start {
					return 0, false
				}
				digits := (s[start:p] + "00")[:3]
				milli, _ = strconv.Atoi(digits)
			}
		}
		if lit('Z') {
			hasOffset = true
		} else if p < len(s) && (s[p] == '+' || s[p] == '-') {
			sign := 1
			if s[p] == '-' {
				sign = -1
			}
			p++
			oh, ok1 := num(2)
			if !ok1 || !lit(':') {
				return 0, false
			}
			om, ok2 := num(2)
			if !ok2 || oh > 23 || om > 59 {
				return 0, false
			}
			hasOffset = true
			offsetMin = sign * (oh*60 + om)
		}
		if p != len(s) {
			return 0, false
		}
		if hour > 24 || minute > 59 || second > 59 || (hour == 24 && (minute != 0 || second != 0 || milli != 0)) {
			return 0, false
		}
	}
	loc := time.UTC
	if hasTime && !hasOffset {
		loc = time.Local
	}
	t := time.Date(year, time.Month(month), day, hour, minute, second, 0, loc)
	ms := float64(t.Unix())*1000 + float64(milli) - float64(offsetMin)*60000
	return ms, true
}

func jsRound(x float64) float64 { return math.Floor(x + 0.5) }

type relativeUnit struct {
	divisor float64
	next    string
}

var relativeUnits = []relativeUnit{
	{60, "second"}, {60, "minute"}, {24, "hour"}, {7, "day"}, {4.34524, "week"}, {12, "month"}, {math.Inf(1), "year"},
}

func relativeAgeFromISO(iso string, nowMs float64) string {
	then, ok := parseISODateMs(iso)
	if !ok {
		return "unknown age"
	}
	seconds := jsRound((nowMs - then) / 1000)
	suffix := "ago"
	if seconds < 0 {
		suffix = "from now"
	}
	seconds = math.Abs(seconds)
	if seconds < 5 {
		return "just now"
	}
	value, unit := seconds, "second"
	for _, u := range relativeUnits {
		if value < u.divisor {
			break
		}
		value /= u.divisor
		unit = u.next
	}
	rounded := math.Max(1, jsRound(value))
	plural := "s"
	if rounded == 1 {
		plural = ""
	}
	return jsjson.FormatNumber(rounded) + " " + unit + plural + " " + suffix
}

// tryGit runs git with the frozen five-second timeout and 2 MiB buffer and
// returns the trimmed stdout, or false on any failure.
func tryGit(repository string, args ...string) (string, bool) {
	out, err := nodeproc.ExecFileSync("git", append([]string{"-C", repository}, args...),
		nodeproc.SpawnSyncOptions{Timeout: 5 * time.Second, MaxBuffer: 2 << 20})
	if err != nil {
		return "", false
	}
	return jsstr.Trim(jsstr.DecodeUTF8(out)), true
}

func nowMilliseconds() float64 { return float64(clock.Now().UnixMilli()) }

func humanizeRange(draft *jsjson.Value, nowMs float64) humanizedRange {
	if draft == nil {
		return humanizedRange{
			summary:           "No linked range",
			unavailableReason: "Standalone proposal — no incident draft range was supplied.",
		}
	}
	ancestor := draft.Get("range", "ancestor").Str()
	descendant := draft.Get("range", "descendant").Str()
	sourceValue := draft.Get("range", "source")
	hasSource := sourceValue.Kind() == jsjson.String
	source := sourceValue.Str()
	repository := draft.Get("repository").Str()
	rangeSpec := ancestor + ".." + descendant

	count, countOK := tryGit(repository, "rev-list", "--count", rangeSpec)
	nameOnly, nameOK := tryGit(repository, "diff-tree", "--no-commit-id", "--name-only", "-r", rangeSpec)
	log, logOK := tryGit(repository, "log", "--format=%H%x09%an%x09%aI%x09%s", "--no-decorate", rangeSpec)

	if !countOK || !logOK {
		return humanizedRange{
			summary: "Range " + shortRevision(ancestor) + ".." + shortRevision(descendant),
			lines: []rangeCommitLine{
				{shortRevision(ancestor), "(known good)", "—", "—"},
				{shortRevision(descendant), "(known bad)", "—", "—"},
			},
			source: source, hasSource: hasSource,
			unavailableReason: "Git metadata unavailable for this draft repository path; showing abbreviated endpoints only.",
		}
	}

	files := 0
	if nameOK && nameOnly != "" {
		seen := map[string]bool{}
		for _, line := range splitLines(nameOnly) {
			if line != "" {
				seen[line] = true
			}
		}
		files = len(seen)
	}
	var lines []rangeCommitLine
	for _, line := range splitLines(log) {
		if line == "" {
			continue
		}
		if len(lines) == 40 {
			break
		}
		parts := strings.Split(line, "\t")
		for len(parts) < 3 {
			parts = append(parts, "")
		}
		subject := strings.Join(parts[3:], "\t")
		if subject == "" {
			subject = "(no subject)"
		}
		lines = append(lines, rangeCommitLine{shortRevision(parts[0]), subject, parts[1], relativeAgeFromISO(parts[2], nowMs)})
	}
	commitCount := jsjson.ToNumber(count)
	var countText string
	if math.IsNaN(commitCount) || math.IsInf(commitCount, 0) {
		countText = strconv.Itoa(len(lines))
	} else {
		countText = jsjson.FormatNumber(commitCount)
	}
	return humanizedRange{
		summary: countText + " commits · " + strconv.Itoa(files) + " files",
		lines:   lines, source: source, hasSource: hasSource,
	}
}

func buildWitnessReviewView(review jsjson.Value, draft *jsjson.Value, state witnessReviewPageState) witnessReviewView {
	proposal := review.Get("proposal")
	witness := proposal.Get("witness")
	var overlays []humanizedOverlay
	for _, o := range witness.Get("overlays").Items() {
		overlays = append(overlays, humanizeOverlay(o.Get("path").Str(), o.Get("bytesBase64").Str()))
	}
	lifecycle := "PROPOSED → awaiting human approval"
	if state.Frozen != nil {
		lifecycle = "PROPOSED → APPROVED → FROZEN"
	} else if state.Approved != nil {
		lifecycle = "PROPOSED → APPROVED → awaiting freeze"
	}
	nowMs := nowMilliseconds()
	if state.NowMs != nil {
		nowMs = *state.NowMs
	}
	evidence := [][2]string{
		{"Proposal digest", proposal.Get("proposalDigest").Str()},
		{"Review digest", review.Get("reviewDigest").Str()},
		{"Command digest", witness.Get("commandDigest").Str()},
		{"Overlay digest", witness.Get("overlayDigest").Str()},
		{"Packet digest", proposal.Get("incidentPacketDigest").Str()},
	}
	if draft != nil {
		evidence = append(evidence, [2]string{"Draft digest", draft.Get("draftDigest").Str()})
	}
	if state.Approved != nil {
		evidence = append(evidence, [2]string{"Approval digest", state.Approved.Get("approvalDigest").Str()})
	}
	if state.Frozen != nil {
		evidence = append(evidence, [2]string{"Frozen digest", state.Frozen.Get("frozenDigest").Str()})
	}
	return witnessReviewView{
		symptom:   proposal.Get("incidentPacket", "symptom").Str(),
		command:   witness.Get("command").Str(),
		behavior:  witness.Get("behavior").Str(),
		overlays:  overlays,
		timeout:   jsjson.FormatNumber(witness.Get("policy", "timeoutSeconds").Num()) + "s",
		rng:       humanizeRange(draft, nowMs),
		evidence:  evidence,
		lifecycle: lifecycle,
	}
}

func (v witnessReviewView) cageLine() string { return "Docker · network OFF · timeout " + v.timeout }

// RenderWitnessReviewText is the TTY presentation (first 40 rows per overlay).
func RenderWitnessReviewText(review jsjson.Value, draft *jsjson.Value) string {
	return renderWitnessReviewText(review, draft, witnessReviewPageState{})
}

func renderWitnessReviewText(review jsjson.Value, draft *jsjson.Value, state witnessReviewPageState) string {
	view := buildWitnessReviewView(review, draft, state)
	lines := []string{
		"FaultLine witness review (local, read-only until you approve)",
		"Lifecycle: " + view.lifecycle,
		"Cage: " + view.cageLine(),
		"",
		"Symptom: " + view.symptom,
		"",
		"Command (not executed):",
		view.command,
		"",
		"Behavior: " + view.behavior,
		"",
		"Range: " + view.rng.summary,
	}
	if view.rng.hasSource {
		lines = append(lines, "Source: "+view.rng.source)
	}
	if view.rng.unavailableReason != "" {
		lines = append(lines, view.rng.unavailableReason)
	}
	for _, c := range view.rng.lines {
		lines = append(lines, "  "+c.short+"  "+c.subject+"  · "+c.author+"  · "+c.relativeAge)
	}
	lines = append(lines, "", "Overlays ("+strconv.Itoa(len(view.overlays))+"):")
	if len(view.overlays) == 0 {
		lines = append(lines, "  (none)")
	}
	for _, o := range view.overlays {
		if o.utf8 {
			lines = append(lines, "  "+o.path+" · UTF-8 · "+strconv.Itoa(o.byteLength)+" bytes")
			rows := splitLines(o.text)
			if len(rows) > 40 {
				rows = rows[:40]
			}
			for _, row := range rows {
				lines = append(lines, "    | "+row)
			}
		} else {
			lines = append(lines, "  "+o.path+" · binary · "+strconv.Itoa(o.byteLength)+" bytes (base64 below)", "    "+o.base64)
		}
	}
	lines = append(lines, "", "Evidence record (digests — expand only if needed):")
	labels := map[string]string{
		"Proposal digest": "  proposal: ", "Review digest": "  review:   ", "Command digest": "  command:  ",
		"Overlay digest": "  overlay:  ", "Packet digest": "  packet:   ", "Draft digest": "  draft:    ",
		"Approval digest": "  approval: ", "Frozen digest": "  frozen:   ",
	}
	for _, e := range view.evidence {
		lines = append(lines, labels[e[0]]+e[1])
	}
	return strings.Join(lines, "\n") + "\n"
}

// RenderWitnessReviewHTML renders the pending review page (no approval or
// freeze record yet) with the supplied form credential.
func RenderWitnessReviewHTML(review jsjson.Value, draft *jsjson.Value, credential string, twoStep bool) string {
	return renderWitnessReviewPage(review, draft, credential, twoStep, witnessReviewPageState{})
}

func renderWitnessReviewPage(review jsjson.Value, draft *jsjson.Value, token string, twoStep bool, state witnessReviewPageState) string {
	view := buildWitnessReviewView(review, draft, state)
	e := EscapeHTML
	noticeHTML := ""
	if state.Notice != nil {
		noticeHTML = `<p class="review-notice" role="status">` + e(*state.Notice) + `</p>`
	}
	reviewDigest := review.Get("reviewDigest").Str()
	var confirm string
	if twoStep {
		confirm = approvalFormHTML(state, token, reviewDigest) + freezeFormHTML(state, token, reviewDigest)
	} else {
		confirm = atomicFormHTML(state, token, reviewDigest)
	}
	flow := "approve &amp; freeze in one action"
	if twoStep {
		flow = "approve and freeze in two steps"
	}
	reviewState := "PENDING"
	if state.Frozen != nil {
		reviewState = "FROZEN"
	}
	notice := "Local review workbench: exact command and overlay content are shown only on 127.0.0.1. FaultLine will not execute or materialize them from this page."
	var b strings.Builder
	b.WriteString(renderIncidentPageDocumentStart("local witness review"))
	b.WriteString("\n")
	b.WriteString(renderIncidentPageProductChrome(chromeOptions{command: "fl witness review", notice: &notice}))
	b.WriteString("\n")
	b.WriteString(`<section class="hero"><div><div class="eyebrow">FAULTLINE · HUMAN WITNESS REVIEW</div><h1>Freeze the <em>question</em><br>only after review.</h1><p class="lede">Inspect the exact immutable proposal, then ` + flow + `. This page never runs the displayed command.</p>` + noticeHTML + `<p class="cage-badges" aria-label="Execution cage">` + e(view.cageLine()) + `</p></div><aside class="card metric-card" aria-label="Witness review summary"><div class="metric"><b>` + strconv.Itoa(len(view.overlays)) + `</b><small>overlays</small></div><div class="metric"><b>` + e(view.timeout) + `</b><small>timeout</small></div><div class="metric"><b>OFF</b><small>network</small></div><div class="metric"><b>` + reviewState + `</b><small>review state</small></div></aside></section>
<section class="section" id="break"><div class="section-head"><span class="number">01</span><h2>REVIEW</h2></div><div class="grid"><article class="card panel witness"><h3>Strictly blinded incident packet</h3><p><strong>` + e(view.symptom) + `</strong></p><div class="proof"><span>Lifecycle</span><small>` + e(view.lifecycle) + `</small></div><small>Raw CI-log content is intentionally not displayed in this browser surface.</small></article>` + rangePanelHTML(view.rng) + statusPanelHTML(state) + `</div></section>
<section class="section" id="find"><div class="section-head"><span class="number">02</span><h2>EXACT WITNESS</h2></div><div class="grid"><article class="card panel"><h3>Command — not executed</h3><pre>` + e(view.command) + `</pre><p><strong>` + e(view.behavior) + `</strong></p></article><article class="card panel"><h3>Execution cage</h3><p>` + e(view.cageLine()) + `</p><div class="proof"><span>Credentials</span><small>redacted</small></div></article></div></section>
<section class="section" id="prove"><div class="section-head"><span class="number">03</span><h2>OVERLAYS</h2></div><article class="card panel"><h3>Reviewed overlay content — not materialized</h3><p>UTF-8 overlays are shown decoded. Binary overlays keep canonical base64.</p>` + overlayRowsHTML(view.overlays) + `</article></section>
<section class="section" id="fix"><div class="section-head"><span class="number">04</span><h2>CONFIRM</h2></div><div class="grid">` + evidenceDetailsHTML(view) + confirm + `</div></section>
<section class="section" id="prevent"><div class="section-head"><span class="number">05</span><h2>PROOF BOUNDARY</h2></div><article class="card panel"><h3>What this does not establish</h3><p>A local approval/freeze is not a reproduction, Docker execution, first-bad-state result, repair, or prevention claim.</p></article></section>
`)
	b.WriteString(renderIncidentPageDocumentEnd("FaultLine records a human-reviewed witness without executing it. Proof claims require a separately completed Docker-isolated investigation and a verified portable bundle.", nil))
	return b.String()
}

func statusPanelHTML(state witnessReviewPageState) string {
	switch {
	case state.Frozen != nil:
		by := "reviewer"
		if state.Approved != nil {
			by = state.Approved.Get("approvedBy").Str()
		}
		return `<article class="card panel"><h3>Freeze record</h3><p><strong>FROZEN</strong> by ` + EscapeHTML(by) + `.</p><p><small>Open Evidence record below for digests. No server action reruns the witness.</small></p></article>`
	case state.Approved != nil:
		return `<article class="card panel"><h3>Human approval</h3><p><strong>APPROVED</strong> by ` + EscapeHTML(state.Approved.Get("approvedBy").Str()) + `.</p><small>The proposal is not frozen yet. Freezing requires one more explicit action below.</small></article>`
	}
	return `<article class="card panel"><h3>Human approval</h3><p class="empty-state"><strong>NOT APPROVED</strong><br>Review the exact command, overlays, and cage before recording an approval.</p></article>`
}

func rangePanelHTML(r humanizedRange) string {
	var lines string
	if len(r.lines) == 0 {
		reason := r.unavailableReason
		if reason == "" {
			reason = "No commits to show."
		}
		lines = `<p class="empty-state">` + EscapeHTML(reason) + `</p>`
	} else {
		var b strings.Builder
		b.WriteString(`<ul class="range-log">`)
		for _, l := range r.lines {
			b.WriteString(`<li><code>` + EscapeHTML(l.short) + `</code> <strong>` + EscapeHTML(l.subject) + `</strong> <span>` + EscapeHTML(l.author) + `</span> <em>` + EscapeHTML(l.relativeAge) + `</em></li>`)
		}
		b.WriteString(`</ul>`)
		lines = b.String()
	}
	source := ""
	if r.hasSource {
		source = `<p><small>Source: ` + EscapeHTML(r.source) + `</small></p>`
	}
	reason := ""
	if r.unavailableReason != "" {
		reason = `<p><small>` + EscapeHTML(r.unavailableReason) + `</small></p>`
	}
	return `<article class="card panel"><h3>Range · ` + EscapeHTML(r.summary) + `</h3>` + source + reason + lines + `</article>`
}

func evidenceDetailsHTML(v witnessReviewView) string {
	var b strings.Builder
	b.WriteString(`<details class="card panel evidence-record"><summary>Evidence record</summary><p><small>Digests are collapsed here so the review surface stays human-readable. Expand only when you need to retain or compare hashes.</small></p>`)
	for _, row := range v.evidence {
		b.WriteString(`<div class="proof"><span>` + EscapeHTML(row[0]) + `</span><code>` + EscapeHTML(row[1]) + `</code></div>`)
	}
	b.WriteString(`</details>`)
	return b.String()
}

func overlayRowsHTML(overlays []humanizedOverlay) string {
	if len(overlays) == 0 {
		return `<p class="empty-state"><strong>No overlays</strong><br>The proposed command is reviewed without generated overlay bytes.</p>`
	}
	var b strings.Builder
	for _, o := range overlays {
		if o.utf8 {
			b.WriteString(`<details open><summary><code>` + EscapeHTML(o.path) + `</code> · UTF-8 · ` + strconv.Itoa(o.byteLength) + ` bytes</summary><pre>` + EscapeHTML(o.text) + `</pre></details>`)
		} else {
			b.WriteString(`<details><summary><code>` + EscapeHTML(o.path) + `</code> · binary · ` + strconv.Itoa(o.byteLength) + ` bytes</summary><p><small>Base64 (binary content)</small></p><pre>` + EscapeHTML(o.base64) + `</pre></details>`)
		}
	}
	return b.String()
}

func hiddenFields(token, reviewDigest string) string {
	return `<input type="hidden" name="token" value="` + EscapeHTML(token) + `" /><input type="hidden" name="reviewDigest" value="` + EscapeHTML(reviewDigest) + `" />`
}

const approvalFields = `<label>Reviewer identity<input name="approvedBy" required maxlength="512" autocomplete="name" /></label><label>Optional review note<textarea name="note" maxlength="8000" rows="3"></textarea></label><label class="acknowledgement"><input type="checkbox" name="acknowledged" value="exact-witness-reviewed" required />I reviewed the exact command and every visible overlay.</label>`

func atomicFormHTML(state witnessReviewPageState, token, reviewDigest string) string {
	if state.Approved != nil || state.Frozen != nil {
		return ""
	}
	return `<form class="card panel review-form" method="post" action="/approve-freeze"><h3>Approve &amp; freeze</h3><p>One action writes both the human approval and the write-once freeze record. It does not run the command.</p>` + hiddenFields(token, reviewDigest) + approvalFields + `<button type="submit">Approve &amp; freeze this witness</button></form>`
}

func approvalFormHTML(state witnessReviewPageState, token, reviewDigest string) string {
	if state.Approved != nil || state.Frozen != nil {
		return ""
	}
	return `<form class="card panel review-form" method="post" action="/approve"><h3>Approve reviewed witness</h3><p>Two-step mode: approval first, freeze second.</p>` + hiddenFields(token, reviewDigest) + approvalFields + `<button type="submit">Approve this reviewed witness</button></form>`
}

func freezeFormHTML(state witnessReviewPageState, token, reviewDigest string) string {
	if state.Approved == nil || state.Frozen != nil {
		return ""
	}
	return `<form class="card panel review-form" method="post" action="/freeze"><h3>Freeze approved witness</h3><p>Freezing snapshots the already-approved proposal as a write-once record.</p>` + hiddenFields(token, reviewDigest) + `<button type="submit">Freeze this approved witness</button></form>`
}

// renderWitnessReviewFailurePage is the 403/409 refusal page.
func renderWitnessReviewFailurePage(message string) string {
	notice := "The local review server refused to present a proposal that failed its blinded-protocol or integrity checks."
	return renderIncidentPageDocumentStart("witness review unavailable") + "\n" +
		renderIncidentPageProductChrome(chromeOptions{command: "fl witness review", notice: &notice}) + "\n" +
		`<section class="hero"><div><div class="eyebrow">FAULTLINE · REVIEW REFUSED</div><h1>Do not freeze<br><em>this proposal.</em></h1><p class="lede">` + EscapeHTML(message) + `</p></div></section>` + "\n" +
		renderIncidentPageDocumentEnd("Create a fresh, strictly blinded proposal after resolving the integrity problem. FaultLine did not execute, approve, or freeze anything.", nil)
}

// ReadGitUserName is `git config --get user.name` in the current directory,
// or local-reviewer when unavailable or blank.
func ReadGitUserName() string {
	out, err := nodeproc.ExecFileSync("git", []string{"config", "--get", "user.name"},
		nodeproc.SpawnSyncOptions{Timeout: 5 * time.Second, MaxBuffer: 2 << 20})
	if err == nil {
		if name := jsstr.Trim(jsstr.DecodeUTF8(out)); name != "" {
			return name
		}
	}
	return "local-reviewer"
}
