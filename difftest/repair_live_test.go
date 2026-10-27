//go:build faultline_test

package difftest

import (
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/gen"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/repair"
)

// Slice 4c Task 6: the repair evidence packet, brief validation, OpenAI
// response extraction and the brief json_schema against src/repair-brief.ts
// and src/openai-response.ts.

func repairText(v jsjson.Value) string { return jsjson.Stringify(v) }

func repairParse(t *testing.T, text string) jsjson.Value {
	t.Helper()
	v, err := jsjson.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// repairClone deep-copies a JSON value through text.
func repairClone(t *testing.T, v jsjson.Value) jsjson.Value { return repairParse(t, repairText(v)) }

func repairStrings(items ...string) jsjson.Value {
	out := make([]jsjson.Value, len(items))
	for i, s := range items {
		out[i] = jsjson.MakeString(s)
	}
	return jsjson.MakeArray(out)
}

func repairHex(r *rand.Rand, n int) string {
	var b strings.Builder
	for range n {
		b.WriteByte("0123456789abcdef"[r.IntN(16)])
	}
	return b.String()
}

func repairPacketGot(text string) string {
	v, err := jsjson.Parse(text)
	if err != nil {
		return "THREW:" + err.Error()
	}
	return threwOr(repair.CreateEvidencePacket(v))
}

// repairTransitions builds n schema-valid transitions.
func repairTransitions(r *rand.Rand, n int) jsjson.Value {
	state := func(index int, verdict string) jsjson.Value {
		ids := func() jsjson.Value {
			return repairStrings("sha256:"+repairHex(r, 64), "sha256:"+repairHex(r, 64), "sha256:"+repairHex(r, 64))
		}
		commitLen := []int{40, 64}[r.IntN(2)]
		return jsjson.MustObject("stateIndex", jsjson.MakeNumber(float64(index)), "commit", jsjson.MakeString(repairHex(r, commitLen)),
			"tree", jsjson.MakeString(repairHex(r, commitLen)), "verdict", jsjson.MakeString(verdict), "executionIds", ids(), "runIds", ids())
	}
	items := make([]jsjson.Value, n)
	for i := range items {
		kind, b, a := "PASS_TO_FAIL", "PASS", "FAIL"
		if r.IntN(2) == 0 {
			kind, b, a = "FAIL_TO_PASS", "FAIL", "PASS"
		}
		items[i] = jsjson.MustObject("kind", jsjson.MakeString(kind), "before", state(i, b), "after", state(i+1, a))
	}
	return jsjson.MakeArray(items)
}

// mutateInvestigation applies one targeted change to a copy of base and
// returns the JSON text.
func mutateInvestigation(t *testing.T, r *rand.Rand, base jsjson.Value) string {
	inv := repairClone(t, base)
	o := inv.Obj()
	proof, witness := o.Field("proof").Obj(), o.Field("witness").Obj()
	switch r.IntN(20) {
	case 0:
		proof.Set("executionTrust", jsjson.MakeString([]string{"INJECTED_RUNNER", "UNSAFE_LOCAL", "NATIVE_DOCKER", "OTHER"}[r.IntN(4)]))
	case 1:
		proof.Set("dockerIsolated", jsjson.MakeBool(r.IntN(2) == 0))
	case 2:
		proof.Set("isProof", jsjson.MakeBool(r.IntN(2) == 0))
	case 3:
		o.Set("status", jsjson.MakeString([]string{"INVALID_REQUEST", "RANGE_ERROR", "EXECUTION_ERROR", "COMPLETED", "DONE"}[r.IntN(5)]))
	case 4:
		o.Set("transitions", jsjson.MakeArray(nil))
	case 5:
		o.Set("witness", jsjson.MakeNull())
	case 6:
		witness.Set("witnessDigest", []jsjson.Value{jsjson.MakeNull(), jsjson.MakeString(""), jsjson.MakeString("sha256:" + repairHex(r, 64))}[r.IntN(3)])
	case 7:
		witness.Set("frozenDigest", []jsjson.Value{jsjson.MakeNull(), jsjson.MakeString("sha256:" + repairHex(r, 64))}[r.IntN(2)])
	case 8:
		// 254 transitions make 256 facts; more exceed the packet maximum.
		o.Set("transitions", repairTransitions(r, []int{1, 2, 253, 254, 255, 300}[r.IntN(6)]))
	case 9:
		o.Set("transitions", repairTransitions(r, 1+r.IntN(6)))
	case 10:
		states := o.Field("states").Items()
		o.Set("states", jsjson.MakeArray(states[:r.IntN(len(states)+1)]))
	case 11:
		// before.executionIds alone sets the count; a short after does not.
		tr := o.Field("transitions").Items()[0].Obj()
		tr.Field("after").Obj().Set("executionIds", repairStrings("sha256:"+repairHex(r, 64)))
	case 12:
		tr := o.Field("transitions").Items()[r.IntN(2)].Obj()
		tr.Field("before").Obj().Set("executionIds", repairStrings())
	case 13:
		o.Set("recorder", jsjson.MakeString("other"))
	case 14:
		text := repairText(inv)
		return strings.Replace(text, `"executionsPerState":3`, `"executionsPerState":`+[]string{"3e0", "3.0", "30e-1", "0.3e1", "3.000000000000001", "4", "-3"}[r.IntN(7)], 1)
	case 15:
		o.Set("zzExtra", jsjson.MakeNumber(1))
	case 16:
		proof.Set("proofTransitions", jsjson.MakeNumber(float64(r.IntN(5))))
		o.Set("errors", repairStrings("e1", loneSurrogate, astralChar))
	case 17:
		o.Set("repository", []jsjson.Value{jsjson.MakeNull(), jsjson.MakeString(astralChar)}[r.IntN(2)])
	default:
		return gen.CorruptDeep(r, repairText(inv))
	}
	return repairText(inv)
}

func TestLiveRepairEvidencePacket(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	bases, err := filepath.Glob(filepath.Join(oracle.RepoRoot(t), "difftest", "testdata", "bases", "git-*", "investigation.json"))
	if err != nil || len(bases) == 0 {
		t.Fatal("no git bases", err)
	}
	for _, path := range bases {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		liveCheck(t, c, "createRepairEvidencePacket", `{"text":`+jsjson.Quote(text)+"}", repairPacketGot(text))
	}
	base := repairParse(t, seedTexts(t, "git-unbound/investigation.json")[0])
	r := rand.New(rand.NewPCG(46, 1))
	refusal, tooManyFacts, investigationInvalid, packets := 0, 0, 0, 0
	for range 400 {
		text := mutateInvestigation(t, r, base)
		got := repairPacketGot(text)
		liveCheck(t, c, "createRepairEvidencePacket", `{"text":`+jsjson.Quote(text)+"}", got)
		switch {
		case strings.HasPrefix(got, "THREW:A post-localization repair brief requires"):
			refusal++
		case strings.Contains(got, `"path": [`+"\n"+`      "facts"`):
			tooManyFacts++
		case strings.HasPrefix(got, "THREW:"):
			investigationInvalid++
		default:
			packets++
		}
	}
	t.Logf("refusals %d, packet ZodErrors %d, investigation errors %d, packets %d", refusal, tooManyFacts, investigationInvalid, packets)
	if refusal == 0 || tooManyFacts == 0 || investigationInvalid == 0 || packets == 0 {
		t.Fatal("an outcome was never reached")
	}
}

// repairSign sets packetDigest to the digest of the rest of the packet.
func repairSign(packet jsjson.Value) {
	o := packet.Obj()
	o.Delete("packetDigest")
	o.Set("packetDigest", jsjson.MakeString(jsexc.Must(canonical.DigestJSON(packet))))
}

func repairStatement(r *rand.Rand, max int) string {
	switch r.IntN(12) {
	case 0:
		return ""
	case 1:
		return strings.Repeat("s", max)
	case 2:
		return strings.Repeat("s", max+1)
	case 3:
		return strings.Repeat(astralChar, max/2)
	case 4:
		return strings.Repeat(astralChar, max/2) + "s"
	case 5:
		return loneSurrogate
	}
	return "statement " + strconv.Itoa(r.IntN(100))
}

// randomRepairPacket returns a packet with n facts, mostly valid and signed.
func randomRepairPacket(r *rand.Rand, odd bool) jsjson.Value {
	n := 2 + r.IntN(6)
	if odd && r.IntN(6) == 0 {
		n = []int{0, 1, 2, 256, 257}[r.IntN(5)]
	}
	facts := make([]jsjson.Value, n)
	for i := range facts {
		id, kind, statement := "E"+strconv.Itoa(i+1), "DERIVED", "fact "+strconv.Itoa(i+1)
		if i == 0 {
			kind = "EXECUTED"
		}
		if odd && r.IntN(20) == 0 {
			switch r.IntN(4) {
			case 0:
				id = []string{"E0", "E01", "e1", "E", "E1 ", "F1", "E99"}[r.IntN(7)]
			case 1:
				kind = []string{"executed", "OTHER", ""}[r.IntN(3)]
			default:
				statement = repairStatement(r, 2000)
			}
		}
		facts[i] = jsjson.MustObject("id", jsjson.MakeString(id), "kind", jsjson.MakeString(kind), "statement", jsjson.MakeString(statement))
	}
	limitations := []string{"limit one", "limit two"}
	if odd && r.IntN(8) == 0 {
		switch r.IntN(5) {
		case 0:
			limitations = nil
		case 1:
			limitations = make([]string, 16+r.IntN(2))
			for i := range limitations {
				limitations[i] = "l"
			}
		default:
			limitations = []string{repairStatement(r, 1000)}
		}
	}
	packet := jsjson.MustObject(
		"schemaVersion", jsjson.MakeString(repair.EvidencePacketVersion),
		"investigationDigest", jsjson.MakeString("sha256:"+strings.Repeat("1", 64)),
		"frozenWitnessDigest", jsjson.MakeString("sha256:"+strings.Repeat("2", 64)),
		"recorder", jsjson.MakeString("git-commit-range-replay"),
		"nativeCodexInterception", jsjson.MakeBool(false),
		"facts", jsjson.MakeArray(facts),
		"limitations", repairStrings(limitations...),
	)
	o := packet.Obj()
	if odd && r.IntN(10) == 0 {
		switch r.IntN(6) {
		case 0:
			o.Set("schemaVersion", jsjson.MakeString("faultline.repair-evidence.v1"))
		case 1:
			o.Set("nativeCodexInterception", jsjson.MakeBool(true))
		case 2:
			o.Set("zzExtra", jsjson.MakeNull())
		case 3:
			o.Delete("recorder")
		case 4:
			o.Set("investigationDigest", jsjson.MakeString("sha256:"+strings.Repeat("A", 64)))
		default:
			o.Set("facts", jsjson.MakeString("facts"))
		}
	}
	repairSign(packet)
	if odd && r.IntN(10) == 0 {
		o.Set("packetDigest", jsjson.MakeString([]string{"sha256:" + strings.Repeat("3", 64), "sha256:zz", ""}[r.IntN(3)]))
	}
	return packet
}

func randomRepairIDs(r *rand.Rand, facts int, odd bool) jsjson.Value {
	n := 1 + r.IntN(3)
	if odd && r.IntN(10) == 0 {
		n = []int{0, 1, 32, 33}[r.IntN(4)]
	}
	ids := make([]string, n)
	for i := range ids {
		ids[i] = "E" + strconv.Itoa(1+r.IntN(max(facts, 1)))
		if odd && r.IntN(12) == 0 {
			ids[i] = []string{"E0", "E01", "E9", "E" + strconv.Itoa(facts+1), "E1000", "x", ""}[r.IntN(7)]
		}
	}
	if odd && n > 1 && r.IntN(6) == 0 {
		ids[n-1] = ids[0] // a duplicate
	}
	return repairStrings(ids...)
}

func randomRecommendation(r *rand.Rand, facts int, odd bool) jsjson.Value {
	statement := "recommendation"
	if odd && r.IntN(15) == 0 {
		statement = repairStatement(r, 2000)
	}
	rec := jsjson.MustObject("statement", jsjson.MakeString(statement), "evidenceIds", randomRepairIDs(r, facts, odd))
	if odd && r.IntN(40) == 0 {
		rec.Obj().Set("zzExtra", jsjson.MakeBool(true))
	}
	return rec
}

func randomRecommendations(r *rand.Rand, facts, minimum int, odd bool) jsjson.Value {
	n := max(minimum, 1+r.IntN(2))
	if odd && r.IntN(10) == 0 {
		n = []int{0, 1, 8, 9}[r.IntN(4)]
	}
	items := make([]jsjson.Value, n)
	for i := range items {
		items[i] = randomRecommendation(r, facts, odd)
	}
	return jsjson.MakeArray(items)
}

func randomRepairBrief(r *rand.Rand, packet jsjson.Value, odd bool) jsjson.Value {
	facts := len(packet.Get("facts").Items())
	digest := packet.Get("packetDigest")
	if digest.Kind() != jsjson.String {
		digest = jsjson.MakeString("sha256:" + strings.Repeat("4", 64))
	}
	if odd && r.IntN(12) == 0 {
		digest = jsjson.MakeString([]string{"sha256:" + strings.Repeat("5", 64), "sha256:bad"}[r.IntN(2)])
	}
	uncertainties := []string{"u"}
	if odd && r.IntN(10) == 0 {
		switch r.IntN(4) {
		case 0:
			uncertainties = nil
		case 1:
			uncertainties = make([]string, 8+r.IntN(2))
			for i := range uncertainties {
				uncertainties[i] = "u"
			}
		default:
			uncertainties = []string{repairStatement(r, 1000)}
		}
	}
	prevention := jsjson.MustObject("hardEnforcement", randomRecommendations(r, facts, 1, odd), "softGuidance", randomRecommendations(r, facts, 0, odd))
	brief := jsjson.MustObject(
		"schemaVersion", jsjson.MakeString(repair.BriefVersion),
		"evidencePacketDigest", digest,
		"proposedInvariant", randomRecommendation(r, facts, odd),
		"repairDirections", randomRecommendations(r, facts, 1, odd),
		"prevention", prevention,
		"uncertainties", repairStrings(uncertainties...),
	)
	if odd && r.IntN(15) == 0 {
		switch r.IntN(5) {
		case 0:
			brief.Obj().Set("zzExtra", jsjson.MakeNull())
		case 1:
			prevention.Obj().Set("zzExtra", jsjson.MakeNull())
		case 2:
			brief.Obj().Set("schemaVersion", jsjson.MakeString("faultline.repair-brief.v2"))
		case 3:
			brief.Obj().Delete("prevention")
		default:
			return []jsjson.Value{jsjson.MakeNull(), jsjson.MakeArray(nil), jsjson.MakeString("brief")}[r.IntN(3)]
		}
	}
	return brief
}

func TestLiveValidateRepairBrief(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(46, 2))
	real, err := repair.CreateEvidencePacket(repairParse(t, seedTexts(t, "git-unbound/investigation.json")[0]))
	if err != nil {
		t.Fatal(err)
	}
	reached := map[string]bool{}
	for i := range 1500 {
		odd := i%3 != 0
		packet := randomRepairPacket(r, odd)
		if i%10 == 0 {
			packet = repairClone(t, real)
		}
		brief := randomRepairBrief(r, packet, odd)
		pt, bt := repairText(packet), repairText(brief)
		var out string
		if err := jsexc.Try(func() {
			out = repairText(repair.ValidateBrief(repairParse(t, pt), repairParse(t, bt)).JSON())
		}); err != nil {
			out = "THREW:" + jsexc.Message(err)
		}
		liveCheck(t, c, "validateRepairBrief", `{"packet":`+jsjson.Quote(pt)+`,"brief":`+jsjson.Quote(bt)+"}", out)
		v := repairParse(t, out)
		if v.Get("valid").Bool() {
			reached["valid"] = true
		}
		for _, e := range v.Get("errors").Items() {
			msg, _, _ := strings.Cut(e.Str(), ":")
			reached[msg] = true
		}
	}
	for _, want := range []string{"valid", "repair evidence packet is invalid", "repair evidence packet digest does not match its canonical contents",
		"repair brief schema validation failed", "repair brief references a different evidence packet digest", "repair brief cites an unknown evidence ID"} {
		if !reached[want] {
			t.Errorf("never reached %q (%v)", want, reached)
		}
	}
}

func randomResponseText(r *rand.Rand) jsjson.Value {
	switch r.IntN(14) {
	case 0:
		return jsjson.MakeString("")
	case 1:
		return jsjson.MakeString(loneSurrogate)
	case 2:
		return jsjson.MakeString(astralChar)
	case 3:
		return jsjson.MakeNull()
	case 4:
		return jsjson.MakeNumber(1)
	case 5:
		return jsjson.MakeArray(nil)
	case 6:
		return jsjson.MakeString("{}")
	}
	return jsjson.MakeString([]string{`{"a":1}`, "x", "y", " "}[r.IntN(4)])
}

func randomResponsePart(r *rand.Rand) jsjson.Value {
	switch r.IntN(16) {
	case 0:
		return []jsjson.Value{jsjson.MakeNull(), jsjson.MakeNumber(0), jsjson.MakeString("p"), jsjson.MakeArray(nil)}[r.IntN(4)]
	case 1:
		return jsjson.MustObject("type", jsjson.MakeString("refusal"), "refusal", jsjson.MakeString("no"))
	case 2:
		return jsjson.MustObject("text", randomResponseText(r))
	case 3:
		return jsjson.MustObject("type", []jsjson.Value{jsjson.MakeString("OUTPUT_TEXT"), jsjson.MakeNull(), jsjson.MakeString("output_text ")}[r.IntN(3)], "text", jsjson.MakeString("t"))
	case 4:
		return jsjson.MustObject("type", jsjson.MakeString("output_text"))
	}
	part := jsjson.MustObject("type", jsjson.MakeString("output_text"), "text", randomResponseText(r))
	if r.IntN(6) == 0 {
		part.Obj().Set("annotations", jsjson.MakeArray(nil))
	}
	return part
}

func randomResponseItem(r *rand.Rand) jsjson.Value {
	switch r.IntN(14) {
	case 0:
		return []jsjson.Value{jsjson.MakeNull(), jsjson.MakeNumber(1), jsjson.MakeString("i"), jsjson.MakeArray(nil)}[r.IntN(4)]
	case 1:
		return jsjson.MustObject("type", jsjson.MakeString("reasoning"))
	case 2:
		return jsjson.MustObject("type", jsjson.MakeString("message"), "content", []jsjson.Value{jsjson.MakeNull(), jsjson.MakeObject(jsjson.NewObj()), jsjson.MakeString("c")}[r.IntN(3)])
	}
	n := r.IntN(3)
	if r.IntN(3) == 0 {
		n = 1
	}
	parts := make([]jsjson.Value, n)
	for i := range parts {
		parts[i] = randomResponsePart(r)
	}
	return jsjson.MustObject("type", jsjson.MakeString("message"), "content", jsjson.MakeArray(parts))
}

func randomOpenAIResponse(r *rand.Rand) string {
	switch r.IntN(30) {
	case 0:
		return []string{"null", "[]", `"s"`, "1", "true", "[{}]"}[r.IntN(6)]
	case 1:
		// __proto__ is an ordinary own key in JSON.parse output.
		return []string{`{"__proto__":{"output_text":"x"}}`, `{"__proto__":{"output":[]},"output_text":"x"}`, `{"output":[{"__proto__":{"content":[]}}]}`,
			`{"output":[{"content":[{"__proto__":{"type":"output_text"},"text":"x"}]}]}`, `{"output_text":"a","output_text":"b"}`,
			`{"output":[{"content":[{"type":"output_text","text":"\ud800"}]}]}`}[r.IntN(6)]
	}
	o := jsjson.NewObj()
	if r.IntN(5) != 0 {
		if r.IntN(10) == 0 {
			o.Set("output", []jsjson.Value{jsjson.MakeNull(), jsjson.MakeObject(jsjson.NewObj()), jsjson.MakeString("o")}[r.IntN(3)])
		} else {
			n := r.IntN(3)
			if r.IntN(2) == 0 {
				n = 1
			}
			items := make([]jsjson.Value, n)
			for i := range items {
				items[i] = randomResponseItem(r)
			}
			o.Set("output", jsjson.MakeArray(items))
		}
	}
	if r.IntN(2) == 0 {
		o.Set("output_text", randomResponseText(r))
	}
	if r.IntN(4) == 0 {
		o.Set("id", jsjson.MakeString("resp_1"))
	}
	return repairText(jsjson.MakeObject(o))
}

func TestLiveExtractOpenAiResponseText(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(46, 3))
	reached := map[string]bool{}
	for range 1000 {
		text := randomOpenAIResponse(r)
		got := func() string {
			v, err := jsjson.Parse(text)
			if err != nil {
				return "THREW:" + err.Error()
			}
			s, err := repair.ExtractResponseText(v)
			if err != nil {
				return "THREW:" + err.Error()
			}
			return s
		}()
		liveCheck(t, c, "extractOpenAiResponseText", `{"text":`+jsjson.Quote(text)+"}", got)
		if strings.HasPrefix(got, "THREW:") {
			reached[got] = true
		} else {
			reached["ok"] = true
		}
	}
	// openai-response.ts:28-70 throws ten distinct texts.
	if len(reached) != 11 {
		t.Errorf("reached %d outcomes, want the 10 errors and a text: %v", len(reached), reached)
	}
}

func TestLiveRepairBriefJSONSchema(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	want := jsjson.Stringify(jsjson.MustObject("format", jsjson.MustObject("type", jsjson.MakeString("json_schema"),
		"name", jsjson.MakeString("faultline_repair_brief"), "strict", jsjson.MakeBool(true), "schema", repair.BriefJSONSchema())))
	liveCheck(t, c, "repairBriefJsonSchema", "{}", want)
}

// --- Slice 4c Task 11: the repair worktree ---

// repairGitRecorder writes a git wrapper that logs each call's argv to log
// and runs the real git, in a fresh directory; it returns the PATH to use.
func repairGitRecorder(t *testing.T, log string) string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n{ for a in \"$@\"; do printf '%s\\n' \"$a\"; done; echo --; } >> '" + log + "'\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir + string(os.PathListSeparator) + os.Getenv("PATH")
}

// repairWorktreeRepo makes a repository with fixed dates (so both sides get
// the same commit ids) and reflogs off, as the writer scenarios do.
func repairWorktreeRepo(t *testing.T, repo string) string {
	t.Helper()
	run := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.test", "-c", "core.logAllRefUpdates=false"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE=2026-07-16T12:00:00Z", "GIT_COMMITTER_DATE=2026-07-16T12:00:00Z")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	os.MkdirAll(repo, 0o755)
	run("init", "-q")
	run("config", "core.logAllRefUpdates", "false")
	os.WriteFile(filepath.Join(repo, "state.txt"), []byte("bad\n"), 0o644)
	run("add", "state.txt")
	run("commit", "-q", "-m", "bad")
	return run("rev-parse", "HEAD")
}

// repairDescribeTree lists root, with .git contents masked: a .git entry is
// one line, and the names under repo/.git/worktrees are listed.
func repairDescribeTree(root string) string {
	var lines []string
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		switch {
		case info.Name() == ".git" && rel != ".":
			lines = append(lines, "G "+rel)
			if names, err := os.ReadDir(filepath.Join(path, "worktrees")); err == nil {
				for _, n := range names {
					lines = append(lines, "W "+rel+"/worktrees/"+n.Name())
				}
			}
			return filepath.SkipDir
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(path)
			lines = append(lines, "L "+rel+" "+strings.ReplaceAll(target, root, "<R>"))
		case info.IsDir():
			lines = append(lines, "D "+rel+" "+fmt.Sprintf("%04o", uint32(info.Mode().Perm())))
		case info.Mode().IsRegular():
			b, _ := os.ReadFile(path)
			lines = append(lines, "F "+rel+" "+fmt.Sprintf("%04o", uint32(info.Mode().Perm()))+" "+fmt.Sprintf("%q", strings.ReplaceAll(string(b), root, "<R>")))
		default:
			lines = append(lines, "O "+rel+" "+info.Mode().String())
		}
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

type repairWorktreeCase struct {
	name, kind string
	keep       bool
}

// TestLiveRepairWorktree runs create, patch collection and removal in
// fresh repositories on both sides under one seed (the repairId is the first
// UUID read), and compares the results, the trees (.git masked) and the
// recorded git argv.
func TestLiveRepairWorktree(t *testing.T) {
	const seed = "live-repair-worktree"
	c := oracle.StartSeeded(t, seed)
	defer c.Close()
	clock.ResetForTest(seed)
	kinds := []string{"normal", "unknown-base", "non-repository", "symlinked-parent", "existing-output", "bundle-symlink",
		"worktrees-blocked", "nested-output", "bad-id", "missing-bundle", "removed-worktree", "dirty-bundle-subdir"}
	var cases []repairWorktreeCase
	for i := 0; i < 80; i++ {
		kind := kinds[i%len(kinds)]
		if kind == "bundle-symlink" && runtime.GOOS != "linux" {
			kind = "normal" // Node's cpSync differs on libc++ for links
		}
		cases = append(cases, repairWorktreeCase{name: fmt.Sprintf("%02d-%s", i, kind), kind: kind, keep: (i/len(kinds))%2 == 1})
	}
	kindsSeen := map[string]int{}
	realGit, _ := exec.LookPath("git")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			type side struct {
				root, repo, log string
				out, patch      string
				cleanup, tree   string
				argv            string
			}
			var sides [2]side
			for k := range sides {
				root, _ := filepath.EvalSymlinks(t.TempDir())
				s := &sides[k]
				s.root, s.repo, s.log = root, filepath.Join(root, "repo"), filepath.Join(root, "git.log")
				commit := repairWorktreeRepo(t, s.repo)
				bundle := filepath.Join(root, "bundle")
				os.MkdirAll(filepath.Join(bundle, "runs"), 0o755)
				os.WriteFile(filepath.Join(bundle, "manifest.json"), []byte("{}\n"), 0o644)
				os.WriteFile(filepath.Join(bundle, "runs", "a.json"), []byte("[1]\n"), 0o600)
				output := filepath.Join(root, "repairs", "r1")
				base, repository := commit, s.repo
				switch tc.kind {
				case "unknown-base":
					base = strings.Repeat("ab", 20)
				case "non-repository":
					repository = filepath.Join(root, "plain")
					os.Mkdir(repository, 0o755)
				case "symlinked-parent":
					os.Mkdir(filepath.Join(root, "real"), 0o755)
					os.Symlink("real", filepath.Join(root, "link"))
					output = filepath.Join(root, "link", "r1")
				case "existing-output":
					os.MkdirAll(output, 0o755)
				case "bundle-symlink":
					os.Symlink("manifest.json", filepath.Join(bundle, "link"))
					os.Symlink("../nowhere", filepath.Join(bundle, "runs", "dangling"))
				case "worktrees-blocked":
					os.WriteFile(filepath.Join(s.repo, ".git", "worktrees"), []byte("not a directory"), 0o644)
				case "nested-output":
					output = filepath.Join(root, "a", "b", "c", "r1")
				case "bad-id":
					base = []string{"nothex", strings.ToUpper(commit), commit[:39], commit + "0", ""}[len(tc.name)%5]
				case "missing-bundle":
					bundle = filepath.Join(root, "no-bundle")
				case "dirty-bundle-subdir":
					os.MkdirAll(filepath.Join(bundle, "a", "b", "c"), 0o700)
					os.WriteFile(filepath.Join(bundle, "a", "b", "c", "f"), nil, 0o400)
				}
				path := repairGitRecorder(t, s.log)
				os.WriteFile(s.log, nil, 0o644)
				args := fmt.Sprintf(`{"repository":%s,"baseCommit":%s,"bundleDirectory":%s,"outputDirectory":%s,"path":%s}`,
					jsjson.Stringify(jsjson.MakeString(repository)), jsjson.Stringify(jsjson.MakeString(base)),
					jsjson.Stringify(jsjson.MakeString(bundle)), jsjson.Stringify(jsjson.MakeString(output)), jsjson.Stringify(jsjson.MakeString(path)))
				var created, patch, cleanup string
				var sess repair.Session
				ok := false
				if k == 0 {
					t.Setenv("PATH", path)
					var err error
					sess, err = repair.CreateWorktree(repository, base, bundle, output)
					if err != nil {
						created = "THREW:" + jsexc.Message(err)
					} else {
						ok = true
						created = jsjson.Stringify(jsjson.MustObject("repairId", jsjson.MakeString(sess.RepairID), "managedRoot", jsjson.MakeString(sess.ManagedRoot),
							"worktreePath", jsjson.MakeString(sess.WorktreePath), "proofReadonlyPath", jsjson.MakeString(sess.ProofReadonlyPath),
							"instructionsPath", jsjson.MakeString(sess.InstructionsPath), "artifactsPath", jsjson.MakeString(sess.ArtifactsPath),
							"baseCommit", jsjson.MakeString(sess.BaseCommit), "repository", jsjson.MakeString(sess.Repository)))
					}
				} else {
					created = oracleText(t, c, "createRepairWorktree", args)
					if !strings.HasPrefix(created, "THREW:") {
						ok = true
						v := repairParse(t, created)
						g := func(f string) string { x, _ := v.Obj().Get(f); return x.Str() }
						sess = repair.Session{ManagedRoot: g("managedRoot"), WorktreePath: g("worktreePath"), Repository: g("repository")}
					}
				}
				if ok {
					if tc.kind != "removed-worktree" {
						os.WriteFile(filepath.Join(sess.WorktreePath, "state.txt"), []byte("good\n"), 0o644)
						os.WriteFile(filepath.Join(sess.WorktreePath, "new.txt"), []byte("new\n"), 0o644)
						os.WriteFile(filepath.Join(sess.WorktreePath, "bin.dat"), []byte{0, 1, 2, 0xff}, 0o644)
					}
					exec.Command(realGit, "-C", sess.WorktreePath, "add", "-N", "new.txt", "bin.dat").Run()
					if tc.kind == "removed-worktree" {
						os.RemoveAll(sess.WorktreePath)
					}
					rargs := fmt.Sprintf(`{"repository":%s,"worktreePath":%s,"managedRoot":%s,"keepManagedRoot":%t,"path":%s}`,
						jsjson.Stringify(jsjson.MakeString(sess.Repository)), jsjson.Stringify(jsjson.MakeString(sess.WorktreePath)),
						jsjson.Stringify(jsjson.MakeString(sess.ManagedRoot)), tc.keep, jsjson.Stringify(jsjson.MakeString(path)))
					pargs := fmt.Sprintf(`{"worktreePath":%s,"path":%s}`, jsjson.Stringify(jsjson.MakeString(sess.WorktreePath)), jsjson.Stringify(jsjson.MakeString(path)))
					cl := func(r repair.Cleanup) string {
						errs := make([]jsjson.Value, len(r.Errors))
						for i, e := range r.Errors {
							errs[i] = jsjson.MakeString(e)
						}
						return jsjson.Stringify(jsjson.MustObject("cleaned", jsjson.MakeBool(r.Cleaned), "errors", jsjson.MakeArray(errs)))
					}
					if k == 0 {
						p, err := repair.CollectPatch(sess.WorktreePath)
						patch = p
						if err != nil {
							patch = "THREW:" + jsexc.Message(err)
						}
						cleanup = cl(repair.RemoveWorktree(sess.Repository, sess.WorktreePath, sess.ManagedRoot, tc.keep))
					} else {
						patch = oracleText(t, c, "collectRepairPatch", pargs)
						cleanup = oracleText(t, c, "removeRepairWorktree", rargs)
					}
				}
				b, _ := os.ReadFile(s.log)
				s.argv, s.out, s.patch, s.cleanup = string(b), created, patch, cleanup
				s.tree = repairDescribeTree(root)
				mask := func(x string) string { return strings.ReplaceAll(x, root, "<R>") }
				s.argv, s.out, s.patch, s.cleanup, s.tree = mask(s.argv), mask(s.out), mask(s.patch), mask(s.cleanup), mask(s.tree)
			}
			g, w := sides[0], sides[1]
			if g.out != w.out {
				t.Fatalf("create:\n go   %s\n node %s", g.out, w.out)
			}
			if g.patch != w.patch {
				t.Fatalf("patch:\n go   %q\n node %q", g.patch, w.patch)
			}
			if g.cleanup != w.cleanup {
				t.Fatalf("cleanup:\n go   %s\n node %s", g.cleanup, w.cleanup)
			}
			if g.argv != w.argv {
				t.Fatalf("git argv:\n--- go\n%s\n--- node\n%s", g.argv, w.argv)
			}
			if g.tree != w.tree {
				t.Fatalf("trees:\n--- go\n%s\n--- node\n%s", g.tree, w.tree)
			}
			if strings.HasPrefix(w.out, "THREW:") {
				kindsSeen["THREW"]++
			} else {
				kindsSeen["ok"]++
			}
		})
	}
	t.Logf("outcomes: %v", kindsSeen)
}
