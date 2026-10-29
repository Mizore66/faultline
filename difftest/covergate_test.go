//go:build faultline_test

package difftest

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The coverage gate keys error checks by position, where
// TestGoldensReachEveryErrorLiteral keys them by text: a literal counts as
// reached there when another check prints the same or a longer text
// (round 5, §8 F10). With FAULTLINE_COVERAGE_GATE=1, TestMain builds fl with
// -cover over the gated packages (gatedDirs) and runs the golden and writer
// replays with GOCOVERDIR set; after a full run (no -test.run), every
// statement in a gated package that builds an error literal must have run in
// some golden or writer case, unless the literal is listed in
// unreachableLiterals, its line carries raceOnlyMarker, or its file is
// live-gated. cmd/fl is listed too: a binary whose main package is not
// instrumented writes no coverage data at all.
var coverPkgs = func() string {
	pkgs := []string{"github.com/Mizore66/faultline/cmd/fl"}
	for _, dir := range gatedDirs {
		pkgs = append(pkgs, "github.com/Mizore66/faultline/"+dir+"/...")
	}
	return strings.Join(pkgs, ",")
}()

func coverageGate(repo, coverDir string) error {
	profile := filepath.Join(coverDir, "profile.txt")
	cmd := exec.Command("go", "tool", "covdata", "textfmt", "-i="+coverDir, "-o="+profile)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go tool covdata: %v: %s", err, out)
	}
	blocks := map[string][]coverBlock{} // by file path relative to the module
	f, err := os.Open(profile)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	const module = "github.com/Mizore66/faultline/"
	for scanner.Scan() {
		line := scanner.Text()
		// github.com/…/file.go:12.3,14.5 2 1
		name, rest, ok := strings.Cut(line, ":")
		if !ok || !strings.HasPrefix(name, module) {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) != 3 {
			continue
		}
		span := strings.Split(fields[0], ",")
		start, _ := strconv.Atoi(strings.Split(span[0], ".")[0])
		end, _ := strconv.Atoi(strings.Split(span[1], ".")[0])
		count, _ := strconv.Atoi(fields[2])
		rel := strings.TrimPrefix(name, module)
		blocks[rel] = append(blocks[rel], coverBlock{start, end, count})
	}
	if len(blocks) == 0 {
		return fmt.Errorf("coverage gate: the profile has no blocks of the gated packages")
	}
	var sites []errorSite
	for _, dir := range gatedDirs {
		found, err := errorSites(filepath.Join(repo, filepath.FromSlash(dir)))
		if err != nil {
			return err
		}
		sites = append(sites, found...)
	}
	// FAULTLINE_COVER_ONLY="internal/repair internal/attest" judges just
	// those packages: a bounded run of the cases that reach them cannot
	// judge the others.
	only := strings.Fields(strings.ReplaceAll(os.Getenv("FAULTLINE_COVER_ONLY"), ",", " "))
	if err := refuseNarrowedGate(only); err != nil {
		return err
	}
	var missed bytes.Buffer
	for _, m := range judgeSites(repo, sites, blocks, only, sourceLine, unreachableSites) {
		missed.WriteString(m)
	}
	if missed.Len() > 0 {
		return fmt.Errorf("coverage gate: add a mutation template that reaches each of these, or list it in unreachableLiterals with the reason\n%s", missed.String())
	}
	return nil
}

// siteKey names one error site exempted by unreachableSites: the file
// (relative to the module root) and the exact literal.
type siteKey struct{ file, text string }

// unreachableSites exempts single error sites, by file and literal, whose
// text a golden produces elsewhere (so unreachableLiterals cannot list it)
// but whose own position no shipped fl path reaches.
//
// An entry must match exactly one error site, carry a reason, name a file
// that is not live-gated and a text unreachableLiterals does not list; one
// that a replay runs is reported as stale (judgeSites). It is not a marker:
// nothing else can be exempted by a comment.
var unreachableSites = map[siteKey]string{
	{"internal/bundle/gitproof/semantics.go", "Cannot bind an invalid Codex lifecycle ledger:"}: "unreachable from the shipped Go CLI paths: bindParsedLifecycleLedger " +
		"runs from writeGitInvestigationProofBundle with WriteOptions.LifecycleLedger, whose producers are investigateCommand (internal/cli/investigate.go), " +
		"continueIncident (internal/incident/continue.go) and, for the turn bundle, turnsCommand (internal/cli/turns.go, which calls turnproof.Write, not this one); " +
		"each reads the ledger with gitproof.ReadVerifiedCodexLifecycleLedger (ledgerio.go), which refuses an invalid ledger, and the verifier's own call " +
		"binds only after VerifyCodexLifecycleLedger returned valid; HeadHash is never nil. " +
		"The text is also printed by turnproof's BindTurnLifecycle (turn-ledger-hash), so unreachableLiterals cannot list it. " +
		"The exported WriteOptions is still open to a library caller; no TS producer is claimed",
}

// siteExempt reports whether the site (rel, text) is listed in table.
func siteExempt(table map[siteKey]string, rel, text string) bool {
	_, ok := table[siteKey{rel, text}]
	return ok
}

// validateSiteTable returns one message per entry of table that is stale,
// ambiguous, unexplained, live-gated, redundant with unreachableLiterals or
// without a twin or without a golden: sites is every error site of the gated
// packages (file paths under repo), lineOf reads a source line and goldens is
// the committed golden text.
func validateSiteTable(table map[siteKey]string, repo string, sites []errorSite, lineOf func(file string, line int) string, goldens string) []string {
	var out []string
	for key, reason := range table {
		switch {
		case strings.TrimSpace(reason) == "":
			out = append(out, fmt.Sprintf("unreachableSites %s %q has no reason", key.file, key.text))
		case liveGatedFiles[key.file] != "":
			out = append(out, fmt.Sprintf("unreachableSites %s %q: the file is live-gated", key.file, key.text))
		}
		if _, ok := unreachableLiterals[key.text]; ok {
			out = append(out, fmt.Sprintf("unreachableSites %s %q is already in unreachableLiterals", key.file, key.text))
		}
		matches := 0
		for _, s := range sites {
			if rel, _ := filepath.Rel(repo, s.file); filepath.ToSlash(rel) == key.file && s.text == key.text {
				matches++
			}
		}
		switch {
		case matches == 0:
			out = append(out, fmt.Sprintf("unreachableSites %s %q matches no error site (stale or renamed)", key.file, key.text))
		case matches > 1:
			out = append(out, fmt.Sprintf("unreachableSites %s %q matches %d error sites (ambiguous)", key.file, key.text, matches))
		}
		// The entry is for a duplicate only: another position that prints
		// the same text must exist, so the text has a producer apart from
		// the exempt site. A live-gated twin counts (its file is checked by
		// a live test, not by position); one that is itself listed or sits
		// on a race-only or unported line does not.
		twins := 0
		for _, s := range sites {
			rel, _ := filepath.Rel(repo, s.file)
			rel = filepath.ToSlash(rel)
			if s.text != key.text || rel == key.file || siteExempt(table, rel, s.text) {
				continue
			}
			if line := lineOf(s.file, s.line); strings.Contains(line, raceOnlyMarker) || strings.Contains(line, unportedCallerMarker) {
				continue
			}
			twins++
		}
		if twins == 0 {
			out = append(out, fmt.Sprintf("unreachableSites %s %q: no other unexempt, unmarked source position carries this text (an entry is for a duplicate only)", key.file, key.text))
		}
		// Whatever the twin's location, the committed goldens must produce
		// the text (as TestGoldensReachEveryErrorLiteral demands of an
		// ordinary literal): a twin that no replay prints is no proof.
		produced := true
		for _, frag := range messageFragments(key.text) {
			quoted := strconv.Quote(frag)
			produced = produced && strings.Contains(goldens, quoted[1:len(quoted)-1])
		}
		if !produced {
			out = append(out, fmt.Sprintf("unreachableSites %s %q: no committed golden produces this text", key.file, key.text))
		}
	}
	slices.Sort(out)
	return out
}

// coverBlock is one statement block of the coverage profile.
type coverBlock struct{ start, end, count int }

// judgeSites returns one message per error site of sites (restricted to the
// packages in only, when given) that no replay ran and nothing exempts,
// and per exempted site (unreachableSites) that a replay did run, whose
// exemption is then stale.
func judgeSites(repo string, sites []errorSite, blocks map[string][]coverBlock, only []string, lineOf func(file string, line int) string, table map[siteKey]string) []string {
	var out []string
	for _, s := range sites {
		rel, _ := filepath.Rel(repo, s.file)
		rel = filepath.ToSlash(rel)
		if len(only) > 0 && !slices.ContainsFunc(only, func(dir string) bool { return strings.HasPrefix(rel, strings.TrimSuffix(dir, "/")+"/") }) {
			continue
		}
		if liveGatedFiles[rel] != "" {
			continue
		}
		if _, ok := unreachableLiterals[s.text]; ok {
			continue
		}
		inBlock, covered := false, false
		for _, b := range blocks[rel] {
			if b.start <= s.line && s.line <= b.end {
				inBlock = true
				covered = covered || b.count > 0
			}
		}
		// A site in no block is a declaration (a schema's message), which
		// coverage does not instrument; TestGoldensReachEveryErrorLiteral
		// checks those by text. A site marked raceOnlyMarker on its line can
		// only be reached by a file that changes between two walks, and one
		// marked unportedCallerMarker only by a command not ported yet.
		if line := lineOf(s.file, s.line); !inBlock || strings.Contains(line, raceOnlyMarker) || strings.Contains(line, unportedCallerMarker) {
			continue
		}
		if siteExempt(table, rel, s.text) {
			if covered {
				out = append(out, fmt.Sprintf("%s:%d: %q is listed in unreachableSites but a replay ran it; drop the entry\n", rel, s.line, s.text))
			}
			continue
		}
		if !covered {
			out = append(out, fmt.Sprintf("%s:%d: no golden case runs the check that prints %q\n", rel, s.line, s.text))
		}
	}
	return out
}

// raceOnlyMarker marks an error site that only a bundle changing while it is
// verified (or a file changing between two checks of fl) can reach.
const raceOnlyMarker = "coverage gate: race only"

// unportedCallerMarker marks an error site in a shared helper that only a
// command not yet ported reaches (its text is reached elsewhere, so
// unreachableLiterals cannot list it); the comment names the command.
const unportedCallerMarker = "coverage gate: unported caller"

func sourceLine(file string, line int) string {
	b, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(b), "\n")
	if line < 1 || line > len(lines) {
		return ""
	}
	return lines[line-1]
}

// TestCoverageWindows judges the counters several bounded windows left in
// FAULTLINE_COVER_REPORT (each window: FAULTLINE_COVER_WINDOW=<dir> with a
// prebuilt -cover fl in FAULTLINE_FL_BINARY), restricted by
// FAULTLINE_COVER_ONLY. It is the local stand-in for a monolithic
// FAULTLINE_COVERAGE_GATE=1 run, and says so: it does not replace one.
func TestCoverageWindows(t *testing.T) {
	dir := os.Getenv("FAULTLINE_COVER_REPORT")
	if dir == "" {
		t.Skip("FAULTLINE_COVER_REPORT names the merged window directory")
	}
	if err := coverageGate(repoRoot(t), dir); err != nil {
		t.Fatal(err)
	}
	t.Logf("LIVE-GROUP-AUDIT coverage windows: every judged error site ran (restricted to %q)", os.Getenv("FAULTLINE_COVER_ONLY"))
}

// refuseNarrowedGate stops FAULTLINE_COVER_ONLY from narrowing a full
// FAULTLINE_COVERAGE_GATE=1 judgment: only a bounded window report
// (TestCoverageWindows) may restrict the packages.
func refuseNarrowedGate(only []string) error {
	if len(only) > 0 && os.Getenv("FAULTLINE_COVERAGE_GATE") == "1" {
		return fmt.Errorf("FAULTLINE_COVER_ONLY narrows the packages judged and cannot be combined with FAULTLINE_COVERAGE_GATE=1")
	}
	return nil
}

func TestNarrowedGateIsRefused(t *testing.T) {
	t.Setenv("FAULTLINE_COVERAGE_GATE", "1")
	if refuseNarrowedGate([]string{"internal/repair"}) == nil {
		t.Fatal("a narrowed full gate must be refused")
	}
	if refuseNarrowedGate(nil) != nil {
		t.Fatal("an unnarrowed full gate is fine")
	}
	t.Setenv("FAULTLINE_COVERAGE_GATE", "")
	if refuseNarrowedGate([]string{"internal/repair"}) != nil {
		t.Fatal("a window report may restrict")
	}
}

const semanticsInvalidLedger = "Cannot bind an invalid Codex lifecycle ledger:"

func gateSite(file, text string, line int) errorSite {
	return errorSite{text: text, file: filepath.Join("/repo", filepath.FromSlash(file)), line: line, pos: file}
}

func TestSiteExemptIsKeyedByFileAndLiteral(t *testing.T) {
	table := map[siteKey]string{{"internal/a/x.go", "long literal text"}: "reason"}
	if !siteExempt(table, "internal/a/x.go", "long literal text") {
		t.Error("the listed file and literal must be exempt")
	}
	if siteExempt(table, "internal/b/x.go", "long literal text") {
		t.Error("the same literal in another file must not be exempt")
	}
	if siteExempt(table, "internal/a/x.go", "another literal text") {
		t.Error("another literal in the same file must not be exempt")
	}
}

func TestJudgeSitesHonoursOnlyTheListedSite(t *testing.T) {
	table := map[siteKey]string{{"internal/a/x.go", "long literal text"}: "reason"}
	sites := []errorSite{gateSite("internal/a/x.go", "long literal text", 10), gateSite("internal/b/x.go", "long literal text", 10), gateSite("internal/a/x.go", "another literal text", 20)}
	blocks := map[string][]coverBlock{
		"internal/a/x.go": {{9, 11, 0}, {19, 21, 0}},
		"internal/b/x.go": {{9, 11, 0}},
	}
	got := judgeSites("/repo", sites, blocks, nil, func(string, int) string { return "" }, table)
	if len(got) != 2 || !strings.HasPrefix(got[0], "internal/b/x.go:10:") || !strings.HasPrefix(got[1], "internal/a/x.go:20:") {
		t.Errorf("only the exempt site may pass uncovered; got %q", got)
	}
}

func TestJudgeSitesRejectsAnExemptSiteThatRan(t *testing.T) {
	table := map[siteKey]string{{"internal/a/x.go", "long literal text"}: "reason"}
	sites := []errorSite{gateSite("internal/a/x.go", "long literal text", 10)}
	blocks := map[string][]coverBlock{"internal/a/x.go": {{9, 11, 3}}}
	got := judgeSites("/repo", sites, blocks, nil, func(string, int) string { return "" }, table)
	if len(got) != 1 || !strings.Contains(got[0], "is listed in unreachableSites but a replay ran it") {
		t.Errorf("an exempt site a replay ran must be reported stale; got %q", got)
	}
}

func noLine(string, int) string { return "" }

func TestValidateSiteTable(t *testing.T) {
	const text = "long literal text"
	const goldens = `{"stdout":"- x: long literal text\n"}`
	exempt := gateSite("internal/a/x.go", text, 10)
	twin := gateSite("internal/b/x.go", text, 30)
	other := gateSite("internal/c/x.go", "another literal text", 5)
	key := siteKey{"internal/a/x.go", text}
	const noTwin = "no other unexempt, unmarked source position carries this text"
	const noGolden = "no committed golden produces this text"
	cases := []struct {
		name    string
		table   map[siteKey]string
		sites   []errorSite
		goldens string
		wantSub string
	}{
		{"valid duplicate", map[siteKey]string{key: "reason"}, []errorSite{exempt, twin}, goldens, ""},
		{"stale", map[siteKey]string{key: "reason"}, []errorSite{twin}, goldens, "matches no error site"},
		{"ambiguous", map[siteKey]string{key: "reason"}, []errorSite{exempt, gateSite("internal/a/x.go", text, 40), twin}, goldens, "matches 2 error sites"},
		{"empty reason", map[siteKey]string{key: "  "}, []errorSite{exempt, twin}, goldens, "has no reason"},
		{"sole producer", map[siteKey]string{key: "reason"}, []errorSite{exempt}, goldens, noTwin},
		{"other text only", map[siteKey]string{key: "reason"}, []errorSite{exempt, other}, goldens, noTwin},
		{"only an exempt twin", map[siteKey]string{key: "reason", {"internal/b/x.go", text}: "reason"}, []errorSite{exempt, twin}, goldens, noTwin},
		{"a live-gated twin is a producer", map[siteKey]string{key: "reason"}, []errorSite{exempt, gateSite("internal/bundle/gitproof/ledger.go", text, 7)}, goldens, ""},
		{"twin without a golden text", map[siteKey]string{key: "reason"}, []errorSite{exempt, twin}, `{"stdout":"nothing"}`, noGolden},
		{"live-gated twin without a golden text", map[siteKey]string{key: "reason"}, []errorSite{exempt, gateSite("internal/bundle/gitproof/ledger.go", text, 7)}, "", noGolden},
	}
	for _, c := range cases {
		got := validateSiteTable(c.table, "/repo", c.sites, noLine, c.goldens)
		if c.wantSub == "" && len(got) != 0 || c.wantSub != "" && !strings.Contains(strings.Join(got, "\n"), c.wantSub) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.wantSub)
		}
	}
	// A twin on a race-only or unported line is not judged either.
	for _, marker := range []string{raceOnlyMarker, unportedCallerMarker} {
		marked := func(file string, line int) string {
			if strings.HasSuffix(filepath.ToSlash(file), "b/x.go") {
				return "x // " + marker
			}
			return ""
		}
		if got := validateSiteTable(map[siteKey]string{key: "reason"}, "/repo", []errorSite{exempt, twin}, marked, goldens); !strings.Contains(strings.Join(got, "\n"), noTwin) {
			t.Errorf("a %q twin must not count; got %q", marker, got)
		}
	}
	// The entry's own file must not be live-gated.
	live := siteKey{"internal/bundle/gitproof/ledger.go", text}
	if got := validateSiteTable(map[siteKey]string{live: "reason"}, "/repo", []errorSite{gateSite(live.file, text, 1), twin}, noLine, goldens); !strings.Contains(strings.Join(got, "\n"), "the file is live-gated") {
		t.Errorf("live-gated entry file: got %q", got)
	}
	dup := siteKey{"internal/a/x.go", "Git proof bundle directory does not exist"}
	if got := validateSiteTable(map[siteKey]string{dup: "reason"}, "/repo", []errorSite{gateSite(dup.file, dup.text, 10), gateSite("internal/b/x.go", dup.text, 3)}, noLine, dup.text); !strings.Contains(strings.Join(got, "\n"), "is already in unreachableLiterals") {
		t.Errorf("a text listed in unreachableLiterals: got %q", got)
	}
}

// TestUnreachableSitesTable checks the committed table against the source.
func TestUnreachableSitesTable(t *testing.T) {
	repo := repoRoot(t)
	var sites []errorSite
	for _, dir := range gatedDirs {
		found, err := errorSites(filepath.Join(repo, filepath.FromSlash(dir)))
		if err != nil {
			t.Fatal(err)
		}
		sites = append(sites, found...)
	}
	for _, m := range validateSiteTable(unreachableSites, repo, sites, sourceLine, goldenText(t, repo)) {
		t.Error(m)
	}
	key := siteKey{"internal/bundle/gitproof/semantics.go", semanticsInvalidLedger}
	if reason := unreachableSites[key]; !strings.Contains(reason, "ReadVerifiedCodexLifecycleLedger") || !strings.Contains(reason, "internal/cli/investigate.go") {
		t.Errorf("the invalid-ledger guard must be listed with its CLI producers; reason %q", reason)
	}
	// The real twin and the golden that proves the text are pinned.
	var twinFiles []string
	for _, st := range sites {
		if rel, _ := filepath.Rel(repo, st.file); st.text == semanticsInvalidLedger && filepath.ToSlash(rel) != key.file {
			twinFiles = append(twinFiles, filepath.ToSlash(rel))
		}
	}
	if !slices.Equal(twinFiles, []string{"internal/bundle/turnproof/semantics.go"}) {
		t.Errorf("the invalid-ledger text must have exactly one twin, turnproof/semantics.go; got %v", twinFiles)
	}
	golden, err := os.ReadFile(filepath.Join(repo, "difftest", "testdata", "golden", "turn-two-states.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(golden), "lifecycle ledger cannot bind to the investigation: "+semanticsInvalidLedger) {
		t.Error("golden/turn-two-states.jsonl must hold the verifier prefix and the invalid-ledger text")
	}
	if len(unreachableSites) != 1 {
		t.Errorf("unreachableSites holds %d entries; one is approved", len(unreachableSites))
	}
}
