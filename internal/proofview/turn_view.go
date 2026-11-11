package proofview

import (
	"strings"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

const turnDashboardHead = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>FaultLine turn dashboard</title>
<style>body{margin:0;background:#fafaf9;color:#1c1917;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}main{max-width:1100px;margin:0 auto;padding:2rem 1.5rem}h1{font-size:2rem;margin:0 0 .5rem}h2{font-size:1.1rem;margin:2rem 0 .75rem}.status{font-family:ui-monospace,Menlo,monospace;font-weight:700}.proof{display:flex;gap:1rem;padding:.5rem 0;border-top:1px solid #e7e5e4}.proof>span:first-child{flex:0 0 12rem;color:#57534e}.proof>span:last-child{overflow-wrap:anywhere}table{border-collapse:collapse;width:100%}th,td{text-align:left;vertical-align:top;padding:.5rem;border-top:1px solid #e7e5e4;overflow-wrap:anywhere;font-size:.85rem}th{color:#57534e}.empty{color:#57534e}</style></head>
<body><main>
`

// turnText renders a scalar field as display text; absent values are empty.
func turnText(v jsjson.Value) string {
	switch v.Kind() {
	case jsjson.String:
		return v.Str()
	case jsjson.Number:
		return jsjson.FormatNumber(v.Num())
	case jsjson.Bool:
		if v.Bool() {
			return "true"
		}
		return "false"
	}
	return ""
}

func turnProofLine(label, value string) string {
	return `<div class="proof"><span>` + EscapeHTML(label) + `</span><span>` + EscapeHTML(value) + "</span></div>\n"
}

func turnSection(title, body string) string {
	return "<section>\n<h2>" + EscapeHTML(title) + "</h2>\n" + body + "</section>\n"
}

func turnTexts(v jsjson.Value) string {
	items := v.Items()
	parts := make([]string, len(items))
	for i, item := range items {
		parts[i] = turnText(item)
	}
	return strings.Join(parts, ", ")
}

func turnTable(headers []string, rows [][]string) string {
	var b strings.Builder
	b.WriteString("<table><thead><tr>")
	for _, h := range headers {
		b.WriteString("<th>" + EscapeHTML(h) + "</th>")
	}
	b.WriteString("</tr></thead>\n<tbody>\n")
	for _, row := range rows {
		b.WriteString("<tr>")
		for _, cell := range row {
			b.WriteString("<td>" + EscapeHTML(cell) + "</td>")
		}
		b.WriteString("</tr>\n")
	}
	b.WriteString("</tbody></table>\n")
	return b.String()
}

// RenderTurnDashboard renders an already verified turn investigation result.
func RenderTurnDashboard(investigation jsjson.Value) string {
	proof := investigation.Get("proof")
	result := "NOT PROOF"
	if proof.Get("isProof").Kind() == jsjson.Bool && proof.Get("isProof").Bool() {
		result = "PROOF"
	}
	var b strings.Builder
	b.WriteString(turnDashboardHead)
	b.WriteString("<h1>Turn dashboard</h1>\n")
	b.WriteString(`<p class="status">` + EscapeHTML(turnText(investigation.Get("status"))) + "</p>\n")
	b.WriteString(turnSection("Proof",
		turnProofLine("Result", result)+
			turnProofLine("Reason", turnText(proof.Get("reason")))+
			turnProofLine("Evidence grade", turnText(proof.Get("evidenceGrade")))+
			turnProofLine("Evidence label", turnText(proof.Get("evidenceLabel")))+
			turnProofLine("Environment homogeneity", turnText(investigation.Get("environmentHomogeneity")))))
	intro := investigation.Get("introduction")
	b.WriteString(turnSection("Introduction",
		turnProofLine("Status", turnText(intro.Get("status")))+
			turnProofLine("Reason", turnText(intro.Get("reason")))))

	errs := investigation.Get("errors").Items()
	if len(errs) == 0 {
		b.WriteString(turnSection("Errors", `<p class="empty">No errors</p>`+"\n"))
	} else {
		list := "<ul>\n"
		for _, e := range errs {
			list += "<li>" + EscapeHTML(turnText(e)) + "</li>\n"
		}
		b.WriteString(turnSection("Errors", list+"</ul>\n"))
	}

	states := investigation.Get("stableStates").Items()
	if len(states) == 0 {
		b.WriteString(turnSection("Stable states", `<p class="empty">No stable states</p>`+"\n"))
	} else {
		rows := make([][]string, len(states))
		for i, s := range states {
			rows[i] = []string{
				turnText(s.Get("turnId")), turnText(s.Get("turnOrdinal")), turnText(s.Get("verdict")),
				shortDigest(turnText(s.Get("treeDigest"))), shortDigest(turnText(s.Get("snapshotDigest"))),
				turnTexts(s.Get("executionIds")), turnTexts(s.Get("runIds")),
			}
		}
		b.WriteString(turnSection("Stable states", turnTable(
			[]string{"Turn", "Ordinal", "Verdict", "Tree digest", "Snapshot digest", "Executions", "Runs"}, rows)))
	}

	transitions := investigation.Get("transitions").Items()
	if len(transitions) == 0 {
		b.WriteString(turnSection("Transitions", `<p class="empty">No transitions</p>`+"\n"))
	} else {
		rows := make([][]string, len(transitions))
		for i, tr := range transitions {
			rows[i] = []string{
				turnText(tr.Get("kind")), turnText(tr.Get("before", "turnOrdinal")), turnText(tr.Get("after", "turnOrdinal")),
			}
		}
		b.WriteString(turnSection("Transitions", turnTable([]string{"Kind", "Before", "After"}, rows)))
	}

	recording := turnProofLine("Repository", turnText(investigation.Get("repository"))) +
		turnProofLine("Ledger digest", shortDigest(turnText(investigation.Get("ledgerDigest")))) +
		turnProofLine("Executions per state", turnText(investigation.Get("executionsPerState"))) +
		turnProofLine("Recorder", turnText(investigation.Get("recorder"))) +
		turnProofLine("Native Codex interception", turnText(investigation.Get("nativeCodexInterception")))
	mapping := investigation.Get("runtimeMapping")
	if mapping.Kind() == jsjson.Object && mapping.Obj().Len() > 0 {
		for _, key := range mapping.Obj().Keys() {
			recording += turnProofLine("Runtime mapping "+key, turnText(mapping.Get(key)))
		}
	} else {
		recording += turnProofLine("Runtime mapping", "none")
	}
	b.WriteString(turnSection("Recording", recording))
	b.WriteString("</main></body></html>\n")
	return b.String()
}

// RenderTurnVerificationFailure is the frozen failed-verification page.
func RenderTurnVerificationFailure(errors []string) string {
	return "<!doctype html><html><body><h1>Turn package failed verification</h1><pre>" +
		EscapeHTML(strings.Join(errors, "\n")) + "</pre></body></html>"
}

// RenderRawJSONPage renders the file at path as an escaped preformatted page.
func RenderRawJSONPage(path string) (string, error) {
	body, err := nodefs.ReadText(path)
	if err != nil {
		return "", err
	}
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>` + EscapeHTML(nodefs.Basename(path)) + `</title>
<style>body{margin:0;background:#111;color:#e7e5e4;font-family:ui-monospace,SFMono-Regular,Menlo,monospace}
pre{margin:0;padding:1.25rem;white-space:pre-wrap;word-break:break-word}</style></head>
<body><pre>` + EscapeHTML(body) + "</pre></body></html>", nil
}
