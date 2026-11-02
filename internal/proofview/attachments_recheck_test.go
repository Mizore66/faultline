package proofview

import (
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/minimize"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/repair"
)

// ---- shared facts: one real base load for the whole staged suite ----

type baseFacts struct {
	view          View
	invDigest     string
	frozenDigest  string
	witnessDigest string
	passToFail    jsjson.Value // the base PASS_TO_FAIL transition
	failToPass    jsjson.Value // a real non-PASS_TO_FAIL transition
}

var (
	factsOnce sync.Once
	facts     baseFacts
	factsErr  error
)

func loadFacts(t *testing.T) baseFacts {
	t.Helper()
	factsOnce.Do(func() {
		corpus := serveBundleCorpus(t)
		bundleDir, root, _ := attachmentInputs(t, corpus, serveBundleCase(t, corpus, "view-root-match"))
		view, err := LoadVerifiedGitProofView(bundleDir, root)
		if err != nil {
			factsErr = err
			return
		}
		facts.view = view
		facts.invDigest = bundle.Must(canonical.DigestJSON(view.Investigation))
		facts.frozenDigest = view.FrozenWitness.Get("frozenDigest").Str()
		facts.witnessDigest = view.FrozenWitness.Get("witnessDigest").Str()
		for _, tr := range view.Investigation.Get("transitions").Items() {
			switch tr.Get("kind").Str() {
			case "PASS_TO_FAIL":
				facts.passToFail = tr
			case "FAIL_TO_PASS":
				facts.failToPass = tr
			}
		}
	})
	if factsErr != nil {
		t.Fatal(factsErr)
	}
	if facts.passToFail.Kind() != jsjson.Object || facts.failToPass.Kind() != jsjson.Object || facts.frozenDigest == facts.witnessDigest {
		t.Fatal("retained fixture lacks distinct digests or its two transitions")
	}
	return facts
}

func dg(c byte) string { return "sha256:" + strings.Repeat(string(c), 64) }

func state(commit, tree string) jsjson.Value {
	return jsjson.MustObject("commit", str(commit), "tree", str(tree))
}

func endpoint(tr jsjson.Value, side string) jsjson.Value {
	return state(tr.Get(side, "commit").Str(), tr.Get(side, "tree").Str())
}

// attempt runs fn under jsexc.Try only (no broad recover).
func attempt(fn func()) (string, bool) {
	err := jsexc.Try(fn)
	if err != nil {
		return jsexc.Message(err), false
	}
	return "", true
}

func sptr(s string) *string { return &s }

// ---- staged dependencies: invocation-local values, fail on any extra call ----

type staged struct {
	t     *testing.T
	trace []string

	minQ   []minimize.Verification
	repQ   []repair.BriefVerification
	readQ  []repairContents
	prevQ  []stagedPrevention
	minArg []stagedArgs
	repArg []stagedArgs
	preArg []stagedArgs
}

type stagedArgs struct {
	path     string
	expected string
	provided bool
}

type stagedPrevention struct {
	result     bundle.Result
	man, body  jsjson.Value
	rootString string
}

func (s *staged) deps() attachmentDependencies {
	return attachmentDependencies{
		minimize: func(path, expected string, provided bool) minimize.Verification {
			s.trace = append(s.trace, "min.verify")
			s.minArg = append(s.minArg, stagedArgs{path, expected, provided})
			if len(s.minQ) == 0 {
				s.t.Fatalf("unexpected minimize verification #%d; trace %v", len(s.minArg), s.trace)
			}
			v := s.minQ[0]
			s.minQ = s.minQ[1:]
			return v
		},
		repair: func(path string, expected *string) repair.BriefVerification {
			s.trace = append(s.trace, "repair.verify")
			a := stagedArgs{path: path, provided: expected != nil}
			if expected != nil {
				a.expected = *expected
			}
			s.repArg = append(s.repArg, a)
			if len(s.repQ) == 0 {
				s.t.Fatalf("unexpected repair verification; trace %v", s.trace)
			}
			v := s.repQ[0]
			s.repQ = s.repQ[1:]
			return v
		},
		readRepair: func(string) repairContents {
			s.trace = append(s.trace, "repair.read")
			if len(s.readQ) == 0 {
				s.t.Fatalf("unexpected repair read; trace %v", s.trace)
			}
			v := s.readQ[0]
			s.readQ = s.readQ[1:]
			return v
		},
		prevention: func(path, expected string, provided bool) (bundle.Result, jsjson.Value, jsjson.Value) {
			s.trace = append(s.trace, "prevention.verify")
			s.preArg = append(s.preArg, stagedArgs{path, expected, provided})
			if len(s.prevQ) == 0 {
				s.t.Fatalf("unexpected prevention verification; trace %v", s.trace)
			}
			v := s.prevQ[0]
			s.prevQ = s.prevQ[1:]
			return v.result, v.man, v.body
		},
	}
}

func expectTrace(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("trace\n got %v\nwant %v", got, want)
	}
}

// ---- minimization ----

func minValue(witness, before, after jsjson.Value) jsjson.Value {
	return jsjson.MustObject("witness", witness, "before", before, "after", after, "marker", str("m"))
}

func goodWitness(f baseFacts) jsjson.Value {
	return jsjson.MustObject("frozenDigest", str(f.frozenDigest), "witnessDigest", str(f.witnessDigest))
}

func minVerification(result jsjson.Value, digest, status string) minimize.Verification {
	d := digest
	return minimize.Verification{Valid: true, Result: &result, ResultDigest: &d, ExternalDigestStatus: status}
}

func TestAttachmentMinimizationBindingGuardsInTSOrder(t *testing.T) {
	f := loadFacts(t)
	good := goodWitness(f)
	before, after := endpoint(f.passToFail, "before"), endpoint(f.passToFail, "after")
	null := jsjson.MakeNull()
	wrongFrozen := jsjson.MustObject("frozenDigest", str(dg('e')), "witnessDigest", str(f.witnessDigest))
	wrongWitness := jsjson.MustObject("frozenDigest", str(f.frozenDigest), "witnessDigest", str(dg('e')))
	// A frozen digest copied into the witness slot catches the two digests being swapped.
	swapped := jsjson.MustObject("frozenDigest", str(f.witnessDigest), "witnessDigest", str(f.frozenDigest))
	bothWrong := jsjson.MustObject("frozenDigest", str(dg('e')), "witnessDigest", str(dg('f')))

	const (
		eNullWitness = "minimization has no frozen-witness summary"
		eFrozen      = "minimization frozen digest does not match the Git proof witness"
		eWitness     = "minimization witness digest does not match the Git proof witness"
		eNullStates  = "minimization has no resolved before/after states"
		eRange       = "minimization range is not a stable PASS_TO_FAIL transition in the Git proof package"
	)
	cases := []struct {
		name                   string
		witness, before, after jsjson.Value
		want                   []string
	}{
		{"witness-null", null, before, after, []string{eNullWitness}},
		{"frozen-wrong", wrongFrozen, before, after, []string{eFrozen}},
		{"witness-wrong", wrongWitness, before, after, []string{eWitness}},
		{"digests-swapped", swapped, before, after, []string{eFrozen, eWitness}},
		{"both-wrong-order", bothWrong, before, after, []string{eFrozen, eWitness}},
		{"before-null", good, null, after, []string{eNullStates}},
		{"after-null", good, before, null, []string{eNullStates}},
		{"both-null-once", good, null, null, []string{eNullStates}},
		{"before-commit", good, state(dg('1'), before.Get("tree").Str()), after, []string{eRange}},
		{"before-tree", good, state(before.Get("commit").Str(), dg('1')), after, []string{eRange}},
		{"after-commit", good, before, state(dg('1'), after.Get("tree").Str()), []string{eRange}},
		{"after-tree", good, before, state(after.Get("commit").Str(), dg('1')), []string{eRange}},
		{"wrong-transition-kind", good, endpoint(f.failToPass, "before"), endpoint(f.failToPass, "after"), []string{eRange}},
		{"null-witness-then-null-states", null, null, after, []string{eNullWitness, eNullStates}},
		{"frozen-wrong-then-range", wrongFrozen, state(dg('1'), "x"), after, []string{eFrozen, eRange}},
		{"witness-wrong-then-range", wrongWitness, before, state(dg('1'), "x"), []string{eWitness, eRange}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &staged{t: t, minQ: []minimize.Verification{minVerification(minValue(c.witness, c.before, c.after), dg('a'), "NOT_PROVIDED")}}
			msg, ok := attempt(func() { loadMinimization("rel/min.json", nil, f.view.Investigation, f.view.FrozenWitness, s.deps()) })
			want := "Refusing to attach a minimization record for a different incident: " + strings.Join(c.want, "; ")
			if ok || msg != want {
				t.Fatalf("ok=%v\n got %q\nwant %q", ok, msg, want)
			}
			// Binding failure happens after the first verification and before the second.
			expectTrace(t, s.trace, "min.verify")
		})
	}
}

func TestAttachmentMinimizationPassesTwiceAndReturnsSecondSnapshot(t *testing.T) {
	f := loadFacts(t)
	before, after := endpoint(f.passToFail, "before"), endpoint(f.passToFail, "after")
	first := minVerification(minValue(goodWitness(f), before, after), dg('a'), "NOT_PROVIDED")
	// Same canonical content, different member order and distinct status: tells the passes apart.
	reordered := jsjson.MustObject("marker", str("m"), "after", after, "before", before, "witness", goodWitness(f))
	second := minVerification(reordered, dg('a'), "MATCH")
	s := &staged{t: t, minQ: []minimize.Verification{first, second}}
	var got *VerifiedMinimizationAttachment
	if msg, ok := attempt(func() {
		got = loadMinimization("rel/min.json", sptr(dg('a')), f.view.Investigation, f.view.FrozenWitness, s.deps())
	}); !ok {
		t.Fatal(msg)
	}
	expectTrace(t, s.trace, "min.verify", "min.verify")
	if got == nil || got.ExternalDigestStatus != "MATCH" || got.ResultDigest != dg('a') {
		t.Fatalf("not the second pass: %+v", got)
	}
	if jsjson.Stringify(got.Result) != jsjson.Stringify(reordered) {
		t.Fatalf("result is not the second snapshot, in its own order")
	}
	want := nodefs.Resolve("rel/min.json")
	for i, a := range s.minArg {
		if a.path != want || !a.provided || a.expected != dg('a') {
			t.Fatalf("pass %d args %+v, want resolved path %q and the supplied digest", i, a, want)
		}
	}
}

func TestAttachmentMinimizationNilExpectedIsUndefinedAndEmptyIsProvided(t *testing.T) {
	f := loadFacts(t)
	ok1 := minVerification(minValue(goodWitness(f), endpoint(f.passToFail, "before"), endpoint(f.passToFail, "after")), dg('a'), "NOT_PROVIDED")
	for name, c := range map[string]struct {
		expected *string
		provided bool
		value    string
	}{"nil": {nil, false, ""}, "empty": {sptr(""), true, ""}} {
		t.Run(name, func(t *testing.T) {
			s := &staged{t: t, minQ: []minimize.Verification{ok1, ok1}}
			if msg, ok := attempt(func() { loadMinimization("m.json", c.expected, f.view.Investigation, f.view.FrozenWitness, s.deps()) }); !ok {
				t.Fatal(msg)
			}
			for _, a := range s.minArg {
				if a.provided != c.provided || a.expected != c.value {
					t.Fatalf("args %+v", a)
				}
			}
		})
	}
}

func TestAttachmentMinimizationRecheck(t *testing.T) {
	f := loadFacts(t)
	good := minValue(goodWitness(f), endpoint(f.passToFail, "before"), endpoint(f.passToFail, "after"))
	other := jsjson.MustObject("different", str("content"))
	pass := func(digest string) minimize.Verification { return minVerification(good, digest, "NOT_PROVIDED") }
	const invalid = "Refusing to attach an invalid minimization record: "
	const changed = "Refusing to attach a minimization record that changed during verification: "
	nilDigest := minimize.Verification{Valid: true, Result: &good}
	nilResult := minimize.Verification{Valid: true, ResultDigest: sptr(dg('a'))}
	cases := []struct {
		name  string
		q     []minimize.Verification
		want  string
		calls int
	}{
		{"first-invalid", []minimize.Verification{{Valid: false, Errors: []string{"bad one", "bad two"}}}, invalid + "bad one; bad two", 1},
		{"first-invalid-empty-errors", []minimize.Verification{{Valid: false}}, invalid, 1},
		{"first-valid-without-result", []minimize.Verification{nilResult}, invalid, 1},
		{"first-valid-without-digest", []minimize.Verification{nilDigest}, invalid, 1},
		{"second-invalid", []minimize.Verification{pass(dg('a')), {Valid: false, Errors: []string{"gone"}}}, changed + "gone", 2},
		{"second-invalid-no-errors", []minimize.Verification{pass(dg('a')), {Valid: false}}, changed + "digest changed", 2},
		{"second-digest-differs", []minimize.Verification{pass(dg('a')), pass(dg('b'))}, changed + "digest changed", 2},
		{"second-result-differs-same-digest", []minimize.Verification{pass(dg('a')), minVerification(other, dg('a'), "NOT_PROVIDED")}, changed + "digest changed", 2},
		{"second-valid-without-result", []minimize.Verification{pass(dg('a')), nilResult}, changed + "digest changed", 2},
		{"second-valid-without-digest", []minimize.Verification{pass(dg('a')), nilDigest}, changed + "digest changed", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &staged{t: t, minQ: c.q}
			msg, ok := attempt(func() { loadMinimization("m.json", nil, f.view.Investigation, f.view.FrozenWitness, s.deps()) })
			if ok || msg != c.want {
				t.Fatalf("ok=%v\n got %q\nwant %q", ok, msg, c.want)
			}
			if len(s.trace) != c.calls {
				t.Fatalf("trace %v, want %d minimization verifications", s.trace, c.calls)
			}
		})
	}
}

// ---- repair ----

type repairScenario struct {
	f                    baseFacts
	first, second, third repair.BriefVerification
	one, two             repairContents
	firstManifest        jsjson.Value
	thirdManifest        jsjson.Value
}

func repairContentsFor(f baseFacts, investigation, frozen string, note string) repairContents {
	packet := jsjson.MustObject(
		"schemaVersion", str("faultline.repair-evidence.v2"),
		"investigationDigest", str(investigation),
		"frozenWitnessDigest", str(frozen),
		"packetDigest", str(dg('c')),
		"note", str(note))
	brief := jsjson.MustObject("schemaVersion", str("faultline.repair-brief.v1"), "body", str("b"+note))
	return repairContents{packet: packet, brief: brief}
}

func manifestFor(c repairContents, reversed bool) jsjson.Value {
	packetDigest := c.packet.Get("packetDigest").Str()
	briefDigest := bundle.Must(canonical.DigestJSON(c.brief))
	a := jsjson.MustObject("digest", str(packetDigest))
	b := jsjson.MustObject("digest", str(briefDigest))
	if reversed {
		return jsjson.MustObject("repairBrief", b, "evidencePacket", a, "manifestDigest", str(dg('d')))
	}
	return jsjson.MustObject("manifestDigest", str(dg('d')), "evidencePacket", a, "repairBrief", b)
}

func goodRepair(t *testing.T) repairScenario {
	f := loadFacts(t)
	c := repairContentsFor(f, f.invDigest, f.frozenDigest, "n")
	m1, m3 := manifestFor(c, false), manifestFor(c, true)
	return repairScenario{
		f: f, one: c, two: c, firstManifest: m1, thirdManifest: m3,
		first:  repair.BriefVerification{Valid: true, Manifest: m1, ExternalDigestStatus: "s1"},
		second: repair.BriefVerification{Valid: true, Manifest: m1, ExternalDigestStatus: "s2"},
		third:  repair.BriefVerification{Valid: true, Manifest: m3, ExternalDigestStatus: "s3"},
	}
}

func (r repairScenario) run(t *testing.T, expected *string) (*VerifiedRepairAttachment, string, bool, *staged) {
	s := &staged{t: t,
		repQ:  []repair.BriefVerification{r.first, r.second, r.third},
		readQ: []repairContents{r.one, r.two}}
	var got *VerifiedRepairAttachment
	msg, ok := attempt(func() {
		got = loadRepair("rel/repair", expected, r.f.view.Investigation, r.f.view.FrozenWitness, s.deps())
	})
	return got, msg, ok, s
}

func TestAttachmentRepairHappyPathTraceAndFinalSnapshots(t *testing.T) {
	r := goodRepair(t)
	// Distinguish pass two from the final contents: pass two reads carry a different, equal-canonical order.
	reorderedPacket := jsjson.MustObject(
		"note", r.two.packet.Get("note"), "packetDigest", r.two.packet.Get("packetDigest"),
		"frozenWitnessDigest", r.two.packet.Get("frozenWitnessDigest"),
		"investigationDigest", r.two.packet.Get("investigationDigest"), "schemaVersion", r.two.packet.Get("schemaVersion"))
	r.two = repairContents{packet: reorderedPacket, brief: r.two.brief}
	got, msg, ok, s := r.run(t, sptr(dg('a')))
	if !ok {
		t.Fatal(msg)
	}
	expectTrace(t, s.trace, "repair.verify", "repair.read", "repair.verify", "repair.read", "repair.verify")
	if got.ExternalDigestStatus != "s3" {
		t.Fatalf("status %q is not the third verification's", got.ExternalDigestStatus)
	}
	if jsjson.Stringify(got.Manifest) != jsjson.Stringify(r.thirdManifest) || jsjson.Stringify(got.Manifest) == jsjson.Stringify(r.firstManifest) {
		t.Fatalf("manifest is not the third pass's")
	}
	if jsjson.Stringify(got.Packet) != jsjson.Stringify(reorderedPacket) || jsjson.Stringify(got.Brief) != jsjson.Stringify(r.two.brief) {
		t.Fatalf("packet/brief are not the second read's")
	}
	for _, a := range s.repArg {
		if a.path != nodefs.Resolve("rel/repair") || !a.provided || a.expected != dg('a') {
			t.Fatalf("verifier args %+v", a)
		}
	}
	// nil expected stays undefined for all three verifications.
	_, _, ok, s = goodRepair(t).run(t, nil)
	if !ok {
		t.Fatal("nil expected must succeed")
	}
	for _, a := range s.repArg {
		if a.provided {
			t.Fatalf("nil expected was passed as %+v", a)
		}
	}
}

type repairCase struct {
	name   string
	mutate func(r *repairScenario)
	want   string
	trace  []string
}

func hasAnyPrefix(name string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func runRepairCases(t *testing.T, keep func(name string) bool) {
	ran := 0
	for _, c := range repairCases() {
		if !keep(c.name) {
			continue
		}
		ran++
		t.Run(c.name, func(t *testing.T) {
			r := goodRepair(t)
			c.mutate(&r)
			got, msg, ok, s := r.run(t, nil)
			if ok || got != nil || msg != c.want {
				t.Fatalf("ok=%v got=%v\n msg %q\nwant %q", ok, got, msg, c.want)
			}
			expectTrace(t, s.trace, c.trace...)
		})
	}
	if ran == 0 {
		t.Fatal("no repair cases selected")
	}
}

// Initial invalid verification and the two parsed-value bindings.
func TestAttachmentRepairBindingGuards(t *testing.T) {
	runRepairCases(t, func(n string) bool { return hasAnyPrefix(n, "first-", "investigation-", "frozen-") })
}

// The first snapshot (verified manifest versus the first in-memory read) check.
func TestAttachmentRepairFirstSnapshotMismatch(t *testing.T) {
	runRepairCases(t, func(n string) bool { return hasAnyPrefix(n, "manifest-") })
}

// Each manifest, packet, brief and final-content condition independently.
func TestAttachmentRepairRecheck(t *testing.T) {
	runRepairCases(t, func(n string) bool { return hasAnyPrefix(n, "second-", "final-") })
}

func repairCases() []repairCase {
	const (
		invalid = "Refusing to attach an invalid repair brief: "
		payload = "Refusing to attach repair guidance whose in-memory payload does not match its verified manifest."
		diffInv = "Refusing to attach repair guidance from a different Git investigation."
		diffFz  = "Refusing to attach repair guidance for a different frozen witness."
		changed = "Refusing to attach repair guidance that changed during verification: "
	)
	vt, rd := "repair.verify", "repair.read"
	return []repairCase{
		{"first-invalid", func(r *repairScenario) {
			r.first = repair.BriefVerification{Valid: false, Errors: []string{"e1", "e2"}}
		}, invalid + "e1; e2", []string{vt}},
		{"first-invalid-empty-errors-keeps-empty-suffix", func(r *repairScenario) {
			r.first = repair.BriefVerification{Valid: false}
		}, invalid, []string{vt}},
		{"first-valid-manifest-undefined", func(r *repairScenario) {
			r.first = repair.BriefVerification{Valid: true}
		}, invalid, []string{vt}},
		{"first-valid-manifest-null", func(r *repairScenario) {
			r.first = repair.BriefVerification{Valid: true, Manifest: jsjson.MakeNull()}
		}, invalid, []string{vt}},
		{"manifest-packet-digest-differs-from-read", func(r *repairScenario) {
			r.one.packet = jsjson.MustObject("packetDigest", str(dg('9')), "investigationDigest", str(r.f.invDigest), "frozenWitnessDigest", str(r.f.frozenDigest))
		}, payload, []string{vt, rd}},
		{"manifest-brief-digest-differs-from-read", func(r *repairScenario) {
			r.one.brief = jsjson.MustObject("other", str("brief"))
		}, payload, []string{vt, rd}},
		{"investigation-wrong", func(r *repairScenario) {
			r.one.packet = repairContentsFor(r.f, dg('1'), r.f.frozenDigest, "n").packet
		}, diffInv, []string{vt, rd}},
		{"frozen-binding-uses-frozen-not-witness-digest", func(r *repairScenario) {
			r.one.packet = repairContentsFor(r.f, r.f.invDigest, r.f.witnessDigest, "n").packet
		}, diffFz, []string{vt, rd}},
		{"second-invalid", func(r *repairScenario) {
			r.second = repair.BriefVerification{Valid: false, Errors: []string{"s"}}
		}, changed + "s", []string{vt, rd, vt}},
		{"second-invalid-fallback", func(r *repairScenario) {
			r.second = repair.BriefVerification{Valid: false}
		}, changed + "manifest changed", []string{vt, rd, vt}},
		{"second-manifest-absent", func(r *repairScenario) {
			r.second = repair.BriefVerification{Valid: true}
		}, changed + "manifest changed", []string{vt, rd, vt}},
		{"second-manifest-differs", func(r *repairScenario) {
			m := jsjson.MustObject("manifestDigest", str(dg('d')), "extra", str("x"))
			r.second = repair.BriefVerification{Valid: true, Manifest: m}
		}, changed + "manifest changed", []string{vt, rd, vt}},
		{"final-invalid", func(r *repairScenario) {
			r.third = repair.BriefVerification{Valid: false, Errors: []string{"t"}}
		}, changed + "t", []string{vt, rd, vt, rd, vt}},
		{"final-invalid-fallback", func(r *repairScenario) {
			r.third = repair.BriefVerification{Valid: false}
		}, changed + "artifact contents changed", []string{vt, rd, vt, rd, vt}},
		{"final-manifest-absent", func(r *repairScenario) {
			r.third = repair.BriefVerification{Valid: true}
		}, changed + "artifact contents changed", []string{vt, rd, vt, rd, vt}},
		{"final-manifest-differs", func(r *repairScenario) {
			r.third = repair.BriefVerification{Valid: true, Manifest: jsjson.MustObject("other", str("x"))}
		}, changed + "artifact contents changed", []string{vt, rd, vt, rd, vt}},
		{"second-read-packet-changes-keeping-manifest-digest", func(r *repairScenario) {
			r.two.packet = repairContentsFor(r.f, r.f.invDigest, r.f.frozenDigest, "changed").packet
		}, changed + "artifact contents changed", []string{vt, rd, vt, rd, vt}},
		{"second-read-brief-changes-keeping-manifest-digest", func(r *repairScenario) {
			r.two.brief = jsjson.MustObject("schemaVersion", str("faultline.repair-brief.v1"), "body", str("different"))
		}, changed + "artifact contents changed", []string{vt, rd, vt, rd, vt}},
		{"second-read-digest-no-longer-matches-final-manifest", func(r *repairScenario) {
			r.two.packet = jsjson.MustObject("packetDigest", str(dg('8')), "investigationDigest", str(r.f.invDigest), "frozenWitnessDigest", str(r.f.frozenDigest))
		}, changed + "artifact contents changed", []string{vt, rd, vt, rd, vt}},
	}
}

// ---- prevention ----

type preventionScenario struct {
	f                     baseFacts
	root                  string
	first, second         stagedPrevention
	firstBody, secondBody jsjson.Value
}

func preventionBody(f baseFacts, root, witness string, investigation *string, reversed bool) jsjson.Value {
	o := jsjson.NewObj()
	set := func(k string, v jsjson.Value) { o.Set(k, v) }
	if reversed {
		set("tag", str("t"))
	}
	set("originalProofRoot", str(root))
	set("frozenWitnessDigest", str(witness))
	if investigation != nil {
		set("investigationDigest", str(*investigation))
	}
	if !reversed {
		set("tag", str("t"))
	}
	return jsjson.MakeObject(o)
}

func goodPrevention(t *testing.T, statusFirst, statusSecond string) preventionScenario {
	f := loadFacts(t)
	root := f.view.RootDigest
	inv := f.invDigest
	b1 := preventionBody(f, root, f.witnessDigest, &inv, false)
	b2 := preventionBody(f, root, f.witnessDigest, &inv, true)
	man := jsjson.MustObject("classification", str("PREVENTION_EVIDENCE_SUMMARY"))
	man2 := jsjson.MustObject("classification", str("PREVENTION_EVIDENCE_SUMMARY"), "second", str("yes"))
	r1, r2 := dg('1'), dg('1')
	return preventionScenario{
		f: f, root: root, firstBody: b1, secondBody: b2,
		first:  stagedPrevention{result: bundle.Result{Valid: true, RootDigest: &r1, ExternalRootStatus: statusFirst}, man: man, body: b1},
		second: stagedPrevention{result: bundle.Result{Valid: true, RootDigest: &r2, ExternalRootStatus: statusSecond}, man: man2, body: b2},
	}
}

func (p preventionScenario) run(t *testing.T, expected *string) (*VerifiedPreventionAttachment, string, bool, *staged) {
	s := &staged{t: t, prevQ: []stagedPrevention{p.first, p.second}}
	var got *VerifiedPreventionAttachment
	msg, ok := attempt(func() {
		got = loadPrevention("rel/prev", expected, p.f.view.Investigation, p.f.view.FrozenWitness, p.root, s.deps())
	})
	return got, msg, ok, s
}

func TestAttachmentPreventionHappyPathReturnsSecondPassAndBothClassifications(t *testing.T) {
	p := goodPrevention(t, "NOT_PROVIDED", "MATCH")
	got, msg, ok, s := p.run(t, sptr(dg('a')))
	if !ok {
		t.Fatal(msg)
	}
	expectTrace(t, s.trace, "prevention.verify", "prevention.verify")
	if got.ExternalDigestStatus != "MATCH" || got.RootDigest != dg('1') {
		t.Fatalf("not the second pass: %+v", got)
	}
	if jsjson.Stringify(got.Prevention) != jsjson.Stringify(p.secondBody) || jsjson.Stringify(got.Manifest) != jsjson.Stringify(p.second.man) {
		t.Fatalf("manifest/body are not the second pass's snapshots")
	}
	for _, a := range s.preArg {
		if a.path != nodefs.Resolve("rel/prev") || !a.provided || a.expected != dg('a') {
			t.Fatalf("args %+v", a)
		}
	}
	for _, class := range []string{"PREVENTION_EVIDENCE_SUMMARY", "PREVENTION_VERIFIED"} {
		q := goodPrevention(t, "NOT_PROVIDED", "NOT_PROVIDED")
		q.second.man = jsjson.MustObject("classification", str(class))
		if g, msg, ok, _ := q.run(t, nil); !ok || g.Manifest.Get("classification").Str() != class {
			t.Fatalf("%s must attach: %v %s", class, ok, msg)
		}
	}
	// nil expected stays undefined; an empty supplied string is provided.
	_, _, _, s = goodPrevention(t, "x", "y").run(t, nil)
	for _, a := range s.preArg {
		if a.provided || a.expected != "" {
			t.Fatalf("nil expected passed as %+v", a)
		}
	}
	_, _, _, s = goodPrevention(t, "x", "y").run(t, sptr(""))
	for _, a := range s.preArg {
		if !a.provided || a.expected != "" {
			t.Fatalf("empty expected passed as %+v", a)
		}
	}
}

func TestAttachmentPreventionAbsentInvestigationDigestAttaches(t *testing.T) {
	p := goodPrevention(t, "a", "b")
	body := preventionBody(p.f, p.root, p.f.witnessDigest, nil, false)
	p.first.body, p.second.body = body, body
	if _, msg, ok, _ := p.run(t, nil); !ok {
		t.Fatalf("an absent optional investigationDigest must attach: %s", msg)
	}
}

type preventionCase struct {
	name   string
	mutate func(p *preventionScenario)
	want   string
	trace  []string
}

func runPreventionCases(t *testing.T, keep func(name string) bool) {
	ran := 0
	for _, c := range preventionCases() {
		if !keep(c.name) {
			continue
		}
		ran++
		t.Run(c.name, func(t *testing.T) {
			p := goodPrevention(t, "a", "b")
			c.mutate(&p)
			got, msg, ok, s := p.run(t, nil)
			if ok || got != nil || msg != c.want {
				t.Fatalf("ok=%v got=%v\n msg %q\nwant %q", ok, got, msg, c.want)
			}
			expectTrace(t, s.trace, c.trace...)
		})
	}
	if ran == 0 {
		t.Fatal("no prevention cases selected")
	}
}

func TestAttachmentPreventionBindingGuards(t *testing.T) {
	runPreventionCases(t, func(n string) bool { return !strings.HasPrefix(n, "second-") })
}

// Root, body and later-invalid rejection on the second pass.
func TestAttachmentPreventionRecheck(t *testing.T) {
	runPreventionCases(t, func(n string) bool { return strings.HasPrefix(n, "second-") })
}

func preventionCases() []preventionCase {
	const (
		invalid = "Refusing to attach an invalid prevention proof: "
		diffRt  = "Refusing to attach a prevention proof bound to a different Git proof root."
		diffFz  = "Refusing to attach a prevention proof for a different frozen witness."
		diffInv = "Refusing to attach a prevention proof from a different Git investigation."
		changed = "Refusing to attach a prevention proof that changed during verification: "
	)
	pv := "prevention.verify"
	wrongInv := dg('7')
	return []preventionCase{
		{"first-invalid", func(p *preventionScenario) {
			p.first.result.Valid, p.first.result.Errors = false, []string{"x", "y"}
		}, invalid + "x; y", []string{pv}},
		{"first-invalid-empty-errors", func(p *preventionScenario) { p.first.result.Valid = false }, invalid, []string{pv}},
		{"first-manifest-absent", func(p *preventionScenario) { p.first.man = jsjson.Value{} }, invalid, []string{pv}},
		{"first-body-absent", func(p *preventionScenario) { p.first.body = jsjson.Value{} }, invalid, []string{pv}},
		{"first-root-absent", func(p *preventionScenario) { p.first.result.RootDigest = nil }, invalid, []string{pv}},
		{"original-root-wrong", func(p *preventionScenario) {
			p.first.body = preventionBody(p.f, dg('9'), p.f.witnessDigest, sptr(p.f.invDigest), false)
		}, diffRt, []string{pv}},
		{"witness-binding-uses-witness-not-frozen-digest", func(p *preventionScenario) {
			p.first.body = preventionBody(p.f, p.root, p.f.frozenDigest, sptr(p.f.invDigest), false)
		}, diffFz, []string{pv}},
		{"witness-wrong", func(p *preventionScenario) {
			p.first.body = preventionBody(p.f, p.root, dg('9'), sptr(p.f.invDigest), false)
		}, diffFz, []string{pv}},
		{"present-investigation-wrong", func(p *preventionScenario) {
			p.first.body = preventionBody(p.f, p.root, p.f.witnessDigest, &wrongInv, false)
		}, diffInv, []string{pv}},
		{"second-invalid", func(p *preventionScenario) {
			p.second.result.Valid, p.second.result.Errors = false, []string{"z"}
		}, changed + "z", []string{pv, pv}},
		{"second-invalid-fallback", func(p *preventionScenario) { p.second.result.Valid = false }, changed + "digest changed", []string{pv, pv}},
		{"second-manifest-absent", func(p *preventionScenario) { p.second.man = jsjson.Value{} }, changed + "digest changed", []string{pv, pv}},
		{"second-body-absent", func(p *preventionScenario) { p.second.body = jsjson.Value{} }, changed + "digest changed", []string{pv, pv}},
		{"second-root-absent", func(p *preventionScenario) { p.second.result.RootDigest = nil }, changed + "digest changed", []string{pv, pv}},
		{"second-root-differs", func(p *preventionScenario) { r := dg('2'); p.second.result.RootDigest = &r }, changed + "digest changed", []string{pv, pv}},
		{"second-body-differs", func(p *preventionScenario) {
			p.second.body = preventionBody(p.f, p.root, dg('5'), sptr(p.f.invDigest), true)
		}, changed + "digest changed", []string{pv, pv}},
	}
}

// ---- loadAttachments orchestration ----

func allStaged(t *testing.T) (*staged, AttachmentOptions, baseFacts) {
	f := loadFacts(t)
	s := &staged{t: t}
	minGood := minValue(goodWitness(f), endpoint(f.passToFail, "before"), endpoint(f.passToFail, "after"))
	s.minQ = []minimize.Verification{minVerification(minGood, dg('a'), "m1"), minVerification(minGood, dg('a'), "m2")}
	r := goodRepair(t)
	s.repQ = []repair.BriefVerification{r.first, r.second, r.third}
	s.readQ = []repairContents{r.one, r.two}
	p := goodPrevention(t, "p1", "p2")
	s.prevQ = []stagedPrevention{p.first, p.second}
	opts := AttachmentOptions{MinimizationFile: sptr("min.json"), RepairDirectory: sptr("repair"), PreventionDirectory: sptr("prev")}
	return s, opts, f
}

func TestAttachmentLoadAttachmentsRunsMinRepairPreventionSequentially(t *testing.T) {
	s, opts, f := allStaged(t)
	root := f.view.RootDigest
	var got VerifiedAttachments
	if msg, ok := attempt(func() {
		got = loadAttachments(opts, f.view.Investigation, f.view.FrozenWitness, &root, s.deps())
	}); !ok {
		t.Fatal(msg)
	}
	expectTrace(t, s.trace,
		"min.verify", "min.verify",
		"repair.verify", "repair.read", "repair.verify", "repair.read", "repair.verify",
		"prevention.verify", "prevention.verify")
	if got.Minimization == nil || got.Repair == nil || got.Prevention == nil {
		t.Fatalf("all three must attach: %+v", got)
	}
	if got.Minimization.ExternalDigestStatus != "m2" || got.Repair.ExternalDigestStatus != "s3" || got.Prevention.ExternalDigestStatus != "p2" {
		t.Fatalf("final statuses %q %q %q", got.Minimization.ExternalDigestStatus, got.Repair.ExternalDigestStatus, got.Prevention.ExternalDigestStatus)
	}
}

func TestAttachmentLoadAttachmentsZeroAndDigestOnlyTouchNothing(t *testing.T) {
	f := loadFacts(t)
	root := f.view.RootDigest
	d := dg('a')
	for name, opts := range map[string]AttachmentOptions{
		"zero":         {},
		"digests-only": {ExpectedMinimizationDigest: &d, ExpectedRepairDigest: &d, ExpectedPreventionDigest: &d},
	} {
		for rootName, proof := range map[string]*string{"with-root": &root, "without-root": nil} {
			t.Run(name+"/"+rootName, func(t *testing.T) {
				s := &staged{t: t}
				var got VerifiedAttachments
				if msg, ok := attempt(func() {
					got = loadAttachments(opts, f.view.Investigation, f.view.FrozenWitness, proof, s.deps())
				}); !ok {
					t.Fatal(msg)
				}
				if got != (VerifiedAttachments{}) || len(s.trace) != 0 {
					t.Fatalf("got %+v trace %v", got, s.trace)
				}
			})
		}
	}
}

func TestAttachmentPreventionRequiresBaseRoot(t *testing.T) {
	const want = "Prevention attachment requires the verified Git proof root digest."
	s, opts, f := allStaged(t)
	// Earlier attachments are fully processed first; only then is the root required.
	msg, ok := attempt(func() { loadAttachments(opts, f.view.Investigation, f.view.FrozenWitness, nil, s.deps()) })
	if ok || msg != want {
		t.Fatalf("ok=%v msg %q", ok, msg)
	}
	expectTrace(t, s.trace, "min.verify", "min.verify", "repair.verify", "repair.read", "repair.verify", "repair.read", "repair.verify")

	// An earlier failure wins over the missing root, and no prevention verifier runs.
	s2 := &staged{t: t, minQ: []minimize.Verification{{Valid: false, Errors: []string{"first"}}}}
	msg, ok = attempt(func() { loadAttachments(opts, f.view.Investigation, f.view.FrozenWitness, nil, s2.deps()) })
	if ok || msg != "Refusing to attach an invalid minimization record: first" {
		t.Fatalf("ok=%v msg %q", ok, msg)
	}
	expectTrace(t, s2.trace, "min.verify")

	// Prevention alone with no root fails without any callback.
	s3 := &staged{t: t}
	msg, ok = attempt(func() {
		loadAttachments(AttachmentOptions{PreventionDirectory: sptr("prev")}, f.view.Investigation, f.view.FrozenWitness, nil, s3.deps())
	})
	if ok || msg != want || len(s3.trace) != 0 {
		t.Fatalf("ok=%v msg %q trace %v", ok, msg, s3.trace)
	}
}

func TestAttachmentLoadAttachmentsFirstFailureStopsLaterAttachments(t *testing.T) {
	f := loadFacts(t)
	root := f.view.RootDigest
	t.Run("repair-failure-skips-prevention", func(t *testing.T) {
		s, opts, _ := allStaged(t)
		s.repQ = []repair.BriefVerification{{Valid: false, Errors: []string{"r"}}}
		s.prevQ = nil
		msg, ok := attempt(func() { loadAttachments(opts, f.view.Investigation, f.view.FrozenWitness, &root, s.deps()) })
		if ok || msg != "Refusing to attach an invalid repair brief: r" {
			t.Fatalf("ok=%v msg %q", ok, msg)
		}
		expectTrace(t, s.trace, "min.verify", "min.verify", "repair.verify")
	})
	t.Run("min-failure-skips-repair-and-prevention", func(t *testing.T) {
		s, opts, _ := allStaged(t)
		s.minQ = []minimize.Verification{{Valid: false, Errors: []string{"m"}}}
		s.repQ, s.readQ, s.prevQ = nil, nil, nil
		msg, ok := attempt(func() { loadAttachments(opts, f.view.Investigation, f.view.FrozenWitness, &root, s.deps()) })
		if ok || msg != "Refusing to attach an invalid minimization record: m" {
			t.Fatalf("ok=%v msg %q", ok, msg)
		}
		expectTrace(t, s.trace, "min.verify")
	})
}

// ---- real base precedence: one genuine public case ----

// An invalid base (wrong external root) together with an invalid attachment
// must report the base refusal: attachments are loaded only after the base passes.
func TestAttachmentBaseFailureTakesPrecedenceOverAttachmentFailure(t *testing.T) {
	corpus := serveBundleCorpus(t)
	baseCase := serveBundleCase(t, corpus, "view-root-wrong")
	badMin := serveBundleCase(t, corpus, "view-min-expected-wrong")
	bundleDir, _, _ := attachmentInputs(t, corpus, baseCase)
	_, _, minOptions := attachmentInputs(t, corpus, badMin)
	root := baseCase.ExpectedRoot
	_, err := LoadVerifiedGitProofViewWithAttachments(bundleDir, root, minOptions)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	golden := corpus.Goldens[baseCase.ID]
	if golden.Error == nil {
		t.Fatal("retained base refusal missing")
	}
	if got := jsexc.Message(err); got != *golden.Error {
		t.Fatalf("got %q, want the base refusal %q", got, *golden.Error)
	}
	if strings.Contains(jsexc.Message(err), "minimization") {
		t.Fatal("attachment error leaked ahead of the base error")
	}
}

// ---- real repair reader ----

func copiedRepairDir(t *testing.T, id string) string {
	corpus := serveBundleCorpus(t)
	_, _, options := attachmentInputs(t, corpus, serveBundleCase(t, corpus, id))
	if options.RepairDirectory == nil {
		t.Fatalf("%s has no repair directory", id)
	}
	return *options.RepairDirectory
}

// Calls the real reader (no staging). The valid retained package yields the
// validated brief and the schema-parsed packet; a forged citation set is
// refused with the wrapper plus the exact validator errors; a missing file is
// wrapped by the shared read label.
func TestAttachmentRepairReadValidation(t *testing.T) {
	t.Run("valid-package", func(t *testing.T) {
		dir := copiedRepairDir(t, "view-repair-expected-match")
		var got repairContents
		if msg, ok := attempt(func() { got = readRepairContents(dir) }); !ok {
			t.Fatal(msg)
		}
		if got.packet.Kind() != jsjson.Object || got.brief.Kind() != jsjson.Object {
			t.Fatalf("contents %+v", got)
		}
		packet := bundle.Must(bundle.ParseValue(readJSON(nodefs.Join(dir, "evidence-packet.json"), "x"), repair.EvidencePacketSchema))
		if jsjson.Stringify(got.packet) != jsjson.Stringify(packet) {
			t.Fatal("packet is not the schema-parsed retained packet")
		}
	})
	t.Run("forged-invalid-citations", func(t *testing.T) {
		dir := copiedRepairDir(t, "view-repair-citations-invalid")
		packet := bundle.Must(bundle.ParseValue(readJSON(nodefs.Join(dir, "evidence-packet.json"), "x"), repair.EvidencePacketSchema))
		brief := bundle.Must(bundle.ParseValue(readJSON(nodefs.Join(dir, "repair-brief.json"), "x"), repair.BriefSchema))
		validation := repair.ValidateBrief(packet, brief)
		if validation.Valid {
			t.Fatal("the retained citations-invalid package is accepted by the validator; a forged brief fixture is needed")
		}
		want := "Refusing to attach an invalid repair brief payload: " + strings.Join(validation.Errors, "; ")
		msg, ok := attempt(func() { readRepairContents(dir) })
		if ok || msg != want {
			t.Fatalf("ok=%v\n got %q\nwant %q", ok, msg, want)
		}
	})
	t.Run("missing-file-uses-read-label", func(t *testing.T) {
		dir := copiedRepairDir(t, "view-repair-expected-match")
		path := nodefs.Join(dir, "repair-brief.json")
		if msg, ok := attempt(func() { _ = readJSON(path+".absent", "repair brief") }); ok || !strings.HasPrefix(msg, "Unable to read verified repair brief: ") {
			t.Fatalf("ok=%v msg %q", ok, msg)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		msg, ok := attempt(func() { readRepairContents(dir) })
		if ok || !strings.HasPrefix(msg, "Unable to read verified repair brief: ") {
			t.Fatalf("ok=%v msg %q", ok, msg)
		}
	})
}
