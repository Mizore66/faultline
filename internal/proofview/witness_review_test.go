package proofview

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

const reviewDigestN = "sha256:"

func hexDigest(c string) string { return reviewDigestN + strings.Repeat(c, 64) }

// witnessReviewFixture is the review snapshot the hand-written goldens in
// difftest/testdata/witness-review describe. Overlay bytes: bin/blob.dat is
// 00 01 02 ff (NUL, so binary); src/a&b.js is 41 lines (325 bytes).
func witnessReviewFixture(t *testing.T) jsjson.Value {
	t.Helper()
	const utf8Overlay = "bGluZSAxIDxiPiYiJwpsaW5lIDIKbGluZSAzCmxpbmUgNApsaW5lIDUKbGluZSA2CmxpbmUgNwpsaW5lIDgKbGluZSA5CmxpbmUgMTAKbGluZSAxMQpsaW5lIDEyCmxpbmUgMTMKbGluZSAxNApsaW5lIDE1CmxpbmUgMTYKbGluZSAxNwpsaW5lIDE4CmxpbmUgMTkKbGluZSAyMApsaW5lIDIxCmxpbmUgMjIKbGluZSAyMwpsaW5lIDI0CmxpbmUgMjUKbGluZSAyNgpsaW5lIDI3CmxpbmUgMjgKbGluZSAyOQpsaW5lIDMwCmxpbmUgMzEKbGluZSAzMgpsaW5lIDMzCmxpbmUgMzQKbGluZSAzNQpsaW5lIDM2CmxpbmUgMzcKbGluZSAzOApsaW5lIDM5CmxpbmUgNDAKbGluZSA0MQ=="
	return parseReviewJSON(t, `{"schemaVersion":"faultline.witness-review.v1","mode":"LOCAL_READ_ONLY","reviewDigest":"`+hexDigest("2")+`",
"proposal":{"proposalId":"p1","proposalDigest":"`+hexDigest("1")+`","incidentPacketDigest":"`+hexDigest("5")+`",
"incidentPacket":{"symptom":"Refund <b>\"EUR\"</b> & 'USD'","ciLog":"raw-ci-log-secret","repositoryLanguage":"Go","repositorySummary":"s"},
"witness":{"behavior":"Prints <EUR> not 'USD'","command":"node w.mjs --x=\"<y>\" && echo 'z'","commandDigest":"`+hexDigest("3")+`","overlayDigest":"`+hexDigest("4")+`",
"overlays":[{"path":"bin/blob.dat","bytesBase64":"AAEC/w=="},{"path":"src/a&b.js","bytesBase64":"`+utf8Overlay+`"}],
"policy":{"network":"disabled","credentials":"redacted","timeoutSeconds":30}}}}`)
}

func parseReviewJSON(t *testing.T, text string) jsjson.Value {
	t.Helper()
	v, err := jsjson.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func readGolden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "difftest", "testdata", "witness-review", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestWitnessReviewText(t *testing.T) {
	review := witnessReviewFixture(t)
	text := RenderWitnessReviewText(review, nil)
	if strings.Contains(text, "raw-ci-log-secret") {
		t.Fatal("unexpected raw CI log")
	}
	if !strings.Contains(text, "line 40") || strings.Contains(text, "line 41") {
		t.Fatal("TTY overlay truncation differs")
	}
	if want := readGolden(t, "tty.txt"); text != want {
		t.Fatalf("TTY text differs from hand-written golden\n got: %q\nwant: %q", text, want)
	}
}

func TestWitnessReviewHTML(t *testing.T) {
	review := witnessReviewFixture(t)
	for _, tc := range []struct {
		golden  string
		twoStep bool
		action  string
		absent  []string
	}{
		{"atomic.html", false, `action="/approve-freeze"`, []string{`action="/approve"`, `action="/freeze"`}},
		{"two-step.html", true, `action="/approve"`, []string{`action="/approve-freeze"`, `action="/freeze"`}},
	} {
		html := RenderWitnessReviewHTML(review, nil, "test_nonce-ABC123", tc.twoStep)
		if strings.Contains(html, "raw-ci-log-secret") || strings.Contains(html, "<script>") {
			t.Fatalf("%s: unsafe HTML", tc.golden)
		}
		if !strings.Contains(html, tc.action) {
			t.Fatalf("%s: wrong form", tc.golden)
		}
		for _, a := range tc.absent {
			if strings.Contains(html, a) {
				t.Fatalf("%s: unexpected %s", tc.golden, a)
			}
		}
		if want := readGolden(t, tc.golden); html != want {
			t.Fatalf("%s differs from hand-written golden (len got %d want %d)", tc.golden, len(html), len(want))
		}
	}
}

func TestWitnessReviewHTMLStates(t *testing.T) {
	review := witnessReviewFixture(t)
	approved := parseReviewJSON(t, `{"approvedBy":"Rev <i>","approvalDigest":"`+hexDigest("6")+`"}`)
	frozen := parseReviewJSON(t, `{"frozenDigest":"`+hexDigest("7")+`"}`)
	notice := "Done & <dusted>"

	// Approved, not frozen: two-step shows only the freeze form.
	html := renderWitnessReviewPage(review, nil, "tok", true, witnessReviewPageState{Approved: &approved, Notice: &notice})
	for _, want := range []string{
		`<p class="review-notice" role="status">Done &amp; &lt;dusted&gt;</p>`,
		`<p><strong>APPROVED</strong> by Rev &lt;i&gt;.</p><small>The proposal is not frozen yet. Freezing requires one more explicit action below.</small>`,
		`<div class="proof"><span>Lifecycle</span><small>PROPOSED → APPROVED → awaiting freeze</small></div>`,
		`<div class="proof"><span>Approval digest</span><code>` + hexDigest("6") + `</code></div>`,
		`method="post" action="/freeze"><h3>Freeze approved witness</h3>`,
		`<b>PENDING</b>`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("approved page lacks %q", want)
		}
	}
	if strings.Contains(html, `action="/approve"`) || strings.Contains(html, `action="/approve-freeze"`) {
		t.Fatal("approved page must not offer approval")
	}

	// Frozen: no forms at all.
	html = renderWitnessReviewPage(review, nil, "tok", false, witnessReviewPageState{Approved: &approved, Frozen: &frozen})
	for _, want := range []string{
		`<h3>Freeze record</h3><p><strong>FROZEN</strong> by Rev &lt;i&gt;.</p><p><small>Open Evidence record below for digests. No server action reruns the witness.</small></p>`,
		`PROPOSED → APPROVED → FROZEN`,
		`<span>Frozen digest</span><code>` + hexDigest("7") + `</code>`,
		`<b>FROZEN</b>`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("frozen page lacks %q", want)
		}
	}
	if strings.Contains(html, "<form") {
		t.Fatal("frozen page must not contain a form")
	}
	text := renderWitnessReviewText(review, nil, witnessReviewPageState{Approved: &approved, Frozen: &frozen})
	if !strings.Contains(text, "Lifecycle: PROPOSED → APPROVED → FROZEN\n") ||
		!strings.HasSuffix(text, "  approval: "+hexDigest("6")+"\n  frozen:   "+hexDigest("7")+"\n") {
		t.Fatalf("text state differs: %q", text)
	}
}

func TestWitnessReviewCRLFAndNoOverlays(t *testing.T) {
	// command "a\r\nb"; overlay "one\r\ntwo\r\n" (YQ0K... below) and an empty proposal overlay list.
	review := parseReviewJSON(t, `{"reviewDigest":"`+hexDigest("2")+`","proposal":{"proposalDigest":"`+hexDigest("1")+`","incidentPacketDigest":"`+hexDigest("5")+`",
"incidentPacket":{"symptom":"s"},"witness":{"behavior":"b","command":"a\r\nb","commandDigest":"`+hexDigest("3")+`","overlayDigest":"`+hexDigest("4")+`",
"overlays":[{"path":"c.txt","bytesBase64":"b25lDQp0d28NCg=="}],"policy":{"timeoutSeconds":5}}}}`)
	text := RenderWitnessReviewText(review, nil)
	if !strings.Contains(text, "Command (not executed):\na\r\nb\n\nBehavior: b\n") {
		t.Fatalf("command CRLF bytes not preserved: %q", text)
	}
	if !strings.Contains(text, "  c.txt · UTF-8 · 10 bytes\n    | one\n    | two\n    | \n\nEvidence record") {
		t.Fatalf("CRLF overlay rows differ: %q", text)
	}
	html := RenderWitnessReviewHTML(review, nil, "t", false)
	if !strings.Contains(html, "<pre>one\r\ntwo\r\n</pre>") || !strings.Contains(html, "<pre>a\r\nb</pre>") {
		t.Fatal("CRLF bytes not preserved in HTML")
	}

	empty := parseReviewJSON(t, `{"reviewDigest":"`+hexDigest("2")+`","proposal":{"proposalDigest":"`+hexDigest("1")+`","incidentPacketDigest":"`+hexDigest("5")+`",
"incidentPacket":{"symptom":"s"},"witness":{"behavior":"b","command":"c","commandDigest":"`+hexDigest("3")+`","overlayDigest":"`+hexDigest("4")+`",
"overlays":[],"policy":{"timeoutSeconds":5}}}}`)
	if text := RenderWitnessReviewText(empty, nil); !strings.Contains(text, "Overlays (0):\n  (none)\n\nEvidence record") {
		t.Fatalf("empty overlay text differs: %q", text)
	}
	if html := RenderWitnessReviewHTML(empty, nil, "t", false); !strings.Contains(html, `<p class="empty-state"><strong>No overlays</strong><br>The proposed command is reviewed without generated overlay bytes.</p>`) {
		t.Fatal("empty overlay HTML differs")
	}
}

func TestWitnessReviewOverlayKinds(t *testing.T) {
	cases := []struct {
		name, b64, text string
		utf8            bool
		length          int
	}{
		{"ascii", "aGk=", "hi", true, 2},
		{"empty", "", "", true, 0},
		{"multibyte", "w6k=", "é", true, 2},
		{"bom kept", "77u/YQ==", "\ufeffa", true, 4},
		{"nul", "YQBi", "", false, 3},
		{"invalid utf8", "/w==", "", false, 1},
		{"truncated sequence", "ww==", "", false, 1},
		{"surrogate encoding", "7aCA", "", false, 3},
		{"replacement char literal", "77+9", "\ufffd", true, 3},
	}
	for _, tc := range cases {
		o := humanizeOverlay("p", tc.b64)
		if o.utf8 != tc.utf8 || o.byteLength != tc.length || (tc.utf8 && o.text != tc.text) || (!tc.utf8 && o.base64 != tc.b64) {
			t.Fatalf("%s: got %+v", tc.name, o)
		}
	}
}

func TestWitnessReviewRelativeAge(t *testing.T) {
	// Expected strings were produced by evaluating the frozen src
	// relativeAgeFromIso arithmetic under Node with now=2026-06-15T12:00:00.000Z
	// (including its divisor/unit labeling), not by this implementation.
	now := mustDateMs(t, "2026-06-15T12:00:00.000Z")
	for iso, want := range map[string]string{
		"2026-06-15T11:59:58Z":           "just now",
		"2026-06-15T11:59:55Z":           "5 seconds ago",
		"2026-06-15T11:59:00Z":           "1 second ago",
		"2026-06-15T11:58:00Z":           "2 seconds ago",
		"2026-06-15T11:00:00Z":           "1 minute ago",
		"2026-06-15T09:00:00Z":           "3 minutes ago",
		"2026-06-14T12:00:00Z":           "1 hour ago",
		"2026-06-08T12:00:00Z":           "1 day ago",
		"2026-05-15T12:00:00Z":           "1 week ago",
		"2025-06-15T12:00:00Z":           "12 weeks ago",
		"2027-06-15T12:00:00Z":           "12 weeks from now",
		"2026-06-15T12:00:30Z":           "30 seconds from now",
		"2026-06-15T12:00:02.5Z":         "just now",
		"2026-06-15T12:00:07.5Z":         "7 seconds from now",
		"2026-06-15T11:59:52.5Z":         "8 seconds ago",
		"2026-06-15T19:59:55.250+08:00":  "5 seconds ago",
		"2026-06-15T06:30:00.125-05:30":  "just now",
		"2026-06-15T11:00:00+08:00":      "9 minutes ago",
		"2026-06-15T03:00:00.000+00:00":  "9 minutes ago",
		"2026-06-15T20:00:00.9+08:00":    "just now",
		"2026-06-15T11:59:00-00:00":      "1 second ago",
		"2026-06-15T11:00:00.123456789Z": "1 minute ago",
		"2026-06-15T11:59:30+00:00":      "30 seconds ago",
		"2026-06-15T19:59:30+08:00":      "30 seconds ago",
		"2026-06-15T11:00:00.999Z":       "60 seconds ago",
		"2026-06-15T11:59:50.4999Z":      "10 seconds ago",
		"2026-06-15T11:59:59.999+00:00":  "just now",
		"not a date":                     "unknown age",
		"":                               "unknown age",
		"2026-06-15T11:00:00.Z":          "unknown age",
		"2026-06-15T11:00:60Z":           "unknown age",
		"2026-13-01T00:00:00Z":           "unknown age",
	} {
		if got := relativeAgeFromISO(iso, now); got != want {
			t.Errorf("relativeAgeFromISO(%q) = %q, want %q", iso, got, want)
		}
	}
}

func mustDateMs(t *testing.T, iso string) float64 {
	t.Helper()
	ms, ok := parseISODateMs(iso)
	if !ok {
		t.Fatalf("unparseable %s", iso)
	}
	return ms
}

func TestWitnessReviewShortRevision(t *testing.T) {
	for in, want := range map[string]string{
		strings.Repeat("a", 40): strings.Repeat("a", 12),
		strings.Repeat("A", 64): strings.Repeat("A", 12),
		"main":                  "main",
		strings.Repeat("g", 24): strings.Repeat("g", 24),
		strings.Repeat("g", 25): strings.Repeat("g", 21) + "…",
		strings.Repeat("a", 41): strings.Repeat("a", 21) + "…",
		"":                      "",
	} {
		if got := shortRevision(in); got != want {
			t.Errorf("shortRevision(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWitnessReviewSplitLines(t *testing.T) {
	for in, want := range map[string][]string{
		"":           {""},
		"a":          {"a"},
		"a\nb":       {"a", "b"},
		"a\r\nb\r\n": {"a", "b", ""},
		"a\rb":       {"a\rb"},
		"a\r":        {"a\r"},
		"\r\n\r\n":   {"", "", ""},
	} {
		if got := splitLines(in); !reflect.DeepEqual(got, want) {
			t.Errorf("splitLines(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---- Git-backed range display -------------------------------------------

func gitIn(t *testing.T, dir, date string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Ann Ö", "-c", "user.email=a@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "2026-06-15T10:00:00+0000", "init", "-q", ".")
	gitIn(t, dir, "2026-06-15T10:00:00+0000", "commit", "-q", "--allow-empty", "-m", "base")
	return dir
}

func draftFor(t *testing.T, repo, ancestor, descendant string) jsjson.Value {
	t.Helper()
	b := new(strings.Builder)
	fmt.Fprintf(b, `{"repository":%s,"draftDigest":"%s","range":{"ancestor":%s,"descendant":%s,"source":"EXPLICIT"}}`,
		jsjson.Quote(repo), hexDigest("8"), jsjson.Quote(ancestor), jsjson.Quote(descendant))
	return parseReviewJSON(t, b.String())
}

func TestWitnessReviewRange(t *testing.T) {
	now := mustDateMs(t, "2026-06-15T12:00:00.000Z")

	t.Run("no draft", func(t *testing.T) {
		got := humanizeRange(nil, now)
		want := humanizedRange{summary: "No linked range", unavailableReason: "Standalone proposal — no incident draft range was supplied."}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("zero commits", func(t *testing.T) {
		repo := newRepo(t)
		head := gitIn(t, repo, "2026-06-15T10:00:00+0000", "rev-parse", "HEAD")
		d := draftFor(t, repo, head, head)
		got := humanizeRange(&d, now)
		if got.summary != "0 commits · 0 files" || len(got.lines) != 0 || got.source != "EXPLICIT" || got.hasSource != true || got.unavailableReason != "" {
			t.Fatalf("got %+v", got)
		}
		html := renderWitnessReviewPage(witnessReviewFixture(t), &d, "t", false, witnessReviewPageState{NowMs: &now})
		if !strings.Contains(html, `<h3>Range · 0 commits · 0 files</h3><p><small>Source: EXPLICIT</small></p><p class="empty-state">No commits to show.</p>`) {
			t.Fatal("zero-commit range panel differs")
		}
		if !strings.Contains(html, `<span>Draft digest</span><code>`+hexDigest("8")+`</code>`) {
			t.Fatal("draft digest row missing")
		}
	})

	t.Run("one commit with numeric offset", func(t *testing.T) {
		repo := newRepo(t)
		base := gitIn(t, repo, "2026-06-15T10:00:00+0000", "rev-parse", "HEAD")
		if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		gitIn(t, repo, "2026-06-15T16:30:00+0530", "add", "f.txt")
		gitIn(t, repo, "2026-06-15T16:30:00+0530", "commit", "-q", "-m", "fix <a>\ttabbed")
		head := gitIn(t, repo, "2026-06-15T10:00:00+0000", "rev-parse", "HEAD")
		d := draftFor(t, repo, base, head)
		got := humanizeRange(&d, now)
		want := humanizedRange{
			summary: "1 commits · 1 files", source: "EXPLICIT", hasSource: true,
			lines: []rangeCommitLine{{short: head[:12], subject: "fix <a>\ttabbed", author: "Ann Ö", relativeAge: "1 minute ago"}},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v\nwant %+v", got, want)
		}
		html := renderWitnessReviewPage(witnessReviewFixture(t), &d, "t", false, witnessReviewPageState{NowMs: &now})
		wantLi := `<ul class="range-log"><li><code>` + head[:12] + `</code> <strong>fix &lt;a&gt;` + "\t" + `tabbed</strong> <span>Ann Ö</span> <em>1 minute ago</em></li></ul>`
		if !strings.Contains(html, wantLi) || !strings.Contains(html, `<h3>Range · 1 commits · 1 files</h3>`) {
			t.Fatal("one-commit range panel differs")
		}
		text := renderWitnessReviewText(witnessReviewFixture(t), &d, witnessReviewPageState{NowMs: &now})
		wantRow := "Range: 1 commits · 1 files\nSource: EXPLICIT\n  " + head[:12] + "  fix <a>\ttabbed  · Ann Ö  · 1 minute ago\n\nOverlays (2):"
		if !strings.Contains(text, wantRow) {
			t.Fatalf("one-commit text differs: %q", text)
		}
	})

	t.Run("forty one commits shows forty", func(t *testing.T) {
		repo := newRepo(t)
		base := gitIn(t, repo, "2026-06-15T10:00:00+0000", "rev-parse", "HEAD")
		var stream strings.Builder
		for i := 1; i <= 41; i++ {
			parent := "from " + base
			if i > 1 {
				parent = fmt.Sprintf("from :%d", i-1)
			}
			msg := fmt.Sprintf("c%d", i)
			fmt.Fprintf(&stream, "commit refs/heads/main\nmark :%d\ncommitter Ann <a@example.com> 1781521200 +0000\ndata %d\n%s\n%s\nM 100644 inline f%d.txt\ndata 1\nx\n\n", i, len(msg), msg, parent, i)
		}
		fi := exec.Command("git", "-C", repo, "fast-import", "--quiet")
		fi.Stdin = strings.NewReader(stream.String())
		if out, err := fi.CombinedOutput(); err != nil {
			t.Fatalf("fast-import: %v\n%s", err, out)
		}
		head := gitIn(t, repo, "2026-06-15T10:00:00+0000", "rev-parse", "refs/heads/main")
		d := draftFor(t, repo, base, head)
		got := humanizeRange(&d, now)
		if got.summary != "41 commits · 41 files" || len(got.lines) != 40 {
			t.Fatalf("summary %q lines %d", got.summary, len(got.lines))
		}
		if got.lines[0].subject != "c41" || got.lines[0].short != head[:12] || got.lines[39].subject != "c2" {
			t.Fatalf("order differs: first %+v last %+v", got.lines[0], got.lines[39])
		}
		if got.lines[0].relativeAge != "1 minute ago" {
			t.Fatalf("age %q", got.lines[0].relativeAge)
		}
	})

	t.Run("metadata failure falls back to endpoints", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "nope")
		anc, desc := strings.Repeat("a", 40), "refs/heads/some-very-long-branch-name"
		d := draftFor(t, missing, anc, desc)
		got := humanizeRange(&d, now)
		want := humanizedRange{
			summary: "Range " + strings.Repeat("a", 12) + ".." + "refs/heads/some-very-" + "…",
			lines: []rangeCommitLine{
				{short: strings.Repeat("a", 12), subject: "(known good)", author: "—", relativeAge: "—"},
				{short: "refs/heads/some-very-" + "…", subject: "(known bad)", author: "—", relativeAge: "—"},
			},
			source: "EXPLICIT", hasSource: true,
			unavailableReason: "Git metadata unavailable for this draft repository path; showing abbreviated endpoints only.",
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v\nwant %+v", got, want)
		}
		text := renderWitnessReviewText(witnessReviewFixture(t), &d, witnessReviewPageState{NowMs: &now})
		wantText := "Range: Range " + strings.Repeat("a", 12) + "..refs/heads/some-very-…\nSource: EXPLICIT\nGit metadata unavailable for this draft repository path; showing abbreviated endpoints only.\n  " +
			strings.Repeat("a", 12) + "  (known good)  · —  · —\n  refs/heads/some-very-…  (known bad)  · —  · —\n"
		if !strings.Contains(text, wantText) {
			t.Fatalf("fallback text differs: %q", text)
		}
	})
}

func TestReadGitUserName(t *testing.T) {
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)

	repo := newRepo(t)
	gitIn(t, repo, "2026-06-15T10:00:00+0000", "config", "user.name", "  Rev Ewer ")
	t.Chdir(repo)
	if got := ReadGitUserName(); got != "Rev Ewer" {
		t.Fatalf("name %q", got)
	}

	gitIn(t, repo, "2026-06-15T10:00:00+0000", "config", "user.name", "   ")
	if got := ReadGitUserName(); got != "local-reviewer" {
		t.Fatalf("blank name %q", got)
	}

	gitIn(t, repo, "2026-06-15T10:00:00+0000", "config", "--unset", "user.name")
	if got := ReadGitUserName(); got != "local-reviewer" {
		t.Fatalf("unset name %q", got)
	}

	t.Chdir(t.TempDir())
	if got := ReadGitUserName(); got != "local-reviewer" {
		t.Fatalf("no repo %q", got)
	}
}
