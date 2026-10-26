//go:build faultline_test

package difftest

import (
	"encoding/json"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Mizore66/faultline/difftest/gen"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/ledgerbind"
	"github.com/Mizore66/faultline/internal/safegit"
	"github.com/Mizore66/faultline/internal/schema"
	"github.com/Mizore66/faultline/internal/witnessresult"
)

// seedTexts reads files relative to difftest/testdata/bases.
func seedTexts(t *testing.T, rels ...string) []string {
	bases := filepath.Join(oracle.RepoRoot(t), "difftest", "testdata", "bases")
	var out []string
	for _, rel := range rels {
		raw, err := os.ReadFile(filepath.Join(bases, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(raw))
	}
	return out
}

func canonicalOf(v jsjson.Value) string {
	s, err := canonical.CanonicalJSON(v)
	if err != nil {
		return "ERROR:" + err.Error()
	}
	return s
}

// liveCompare calls an oracle op that returns a string (canonical JSON, or
// "THREW:<message>" when TS threw) and compares it with the Go result. Inputs
// are deduplicated: it runs until liveCases distinct inputs were compared (or
// gives up after 5x attempts) and logs how many there were. It returns every
// TS result, so callers can check which messages were reached.
func liveCompare(t *testing.T, op string, next func(c *oracle.Client, r *rand.Rand, i int) (args, got string)) string {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(15, uint64(len(op))))
	seen := map[string]bool{}
	threw := 0
	var outputs strings.Builder
	for i := 0; len(seen) < liveCases && i < 5*liveCases; i++ {
		args, got := next(c, r, i)
		if seen[args] {
			continue
		}
		seen[args] = true
		raw, err := c.Call(op, args)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := jsjson.Parse(raw)
		outputs.WriteString(want.Str() + "\n")
		if strings.HasPrefix(want.Str(), "THREW:") {
			threw++
			continue
		}
		if got != want.Str() {
			t.Fatalf("%s(%s):\ngot  %s\nwant %s", op, args, got, want.Str())
		}
	}
	t.Logf("%s: %d distinct inputs (%d where TS threw)", op, len(seen), threw)
	return outputs.String()
}

// requireReached fails for each error literal in file (relative to the
// module root) that neither a TS result in outputs nor a committed golden
// produced: the live test is the golden gate for that file (liveGatedFiles
// in coverage_test.go).
func requireReached(t *testing.T, file, outputs string) {
	t.Helper()
	if liveGatedFiles[file] == "" {
		t.Errorf("%s is not in liveGatedFiles", file)
	}
	repo := oracle.RepoRoot(t)
	// FAULTLINE_DUMP_LIVE_OUTPUTS=<dir> keeps what the live test compared,
	// to audit unreachableLiterals against it.
	if dir := os.Getenv("FAULTLINE_DUMP_LIVE_OUTPUTS"); dir != "" {
		name := strings.ReplaceAll(file, "/", "_") + ".txt"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(outputs), 0o644); err != nil {
			t.Error(err)
		}
	}
	outputs += goldenText(t, repo)
	for lit, pos := range errorLiterals(t, filepath.Join(repo, filepath.FromSlash(file))) {
		if _, ok := unreachableLiterals[lit]; ok {
			continue
		}
		reached := true
		for _, frag := range messageFragments(lit) {
			reached = reached && strings.Contains(outputs, jsjson.Quote(frag)[1:len(jsjson.Quote(frag))-1])
		}
		if !reached {
			t.Errorf("%s: no generated input reached %q", pos, lit)
		}
	}
}

// liveSummary logs how much a long live test compared, marked so that CI
// turns it into a check-run notice (proof that the test ran in full).
func liveSummary(t *testing.T, format string, args ...any) {
	t.Helper()
	top, _, _ := strings.Cut(t.Name(), "/")
	t.Logf("LIVE-SUMMARY %s: "+format, append([]any{top}, args...)...)
}

// liveOutputs collects, per top-level test, the TS results a live test
// compared (recordOutput), for requireReached.
var (
	liveOutputsMu sync.Mutex
	liveOutputs   = map[string]*strings.Builder{}
)

// recordOutput adds a compared TS result to t's top-level test's outputs.
func recordOutput(t *testing.T, s string) {
	top, _, _ := strings.Cut(t.Name(), "/")
	liveOutputsMu.Lock()
	defer liveOutputsMu.Unlock()
	b := liveOutputs[top]
	if b == nil {
		b = &strings.Builder{}
		liveOutputs[top] = b
	}
	b.WriteString(s)
	b.WriteByte('\n')
}

// recordedOutputs is what recordOutput collected for t's top-level test.
func recordedOutputs(t *testing.T) string {
	top, _, _ := strings.Cut(t.Name(), "/")
	liveOutputsMu.Lock()
	defer liveOutputsMu.Unlock()
	if b := liveOutputs[top]; b != nil {
		return b.String()
	}
	return ""
}

// seededEpochMillis is the seeded clock's epoch (difftest/gen/seed.mjs).
const seededEpochMillis = 1784203200000

// oracleCall calls an oracle op and returns its raw JSON result.
func oracleCall(t *testing.T, c *oracle.Client, op, args string) string {
	raw, err := c.Call(op, args)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// oracleText calls an oracle op that returns a string.
func oracleText(t *testing.T, c *oracle.Client, op, args string) string {
	raw, err := c.Call(op, args)
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsjson.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return v.Str()
}

func TestLiveWitness(t *testing.T) {
	seeds := seedTexts(t, "git-unbound/witness/frozen.json", "git-sample-self-incident/witness/frozen.json")
	outputs := liveCompare(t, "verifyFrozenWitnessRecord", func(_ *oracle.Client, r *rand.Rand, i int) (string, string) {
		text := seeds[r.IntN(len(seeds))]
		if i >= len(seeds) {
			text = gen.CorruptDeep(r, text)
		}
		value, _ := jsjson.Parse(text)
		expected, provided := "", false
		switch r.IntN(4) {
		case 1:
			expected, provided = value.Get("frozenDigest").Str(), true
		case 2:
			expected, provided = "nope", true
		case 3: // well formed, but not this record's digest
			expected, provided = "sha256:"+strings.Repeat("0", 64), true
		}
		args := `{"text":` + jsjson.Quote(text)
		if provided {
			args += `,"expected":` + jsjson.Quote(expected)
		}
		return args + "}", canonicalOf(gitproof.VerifyFrozenWitnessRecord(value, expected, provided).JSON())
	})
	requireReached(t, "internal/bundle/gitproof/witness.go", outputs+liveWitnessOverlays(t))
}

// liveWitnessOverlays compares multi-overlay witnesses built by frozen TS
// (proposeWitness sorts overlay paths with localeCompare), then reordered,
// or given a case-alias duplicate, and re-signed, so the overlay order and
// duplicate checks run on real collation-sensitive paths. Paths mix scripts,
// contractions, marks and case variants. It returns every TS result.
func liveWitnessOverlays(t *testing.T) string {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(21, 4))
	var outputs strings.Builder
	built, compared := 0, 0
	for round := 0; round < 400; round++ {
		var paths []string
		for _, key := range gen.RandomKeys(r) {
			if key != "" && !strings.ContainsAny(key, "/\\:*?\"<>|\x00") && strings.TrimSpace(key) == key {
				paths = append(paths, key+".md")
			}
			if len(paths) == 2+r.IntN(7) {
				break
			}
		}
		if len(paths) < 2 {
			continue
		}
		args, _ := json.Marshal(map[string]any{"paths": paths})
		raw, err := c.Call("makeWitness", string(args))
		if err != nil {
			continue // TS rejects the paths (unsafe or case aliases)
		}
		built++
		text, _ := jsjson.Parse(raw)
		variants := []string{text.Str()}
		// Adjacent swap and full reversal of the overlays, re-signed.
		frozen, _ := jsjson.Parse(text.Str())
		overlays := frozen.Get("proposal", "witness", "overlays").Items()
		i := r.IntN(len(overlays) - 1)
		swapped := slices.Clone(overlays)
		swapped[i], swapped[i+1] = swapped[i+1], swapped[i]
		reversed := slices.Clone(overlays)
		slices.Reverse(reversed)
		alias := slices.Clone(overlays)
		dup, _ := jsjson.Parse(jsjson.Stringify(alias[1]))
		dup.Obj().Set("path", jsjson.MakeString(strings.ToUpper(alias[0].Get("path").Str())))
		alias[1] = dup
		for _, order := range [][]jsjson.Value{swapped, reversed, alias} {
			frozen.Get("proposal", "witness").Obj().Set("overlays", jsjson.MakeArray(order))
			variants = append(variants, oracleText(t, c, "resignWitness", `{"text":`+jsjson.Quote(jsjson.Stringify(frozen))+"}"))
		}
		for _, v := range variants {
			value, _ := jsjson.Parse(v)
			for _, expected := range []string{value.Get("frozenDigest").Str(), "sha256:" + strings.Repeat("0", 64)} {
				want := oracleText(t, c, "verifyFrozenWitnessRecord", `{"text":`+jsjson.Quote(v)+`,"expected":`+jsjson.Quote(expected)+"}")
				got := canonicalOf(gitproof.VerifyFrozenWitnessRecord(value, expected, true).JSON())
				if got != want {
					t.Fatalf("verifyFrozenWitnessRecord(%s, %s):\ngot  %s\nwant %s", v, expected, got, want)
				}
				outputs.WriteString(want + "\n")
				compared++
			}
		}
	}
	t.Logf("%d multi-overlay witnesses built by TS, %d verifications compared", built, compared)
	return outputs.String()
}

// Ledger seeds: the two committed ledgers plus a TS-built one with every
// event type (baseline and turn snapshots, tool use). Half of the mutated
// ledgers are re-signed by TS so the semantic checks run behind a valid chain.
var ledgerDigest = regexp.MustCompile(`"digest":"sha256:[0-9a-f]{64}"`)

func TestLiveLedger(t *testing.T) {
	seeds := seedTexts(t, "git-partially-bound/lifecycle/ledger.json", "git-fully-bound/lifecycle/ledger.json")
	var rich string
	outputs := liveCompare(t, "verifyCodexLifecycleLedger", func(c *oracle.Client, r *rand.Rand, i int) (string, string) {
		if rich == "" {
			rich = oracleText(t, c, "richLedger", "{}")
			seeds = append(seeds, rich)
			// A tool name outside the allowed characters, re-signed, for
			// the schema's toolName pattern.
			badTool := strings.Replace(rich, `"toolName":"shell"`, `"toolName":"sh ell!"`, 1)
			seeds = append(seeds, oracleText(t, c, "resignLedger", `{"text":`+jsjson.Quote(badTool)+"}"))
		}
		text := seeds[r.IntN(len(seeds))]
		if i >= len(seeds) {
			if digests := ledgerDigest.FindAllStringIndex(text, -1); len(digests) > 0 && r.IntN(8) == 0 {
				// One checkpoint or snapshot digest that no longer matches
				// its contents, behind a re-signed chain (round 6: no input
				// reached those two checks).
				at := digests[r.IntN(len(digests))]
				text = text[:at[0]] + `"digest":"sha256:` + strings.Repeat("1", 64) + `"` + text[at[1]:]
				text = oracleText(t, c, "resignLedger", `{"text":`+jsjson.Quote(text)+"}")
			} else if r.IntN(2) == 0 {
				// A structural event change behind a re-signed chain
				// reaches the lifecycle state machine.
				for n := r.IntN(2); n >= 0; n-- {
					text = gen.MutateLedgerEvents(r, text)
				}
				text = oracleText(t, c, "resignLedger", `{"text":`+jsjson.Quote(text)+"}")
			} else {
				for n := r.IntN(2); n >= 0; n-- {
					text = gen.CorruptDeep(r, text)
				}
				if r.IntN(2) == 0 {
					text = oracleText(t, c, "resignLedger", `{"text":`+jsjson.Quote(text)+"}")
				}
			}
		}
		value, _ := jsjson.Parse(text)
		return `{"text":` + jsjson.Quote(text) + "}", canonicalOf(gitproof.VerifyCodexLifecycleLedger(value).JSON())
	})
	requireReached(t, "internal/bundle/gitproof/ledger.go", outputs)
	if !strings.Contains(rich, "TURN_TREE_SNAPSHOT") || !strings.Contains(rich, "TOOL_USE_STARTED") || !strings.Contains(rich, "SESSION_BASELINE_SNAPSHOT") {
		t.Fatalf("rich ledger seed lacks snapshot or tool-use events")
	}
}

// Ledger-bound investigations: the record `fl ledger bind` wrote, then
// deeply corrupted copies. Half are re-signed by TS (bindingDigest
// recomputed) so the ledger, investigation and state-binding checks run
// behind a matching digest. The expected digest is absent, the record's own,
// malformed, or a well-formed digest of something else.
func TestLiveLedgerBinding(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(oracle.RepoRoot(t), "difftest", "testdata", "scenarios", "ledger-bound", "tree", "bound.json"))
	if err != nil {
		t.Fatal(err)
	}
	seed := string(raw)
	var extras []string
	outputs := liveCompare(t, "verifyLedgerBoundInvestigation", func(c *oracle.Client, r *rand.Rand, i int) (string, string) {
		text := seed
		if extras == nil {
			extras = ledgerBoundDuplicates(t, c, seed)
		}
		if i > 0 && i <= len(extras) {
			text = extras[i-1]
		} else if i > 0 {
			text = gen.CorruptDeep(r, text)
			if r.IntN(2) == 0 {
				text = oracleText(t, c, "resignBound", `{"text":`+jsjson.Quote(text)+"}")
			}
		}
		value, _ := jsjson.Parse(text)
		expected, provided := "", false
		switch r.IntN(4) {
		case 1:
			expected, provided = value.Get("bindingDigest").Str(), true
		case 2:
			expected, provided = "nope", true
		case 3:
			expected, provided = "sha256:"+strings.Repeat("0", 64), true
		}
		args := `{"text":` + jsjson.Quote(text)
		if provided {
			args += `,"expected":` + jsjson.Quote(expected)
		}
		return args + "}", canonicalOf(ledgerbind.Verify(value, expected, provided))
	})
	requireReached(t, "internal/ledgerbind/ledgerbind.go", outputs)
}

// ledgerBoundDuplicates are two re-signed variants random corruption does not
// reach: the investigation's last state repeated (duplicate Git state, and a
// checkpoint that matches two states), and a checkpoint event repeated in
// the ledger (duplicate checkpoint facts).
func ledgerBoundDuplicates(t *testing.T, c *oracle.Client, seed string) []string {
	clone := func(v jsjson.Value) jsjson.Value {
		out, err := jsjson.Parse(jsjson.Stringify(v))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	states := func() string {
		v, _ := jsjson.Parse(seed)
		list := v.Get("investigation", "states").Items()
		dup := clone(list[len(list)-1])
		dup.Obj().Set("index", jsjson.MakeNumber(float64(len(list))))
		v.Get("investigation").Obj().Set("states", jsjson.MakeArray(append(slices.Clone(list), dup)))
		return jsjson.Stringify(v)
	}()
	events := func() string {
		v, _ := jsjson.Parse(seed)
		list := v.Get("ledger", "events").Items()
		for _, event := range list {
			if event.Get("event", "type").Str() == "WORKTREE_CHECKPOINT" {
				dup := clone(event)
				dup.Obj().Set("sequence", jsjson.MakeNumber(float64(len(list)+1)))
				dup.Obj().Set("eventId", jsjson.MakeString("duplicate-checkpoint"))
				v.Get("ledger").Obj().Set("events", jsjson.MakeArray(append(slices.Clone(list), dup)))
				break
			}
		}
		return jsjson.Stringify(v)
	}()
	var out []string
	for _, text := range []string{states, events} {
		out = append(out, oracleText(t, c, "resignBound", `{"text":`+jsjson.Quote(text)+"}"))
	}
	return out
}

// Sandbox seeds: the committed Docker audits plus TS-signed legacy v1,
// UNSAFE_LOCAL and non-empty environment variants. Mutations are re-signed
// half the time, and both sides run the schema first, as verify does.
func TestLiveSandbox(t *testing.T) {
	dir := filepath.Join(oracle.RepoRoot(t), "difftest", "testdata", "bases", "git-unbound", "runs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var seeds []string
	for _, e := range entries {
		raw, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		run, _ := jsjson.Parse(string(raw))
		seeds = append(seeds, jsjson.Stringify(run.Get("sandbox")))
	}
	variants := 0
	accepted := map[string]int{}
	outputs := liveCompare(t, "validateSandboxPlanAudit", func(c *oracle.Client, r *rand.Rand, i int) (string, string) {
		if variants == 0 {
			docker := seeds[0]
			for _, v := range []string{"legacy", "unsafe-local", "environment"} {
				seeds = append(seeds, oracleText(t, c, "sandboxVariant", `{"text":`+jsjson.Quote(docker)+`,"variant":"`+v+`"}`))
				variants++
			}
			// A name repeated in each environment list, re-signed (round 5,
			// §8 F3: the duplicate-name check had no input).
			for _, list := range []string{"passed", "allowedKeys", "redactedKeys", "fixedKeys"} {
				v, _ := jsjson.Parse(seeds[len(seeds)-1])
				env := v.Get("environment").Obj()
				items := env.Field(list).Items()
				if len(items) == 0 {
					continue
				}
				env.Set(list, jsjson.MakeArray(append(slices.Clone(items), items[0])))
				seeds = append(seeds, oracleText(t, c, "resignSandbox", `{"text":`+jsjson.Quote(jsjson.Stringify(v))+`,"legacy":false}`))
				variants++
			}
			// Each policy limit at its maximum and one past it, re-signed, so
			// an off-by-one comparison changes the verdict here.
			for _, limit := range []struct {
				name string
				max  float64
			}{{"timeoutMs", 300_000}, {"maxOutputBytes", 8_388_608}, {"cpuCount", 4}, {"memoryBytes", 2_147_483_648}, {"pidsLimit", 512}, {"tmpfsBytes", 536_870_912}} {
				for _, n := range []float64{limit.max, limit.max + 1} {
					v, _ := jsjson.Parse(docker)
					v.Get("runtime", "limits").Obj().Set(limit.name, jsjson.MakeNumber(n))
					seeds = append(seeds, oracleText(t, c, "resignSandbox", `{"text":`+jsjson.Quote(jsjson.Stringify(v))+`,"legacy":false}`))
					variants++
				}
			}
		}
		seed := r.IntN(len(seeds))
		if i < len(seeds) {
			seed = i // every seed once, unchanged, before the corrupted ones
		}
		text := seeds[seed]
		if i >= len(seeds) {
			for n := r.IntN(3); n >= 0; n-- {
				text = gen.CorruptLeaf(r, text)
			}
			if r.IntN(2) == 0 {
				legacy := "false"
				if r.IntN(3) == 0 {
					legacy = "true"
				}
				text = oracleText(t, c, "resignSandbox", `{"text":`+jsjson.Quote(text)+`,"legacy":`+legacy+"}")
			}
		}
		value, _ := jsjson.Parse(text)
		parsed, issues, ok := schema.Parse(gitproof.SandboxAuditSchema, value)
		if !ok {
			return `{"text":` + jsjson.Quote(text) + "}", "SCHEMA:" + schema.ErrorMessage(issues)
		}
		errs := gitproof.ValidateSandboxPlanAudit(parsed)
		if len(errs) == 0 {
			accepted[parsed.Get("kind").Str()+envTag(text)]++
		}
		items := []jsjson.Value{}
		for _, e := range errs {
			items = append(items, jsjson.MakeString(e))
		}
		return `{"text":` + jsjson.Quote(text) + "}", canonicalOf(jsjson.MakeArray(items))
	})
	requireReached(t, "internal/bundle/gitproof/sandbox.go", outputs)
	t.Logf("accepted audits among generated inputs: %v", accepted)
	for _, kind := range []string{"DOCKER_ISOLATED", "UNSAFE_LOCAL"} {
		if accepted[kind] == 0 {
			t.Errorf("no %s audit was accepted: the digest-valid variants are not reaching the acceptance path", kind)
		}
	}
}

// envTag marks audits with the non-empty environment policy variant;
// it only labels the acceptance log.
func envTag(text string) string {
	if strings.Contains(text, "FEATURE_FLAG") {
		return "+env"
	}
	return ""
}

// Witness-result stdouts: random mixes of marker and bare lines, valid and
// broken JSON, extra keys, long details (counted in UTF-16 units), CRLF, and
// duplicate or conflicting results. The three witness-result ops are compared.
func TestLiveWitnessResult(t *testing.T) {
	outcomes := []string{"PREDICATE_PASS", "PREDICATE_FAIL", "INCOMPATIBLE_STATE", "HARNESS_ERROR", "INFRASTRUCTURE_ERROR", "BOGUS", ""}
	detailOf := func(r *rand.Rand) string {
		switch r.IntN(6) {
		case 0:
			return `"detail":"` + strings.Repeat("a", 4094+r.IntN(5)) + `"`
		case 1:
			return `"detail":"` + strings.Repeat("\U0001F600", 2046+r.IntN(5)) + `"`
		case 2:
			return `"detail":"x\ud800y\n"`
		case 3:
			return `"detail":7`
		case 4:
			return `"detail":"` + string(rune('a'+r.IntN(3))) + `"`
		}
		return ""
	}
	lineOf := func(r *rand.Rand) string {
		protocol := `"protocol":"faultline.witness-result.v1"`
		if r.IntN(12) == 0 {
			protocol = `"protocol":"faultline.witness-result.v2"`
		}
		parts := []string{protocol, `"outcome":"` + outcomes[r.IntN(len(outcomes))] + `"`}
		if d := detailOf(r); d != "" {
			parts = append(parts, d)
		}
		if r.IntN(10) == 0 {
			parts = append(parts, `"extra":1`)
		}
		if r.IntN(15) == 0 {
			parts = slices.Delete(parts, 0, 1)
		}
		r.Shuffle(len(parts), func(i, j int) { parts[i], parts[j] = parts[j], parts[i] })
		body := "{" + strings.Join(parts, ",") + "}"
		switch r.IntN(8) {
		case 0:
			body = body[:len(body)-1]
		case 1:
			body = "  " + body + "\t"
		case 2:
			body = "[" + body + "]"
		}
		switch r.IntN(4) {
		case 0:
			return "FAULTLINE_WITNESS_RESULT=" + body
		case 1:
			return "FAULTLINE_WITNESS_RESULT= " + body
		case 2:
			return "x " + body
		}
		return body
	}
	toValue := func(p witnessresult.Parsed) jsjson.Value {
		o := jsjson.NewObj()
		o.Set("ok", jsjson.MakeBool(p.Ok))
		switch {
		case !p.Ok:
			o.Set("reason", jsjson.MakeString(p.Reason))
		case p.Result == nil:
			o.Set("result", jsjson.MakeNull())
		default:
			r := jsjson.NewObj()
			r.Set("protocol", jsjson.MakeString(witnessresult.Protocol))
			r.Set("outcome", jsjson.MakeString(p.Result.Outcome))
			if p.Result.HasDetail {
				r.Set("detail", jsjson.MakeString(p.Result.Detail))
			}
			o.Set("result", jsjson.MakeObject(r))
		}
		return jsjson.MakeObject(o)
	}
	liveCompare(t, "resolveWitnessResultFromStdout", func(c *oracle.Client, r *rand.Rand, i int) (string, string) {
		var lines []string
		for n := r.IntN(4); n >= 0; n-- {
			switch r.IntN(5) {
			case 0:
				lines = append(lines, "state=bad")
			case 1:
				lines = append(lines, "")
			default:
				line := lineOf(r)
				lines = append(lines, line)
				if r.IntN(4) == 0 {
					lines = append(lines, line)
				}
			}
		}
		stdout := strings.Join(lines, []string{"\n", "\r\n"}[r.IntN(2)])
		if r.IntN(2) == 0 {
			stdout += "\n"
		}
		args := `{"stdout":` + jsjson.Quote(stdout) + "}"
		p := witnessresult.Resolve(stdout)
		if p.Ok && p.Result != nil {
			exit := r.IntN(3)
			got := canonicalOf(jsjson.MakeBool(witnessresult.ConsistentWithExitCode(*p.Result, exit)))
			want := oracleText(t, c, "witnessResultConsistentWithExitCode", `{"outcome":"`+p.Result.Outcome+`","exitCode":`+strconv.Itoa(exit)+"}")
			if got != want {
				t.Fatalf("consistent(%s,%d): got %s want %s", p.Result.Outcome, exit, got, want)
			}
			cl := witnessresult.Classify(p.Result.Outcome)
			o := jsjson.NewObj()
			o.Set("verdict", jsjson.MakeString(cl.Verdict))
			o.Set("reason", jsjson.MakeString(cl.Reason))
			want = oracleText(t, c, "classifyFromWitnessResult", `{"outcome":"`+p.Result.Outcome+`"}`)
			if got := canonicalOf(jsjson.MakeObject(o)); got != want {
				t.Fatalf("classify(%s): got %s want %s", p.Result.Outcome, got, want)
			}
		}
		return args, canonicalOf(toValue(p))
	})
}

// TestLiveSafeGitLists compares internal/safegit with src/doctor.ts's
// HOST_COMMAND_SAFE_GIT_CONFIG and DOCTOR_SAFE_GIT_CONFIG. (TurnSnapshot's
// list is built inline in turn-snapshot.ts, so only its two Doctor-based
// pieces are compared here.)
func TestLiveSafeGitLists(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	raw, err := c.Call("safeGitLists", "{}")
	if err != nil {
		t.Fatal(err)
	}
	var want struct{ HostCommand, Doctor, Intake []string }
	if err := json.Unmarshal([]byte(raw), &want); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(safegit.HostCommand(), want.HostCommand) {
		t.Errorf("HostCommand:\ngot  %q\nwant %q", safegit.HostCommand(), want.HostCommand)
	}
	if !slices.Equal(safegit.Doctor(), want.Doctor) {
		t.Errorf("Doctor:\ngot  %q\nwant %q", safegit.Doctor(), want.Doctor)
	}
	if !slices.Equal(safegit.Intake(), want.Intake) || len(want.Intake) == 0 {
		t.Errorf("Intake:\ngot  %q\nwant %q", safegit.Intake(), want.Intake)
	}
	if ts := safegit.TurnSnapshot(); !slices.Equal(ts[:len(want.Doctor)], want.Doctor) {
		t.Errorf("TurnSnapshot does not start with Doctor: %q", ts)
	}
}
