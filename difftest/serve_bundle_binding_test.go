//go:build faultline_test

package difftest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/difftest/servefixture"
	"github.com/Mizore66/faultline/internal/minimize"
)

// The supplemental serve-bundle binding corpus (difftest/testdata/serve-bundle-
// binding) holds five minimization records that the frozen TS verifier accepts
// but the serve --bundle adapter refuses at its Git-proof binding checks. The
// original corpus is untouched. The records and their goldens come from the
// actual frozen TS CLI (difftest/gen/gen-serve-bundle-binding.ts); nothing here
// authors an expectation.

const (
	serveBundleBindingDir   = "testdata/serve-bundle-binding"
	serveBundleBindingInput = "binding-minimization.json"
)

// The five ids and their inputs, restated independently of the retained metadata.
var serveBundleBindingNames = []string{"foreign-frozen", "foreign-witness", "witness-null", "nullable-states", "different-range"}

type bindingCase struct {
	ID                         string   `json:"id"`
	Input                      string   `json:"input"`
	ExpectedRoot               string   `json:"expectedRoot"`
	ExpectedMinimizationDigest string   `json:"expectedMinimizationDigest"`
	Argv                       []string `json:"argv"`
}

type bindingGolden struct {
	ID      string `json:"id"`
	Process struct {
		Stdout   string  `json:"stdout"`
		Stderr   string  `json:"stderr"`
		ExitCode *int    `json:"exitCode"`
		Signal   *string `json:"signal"`
	} `json:"process"`
}

type bindingInventory struct {
	Schema string `json:"schema"`
	Source struct {
		Corpus             string `json:"corpus"`
		Fixture            string `json:"fixture"`
		ProofRoot          string `json:"proofRoot"`
		MinimizationDigest string `json:"minimizationDigest"`
		MinimizationFile   string `json:"minimizationFile"`
		MinimizationSha256 string `json:"minimizationSha256"`
	} `json:"source"`
	Files []struct {
		Path   string `json:"path"`
		Sha256 string `json:"sha256"`
		Bytes  int    `json:"bytes"`
	} `json:"files"`
}

// bindingInput is what the loader verified about one retained record, so the
// copier can refuse a record that changed after loading.
type bindingInput struct {
	sha256 string
	bytes  int
}

type serveBundleBinding struct {
	dir     string
	cases   []bindingCase
	goldens []bindingGolden
	inputs  map[string]bindingInput // by inventory path
}

// bindingShape names the exact members of a JSON object; nil is any value and
// the "[]" key describes the elements of an array.
type bindingShape map[string]bindingShape

var (
	bindingCaseShape      = bindingShape{"id": nil, "input": nil, "expectedRoot": nil, "expectedMinimizationDigest": nil, "argv": nil}
	bindingGoldenShape    = bindingShape{"id": nil, "process": {"stdout": nil, "stderr": nil, "exitCode": nil, "signal": nil}}
	bindingInventoryShape = bindingShape{
		"schema": nil,
		"source": {"corpus": nil, "fixture": nil, "proofRoot": nil, "minimizationDigest": nil, "minimizationFile": nil, "minimizationSha256": nil},
		"files":  {"[]": {"path": nil, "sha256": nil, "bytes": nil}},
	}
)

// bindingWalk rejects a duplicate member at any depth.
func bindingWalk(dec *json.Decoder) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return err
			}
			key, _ := keyToken.(string)
			if seen[key] {
				return fmt.Errorf("duplicate JSON member %q", key)
			}
			seen[key] = true
			if err := bindingWalk(dec); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := bindingWalk(dec); err != nil {
				return err
			}
		}
	}
	_, err = dec.Token()
	return err
}

func bindingCheckShape(value any, shape bindingShape, label string) error {
	if shape == nil {
		return nil
	}
	switch v := value.(type) {
	case map[string]any:
		if _, array := shape["[]"]; array || len(v) != len(shape) {
			return fmt.Errorf("%s must have exactly the members of its schema", label)
		}
		for key, child := range v {
			childShape, ok := shape[key]
			if !ok {
				return fmt.Errorf("%s has the unexpected member %q", label, key)
			}
			if err := bindingCheckShape(child, childShape, label+"."+key); err != nil {
				return err
			}
		}
	case []any:
		element, ok := shape["[]"]
		if !ok {
			return fmt.Errorf("%s must not be an array", label)
		}
		for i, child := range v {
			if err := bindingCheckShape(child, element, fmt.Sprintf("%s[%d]", label, i)); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("%s must be an object or array", label)
	}
	return nil
}

// strictBindingDecode decodes one JSON value into v after requiring no
// duplicate member, exactly the members of shape (none absent, none unknown),
// and no trailing data, so a missing member cannot silently default.
func strictBindingDecode(data []byte, v any, shape bindingShape) error {
	scan := json.NewDecoder(bytes.NewReader(data))
	if err := bindingWalk(scan); err != nil {
		return err
	}
	if _, err := scan.Token(); err != io.EOF {
		return fmt.Errorf("unexpected data after the JSON value")
	}
	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		return err
	}
	if err := bindingCheckShape(generic, shape, "$"); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("unexpected data after the JSON value")
	}
	return nil
}

// bindingRequireStrings requires the named members of a golden's process object
// to be JSON strings: encoding/json would otherwise decode null into "" and a
// retained stdout:null would pass as an empty stdout. The empty string stays valid.
func bindingRequireStrings(line string, names ...string) error {
	var row struct {
		Process map[string]json.RawMessage `json:"process"`
	}
	if err := json.Unmarshal([]byte(line), &row); err != nil {
		return err
	}
	for _, name := range names {
		raw := bytes.TrimSpace(row.Process[name])
		if len(raw) == 0 || raw[0] != '"' {
			return fmt.Errorf("process.%s must be a JSON string, not %s", name, raw)
		}
	}
	return nil
}

func bindingLines(path string) ([]string, error) {
	data, err := bindingRegular(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return nil, fmt.Errorf("%s must be non-empty and end with a newline", path)
	}
	lines := strings.Split(string(data[:len(data)-1]), "\n")
	if slices.Contains(lines, "") {
		return nil, fmt.Errorf("%s has a blank line", path)
	}
	return lines, nil
}

func bindingRegular(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	return os.ReadFile(path)
}

// loadServeBundleBinding is loadServeBundleBindingAt over the committed corpus;
// any rejection fails the test.
func loadServeBundleBinding(t *testing.T, original *servefixture.Corpus) serveBundleBinding {
	t.Helper()
	b, err := loadServeBundleBindingAt(serveBundleBindingDir, original)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// loadServeBundleBindingAt strictly loads the supplemental corpus at dir: the
// exact file set, the pinned provenance, per-file bytes and hashes, the exact
// five ids in order, the original base root, the fixed argv, a Go-verifier-valid
// record per case whose digest is the retained expected one, and completed
// nonzero goldens.
func loadServeBundleBindingAt(dir string, original *servefixture.Corpus) (serveBundleBinding, error) {
	fail := func(format string, args ...any) (serveBundleBinding, error) {
		return serveBundleBinding{}, fmt.Errorf(format, args...)
	}
	attributes, err := bindingRegular(filepath.Join(dir, ".gitattributes"))
	if err != nil {
		return fail("%v", err)
	}
	if string(attributes) != "* -text\n" {
		return fail(".gitattributes is %q", attributes)
	}
	const baseline = `{"schema":"faultline.serve-bundle-oracle.v1","sourceCommit":"ce99a793af0b020bf1b9cf21114f8d7897b37207","manifestCommit":"ff5fe7b1120410e60c321f3ae620cf524c5694ec","manifestVersion":"0.1.4","nodeVersion":"22.22.2"}` + "\n"
	provenance, err := bindingRegular(filepath.Join(dir, "baseline.json"))
	if err != nil {
		return fail("%v", err)
	}
	if string(provenance) != baseline {
		return fail("baseline.json is not the exact pinned provenance: %q", provenance)
	}
	want := []string{".gitattributes", "baseline.json", "cases.jsonl", "golden.jsonl", "inventory.json"}
	for _, name := range serveBundleBindingNames {
		want = append(want, "minimization/"+name+".json")
	}
	slices.Sort(want)
	rootInfo, err := os.Lstat(dir)
	if err != nil {
		return fail("%v", err)
	}
	if !rootInfo.IsDir() {
		return fail("%s is a link or not a directory", dir)
	}
	var present []string
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == dir {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "minimization" {
				return fmt.Errorf("unexpected directory %s", rel)
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is a link or special file", path)
		}
		present = append(present, rel)
		return nil
	})
	if err != nil {
		return fail("%v", err)
	}
	slices.Sort(present)
	if !slices.Equal(present, want) {
		return fail("binding corpus files %v, want exactly %v", present, want)
	}

	// The original base root, taken from the original corpus's own retained rows.
	var root string
	for _, c := range original.Cases {
		if c.ID == "cli-serve-111" && c.ExpectedRoot != nil {
			root = *c.ExpectedRoot
		}
	}
	if root == "" {
		return fail("original corpus has no cli-serve-111 expected root")
	}
	// The source minimization digest and hash are those of the original corpus's
	// own retained coherent record (its inventory was verified when it loaded).
	originalFixture, err := original.FixturePath("coherent")
	if err != nil {
		return fail("%v", err)
	}
	originalMinimization := filepath.Join(originalFixture, "minimization.json")
	originalBytes, err := bindingRegular(originalMinimization)
	if err != nil {
		return fail("%v", err)
	}
	originalSum := sha256.Sum256(originalBytes)
	originalVerification := minimize.VerifyFile(originalMinimization, "", false)
	if !originalVerification.Valid || originalVerification.ResultDigest == nil {
		return fail("the original coherent minimization does not verify: %q", originalVerification.Errors)
	}
	var inventory bindingInventory
	rawInventory, err := bindingRegular(filepath.Join(dir, "inventory.json"))
	if err != nil {
		return fail("%v", err)
	}
	if err := strictBindingDecode(rawInventory, &inventory, bindingInventoryShape); err != nil {
		return fail("inventory.json: %v", err)
	}
	if inventory.Schema != "faultline.serve-bundle-binding.v1" || inventory.Source.Corpus != "serve-bundle" || inventory.Source.Fixture != "coherent" ||
		inventory.Source.ProofRoot != root || inventory.Source.MinimizationFile != "minimization.json" ||
		inventory.Source.MinimizationDigest != *originalVerification.ResultDigest ||
		inventory.Source.MinimizationSha256 != hex.EncodeToString(originalSum[:]) {
		return fail("inventory provenance %+v does not match the original corpus's coherent fixture", inventory.Source)
	}
	// The fixed path set and count are enforced before any listed path is opened.
	var inventoried, wantInputs []string
	for _, f := range inventory.Files {
		inventoried = append(inventoried, f.Path)
	}
	for _, name := range serveBundleBindingNames {
		wantInputs = append(wantInputs, "minimization/"+name+".json")
	}
	slices.Sort(inventoried)
	slices.Sort(wantInputs)
	if !slices.Equal(inventoried, wantInputs) {
		return fail("inventory lists %v, want exactly %v once each", inventoried, wantInputs)
	}
	inputs := map[string]bindingInput{}
	for _, f := range inventory.Files {
		data, err := bindingRegular(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if err != nil {
			return fail("%v", err)
		}
		sum := sha256.Sum256(data)
		if len(data) != f.Bytes || hex.EncodeToString(sum[:]) != f.Sha256 {
			return fail("%s does not match its inventory entry", f.Path)
		}
		inputs[f.Path] = bindingInput{sha256: f.Sha256, bytes: f.Bytes}
	}

	caseLines, err := bindingLines(filepath.Join(dir, "cases.jsonl"))
	if err != nil {
		return fail("%v", err)
	}
	goldenLines, err := bindingLines(filepath.Join(dir, "golden.jsonl"))
	if err != nil {
		return fail("%v", err)
	}
	if len(caseLines) != len(serveBundleBindingNames) || len(goldenLines) != len(serveBundleBindingNames) {
		return fail("%d cases and %d goldens, want %d each", len(caseLines), len(goldenLines), len(serveBundleBindingNames))
	}
	out := serveBundleBinding{dir: dir, inputs: inputs}
	digests := map[string]bool{}
	for i, name := range serveBundleBindingNames {
		var c bindingCase
		if err := strictBindingDecode([]byte(caseLines[i]), &c, bindingCaseShape); err != nil {
			return fail("case %d: %v", i+1, err)
		}
		input := "minimization/" + name + ".json"
		if c.ID != "binding-"+name || c.Input != input || c.ExpectedRoot != root {
			return fail("case %d is %+v, want binding-%s over %s and root %s", i+1, c, name, input, root)
		}
		if _, ok := inputs[input]; !ok {
			return fail("%s is not inventoried", input)
		}
		wantArgv := []string{"serve", "--bundle", "{ROOT}/bundle", "--expect-root", root, "--minimization", "{ROOT}/" + serveBundleBindingInput,
			"--expect-minimization", c.ExpectedMinimizationDigest, "--port", "abc"}
		if !slices.Equal(c.Argv, wantArgv) {
			return fail("%s argv %q is not the fixed serve contract %q", c.ID, c.Argv, wantArgv)
		}
		// The retained record must be accepted by the Go verifier with exactly this digest.
		v := minimize.VerifyFile(filepath.Join(dir, filepath.FromSlash(input)), c.ExpectedMinimizationDigest, true)
		if !v.Valid || v.ExternalDigestStatus != "MATCH" || digests[c.ExpectedMinimizationDigest] {
			return fail("%s: record is not verifier-valid with its distinct retained digest: valid=%v status=%s errors=%q", c.ID, v.Valid, v.ExternalDigestStatus, v.Errors)
		}
		digests[c.ExpectedMinimizationDigest] = true
		var g bindingGolden
		if err := strictBindingDecode([]byte(goldenLines[i]), &g, bindingGoldenShape); err != nil {
			return fail("golden %d: %v", i+1, err)
		}
		if err := bindingRequireStrings(goldenLines[i], "stdout", "stderr"); err != nil {
			return fail("golden %d: %v", i+1, err)
		}
		if g.ID != c.ID || g.Process.ExitCode == nil || *g.Process.ExitCode == 0 || g.Process.Signal != nil || g.Process.Stdout != "" ||
			!strings.HasPrefix(g.Process.Stderr, "FaultLine error: ") || !strings.HasSuffix(g.Process.Stderr, "\nNext: pnpm fl help\n") {
			return fail("golden %d is not a completed refusal for %s: %+v", i+1, c.ID, g)
		}
		out.cases = append(out.cases, c)
		out.goldens = append(out.goldens, g)
	}
	return out, nil
}

// copyBindingCaseAt copies a fresh coherent fixture into root and only the
// retained record. The record is reread and must still have the bytes and
// sha256 the loader verified for it; a changed record is an error before
// anything is written. (This detects change after load; it is not a claim of
// confinement against a concurrent writer.)
func copyBindingCaseAt(original *servefixture.Corpus, b serveBundleBinding, c bindingCase, root string) error {
	want, ok := b.inputs[c.Input]
	if !ok {
		return fmt.Errorf("%s was not loaded from the inventory", c.Input)
	}
	data, err := bindingRegular(filepath.Join(b.dir, filepath.FromSlash(c.Input)))
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if len(data) != want.bytes || hex.EncodeToString(sum[:]) != want.sha256 {
		return fmt.Errorf("%s changed after it was loaded and verified", c.Input)
	}
	if err := original.CopyFixture("coherent", root); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, serveBundleBindingInput), data, 0o600)
}

// copyBindingCase is copyBindingCaseAt into a fresh test root.
func copyBindingCase(t *testing.T, original *servefixture.Corpus, b serveBundleBinding, c bindingCase) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "fixture")
	if err := copyBindingCaseAt(original, b, c, root); err != nil {
		t.Fatal(err)
	}
	return root
}

func resolveBindingArgv(c bindingCase, root string) []string {
	out := make([]string, len(c.Argv))
	for i, arg := range c.Argv {
		switch {
		case arg == "{ROOT}":
			out[i] = root
		case strings.HasPrefix(arg, "{ROOT}/"):
			out[i] = filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(arg, "{ROOT}/")))
		default:
			out[i] = arg
		}
	}
	return out
}

// Catches a binding diagnostic that diverges from the frozen-TS capture: the
// verifier-valid foreign-witness, nullable-state, null-witness or foreign-range
// record being accepted, or refused with other text, stream, status, or after
// the port error. Runs the instrumented flBinary (GOCOVERDIR is inherited); it
// needs no Node, Git history or manifest.
func TestServeBundleBindingCLIReplay(t *testing.T) {
	original := loadServeBundleCorpus(t)
	binding := loadServeBundleBinding(t, original)
	for i, c := range binding.cases {
		t.Run(c.ID, func(t *testing.T) {
			root := copyBindingCase(t, original, binding, c)
			stdout, stderr, exit := runServeBundleToExit(t, root, resolveBindingArgv(c, root))
			want := binding.goldens[i].Process
			if got := serveBundleNormalize(stdout, root); got != want.Stdout {
				t.Errorf("stdout\n got %q\nwant %q", got, want.Stdout)
			}
			if got := serveBundleNormalize(stderr, root); got != want.Stderr {
				t.Errorf("stderr\n got %q\nwant %q", got, want.Stderr)
			}
			if exit != *want.ExitCode {
				t.Errorf("exit = %d, want %d", exit, *want.ExitCode)
			}
		})
	}
}

// Catches any difference between the frozen TS CLI and the Go fl on the five
// binding records, and drift of the retained captures, on fresh independent
// roots. oracle.Skip is first; once enabled nothing else skips.
func TestLiveServeBundleBinding(t *testing.T) {
	oracle.Skip(t)
	node, cli := pinServeBundleOracle(t)
	original := loadServeBundleCorpus(t)
	binding := loadServeBundleBinding(t, original)
	sides := []liveSide{{"TS", node, []string{cli}}, {"Go", flBinary, nil}}
	for i, c := range binding.cases {
		t.Run(c.ID, func(t *testing.T) {
			type outcome struct {
				stdout, stderr string
				exit           int
			}
			results := make([]outcome, len(sides))
			for s, side := range sides {
				root := copyBindingCase(t, original, binding, c)
				stdout, stderr, exit := runLiveToExit(t, side, root, resolveBindingArgv(c, root))
				results[s] = outcome{serveBundleNormalize(stdout, root), serveBundleNormalize(stderr, root), exit}
				// Durable native evidence (a no-op without the evidence directory).
				recordServeBundleCapture(t, serveBundleCapture{
					Corpus: evidenceSupplementalCorpus, ID: c.ID, Kind: "cli-error", Fixture: "coherent", Side: side.name, Argv: c.Argv,
					ExpectedRoot: c.ExpectedRoot, Digests: map[string]string{"minimization": c.ExpectedMinimizationDigest}, Input: c.Input,
					Stdout: stdout, Stderr: stderr, Exit: &exit, NormStdout: results[s].stdout, NormStderr: results[s].stderr,
					Root: root, // the only declared substitution of the supplemental cases
				})
			}
			if results[0] != results[1] {
				t.Errorf("Go differs from TS\nTS %+v\nGo %+v", results[0], results[1])
			}
			want := binding.goldens[i].Process
			if results[0].stdout != want.Stdout || results[0].stderr != want.Stderr || results[0].exit != *want.ExitCode {
				t.Errorf("TS oracle drift from the retained capture\n got %+v\nwant %+v", results[0], want)
			}
		})
	}
}

// Catches argv token expansion drifting: only a whole "{ROOT}" and a
// "{ROOT}/relative" prefix are expanded, everything else (including a hostile
// metadata command path) stays literal.
func TestServeBundleBindingArgvExpansion(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "case")
	got := resolveBindingArgv(bindingCase{Argv: []string{"serve", "{ROOT}/" + serveBundleBindingInput, "{ROOT}", "../{ROOT}", "--port", "abc"}}, root)
	want := []string{"serve", filepath.Join(root, serveBundleBindingInput), root, "../{ROOT}", "--port", "abc"}
	if !slices.Equal(got, want) {
		t.Fatalf("argv expansion %q, want %q", got, want)
	}
}
