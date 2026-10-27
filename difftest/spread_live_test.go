//go:build faultline_test

package difftest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/bundle/turnproof"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/schema"
	"github.com/Mizore66/faultline/internal/turninv"
)

// V8 spreads call arguments on the machine stack, so a list spread into a
// call (errors.push(...list)) overflows it past a limit that depends on the
// platform and the call site (internal/jsjson/stack.go). Check each modelled
// site at its limit and one past it through `node dist/cli.js verify` and
// through fl; on a mismatch, bisect TS's limit and report it.
func TestLiveSpreadLimitsMatchNode(t *testing.T) {
	oracle.Start(t).Close() // skip unless FAULTLINE_NODE_ORACLE=1
	repo, _ := filepath.Abs("..")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	engines := map[string][]string{"TS": {node, filepath.Join(repo, "dist", "cli.js")}, "Go": {flBinary}}
	overflows := func(argv []string, dir string) bool {
		cmd := exec.Command(argv[0], append(argv[1:], "verify", dir)...)
		out, _ := cmd.Output()
		if !strings.Contains(string(out), "self-consistency") {
			t.Fatalf("unexpected output %.300q", out)
		}
		return strings.Contains(string(out), "Maximum call stack size exceeded")
	}
	sites := []struct {
		name  string
		limit int
		build func(dir string, n int) // bundle whose spread at the site has n elements
	}{
		{"demo walker, depth 1", demoWalkerLimit(1), buildWalkerBundle},
		{"git semantics", spreadLimit(func(n int) bool { return jsjson.SpreadFits(n, jsjson.SpreadGitSemantics) }), buildSemanticsBundle},
		{"turn semantics", spreadLimit(func(n int) bool { return jsjson.SpreadFits(n, jsjson.SpreadTurnSemantics) }), buildTurnSemanticsBundle},
		{"git ledger", spreadLimit(func(n int) bool { return jsjson.SpreadFits(n, jsjson.SpreadGitLedger) }), buildLedgerBundle},
	}
	for _, site := range sites {
		t.Run(site.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "b")
			check := func(argv []string, n int) bool {
				site.build(dir, n)
				return overflows(argv, dir)
			}
			for name, argv := range engines {
				if check(argv, site.limit) || !check(argv, site.limit+1) {
					lo, hi := site.limit-20000, site.limit+20000
					for hi-lo > 1 {
						m := (lo + hi) / 2
						if check(argv, m) {
							hi = m
						} else {
							lo = m
						}
					}
					t.Errorf("%s on %s/%s: last length that passes is %d, model says %d", name, runtime.GOOS, runtime.GOARCH, lo, site.limit)
				}
			}
		})
	}
}

// spreadLimit is the last n the model lets through.
func spreadLimit(fits func(int) bool) int {
	lo, hi := 1, 1<<20
	for hi-lo > 1 {
		m := (lo + hi) / 2
		if fits(m) {
			lo = m
		} else {
			hi = m
		}
	}
	return lo
}

func demoWalkerLimit(depth int) int {
	return spreadLimit(func(n int) bool { return jsjson.CollectSpreadFits(n, depth) })
}

// buildWalkerBundle is demo-replay with n empty files in sub/, which the
// walker spreads into the root's list.
func buildWalkerBundle(dir string, n int) {
	if _, err := os.Stat(dir); err != nil {
		copyTreeNoT(filepath.Join("testdata", "bases", "demo-replay"), dir)
		os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "sub"))
	have := len(entries)
	for ; have < n; have++ {
		os.WriteFile(filepath.Join(dir, "sub", strconv.FormatInt(int64(have), 36)), nil, 0o644)
	}
	for ; have > n; have-- {
		os.Remove(filepath.Join(dir, "sub", strconv.FormatInt(int64(have-1), 36)))
	}
}

// buildSemanticsBundle is git-two-states with n-2 extra contiguous states
// in investigation.json: n semantic errors.
func buildSemanticsBundle(dir string, n int) {
	os.RemoveAll(dir)
	copyTreeNoT(filepath.Join("testdata", "bases", "git-two-states"), dir)
	path := filepath.Join(dir, "investigation.json")
	inv, _ := parseObjectFile(path)
	states := inv.Field("states").Items()
	base := len(states)
	for i := range n - 2 {
		h := fmt.Sprintf("e%039x", i+1)
		s := jsjson.NewObj()
		s.Set("index", jsjson.MakeNumber(float64(base+i)))
		s.Set("commit", jsjson.MakeString(h))
		s.Set("tree", jsjson.MakeString(h))
		states = append(states, jsjson.MakeObject(s))
	}
	inv.Set("states", jsjson.MakeArray(states))
	writeJSON(path, jsjson.MakeObject(inv))
	rehash(dir, "git-two-states")
}

// turnSemanticsFixed is the number of semantic errors the extra
// environment fingerprints do not add one-for-one (the length mismatch):
// measured once from the Go port, whose errors the oracle matches in
// TestLiveTurnSemantics.
var turnSemanticsFixed = -1

// buildTurnSemanticsBundle is turn-two-states with extra environment
// fingerprints in investigation.json, rehashed: each has no runtimeMapping
// entry and no state, so it adds exactly one semantic error ("runtimeMapping
// is missing fingerprint"). (Extra states would each spawn git cat-file in
// the pack check; extra runs would pass the 128 MiB artifact limit.)
func buildTurnSemanticsBundle(dir string, n int) {
	build := func(dir string, extra int) {
		os.RemoveAll(dir)
		copyTreeNoT(filepath.Join("testdata", "bases", "turn-two-states"), dir)
		path := filepath.Join(dir, "investigation.json")
		inv, _ := parseObjectFile(path)
		fingerprints := inv.Field("environmentFingerprints").Items()
		for i := range extra {
			f := jsjson.NewObj()
			f.Set("schemaVersion", jsjson.MakeString("faultline.environment-fingerprint.v2"))
			f.Set("files", jsjson.MakeObject(jsjson.NewObj()))
			f.Set("digest", jsjson.MakeString("sha256:"+strings.Repeat("0", 48)+fmt.Sprintf("%016x", i+1)))
			fingerprints = append(fingerprints, jsjson.MakeObject(f))
		}
		inv.Set("environmentFingerprints", jsjson.MakeArray(fingerprints))
		writeCanonical(path, jsjson.MakeObject(inv))
		rehash(dir, "turn-two-states")
	}
	if turnSemanticsFixed < 0 {
		parent, err := os.MkdirTemp("", "faultline-turn-spread-probe-")
		if err != nil {
			panic(err)
		}
		defer os.RemoveAll(parent)
		probe := filepath.Join(parent, "b")
		build(probe, 1)
		inv, _ := parseObjectFile(filepath.Join(probe, "investigation.json"))
		result, _, ok := schema.Parse(turninv.ResultSchema, jsjson.MakeObject(inv))
		if !ok {
			panic("probe investigation does not parse")
		}
		frozenText, _ := os.ReadFile(filepath.Join(probe, "witness", "frozen.json"))
		frozenValue, _ := jsjson.Parse(string(frozenText))
		frozen, _, _ := schema.Parse(gitproof.FrozenWitnessSchema, frozenValue)
		turnSemanticsFixed = len(turnproof.ValidateProofSemantics(result, frozen)) - 1
	}
	build(dir, n-turnSemanticsFixed)
}

// buildLedgerBundle is git-fully-bound with n copies of the SESSION_ENDED
// event chained after it: n "occurs after SESSION_ENDED" errors.
func buildLedgerBundle(dir string, n int) {
	os.RemoveAll(dir)
	copyTreeNoT(filepath.Join("testdata", "bases", "git-fully-bound"), dir)
	path := filepath.Join(dir, "lifecycle", "ledger.json")
	ledger, _ := parseObjectFile(path)
	events := ledger.Field("events").Items()
	last := events[len(events)-1]
	seq := int(last.Get("sequence").Num())
	for i := range n {
		e := cloneJSON(last)
		e.Obj().Set("sequence", jsjson.MakeNumber(float64(seq+i+1)))
		e.Obj().Set("eventId", jsjson.MakeString(fmt.Sprintf("event-%08x-0000-4000-8000-000000000000", i)))
		events = append(events, e)
	}
	ledger.Set("events", jsjson.MakeArray(events))
	resignLedgerJSON(jsjson.MakeObject(ledger))
	writeJSON(path, jsjson.MakeObject(ledger))
	rehash(dir, "git-fully-bound")
}

// Node 22's rmSync({recursive: true}) is the JS rimrafSync, which recurses
// on V8's stack: an entry deeper than jsjson.RimrafDepth below the git
// verifier's temporary bare repository overflows it in the finally block.
// A reference-transaction hook builds the chain during fetch.
func TestLiveRimrafDepthMatchesNode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell hook")
	}
	oracle.Start(t).Close() // skip unless FAULTLINE_NODE_ORACLE=1
	repo, _ := filepath.Abs("..")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	hooks := t.TempDir()
	hook := "#!/bin/sh\n[ \"$1\" = committed ] || exit 0\n[ -n \"$DEEPN\" ] || exit 0\n" +
		"i=1; while [ $i -lt $DEEPN ]; do mkdir d && cd d || exit 0; i=$((i+1)); done; touch f\n"
	os.WriteFile(filepath.Join(hooks, "reference-transaction"), []byte(hook), 0o755)
	config := filepath.Join(t.TempDir(), "gitconfig")
	os.WriteFile(config, []byte("[core]\n\thooksPath = "+hooks+"\n"), 0o644)
	bundle := filepath.Join(repo, "difftest", "testdata", "bases", "git-two-states")
	// depth is the depth of the leaf file below the bare repository.
	run := func(argv []string, depth int) string {
		cmd := exec.Command(argv[0], append(argv[1:], "verify", bundle)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+config, "DEEPN="+strconv.Itoa(depth))
		out, _ := cmd.CombinedOutput()
		return string(out)
	}
	engines := map[string][]string{"TS": {node, filepath.Join(repo, "dist", "cli.js")}, "Go": {flBinary}}
	limit := jsjson.RimrafDepth
	if runtime.GOOS == "darwin" {
		t.Skip("PATH_MAX stops the chain before V8's stack overflows")
	}
	for name, argv := range engines {
		ok, over := run(argv, limit), run(argv, limit+1)
		if !strings.Contains(ok, "self-consistency: VALID") || !strings.Contains(over, "Maximum call stack size exceeded") {
			lo, hi := limit-50, limit+50
			for hi-lo > 1 {
				m := (lo + hi) / 2
				if strings.Contains(run(argv, m), "Maximum call stack size exceeded") {
					hi = m
				} else {
					lo = m
				}
			}
			t.Errorf("%s on %s/%s: deepest entry removed is %d, model says %d\nat model: %.300q\none past: %.300q",
				name, runtime.GOOS, runtime.GOARCH, lo, limit, ok, over)
		}
	}
}

func copyTreeNoT(from, to string) {
	filepath.WalkDir(from, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		dest := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, b, 0o644)
	})
}
