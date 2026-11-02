package servefixture

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// The required ID set is a hand-written literal. It must never be derived from
// cases.jsonl, golden.jsonl or Load's own output.
var requiredFamilies = []struct {
	prefix   string
	suffixes string
}{
	{"view-root-", "absent match wrong"},
	{"view-min-", "uncertified expected-absent expected-match expected-wrong witness-null frozen-wrong witness-wrong before-null after-null before-commit before-tree after-commit after-tree wrong-transition"},
	{"view-repair-", "expected-absent expected-match expected-wrong investigation-wrong frozen-wrong legacy citations-invalid manifest-malformed packet-malformed brief-malformed manifest-content"},
	{"view-prevention-", "summary verified expected-absent expected-match expected-wrong root-wrong witness-wrong investigation-wrong investigation-absent runs-wrong runs-missing"},
	{"view-private-", "sentinels prevention-priority prevention-only-hero"},
	{"cli-options-", "bundle-missing bundle-empty bundle-prefixed bundle-duplicate bundle-equals duplicate-first equals-ignored min-path-only min-digest-only repair-path-only repair-digest-only prevention-path-only prevention-digest-only all-values-first pair-min-first pair-repair-first root-raw relative-paths"},
	{"cli-order-", "pair-before-io base-before-port attachment-before-port later-value-before-pair"},
	{"cli-port-", "default zero empty missing next-flag whitespace hex exponent fractional negative nan infinity out-of-range occupied"},
	{"cli-serve-", "000 001 010 011 100 101 110 111 no-root ignored-flags"},
}

const rejectSourceList = "view-min-expected-wrong view-min-frozen-wrong view-min-witness-wrong view-min-before-commit view-min-before-tree view-min-after-commit view-min-after-tree view-min-wrong-transition " +
	"view-repair-expected-wrong view-repair-investigation-wrong view-repair-frozen-wrong view-repair-citations-invalid view-repair-manifest-malformed view-repair-packet-malformed view-repair-brief-malformed view-repair-manifest-content " +
	"view-prevention-expected-wrong view-prevention-root-wrong view-prevention-witness-wrong view-prevention-investigation-wrong view-prevention-runs-wrong view-prevention-runs-missing"

func requiredIDs() []string {
	var ids []string
	for _, bits := range []string{"000", "001", "010", "011", "100", "101", "110", "111"} {
		ids = append(ids, "view-combination-"+bits)
	}
	for _, f := range requiredFamilies {
		for _, s := range strings.Fields(f.suffixes) {
			ids = append(ids, f.prefix+s)
		}
	}
	for _, s := range strings.Fields(rejectSourceList) {
		ids = append(ids, "cli-reject-"+s)
	}
	return ids
}

func TestRequiredTableSize(t *testing.T) {
	// Catches a typo in this test's own literal silently shrinking the contract.
	ids := requiredIDs()
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate in test table: %s", id)
		}
		seen[id] = true
	}
	if len(ids) != 118 {
		t.Fatalf("required id count = %d, want 118", len(ids))
	}
}

// ---- synthetic corpus builder (real files, independently hashed) ----

const validBaseline = `{"schema":"faultline.serve-bundle-oracle.v1","sourceCommit":"ce99a793af0b020bf1b9cf21114f8d7897b37207","manifestCommit":"ff5fe7b1120410e60c321f3ae620cf524c5694ec","manifestVersion":"0.1.4","nodeVersion":"22.22.2"}` + "\n"

var hexDigest = "sha256:" + strings.Repeat("a", 64)

type fileEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}
type digestSet struct {
	ProofRoot          string `json:"proofRoot"`
	Minimization       string `json:"minimization"`
	Repair             string `json:"repair"`
	PreventionSummary  string `json:"preventionSummary"`
	PreventionVerified string `json:"preventionVerified"`
}
type fixtureEntry struct {
	ID               string      `json:"id"`
	Root             string      `json:"root"`
	Files            []fileEntry `json:"files"`
	Digests          digestSet   `json:"digests"`
	PrivateSentinels []string    `json:"privateSentinels"`
}
type fixturesFile struct {
	Schema   string         `json:"schema"`
	Fixtures []fixtureEntry `json:"fixtures"`
}

type parts struct {
	baseline []byte
	fixtures fixturesFile
	// rawFixtures, if non-nil, replaces the marshalled fixtures.json.
	rawFixtures []byte
	cases       []Case
	goldens     []Golden
	// rawCases/rawGoldens, if non-nil, replace the marshalled JSONL.
	rawCases    []byte
	rawGoldens  []byte
	pages       map[string]string
	inputs      map[string]map[string]string // fixture id -> rel path -> bytes
	fixtureRoot map[string]string            // fixture id -> corpus-relative root
}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func strp(s string) *string { return &s }
func intp(i int) *int       { return &i }

func kindOf(id string) string {
	switch {
	case strings.HasPrefix(id, "view-"):
		return "view"
	case strings.HasPrefix(id, "cli-serve-"):
		return "cli-serve"
	}
	if strings.HasPrefix(id, "cli-port-") {
		switch strings.TrimPrefix(id, "cli-port-") {
		case "default", "zero", "empty", "missing", "whitespace", "hex", "exponent":
			return "cli-serve"
		}
	}
	return "cli-error"
}

func viewSucceeds(id string) bool {
	return strings.HasPrefix(id, "view-combination-") || id == "view-root-absent" || id == "view-root-match"
}

func newParts() *parts {
	p := &parts{
		baseline:    []byte(validBaseline),
		pages:       map[string]string{},
		inputs:      map[string]map[string]string{},
		fixtureRoot: map[string]string{"coherent": "inputs/coherent"},
	}
	p.inputs["coherent"] = map[string]string{
		"bundle/manifest.json":                       "{\"m\":1}\n",
		"bundle/investigation.json":                  "{\"i\":2}\r\n",
		"minimization.json":                          "{\"min\":true}\n",
		".faultline/prevention-proofs/verified/x.js": "\x00\xff binary\n",
	}
	for _, id := range requiredIDs() {
		c := Case{ID: id, Kind: kindOf(id), Fixture: "coherent", Argv: []string{}}
		if c.Kind != "view" {
			c.Argv = []string{"serve", "--bundle", "{ROOT}/bundle"}
		}
		if id == "view-combination-111" {
			c.ExpectedRoot = strp(hexDigest)
			c.Options = Options{
				MinimizationFile: strp("minimization.json"), ExpectedMinimizationDigest: strp(hexDigest),
				RepairDirectory: strp(".faultline/repair-briefs/attached"), ExpectedRepairDigest: strp(hexDigest),
				PreventionDirectory: strp(".faultline/prevention-proofs/verified"), ExpectedPreventionDigest: strp(hexDigest),
			}
		}
		p.cases = append(p.cases, c)
		g := Golden{ID: id}
		switch c.Kind {
		case "view":
			if viewSucceeds(id) {
				g.ValueJSON = strp(`{"v":"` + id + `"}`)
				g.Page = strp(id + ".html")
				p.pages[id+".html"] = "<html>" + id + "\r\n\xff</html>"
			} else {
				g.Error = strp("exact caught message for " + id)
			}
		case "cli-serve":
			g.Page = strp(id + ".html")
			p.pages[id+".html"] = "<html>served " + id + "</html>\n"
			g.Process = &ProcessResult{Stdout: "FaultLine read-only Git proof page: http://127.0.0.1:{PORT}\nPress Ctrl+C to stop.\n", ExitCode: intp(0)}
		default:
			g.Process = &ProcessResult{Stderr: "error\n", ExitCode: intp(1)}
		}
		p.goldens = append(p.goldens, g)
	}
	return p
}

func (p *parts) rebuildFixtures() {
	p.fixtures = fixturesFile{Schema: "faultline.serve-bundle-fixtures.v1"}
	ids := make([]string, 0, len(p.inputs))
	for id := range p.inputs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		entry := fixtureEntry{
			ID: id, Root: p.fixtureRoot[id], Files: []fileEntry{},
			Digests:          digestSet{hexDigest, hexDigest, hexDigest, hexDigest, hexDigest},
			PrivateSentinels: []string{"FL_PRIVATE_OVERLAY_5C_NOT_FOR_HTML"},
		}
		names := make([]string, 0)
		for name := range p.inputs[id] {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			content := p.inputs[id][name]
			entry.Files = append(entry.Files, fileEntry{Path: name, SHA256: sum(content), Bytes: len(content)})
		}
		p.fixtures.Fixtures = append(p.fixtures.Fixtures, entry)
	}
}

func jsonl[T any](t *testing.T, rows []T) []byte {
	t.Helper()
	var buf bytes.Buffer
	for _, r := range rows {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// build writes the corpus. mutate runs after the default parts are complete but
// before fixtures.json is derived from the (possibly edited) inputs, unless the
// test sets rawFixtures or edits p.fixtures through postFixtures.
func build(t *testing.T, mutate func(*parts)) string {
	return buildWith(t, mutate, nil)
}

func buildWith(t *testing.T, mutate func(*parts), postFixtures func(*parts)) string {
	t.Helper()
	p := newParts()
	if mutate != nil {
		mutate(p)
	}
	p.rebuildFixtures()
	if postFixtures != nil {
		postFixtures(p)
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "baseline.json"), string(p.baseline))
	writeFile(t, filepath.Join(root, ".gitattributes"), "* -text\n")
	fx := p.rawFixtures
	if fx == nil {
		b, err := json.Marshal(p.fixtures)
		if err != nil {
			t.Fatal(err)
		}
		fx = b
	}
	writeFile(t, filepath.Join(root, "fixtures.json"), string(fx))
	cs, gs := p.rawCases, p.rawGoldens
	if cs == nil {
		cs = jsonl(t, p.cases)
	}
	if gs == nil {
		gs = jsonl(t, p.goldens)
	}
	writeFile(t, filepath.Join(root, "cases.jsonl"), string(cs))
	writeFile(t, filepath.Join(root, "golden.jsonl"), string(gs))
	for name, content := range p.pages {
		writeFile(t, filepath.Join(root, "pages", name), content)
	}
	for id, files := range p.inputs {
		for name, content := range files {
			writeFile(t, filepath.Join(root, filepath.FromSlash(p.fixtureRoot[id]), filepath.FromSlash(name)), content)
		}
	}
	return root
}

func mustLoad(t *testing.T, root string) *Corpus {
	t.Helper()
	c, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return c
}

func mustReject(t *testing.T, root string, mention string) {
	t.Helper()
	c, err := Load(root)
	if err == nil {
		t.Fatalf("Load accepted an invalid corpus (%d cases)", len(c.Cases))
	}
	if mention != "" && !strings.Contains(err.Error(), mention) {
		t.Fatalf("error %q does not mention %q", err, mention)
	}
}

// ---- positive baseline ----

func TestLoadAcceptsCompleteCorpus(t *testing.T) {
	// Without this, every negative test below could pass by always failing.
	c := mustLoad(t, build(t, nil))
	if len(c.Cases) != 118 || len(c.Goldens) != 118 {
		t.Fatalf("cases=%d goldens=%d, want 118", len(c.Cases), len(c.Goldens))
	}
	for i, want := range requiredIDs() {
		_ = i
		g, ok := c.Goldens[want]
		if !ok || g.ID != want {
			t.Fatalf("golden for %s missing or mislabeled", want)
		}
	}
	// Order of cases.jsonl is preserved so consumers iterate stably.
	for i, id := range requiredIDs() {
		if c.Cases[i].ID != id {
			t.Fatalf("case %d = %s, want %s", i, c.Cases[i].ID, id)
		}
	}
}

func TestCommittedCorpusLoads(t *testing.T) {
	// The real retained corpus must satisfy the same contract. Before the
	// capture exists this fails with a missing-corpus error (infrastructure absence).
	c, err := Load(filepath.Join("..", "testdata", "serve-bundle"))
	if err != nil {
		t.Fatalf("committed corpus: %v", err)
	}
	if len(c.Cases) != 118 {
		t.Fatalf("committed corpus has %d cases, want 118", len(c.Cases))
	}
	// Checkout must not convert retained capture bytes.
	attributes, err := os.ReadFile(filepath.Join(c.Root, ".gitattributes"))
	if err != nil {
		t.Fatal(err)
	}
	if string(attributes) != "* -text\n" {
		t.Fatalf(".gitattributes = %q, want %q", attributes, "* -text\n")
	}
}

// ---- metadata ----

func TestBaselineMustBeExact(t *testing.T) {
	cases := map[string]string{
		"schema":                           strings.Replace(validBaseline, "oracle.v1", "oracle.v2", 1),
		"sourceCommit":                     strings.Replace(validBaseline, "ce99a793", "ce99a794", 1),
		"manifestCommit":                   strings.Replace(validBaseline, "ff5fe7b1", "ff5fe7b2", 1),
		"manifestVersion":                  strings.Replace(validBaseline, "0.1.4", "0.1.3", 1),
		"nodeVersion":                      strings.Replace(validBaseline, "22.22.2", "22.23.4", 1),
		"missing key":                      `{"schema":"faultline.serve-bundle-oracle.v1","sourceCommit":"ce99a793af0b020bf1b9cf21114f8d7897b37207","manifestCommit":"ff5fe7b1120410e60c321f3ae620cf524c5694ec","manifestVersion":"0.1.4"}`,
		"extra key":                        strings.Replace(validBaseline, "}\n", `,"extra":"x"}`+"\n", 1),
		"duplicate member":                 strings.Replace(validBaseline, `"nodeVersion":"22.22.2"`, `"nodeVersion":"22.22.2","nodeVersion":"22.22.2"`, 1),
		"duplicate member different value": strings.Replace(validBaseline, `"nodeVersion":"22.22.2"`, `"nodeVersion":"22.23.4","nodeVersion":"22.22.2"`, 1),
		"wrong type":                       strings.Replace(validBaseline, `"22.22.2"`, `22`, 1),
		"not an object":                    `["schema"]`,
		"empty":                            ``,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if body == validBaseline {
				t.Fatal("mutation did not change the baseline")
			}
			mustReject(t, build(t, func(p *parts) { p.baseline = []byte(body) }), "")
		})
	}
}

func TestBaselineAbsentIsRejected(t *testing.T) {
	root := build(t, nil)
	if err := os.Remove(filepath.Join(root, "baseline.json")); err != nil {
		t.Fatal(err)
	}
	mustReject(t, root, "")
}

func TestCorpusRootMissingIsRejected(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nothing")); err == nil {
		t.Fatal("Load accepted a missing corpus root")
	}
}

func TestFixturesJSONIsStrict(t *testing.T) {
	good := func(p *parts) string { p.rebuildFixtures(); b, _ := json.Marshal(p.fixtures); return string(b) }
	mutations := map[string]func(string) string{
		"wrong schema":      func(s string) string { return strings.Replace(s, "fixtures.v1", "fixtures.v2", 1) },
		"extra top key":     func(s string) string { return strings.Replace(s, "{\"schema\"", "{\"extra\":1,\"schema\"", 1) },
		"extra fixture key": func(s string) string { return strings.Replace(s, "\"digests\"", "\"surplus\":1,\"digests\"", 1) },
		"duplicate member": func(s string) string {
			return strings.Replace(s, "\"fixtures\":", "\"schema\":\"faultline.serve-bundle-fixtures.v1\",\"fixtures\":", 1)
		},
		"bad digest string": func(s string) string {
			return strings.Replace(s, "\"proofRoot\":\""+hexDigest+"\"", "\"proofRoot\":\"sha256:XYZ\"", 1)
		},
		"uppercase digest": func(s string) string {
			return strings.Replace(s, "\"repair\":\""+hexDigest+"\"", "\"repair\":\"sha256:"+strings.Repeat("A", 64)+"\"", 1)
		},
		"missing digest key": func(s string) string { return strings.Replace(s, ",\"preventionVerified\":\""+hexDigest+"\"", "", 1) },
		"truncated":          func(s string) string { return s[:len(s)/2] },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			var original string
			raw := build(t, func(p *parts) { original = good(p) })
			_ = raw
			changed := mutate(original)
			if changed == original {
				t.Fatal("mutation did not change fixtures.json")
			}
			mustReject(t, build(t, func(p *parts) { p.rawFixtures = []byte(changed) }), "")
		})
	}
}

func TestFixtureRootMustStayInsideCorpus(t *testing.T) {
	for _, bad := range []string{"../outside", "/abs/root", "inputs/../../x", "inputs\\coherent", ""} {
		t.Run(bad, func(t *testing.T) {
			root := buildWith(t, nil, func(p *parts) { p.fixtures.Fixtures[0].Root = bad })
			mustReject(t, root, "")
		})
	}
}

func TestFixtureInventoryPathsMustBeSafe(t *testing.T) {
	for _, bad := range []string{"../escape.json", "/abs.json", "a/../b.json", "back\\slash.json", "./dot.json", "", "dir//double.json"} {
		t.Run(bad, func(t *testing.T) {
			root := buildWith(t, nil, func(p *parts) {
				p.fixtures.Fixtures[0].Files = append(p.fixtures.Fixtures[0].Files, fileEntry{Path: bad, SHA256: sum(""), Bytes: 0})
			})
			mustReject(t, root, "")
		})
	}
}

// ---- case/golden alignment ----

func TestMissingRequiredCaseRejectedEvenWhenGoldenAlsoRemoved(t *testing.T) {
	// Catches a Load that only checks cases against goldens and not the pinned list.
	for _, id := range []string{"view-combination-101", "cli-port-nan", "cli-reject-view-prevention-runs-missing"} {
		t.Run(id, func(t *testing.T) {
			root := build(t, func(p *parts) {
				p.cases = removeCase(p.cases, id)
				p.goldens = removeGolden(p.goldens, id)
			})
			mustReject(t, root, id)
		})
	}
}

func TestDuplicateCaseRejected(t *testing.T) {
	id := "view-root-match"
	root := build(t, func(p *parts) {
		for _, c := range p.cases {
			if c.ID == id {
				p.cases = append(p.cases, c)
			}
		}
	})
	mustReject(t, root, id)
}

func TestDuplicateGoldenRejected(t *testing.T) {
	id := "cli-port-zero"
	root := build(t, func(p *parts) {
		for _, g := range p.goldens {
			if g.ID == id {
				p.goldens = append(p.goldens, g)
			}
		}
	})
	mustReject(t, root, id)
}

func TestUnknownCaseRejectedEvenWithMatchingGolden(t *testing.T) {
	// Adding a case and its golden together must not widen the corpus.
	root := build(t, func(p *parts) {
		p.cases = append(p.cases, Case{ID: "cli-port-extra", Kind: "cli-error", Fixture: "coherent", Argv: []string{"serve"}})
		p.goldens = append(p.goldens, Golden{ID: "cli-port-extra", Process: &ProcessResult{ExitCode: intp(1)}})
	})
	mustReject(t, root, "cli-port-extra")
}

func TestGoldenWithoutCaseRejected(t *testing.T) {
	root := build(t, func(p *parts) { p.goldens = append(p.goldens, Golden{ID: "orphan", Error: strp("x")}) })
	mustReject(t, root, "orphan")
}

func TestCaseWithoutGoldenRejected(t *testing.T) {
	id := "view-private-sentinels"
	mustReject(t, build(t, func(p *parts) { p.goldens = removeGolden(p.goldens, id) }), id)
}

func TestUnknownFixtureRejected(t *testing.T) {
	root := build(t, func(p *parts) { p.cases[3].Fixture = "no-such-fixture" })
	mustReject(t, root, "")
}

func TestUnknownKindRejected(t *testing.T) {
	root := build(t, func(p *parts) { p.cases[0].Kind = "cli-serves" })
	mustReject(t, root, "")
}

func TestUnknownJSONLFieldsAndMalformedLinesRejected(t *testing.T) {
	good := func(t *testing.T) (cases, goldens string) {
		p := newParts()
		return string(jsonl(t, p.cases)), string(jsonl(t, p.goldens))
	}
	t.Run("case extra field", func(t *testing.T) {
		cs, _ := good(t)
		cs = strings.Replace(cs, `{"id":`, `{"surprise":1,"id":`, 1)
		mustReject(t, build(t, func(p *parts) { p.rawCases = []byte(cs) }), "")
	})
	t.Run("golden extra field", func(t *testing.T) {
		_, gs := good(t)
		gs = strings.Replace(gs, `{"id":`, `{"surprise":1,"id":`, 1)
		mustReject(t, build(t, func(p *parts) { p.rawGoldens = []byte(gs) }), "")
	})
	t.Run("case duplicate member", func(t *testing.T) {
		cs, _ := good(t)
		cs = strings.Replace(cs, `{"id":"view-combination-000",`, `{"id":"view-combination-000","id":"view-combination-000",`, 1)
		mustReject(t, build(t, func(p *parts) { p.rawCases = []byte(cs) }), "")
	})
	t.Run("blank line in the middle", func(t *testing.T) {
		cs, _ := good(t)
		cs = strings.Replace(cs, "\n", "\n\n", 1)
		mustReject(t, build(t, func(p *parts) { p.rawCases = []byte(cs) }), "")
	})
	t.Run("truncated final line", func(t *testing.T) {
		_, gs := good(t)
		mustReject(t, build(t, func(p *parts) { p.rawGoldens = []byte(gs[:len(gs)-12]) }), "")
	})
}

// ---- golden shape ----

func TestGoldenShapeRules(t *testing.T) {
	cases := map[string]func(p *parts){
		"view success without page":      func(p *parts) { setGolden(p, "view-combination-001", func(g *Golden) { g.Page = nil }) },
		"view success without valueJSON": func(p *parts) { setGolden(p, "view-combination-001", func(g *Golden) { g.ValueJSON = nil }) },
		"view success also has error":    func(p *parts) { setGolden(p, "view-combination-001", func(g *Golden) { g.Error = strp("x") }) },
		"view success has process": func(p *parts) {
			setGolden(p, "view-combination-001", func(g *Golden) { g.Process = &ProcessResult{ExitCode: intp(0)} })
		},
		"view failure has page": func(p *parts) {
			setGolden(p, "view-min-witness-null", func(g *Golden) { g.Page = strp("view-combination-001.html") })
		},
		"view failure has valueJSON":   func(p *parts) { setGolden(p, "view-min-witness-null", func(g *Golden) { g.ValueJSON = strp("{}") }) },
		"view failure without message": func(p *parts) { setGolden(p, "view-min-witness-null", func(g *Golden) { g.Error = nil }) },
		"cli-error without process":    func(p *parts) { setGolden(p, "cli-port-nan", func(g *Golden) { g.Process = nil }) },
		"cli-serve without process":    func(p *parts) { setGolden(p, "cli-serve-000", func(g *Golden) { g.Process = nil }) },
		"cli-serve without page":       func(p *parts) { setGolden(p, "cli-serve-000", func(g *Golden) { g.Page = nil }) },
		"cli-error with valueJSON":     func(p *parts) { setGolden(p, "cli-port-nan", func(g *Golden) { g.ValueJSON = strp("{}") }) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) { mustReject(t, build(t, mutate), "") })
	}
}

// ---- pages ----

func TestRemovedPageRejectedEvenWhenCasesAndGoldensAreEdited(t *testing.T) {
	// A deleted full page must be caught by Load; removing the golden's page
	// reference as well must not make a page-bearing case acceptable.
	t.Run("file deleted", func(t *testing.T) {
		root := build(t, nil)
		if err := os.Remove(filepath.Join(root, "pages", "cli-serve-101.html")); err != nil {
			t.Fatal(err)
		}
		mustReject(t, root, "cli-serve-101")
	})
	t.Run("file deleted and golden reference nulled", func(t *testing.T) {
		root := build(t, func(p *parts) {
			setGolden(p, "cli-serve-101", func(g *Golden) { g.Page = nil })
			delete(p.pages, "cli-serve-101.html")
		})
		mustReject(t, root, "")
	})
	t.Run("view page deleted", func(t *testing.T) {
		root := build(t, nil)
		if err := os.Remove(filepath.Join(root, "pages", "view-combination-011.html")); err != nil {
			t.Fatal(err)
		}
		mustReject(t, root, "view-combination-011")
	})
}

func TestUnsafePagePathsRejected(t *testing.T) {
	for _, bad := range []string{"../escape.html", "/abs/page.html", "a\\b.html", "", ".", "..", "dir/../x.html", "nul\x00.html"} {
		t.Run(fmt.Sprintf("%q", bad), func(t *testing.T) {
			root := build(t, func(p *parts) {
				setGolden(p, "view-combination-000", func(g *Golden) { g.Page = strp(bad) })
			})
			mustReject(t, root, "")
		})
	}
}

func TestPageIsAFileNotADirectoryOrLink(t *testing.T) {
	root := build(t, nil)
	page := filepath.Join(root, "pages", "view-combination-100.html")
	if err := os.Remove(page); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(page, 0o700); err != nil {
		t.Fatal(err)
	}
	mustReject(t, root, "")
}

func TestPageReturnsExactBytes(t *testing.T) {
	// Catches newline/BOM/encoding conversion and truncation of retained page bytes.
	c := mustLoad(t, build(t, nil))
	got, err := c.Page("view-combination-000")
	if err != nil {
		t.Fatal(err)
	}
	want := "<html>view-combination-000\r\n\xff</html>"
	if string(got) != want {
		t.Fatalf("page = %q, want %q", got, want)
	}
	if _, err := c.Page("view-min-witness-null"); err == nil {
		t.Fatal("Page succeeded for a case without a page")
	}
	if _, err := c.Page("no-such-case"); err == nil {
		t.Fatal("Page succeeded for an unknown case")
	}
}

func TestPageRereadsRetainedBytesOnDemand(t *testing.T) {
	// If a page is tampered after Load, Page must not serve stale/unverified data silently:
	// it either returns the new bytes or an error, never the original from a cache
	// that disagrees with disk. This pins "no hidden rewriting" of retained bytes.
	root := build(t, nil)
	c := mustLoad(t, root)
	path := filepath.Join(root, "pages", "view-combination-000.html")
	if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := c.Page("view-combination-000")
	if err == nil && string(got) != "tampered" {
		t.Fatalf("Page returned %q that disagrees with the file on disk", got)
	}
}

// ---- fixture inventory integrity ----

func TestInventoryIntegrityNegatives(t *testing.T) {
	t.Run("changed bytes same length", func(t *testing.T) {
		root := build(t, nil)
		writeFile(t, filepath.Join(root, "inputs", "coherent", "minimization.json"), "{\"min\":false}\n")
		mustReject(t, root, "minimization.json")
	})
	t.Run("changed bytes different length", func(t *testing.T) {
		root := build(t, nil)
		writeFile(t, filepath.Join(root, "inputs", "coherent", "bundle", "manifest.json"), "{\"m\":1}\n\n")
		mustReject(t, root, "manifest.json")
	})
	t.Run("line ending conversion", func(t *testing.T) {
		root := build(t, nil)
		writeFile(t, filepath.Join(root, "inputs", "coherent", "bundle", "investigation.json"), "{\"i\":2}\n")
		mustReject(t, root, "investigation.json")
	})
	t.Run("missing file on disk", func(t *testing.T) {
		root := build(t, nil)
		if err := os.Remove(filepath.Join(root, "inputs", "coherent", "minimization.json")); err != nil {
			t.Fatal(err)
		}
		mustReject(t, root, "minimization.json")
	})
	t.Run("extra file on disk", func(t *testing.T) {
		root := build(t, nil)
		writeFile(t, filepath.Join(root, "inputs", "coherent", "bundle", "extra.json"), "{}")
		mustReject(t, root, "extra.json")
	})
	t.Run("extra dotfile on disk", func(t *testing.T) {
		root := build(t, nil)
		writeFile(t, filepath.Join(root, "inputs", "coherent", ".faultline", "hidden"), "x")
		mustReject(t, root, "hidden")
	})
	t.Run("inventory entry missing", func(t *testing.T) {
		root := buildWith(t, nil, func(p *parts) {
			f := p.fixtures.Fixtures[0].Files
			p.fixtures.Fixtures[0].Files = f[:len(f)-1]
		})
		mustReject(t, root, "")
	})
	t.Run("wrong sha256 in inventory", func(t *testing.T) {
		root := buildWith(t, nil, func(p *parts) { p.fixtures.Fixtures[0].Files[0].SHA256 = sum("something else") })
		mustReject(t, root, "")
	})
	t.Run("wrong byte length in inventory", func(t *testing.T) {
		root := buildWith(t, nil, func(p *parts) { p.fixtures.Fixtures[0].Files[0].Bytes++ })
		mustReject(t, root, "")
	})
	t.Run("malformed sha256 in inventory", func(t *testing.T) {
		root := buildWith(t, nil, func(p *parts) {
			p.fixtures.Fixtures[0].Files[0].SHA256 = strings.ToUpper(p.fixtures.Fixtures[0].Files[0].SHA256)
		})
		mustReject(t, root, "")
	})
	t.Run("duplicate inventory path", func(t *testing.T) {
		root := buildWith(t, nil, func(p *parts) {
			p.fixtures.Fixtures[0].Files = append(p.fixtures.Fixtures[0].Files, p.fixtures.Fixtures[0].Files[0])
		})
		mustReject(t, root, "")
	})
	t.Run("duplicate fixture id", func(t *testing.T) {
		root := buildWith(t, nil, func(p *parts) { p.fixtures.Fixtures = append(p.fixtures.Fixtures, p.fixtures.Fixtures[0]) })
		mustReject(t, root, "")
	})
	t.Run("inventory lists a directory entry", func(t *testing.T) {
		root := buildWith(t, nil, func(p *parts) {
			p.fixtures.Fixtures[0].Files = append(p.fixtures.Fixtures[0].Files, fileEntry{Path: "bundle", SHA256: sum(""), Bytes: 0})
		})
		mustReject(t, root, "")
	})
}

func TestEmptyFixtureFileInventoriedByHash(t *testing.T) {
	// A zero-byte file is real evidence (e.g. an empty ledger); it must be inventoried, not skipped.
	root := build(t, func(p *parts) { p.inputs["coherent"]["empty.json"] = "" })
	mustLoad(t, root)
	// The same corpus with that empty file deleted from disk must fail.
	if err := os.Remove(filepath.Join(root, "inputs", "coherent", "empty.json")); err != nil {
		t.Fatal(err)
	}
	mustReject(t, root, "empty.json")
}

func TestInventoryRejectsLinksInRetainedTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows; Windows retained trees contain no links by construction")
	}
	t.Run("symlink to a file", func(t *testing.T) {
		root := build(t, nil)
		if err := os.Symlink("minimization.json", filepath.Join(root, "inputs", "coherent", "link.json")); err != nil {
			t.Fatal(err)
		}
		mustReject(t, root, "link.json")
	})
	t.Run("symlink replacing an inventoried file", func(t *testing.T) {
		root := build(t, nil)
		target := filepath.Join(root, "inputs", "coherent", "minimization.json")
		if err := os.Remove(target); err != nil {
			t.Fatal(err)
		}
		other := filepath.Join(root, "inputs", "coherent", "bundle", "manifest.json")
		if err := os.Symlink(other, target); err != nil {
			t.Fatal(err)
		}
		mustReject(t, root, "")
	})
	t.Run("symlinked directory", func(t *testing.T) {
		root := build(t, nil)
		if err := os.Symlink(filepath.Join(root, "inputs", "coherent", "bundle"), filepath.Join(root, "inputs", "coherent", "bundle-link")); err != nil {
			t.Fatal(err)
		}
		mustReject(t, root, "")
	})
	t.Run("symlinked page", func(t *testing.T) {
		root := build(t, nil)
		page := filepath.Join(root, "pages", "view-combination-000.html")
		if err := os.Remove(page); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(root, "pages", "view-combination-001.html"), page); err != nil {
			t.Fatal(err)
		}
		mustReject(t, root, "")
	})
}

func TestPermissionBitsAreNotInventoried(t *testing.T) {
	// Git does not retain 0600 and Windows differs; a checkout with relaxed modes must load.
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits are meaningless on Windows")
	}
	root := build(t, nil)
	for _, rel := range []string{"inputs/coherent/minimization.json", "pages/view-combination-000.html"} {
		if err := os.Chmod(filepath.Join(root, filepath.FromSlash(rel)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustLoad(t, root)
}

// ---- CopyFixture ----

func TestCopyFixtureCopiesExactBytesToANewDestination(t *testing.T) {
	root := build(t, nil)
	c := mustLoad(t, root)
	dest := filepath.Join(t.TempDir(), "owned")
	if err := c.CopyFixture("coherent", dest); err != nil {
		t.Fatal(err)
	}
	p := newParts()
	for name, want := range p.inputs["coherent"] {
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(got) != want {
			t.Fatalf("%s = %q, want %q (CRLF/binary bytes must survive)", name, got, want)
		}
	}
	// Exactly the inventoried files, nothing added (no repaired digests, no generated artifacts).
	var found []string
	_ = filepath.WalkDir(dest, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dest, path)
			found = append(found, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(found)
	var want []string
	for name := range p.inputs["coherent"] {
		want = append(want, name)
	}
	sort.Strings(want)
	if strings.Join(found, "|") != strings.Join(want, "|") {
		t.Fatalf("copied files = %v, want %v", found, want)
	}
}

func TestCopyFixtureIsIndependentOfTheRetainedCorpus(t *testing.T) {
	// Catches a copy that hard-links or symlinks, letting a test mutate retained evidence.
	root := build(t, nil)
	c := mustLoad(t, root)
	dest := filepath.Join(t.TempDir(), "owned")
	if err := c.CopyFixture("coherent", dest); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dest, "minimization.json"), "mutated by test")
	got, err := os.ReadFile(filepath.Join(root, "inputs", "coherent", "minimization.json"))
	if err != nil || string(got) != "{\"min\":true}\n" {
		t.Fatalf("retained corpus changed: %q, %v", got, err)
	}
	info, err := os.Lstat(filepath.Join(dest, "minimization.json"))
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("copied file is not a regular file: %v %v", info, err)
	}
}

func TestCopyFixtureRefusesExistingDestination(t *testing.T) {
	c := mustLoad(t, build(t, nil))
	dest := t.TempDir() // exists
	marker := filepath.Join(dest, "marker")
	writeFile(t, marker, "keep")
	if err := c.CopyFixture("coherent", dest); err == nil {
		t.Fatal("CopyFixture merged into an existing destination")
	}
	if b, err := os.ReadFile(marker); err != nil || string(b) != "keep" {
		t.Fatalf("existing destination modified: %q %v", b, err)
	}
	if entries, _ := os.ReadDir(dest); len(entries) != 1 {
		t.Fatalf("existing destination gained entries: %v", entries)
	}
}

func TestCopyFixtureRejectsUnknownAndTraversingIDs(t *testing.T) {
	c := mustLoad(t, build(t, nil))
	for _, id := range []string{"missing", "", "../coherent", "coherent/..", "/coherent", "inputs/coherent", "..", "coherent\\x"} {
		t.Run(fmt.Sprintf("%q", id), func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "d")
			if err := c.CopyFixture(id, dest); err == nil {
				t.Fatal("CopyFixture accepted the id")
			}
			if _, err := os.Lstat(dest); err == nil {
				t.Fatal("destination created for a rejected id")
			}
		})
	}
}

func TestCopyFixtureDoesNotRevalidateSilently(t *testing.T) {
	// If the retained bytes were tampered after Load, CopyFixture must not
	// "repair" them to the inventory: it copies what is there or errors.
	root := build(t, nil)
	c := mustLoad(t, root)
	writeFile(t, filepath.Join(root, "inputs", "coherent", "minimization.json"), "tampered")
	dest := filepath.Join(t.TempDir(), "owned")
	if err := c.CopyFixture("coherent", dest); err != nil {
		return // refusing is acceptable
	}
	got, _ := os.ReadFile(filepath.Join(dest, "minimization.json"))
	if string(got) == "{\"min\":true}\n" {
		t.Fatal("CopyFixture silently restored inventory bytes")
	}
}

// ---- FixturePath ----

func TestFixturePath(t *testing.T) {
	root := build(t, nil)
	c := mustLoad(t, root)
	got, err := c.FixturePath("coherent")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "inputs", "coherent"); filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("FixturePath = %q, want %q", got, want)
	}
	for _, id := range []string{"", "missing", "..", "../coherent", "coherent/../coherent", "/etc", "inputs/coherent"} {
		if p, err := c.FixturePath(id); err == nil {
			t.Fatalf("FixturePath(%q) = %q, want error", id, p)
		}
	}
}

// ---- ResolveOptions ----

func TestResolveOptionsResolvesOnlyPathsAndKeepsDigestsLiteral(t *testing.T) {
	root := build(t, nil)
	c := mustLoad(t, root)
	base, _ := c.FixturePath("coherent")
	in := c.Cases[caseIndex(t, c, "view-combination-111")].Options
	got, err := c.ResolveOptions("view-combination-111", in)
	if err != nil {
		t.Fatal(err)
	}
	for name, pair := range map[string][2]*string{
		"minimization": {got.MinimizationFile, in.MinimizationFile},
		"repair":       {got.RepairDirectory, in.RepairDirectory},
		"prevention":   {got.PreventionDirectory, in.PreventionDirectory},
	} {
		if pair[0] == nil {
			t.Fatalf("%s path dropped", name)
		}
		if want := filepath.Join(base, filepath.FromSlash(*pair[1])); filepath.Clean(*pair[0]) != filepath.Clean(want) {
			t.Fatalf("%s path = %q, want %q", name, *pair[0], want)
		}
	}
	for name, d := range map[string]*string{"min": got.ExpectedMinimizationDigest, "repair": got.ExpectedRepairDigest, "prevention": got.ExpectedPreventionDigest} {
		if d == nil || *d != hexDigest {
			t.Fatalf("%s digest altered: %v", name, d)
		}
	}
	// The input must not be mutated in place.
	if *in.MinimizationFile != "minimization.json" {
		t.Fatalf("ResolveOptions mutated its argument: %q", *in.MinimizationFile)
	}
}

func TestResolveOptionsKeepsAbsentFieldsAbsent(t *testing.T) {
	c := mustLoad(t, build(t, nil))
	got, err := c.ResolveOptions("view-combination-000", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got != (Options{}) {
		t.Fatalf("empty options gained fields: %+v", got)
	}
	// Digest-only options (one-sided at the library boundary) stay unresolved paths.
	got, err = c.ResolveOptions("view-combination-000", Options{ExpectedRepairDigest: strp("sha256:literal-not-a-path")})
	if err != nil || got.RepairDirectory != nil || got.ExpectedRepairDigest == nil || *got.ExpectedRepairDigest != "sha256:literal-not-a-path" {
		t.Fatalf("digest-only options changed: %+v %v", got, err)
	}
}

func TestResolveOptionsRejectsEscapingPathsAndUnknownIDs(t *testing.T) {
	c := mustLoad(t, build(t, nil))
	for _, bad := range []string{"../escape", "/abs/path", "a/../../b", "..", "C:\\x", "x\\y", ""} {
		t.Run(bad, func(t *testing.T) {
			if o, err := c.ResolveOptions("view-combination-111", Options{MinimizationFile: strp(bad)}); err == nil {
				t.Fatalf("accepted %q -> %+v", bad, o)
			}
			if o, err := c.ResolveOptions("view-combination-111", Options{RepairDirectory: strp(bad)}); err == nil {
				t.Fatalf("accepted repair %q -> %+v", bad, o)
			}
			if o, err := c.ResolveOptions("view-combination-111", Options{PreventionDirectory: strp(bad)}); err == nil {
				t.Fatalf("accepted prevention %q -> %+v", bad, o)
			}
		})
	}
	if _, err := c.ResolveOptions("no-such-case", Options{}); err == nil {
		t.Fatal("unknown case accepted")
	}
}

// ---- ResolveArgv ----

func TestResolveArgvExpandsOnlyWholeDeclaredTokens(t *testing.T) {
	c := mustLoad(t, build(t, nil))
	root := filepath.Join(t.TempDir(), "case root")
	argv := []string{
		"serve", "--bundle", "{ROOT}/bundle", "--minimization", "{ROOT}/min/result.json",
		"--port", "{PORT}", "--expect-root", "{ROOT}", "--bundle=x{ROOT}/y", "pre{PORT}", "{PORT}suffix",
		"{root}/lower", "{ROOT}x/notprefix", "--literal={ROOT}/kept", "sha256:{PORT}",
	}
	orig := append([]string(nil), argv...)
	got, err := c.ResolveArgv("view-combination-000", root, 43127, argv)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(argv, "\x00") != strings.Join(orig, "\x00") {
		t.Fatal("ResolveArgv mutated its input slice")
	}
	want := []string{
		"serve", "--bundle", filepath.Join(root, "bundle"), "--minimization", filepath.Join(root, "min", "result.json"),
		"--port", "43127", "--expect-root", root, "--bundle=x{ROOT}/y", "pre{PORT}", "{PORT}suffix",
		"{root}/lower", "{ROOT}x/notprefix", "--literal={ROOT}/kept", "sha256:{PORT}",
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResolveArgvRefusesTraversalAndBadPorts(t *testing.T) {
	c := mustLoad(t, build(t, nil))
	root := t.TempDir()
	if out, err := c.ResolveArgv("view-combination-000", root, 1, []string{"{ROOT}/../outside"}); err == nil {
		t.Fatalf("{ROOT}/.. escape accepted: %q", out)
	}
	for _, port := range []int{-1, 65536, 1 << 20} {
		if out, err := c.ResolveArgv("view-combination-000", root, port, []string{"--port", "{PORT}"}); err == nil {
			t.Fatalf("port %d accepted: %q", port, out)
		}
	}
	if _, err := c.ResolveArgv("no-such-case", root, 1, []string{"x"}); err == nil {
		t.Fatal("unknown case accepted")
	}
	if got, err := c.ResolveArgv("cli-port-zero", root, 0, []string{"--port", "{PORT}"}); err != nil || got[1] != "0" {
		t.Fatalf("port 0 = %q, %v", got, err)
	}
	if got, err := c.ResolveArgv("cli-port-zero", root, 65535, nil); err != nil || len(got) != 0 {
		t.Fatalf("nil argv = %q, %v", got, err)
	}
}

// ---- review round 1: exact (case-sensitive) metadata keys ----

// rawFixturesJSON returns the marshalled fixtures.json of a default corpus, so a
// test can edit its exact bytes.
func rawFixturesJSON(t *testing.T) string {
	t.Helper()
	p := newParts()
	p.rebuildFixtures()
	b, err := json.Marshal(p.fixtures)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func replaceOnce(t *testing.T, s, old, replacement string) string {
	t.Helper()
	if !strings.Contains(s, old) {
		t.Fatalf("test setup: %q not found", old)
	}
	return strings.Replace(s, old, replacement, 1)
}

// encoding/json folds key case when filling structs, so a wrong-case key (or a
// case-variant duplicate whose later, folded member overrides an earlier wrong
// value) can look valid unless Load checks exact spellings itself.
func TestMetadataKeysMustHaveExactCase(t *testing.T) {
	cases := controlledCases(t)
	for name, c := range cases {
		t.Run(name, func(t *testing.T) { mustReject(t, c(t), "") })
	}
}

func controlledCases(t *testing.T) map[string]func(*testing.T) string {
	t.Helper()
	cs := string(jsonl(t, newParts().cases))
	gs := string(jsonl(t, newParts().goldens))
	fx := rawFixturesJSON(t)
	withBaseline := func(body string) func(*testing.T) string {
		return func(t *testing.T) string { return build(t, func(p *parts) { p.baseline = []byte(body) }) }
	}
	withFixtures := func(body string) func(*testing.T) string {
		return func(t *testing.T) string { return build(t, func(p *parts) { p.rawFixtures = []byte(body) }) }
	}
	withCases := func(body string) func(*testing.T) string {
		return func(t *testing.T) string { return build(t, func(p *parts) { p.rawCases = []byte(body) }) }
	}
	withGoldens := func(body string) func(*testing.T) string {
		return func(t *testing.T) string { return build(t, func(p *parts) { p.rawGoldens = []byte(body) }) }
	}
	return map[string]func(*testing.T) string{
		"baseline wrong-case key": withBaseline(replaceOnce(t, validBaseline, `"schema":`, `"Schema":`)),
		"baseline all-caps key":   withBaseline(replaceOnce(t, validBaseline, `"nodeVersion":`, `"NODEVERSION":`)),
		"baseline case-variant duplicate, wrong first value then folded correct override": withBaseline(
			replaceOnce(t, validBaseline, `"nodeVersion":"22.22.2"`, `"nodeVersion":"22.23.4","NODEVERSION":"22.22.2"`)),
		"fixtures top-level wrong-case key":     withFixtures(replaceOnce(t, fx, `"schema":`, `"Schema":`)),
		"fixtures wrong-case files key":         withFixtures(replaceOnce(t, fx, `"files":`, `"FILES":`)),
		"fixtures wrong-case nested file key":   withFixtures(replaceOnce(t, fx, `"sha256":`, `"SHA256":`)),
		"fixtures wrong-case nested digest key": withFixtures(replaceOnce(t, fx, `"proofRoot":`, `"ProofRoot":`)),
		"fixtures wrong-case sentinel key":      withFixtures(replaceOnce(t, fx, `"privateSentinels":`, `"PrivateSentinels":`)),
		"fixtures nested case-variant duplicate, wrong first digest then folded correct override": withFixtures(
			replaceOnce(t, fx, `"proofRoot":"`+hexDigest+`"`, `"PROOFROOT":"sha256:`+strings.Repeat("b", 64)+`","proofRoot":"`+hexDigest+`"`)),
		"fixtures file-entry case-variant duplicate, wrong first length": withFixtures(
			replaceOnce(t, fx, `"bytes":`, `"BYTES":999,"bytes":`)),
		"cases wrong-case id":          withCases(replaceOnce(t, cs, `{"id":"view-combination-000"`, `{"ID":"view-combination-000"`)),
		"cases wrong-case options key": withCases(replaceOnce(t, cs, `"minimizationFile":`, `"MinimizationFile":`)),
		"cases case-variant duplicate id, wrong first value then folded correct override": withCases(
			replaceOnce(t, cs, `{"id":"view-combination-000",`, `{"id":"view-root-wrong","ID":"view-combination-000",`)),
		"cases options case-variant duplicate": withCases(
			replaceOnce(t, cs, `"options":{`, `"options":{"MINIMIZATIONFILE":"wrong.json",`)),
		"goldens wrong-case id":          withGoldens(replaceOnce(t, gs, `{"id":"view-combination-000"`, `{"ID":"view-combination-000"`)),
		"goldens wrong-case process key": withGoldens(replaceOnce(t, gs, `"exitCode":1`, `"ExitCode":1`)),
		"goldens case-variant duplicate id, wrong first value then folded correct override": withGoldens(
			replaceOnce(t, gs, `{"id":"view-combination-000",`, `{"id":"view-root-wrong","ID":"view-combination-000",`)),
		"goldens process case-variant duplicate exit code": withGoldens(
			replaceOnce(t, gs, `"exitCode":1`, `"exitCode":0,"EXITCODE":1`)),
	}
}

// ---- review round 1: pinned port kinds ----

func TestPortCaseKindsArePinnedPerID(t *testing.T) {
	// A kind edit made together with a matching golden shape must still be refused:
	// valid port spellings are cli-serve; malformed/occupied ones are cli-error.
	for _, suffix := range strings.Fields(requiredFamilies[7].suffixes) {
		id := "cli-port-" + suffix
		t.Run(id, func(t *testing.T) {
			flipped := "cli-serve"
			if kindOf(id) == "cli-serve" {
				flipped = "cli-error"
			}
			root := build(t, func(p *parts) {
				for i := range p.cases {
					if p.cases[i].ID == id {
						p.cases[i].Kind = flipped
					}
				}
				setGolden(p, id, func(g *Golden) {
					if flipped == "cli-serve" {
						g.Page = strp(id + ".html")
						p.pages[id+".html"] = "<html>forged serve page</html>\n"
						g.Process = &ProcessResult{Stdout: "FaultLine read-only Git proof page: http://127.0.0.1:{PORT}\nPress Ctrl+C to stop.\n", ExitCode: intp(0)}
					} else {
						delete(p.pages, *g.Page)
						g.Page = nil
						g.Process = &ProcessResult{Stderr: "error\n", ExitCode: intp(1)}
					}
				})
			})
			mustReject(t, root, id)
		})
	}
}

// ---- review round 1: completed process status ----

func TestCLIGoldenProcessStatusIsComplete(t *testing.T) {
	sig := "SIGKILL"
	tests := []struct {
		name  string
		id    string
		apply func(*Golden)
	}{
		{"cli-error with null exit code", "cli-port-nan", func(g *Golden) { g.Process.ExitCode = nil }},
		{"cli-error with a signal and no exit code", "cli-port-nan", func(g *Golden) { g.Process.ExitCode = nil; g.Process.Signal = &sig }},
		{"cli-error with a signal beside an exit code", "cli-port-nan", func(g *Golden) { g.Process.Signal = &sig }},
		{"cli-error that exited zero", "cli-port-nan", func(g *Golden) { g.Process.ExitCode = intp(0) }},
		{"cli-serve with null exit code", "cli-serve-000", func(g *Golden) { g.Process.ExitCode = nil }},
		{"cli-serve with nonzero exit", "cli-serve-000", func(g *Golden) { g.Process.ExitCode = intp(1) }},
		{"cli-serve ended by SIGINT signal", "cli-serve-000", func(g *Golden) { g.Process.ExitCode = nil; sigint := "SIGINT"; g.Process.Signal = &sigint }},
		{"cli-serve with exit zero and a signal", "cli-serve-000", func(g *Golden) { sigint := "SIGINT"; g.Process.Signal = &sigint }},
		{"valid-port cli-serve with nonzero exit", "cli-port-zero", func(g *Golden) { g.Process.ExitCode = intp(2) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := build(t, func(p *parts) { setGolden(p, tc.id, tc.apply) })
			mustReject(t, root, tc.id)
		})
	}
}

// ---- review round 1: exact .gitattributes in Load ----

func TestGitattributesMustBeExactInLoad(t *testing.T) {
	for name, body := range map[string]string{
		"text auto":      "* text=auto\n",
		"no newline":     "* -text",
		"crlf":           "* -text\r\n",
		"extra line":     "* -text\n*.json text\n",
		"empty":          "",
		"different glob": "*.json -text\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := build(t, nil)
			writeFile(t, filepath.Join(root, ".gitattributes"), body)
			mustReject(t, root, "gitattributes")
		})
	}
	t.Run("absent", func(t *testing.T) {
		root := build(t, nil)
		if err := os.Remove(filepath.Join(root, ".gitattributes")); err != nil {
			t.Fatal(err)
		}
		mustReject(t, root, "")
	})
}

// ---- review round 1: private sentinel inventory against the retained corpus ----

func collectStrings(value any, out *[]string) {
	switch v := value.(type) {
	case string:
		*out = append(*out, v)
	case []any:
		for _, item := range v {
			collectStrings(item, out)
		}
	case map[string]any:
		for _, item := range v {
			collectStrings(item, out)
		}
	}
}

// retainedContains reports whether a sentinel appears in a retained input file,
// either verbatim or inside a base64 string value (witness overlay bytes).
func retainedContains(data []byte, sentinel string) bool {
	if bytes.Contains(data, []byte(sentinel)) {
		return true
	}
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return false
	}
	var strs []string
	collectStrings(parsed, &strs)
	for _, s := range strs {
		decoded, err := base64.StdEncoding.DecodeString(s)
		if err == nil && bytes.Contains(decoded, []byte(sentinel)) {
			return true
		}
	}
	return false
}

func TestCommittedPrivateSentinelsAreRetainedAndNeverRendered(t *testing.T) {
	root := filepath.Join("..", "testdata", "serve-bundle")
	c, err := Load(root)
	if err != nil {
		t.Fatalf("committed corpus: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "fixtures.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta fixturesFile
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	if len(meta.Fixtures) != 1 || len(meta.Fixtures[0].PrivateSentinels) != 14 {
		t.Fatalf("expected one fixture with exactly 14 private sentinels, got %+v", meta.Fixtures)
	}
	fixture := meta.Fixtures[0]
	inputs := map[string][]byte{}
	for _, file := range fixture.Files {
		data, err := os.ReadFile(filepath.Join(c.Root, filepath.FromSlash(fixture.Root), filepath.FromSlash(file.Path)))
		if err != nil {
			t.Fatal(err)
		}
		inputs[file.Path] = data
	}
	pages := map[string][]byte{}
	for _, g := range c.Goldens {
		if g.Page != nil {
			page, err := c.Page(g.ID)
			if err != nil {
				t.Fatal(err)
			}
			pages[g.ID] = page
		}
	}
	if len(pages) == 0 {
		t.Fatal("no retained pages to check")
	}
	seen := map[string]bool{}
	for _, sentinel := range fixture.PrivateSentinels {
		if sentinel == "" || seen[sentinel] {
			t.Fatalf("empty or repeated sentinel %q", sentinel)
		}
		seen[sentinel] = true
		t.Run(sentinel, func(t *testing.T) {
			found := false
			for _, data := range inputs {
				if retainedContains(data, sentinel) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("sentinel %q is not present in any retained input (inventory would be self-fulfilling)", sentinel)
			}
			for id, page := range pages {
				if bytes.Contains(page, []byte(sentinel)) {
					t.Errorf("sentinel %q appears in the full page of %s", sentinel, id)
				}
			}
		})
	}
}

// ---- helpers ----

func removeCase(rows []Case, id string) []Case {
	var out []Case
	for _, r := range rows {
		if r.ID != id {
			out = append(out, r)
		}
	}
	return out
}

func removeGolden(rows []Golden, id string) []Golden {
	var out []Golden
	for _, r := range rows {
		if r.ID != id {
			out = append(out, r)
		}
	}
	return out
}

func setGolden(p *parts, id string, edit func(*Golden)) {
	for i := range p.goldens {
		if p.goldens[i].ID == id {
			edit(&p.goldens[i])
			return
		}
	}
	panic("no golden " + id)
}

func caseIndex(t *testing.T, c *Corpus, id string) int {
	t.Helper()
	for i, row := range c.Cases {
		if row.ID == id {
			return i
		}
	}
	t.Fatalf("case %s not loaded", id)
	return -1
}
