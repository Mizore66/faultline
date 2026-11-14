//go:build faultline_test

package difftest

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// The guided-investigate oracle corpus (difftest/testdata/guided-investigate/
// oracle) is produced only by difftest/gen/gen-guided-investigate.mjs from
// the frozen TypeScript CLI. This file reads and validates it; nothing in it
// is derived from Go output. The loader is the harness control surface: a
// tampered, missing or extra capture, a changed input, or a changed pin is
// rejected here before any live comparison can trust the corpus.

const (
	guidedOracleSchema    = "faultline.guided-investigate-oracle.v1"
	guidedOracleCaseSchem = "faultline.guided-investigate-oracle-cases.v1"
	guidedCaptureSchema   = "faultline.guided-investigate-capture.v1"
	guidedFrozenCommit    = "ce99a793af0b020bf1b9cf21114f8d7897b37207"
	guidedFrozenSrcTree   = "47b50ed83511d5e7c27e42e587f03d3c9d637bb7"
	guidedOracleNode      = "v22.22.2"
	guidedOracleDir       = "difftest/testdata/guided-investigate/oracle"
)

// guidedOracleInputs are the generator inputs whose bytes the baseline pins,
// relative to the repository root.
var guidedOracleInputs = map[string]string{
	"cases.json":       guidedOracleDir + "/cases.json",
	"clock.mjs":        guidedOracleDir + "/clock.mjs",
	"fakebox/main.go":  guidedOracleDir + "/fakebox/main.go",
	"gen-guided.mjs":   "difftest/gen/gen-guided-investigate.mjs",
	"seed.mjs":         "difftest/gen/seed.mjs",
	"package.json.cli": "package.json",
}

type guidedBaseline struct {
	Schema string `json:"schema"`
	Source struct {
		Commit                string `json:"commit"`
		SrcTree               string `json:"srcTree"`
		FrozenManifestVersion string `json:"frozenManifestVersion"`
		ManifestVersion       string `json:"manifestVersion"`
		CLIVersion            string `json:"cliVersion"`
	} `json:"source"`
	Node struct {
		Version string `json:"version"`
	} `json:"node"`
	Generator struct {
		Path string `json:"path"`
		Mode string `json:"mode"`
	} `json:"generator"`
	// Platform is present only in a native (non-POSIX-default) corpus: the measured platform of the capture.
	Platform *struct {
		GOOS          string `json:"goos"`
		GOARCH        string `json:"goarch"`
		NodePlatform  string `json:"nodePlatform"`
		NodeArch      string `json:"nodeArch"`
		PathSeparator string `json:"pathSeparator"`
	} `json:"platform"`
	Inputs   map[string]string          `json:"inputs"`
	Seed     string                     `json:"seed"`
	CaseIDs  []string                   `json:"caseIds"`
	Counts   struct{ Cases, Steps int } `json:"counts"`
	Captures map[string]string          `json:"captures"`
}

type guidedCaseFile struct {
	Schema     string                   `json:"schema"`
	Seed       string                   `json:"seed"`
	ApprovedBy string                   `json:"approvedBy"`
	Groups     []string                 `json:"groups"`
	Fixtures   map[string]guidedFixture `json:"fixtures"`
	Images     map[string]string        `json:"images"`
	Cases      []guidedCase             `json:"cases"`
}

type guidedFixture struct {
	GitExclude []string `json:"gitExclude"`
	Commits    []struct {
		Message string            `json:"message"`
		Files   map[string]string `json:"files"`
	} `json:"commits"`
}

type guidedCase struct {
	ID         string                     `json:"id"`
	Group      string                     `json:"group"`
	Fixture    string                     `json:"fixture"`
	ExtraRepos []string                   `json:"extraRepos"`
	Tools      guidedTools                `json:"tools"`
	Env        map[string]string          `json:"env"`
	Files      map[string]json.RawMessage `json:"files"`
	RepoFiles  map[string]string          `json:"repoFiles"`
	Steps      []guidedStep               `json:"steps"`
}

type guidedTools struct {
	NodeVersion string `json:"nodeVersion"`
	NoNode      bool   `json:"noNode"`
	NoDocker    bool   `json:"noDocker"`
	DockerDown  bool   `json:"dockerDown"`
	ShowFails   bool   `json:"showFails"`
}

type guidedStep struct {
	Name   string            `json:"name"`
	Args   []string          `json:"args"`
	Cwd    string            `json:"cwd"`
	Env    map[string]string `json:"env"`
	Script []guidedAction    `json:"script"`
	Expect guidedExpect      `json:"expect"`
}

type guidedAction struct {
	Do      string `json:"do"`
	Path    string `json:"path"`
	Token   string `json:"token"`
	Omit    string `json:"omit"`
	Expect  int    `json:"expect"`
	Message string `json:"message"`
}

type guidedExpect struct {
	Exit           *int     `json:"exit"`
	ExitOrSignal   bool     `json:"exitOrSignal"`
	Stdout         *string  `json:"stdout"`
	StdoutContains []string `json:"stdoutContains"`
	StderrContains []string `json:"stderrContains"`
}

// guidedCapture is one retained frozen-TS case capture.
type guidedCapture struct {
	Schema  string `json:"schema"`
	CaseID  string `json:"caseId"`
	Group   string `json:"group"`
	Fixture struct {
		Commits         []string `json:"commits"`
		InputTreeSha256 string   `json:"inputTreeSha256"`
	} `json:"fixture"`
	Replay        string              `json:"replay"`
	Templates     []guidedTemplate    `json:"templates"`
	Steps         []guidedCaptureStep `json:"steps"`
	FinalTree     []guidedTreeEntry   `json:"finalTree"`
	FinalTreeHash string              `json:"finalTreeSha256"`
}

// guidedTemplate is a Node-derived canonical unsigned preimage of a record
// whose digest depends on the owned slot path ({SLOTJ} is the slot as a
// JSON-string-escaped path). The expected digest for a replay is the SHA-256
// of the preimage expanded for the replay's own slot; it is referenced from
// stored text as {DIGEST:<ID>}.
type guidedTemplate struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Preimage string `json:"preimage"`
}

const (
	guidedReplayFull    = "FULL"
	guidedReplayPending = "PENDING_LOCALIZATION_GRAPH"
)

type guidedCaptureStep struct {
	Name      string              `json:"name"`
	Args      []string            `json:"args"`
	Exit      *int                `json:"exit"`
	Signal    *string             `json:"signal"`
	Stdout    string              `json:"stdout"`
	Stderr    string              `json:"stderr"`
	StdoutSha string              `json:"stdoutSha256"`
	StderrSha string              `json:"stderrSha256"`
	Actions   []guidedCapturedAct `json:"actions"`
	Tools     map[string][]string `json:"tools"`
	// ToolCounts is the only tool evidence a PENDING declaration carries.
	ToolCounts map[string]int `json:"toolCounts,omitempty"`
	TreeHash   string         `json:"treeSha256"`
}

type guidedCapturedAct struct {
	Do      string            `json:"do"`
	Path    string            `json:"path,omitempty"`
	Status  int               `json:"status,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Message *bool             `json:"message,omitempty"`
}

type guidedTreeEntry struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Mode    string `json:"mode"`
	Size    int    `json:"size"`
	Sha256  string `json:"sha256"`
	Content string `json:"content"`
	Base64  string `json:"base64"`
	Target  string `json:"target"`
}

type guidedCorpus struct {
	Dir      string
	Baseline guidedBaseline
	Cases    guidedCaseFile
	Captures map[string]guidedCapture
}

func guidedSha256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// loadGuidedCorpus reads and fully validates the corpus under dir. root is
// the repository root against which the pinned generator inputs are found.
func loadGuidedCorpus(root, dir string) (*guidedCorpus, error) {
	read := func(rel string) ([]byte, error) { return os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel))) }
	var c guidedCorpus
	c.Dir = dir
	raw, err := read("baseline.json")
	if err != nil {
		return nil, err
	}
	if err := strictJSON(raw, &c.Baseline); err != nil {
		return nil, fmt.Errorf("baseline.json: %w", err)
	}
	b := &c.Baseline
	if b.Schema != guidedOracleSchema {
		return nil, fmt.Errorf("baseline schema %q", b.Schema)
	}
	if b.Source.Commit != guidedFrozenCommit || b.Source.SrcTree != guidedFrozenSrcTree {
		return nil, fmt.Errorf("baseline source pin %q %q is not the frozen commit/src tree", b.Source.Commit, b.Source.SrcTree)
	}
	if b.Source.FrozenManifestVersion != "0.1.3" || b.Source.ManifestVersion != "0.1.4" || b.Source.CLIVersion != "FaultLine 0.1.4" {
		return nil, fmt.Errorf("baseline manifest provenance %+v", b.Source)
	}
	// A POSIX corpus is never valid on Windows (no silent fallback), and a native corpus only on the exact
	// platform it was measured on.
	if b.Platform == nil {
		if runtime.GOOS == "windows" {
			return nil, fmt.Errorf("the POSIX corpus has no platform block and cannot be used on windows/%s", runtime.GOARCH)
		}
	} else {
		want := map[string]string{"windows": "win32", "linux": "linux", "darwin": "darwin"}[runtime.GOOS]
		wantArch := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
		sep := "/"
		if runtime.GOOS == "windows" {
			sep = `\`
		}
		if b.Platform.GOOS != runtime.GOOS || b.Platform.GOARCH != runtime.GOARCH || b.Platform.NodePlatform != want || b.Platform.NodeArch != wantArch || b.Platform.PathSeparator != sep {
			return nil, fmt.Errorf("corpus platform %+v is not this host %s/%s", *b.Platform, runtime.GOOS, runtime.GOARCH)
		}
	}
	if b.Node.Version != guidedOracleNode {
		return nil, fmt.Errorf("baseline Node %q, want %s", b.Node.Version, guidedOracleNode)
	}
	if !strings.HasPrefix(b.Generator.Path, "difftest/gen/gen-guided-investigate.mjs") || b.Generator.Mode != "capture" {
		return nil, fmt.Errorf("baseline generator identity %+v", b.Generator)
	}
	for name, rel := range guidedOracleInputs {
		want, ok := b.Inputs[name]
		if !ok {
			return nil, fmt.Errorf("baseline lacks input %s", name)
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		if name == "package.json.cli" {
			// The manifest is pinned by its version field only.
			var m struct{ Version string }
			if err := json.Unmarshal(data, &m); err != nil || m.Version != "0.1.4" {
				return nil, fmt.Errorf("package.json version %q", m.Version)
			}
			continue
		}
		if got := guidedSha256(data); got != want {
			return nil, fmt.Errorf("input %s changed since capture: %s != %s", name, got, want)
		}
	}
	if len(b.Inputs) != len(guidedOracleInputs) {
		return nil, fmt.Errorf("baseline pins %d inputs, want %d", len(b.Inputs), len(guidedOracleInputs))
	}
	// cases.json is the input; its case list is the authority for completeness.
	rawCases, err := read("cases.json")
	if err != nil {
		return nil, err
	}
	if want, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(guidedOracleInputs["cases.json"]))); err != nil || !bytes.Equal(want, rawCases) {
		return nil, fmt.Errorf("the corpus cases.json is not the repository's pinned cases.json (%v)", err)
	}
	if err := strictJSON(rawCases, &c.Cases); err != nil {
		return nil, fmt.Errorf("cases.json: %w", err)
	}
	if c.Cases.Schema != guidedOracleCaseSchem || c.Cases.Seed != b.Seed {
		return nil, fmt.Errorf("cases.json schema/seed %q/%q", c.Cases.Schema, c.Cases.Seed)
	}
	var ids []string
	steps := 0
	seen := map[string]bool{}
	for _, cs := range c.Cases.Cases {
		if seen[cs.ID] {
			return nil, fmt.Errorf("duplicate case %s", cs.ID)
		}
		seen[cs.ID] = true
		if !slices.Contains(c.Cases.Groups, cs.Group) {
			return nil, fmt.Errorf("case %s has unknown group %q", cs.ID, cs.Group)
		}
		ids = append(ids, cs.ID)
		steps += len(cs.Steps)
	}
	if !slices.Equal(ids, b.CaseIDs) || b.Counts.Cases != len(ids) || b.Counts.Steps != steps {
		return nil, fmt.Errorf("baseline case list/counts disagree with cases.json (%d cases, %d steps)", len(ids), steps)
	}
	// Exactly the listed capture files exist: no missing, no extra.
	entries, err := os.ReadDir(filepath.Join(dir, "captures"))
	if err != nil {
		return nil, err
	}
	var onDisk []string
	for _, e := range entries {
		onDisk = append(onDisk, strings.TrimSuffix(e.Name(), ".json"))
	}
	sort.Strings(onDisk)
	want := slices.Clone(ids)
	sort.Strings(want)
	if !slices.Equal(onDisk, want) {
		return nil, fmt.Errorf("capture files %v do not match case ids %v", onDisk, want)
	}
	if len(b.Captures) != len(ids) {
		return nil, fmt.Errorf("baseline lists %d captures for %d cases", len(b.Captures), len(ids))
	}
	c.Captures = map[string]guidedCapture{}
	for _, cs := range c.Cases.Cases {
		data, err := read("captures/" + cs.ID + ".json")
		if err != nil {
			return nil, err
		}
		if got := guidedSha256(data); got != b.Captures[cs.ID] {
			return nil, fmt.Errorf("capture %s was altered: %s != %s", cs.ID, got, b.Captures[cs.ID])
		}
		var cap guidedCapture
		if err := strictJSON(data, &cap); err != nil {
			return nil, fmt.Errorf("capture %s: %w", cs.ID, err)
		}
		if cap.Schema != guidedCaptureSchema || cap.CaseID != cs.ID || cap.Group != cs.Group || len(cap.Steps) != len(cs.Steps) {
			return nil, fmt.Errorf("capture %s does not describe its case", cs.ID)
		}
		for i, st := range cap.Steps {
			if st.Name != cs.Steps[i].Name {
				return nil, fmt.Errorf("capture %s step %d is %q, case says %q", cs.ID, i, st.Name, cs.Steps[i].Name)
			}
		}
		if err := validateGuidedCapture(cap); err != nil {
			return nil, fmt.Errorf("capture %s: %w", cs.ID, err)
		}
		c.Captures[cs.ID] = cap
	}
	return &c, nil
}

// guidedDigestTokenRE matches a stored reference to a slot-derived value:
// {DIGEST:<id>} is the "sha256:<hex>" digest of the template's object and
// {FILE:<id>} the bare hex hash of its file bytes.
var guidedDigestTokenRE = regexp.MustCompile(`\{(DIGEST|FILE):([^}]+)\}`)

func guidedCompactTreeHash(entries []guidedTreeEntry) string {
	type compact struct {
		Path   string `json:"path"`
		Kind   string `json:"kind"`
		Mode   string `json:"mode"`
		Size   int    `json:"size"`
		Sha256 string `json:"sha256"`
		Target string `json:"target"`
	}
	list := make([]compact, len(entries))
	for i, e := range entries {
		list[i] = compact{e.Path, e.Kind, e.Mode, e.Size, e.Sha256, e.Target}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(list); err != nil {
		return ""
	}
	return guidedSha256(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
}

// guidedStoredBytes is the entry's stored (token-normalized) file bytes.
func guidedStoredBytes(e guidedTreeEntry) ([]byte, error) {
	if e.Base64 != "" {
		return base64.StdEncoding.DecodeString(e.Base64)
	}
	return []byte(e.Content), nil
}

// guidedTemplateParts splits a template id into its kind (draft, json or
// text) and the tree path it describes.
func guidedTemplateParts(id string) (kind, path string, ok bool) {
	kind, path, ok = strings.Cut(id, ":")
	return kind, path, ok && path != "" && (kind == "draft" || kind == "json" || kind == "text")
}

type guidedResolved struct{ Digest, File string }

// resolveGuidedTemplates expands every template for one owned slot. Kinds:
//
//	draft  digest = sha256 of the canonical unsigned preimage (the record's draftDigest)
//	json   digest = sha256 of the canonical preimage, file hash = sha256 of preimage+LF
//	text   digest = file hash = sha256 of the preimage bytes
//
// A template may name another by {DIGEST:id} (the "sha256:" form) or
// {FILE:id} (the bare hex file hash). A missing or cyclic dependency, a FILE
// reference to a draft, an unknown kind and a duplicate id are errors. The only
// inputs are the frozen template text and the slot.
func resolveGuidedTemplates(slot string, templates []guidedTemplate) (map[string]guidedResolved, error) {
	by := map[string]guidedTemplate{}
	for _, t := range templates {
		if _, dup := by[t.ID]; dup {
			return nil, fmt.Errorf("duplicate template %q", t.ID)
		}
		by[t.ID] = t
	}
	esc := guidedJSONEscaped(slot)
	out := map[string]guidedResolved{}
	active := map[string]bool{}
	var resolve func(id string) (guidedResolved, error)
	resolve = func(id string) (guidedResolved, error) {
		if r, ok := out[id]; ok {
			return r, nil
		}
		t, ok := by[id]
		if !ok {
			return guidedResolved{}, fmt.Errorf("digest reference %q has no template", id)
		}
		if active[id] {
			return guidedResolved{}, fmt.Errorf("cyclic digest dependency at %q", id)
		}
		active[id] = true
		defer delete(active, id)
		kind, _, ok := guidedTemplateParts(id)
		if !ok {
			return guidedResolved{}, fmt.Errorf("template %q has an unknown kind", id)
		}
		var firstErr error
		text := guidedDigestTokenRE.ReplaceAllStringFunc(strings.ReplaceAll(t.Preimage, "{SLOTJ}", esc), func(m string) string {
			sub := guidedDigestTokenRE.FindStringSubmatch(m)
			dep, err := resolve(sub[2])
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return ""
			}
			if sub[1] == "FILE" {
				if dep.File == "" && firstErr == nil {
					firstErr = fmt.Errorf("template %q takes the file hash of %q, which has no file", id, sub[2])
				}
				return dep.File
			}
			return "sha256:" + dep.Digest
		})
		if firstErr != nil {
			return guidedResolved{}, firstErr
		}
		var r guidedResolved
		switch kind {
		case "draft":
			r = guidedResolved{Digest: guidedSha256([]byte(text))}
		case "json":
			r = guidedResolved{Digest: guidedSha256([]byte(text)), File: guidedSha256([]byte(text + "\n"))}
		default:
			h := guidedSha256([]byte(text))
			r = guidedResolved{Digest: h, File: h}
		}
		out[id] = r
		return r, nil
	}
	for _, t := range templates {
		if _, err := resolve(t.ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// validateGuidedCapture checks a capture's internal integrity: every stored
// hash covers exactly the stored bytes, the replay class is FULL, and the
// template/digest links form a closed, acyclic graph tied to the stored
// records.
func validateGuidedCapture(c guidedCapture) error {
	if c.Replay != guidedReplayFull {
		return fmt.Errorf("replay class %q is not %s: every case must be a complete capture", c.Replay, guidedReplayFull)
	}
	for i, st := range c.Steps {
		if len(st.ToolCounts) != 0 {
			return fmt.Errorf("full step %d carries tool counts instead of records", i)
		}
		if guidedSha256([]byte(st.Stdout)) != st.StdoutSha || guidedSha256([]byte(st.Stderr)) != st.StderrSha {
			return fmt.Errorf("step %d stdout/stderr hash does not cover the stored text", i)
		}
	}
	stored := map[string]guidedTreeEntry{}
	for _, e := range c.FinalTree {
		if _, dup := stored[e.Path]; dup {
			return fmt.Errorf("duplicate tree entry %s", e.Path)
		}
		stored[e.Path] = e
		if e.Kind != "file" {
			continue
		}
		b, err := guidedStoredBytes(e)
		if err != nil || len(b) != e.Size || guidedSha256(b) != e.Sha256 {
			return fmt.Errorf("tree entry %s size/sha256 does not cover its stored bytes", e.Path)
		}
	}
	if guidedCompactTreeHash(c.FinalTree) != c.FinalTreeHash {
		return fmt.Errorf("final tree hash does not cover the stored tree")
	}
	known := map[string]guidedTemplate{}
	for _, t := range c.Templates {
		kind, path, ok := guidedTemplateParts(t.ID)
		if _, dup := known[t.ID]; dup || !ok || path != t.Path {
			return fmt.Errorf("template %q is duplicated or does not name its record", t.ID)
		}
		known[t.ID] = t
		e, ok := stored[t.Path]
		if !ok || e.Kind != "file" || e.Base64 != "" {
			return fmt.Errorf("template %s has no stored record", t.ID)
		}
		switch kind {
		case "draft":
			// The template must be exactly the stored record's unsigned preimage.
			var record map[string]any
			if err := json.Unmarshal([]byte(e.Content), &record); err != nil {
				return fmt.Errorf("template %s: stored record is not JSON: %w", t.ID, err)
			}
			if d, _ := record["draftDigest"].(string); d != "{DIGEST:"+t.ID+"}" {
				return fmt.Errorf("template %s: stored digest %q is not its named reference", t.ID, d)
			}
			delete(record, "draftDigest")
			var pre map[string]any
			if err := json.Unmarshal([]byte(t.Preimage), &pre); err != nil || !reflect.DeepEqual(pre, record) {
				return fmt.Errorf("template %s preimage is not the stored record's unsigned form", t.ID)
			}
		case "json":
			if e.Content != t.Preimage+"\n" || !json.Valid([]byte(t.Preimage)) {
				return fmt.Errorf("template %s preimage plus LF is not the stored canonical JSON file", t.ID)
			}
		default:
			if e.Content != t.Preimage {
				return fmt.Errorf("template %s preimage is not the stored file", t.ID)
			}
		}
	}
	// Closed and acyclic: every reference resolves and none loops.
	if _, err := resolveGuidedTemplates("/validation/slot", c.Templates); err != nil {
		return err
	}
	// Every template is reachable from stored text, directly or through another template.
	used := map[string]bool{}
	var mark func(text string)
	mark = func(text string) {
		for _, m := range guidedDigestTokenRE.FindAllStringSubmatch(text, -1) {
			if !used[m[2]] {
				used[m[2]] = true
				if t, ok := known[m[2]]; ok {
					mark(t.Preimage)
				}
			}
		}
	}
	for _, st := range c.Steps {
		for _, text := range append([]string{st.Stdout, st.Stderr}, st.Args...) {
			mark(text)
		}
		for _, recs := range st.Tools {
			for _, r := range recs {
				mark(r)
			}
		}
	}
	for _, e := range c.FinalTree {
		mark(e.Content)
	}
	for id := range used {
		if _, ok := known[id]; !ok {
			return fmt.Errorf("digest reference %q has no template", id)
		}
	}
	for id := range known {
		if !used[id] {
			return fmt.Errorf("template %s is never referenced", id)
		}
	}
	return nil
}

func strictJSON(data []byte, v any) error {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("trailing data")
	}
	return nil
}

// copyGuidedCorpus copies the corpus directory to a fresh temp dir for the
// mutation controls.
func copyGuidedCorpus(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// guidedCorpusDir selects the corpus under test. FAULTLINE_GUIDED_CORPUS_DIR (absolute) names a fresh native
// capture directory for CI to replay before import; the loader still requires the exact pins and the exact
// host platform. Without it POSIX uses the committed Node-free corpus, and Windows uses the committed
// native corpus oracle/native/windows-<arch>, which must exist (there is no POSIX fallback and no skip).
func guidedCorpusDir(t *testing.T) (root, dir string) {
	t.Helper()
	root = repoRoot(t)
	if v := os.Getenv("FAULTLINE_GUIDED_CORPUS_DIR"); v != "" {
		if !filepath.IsAbs(v) {
			t.Fatalf("FAULTLINE_GUIDED_CORPUS_DIR %q must be absolute", v)
		}
		return root, v
	}
	if runtime.GOOS == "windows" {
		return root, filepath.Join(root, filepath.FromSlash(guidedOracleDir), "native", "windows-"+runtime.GOARCH)
	}
	return root, filepath.Join(root, filepath.FromSlash(guidedOracleDir))
}

// TestLiveGuidedInvestigateCorpus validates the retained frozen corpus and
// proves the harness rejects every kind of corruption: a tampered capture, a
// missing case file, an extra case file, a removed case, a changed pin, a
// changed Node version and a changed generator input. It needs no Node and no
// oracle enablement.
func TestLiveGuidedInvestigateCorpus(t *testing.T) {
	root, dir := guidedCorpusDir(t)
	good, err := loadGuidedCorpus(root, dir)
	if err != nil {
		t.Fatalf("committed corpus rejected: %v", err)
	}
	t.Logf("corpus: %d cases, %d steps", good.Baseline.Counts.Cases, good.Baseline.Counts.Steps)
	if good.Baseline.Counts.Cases < 20 {
		t.Fatalf("corpus has only %d cases", good.Baseline.Counts.Cases)
	}
	first := good.Cases.Cases[0].ID
	mutate := func(name string, f func(dir string)) {
		t.Run(name, func(t *testing.T) {
			tmp := copyGuidedCorpus(t, dir)
			f(tmp)
			if _, err := loadGuidedCorpus(root, tmp); err == nil {
				t.Fatal("corrupted corpus was accepted")
			}
		})
	}
	editFile := func(dir, rel string, edit func(string) string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		out := edit(string(b))
		if out == string(b) {
			t.Fatalf("edit of %s changed nothing", rel)
		}
		if err := os.WriteFile(p, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mutate("tampered capture byte", func(d string) {
		editFile(d, "captures/"+first+".json", func(s string) string { return strings.Replace(s, "\"exit\"", "\"exit \"", 1) })
	})
	mutate("missing capture", func(d string) {
		if err := os.Remove(filepath.Join(d, "captures", first+".json")); err != nil {
			t.Fatal(err)
		}
	})
	mutate("extra capture", func(d string) {
		src, _ := os.ReadFile(filepath.Join(d, "captures", first+".json"))
		if err := os.WriteFile(filepath.Join(d, "captures", "extra-case.json"), src, 0o644); err != nil {
			t.Fatal(err)
		}
	})
	mutate("removed case from cases.json", func(d string) {
		editFile(d, "cases.json", func(s string) string { return strings.Replace(s, `"id": "`+first+`"`, `"id": "renamed-case"`, 1) })
	})
	mutate("wrong source pin", func(d string) {
		editFile(d, "baseline.json", func(s string) string { return strings.Replace(s, guidedFrozenCommit, strings.Repeat("0", 40), 1) })
	})
	mutate("wrong node version", func(d string) {
		editFile(d, "baseline.json", func(s string) string { return strings.Replace(s, guidedOracleNode, "v22.23.4", 1) })
	})
	mutate("foreign platform block", func(d string) {
		editFile(d, "baseline.json", func(s string) string {
			return strings.Replace(s, `"inputs": {`, `"platform": {"goos": "plan9", "goarch": "mips", "nodePlatform": "plan9", "nodeArch": "mips", "pathSeparator": "/"},
  "inputs": {`, 1)
		})
	})
	mutate("cases.json differs from the pinned repository file", func(d string) {
		editFile(d, "cases.json", func(s string) string { return strings.Replace(s, `"approvedBy"`, `"approvedBy "`, 1) })
	})
	mutate("wrong manifest provenance", func(d string) {
		editFile(d, "baseline.json", func(s string) string { return strings.Replace(s, "FaultLine 0.1.4", "FaultLine 0.1.3", 1) })
	})
	mutate("wrong capture digest in baseline", func(d string) {
		editFile(d, "baseline.json", func(s string) string {
			return strings.Replace(s, good.Baseline.Captures[first], strings.Repeat("0", 64), 1)
		})
	})
	mutate("changed generator input pin", func(d string) {
		editFile(d, "baseline.json", func(s string) string {
			return strings.Replace(s, good.Baseline.Inputs["clock.mjs"], strings.Repeat("0", 64), 1)
		})
	})
	mutate("case count disagreement", func(d string) {
		editFile(d, "baseline.json", func(s string) string {
			return strings.Replace(s, fmt.Sprintf(`"cases": %d`, good.Baseline.Counts.Cases), `"cases": 1`, 1)
		})
	})
	// Template and digest-link controls: every one must be rejected by the
	// loader before any replay can trust the capture.
	templated := ""
	for _, cs := range good.Cases.Cases {
		if good.Captures[cs.ID].Replay == guidedReplayFull && len(good.Captures[cs.ID].Templates) > 0 {
			templated = cs.ID
			break
		}
	}
	if templated == "" {
		t.Fatal("no capture carries digest templates")
	}
	tpl := good.Captures[templated].Templates[0]
	rewriteCapture := func(d string, edit func(string) string) {
		editFile(d, "captures/"+templated+".json", edit)
		// Re-pin the capture hash so only the integrity checks inside the
		// capture can reject it.
		data, _ := os.ReadFile(filepath.Join(d, "captures", templated+".json"))
		editFile(d, "baseline.json", func(s string) string {
			return strings.Replace(s, good.Baseline.Captures[templated], guidedSha256(data), 1)
		})
	}
	mutate("template preimage changed", func(d string) {
		rewriteCapture(d, func(s string) string { return strings.Replace(s, `\"createdAt\":`, `\"createdAtX\":`, 1) })
	})
	mutate("digest link to an unknown template", func(d string) {
		rewriteCapture(d, func(s string) string { return strings.Replace(s, "{DIGEST:"+tpl.ID+"}", "{DIGEST:draft:nowhere}", 1) })
	})
	mutate("template never referenced", func(d string) {
		rewriteCapture(d, func(s string) string {
			return strings.ReplaceAll(s, "{DIGEST:"+tpl.ID+"}", "sha256:"+strings.Repeat("0", 64))
		})
	})
	mutate("replay class changed", func(d string) {
		rewriteCapture(d, func(s string) string { return strings.Replace(s, `"replay": "FULL"`, `"replay": "NONE"`, 1) })
	})
	mutate("stored stdout altered under an unchanged hash", func(d string) {
		rewriteCapture(d, func(s string) string { return strings.Replace(s, `"stdout": ""`, `"stdout": "x"`, 1) })
	})
	mutate("tree content altered under an unchanged hash", func(d string) {
		rewriteCapture(d, func(s string) string {
			return strings.Replace(s, `"commandDigest\":\"sha256:`, `"commandDigest\":\"sha256:0`, 1)
		})
	})
	// Every case is a complete capture; a declaration-only class is rejected.
	mutate("replay class downgraded to pending", func(d string) {
		rewriteCapture(d, func(s string) string {
			return strings.Replace(s, `"replay": "FULL"`, `"replay": "`+guidedReplayPending+`"`, 1)
		})
	})
	for _, cs := range good.Cases.Cases {
		if good.Captures[cs.ID].Replay != guidedReplayFull {
			t.Errorf("case %s is %s, not FULL", cs.ID, good.Captures[cs.ID].Replay)
		}
	}
	// A proof-bundle graph capture: template kinds and the {FILE:} links.
	const graphCase = "continuation-fakebox-clean-resume"
	gcap := good.Captures[graphCase]
	kinds := map[string]int{}
	for _, tp := range gcap.Templates {
		k, _, _ := guidedTemplateParts(tp.ID)
		kinds[k]++
	}
	if kinds["draft"] != 1 || kinds["json"] != 4 || kinds["text"] != 2 {
		t.Fatalf("%s templates by kind = %v, want 1 draft, 4 json, 2 text", graphCase, kinds)
	}
	rewriteGraph := func(d string, edit func(string) string) {
		editFile(d, "captures/"+graphCase+".json", edit)
		data, _ := os.ReadFile(filepath.Join(d, "captures", graphCase+".json"))
		editFile(d, "baseline.json", func(s string) string {
			return strings.Replace(s, good.Baseline.Captures[graphCase], guidedSha256(data), 1)
		})
	}
	var hashesTpl, manifestTpl guidedTemplate
	for _, tp := range gcap.Templates {
		if strings.HasPrefix(tp.ID, "text:") && hashesTpl.ID == "" {
			hashesTpl = tp
		}
		if strings.HasPrefix(tp.ID, "json:") && strings.HasSuffix(tp.ID, "manifest.json") && manifestTpl.ID == "" {
			manifestTpl = tp
		}
	}
	mutate("graph: template id renamed to an unknown kind", func(d string) {
		rewriteGraph(d, func(s string) string {
			return strings.ReplaceAll(s, hashesTpl.ID, "blob:"+strings.TrimPrefix(hashesTpl.ID, "text:"))
		})
	})
	mutate("graph: file link to a missing template", func(d string) {
		rewriteGraph(d, func(s string) string {
			return strings.Replace(s, "{FILE:"+manifestTpl.ID+"}", "{FILE:json:nowhere}", 1)
		})
	})
	mutate("graph: stored hashes file altered under a template", func(d string) {
		rewriteGraph(d, func(s string) string { return strings.Replace(s, "  investigation.json", "  investigation.jsoX", 1) })
	})
	mutate("graph: duplicate template", func(d string) {
		rewriteGraph(d, func(s string) string {
			i := strings.Index(s, `"templates": [`) + len(`"templates": [`)
			j := strings.Index(s[i:], "{")
			end := i + j
			k := strings.Index(s[end:], "\n    },") + len("\n    }")
			one := s[end : end+k]
			return s[:end] + one + ",\n    " + s[end:]
		})
	})
	mutate("capture describes another case", func(d string) {
		other := good.Cases.Cases[1].ID
		src, _ := os.ReadFile(filepath.Join(d, "captures", other+".json"))
		if err := os.WriteFile(filepath.Join(d, "captures", first+".json"), src, 0o644); err != nil {
			t.Fatal(err)
		}
	})
}

// TestGuidedFailureRetentionAndIsolation proves the failure path of the live
// harness without Node: a failed side's slot is moved, whole and without
// overwriting, into its evidence directory, and the next case has a different
// slot path.
func TestGuidedFailureRetentionAndIsolation(t *testing.T) {
	work := t.TempDir()
	evidence := filepath.Join(work, "evidence", "case-a")
	if guidedCaseSlot(work, "case-a") == guidedCaseSlot(work, "case-b") {
		t.Fatal("two cases share a slot path")
	}
	for round := 0; round < 2; round++ {
		slot := guidedCaseSlot(work, "case-a")
		for _, suffix := range []string{"", ".tools", ".home", ".tmp"} {
			if err := os.MkdirAll(slot+suffix, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(slot+suffix, "f"), []byte(fmt.Sprint(round, suffix)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		guidedRetainFailed(t, slot, evidence, "ts")
		for _, suffix := range []string{"", ".tools", ".home", ".tmp"} {
			if _, err := os.Lstat(slot + suffix); err == nil {
				t.Fatalf("round %d: %s still in place after retention", round, slot+suffix)
			}
		}
	}
	for name, want := range map[string]string{"ts-failed": "0", "ts-failed.1": "1", "ts-failed.tools": "0.tools", "ts-failed.1.tmp": "1.tmp"} {
		b, err := os.ReadFile(filepath.Join(evidence, name, "f"))
		if err != nil || string(b) != want {
			t.Fatalf("%s: %q %v, want %q", name, b, err, want)
		}
	}
	if _, err := os.Lstat(guidedCaseSlot(work, "case-b")); err == nil {
		t.Fatal("next case slot exists")
	}
}

// TestGuidedIndexViewIgnoresStatCache proves the live index observation
// ignores the stat cache but sees an entry change.
func TestGuidedIndexViewIgnoresStatCache(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git is required: %v", err)
	}
	realGit, _ = filepath.Abs(realGit)
	slot := filepath.Join(t.TempDir(), "slot")
	repo := filepath.Join(slot, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		cmd := exec.Command(realGit, args...)
		cmd.Dir = repo
		cmd.Env = []string{"PATH=" + filepath.Dir(realGit), "HOME=" + repo, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "a"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "a")
	p := &guidedPlan{slot: slot, realGit: realGit}
	before := p.indexView(t, "repo/.git/index")
	time.Sleep(10 * time.Millisecond)
	if err := os.Chtimes(filepath.Join(repo, "a"), time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	git("update-index", "--refresh")
	if after := p.indexView(t, "repo/.git/index"); after != before {
		t.Fatalf("stat-cache refresh changed the view: %q vs %q", before, after)
	}
	git("update-index", "--chmod=+x", "a")
	if after := p.indexView(t, "repo/.git/index"); after == before {
		t.Fatal("an entry mode change was not seen")
	}
}

func TestGuidedTemplateGraphResolution(t *testing.T) {
	tpl := func(id, pre string) guidedTemplate {
		_, path, _ := guidedTemplateParts(id)
		return guidedTemplate{ID: id, Path: path, Preimage: pre}
	}
	slot := "/a/b"
	good := []guidedTemplate{
		tpl("json:x/m.json", `{"k":"{SLOTJ}","d":"{DIGEST:json:x/i.json}"}`),
		tpl("json:x/i.json", `{"repo":"{SLOTJ}"}`),
		tpl("text:x/h.txt", "{FILE:json:x/i.json}  i.json\n{FILE:json:x/m.json}  m.json\n"),
	}
	got, err := resolveGuidedTemplates(slot, good)
	if err != nil {
		t.Fatal(err)
	}
	// Independent arithmetic: the inner object first, then each dependent.
	inner := guidedSha256([]byte(`{"repo":"/a/b"}`))
	if got["json:x/i.json"].Digest != inner || got["json:x/i.json"].File != guidedSha256([]byte(`{"repo":"/a/b"}`+"\n")) {
		t.Fatalf("inner resolved to %+v", got["json:x/i.json"])
	}
	outer := guidedSha256([]byte(`{"k":"/a/b","d":"sha256:` + inner + `"}`))
	if got["json:x/m.json"].Digest != outer {
		t.Fatalf("outer digest %s, want %s", got["json:x/m.json"].Digest, outer)
	}
	other, err := resolveGuidedTemplates("/a/bb", good)
	if err != nil || other["text:x/h.txt"].Digest == got["text:x/h.txt"].Digest {
		t.Fatalf("a different slot must change every dependent digest: %v", err)
	}
	for name, bad := range map[string][]guidedTemplate{
		"missing":   {tpl("json:x/m.json", `{"d":"{DIGEST:json:x/none.json}"}`)},
		"cycle":     {tpl("json:x/a.json", `{"d":"{DIGEST:json:x/b.json}"}`), tpl("json:x/b.json", `{"d":"{DIGEST:json:x/a.json}"}`)},
		"self":      {tpl("json:x/a.json", `{"d":"{DIGEST:json:x/a.json}"}`)},
		"dup":       {tpl("json:x/a.json", `{}`), tpl("json:x/a.json", `{}`)},
		"kind":      {{ID: "blob:x", Path: "x", Preimage: "x"}},
		"filedraft": {tpl("draft:x/d.json", `{}`), tpl("text:x/t", "{FILE:draft:x/d.json}")},
	} {
		if _, err := resolveGuidedTemplates(slot, bad); err == nil {
			t.Errorf("%s graph was accepted", name)
		}
	}
}

// TestGuidedHashedInputsKeepLFUnderAutocrlf proves, with a controlled local repository and a repository-local
// core.autocrlf=true (the default of hosted Windows Git), that every byte-pinned generator input is
// materialized with exactly its committed LF bytes by the repository's real .gitattributes, and that the
// control is sensitive: without the seed.mjs rule the same checkout yields CRLF.
func TestGuidedHashedInputsKeepLFUnderAutocrlf(t *testing.T) {
	root := repoRoot(t)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git is required: %v", err)
	}
	attrs, err := os.ReadFile(filepath.Join(root, ".gitattributes"))
	if err != nil {
		t.Fatal(err)
	}
	var inputs []string
	for name, rel := range guidedOracleInputs {
		if name != "package.json.cli" {
			inputs = append(inputs, rel)
		}
	}
	sort.Strings(inputs)
	materialize := func(t *testing.T, attributes string) map[string][]byte {
		t.Helper()
		repo := t.TempDir()
		home := t.TempDir()
		git := func(args ...string) {
			t.Helper()
			cmd := exec.Command(realGit, args...)
			cmd.Dir = repo
			cmd.Env = []string{"PATH=" + filepath.Dir(realGit), "HOME=" + home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull,
				"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.invalid"}
			for _, n := range []string{"SystemRoot", "SYSTEMROOT"} {
				if v, ok := os.LookupEnv(n); ok {
					cmd.Env = append(cmd.Env, n+"="+v)
				}
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		git("init", "-q")
		git("config", "core.autocrlf", "false") // commit exact bytes first
		put := func(rel string, data []byte) {
			dst := filepath.Join(repo, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dst, data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		put(".gitattributes", []byte(attributes))
		for _, rel := range inputs {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(data, []byte("\r")) {
				t.Fatalf("%s holds a CR byte in the repository", rel)
			}
			put(rel, data)
		}
		git("add", "-A")
		git("commit", "-q", "-m", "inputs")
		git("config", "core.autocrlf", "true")
		for _, rel := range inputs {
			if err := os.Remove(filepath.Join(repo, filepath.FromSlash(rel))); err != nil {
				t.Fatal(err)
			}
		}
		git("checkout", "--", ".")
		out := map[string][]byte{}
		for _, rel := range inputs {
			data, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(rel)))
			if err != nil {
				t.Fatal(err)
			}
			out[rel] = data
		}
		return out
	}
	got := materialize(t, string(attrs))
	for _, rel := range inputs {
		want, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if !bytes.Equal(got[rel], want) {
			t.Errorf("%s was not materialized with its committed LF bytes under core.autocrlf=true", rel)
		}
	}
	// Negative control: dropping the seed.mjs rule must change the checkout (CRLF), so the check above can fail.
	var without []string
	for _, line := range strings.Split(string(attrs), "\n") {
		if !strings.HasPrefix(line, "difftest/gen/seed.mjs ") {
			without = append(without, line)
		}
	}
	if len(without) == len(strings.Split(string(attrs), "\n")) {
		t.Fatal(".gitattributes has no difftest/gen/seed.mjs rule")
	}
	bad := materialize(t, strings.Join(without, "\n"))
	if !bytes.Contains(bad["difftest/gen/seed.mjs"], []byte("\r\n")) {
		t.Fatal("control: seed.mjs stayed LF without its rule, so this test would not detect a missing rule")
	}
}
