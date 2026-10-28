//go:build faultline_test

package difftest

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	requireReached(t, "internal/repair/response.go", recordedOutputs(t))
}

func TestLiveRepairBriefJSONSchema(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	// The request body's `text` member, from the one builder ProposeBrief uses.
	liveCheck(t, c, "repairBriefJsonSchema", "{}", jsjson.Stringify(repair.BriefTextFormat()))
}

// Slice 4c Task 10: proposeRepairBriefWithGpt against a scripted local
// server, both sides in turn. Task 1's fake OpenAI server records only the
// method, path, Authorization, Content-Type and body; this one also records
// the five headers undici adds, so the Go client's explicit copies are
// checked too, and it can redirect and compress, which the fake never does.

type repairReply struct {
	status   int
	headers  [][2]string
	body     []byte
	redirect int // a 3xx status sent first, with Location /v1/final
	// location, when set, replaces that Location: %HOST% is the request host
	// and a redirect to /v1/responses itself never ends.
	location string
}

type repairServer struct {
	mu      sync.Mutex
	reply   repairReply
	records []string
}

func (s *repairServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	defer s.mu.Unlock()
	h := func(name string) string { return strings.Join(r.Header.Values(name), ",") }
	s.records = append(s.records, strings.Join([]string{"openai", r.Method, r.URL.Path, h("Authorization"), h("Content-Type"),
		h("Accept"), h("Accept-Language"), h("Sec-Fetch-Mode"), h("User-Agent"), h("Accept-Encoding"), string(body)}, "\x1f"))
	if s.reply.redirect != 0 && (r.URL.Path == "/v1/responses" || s.reply.location != "") {
		location := "/v1/final"
		if s.reply.location != "" {
			location = strings.ReplaceAll(s.reply.location, "%HOST%", r.Host)
		}
		w.Header().Set("Location", location)
		w.WriteHeader(s.reply.redirect)
		return
	}
	for _, kv := range s.reply.headers {
		w.Header().Set(kv[0], kv[1])
	}
	w.WriteHeader(s.reply.status)
	w.Write(s.reply.body)
}

func (s *repairServer) run(reply repairReply, call func() string) (string, []string) {
	s.mu.Lock()
	s.reply, s.records = reply, nil
	s.mu.Unlock()
	out := call()
	s.mu.Lock()
	defer s.mu.Unlock()
	return out, s.records
}

// repairBriefFor returns a brief for packet citing ids, with digest as its
// evidencePacketDigest.
func repairBriefFor(digest string, statement string, ids ...string) jsjson.Value {
	rec := func() jsjson.Value {
		return jsjson.MustObject("statement", jsjson.MakeString(statement), "evidenceIds", repairStrings(ids...))
	}
	return jsjson.MustObject("schemaVersion", jsjson.MakeString(repair.BriefVersion), "evidencePacketDigest", jsjson.MakeString(digest),
		"proposedInvariant", rec(), "repairDirections", jsjson.MakeArray([]jsjson.Value{rec()}),
		"prevention", jsjson.MustObject("hardEnforcement", jsjson.MakeArray([]jsjson.Value{rec()}), "softGuidance", jsjson.MakeArray(nil)),
		"uncertainties", repairStrings("u"))
}

func repairOutput(texts ...string) jsjson.Value {
	parts := make([]jsjson.Value, len(texts))
	for i, text := range texts {
		parts[i] = jsjson.MustObject("type", jsjson.MakeString("output_text"), "text", jsjson.MakeString(text))
	}
	return jsjson.MakeArray([]jsjson.Value{jsjson.MustObject("type", jsjson.MakeString("message"), "content", jsjson.MakeArray(parts))})
}

func repairGzip(b []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

func repairDeflate(b []byte, wrapped bool) []byte {
	var buf bytes.Buffer
	if wrapped {
		w := zlib.NewWriter(&buf)
		w.Write(b)
		w.Close()
	} else {
		w, _ := flate.NewWriter(&buf, flate.DefaultCompression)
		w.Write(b)
		w.Close()
	}
	return buf.Bytes()
}

// randomRepairReply scripts one reply for packet; kind names it.
func randomRepairReply(r *rand.Rand, packet jsjson.Value) (string, repairReply) {
	digest := packet.Get("packetDigest").Str()
	brief := repairText(repairBriefFor(digest, "keep the invariant", "E1", "E2"))
	json200 := func(response jsjson.Value) repairReply {
		return repairReply{status: 200, headers: [][2]string{{"Content-Type", "application/json"}}, body: []byte(repairText(response))}
	}
	ok := json200(jsjson.MustObject("id", jsjson.MakeString("resp_1"), "output", repairOutput(brief)))
	switch k := r.IntN(30); k {
	case 0:
		return "valid", ok
	case 1:
		return "valid-fast-path", json200(jsjson.MustObject("output_text", jsjson.MakeString(brief)))
	case 2:
		return "valid-both", json200(jsjson.MustObject("output", repairOutput(brief), "output_text", jsjson.MakeString(brief)))
	case 3:
		return "wrong-citations", json200(jsjson.MustObject("output", repairOutput(repairText(repairBriefFor(digest, "s", "E1", "E99", "E1000")))))
	case 4:
		return "wrong-digest", json200(jsjson.MustObject("output", repairOutput(repairText(repairBriefFor("sha256:"+strings.Repeat("9", 64), "s", "E1")))))
	case 5:
		return "multiple-outputs", json200(jsjson.MustObject("output", repairOutput(brief, brief)))
	case 6:
		return "fast-path-disagrees", json200(jsjson.MustObject("output", repairOutput(brief), "output_text", jsjson.MakeString(brief+" ")))
	case 7, 8, 9:
		status := []int{400, 429, 500}[k-7]
		body := []string{`{"error":{"message":"bad request","type":"invalid_request_error"}}`, "rate limited " + astralChar,
			"upstream \xff\xfe down \xed\xa0\x80", "", "\xef\xbb\xbfwith a BOM"}[r.IntN(5)]
		return "http-" + strconv.Itoa(status), repairReply{status: status, headers: [][2]string{{"Content-Type", "text/plain"}}, body: []byte(body)}
	case 10:
		return "not-json", repairReply{status: 200, headers: [][2]string{{"Content-Type", "text/html"}}, body: []byte([]string{"<html>oops</html>", "{", "[1,]", "{\"output\":tru}", "nul"}[r.IntN(5)])}
	case 11:
		ok.body = append([]byte("\xef\xbb\xbf"), ok.body...)
		return "bom", ok
	case 12:
		ok.body = append([]byte("\xef\xbb\xbf\xef\xbb\xbf"), ok.body...)
		return "double-bom", ok
	case 13:
		ok.body = append([]byte("\xef\xbb\xbf\xef\xbb\xbf\xef\xbb\xbf"), ok.body...)
		return "triple-bom", ok
	case 14:
		// Bad bytes inside the brief's statement decode to U+FFFD.
		text := strings.Replace(string(ok.body), "keep the invariant", "keep \xff the \xc3 invariant \xed\xa0\x80", 1)
		ok.body = []byte(text)
		return "invalid-utf8", ok
	case 15:
		return "empty-body", repairReply{status: 200, body: nil}
	case 16:
		return "text-not-json", json200(jsjson.MustObject("output_text", jsjson.MakeString([]string{"not json", "{'a':1}", brief[:40], "\ufeff{}"}[r.IntN(4)])))
	case 17:
		return "text-schema-invalid", json200(jsjson.MustObject("output_text", jsjson.MakeString([]string{"{}", "null", "[]", `{"schemaVersion":"faultline.repair-brief.v1"}`}[r.IntN(4)])))
	case 18:
		ok.headers = append(ok.headers, [2]string{"Content-Encoding", []string{"gzip", "x-gzip", "GZIP", " gzip "}[r.IntN(4)]})
		ok.body = repairGzip(ok.body)
		return "gzip", ok
	case 19:
		ok.headers = append(ok.headers, [2]string{"Content-Encoding", "deflate"})
		ok.body = repairDeflate(ok.body, r.IntN(2) == 0)
		return "deflate", ok
	case 20:
		ok.headers = append(ok.headers, [2]string{"Content-Encoding", "gzip"})
		g := repairGzip(ok.body)
		switch r.IntN(9) {
		case 0:
			ok.body = g[:len(g)/2] // truncated: what was decoded, no error
		case 1:
			g[len(g)-6] ^= 1 // a bad CRC
			ok.body = g
		case 2:
			// not gzip at all: the body is sent as is
		case 3:
			ok.body = append(g, g...) // two members
		case 4:
			ok.body = []byte{[]byte{0x1f, 0x00, 'x'}[r.IntN(3)]}
		case 5:
			ok.body = g[:10] // the header alone
		case 6:
			ok.body = append(g, 0, 'x', 'y') // a 0x00 ends the stream
		case 7:
			ok.body = append(g, 'x', 'y') // garbage starts another member
		default:
			g[len(g)-2] ^= 1 // a bad length
			ok.body = g
		}
		return "gzip-odd", ok
	case 21:
		ok.headers = append(ok.headers, [2]string{"Content-Encoding", []string{"identity", "br, identity", "gzip, , "}[r.IntN(3)]})
		return "unknown-coding", ok
	case 26:
		ok.headers = append(ok.headers, [2]string{"Content-Encoding", "deflate"})
		z := repairDeflate(ok.body, true)
		switch r.IntN(5) {
		case 0:
			z[len(z)-1] ^= 1 // a bad Adler-32
		case 1:
			z = z[:1+r.IntN(len(z)-1)]
		case 2:
			z[1] ^= 1 // a bad header check
		case 3:
			z = append(z, 'x', 'y')
		default:
			z = nil
		}
		ok.body = z
		return "deflate-odd", ok
	case 27:
		ok.headers = append(ok.headers, [2]string{"Content-Encoding", []string{"gzip, deflate", "deflate, gzip", "a,b,c,d,e,f", "gzip,gzip,gzip,gzip,gzip"}[r.IntN(4)]})
		ok.body = repairDeflate(repairGzip(ok.body), true)
		return "multi-coding", ok
	case 22:
		ok.redirect = []int{301, 302, 303, 307, 308}[r.IntN(5)]
		if r.IntN(3) == 0 {
			// The causes undici gives fetch failed, which postJSON replaces:
			// a non-HTTP scheme, a loop past 20 hops, credentials in Location.
			ok.location = []string{"ftp://example.test/x", "/v1/responses", "http://u:p@%HOST%/v1/final"}[r.IntN(3)]
			return "redirect-error", ok
		}
		return "redirect", ok
	case 23:
		return "status-" + strconv.Itoa(k), repairReply{status: []int{201, 204, 299, 300, 304, 199 + 1}[r.IntN(6)], body: ok.body}
	case 24:
		return "error-json", json200(jsjson.MustObject("error", jsjson.MakeString("x"), "output", jsjson.MakeString("o")))
	case 25:
		ok.body = []byte(strings.Replace(string(ok.body), `"resp_1"`, `"\ud800"`, 1))
		return "escaped-surrogate", ok
	}
	return "valid", ok
}

// repairSecretPacket is a signed packet whose fact statements hold text
// redactValue replaces before the POST.
func repairSecretPacket(r *rand.Rand) jsjson.Value {
	secrets := []string{"token=abcdef0123456789", "Authorization: Bearer abcdefghijklmnop", "password: 'hunter22hunter22'", "ok statement"}
	facts := make([]jsjson.Value, 3)
	for i := range facts {
		facts[i] = jsjson.MustObject("id", jsjson.MakeString("E"+strconv.Itoa(i+1)), "kind", jsjson.MakeString("DERIVED"),
			"statement", jsjson.MakeString("fact "+secrets[r.IntN(len(secrets))]))
	}
	packet := jsjson.MustObject(
		"schemaVersion", jsjson.MakeString(repair.EvidencePacketVersion),
		"investigationDigest", jsjson.MakeString("sha256:"+strings.Repeat("1", 64)),
		"frozenWitnessDigest", jsjson.MakeString("sha256:"+strings.Repeat("2", 64)),
		"recorder", jsjson.MakeString("git-commit-range-replay"),
		"nativeCodexInterception", jsjson.MakeBool(false),
		"facts", jsjson.MakeArray(facts),
		"limitations", repairStrings("limit"),
	)
	repairSign(packet)
	return packet
}

func TestLiveProposeRepairBrief(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	srv := &repairServer{}
	hs := httptest.NewServer(srv)
	defer hs.Close()
	url := hs.URL + "/v1/responses"
	t.Setenv("FAULTLINE_TEST_OPENAI_URL", url)
	t.Setenv("OPENAI_API_KEY", "")

	real, err := repair.CreateEvidencePacket(repairParse(t, seedTexts(t, "git-unbound/investigation.json")[0]))
	if err != nil {
		t.Fatal(err)
	}
	models := []string{"gpt-5.6", "", "model " + astralChar, strings.Repeat("m", 300), "lone " + loneSurrogate}
	r := rand.New(rand.NewPCG(46, 10))
	reached := map[string]bool{}
	for i := range 260 {
		packet := repairClone(t, real)
		if i%4 == 1 {
			packet = repairSecretPacket(r)
		}
		key := jsjson.MakeString("fl-test-key")
		var kind string
		var reply repairReply
		switch {
		case i < 220:
			kind, reply = randomRepairReply(r, packet)
		case i < 240:
			// Keys fetch sends after trimming, or refuses before connecting.
			kind = "key"
			_, reply = randomRepairReply(r, packet)
			key = []jsjson.Value{jsjson.MakeNull(), jsjson.MakeString(""), jsjson.MakeString(" fl-test-key\t"), jsjson.MakeString("fl-tést-key"),
				jsjson.MakeString("fl\ntest"), jsjson.MakeString("flĀ"), jsjson.MakeString(astralChar), jsjson.MakeString("fl\x7ftest"),
				jsjson.MakeString("fl\x01test"), jsjson.MakeString("\n"), jsjson.MakeString("a\r"), jsjson.MakeString("fl\ttest")}[r.IntN(12)]
		default:
			// Packets refused before the key is read.
			kind = "packet"
			o := packet.Obj()
			switch r.IntN(4) {
			case 0:
				o.Set("packetDigest", jsjson.MakeString("sha256:"+strings.Repeat("3", 64)))
			case 1:
				o.Set("zzExtra", jsjson.MakeNull())
			case 2:
				o.Set("facts", jsjson.MakeArray(nil))
				repairSign(packet)
			default:
				o.Field("facts").Items()[0].Obj().Set("statement", jsjson.MakeString("changed"))
			}
		}
		model := models[r.IntN(len(models))]
		pt := repairText(packet)
		if key.Kind() == jsjson.Null {
			os.Unsetenv("OPENAI_API_KEY")
		} else {
			os.Setenv("OPENAI_API_KEY", key.Str())
		}
		got, goRecords := srv.run(reply, func() string {
			var out string
			if err := jsexc.Try(func() {
				brief, err := repair.ProposeBrief(repairParse(t, pt), model)
				if err != nil {
					out = "THREW:" + jsexc.Message(err)
					return
				}
				out = repairText(brief)
			}); err != nil {
				out = "THREW:" + jsexc.Message(err)
			}
			return out
		})
		args := `{"packet":` + jsjson.Quote(pt) + `,"model":` + jsjson.Quote(model) + `,"apiKey":` + repairText(key) + `,"url":` + jsjson.Quote(url) + "}"
		want, tsRecords := srv.run(reply, func() string { return oracleText(t, c, "proposeRepairBriefWithGpt", args) })
		if got != want {
			t.Fatalf("case %d (%s, model %q, key %s):\ngot  %s\nwant %s", i, kind, model, repairText(key), got, want)
		}
		recordOutput(t, jsjson.Quote(want))
		if !slices.Equal(goRecords, tsRecords) {
			t.Fatalf("case %d (%s): requests differ:\ngo %q\nts %q", i, kind, goRecords, tsRecords)
		}
		outcome := got
		if !strings.HasPrefix(got, "THREW:") {
			outcome = "brief"
		}
		reached[kind+" -> "+outcome] = true
	}
	keys := make([]string, 0, len(reached))
	for k := range reached {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	short := make([]string, len(keys))
	for i, k := range keys {
		short[i] = strconv.QuoteToGraphic(k[:min(len(k), 100)])
	}
	t.Logf("%d outcomes:\n%s", len(keys), strings.Join(short, "\n"))
	for _, want := range []string{"Refusing to send a repair packet with an invalid digest.", "OPENAI_API_KEY is required", "OpenAI Responses request failed: 400",
		"OpenAI Responses request failed: 429", "OpenAI Responses request failed: 500", "GPT-5.6 repair brief was rejected: repair brief cites",
		"GPT-5.6 repair brief was rejected: repair brief references", "multiple output_text values", "fast path disagrees",
		"Unexpected end of JSON input", "is not valid JSON", "terminated", "fetch failed", "Cannot convert argument to a ByteString", "is an invalid header value"} {
		found := false
		for _, k := range keys {
			found = found || strings.Contains(k, want)
		}
		if !found {
			t.Errorf("never reached %q", want)
		}
	}
	if !reached["valid -> brief"] {
		t.Error("never produced a brief")
	}
	requireReached(t, "internal/repair/live.go", recordedOutputs(t))
}

// repairStreams are compressed inputs: every zlib level and strategy, as
// gzip, zlib and raw deflate, over short, repetitive, random and
// multi-byte texts.
func repairStreams(r *rand.Rand) [][2]any {
	random := make([]byte, 3000)
	for i := range random {
		random[i] = byte(r.IntN(256))
	}
	texts := [][]byte{[]byte(`{"output_text":"abc"}`), nil, []byte("a"), []byte(strings.Repeat("abcabcabc", 200)), random,
		[]byte(strings.Repeat("é"+astralChar, 400))}
	var out [][2]any
	for _, text := range texts {
		for _, level := range []int{flate.NoCompression, flate.BestSpeed, flate.DefaultCompression, flate.BestCompression, flate.HuffmanOnly} {
			var g, z, d bytes.Buffer
			gw, _ := gzip.NewWriterLevel(&g, level)
			gw.Write(text)
			gw.Close()
			zw, _ := zlib.NewWriterLevel(&z, level)
			zw.Write(text)
			zw.Close()
			dw, _ := flate.NewWriter(&d, level)
			dw.Write(text)
			dw.Close()
			out = append(out, [2]any{"gzip", g.Bytes()}, [2]any{"deflate", z.Bytes()}, [2]any{"deflate", d.Bytes()})
		}
	}
	return out
}

// TestLiveResponseDecoders checks the Go ports of the decoders undici runs
// on a Content-Encoding (Node's zlib with Z_SYNC_FLUSH, which yields what a
// truncated stream decoded) against Node's zlib: on truncations, bit
// flips, trailing bytes and random block headers.
func TestLiveResponseDecoders(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(46, 11))
	check := func(kind string, b []byte) string {
		out, err := repair.DecodeResponseBodyForTest(kind, b)
		got := hex.EncodeToString(out)
		if err != nil {
			got = "E"
		}
		liveCheck(t, c, "responseDecode", `{"kind":"`+kind+`","hex":"`+hex.EncodeToString(b)+`"}`, got)
		return got
	}
	errors, partial := 0, 0
	for _, s := range repairStreams(r) {
		kind, full := s[0].(string), s[1].([]byte)
		for n := 0; n <= len(full); n += max(1, len(full)/40) {
			if got := check(kind, full[:n]); got == "E" {
				errors++
			} else if n < len(full) && got != "" {
				partial++
			}
		}
		for range 25 {
			b := slices.Clone(full)
			if len(b) == 0 {
				break
			}
			for range 1 + r.IntN(3) {
				b[r.IntN(len(b))] ^= 1 << r.IntN(8)
			}
			if check(kind, b) == "E" {
				errors++
			}
		}
		check(kind, append(slices.Clone(full), 0, 1, 2))
		check(kind, append(slices.Clone(full), 'x', 'y'))
		check(kind, append(slices.Clone(full), full...))
	}
	// Random raw blocks: stored, fixed and dynamic headers (small HLIT and
	// HDIST half the time, so the code tables complete).
	for range 3000 {
		b := make([]byte, 1+r.IntN(120))
		for i := range b {
			b[i] = byte(r.IntN(256))
		}
		b[0] = b[0]&^7 | []byte{0b101, 0b011, 0b100, 0b101}[r.IntN(4)]
		if r.IntN(2) == 0 && len(b) > 1 {
			b[1] &= 0x0f
		}
		check("deflate", b)
		check("gzip", append([]byte{0x1f, 0x8b, 8, byte(r.IntN(32)), 0, 0, 0, 0, 0, 3}, b...))
	}
	t.Logf("%d errors, %d partial decodes", errors, partial)
	if errors == 0 || partial == 0 {
		t.Fatal("an outcome was never reached")
	}
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
		"worktrees-blocked", "nested-output", "bad-id", "missing-bundle", "removed-worktree", "dirty-bundle-subdir", "gitdir-deleted", "empty-managed-root"}
	var cases []repairWorktreeCase
	// 28 cases is two rounds of every kind (keep off, then on); the knob
	// raises it, as FAULTLINE_CP_CASES does for cpSync.
	n := 2 * len(kinds)
	if v := os.Getenv("FAULTLINE_REPAIR_WT_CASES"); v != "" {
		var err error
		if n, err = strconv.Atoi(v); err != nil {
			t.Fatalf("FAULTLINE_REPAIR_WT_CASES: %v", err)
		}
	}
	for i := 0; i < n; i++ {
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
				must := func(err error) {
					t.Helper()
					if err != nil {
						t.Fatal(err)
					}
				}
				must(os.MkdirAll(filepath.Join(bundle, "runs"), 0o755))
				must(os.WriteFile(filepath.Join(bundle, "manifest.json"), []byte("{}\n"), 0o644))
				must(os.WriteFile(filepath.Join(bundle, "runs", "a.json"), []byte("[1]\n"), 0o600))
				output := filepath.Join(root, "repairs", "r1")
				base, repository := commit, s.repo
				switch tc.kind {
				case "unknown-base":
					base = strings.Repeat("ab", 20)
				case "non-repository":
					repository = filepath.Join(root, "plain")
					must(os.Mkdir(repository, 0o755))
				case "symlinked-parent":
					must(os.Mkdir(filepath.Join(root, "real"), 0o755))
					os.Symlink("real", filepath.Join(root, "link"))
					output = filepath.Join(root, "link", "r1")
				case "existing-output":
					must(os.MkdirAll(output, 0o755))
				case "bundle-symlink":
					must(os.Symlink("manifest.json", filepath.Join(bundle, "link")))
					os.Symlink("../nowhere", filepath.Join(bundle, "runs", "dangling"))
				case "worktrees-blocked":
					must(os.WriteFile(filepath.Join(s.repo, ".git", "worktrees"), []byte("not a directory"), 0o644))
				case "nested-output":
					output = filepath.Join(root, "a", "b", "c", "r1")
				case "bad-id":
					// every variant runs in this case, below
				case "missing-bundle":
					bundle = filepath.Join(root, "no-bundle")
				case "dirty-bundle-subdir":
					must(os.MkdirAll(filepath.Join(bundle, "a", "b", "c"), 0o700))
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
				if tc.kind == "bad-id" {
					// Each id the regexp rejects throws before any filesystem
					// work; the 64-hex id that names no object runs last,
					// because it gets as far as creating the output.
					variants := []string{"nothex", strings.ToUpper(commit), commit[:39], commit + "0", "", strings.Repeat("0", 63), strings.Repeat("c", 64)}
					for _, id := range variants {
						var one string
						if k == 0 {
							t.Setenv("PATH", path)
							_, err := repair.CreateWorktree(repository, id, bundle, output)
							one = "THREW:" + jsexc.Message(err)
							if err == nil {
								one = "OK"
							}
						} else {
							one = oracleText(t, c, "createRepairWorktree", fmt.Sprintf(`{"repository":%s,"baseCommit":%s,"bundleDirectory":%s,"outputDirectory":%s,"path":%s}`,
								jsjson.Stringify(jsjson.MakeString(repository)), jsjson.Stringify(jsjson.MakeString(id)),
								jsjson.Stringify(jsjson.MakeString(bundle)), jsjson.Stringify(jsjson.MakeString(output)), jsjson.Stringify(jsjson.MakeString(path))))
						}
						created += one + "\n"
					}
				} else if k == 0 {
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
					if tc.kind != "removed-worktree" {
						if out, err := exec.Command(realGit, "-C", sess.WorktreePath, "add", "-N", "new.txt", "bin.dat").CombinedOutput(); err != nil {
							t.Fatalf("git add -N: %v\n%s", err, out)
						}
					}
					if tc.kind == "removed-worktree" {
						must(os.RemoveAll(sess.WorktreePath))
					}
					if tc.kind == "gitdir-deleted" {
						// git no longer knows the worktree: remove fails, the
						// rmSync fallback runs, then prune.
						must(os.RemoveAll(filepath.Join(sess.Repository, ".git", "worktrees", "worktree")))
					}
					managedRoot := sess.ManagedRoot
					if tc.kind == "empty-managed-root" {
						managedRoot = ""
					}
					rargs := fmt.Sprintf(`{"repository":%s,"worktreePath":%s,"managedRoot":%s,"keepManagedRoot":%t,"path":%s}`,
						jsjson.Stringify(jsjson.MakeString(sess.Repository)), jsjson.Stringify(jsjson.MakeString(sess.WorktreePath)),
						jsjson.Stringify(jsjson.MakeString(managedRoot)), tc.keep, jsjson.Stringify(jsjson.MakeString(path)))
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
						cleanup = cl(repair.RemoveWorktree(sess.Repository, sess.WorktreePath, managedRoot, tc.keep))
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
			recordOutput(t, w.out+"\n"+w.cleanup+"\n"+w.patch)
			kindsSeen[tc.kind]++
			if tc.kind == "gitdir-deleted" || tc.kind == "empty-managed-root" || tc.kind == "bad-id" {
				t.Logf("node: create %.150q cleanup %.300q", w.out, w.cleanup)
			}
		})
	}
	t.Logf("cases per kind: %v", kindsSeen)
	if os.Getenv("FAULTLINE_REPAIR_WT_CASES") == "" {
		requireReached(t, "internal/repair/worktree.go", recordedOutputs(t))
	}
}
