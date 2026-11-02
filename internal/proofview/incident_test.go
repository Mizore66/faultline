package proofview

// Tests for the pure interactive incident renderer (Go Slice 5b.2, Task 2).
//
// The primary assertion is complete-page equality against bytes captured from the
// frozen TypeScript renderer (difftest/testdata/incident-renderer). No expected page is
// ever derived from Go. The supplementary tests pin individual source branches with
// hand-written expectations taken from src/ui.ts at the frozen commit; they never
// replace the whole-page comparisons.
//
// The corpus is located relative to this package by default. A compiled test binary run
// from an empty working directory with an empty PATH must be given an absolute location:
//
//	proofview.test -test.run '^TestIncident' -incident-corpus-dir=/abs/difftest/testdata/incident-renderer
//
// The flag is test-only; production code has no filesystem seam.
//
// Non-ASCII string contents use explicit escapes so semantic trap characters remain visible.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
)

var incidentTestCorpusFlag = flag.String("incident-corpus-dir", "",
	"test-only absolute path of difftest/testdata/incident-renderer (default: relative to the package)")

const (
	incidentTestDefaultDir = "../../difftest/testdata/incident-renderer"

	// Independent pins copied from the reviewed baseline; the baseline itself is also read and
	// compared, but a self-consistent baseline alone is not accepted as provenance.
	incidentTestGoldenSHA256   = "f0326af0a916669d4b233c6df8f5ef7ac6aefaa3ad857349733aa3471f621290"
	incidentTestGoldenBytes    = 2011479
	incidentTestHTMLSHA256     = "97b431fcb07d4b900bc06c52a50253415c57fec6f9307e41754759ef3e4c0c0d"
	incidentTestHTMLBytes      = 33257
	incidentTestRetainedSHA256 = "e493ca2e8d4e81378e1bf074cd15794d91a5b0577d0bc957cfc061b18b5561e3"
	incidentTestRetainedBytes  = 46004
	incidentTestFrozenCommit   = "ce99a793af0b020bf1b9cf21114f8d7897b37207"

	incidentTestEmDash = "\u2014"
	// JS whitespace: space, NBSP, BOM, LS, PS, IDEOGRAPHIC SPACE, EM SPACE, tab, LF, VT, FF.
	incidentTestWS = " \u00a0\ufeff\u2028\u2029\u3000\u2003\t\n\v\f"
)

var incidentTestIDs = []string{
	"replay", "retained-rerun", "no-regression", "unstable-regression", "first-stable-regression",
	"empty-collections", "contribution-truncation", "unknown-state-labels", "checkpoint-names",
	"timeline-names", "caption-names", "verdicts-and-reasons", "change-names", "attempt-notes",
	"claim-kinds-and-statements", "grade-a", "grade-b", "grade-c", "escaping", "unicode",
	"lone-surrogates", "js-whitespace-and-case", "large-integer-metrics", "numeric-durations",
}

type incidentTestRow struct {
	ID           string `json:"id"`
	AnalysisJSON string `json:"analysisJSON"`
	HTMLJSON     string `json:"htmlJSON"`
	AfterJSON    string `json:"afterJSON"`
}

type incidentTestCorpus struct {
	dir      string
	golden   []byte
	html     []byte
	baseline []byte
	rows     []incidentTestRow
	expected []string // complete WTF-8 pages decoded from htmlJSON with jsjson.Parse
	err      error
}

var (
	incidentTestOnce sync.Once
	incidentTestData incidentTestCorpus
)

func incidentTestLoad(t *testing.T) *incidentTestCorpus {
	t.Helper()
	incidentTestOnce.Do(func() { incidentTestData = incidentTestRead() })
	if incidentTestData.err != nil {
		t.Fatalf("incident corpus invalid: %v", incidentTestData.err)
	}
	return &incidentTestData
}

func incidentTestSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// incidentTestRead loads and validates the committed corpus bindings without Node, dist/ or
// the repository root: IDs, order, duplicates, lengths, hashes, input before/after pairs.
func incidentTestRead() (c incidentTestCorpus) {
	fail := func(format string, args ...any) incidentTestCorpus {
		c.err = fmt.Errorf(format, args...)
		return c
	}
	dir := *incidentTestCorpusFlag
	if dir == "" {
		dir = incidentTestDefaultDir
	} else if !filepath.IsAbs(dir) {
		return fail("-incident-corpus-dir must be absolute: %q", dir)
	}
	c.dir = dir
	var err error
	if c.golden, err = os.ReadFile(filepath.Join(dir, "golden.jsonl")); err != nil {
		return fail("%v", err)
	}
	if c.html, err = os.ReadFile(filepath.Join(dir, "incident.html")); err != nil {
		return fail("%v", err)
	}
	if c.baseline, err = os.ReadFile(filepath.Join(dir, "baseline.json")); err != nil {
		return fail("%v", err)
	}
	if len(c.golden) != incidentTestGoldenBytes || incidentTestSum(c.golden) != incidentTestGoldenSHA256 {
		return fail("golden.jsonl length/hash differs from the pinned corpus (%d bytes, %s)", len(c.golden), incidentTestSum(c.golden))
	}
	if len(c.html) != incidentTestHTMLBytes || incidentTestSum(c.html) != incidentTestHTMLSHA256 {
		return fail("incident.html length/hash differs from the pinned baseline (%d bytes, %s)", len(c.html), incidentTestSum(c.html))
	}
	if !utf8.Valid(c.golden) || !utf8.Valid(c.html) {
		return fail("corpus is not valid UTF-8")
	}
	text := string(c.golden)
	if !strings.HasSuffix(text, "\n") {
		return fail("golden.jsonl lacks a final newline")
	}
	lines := strings.Split(text[:len(text)-1], "\n")
	if len(lines) != len(incidentTestIDs) {
		return fail("golden.jsonl has %d rows, want %d", len(lines), len(incidentTestIDs))
	}
	seenID := map[string]bool{}
	seenInput := map[string]bool{}
	for i, line := range lines {
		envelope, perr := jsjson.Parse(line)
		if perr != nil || envelope.Kind() != jsjson.Object {
			return fail("row %d is not a JSON object: %v", i+1, perr)
		}
		keys := envelope.Obj().Keys()
		if strings.Join(keys, ",") != "id,analysisJSON,htmlJSON,afterJSON" {
			return fail("row %d fields/order: %v", i+1, keys)
		}
		var row incidentTestRow
		if uerr := json.Unmarshal([]byte(line), &row); uerr != nil {
			return fail("row %d: %v", i+1, uerr)
		}
		if row.ID != incidentTestIDs[i] {
			return fail("row %d id %q, want %q (order/missing)", i+1, row.ID, incidentTestIDs[i])
		}
		if seenID[row.ID] {
			return fail("duplicate id %q", row.ID)
		}
		seenID[row.ID] = true
		if seenInput[row.AnalysisJSON] {
			return fail("%s: duplicate input", row.ID)
		}
		seenInput[row.AnalysisJSON] = true
		if row.AfterJSON != row.AnalysisJSON {
			return fail("%s: captured input changed across the TS render", row.ID)
		}
		analysis, aerr := jsjson.Parse(row.AnalysisJSON)
		if aerr != nil || jsjson.Stringify(analysis) != row.AnalysisJSON {
			return fail("%s: analysisJSON does not round-trip through jsjson (%v)", row.ID, aerr)
		}
		page, herr := jsjson.Parse(row.HTMLJSON)
		if herr != nil || page.Kind() != jsjson.String {
			return fail("%s: htmlJSON is not a JSON string (%v)", row.ID, herr)
		}
		if jsjson.Stringify(page) != row.HTMLJSON {
			return fail("%s: htmlJSON does not round-trip through jsjson", row.ID)
		}
		c.rows = append(c.rows, row)
		c.expected = append(c.expected, page.Str())
	}
	// The WTF-8 encoding of U+D800 must survive decoding of the lone-surrogates expected page.
	if !strings.Contains(c.expected[20], "\xed\xa0\x80") {
		return fail("lone-surrogates expected page lost its lone high surrogate")
	}
	return c
}

func incidentTestFirstDiff(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return fmt.Sprintf("first difference at byte %d (got len %d, want len %d)", i, len(a), len(b))
		}
	}
	return fmt.Sprintf("common prefix %d bytes (got len %d, want len %d)", n, len(a), len(b))
}

// ---------------------------------------------------------------------------------------------
// Corpus bindings
// ---------------------------------------------------------------------------------------------

type incidentTestBaseline struct {
	Schema                 string `json:"schema"`
	CurrentManifestVersion string `json:"currentManifestVersion"`
	FrozenSource           struct {
		Commit          string `json:"commit"`
		ManifestVersion string `json:"manifestVersion"`
	} `json:"frozenSource"`
	Runtime struct {
		Node           string `json:"node"`
		Version        string `json:"version"`
		Locale         string `json:"locale"`
		GroupingSample string `json:"groupingSample"`
	} `json:"runtime"`
	RetainedInput struct {
		Path   string `json:"path"`
		Bytes  int    `json:"bytes"`
		SHA256 string `json:"sha256"`
	} `json:"retainedInput"`
	Cases  []string `json:"cases"`
	Corpus struct {
		Golden struct {
			Path   string `json:"path"`
			Bytes  int    `json:"bytes"`
			SHA256 string `json:"sha256"`
			Rows   int    `json:"rows"`
		} `json:"golden"`
		HTML struct {
			Path   string `json:"path"`
			Bytes  int    `json:"bytes"`
			SHA256 string `json:"sha256"`
		} `json:"html"`
	} `json:"corpus"`
}

func TestIncidentCorpusBindings(t *testing.T) {
	c := incidentTestLoad(t)

	var b incidentTestBaseline
	if err := json.Unmarshal(c.baseline, &b); err != nil {
		t.Fatal(err)
	}
	if b.Schema != "faultline.incident-renderer-oracle.v1" {
		t.Errorf("baseline schema %q", b.Schema)
	}
	if b.FrozenSource.Commit != incidentTestFrozenCommit || b.FrozenSource.ManifestVersion != "0.1.3" || b.CurrentManifestVersion != "0.1.4" {
		t.Errorf("baseline source/manifest pins differ: %+v %q", b.FrozenSource, b.CurrentManifestVersion)
	}
	if b.Runtime.Node != "22.22.2" || b.Runtime.Version != "v22.22.2" || b.Runtime.Locale != "en-US" || b.Runtime.GroupingSample != "1,234,567" {
		t.Errorf("baseline oracle runtime/locale differs: %+v", b.Runtime)
	}
	if strings.Join(b.Cases, ",") != strings.Join(incidentTestIDs, ",") {
		t.Errorf("baseline case list/order differs: %v", b.Cases)
	}
	if b.Corpus.Golden.Rows != len(incidentTestIDs) ||
		b.Corpus.Golden.Bytes != len(c.golden) || b.Corpus.Golden.SHA256 != incidentTestSum(c.golden) ||
		b.Corpus.Golden.Path != "difftest/testdata/incident-renderer/golden.jsonl" {
		t.Errorf("baseline golden binding differs: %+v", b.Corpus.Golden)
	}
	if b.Corpus.HTML.Bytes != len(c.html) || b.Corpus.HTML.SHA256 != incidentTestSum(c.html) ||
		b.Corpus.HTML.Path != "difftest/testdata/incident-renderer/incident.html" {
		t.Errorf("baseline html binding differs: %+v", b.Corpus.HTML)
	}
	if b.RetainedInput.Bytes != incidentTestRetainedBytes || b.RetainedInput.SHA256 != incidentTestRetainedSHA256 {
		t.Errorf("baseline retained-input pin differs: %+v", b.RetainedInput)
	}

	// The standalone UTF-8 baseline is the replay row under the TS transport encoding.
	if got := jsstr.ToUTF8(c.expected[0]); got != string(c.html) {
		t.Errorf("incident.html differs from the replay row: %s", incidentTestFirstDiff(got, string(c.html)))
	}

	// The retained RERUN input is a committed file; the retained-rerun row must be its parse.
	retained, err := os.ReadFile(filepath.Join(c.dir, "..", "bases", "demo-rerun", "analysis.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(retained) != incidentTestRetainedBytes || incidentTestSum(retained) != incidentTestRetainedSHA256 {
		t.Fatalf("retained RERUN input differs from the pinned bytes (%d, %s)", len(retained), incidentTestSum(retained))
	}
	v, err := jsjson.Parse(string(retained))
	if err != nil {
		t.Fatal(err)
	}
	if jsjson.Stringify(v) != c.rows[1].AnalysisJSON {
		t.Error("retained-rerun row is not the retained RERUN analysis")
	}
	if got := v.Get("mode"); got.Kind() != jsjson.String || got.Str() != "RERUN" {
		t.Error("retained input is not a RERUN analysis")
	}
	replay, err := jsjson.Parse(c.rows[0].AnalysisJSON)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Get("mode").Str() != "REPLAY" || replay.Get("grade", "value").Str() != "D" {
		t.Error("replay baseline must be REPLAY with grade D")
	}
}

// ---------------------------------------------------------------------------------------------
// Complete frozen-TS parity
// ---------------------------------------------------------------------------------------------

func TestIncidentPageExact(t *testing.T) {
	c := incidentTestLoad(t)
	for i, row := range c.rows {
		id, want := row.ID, c.expected[i]
		t.Run(id, func(t *testing.T) {
			analysis, err := jsjson.Parse(row.AnalysisJSON)
			if err != nil {
				t.Fatal(err)
			}
			before := jsjson.Stringify(analysis)
			got := RenderIncidentPage(analysis)
			if got != want {
				t.Fatalf("%s: complete incident page differs from frozen TS: %s", id, incidentTestFirstDiff(got, want))
			}
			if after := jsjson.Stringify(analysis); after != before {
				t.Fatalf("%s: renderer mutated analysis", id)
			}
			if after := jsjson.Stringify(analysis); after != row.AfterJSON {
				t.Fatalf("%s: input differs from captured after-input", id)
			}
			// Transport encoding is identical to Buffer.from(page, "utf8").
			gotBytes, wantBytes := jsstr.ToUTF8(got), jsstr.ToUTF8(want)
			if gotBytes != wantBytes || !utf8.ValidString(gotBytes) {
				t.Fatalf("%s: UTF-8 transport bytes differ", id)
			}
			if id == "replay" && gotBytes != string(c.html) {
				t.Fatalf("replay: UTF-8 bytes differ from the committed incident.html baseline")
			}
			// Repeated call on the same value is deterministic and still non-mutating.
			if again := RenderIncidentPage(analysis); again != want {
				t.Fatalf("%s: second call differs", id)
			}
			if jsjson.Stringify(analysis) != before {
				t.Fatalf("%s: second call mutated analysis", id)
			}
		})
	}
}

func TestIncidentConcurrentIndependentInputs(t *testing.T) {
	c := incidentTestLoad(t)
	const workers = 8
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for k := range c.rows {
				// Different start offsets so workers interleave over different cases.
				i := (k + w*3) % len(c.rows)
				analysis, err := jsjson.Parse(c.rows[i].AnalysisJSON)
				if err != nil {
					t.Errorf("worker %d %s: %v", w, c.rows[i].ID, err)
					return
				}
				before := jsjson.Stringify(analysis)
				if got := RenderIncidentPage(analysis); got != c.expected[i] {
					t.Errorf("worker %d %s: page differs from frozen TS", w, c.rows[i].ID)
				}
				if jsjson.Stringify(analysis) != before {
					t.Errorf("worker %d %s: renderer mutated analysis", w, c.rows[i].ID)
				}
			}
		}(w)
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------------------------
// Supplementary branch regressions (hand-written from src/ui.ts at the frozen commit)
// ---------------------------------------------------------------------------------------------

func incidentTestReplay(t *testing.T) jsjson.Value {
	t.Helper()
	v, err := jsjson.Parse(incidentTestLoad(t).rows[0].AnalysisJSON)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func incidentTestObj(t *testing.T, v jsjson.Value, keys ...string) *jsjson.Obj {
	t.Helper()
	o := v.Get(keys...)
	if o.Kind() != jsjson.Object {
		t.Fatalf("%v is not an object", keys)
	}
	return o.Obj()
}

func incidentTestRender(t *testing.T, a jsjson.Value) string {
	t.Helper()
	before := jsjson.Stringify(a)
	got := RenderIncidentPage(a)
	if jsjson.Stringify(a) != before {
		t.Fatal("renderer mutated analysis")
	}
	return got
}

func incidentTestStrings(items ...string) jsjson.Value {
	vs := make([]jsjson.Value, len(items))
	for i, s := range items {
		vs[i] = jsjson.MakeString(s)
	}
	return jsjson.MakeArray(vs)
}

func incidentTestRun(t *testing.T, tmpl jsjson.Value, stateID, verdict, reason, kind string, duration float64) jsjson.Value {
	t.Helper()
	v, err := jsjson.Parse(jsjson.Stringify(tmpl))
	if err != nil {
		t.Fatal(err)
	}
	o := v.Obj()
	o.Set("stateId", jsjson.MakeString(stateID))
	o.Set("verdict", jsjson.MakeString(verdict))
	o.Set("reasonCode", jsjson.MakeString(reason))
	o.Set("executionKind", jsjson.MakeString(kind))
	o.Set("durationMs", jsjson.MakeNumber(duration))
	return v
}

// incidentTestPassRuns builds CACHED PASS/PREDICATE_PASS runs for the given state IDs.
func incidentTestPassRuns(t *testing.T, a jsjson.Value, ids ...string) jsjson.Value {
	t.Helper()
	tmpl := a.Get("timelineRuns").Items()[0]
	runs := make([]jsjson.Value, len(ids))
	for i, id := range ids {
		runs[i] = incidentTestRun(t, tmpl, id, "PASS", "PREDICATE_PASS", "CACHED", 0)
	}
	return jsjson.MakeArray(runs)
}

// incidentTestPassRow is the expected row of a CACHED PASS/PREDICATE_PASS run, spelled out literally.
func incidentTestPassRow(name string) string {
	return `<div class="run-row"><div><strong>` + name + `</strong><small>Passed the approved test</small></div>` +
		`<div class="run-meta"><span class="verdict pass">WORKING</span><small>saved result</small></div></div>`
}

func incidentTestSection(t *testing.T, page, start, end string) string {
	t.Helper()
	i := strings.Index(page, start)
	if i < 0 {
		t.Fatalf("section start %q missing", start)
	}
	rest := page[i+len(start):]
	j := strings.Index(rest, end)
	if j < 0 {
		t.Fatalf("section end %q missing", end)
	}
	return rest[:j]
}

const (
	incidentTestTimelineStart = `<p class="timeline-intro">Scroll through the timeline. Each step is one saved point in the work.</p><div class="scroll-frame"><div class="timeline-scroll">`
	incidentTestAttemptStart  = `checks used.</strong> Scroll to see each combination FaultLine tried.</p><div class="scroll-frame"><div class="timeline-scroll">`
	incidentTestClaimStart    = `<div class="scroll-frame"><div class="timeline-scroll claims-scroll">`
	incidentTestContribStart  = `<small>This version fails the test</small></div></div>`
	incidentTestScrollEnd     = `</div><div class="scroll-hint">`
	incidentTestContribEnd    = `</article></div></section>`
)

func TestIncidentTimelineNamesAndJSTraps(t *testing.T) {
	lastGood := "Step 4 " + incidentTestEmDash + " last version that still worked"
	firstBad := "Step 5 " + incidentTestEmDash + " first version that broke"
	repaired := "Step 7 " + incidentTestEmDash + " version after a repair"
	cases := []struct{ id, name string }{
		{"Turn 4 " + incidentTestEmDash + " last good", lastGood},
		{"Turn 5 FIRST BAD", firstBad},
		{"turn 7 repaired", repaired},
		{"Turn 8 Reintroduced", "Step 8 " + incidentTestEmDash + " bug came back later"},
		{"last good and first bad", lastGood},
		{"first bad repaired", firstBad},
		{"repaired reintroduced", repaired},
		{"LAST GOOD", lastGood},
		{"Turn 9", "Step 9"},
		{"turn  010", "Step 010"},
		{"Turn 0", "Step 0"}, // "0" is a non-empty (truthy) string
		{"TURN 12 extra", "Step 12"},
		{"turn\t7", "Step 7"},
		{"Turn 3 repair", "Step 3"}, // timeline names look for "repaired", not "repair"
		{"Turn\ufeff4", "Step 4"},
		{"Turn\u00a05", "Step 5"},
		{"Turn\u20285", "Step 5"},
		{"Turn\u30005", "Step 5"},
		{"Turn\v5", "Step 5"},
		{"Turn\f5", "Step 5"},
		// Non-ASCII case-fold traps: non-Unicode /i never folds a non-ASCII code point onto ASCII.
		{"Turn 3 la\u017ft good", "Step 3"},
		{"Turn 3 f\u0131rst bad", "Step 3"},
		{"Turn 8 re\u0130ntroduced", "Step 8"},
		{"la\u017ft good", "la\u017ft good"},
		{"f\u0131rst bad", "f\u0131rst bad"},
		{"\u212aelvin repaired", repaired}, // the ASCII word "repaired" still matches
		{"\u212aelvin Turn 6", "Step 6"},
		// Not matching: \s is JS whitespace only (neither U+180E nor U+0085), \d is ASCII only.
		{"Turn\u180e5", "Turn\u180e5"},
		{"Turn\u0085 4", "Turn\u0085 4"},
		{"Turn\u212a 5", "Turn\u212a 5"},
		{"Turn5", "Turn5"},
		{"Turn \u0663", "Turn \u0663"},
		{"\uff34\uff35\uff32\uff2e 5", "\uff34\uff35\uff32\uff2e 5"},
		{"Turn \uff11\uff12", "Turn \uff11\uff12"},
		{"unlisted-state", "unlisted-state"},
		{"", ""},
		{"   ", "   "},
		{incidentTestWS + "Turn 6" + incidentTestWS, "Step 6"}, // unanchored: surrounding whitespace is irrelevant
	}
	a := incidentTestReplay(t)
	ids := make([]string, len(cases))
	var want strings.Builder
	for i, c := range cases {
		ids[i] = c.id
		want.WriteString(incidentTestPassRow(c.name))
	}
	incidentTestObj(t, a).Set("timelineRuns", incidentTestPassRuns(t, a, ids...))
	page := incidentTestRender(t, a)
	got := incidentTestSection(t, page, incidentTestTimelineStart, incidentTestScrollEnd)
	if got != want.String() {
		t.Errorf("timeline section differs from the hand-written rows: %s", incidentTestFirstDiff(got, want.String()))
		for _, c := range cases {
			if !strings.Contains(got, incidentTestPassRow(c.name)) {
				t.Errorf("timeline label %q: row for %q missing", c.id, c.name)
			}
		}
	}
}

func TestIncidentCheckpointNames(t *testing.T) {
	// "$" is a strict end anchor (no m flag): any trailing terminator leaves the label unchanged.
	// Controller oracle queries (Node 22.22.2): LF, CR, CRLF, U+2028, U+2029 and U+0085 all left the
	// whole label unchanged, and the non-ASCII prefix case (Omega, U+03A9) gave "<Omega>mega version"
	// (the ^\w+ prefix rule cannot match, the suffix rule applies). The other uncertain cases here
	// were confirmed by the same oracle.
	groups := []struct{ ids, want [3]string }{
		{
			[3]string{"\u03a9mega contribution boundary", "Beta_2  CONTRIBUTION extra", "Contribution boundary"},
			[3]string{"\u03a9mega version", "Beta_2 version", "Contribution boundary"},
		},
		{
			[3]string{"Gamma\ufeffcontribution boundary", "Delta\u0085contribution boundary", "Zeta" + incidentTestWS + "contribution"},
			[3]string{"Gamma version", "Delta\u0085contribution boundary", "Zeta version"},
		},
		{
			[3]string{"x-y contribution boundary", "", "ZZ contribution\nboundary"},
			[3]string{"x-y version", "", "ZZ version"},
		},
		{
			[3]string{"-- contribution boundary\n", "-- contribution boundary\r\n", "-- contribution boundary "},
			[3]string{"-- contribution boundary\n", "-- contribution boundary\r\n", "-- contribution boundary "},
		},
		{
			[3]string{"-- contribution boundary\r", "-- contribution boundary ", "-- contribution boundary\u0085"},
			[3]string{"-- contribution boundary\r", "-- contribution boundary ", "-- contribution boundary\u0085"},
		},
		{
			[3]string{"-- contribution boundary\u2028", "-- contribution boundary", "-- contribution boundary\u2029"},
			[3]string{"-- contribution boundary\u2028", "-- version", "-- contribution boundary\u2029"},
		},
		{
			[3]string{"  contribution boundary", "contribution contribution", "_x contribution BOUNDARY"},
			[3]string{" version", "contribution version", "_x version"},
		},
		{
			// The ASCII word class excludes non-ASCII digits and letters. U+0663: the prefix rule
			// fails, the suffix rule strips. "caf\u00e9 contribution": \w+ stops before the accent,
			// \s+ fails, and there is no "boundary" suffix, so it is unchanged. "A\0B ...": \w+ is "A",
			// \s+ fails on NUL, the suffix rule strips.
			[3]string{"\u0663 contribution boundary", "caf\u00e9 contribution", "A\u0000B contribution boundary"},
			[3]string{"\u0663 version", "caf\u00e9 contribution", "A\u0000B version"},
		},
	}
	for n, g := range groups {
		a := incidentTestReplay(t)
		tmpl := a.Get("timelineRuns").Items()[0]
		runs := make([]jsjson.Value, 0, 5)
		for _, id := range g.ids {
			runs = append(runs, incidentTestRun(t, tmpl, id, "PASS", "PREDICATE_PASS", "CACHED", 0))
		}
		// Rows 4 and 5 must be truncated away.
		runs = append(runs, incidentTestRun(t, tmpl, "ignored-fourth contribution boundary", "FAIL", "PREDICATE_FAIL", "CACHED", 0))
		runs = append(runs, incidentTestRun(t, tmpl, "ignored-fifth contribution boundary", "FAIL", "PREDICATE_FAIL", "CACHED", 0))
		incidentTestObj(t, a).Set("contributionRuns", jsjson.MakeArray(runs))
		page := incidentTestRender(t, a)
		got := incidentTestSection(t, page, incidentTestContribStart, incidentTestContribEnd)
		want := incidentTestPassRow(g.want[0]) + incidentTestPassRow(g.want[1]) + incidentTestPassRow(g.want[2])
		if got != want {
			t.Errorf("checkpoint group %d: %s", n, incidentTestFirstDiff(got, want))
		}
		if strings.Contains(page, "ignored-") {
			t.Errorf("checkpoint group %d: rows past the first three were rendered", n)
		}
	}
}

func TestIncidentContributionTruncationAndEmpty(t *testing.T) {
	a := incidentTestReplay(t)
	incidentTestObj(t, a).Set("contributionRuns", incidentTestPassRuns(t, a,
		"contribution-atlas", "contribution-birch", "contribution-cedar", "contribution-delta", "truncated-fourth-sentinel"))
	page := incidentTestRender(t, a)
	got := incidentTestSection(t, page, incidentTestContribStart, incidentTestContribEnd)
	want := incidentTestPassRow("Atlas version") + incidentTestPassRow("Birch version") + incidentTestPassRow("Cedar version")
	if got != want {
		t.Errorf("first three contribution rows only: %s", incidentTestFirstDiff(got, want))
	}
	if strings.Contains(page, "Delta version") || strings.Contains(page, "truncated-fourth-sentinel") {
		t.Error("fourth contribution row leaked")
	}

	a = incidentTestReplay(t)
	o := incidentTestObj(t, a)
	o.Set("contributionRuns", jsjson.MakeArray(nil))
	o.Set("timelineRuns", jsjson.MakeArray(nil))
	o.Set("claims", jsjson.MakeArray(nil))
	incidentTestObj(t, a, "minimization").Set("attempts", jsjson.MakeArray(nil))
	incidentTestObj(t, a, "minimization").Set("candidate", jsjson.MakeArray(nil))
	page = incidentTestRender(t, a)
	if s := incidentTestSection(t, page, incidentTestContribStart, incidentTestContribEnd); s != "" {
		t.Errorf("empty contribution rows rendered %q", s)
	}
	if s := incidentTestSection(t, page, incidentTestTimelineStart, incidentTestScrollEnd); s != "" {
		t.Errorf("empty timeline rows rendered %q", s)
	}
	if s := incidentTestSection(t, page, incidentTestAttemptStart, incidentTestScrollEnd); s != "" {
		t.Errorf("empty attempts rendered %q", s)
	}
	if s := incidentTestSection(t, page, incidentTestClaimStart, incidentTestScrollEnd); s != "" {
		t.Errorf("empty claims rendered %q", s)
	}
	if !strings.Contains(page, `<p class="mono"></p>`) {
		t.Error("empty candidate label should render an empty mono paragraph")
	}
	// No claim rows means no evidence-row markup: nothing is inferred or certified from absence.
	if strings.Contains(page, `class="evidence-row"`) {
		t.Error("page without claims contains evidence rows")
	}
}

func TestIncidentRunReasonsDurationsAndBadges(t *testing.T) {
	a := incidentTestReplay(t)
	tmpl := a.Get("timelineRuns").Items()[0]
	type rc struct {
		verdict, reason, kind, label, class, friendly, meta string
		duration                                            float64
	}
	rows := []rc{
		{"PASS", "WITNESS_SATISFIED", "CACHED", "WORKING", "pass", "Passed the approved test", "saved result", 99},
		{"FAIL", "PREDICATE_PASS", "EXECUTED", "BROKEN", "fail", "Passed the approved test", "3 ms", 3},
		{"UNSTABLE", "WITNESS_ASSERTION_FAILED", "CACHED", "UNSTABLE", "unstable", "Failed the approved test", "saved result", 0},
		{"ERROR", "PREDICATE_FAIL", "EXECUTED", "ERROR", "error", "Failed the approved test", "9 ms", 9},
		{"INAPPLICABLE", "FIXTURE_COMPILE_ERROR", "EXECUTED", "INAPPLICABLE", "inapplicable", "Could not finish this check", "0 ms", 0},
		{"PASS", "", "EXECUTED", "WORKING", "pass", "Could not finish this check", "1 ms", 1},
		{"PASS", "predicate_pass", "EXECUTED", "WORKING", "pass", "Could not finish this check", "1 ms", 1},
		{"PASS", "RUNNER_ERROR", "EXECUTED", "WORKING", "pass", "Could not finish this check", "21 ms", 21},
	}
	runs := make([]jsjson.Value, len(rows))
	var want strings.Builder
	for i, r := range rows {
		runs[i] = incidentTestRun(t, tmpl, "cedar-turn-1", r.verdict, r.reason, r.kind, r.duration)
		want.WriteString(`<div class="run-row"><div><strong>Step 1</strong><small>` + r.friendly + `</small></div>` +
			`<div class="run-meta"><span class="verdict ` + r.class + `">` + r.label + `</span><small>` + r.meta + `</small></div></div>`)
	}
	incidentTestObj(t, a).Set("timelineRuns", jsjson.MakeArray(runs))
	got := incidentTestSection(t, incidentTestRender(t, a), incidentTestTimelineStart, incidentTestScrollEnd)
	if got != want.String() {
		t.Errorf("run rows: %s", incidentTestFirstDiff(got, want.String()))
	}

	// Prevention-section badges and transition badges for every verdict.
	for _, v := range []struct{ verdict, class, label string }{
		{"PASS", "pass", "WORKING"}, {"FAIL", "fail", "BROKEN"}, {"UNSTABLE", "unstable", "UNSTABLE"},
		{"ERROR", "error", "ERROR"}, {"INAPPLICABLE", "inapplicable", "INAPPLICABLE"},
	} {
		a := incidentTestReplay(t)
		incidentTestObj(t, a, "prevention", "lastGood").Set("verdict", jsjson.MakeString(v.verdict))
		badge := `<span class="verdict ` + v.class + `">` + v.label + `</span>`
		page := incidentTestRender(t, a)
		if !strings.Contains(page, `<div class="proof"><span>Version before the bug</span>`+badge+`</div>`) {
			t.Errorf("%s: prevention badge missing", v.verdict)
		}
		if !strings.Contains(page, `<div class="transition"><div>`+badge+`<small>`) {
			t.Errorf("%s: transition badge missing", v.verdict)
		}
	}
}

func TestIncidentNumericRendering(t *testing.T) {
	// Locale-grouped positive integer (en-US, ASCII commas, binary64 shortest digits). The 999, 1000,
	// 1234567, 1e21, 2**53, 2**63 and 123456789012345680000 rows are controller Node 22.22.2 en-US
	// oracle results.
	for _, c := range []struct {
		v    float64
		want string
	}{
		{1, "1"}, {999, "999"}, {1000, "1,000"}, {12345, "12,345"}, {999999, "999,999"},
		{1000000, "1,000,000"}, {1234567, "1,234,567"}, {1e15, "1,000,000,000,000,000"},
		{math.Pow(2, 53), "9,007,199,254,740,992"}, {1e21, "1,000,000,000,000,000,000,000"},
		{math.Pow(2, 63), "9,223,372,036,854,776,000"},
		{123456789012345680000, "123,456,789,012,345,680,000"},
	} {
		a := incidentTestReplay(t)
		incidentTestObj(t, a, "metrics").Set("changedLines", jsjson.MakeNumber(c.v))
		page := incidentTestRender(t, a)
		if !strings.Contains(page, `<div class="metric"><b>`+c.want+`</b><small>lines changed</small></div>`) {
			t.Errorf("changedLines %v: want grouped %q", c.v, c.want)
		}
	}
	// Every other numeric interpolation is plain JS number-to-string, never grouped.
	a := incidentTestReplay(t)
	m := incidentTestObj(t, a, "metrics")
	m.Set("sessions", jsjson.MakeNumber(1234567))
	m.Set("turns", jsjson.MakeNumber(1e21))
	m.Set("files", jsjson.MakeNumber(123456789012345680000))
	m.Set("changedLines", jsjson.MakeNumber(1000))
	b := incidentTestObj(t, a, "minimization", "budget")
	b.Set("used", jsjson.MakeNumber(123456789012345680000))
	b.Set("max", jsjson.MakeNumber(1e21))
	page := incidentTestRender(t, a)
	for _, frag := range []string{
		`<div class="metric"><b>1234567</b><small>work sessions checked</small></div>` +
			`<div class="metric"><b>1e+21</b><small>saved steps checked</small></div>` +
			`<div class="metric"><b>123456789012345680000</b><small>files involved</small></div>` +
			`<div class="metric"><b>1,000</b><small>lines changed</small></div>`,
		`<strong>123456789012345680000 of 1e+21 checks used.</strong>`,
	} {
		if !strings.Contains(page, frag) {
			t.Errorf("missing %q", frag)
		}
	}

	// Durations: JS Number::toString for EXECUTED, "saved result" for CACHED.
	sum := 0.1
	sum += 0.2
	durations := []struct {
		v    float64
		want string
	}{
		{0, "0"}, {1, "1"}, {0.5, "0.5"}, {12.34, "12.34"}, {1e-7, "1e-7"}, {1e21, "1e+21"},
		{123456789012345680000, "123456789012345680000"}, {math.SmallestNonzeroFloat64, "5e-324"},
		{1234567.891, "1234567.891"}, {sum, "0.30000000000000004"}, {100, "100"}, {0.000001, "0.000001"},
		{math.Pow(2, 53), "9007199254740992"}, {math.MaxFloat64, "1.7976931348623157e+308"},
	}
	a = incidentTestReplay(t)
	tmpl := a.Get("timelineRuns").Items()[0]
	var runs []jsjson.Value
	var want strings.Builder
	row := func(meta string) string {
		return `<div class="run-row"><div><strong>Step 1</strong><small>Passed the approved test</small></div>` +
			`<div class="run-meta"><span class="verdict pass">WORKING</span><small>` + meta + `</small></div></div>`
	}
	for _, d := range durations {
		runs = append(runs, incidentTestRun(t, tmpl, "cedar-turn-1", "PASS", "PREDICATE_PASS", "EXECUTED", d.v))
		want.WriteString(row(d.want + " ms"))
	}
	runs = append(runs, incidentTestRun(t, tmpl, "cedar-turn-1", "PASS", "PREDICATE_PASS", "CACHED", 7))
	runs = append(runs, incidentTestRun(t, tmpl, "cedar-turn-1", "PASS", "PREDICATE_PASS", "CACHED", 1e21))
	want.WriteString(row("saved result") + row("saved result"))
	incidentTestObj(t, a).Set("timelineRuns", jsjson.MakeArray(runs))
	got := incidentTestSection(t, incidentTestRender(t, a), incidentTestTimelineStart, incidentTestScrollEnd)
	if got != want.String() {
		t.Errorf("duration rows: %s", incidentTestFirstDiff(got, want.String()))
	}
}

func TestIncidentTransitionAndGradeCopy(t *testing.T) {
	const (
		confirm  = `<p><strong>Confirmed: the bug first shows up between these two versions.</strong></p><small>Here, the same test reliably changes from working to broken.</small>`
		fallback = `<p><strong>We could not confirm exactly where the bug starts.</strong></p><small>If the results are unclear, FaultLine will not guess.</small>`
	)
	tr := func(kind string, stable bool) jsjson.Value {
		before, after := "FAIL", "PASS"
		if kind == "PASS_TO_FAIL" {
			before, after = "PASS", "FAIL"
		}
		return jsjson.MustObject(
			"beforeStateId", jsjson.MakeString("cedar-turn-4"), "afterStateId", jsjson.MakeString("cedar-turn-5"),
			"beforeVerdict", jsjson.MakeString(before), "afterVerdict", jsjson.MakeString(after),
			"stable", jsjson.MakeBool(stable), "kind", jsjson.MakeString(kind),
			"boundaryRunIds", jsjson.MakeArray(nil))
	}
	for _, c := range []struct {
		name string
		ts   []jsjson.Value
		want string
	}{
		{"none", nil, fallback},
		{"unstable pass-to-fail only", []jsjson.Value{tr("PASS_TO_FAIL", false)}, fallback},
		{"stable reverse only", []jsjson.Value{tr("FAIL_TO_PASS", true)}, fallback},
		{"reverse and unstable", []jsjson.Value{tr("FAIL_TO_PASS", true), tr("PASS_TO_FAIL", false), tr("FAIL_TO_PASS", false)}, fallback},
		{"stable after unstable", []jsjson.Value{tr("PASS_TO_FAIL", false), tr("PASS_TO_FAIL", true)}, confirm},
		{"stable first", []jsjson.Value{tr("PASS_TO_FAIL", true)}, confirm},
	} {
		a := incidentTestReplay(t)
		incidentTestObj(t, a).Set("transitions", jsjson.MakeArray(c.ts))
		page := incidentTestRender(t, a)
		other := fallback
		if c.want == fallback {
			other = confirm
		}
		if !strings.Contains(page, c.want) || strings.Contains(page, other) {
			t.Errorf("transitions %q: wrong confirmation copy", c.name)
		}
	}

	for _, g := range []string{"A", "B", "C", "D"} {
		a := incidentTestReplay(t)
		incidentTestObj(t, a, "grade").Set("value", jsjson.MakeString(g))
		page := incidentTestRender(t, a)
		small := `This grade shows how complete and repeatable the checks were.`
		if g == "A" {
			small = `Strong: the important checks all passed.`
		}
		if !strings.Contains(page, `<div class="package-summary"><strong>Result grade `+g+`</strong><small>`+small+`</small></div>`) {
			t.Errorf("grade %s: package summary differs", g)
		}
	}

	// Historical product copy is kept verbatim for cached inputs (not a certification claim).
	page := incidentTestRender(t, incidentTestReplay(t))
	for _, frag := range []string{`<strong>THE FIX CHECKS OUT</strong>`, `<p><strong>Checked both ways</strong>`, `<div class="command">DEMO MODE</div>`} {
		if !strings.Contains(page, frag) {
			t.Errorf("verbatim frozen copy %q missing", frag)
		}
	}
}

func TestIncidentCaptions(t *testing.T) {
	a := incidentTestReplay(t)
	incidentTestObj(t, a, "prevention", "lastGood").Set("stateId", jsjson.MakeString("Repair worktree"))
	incidentTestObj(t, a, "prevention", "firstBad").Set("stateId", jsjson.MakeString("Turn 12 reintroduced"))
	page := incidentTestRender(t, a)
	if !strings.Contains(page, `<small>Repaired version</small></div><div class="arrow">`) ||
		!strings.Contains(page, `<small>Step 8 `+incidentTestEmDash+` bug came back later</small></div></div>`) {
		t.Error("unknown-id captions differ (repair -> Repaired version, reintroduced -> step 8 copy)")
	}
	// Known fixture IDs resolve through their labels.
	page = incidentTestRender(t, incidentTestReplay(t))
	if !strings.Contains(page, `<small>Last good version</small></div><div class="arrow">`) ||
		!strings.Contains(page, `<small>First broken version</small></div></div>`) {
		t.Error("fixture-state captions differ")
	}
	// Caption rules: last good > first bad > repair, each unanchored and ASCII case-insensitive.
	for _, c := range []struct{ id, want string }{
		{"LAST GOOD", "Last good version"}, {"xx FIRST BAD yy", "First broken version"},
		{"REPAIR", "Repaired version"}, {"last good first bad repair", "Last good version"},
		{"la\u017ft good", "la\u017ft good"}, {"f\u0131rst bad", "f\u0131rst bad"},
		{"Turn 3", "Step 3"}, {"", ""},
	} {
		a := incidentTestReplay(t)
		incidentTestObj(t, a, "prevention", "lastGood").Set("stateId", jsjson.MakeString(c.id))
		page := incidentTestRender(t, a)
		if !strings.Contains(page, `<small>`+c.want+`</small></div><div class="arrow">`) {
			t.Errorf("caption for %q: want %q", c.id, c.want)
		}
	}
}

func incidentTestAttempt(id string, subset []string, outcome, note string) jsjson.Value {
	return jsjson.MustObject("id", jsjson.MakeString(id), "subset", incidentTestStrings(subset...),
		"outcome", jsjson.MakeString(outcome), "note", jsjson.MakeString(note))
}

func TestIncidentAttemptsChangesAndNotes(t *testing.T) {
	const (
		noChanges = "With no changes, the test still passes."
		display   = "Changing only the display currency does not break the test."
		boundary  = "Changing only the settlement side is not enough by itself."
		partial   = "This incomplete change could not be applied, so it does not count as proof."
		adding    = "Adding both changes to the working version makes the test fail."
		removing  = "Removing both changes from the broken version makes the test pass again."
	)
	type ac struct {
		subset  []string
		outcome string
		note    string
		changes string
		want    string
	}
	hostile := `<>&"'`
	hostileEsc := `&lt;&gt;&amp;&quot;&#039;`
	attempts := []ac{
		{[]string{"settlement-display-default", "settlement-display-boundary"}, "PASS", "No implicated edits", "display default change + settlement boundary change", noChanges},
		{[]string{"partial-renderer-hunk"}, "UNRESOLVED", "NO IMPLICATED EDITS here", "incomplete change (could not apply)", noChanges},
		{nil, "FAIL", "display default alone", "No changes", display},
		{[]string{"prefix-settlement-display-default-suffix"}, "PASS", "Display Default Alone passes", "display default change", display},
		{[]string{"settlement-display-default-partial"}, "FAIL", "boundary assignment alone", "display default change", boundary},
		{[]string{"xx partial yy", "Partial", "SETTLEMENT-DISPLAY-DEFAULT"}, "UNRESOLVED", "partial patch", "incomplete change (could not apply) + Partial + SETTLEMENT-DISPLAY-DEFAULT", partial},
		{[]string{"settlement-display-boundary", "partial", "settlement-display-default"}, "PASS", "UNRESOLVED hunk", "settlement boundary change + incomplete change (could not apply) + display default change", partial},
		{[]string{"settlement-display-boundar", "partia", hostile}, "FAIL", "reproduces the frozen witness failure", "settlement-display-boundar + partia + " + hostileEsc, adding},
		{nil, "PASS", "restores the frozen witness pass", "No changes", removing},
		{nil, "PASS", "No implicated edits; partial patch", "No changes", noChanges},
		{nil, "PASS", "display default alone and boundary assignment alone", "No changes", display},
		{nil, "FAIL", "partial patch reproduces the frozen witness failure", "No changes", partial},
		{nil, "UNRESOLVED", "unresolved restores the frozen witness pass", "No changes", partial},
		{nil, "PASS", "Reproduces the frozen witness failure", "No changes", adding},
		{nil, "PASS", "PARTIAL PATCH", "No changes", partial},
		{nil, "PASS", "unmapped note", "No changes", "unmapped note"},
		{nil, "PASS", "", "No changes", ""},
		{nil, "PASS", hostile, "No changes", hostileEsc},
		// The pattern holds one literal space; JS whitespace runs and the long s do not match.
		{nil, "PASS", "No" + incidentTestWS + "implicated edits", "No changes", "No" + incidentTestWS + "implicated edits"},
		{nil, "PASS", "unre\u017folved", "No changes", "unre\u017folved"},
		{nil, "PASS", "BOUNDARY ASSIGNMENT ALONE", "No changes", boundary},
	}
	a := incidentTestReplay(t)
	items := make([]jsjson.Value, len(attempts))
	var want strings.Builder
	for i, c := range attempts {
		items[i] = incidentTestAttempt(fmt.Sprintf("a%d", i), c.subset, c.outcome, c.note)
		label, class := "COULD NOT CHECK", "unresolved"
		switch c.outcome {
		case "PASS":
			label, class = "WORKING", "pass"
		case "FAIL":
			label, class = "BROKEN", "fail"
		}
		want.WriteString(`<div class="attempt"><span class="mono">` + c.changes + `</span><b class="attempt-` + class + `">` + label + `</b><small>` + c.want + `</small></div>`)
	}
	incidentTestObj(t, a, "minimization").Set("attempts", jsjson.MakeArray(items))
	incidentTestObj(t, a, "minimization").Set("candidate",
		incidentTestStrings("settlement-display-default", "settlement-display-boundary", "unmapped <x>", "partial-renderer-hunk"))
	page := incidentTestRender(t, a)
	got := incidentTestSection(t, page, incidentTestAttemptStart, incidentTestScrollEnd)
	if got != want.String() {
		t.Errorf("attempt rows: %s", incidentTestFirstDiff(got, want.String()))
	}
	if !strings.Contains(page, `<p class="mono">display default change + settlement boundary change + unmapped &lt;x&gt; + incomplete change (could not apply)</p>`) {
		t.Error("candidate label differs")
	}
}

func TestIncidentClaimMappings(t *testing.T) {
	const (
		passedBefore = "The approved test passed before Cedar step 5 and failed afterward."
		firstStable  = "Cedar step 5 is the first clear place the test flips from working to broken."
		twoEdit      = "Two specific changes are enough to break the working version, and both are needed to keep the failure."
		mixed        = "It looks like settlement currency and display currency got mixed together at that point."
		intent       = "We do not know whether anyone meant to make that tradeoff \u2014 the tests do not show intent."
		cached       = "This demo replay did not fully prove a working-to-broken change."
		noStable     = "This demo could not certify exactly where the bug starts."
		counter      = "These demo checks are helpful, but they are not a full proof package."
	)
	kinds := []struct{ kind, label string }{
		{"EXECUTED", "Measured"}, {"DERIVED", "Confirmed"}, {"INFERRED", "Possible clue"}, {"UNKNOWN", "Not shown"},
	}
	claims := []struct{ statement, want string }{
		{"The test passes before and fails after.", passedBefore},
		{"PASSES BEFORE AND FAILS AFTER", passedBefore},
		{"first stable pass-to-fail", firstStable},
		{"The two-edit candidate is sufficient", twoEdit},
		{"Settlement and display-currency mix", mixed},
		{"Whether an agent intended it", intent},
		{"Cached replay does not establish a transition", cached},
		{"No stable boundary is certified", noStable},
		{"Cached counterfactual results are not a proof", counter},
		{"passes before and fails after; first stable pass-to-fail", passedBefore},
		{"Cached replay does not establish; Settlement and display-currency", mixed},
		{"plain statement", "plain statement"},
		{"", ""},
		{"first stable pass\u2013to\u2013fail", "first stable pass\u2013to\u2013fail"},
		{"FIRST STABLE PASS-TO-FAIL", firstStable},
		{"pa\u017f\u017fes before and fails after", "pa\u017f\u017fes before and fails after"},
		{incidentTestWS + "first stable pass-to-fail" + incidentTestWS, firstStable},
		{"<script>not markup</script>", "&lt;script&gt;not markup&lt;/script&gt;"},
		{`<>&"'`, `&lt;&gt;&amp;&quot;&#039;`},
	}
	a := incidentTestReplay(t)
	items := make([]jsjson.Value, len(claims))
	var want strings.Builder
	for i, c := range claims {
		k := kinds[i%len(kinds)]
		items[i] = jsjson.MustObject("kind", jsjson.MakeString(k.kind), "statement", jsjson.MakeString(c.statement),
			"evidenceIds", incidentTestStrings("evidence-1"))
		want.WriteString(`<div class="evidence-row"><span>` + k.label + `</span><p>` + c.want + `</p></div>`)
	}
	incidentTestObj(t, a).Set("claims", jsjson.MakeArray(items))
	page := incidentTestRender(t, a)
	got := incidentTestSection(t, page, incidentTestClaimStart, incidentTestScrollEnd)
	if got != want.String() {
		t.Errorf("claim rows: %s", incidentTestFirstDiff(got, want.String()))
	}
}

func TestIncidentEscapingAndWTF8(t *testing.T) {
	// Hostile strings: five-character escape, ampersand first, no raw markup, one script element.
	a := incidentTestReplay(t)
	incidentTestObj(t, a, "fixture").Set("title", jsjson.MakeString(`title <>&"'`+"\x00"))
	incidentTestObj(t, a).Set("timelineRuns", incidentTestPassRuns(t, a, `'-- </script><script>alert(1)</script>`, "&#39;&quot;", "&amp; &lt;"))
	page := incidentTestRender(t, a)
	if !strings.Contains(page, "<title>FaultLine "+incidentTestEmDash+" title &lt;&gt;&amp;&quot;&#039;\x00</title>") {
		t.Error("title escaping differs")
	}
	got := incidentTestSection(t, page, incidentTestTimelineStart, incidentTestScrollEnd)
	want := incidentTestPassRow("&#039;-- &lt;/script&gt;&lt;script&gt;alert(1)&lt;/script&gt;") +
		incidentTestPassRow("&amp;#39;&amp;quot;") + incidentTestPassRow("&amp;amp; &amp;lt;")
	if got != want {
		t.Errorf("hostile timeline labels: %s", incidentTestFirstDiff(got, want))
	}
	if strings.Count(page, "<script>") != 1 || strings.Count(page, "</script>") != 1 {
		t.Error("hostile input created extra script elements")
	}

	// Lone surrogates survive as WTF-8 in the returned string and become U+FFFD only at transport.
	lone := jsstr.FromUTF16([]uint16{'a', 0xD800, 'b', 0xDC00, 0xD800, 'c', 0xD83D})
	pair := "ok\U0001F680 valid"
	a = incidentTestReplay(t)
	incidentTestObj(t, a, "fixture").Set("title", jsjson.MakeString(lone+pair))
	page = incidentTestRender(t, a)
	if !strings.Contains(page, "<title>FaultLine "+incidentTestEmDash+" "+lone+pair+"</title>") {
		t.Error("lone surrogates were not preserved in the internal string")
	}
	if utf8.ValidString(page) {
		t.Error("returned page should still be WTF-8 (invalid UTF-8) while lone surrogates are present")
	}
	out := jsstr.ToUTF8(page)
	if !utf8.ValidString(out) || !strings.Contains(out, "a\ufffdb\ufffd\ufffdc\ufffd"+pair) {
		t.Error("UTF-8 transport encoding differs (one U+FFFD per lone surrogate)")
	}
}
