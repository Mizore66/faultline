package guided

// Permanent, Node-free replay of the frozen guided orchestration PROJECTION corpus
// (difftest/testdata/guided-investigate/projection). The corpus was produced by running the unchanged
// frozen compiled orchestration with scripted dependency stubs (difftest/gen/gen-guided-projection.mjs).
// This replay drives the private run(o, runDeps) with the same scripts through a generic adapter and
// compares the complete serialized capture (result or error, phase sequence, dependency transcript)
// byte-for-byte with the Node-produced file. It is a scripted-dependency projection: not end-to-end and
// not native Docker proof evidence. This test only reads fixtures; it never writes them.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/incident"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/sandbox"
)

const (
	projSchema       = "faultline.guided-projection.v1"
	projFrozenCommit = "ce99a793af0b020bf1b9cf21114f8d7897b37207"
	projSourceSHA    = "8117310db75752f5e7faf160adf0be53c9832070b8c6a38a83b0572fd5a1783c"
	projNode         = "22.22.2"
	// projCompiledSHA pins the legitimate frozen build of dist/guided-investigate.js.
	projCompiledSHA = "b50a8060c8d85b3539c05ed8dfa5d44c4d3ce7d01d6af8ff6418c647b2fbfb7a"
)

var projDir = filepath.Join("..", "..", "difftest", "testdata", "guided-investigate", "projection")

func projHash(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// projCorpus is the in-memory corpus so tamper controls can mutate copies without touching disk.
type projCorpus struct {
	cases     []byte
	manifest  []byte
	captures  map[string][]byte // relative name -> bytes
	generator []byte
	loader    []byte
}

func loadProjCorpus(t testing.TB) projCorpus {
	t.Helper()
	read := func(p string) []byte {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read retained projection evidence: %v", err)
		}
		return b
	}
	c := projCorpus{
		cases:     read(filepath.Join(projDir, "cases.json")),
		manifest:  read(filepath.Join(projDir, "manifest.json")),
		generator: read(filepath.Join("..", "..", "difftest", "gen", "gen-guided-projection.mjs")),
		loader:    read(filepath.Join("..", "..", "difftest", "gen", "guided-projection-loader.mjs")),
		captures:  map[string][]byte{},
	}
	entries, err := os.ReadDir(filepath.Join(projDir, "captures"))
	if err != nil {
		t.Fatalf("read captures dir: %v", err)
	}
	for _, e := range entries {
		c.captures["captures/"+e.Name()] = read(filepath.Join(projDir, "captures", e.Name()))
	}
	return c
}

func projParse(b []byte) (jsjson.Value, error) { return jsjson.Parse(string(b)) }

// verifyProjCorpus checks the manifest, pins and every hash and returns the case list.
func verifyProjCorpus(c projCorpus) ([]jsjson.Value, error) {
	m, err := projParse(c.manifest)
	if err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	cs, err := projParse(c.cases)
	if err != nil {
		return nil, fmt.Errorf("cases: %w", err)
	}
	if m.Get("schema").Str() != projSchema || cs.Get("schema").Str() != projSchema {
		return nil, errors.New("schema mismatch")
	}
	if m.Get("frozenCommit").Str() != projFrozenCommit {
		return nil, errors.New("manifest frozen commit pin mismatch")
	}
	if m.Get("frozenSource", "sha256").Str() != projSourceSHA {
		return nil, errors.New("manifest frozen source pin mismatch")
	}
	if m.Get("node").Str() != projNode {
		return nil, errors.New("manifest Node version pin mismatch")
	}
	if m.Get("compiledModule", "sha256").Str() != projCompiledSHA {
		return nil, errors.New("manifest compiled module hash differs from the pinned legitimate frozen build")
	}
	if m.Get("realDependencies", "dist/doctor.js").Str() != projRealDoctorSHA {
		return nil, errors.New("manifest real doctor dependency pin mismatch")
	}
	if m.Get("realDependencies", "dist/incident-store.js").Str() != projRealStoreSHA {
		return nil, errors.New("manifest real incident-store dependency pin mismatch")
	}
	if m.Get("generator", "sha256").Str() != projHash(c.generator) {
		return nil, errors.New("generator hash differs from retained manifest")
	}
	if m.Get("loader", "sha256").Str() != projHash(c.loader) {
		return nil, errors.New("loader hash differs from retained manifest")
	}
	if m.Get("casesFileSha256").Str() != projHash(c.cases) {
		return nil, errors.New("cases.json hash differs from manifest")
	}
	cases := cs.Get("cases").Items()
	entries := m.Get("cases").Items()
	if len(cases) != len(entries) {
		return nil, fmt.Errorf("case count %d differs from manifest %d", len(cases), len(entries))
	}
	seen := map[string]bool{}
	for i, cv := range cases {
		id := cv.Get("id").Str()
		e := entries[i]
		if e.Get("id").Str() != id || seen[id] {
			return nil, fmt.Errorf("case %d id %q differs from manifest or duplicated", i, id)
		}
		seen[id] = true
		if e.Get("inputSha256").Str() != projHash([]byte(jsjson.Stringify(cv))) {
			return nil, fmt.Errorf("case %s input hash differs from manifest", id)
		}
		name := e.Get("output").Str()
		if name != "captures/"+id+".json" {
			return nil, fmt.Errorf("case %s output path %q unexpected", id, name)
		}
		body, ok := c.captures[name]
		if !ok {
			return nil, fmt.Errorf("case %s capture missing", id)
		}
		if e.Get("outputSha256").Str() != projHash(body) {
			return nil, fmt.Errorf("case %s capture hash differs from manifest", id)
		}
	}
	if len(c.captures) != len(cases) {
		return nil, fmt.Errorf("%d capture files for %d cases", len(c.captures), len(cases))
	}
	return cases, nil
}

// ---------------------------------------------------------------- generic tagged encoding

func projTag(kind string, kv ...any) jsjson.Value {
	return jsjson.MustObject(append([]any{"$t", jsjson.MakeString(kind)}, kv...)...)
}

func projUndefined() jsjson.Value { return projTag("undefined") }

func projStr(s string) jsjson.Value { return jsjson.MakeString(projNormalizeHost(s)) }

const (
	projRealDoctorSHA = "b3c03fe38ebba86a74fab01579402d5b3573ccf8da0e42ae61b5f66fecf4e6c0"
	projRealStoreSHA  = "777062b0aea9f1cccc92556f4ae14f50e1be61e91c6549fcd129c5af4d0003a3"
)

// Declared owned path roles (placeholders in fixtures/captures). Only the exact whole-string role prefix is
// expanded, and only exact owned-root tokens are normalized back; never blanket string or separator edits.
var projRoleRoots = [][2]string{{"$ROOT", "/tmp/fl-proj-owned"}, {"$FOREIGN", "/tmp/fl-proj-foreign"}}

func projHostRoots() map[string]string {
	m := map[string]string{}
	for _, r := range projRoleRoots {
		m[r[0]] = nodefs.Resolve(r[1])
	}
	return m
}

func projExpandWith(s string, roots map[string]string, sep string) string {
	for _, r := range projRoleRoots {
		if s == r[0] || strings.HasPrefix(s, r[0]+"/") {
			return roots[r[0]] + strings.ReplaceAll(s[len(r[0]):], "/", sep)
		}
	}
	return s
}

func projIsWS(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' || b == '\v'
}

// projNormalizeWith mirrors the generator's normalizePaths: an owned root is rewritten to its role only when
// it starts a whitespace-delimited token and is followed by whitespace, end, or the native separator.
func projNormalizeWith(s string, roots map[string]string, sep string) string {
	var out strings.Builder
	i := 0
outer:
	for i < len(s) {
		if i == 0 || projIsWS(s[i-1]) {
			for _, r := range projRoleRoots {
				root := roots[r[0]]
				if strings.HasPrefix(s[i:], root) {
					end := i + len(root)
					if end == len(s) || projIsWS(s[end]) || strings.HasPrefix(s[end:], sep) {
						j := end
						for j < len(s) && !projIsWS(s[j]) {
							j++
						}
						out.WriteString(r[0] + strings.ReplaceAll(s[end:j], sep, "/"))
						i = j
						continue outer
					}
				}
			}
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

func projNormalizeHost(s string) string {
	return projNormalizeWith(s, projHostRoots(), string(os.PathSeparator))
}

// projDeepMapStrings rebuilds a JSON value with f applied to every string (member order kept).
func projDeepMapStrings(v jsjson.Value, f func(string) string) jsjson.Value {
	switch v.Kind() {
	case jsjson.String:
		return jsjson.MakeString(f(v.Str()))
	case jsjson.Array:
		items := make([]jsjson.Value, 0, len(v.Items()))
		for _, it := range v.Items() {
			items = append(items, projDeepMapStrings(it, f))
		}
		return jsjson.MakeArray(items)
	case jsjson.Object:
		o := jsjson.NewObj()
		for _, k := range v.Obj().Keys() {
			o.Set(k, projDeepMapStrings(v.Obj().Field(k), f))
		}
		return jsjson.MakeObject(o)
	}
	return v
}

func projStrPtr(p *string) jsjson.Value {
	if p == nil {
		return projUndefined()
	}
	return projStr(*p)
}

func projObject(members ...any) jsjson.Value {
	var list []jsjson.Value
	for i := 0; i < len(members); i += 2 {
		list = append(list, jsjson.MakeArray([]jsjson.Value{projStr(members[i].(string)), members[i+1].(jsjson.Value)}))
	}
	return projTag("object", "members", jsjson.MakeArray(list))
}

// projEncode is the generic JSON-value to tagged-value encoding (mirrors the Node encoder).
func projEncode(v jsjson.Value) jsjson.Value {
	switch v.Kind() {
	case jsjson.Undefined:
		return projUndefined()
	case jsjson.String:
		return projStr(v.Str())
	case jsjson.Array:
		items := make([]jsjson.Value, 0, len(v.Items()))
		for _, it := range v.Items() {
			items = append(items, projEncode(it))
		}
		return projTag("array", "items", jsjson.MakeArray(items))
	case jsjson.Object:
		var members []any
		for _, k := range v.Obj().Keys() {
			members = append(members, k, projEncode(v.Obj().Field(k)))
		}
		return projObject(members...)
	}
	return v
}

// ---------------------------------------------------------------- identity probes

type projSigKey struct{}

// projIdentity records which fixture-declared marker a forwarded function or context carries.
type projIdentity struct{ last string }

func (p *projIdentity) fn(id string) func(string) {
	return func(string) { p.last = id }
}

func (p *projIdentity) runner(id string) sandbox.Runner {
	return func(sandbox.Plan) (sandbox.RunResult, error) { p.last = id; return sandbox.RunResult{}, nil }
}

func (p *projIdentity) probeFn(f func(string)) (jsjson.Value, error) {
	if f == nil {
		return jsjson.Value{}, errors.New("nil function forwarded")
	}
	p.last = ""
	f("probe")
	return projTag("fn", "id", projStr(p.last)), nil
}

func (p *projIdentity) probeRunner(r sandbox.Runner) jsjson.Value {
	p.last = ""
	_, _ = r(sandbox.Plan{})
	return projTag("fn", "id", projStr(p.last))
}

func projProbeSignal(ctx context.Context) jsjson.Value {
	id, _ := ctx.Value(projSigKey{}).(string)
	return projTag("signal", "id", projStr(id), "aborted", jsjson.MakeBool(ctx.Err() != nil))
}

// ---------------------------------------------------------------- replay

type projScript map[string][]jsjson.Value

func projCapture(raw jsjson.Value) (string, error) {
	hostRoots, sep := projHostRoots(), string(os.PathSeparator)
	c := projDeepMapStrings(raw, func(s string) string { return projExpandWith(s, hostRoots, sep) })
	id := c.Get("id").Str()
	var events []jsjson.Value
	ident := &projIdentity{}
	script := projScript{}
	if s := c.Get("script"); s.Kind() == jsjson.Object {
		for _, k := range s.Obj().Keys() {
			switch k {
			case "doctor", "readDraft", "verify", "waitForFreeze", "resolveRuntime", "continue":
			default:
				return "", fmt.Errorf("%s: unknown script kind %s", id, k)
			}
			script[k] = append([]jsjson.Value{}, s.Obj().Field(k).Items()...)
		}
	}
	record := func(kind string, args ...jsjson.Value) {
		events = append(events, jsjson.MustObject("kind", projStr(kind), "args", jsjson.MakeArray(args)))
	}
	var misuse []string
	next := func(kind string) (jsjson.Value, error) {
		q := script[kind]
		if len(q) == 0 {
			misuse = append(misuse, "unscripted "+kind+" call")
			return jsjson.Value{}, errors.New("unscripted " + kind + " call")
		}
		step := q[0]
		script[kind] = q[1:]
		if th := step.Get("throw"); th.Kind() == jsjson.String {
			return jsjson.Value{}, errors.New(th.Str())
		}
		return step.Get("return"), nil
	}

	// Options: generic fixture-key to Go-field adapter.
	var o Options
	var d runDeps
	waiter := false
	for _, k := range c.Get("options").Obj().Keys() {
		v := c.Get("options").Obj().Field(k)
		str := func() *string { s := v.Str(); return &s }
		num := func() *float64 { n := v.Num(); return &n }
		marker := func(key string) (string, bool) {
			m := v.Get(key)
			return m.Str(), v.Kind() == jsjson.Object && m.Kind() == jsjson.String
		}
		switch k {
		case "repository":
			o.Repository = v.Str()
		case "ciLogPath":
			o.CILogPath = str()
		case "resumeId":
			o.ResumeID = str()
		case "command":
			o.Command = str()
		case "from":
			o.From = str()
		case "to":
			o.To = str()
		case "incidentId":
			o.IncidentID = str()
		case "runtime":
			o.Runtime = str()
		case "image":
			o.Image = str()
		case "expectDigest":
			o.ExpectDigest = str()
		case "ledgerFile":
			o.LedgerFile = str()
		case "outputDirectory":
			o.Output = str()
		case "witnessStore":
			o.WitnessStore = str()
		case "draftStore":
			o.DraftStore = str()
		case "maxStates":
			o.MaxStates = num()
		case "reviewPort":
			o.ReviewPort = num()
		case "runner":
			mid, ok := marker("$fn")
			if !ok {
				return "", fmt.Errorf("%s: runner needs a $fn marker", id)
			}
			o.Runner = ident.runner(mid)
		case "onReviewReady":
			mid, ok := marker("$fn")
			if !ok {
				return "", fmt.Errorf("%s: onReviewReady needs a $fn marker", id)
			}
			o.OnReviewReady = ident.fn(mid)
		case "waitForFreeze":
			if _, ok := marker("$fn"); !ok {
				return "", fmt.Errorf("%s: waitForFreeze needs a $fn marker", id)
			}
			waiter = true
		case "signal":
			sid, ok := marker("$signal")
			if !ok {
				return "", fmt.Errorf("%s: signal needs a $signal marker", id)
			}
			ctx, cancel := context.WithCancel(context.WithValue(context.Background(), projSigKey{}, sid))
			if v.Get("aborted").Bool() {
				cancel()
			}
			defer cancel()
			o.Signal = ctx
		default:
			return "", fmt.Errorf("%s: unknown option %s", id, k)
		}
	}
	o.OnPhase = func(name string, detail *string) {
		args := []jsjson.Value{projStr(name)}
		if detail != nil {
			args = append(args, projStr(*detail))
		}
		record("phase", args...)
	}

	d.doctor = func(repository string) jsjson.Value {
		record("doctor", projObject("repository", projStr(repository)))
		v, err := next("doctor")
		if err != nil {
			panic(err)
		}
		return v
	}
	d.intake = func(IntakeOptions) (IntakeResult, error) {
		misuse = append(misuse, "intake called; fresh intake is outside the projection boundary")
		return IntakeResult{}, errors.New("intake not scripted")
	}
	d.readDraft = func(store, id string) (jsjson.Value, string, error) {
		record("readDraft", projStr(store), projStr(id))
		v, err := next("readDraft")
		if err != nil {
			return jsjson.Value{}, "", err
		}
		return v.Get("draft"), v.Get("path").Str(), nil
	}
	d.verify = func(store, id, expected string, provided bool) gitproof.WitnessVerification {
		exp := projUndefined()
		if provided {
			exp = projStr(expected)
		}
		record("verify", projStr(store), projStr(id), exp)
		v, err := next("verify")
		if err != nil {
			panic(err)
		}
		w := gitproof.WitnessVerification{Valid: v.Get("valid").Bool(), ExternalDigestStatus: v.Get("externalDigestStatus").Str()}
		for _, e := range v.Get("errors").Items() {
			w.Errors = append(w.Errors, e.Str())
		}
		if fd := v.Get("frozenDigest"); fd.Kind() == jsjson.String {
			s := fd.Str()
			w.FrozenDigest = &s
		}
		return w
	}
	d.waitForFreeze = func(f FreezeOptions) (FreezeResult, error) {
		if !waiter {
			misuse = append(misuse, "waiter called without waitForFreeze option")
		}
		members := []any{"proposalId", projStr(f.ProposalID), "witnessStore", projStr(f.WitnessStore), "draftStore", projStr(f.DraftStore)}
		if f.Port != nil {
			members = append(members, "port", jsjson.MakeNumber(*f.Port))
		}
		if f.OnReviewReady != nil {
			fv, err := ident.probeFn(f.OnReviewReady)
			if err != nil {
				return FreezeResult{}, err
			}
			members = append(members, "onReviewReady", fv)
		}
		if f.Signal != nil {
			members = append(members, "signal", projProbeSignal(f.Signal))
		}
		record("waitForFreeze", projObject(members...))
		v, err := next("waitForFreeze")
		if err != nil {
			return FreezeResult{}, err
		}
		r := FreezeResult{FrozenDigest: v.Get("frozenDigest").Str(), AlreadyFrozen: v.Get("alreadyFrozen").Bool()}
		if u := v.Get("reviewUrl"); u.Kind() == jsjson.String {
			s := u.Str()
			r.ReviewURL = &s
		}
		return r, nil
	}
	d.resolveRuntime = func(alias string) (jsjson.Value, error) {
		record("resolveRuntime", projStr(alias))
		return next("resolveRuntime")
	}
	d.cont = func(co incident.ContinueOptions) (jsjson.Value, error) {
		if co.Injected != (co.Runner != nil) {
			misuse = append(misuse, "Injected flag differs from runner presence")
		}
		img := projUndefined()
		if co.Image != nil {
			img = projStr(*co.Image)
		}
		members := []any{"draft", projEncode(co.Draft), "repository", projStr(co.Repository), "witnessStore", projStr(co.Store),
			"expectedFrozenDigest", projStr(co.ExpectedFrozenDigest), "image", img}
		if co.Runner != nil {
			members = append(members, "runner", ident.probeRunner(co.Runner))
		}
		if co.LedgerFile != nil {
			members = append(members, "ledgerFile", projStr(*co.LedgerFile))
		}
		if co.MaxStates != nil {
			members = append(members, "maxStates", jsjson.MakeNumber(*co.MaxStates))
		}
		if co.Output != nil {
			members = append(members, "outputDirectory", projStr(*co.Output))
		}
		record("continue", projObject(members...))
		return next("continue")
	}

	var result jsjson.Value
	var runErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				if e, ok := r.(error); ok {
					runErr = e
					return
				}
				panic(r)
			}
		}()
		result, runErr = run(o, d)
	}()
	for k, q := range script {
		if len(q) != 0 {
			misuse = append(misuse, fmt.Sprintf("%d unused %s script step(s)", len(q), k))
		}
	}
	if len(misuse) != 0 {
		sort.Strings(misuse)
		return "", fmt.Errorf("%s: adapter misuse: %s", id, strings.Join(misuse, "; "))
	}
	var outcome jsjson.Value
	if runErr != nil {
		outcome = jsjson.MustObject("kind", projStr("error"), "message", projStr(runErr.Error()))
	} else {
		outcome = jsjson.MustObject("kind", projStr("result"), "json", projStr(jsjson.StringifyIndent(projDeepMapStrings(result, projNormalizeHost), "  ")))
	}
	cap := jsjson.MustObject("id", projStr(id), "outcome", outcome, "events", jsjson.MakeArray(events))
	return jsjson.StringifyIndent(cap, "  ") + "\n", nil
}

// replayProjCorpus verifies the corpus and replays every case, returning every failure.
func replayProjCorpus(c projCorpus) []string {
	cases, err := verifyProjCorpus(c)
	if err != nil {
		return []string{err.Error()}
	}
	var failures []string
	for _, cv := range cases {
		id := cv.Get("id").Str()
		got, err := projCapture(cv)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		if got != string(c.captures["captures/"+id+".json"]) {
			failures = append(failures, id+": Go projection differs from Node-derived capture")
		}
	}
	return failures
}

func TestGuidedProjectionGolden(t *testing.T) {
	c := loadProjCorpus(t)
	cases, err := verifyProjCorpus(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) < 20 {
		t.Fatalf("only %d projection cases", len(cases))
	}
	for _, cv := range cases {
		id := cv.Get("id").Str()
		t.Run(id, func(t *testing.T) {
			got, err := projCapture(cv)
			if err != nil {
				t.Fatal(err)
			}
			want := string(c.captures["captures/"+id+".json"])
			if got != want {
				t.Fatalf("projection differs from Node-derived capture\n--- got\n%s\n--- want\n%s", got, want)
			}
		})
	}
}

func projClone(c projCorpus) projCorpus {
	n := c
	n.captures = map[string][]byte{}
	for k, v := range c.captures {
		n.captures[k] = append([]byte{}, v...)
	}
	n.manifest = append([]byte{}, c.manifest...)
	n.cases = append([]byte{}, c.cases...)
	return n
}

// TestGuidedProjectionHarnessControls proves the comparison is not vacuous: each tampered corpus must be
// rejected, and a perturbed replay must differ from the untouched capture.
func TestGuidedProjectionHarnessControls(t *testing.T) {
	base := loadProjCorpus(t)
	if f := replayProjCorpus(base); len(f) != 0 {
		t.Fatalf("pristine corpus must replay clean: %v", f)
	}
	mutate := func(name string, f func(*projCorpus)) {
		t.Run(name, func(t *testing.T) {
			c := projClone(base)
			f(&c)
			if len(replayProjCorpus(c)) == 0 {
				t.Fatalf("tampered corpus %q was accepted", name)
			}
		})
	}
	mutate("expected-result-value", func(c *projCorpus) {
		k := "captures/runtime-alias-resolved.json"
		c.captures[k] = []byte(strings.Replace(string(c.captures[k]), "GUIDED_INVESTIGATION_NOT_PROOF", "GUIDED_PROOF_BUNDLE_READY", 1))
	})
	mutate("expected-transcript-argument", func(c *projCorpus) {
		k := "captures/waiter-fresh-freeze-proof-forwarding.json"
		c.captures[k] = []byte(strings.Replace(string(c.captures[k]), "\"port\"", "\"port2\"", 1))
	})
	mutate("source-pin", func(c *projCorpus) {
		c.manifest = []byte(strings.Replace(string(c.manifest), projSourceSHA, strings.Repeat("0", 64), 1))
	})
	mutate("compiled-pin-random-64hex", func(c *projCorpus) {
		c.manifest = []byte(strings.Replace(string(c.manifest), projCompiledSHA, strings.Repeat("1", 64), 1))
	})
	mutate("case-omission", func(c *projCorpus) {
		delete(c.captures, "captures/node-blocked.json")
	})
	mutate("case-input-edit", func(c *projCorpus) {
		c.cases = []byte(strings.Replace(string(c.cases), "\"echo", "\"ech0", 1))
	})
	mutate("generator-hash", func(c *projCorpus) { c.generator = append([]byte{}, append(c.generator, '\n')...) })

	// Replay perturbation: a differing script value must change the serialized capture.
	cases, err := verifyProjCorpus(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, cv := range cases {
		if cv.Get("id").Str() != "runtime-alias-resolved" {
			continue
		}
		perturbed, err := jsjson.Parse(strings.Replace(jsjson.Stringify(cv), "\"python\",\"image\"", "\"python\",\"imagf\"", 1))
		if err != nil {
			t.Fatal(err)
		}
		// Image key renamed: the alias resolves to an undefined image, so the replay must diverge or fail.
		got, cerr := projCapture(perturbed)
		if cerr == nil && got == string(base.captures["captures/runtime-alias-resolved.json"]) {
			t.Fatal("perturbed script replayed identically to the Node capture")
		}
		return
	}
	t.Fatal("control case runtime-alias-resolved missing")
}

// TestGuidedProjectionManifestTamperControls rejects each remaining manifest pin edit.
func TestGuidedProjectionManifestTamperControls(t *testing.T) {
	base := loadProjCorpus(t)
	edits := map[string][2]string{
		"frozen-commit":     {projFrozenCommit, strings.Repeat("2", 40)},
		"node-version":      {`"node": "` + projNode + `"`, `"node": "22.23.4"`},
		"real-doctor-pin":   {projRealDoctorSHA, strings.Repeat("3", 64)},
		"real-store-pin":    {projRealStoreSHA, strings.Repeat("4", 64)},
		"first-output-hash": {`"outputSha256": "`, `"outputSha256": "0`},
		"first-input-hash":  {`"inputSha256": "`, `"inputSha256": "0`},
	}
	for name, e := range edits {
		t.Run(name, func(t *testing.T) {
			c := projClone(base)
			if !strings.Contains(string(c.manifest), e[0]) {
				t.Fatalf("control edit anchor %q absent from manifest", e[0])
			}
			c.manifest = []byte(strings.Replace(string(c.manifest), e[0], e[1], 1))
			if len(replayProjCorpus(c)) == 0 {
				t.Fatalf("tampered manifest %q was accepted", name)
			}
		})
	}
	t.Run("loader-bytes", func(t *testing.T) {
		c := projClone(base)
		c.loader = append(append([]byte{}, c.loader...), '\n')
		if len(replayProjCorpus(c)) == 0 {
			t.Fatal("tampered loader accepted")
		}
	})
}

// TestGuidedProjectionPathRoleNormalization checks the owned-root rules with host-independent POSIX and
// Windows-native examples. Only exact owned roots followed by a separator, whitespace or end are rewritten.
func TestGuidedProjectionPathRoleNormalization(t *testing.T) {
	posix := map[string]string{"$ROOT": "/tmp/fl-proj-owned", "$FOREIGN": "/tmp/fl-proj-foreign"}
	win := map[string]string{"$ROOT": `C:\tmp\fl-proj-owned`, "$FOREIGN": `C:\tmp\fl-proj-foreign`}
	cases := []struct {
		name, in, want string
		roots          map[string]string
		sep            string
	}{
		{"posix-subpath", "/tmp/fl-proj-owned/repo/.faultline", "$ROOT/repo/.faultline", posix, "/"},
		{"posix-in-sentence", "bad /tmp/fl-proj-owned/ws here", "bad $ROOT/ws here", posix, "/"},
		{"posix-lookalike-prefix", "/tmp/fl-proj-owned2/repo", "/tmp/fl-proj-owned2/repo", posix, "/"},
		{"posix-foreign", "/tmp/fl-proj-foreign/other", "$FOREIGN/other", posix, "/"},
		{"posix-unrelated-slashes", `a/b/c and x\y`, `a/b/c and x\y`, posix, "/"},
		{"posix-embedded-not-token-start", "xx/tmp/fl-proj-owned/repo", "xx/tmp/fl-proj-owned/repo", posix, "/"},
		{"windows-subpath", `C:\tmp\fl-proj-owned\repo\.faultline`, "$ROOT/repo/.faultline", win, `\`},
		{"windows-lookalike", `C:\tmp\fl-proj-owned2\repo`, `C:\tmp\fl-proj-owned2\repo`, win, `\`},
		{"windows-foreign-drive", `D:\tmp\fl-proj-owned\repo`, `D:\tmp\fl-proj-owned\repo`, win, `\`},
		{"windows-unrelated-backslashes", `a\b\c`, `a\b\c`, win, `\`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := projNormalizeWith(c.in, c.roots, c.sep); got != c.want {
				t.Fatalf("normalize(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
	if got := projExpandWith("$ROOT/repo/x", win, `\`); got != `C:\tmp\fl-proj-owned\repo\x` {
		t.Fatalf("windows expand = %q", got)
	}
	if got := projExpandWith("$ROOTX/repo", win, `\`); got != "$ROOTX/repo" {
		t.Fatalf("role lookalike expanded: %q", got)
	}
	if got := projExpandWith("/tmp/fl-proj-owned/x", posix, "/"); got != "/tmp/fl-proj-owned/x" {
		t.Fatalf("non-role string expanded: %q", got)
	}
}
