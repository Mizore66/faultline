// Package servefixture is test support; cmd/fl does not import it.
//
// It loads the retained serve-bundle oracle corpus (difftest/testdata/serve-bundle)
// captured from the frozen TS implementation. Metadata is parsed with
// encoding/json after a strict duplicate-member scan; artifact bytes are never
// parsed or digested here, only copied byte for byte.
package servefixture

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

type Options struct {
	MinimizationFile           *string `json:"minimizationFile"`
	ExpectedMinimizationDigest *string `json:"expectedMinimizationDigest"`
	RepairDirectory            *string `json:"repairDirectory"`
	ExpectedRepairDigest       *string `json:"expectedRepairDigest"`
	PreventionDirectory        *string `json:"preventionDirectory"`
	ExpectedPreventionDigest   *string `json:"expectedPreventionDigest"`
}

type Case struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Fixture      string   `json:"fixture"`
	ExpectedRoot *string  `json:"expectedRoot"`
	Options      Options  `json:"options"`
	Argv         []string `json:"argv"`
}

type ProcessResult struct {
	Stdout   string  `json:"stdout"`
	Stderr   string  `json:"stderr"`
	ExitCode *int    `json:"exitCode"`
	Signal   *string `json:"signal"`
}

type Golden struct {
	ID        string         `json:"id"`
	ValueJSON *string        `json:"valueJSON"`
	Error     *string        `json:"error"`
	Page      *string        `json:"page"`
	Process   *ProcessResult `json:"process"`
}

type Corpus struct {
	Root    string
	Cases   []Case
	Goldens map[string]Golden

	fixtures map[string]*inventoryFixture
	cases    map[string]Case
}

type inventoryFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}

type inventoryDigests struct {
	ProofRoot          string `json:"proofRoot"`
	Minimization       string `json:"minimization"`
	Repair             string `json:"repair"`
	PreventionSummary  string `json:"preventionSummary"`
	PreventionVerified string `json:"preventionVerified"`
}

type inventoryFixture struct {
	ID               string           `json:"id"`
	Root             string           `json:"root"`
	Files            []inventoryFile  `json:"files"`
	Digests          inventoryDigests `json:"digests"`
	PrivateSentinels []string         `json:"privateSentinels"`
}

type inventoryDocument struct {
	Schema   string              `json:"schema"`
	Fixtures []*inventoryFixture `json:"fixtures"`
}

type baselineDocument struct {
	Schema          string `json:"schema"`
	SourceCommit    string `json:"sourceCommit"`
	ManifestCommit  string `json:"manifestCommit"`
	ManifestVersion string `json:"manifestVersion"`
	NodeVersion     string `json:"nodeVersion"`
}

const (
	baselineSchema   = "faultline.serve-bundle-oracle.v1"
	fixturesSchema   = "faultline.serve-bundle-fixtures.v1"
	pinnedSourceSHA  = "ce99a793af0b020bf1b9cf21114f8d7897b37207"
	pinnedManifestSH = "ff5fe7b1120410e60c321f3ae620cf524c5694ec"
	pinnedManifestV  = "0.1.4"
	pinnedNodeV      = "22.22.2"
)

var (
	digestPattern  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	sha256Pattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	fixtureIDRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

// The required case-ID contract is pinned here independently of cases.jsonl,
// golden.jsonl and the generator's rows. Additions need an explicit edit.
var pinnedFamilies = []struct {
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

const pinnedCombinationBits = "000 001 010 011 100 101 110 111"

const pinnedRejectSources = "view-min-expected-wrong view-min-frozen-wrong view-min-witness-wrong view-min-before-commit view-min-before-tree view-min-after-commit view-min-after-tree view-min-wrong-transition " +
	"view-repair-expected-wrong view-repair-investigation-wrong view-repair-frozen-wrong view-repair-citations-invalid view-repair-manifest-malformed view-repair-packet-malformed view-repair-brief-malformed view-repair-manifest-content " +
	"view-prevention-expected-wrong view-prevention-root-wrong view-prevention-witness-wrong view-prevention-investigation-wrong view-prevention-runs-wrong view-prevention-runs-missing"

func pinnedIDs() []string {
	var ids []string
	for _, bits := range strings.Fields(pinnedCombinationBits) {
		ids = append(ids, "view-combination-"+bits)
	}
	for _, family := range pinnedFamilies {
		for _, suffix := range strings.Fields(family.suffixes) {
			ids = append(ids, family.prefix+suffix)
		}
	}
	for _, source := range strings.Fields(pinnedRejectSources) {
		ids = append(ids, "cli-reject-"+source)
	}
	return ids
}

// ---- strict metadata parsing ----

func walkValue(dec *json.Decoder) error {
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
			if err := walkValue(dec); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := walkValue(dec); err != nil {
				return err
			}
		}
	}
	_, err = dec.Token()
	return err
}

func noTrailing(dec *json.Decoder) error {
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("unexpected data after the JSON value")
	}
	return nil
}

// checkKeys requires every object member name to equal a json tag of the
// matching struct field byte for byte. encoding/json folds key case when it
// fills structs, so without this a wrong-case key (or a case-variant duplicate
// that overrides an earlier value) would decode as if it were exact.
func checkKeys(value any, t reflect.Type, path string) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		members, ok := value.(map[string]any)
		if !ok {
			return nil // null or a type mismatch; the typed decode reports it
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
			if name == "" || name == "-" {
				continue
			}
			fields[name] = t.Field(i).Type
		}
		for key, child := range members {
			fieldType, known := fields[key]
			if !known {
				return fmt.Errorf("%s: member %q is not an exact-case field name", path, key)
			}
			if err := checkKeys(child, fieldType, path+"."+key); err != nil {
				return err
			}
		}
	case reflect.Slice:
		items, ok := value.([]any)
		if !ok {
			return nil
		}
		for i, item := range items {
			if err := checkKeys(item, t.Elem(), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

// strictDecode rejects duplicate members at every depth, wrong-case or unknown
// member names, unknown fields and trailing data before filling v.
func strictDecode(data []byte, v any) error {
	scan := json.NewDecoder(bytes.NewReader(data))
	if err := walkValue(scan); err != nil {
		return err
	}
	if err := noTrailing(scan); err != nil {
		return err
	}
	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		return err
	}
	if err := checkKeys(generic, reflect.TypeOf(v), "$"); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	return noTrailing(dec)
}

func decodeLines[T any](data []byte, name string) ([]T, error) {
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return nil, fmt.Errorf("%s must be non-empty and end with a newline", name)
	}
	var rows []T
	for i, line := range strings.Split(string(data[:len(data)-1]), "\n") {
		if line == "" {
			return nil, fmt.Errorf("%s line %d is blank", name, i+1)
		}
		var row T
		if err := strictDecode([]byte(line), &row); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", name, i+1, err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// ---- path safety ----

// safeRelative accepts a non-empty slash-separated relative path with no empty,
// "." or ".." component, no backslash, NUL or drive/absolute prefix.
func safeRelative(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return !strings.Contains(strings.Split(p, "/")[0], ":")
}

func regularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	return nil
}

// ---- loading ----

func Load(root string) (*Corpus, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	c := &Corpus{Root: abs, Goldens: map[string]Golden{}, fixtures: map[string]*inventoryFixture{}, cases: map[string]Case{}}
	attributes, err := c.read(".gitattributes")
	if err != nil {
		return nil, err
	}
	if string(attributes) != "* -text\n" {
		return nil, errors.New(".gitattributes must be exactly \"* -text\\n\" so checkout cannot convert retained bytes")
	}
	if err := c.loadBaseline(); err != nil {
		return nil, err
	}
	if err := c.loadFixtures(); err != nil {
		return nil, err
	}
	if err := c.loadCases(); err != nil {
		return nil, err
	}
	if err := c.loadGoldens(); err != nil {
		return nil, err
	}
	if err := c.checkPages(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Corpus) read(name string) ([]byte, error) {
	path := filepath.Join(c.Root, name)
	if err := regularFile(path); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func (c *Corpus) loadBaseline() error {
	data, err := c.read("baseline.json")
	if err != nil {
		return err
	}
	var doc baselineDocument
	if err := strictDecode(data, &doc); err != nil {
		return fmt.Errorf("baseline.json: %w", err)
	}
	want := baselineDocument{baselineSchema, pinnedSourceSHA, pinnedManifestSH, pinnedManifestV, pinnedNodeV}
	if doc != want {
		return fmt.Errorf("baseline.json does not carry the exact pinned provenance: got %+v", doc)
	}
	return nil
}

func (c *Corpus) loadFixtures() error {
	data, err := c.read("fixtures.json")
	if err != nil {
		return err
	}
	var doc inventoryDocument
	if err := strictDecode(data, &doc); err != nil {
		return fmt.Errorf("fixtures.json: %w", err)
	}
	if doc.Schema != fixturesSchema {
		return fmt.Errorf("fixtures.json schema = %q", doc.Schema)
	}
	if len(doc.Fixtures) == 0 {
		return errors.New("fixtures.json lists no fixtures")
	}
	for _, f := range doc.Fixtures {
		if f == nil || !fixtureIDRegex.MatchString(f.ID) {
			return errors.New("fixtures.json has a missing or unsafe fixture id")
		}
		if _, dup := c.fixtures[f.ID]; dup {
			return fmt.Errorf("fixtures.json repeats fixture %q", f.ID)
		}
		if f.Root != "inputs/"+f.ID {
			return fmt.Errorf("fixture %s: root %q must be inputs/%s", f.ID, f.Root, f.ID)
		}
		for name, d := range map[string]string{
			"proofRoot": f.Digests.ProofRoot, "minimization": f.Digests.Minimization, "repair": f.Digests.Repair,
			"preventionSummary": f.Digests.PreventionSummary, "preventionVerified": f.Digests.PreventionVerified,
		} {
			if !digestPattern.MatchString(d) {
				return fmt.Errorf("fixture %s: digest %s is not sha256:<64 lowercase hex>", f.ID, name)
			}
		}
		seen := map[string]bool{}
		for _, file := range f.Files {
			switch {
			case !safeRelative(file.Path):
				return fmt.Errorf("fixture %s: unsafe inventory path %q", f.ID, file.Path)
			case seen[file.Path]:
				return fmt.Errorf("fixture %s: duplicate inventory path %q", f.ID, file.Path)
			case !sha256Pattern.MatchString(file.SHA256):
				return fmt.Errorf("fixture %s: file %s has a malformed sha256", f.ID, file.Path)
			case file.Bytes < 0:
				return fmt.Errorf("fixture %s: file %s has a negative length", f.ID, file.Path)
			}
			seen[file.Path] = true
		}
		c.fixtures[f.ID] = f
	}
	for _, f := range doc.Fixtures {
		if err := c.verifyInventory(f); err != nil {
			return err
		}
	}
	return nil
}

func (c *Corpus) fixtureDir(f *inventoryFixture) string {
	return filepath.Join(c.Root, filepath.FromSlash(f.Root))
}

func verifyBytes(f *inventoryFixture, file inventoryFile, data []byte) error {
	if len(data) != file.Bytes {
		return fmt.Errorf("fixture %s: file %s has %d bytes, inventory says %d", f.ID, file.Path, len(data), file.Bytes)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != file.SHA256 {
		return fmt.Errorf("fixture %s: file %s does not match its inventoried sha256", f.ID, file.Path)
	}
	return nil
}

// verifyInventory walks the fixture tree: every regular file must be
// inventoried with matching bytes, no inventoried file may be absent, and links
// or special files are refused. Directory entries and permission bits are not
// inventoried.
func (c *Corpus) verifyInventory(f *inventoryFixture) error {
	dir := c.fixtureDir(f)
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("fixture %s: %w", f.ID, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("fixture %s: root is not a real directory", f.ID)
	}
	want := map[string]inventoryFile{}
	for _, file := range f.Files {
		want[file.Path] = file
	}
	found := map[string]bool{}
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("fixture %s: %s is a symbolic link", f.ID, rel)
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("fixture %s: %s is a special file", f.ID, rel)
		}
		file, ok := want[rel]
		if !ok {
			return fmt.Errorf("fixture %s: unexpected file %s", f.ID, rel)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		found[rel] = true
		return verifyBytes(f, file, data)
	})
	if err != nil {
		return err
	}
	for _, file := range f.Files {
		if !found[file.Path] {
			return fmt.Errorf("fixture %s: inventoried file %s is missing or not a regular file", f.ID, file.Path)
		}
	}
	return nil
}

func (c *Corpus) loadCases() error {
	data, err := c.read("cases.jsonl")
	if err != nil {
		return err
	}
	rows, err := decodeLines[Case](data, "cases.jsonl")
	if err != nil {
		return err
	}
	for _, row := range rows {
		if _, dup := c.cases[row.ID]; dup {
			return fmt.Errorf("duplicate case id %s", row.ID)
		}
		c.cases[row.ID] = row
	}
	for _, id := range pinnedIDs() {
		if _, ok := c.cases[id]; !ok {
			return fmt.Errorf("required case %s is missing", id)
		}
	}
	required := map[string]bool{}
	for _, id := range pinnedIDs() {
		required[id] = true
	}
	for _, row := range rows {
		if !required[row.ID] {
			return fmt.Errorf("unknown case id %s", row.ID)
		}
		if _, ok := c.fixtures[row.Fixture]; !ok {
			return fmt.Errorf("case %s: unknown fixture %q", row.ID, row.Fixture)
		}
		if err := checkKind(row); err != nil {
			return err
		}
	}
	c.Cases = rows
	return nil
}

func checkKind(row Case) error {
	switch row.Kind {
	case "view", "cli-error", "cli-serve":
	default:
		return fmt.Errorf("case %s: unknown kind %q", row.ID, row.Kind)
	}
	isView := strings.HasPrefix(row.ID, "view-")
	if isView != (row.Kind == "view") {
		return fmt.Errorf("case %s: kind %q does not fit its family", row.ID, row.Kind)
	}
	if strings.HasPrefix(row.ID, "cli-serve-") && row.Kind != "cli-serve" {
		return fmt.Errorf("case %s: must be kind cli-serve", row.ID)
	}
	if (strings.HasPrefix(row.ID, "cli-reject-") || strings.HasPrefix(row.ID, "cli-order-")) && row.Kind != "cli-error" {
		return fmt.Errorf("case %s: must be kind cli-error", row.ID)
	}
	if suffix, ok := strings.CutPrefix(row.ID, "cli-port-"); ok {
		want := "cli-error"
		if validPortSuffixes[suffix] {
			want = "cli-serve"
		}
		if row.Kind != want {
			return fmt.Errorf("case %s: kind %q, must be %s", row.ID, row.Kind, want)
		}
	}
	return nil
}

// Port spellings whose valid load plus JS Number coercion still serves; every
// other cli-port case is a completed refusal.
var validPortSuffixes = map[string]bool{
	"default": true, "zero": true, "empty": true, "missing": true, "whitespace": true, "hex": true, "exponent": true,
}

func (c *Corpus) loadGoldens() error {
	data, err := c.read("golden.jsonl")
	if err != nil {
		return err
	}
	rows, err := decodeLines[Golden](data, "golden.jsonl")
	if err != nil {
		return err
	}
	for _, g := range rows {
		if _, dup := c.Goldens[g.ID]; dup {
			return fmt.Errorf("duplicate golden id %s", g.ID)
		}
		if _, ok := c.cases[g.ID]; !ok {
			return fmt.Errorf("golden %s has no case", g.ID)
		}
		c.Goldens[g.ID] = g
	}
	for _, row := range c.Cases {
		g, ok := c.Goldens[row.ID]
		if !ok {
			return fmt.Errorf("case %s has no golden", row.ID)
		}
		if err := checkShape(row, g); err != nil {
			return err
		}
	}
	return nil
}

func checkShape(row Case, g Golden) error {
	switch row.Kind {
	case "view":
		succeeded := g.ValueJSON != nil && g.Page != nil && g.Error == nil
		failed := g.Error != nil && g.ValueJSON == nil && g.Page == nil
		if g.Process != nil || succeeded == failed {
			return fmt.Errorf("golden %s: a view needs valueJSON+page, or only an error, and no process", g.ID)
		}
	case "cli-serve":
		if g.Process == nil || g.Page == nil || g.ValueJSON != nil || g.Error != nil {
			return fmt.Errorf("golden %s: a cli-serve case needs a process and a page only", g.ID)
		}
		if g.Process.ExitCode == nil || *g.Process.ExitCode != 0 || g.Process.Signal != nil {
			return fmt.Errorf("golden %s: a cli-serve case must record a completed exit 0 with no signal", g.ID)
		}
	case "cli-error":
		if g.Process == nil || g.Page != nil || g.ValueJSON != nil || g.Error != nil {
			return fmt.Errorf("golden %s: a cli-error case needs a process only", g.ID)
		}
		if g.Process.ExitCode == nil || *g.Process.ExitCode == 0 || g.Process.Signal != nil {
			return fmt.Errorf("golden %s: a cli-error case must record a completed nonzero exit with no signal", g.ID)
		}
	}
	if g.Page != nil {
		if !safeRelative(*g.Page) {
			return fmt.Errorf("golden %s: unsafe page path %q", g.ID, *g.Page)
		}
	}
	return nil
}

func (c *Corpus) pagePath(name string) string {
	return filepath.Join(c.Root, "pages", filepath.FromSlash(name))
}

// checkPages requires every referenced page to be a regular file and every
// regular file under pages/ to be referenced.
func (c *Corpus) checkPages() error {
	referenced := map[string]bool{}
	for _, row := range c.Cases {
		g := c.Goldens[row.ID]
		if g.Page == nil {
			continue
		}
		if err := regularFile(c.pagePath(*g.Page)); err != nil {
			return fmt.Errorf("case %s: page %s: %w", row.ID, *g.Page, err)
		}
		referenced[*g.Page] = true
	}
	pages := filepath.Join(c.Root, "pages")
	return filepath.WalkDir(pages, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == pages && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if path == pages || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(pages, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !d.Type().IsRegular() {
			return fmt.Errorf("pages/%s is not a regular file", rel)
		}
		if !referenced[rel] {
			return fmt.Errorf("pages/%s is not referenced by any golden", rel)
		}
		return nil
	})
}

// ---- accessors ----

func (c *Corpus) fixtureFor(id string) (*inventoryFixture, error) {
	f, ok := c.fixtures[id]
	if !ok || !fixtureIDRegex.MatchString(id) {
		return nil, fmt.Errorf("unknown fixture %q", id)
	}
	return f, nil
}

func (c *Corpus) caseFixture(id string) (*inventoryFixture, error) {
	row, ok := c.cases[id]
	if !ok {
		return nil, fmt.Errorf("unknown case %q", id)
	}
	return c.fixtureFor(row.Fixture)
}

// Page returns the exact retained page bytes of a case, reread from disk.
func (c *Corpus) Page(id string) ([]byte, error) {
	g, ok := c.Goldens[id]
	if !ok {
		return nil, fmt.Errorf("unknown case %q", id)
	}
	if g.Page == nil {
		return nil, fmt.Errorf("case %s has no retained page", id)
	}
	if !safeRelative(*g.Page) {
		return nil, fmt.Errorf("case %s: unsafe page path %q", id, *g.Page)
	}
	path := c.pagePath(*g.Page)
	if err := regularFile(path); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// FixturePath returns the retained directory of a fixture id.
func (c *Corpus) FixturePath(id string) (string, error) {
	f, err := c.fixtureFor(id)
	if err != nil {
		return "", err
	}
	return c.fixtureDir(f), nil
}

// CopyFixture copies the exact retained regular-file bytes to a destination
// that must not exist. It re-verifies the inventory first and never repairs.
func (c *Corpus) CopyFixture(id, destination string) error {
	f, err := c.fixtureFor(id)
	if err != nil {
		return err
	}
	if destination == "" {
		return errors.New("empty destination")
	}
	if err := c.verifyInventory(f); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("destination %s already exists", destination)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		return err
	}
	fail := func(err error) error {
		_ = os.RemoveAll(destination)
		return err
	}
	dir := c.fixtureDir(f)
	for _, file := range f.Files {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(file.Path)))
		if err != nil {
			return fail(err)
		}
		if err := verifyBytes(f, file, data); err != nil {
			return fail(err)
		}
		target := filepath.Join(destination, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fail(err)
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return fail(err)
		}
	}
	return nil
}

// ResolveOptions resolves only the retained path fields against the case's
// fixture root. Expected digests stay literal. The input is not mutated.
func (c *Corpus) ResolveOptions(id string, options Options) (Options, error) {
	f, err := c.caseFixture(id)
	if err != nil {
		return Options{}, err
	}
	dir := c.fixtureDir(f)
	resolve := func(name string, p *string) (*string, error) {
		if p == nil {
			return nil, nil
		}
		if !safeRelative(*p) {
			return nil, fmt.Errorf("case %s: unsafe retained %s path %q", id, name, *p)
		}
		out := filepath.Join(dir, filepath.FromSlash(*p))
		return &out, nil
	}
	literal := func(p *string) *string {
		if p == nil {
			return nil
		}
		v := *p
		return &v
	}
	var out Options
	if out.MinimizationFile, err = resolve("minimization", options.MinimizationFile); err != nil {
		return Options{}, err
	}
	if out.RepairDirectory, err = resolve("repair", options.RepairDirectory); err != nil {
		return Options{}, err
	}
	if out.PreventionDirectory, err = resolve("prevention", options.PreventionDirectory); err != nil {
		return Options{}, err
	}
	out.ExpectedMinimizationDigest = literal(options.ExpectedMinimizationDigest)
	out.ExpectedRepairDigest = literal(options.ExpectedRepairDigest)
	out.ExpectedPreventionDigest = literal(options.ExpectedPreventionDigest)
	return out, nil
}

// ResolveArgv expands only a whole "{ROOT}" argument, a "{ROOT}/relative"
// prefix, and a whole "{PORT}" argument. Everything else is literal, so odd CLI
// port spellings are passed through exactly as declared.
func (c *Corpus) ResolveArgv(id, root string, port int, argv []string) ([]string, error) {
	if _, ok := c.cases[id]; !ok {
		return nil, fmt.Errorf("unknown case %q", id)
	}
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("port %d is outside 0..65535", port)
	}
	if root == "" {
		return nil, errors.New("empty case root")
	}
	out := make([]string, len(argv))
	for i, arg := range argv {
		switch {
		case arg == "{PORT}":
			out[i] = strconv.Itoa(port)
		case arg == "{ROOT}":
			out[i] = root
		case strings.HasPrefix(arg, "{ROOT}/"):
			rest := strings.TrimPrefix(arg, "{ROOT}/")
			if !safeRelative(rest) {
				return nil, fmt.Errorf("case %s: unsafe {ROOT} path %q", id, arg)
			}
			out[i] = filepath.Join(root, filepath.FromSlash(rest))
		default:
			out[i] = arg
		}
	}
	return out, nil
}
