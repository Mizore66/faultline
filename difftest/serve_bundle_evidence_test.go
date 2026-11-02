//go:build faultline_test

package difftest

import (
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/difftest/servefixture"
)

// Durable native evidence for `serve --bundle` (Task 5B). When the test-only
// environment variable below names a directory, the live comparison tests write
// every side's complete capture there (raw stdout/stderr bytes, status, the full
// served page, the case identity and original argv) and the identity test writes
// the native identity and corpus inventory. Nothing here compares or relaxes any
// existing assertion: recording happens before the comparisons and never
// substitutes for them. Production code has no such variable. A create or write
// failure FAILS the test (never skips); an existing destination file is a
// duplicate and also fails, so no record is ever overwritten.
//
// TestServeBundleEvidenceAudit then validates the whole directory against the
// strictly loaded corpora: exactly the 68 original and 5 supplemental case ids,
// both sides, no missing, duplicate or extra file, the retained expectations, and
// the native identity.

const (
	serveBundleEvidenceEnv      = "FAULTLINE_SERVE_BUNDLE_EVIDENCE_DIR"
	serveBundleEvidenceAuditEnv = "FAULTLINE_SERVE_BUNDLE_EVIDENCE_AUDIT"
	serveBundleEvidenceSchema   = "faultline.serve-bundle-native-evidence.v1"

	evidenceOriginalCorpus     = "serve-bundle"
	evidenceSupplementalCorpus = "serve-bundle-binding"
	evidenceSupplementalCases  = 5
)

func serveBundleEvidenceDir() string { return os.Getenv(serveBundleEvidenceEnv) }

// serveBundleCapture is one side's complete outcome of one case.
type serveBundleCapture struct {
	Corpus, ID, Kind, Fixture, Side string
	Argv                            []string
	ExpectedRoot                    string
	Digests                         map[string]string
	Input                           string
	Stdout, Stderr                  string // raw bytes as the process wrote them
	Exit                            *int
	Signal                          *string
	NormStdout, NormStderr          string // after the declared substitutions only
	Page                            []byte // the full served page; nil for refusals

	// The invocation-local context of the declared substitutions, so the audit
	// can derive the normalized text from the raw bytes: the case root (always
	// replaced by {ROOT}), the occupied port (127.0.0.1:<port> only, refusals
	// with {PORT} in their argv) and the concrete startup URL (servers, stdout
	// only, first occurrence). Zero values mean no such substitution.
	Root string
	Port int
	URL  string
}

// serveBundleCaptureOf is a capture shell for a case of the original corpus.
func serveBundleCaptureOf(c servefixture.Case, side string) serveBundleCapture {
	out := serveBundleCapture{Corpus: evidenceOriginalCorpus, ID: c.ID, Kind: c.Kind, Fixture: c.Fixture, Side: side, Argv: c.Argv, Digests: map[string]string{}}
	if c.ExpectedRoot != nil {
		out.ExpectedRoot = *c.ExpectedRoot
	}
	for name, digest := range map[string]*string{"minimization": c.Options.ExpectedMinimizationDigest, "repair": c.Options.ExpectedRepairDigest, "prevention": c.Options.ExpectedPreventionDigest} {
		if digest != nil {
			out.Digests[name] = *digest
		}
	}
	return out
}

// evidenceNormalization is the context of the declared substitutions (see
// serveBundleCapture); the audit applies exactly these and nothing inferred.
type evidenceNormalization struct {
	Root         string `json:"root"`
	OccupiedPort int    `json:"occupiedPort"`
	ServerURL    string `json:"serverURL"`
}

type serveBundleEvidenceRecord struct {
	Normalization    evidenceNormalization `json:"normalization"`
	Schema           string                `json:"schema"`
	Corpus           string                `json:"corpus"`
	ID               string                `json:"id"`
	Kind             string                `json:"kind"`
	Fixture          string                `json:"fixture"`
	Side             string                `json:"side"`
	Argv             []string              `json:"argv"`
	ExpectedRoot     string                `json:"expectedRoot"`
	Digests          map[string]string     `json:"digests"`
	Input            string                `json:"input"`
	ExitCode         *int                  `json:"exitCode"`
	Signal           *string               `json:"signal"`
	StdoutSHA256     string                `json:"stdoutSha256"`
	StdoutBytes      int                   `json:"stdoutBytes"`
	StderrSHA256     string                `json:"stderrSha256"`
	StderrBytes      int                   `json:"stderrBytes"`
	NormalizedStdout string                `json:"normalizedStdout"`
	NormalizedStderr string                `json:"normalizedStderr"`
	PageSHA256       string                `json:"pageSha256"`
	PageBytes        int                   `json:"pageBytes"`
}

func evidenceSide(side string) string { return strings.ToLower(side) }

func sha256Of(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func evidenceWrite(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// writeServeBundleCapture writes one side's immutable files; record.json is last,
// so a directory without it is an incomplete capture the audit rejects.
func writeServeBundleCapture(dir string, c serveBundleCapture) error {
	base := filepath.Join(dir, "captures", c.Corpus, c.ID, evidenceSide(c.Side))
	if err := os.MkdirAll(base, 0o700); err != nil {
		return err
	}
	record := serveBundleEvidenceRecord{
		Schema: serveBundleEvidenceSchema, Corpus: c.Corpus, ID: c.ID, Kind: c.Kind, Fixture: c.Fixture, Side: evidenceSide(c.Side),
		Argv: c.Argv, ExpectedRoot: c.ExpectedRoot, Digests: c.Digests, Input: c.Input, ExitCode: c.Exit, Signal: c.Signal,
		StdoutSHA256: sha256Of([]byte(c.Stdout)), StdoutBytes: len(c.Stdout), StderrSHA256: sha256Of([]byte(c.Stderr)), StderrBytes: len(c.Stderr),
		NormalizedStdout: c.NormStdout, NormalizedStderr: c.NormStderr,
		Normalization: evidenceNormalization{Root: c.Root, OccupiedPort: c.Port, ServerURL: c.URL},
	}
	if c.Digests == nil {
		record.Digests = map[string]string{}
	}
	if c.Argv == nil {
		record.Argv = []string{}
	}
	files := map[string][]byte{"stdout": []byte(c.Stdout), "stderr": []byte(c.Stderr)}
	if c.Page != nil {
		files["page.html"] = c.Page
		record.PageSHA256, record.PageBytes = sha256Of(c.Page), len(c.Page)
	}
	for name, data := range files {
		if err := evidenceWrite(filepath.Join(base, name), data); err != nil {
			return err
		}
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return evidenceWrite(filepath.Join(base, "record.json"), append(encoded, '\n'))
}

// recordServeBundleCapture is the live tests' hook: a no-op unless the evidence
// directory is configured, a failure otherwise.
func recordServeBundleCapture(t *testing.T, c serveBundleCapture) {
	t.Helper()
	dir := serveBundleEvidenceDir()
	if dir == "" {
		return
	}
	if err := writeServeBundleCapture(dir, c); err != nil {
		t.Fatalf("recording the %s capture of %s/%s: %v", c.Side, c.Corpus, c.ID, err)
	}
}

// ---- native identity and corpus inventory ----

type serveBundleIdentity struct {
	Schema      string `json:"schema"`
	RunnerOS    string `json:"runnerOS"`
	RunnerArch  string `json:"runnerArch"`
	GOOS        string `json:"goos"`
	GOARCH      string `json:"goarch"`
	GoVersion   string `json:"goVersion"`
	GoEnvGOOS   string `json:"goEnvGOOS"`
	GoEnvGOARCH string `json:"goEnvGOARCH"`
	GoEnvCGO    string `json:"goEnvCGOEnabled"`
	NodeVersion string `json:"nodeVersion"`
	CLIVersion  string `json:"cliVersion"`
	FlSHA256    string `json:"flSha256"`
	FlBytes     int64  `json:"flBytes"`
	FlBuildOS   string `json:"flBuildGOOS"`
	FlBuildArch string `json:"flBuildGOARCH"`
	FlBuildCGO  string `json:"flBuildCGOEnabled"`
	Head        string `json:"head"`
	Event       string `json:"event"`
	EventHead   string `json:"eventHead"`
	GithubSHA   string `json:"githubSha"`
	// The matrix leg's INTENDED target, taken from the workflow's explicit
	// per-leg context and recorded as provenance. It is never derived from the
	// actual dimensions and is checked against an independent expectation.
	IntendedGOOS   string `json:"intendedGOOS"`
	IntendedGOARCH string `json:"intendedGOARCH"`
}

// serveBundleTargets are the six required native targets: intended GOOS to the
// intended GOARCH values, with the matching GitHub runner names.
var serveBundleTargets = map[string]map[string]bool{
	"linux":   {"amd64": true, "arm64": true},
	"darwin":  {"amd64": true, "arm64": true},
	"windows": {"amd64": true, "arm64": true},
}

// validServeBundleTarget reports whether (goos, goarch) is one of the six.
func validServeBundleTarget(goos, goarch string) bool { return serveBundleTargets[goos][goarch] }

// evidenceTargetFromEnv reads the explicit intended leg from the workflow's
// test-only context. It fails on an absent, partial or unknown pair and never
// falls back to the actual platform.
func evidenceTargetFromEnv() (goos, goarch string, err error) {
	goos, goarch = os.Getenv("FAULTLINE_EVIDENCE_EXPECT_GOOS"), os.Getenv("FAULTLINE_EVIDENCE_EXPECT_GOARCH")
	switch {
	case goos == "" && goarch == "":
		return "", "", errors.New("FAULTLINE_EVIDENCE_EXPECT_GOOS and FAULTLINE_EVIDENCE_EXPECT_GOARCH are both unset")
	case goos == "" || goarch == "":
		return "", "", fmt.Errorf("partial intended target (GOOS %q, GOARCH %q)", goos, goarch)
	case !validServeBundleTarget(goos, goarch):
		return "", "", fmt.Errorf("unknown intended target %s/%s", goos, goarch)
	}
	return goos, goarch, nil
}

// checkServeBundleTarget binds recorded identity to the independently supplied
// intended target: the pair itself must be one of the six, the recorded intended
// fields must equal it, and every actual dimension (runner, test process, go env,
// launched fl build) must equal it. The structural coherence of the identity is
// checkServeBundleIdentity's separate concern.
func checkServeBundleTarget(id serveBundleIdentity, goos, goarch string) error {
	if !validServeBundleTarget(goos, goarch) {
		return fmt.Errorf("the expected target %q/%q is not one of the six native targets", goos, goarch)
	}
	if id.IntendedGOOS != goos || id.IntendedGOARCH != goarch {
		return fmt.Errorf("recorded intended target %q/%q is not the expected %s/%s", id.IntendedGOOS, id.IntendedGOARCH, goos, goarch)
	}
	runnerOS := map[string]string{"linux": "Linux", "darwin": "macOS", "windows": "Windows"}[goos]
	runnerArch := map[string]string{"amd64": "X64", "arm64": "ARM64"}[goarch]
	actual := map[string][2]string{
		"runner":      {id.RunnerOS + "/" + id.RunnerArch, runnerOS + "/" + runnerArch},
		"test":        {id.GOOS + "/" + id.GOARCH, goos + "/" + goarch},
		"go env":      {id.GoEnvGOOS + "/" + id.GoEnvGOARCH, goos + "/" + goarch},
		"launched fl": {id.FlBuildOS + "/" + id.FlBuildArch, goos + "/" + goarch},
	}
	for name, pair := range actual {
		if pair[0] != pair[1] {
			return fmt.Errorf("the %s ran on %s, not the intended %s", name, pair[0], pair[1])
		}
	}
	return nil
}

type serveBundleInventoryFile struct {
	Path   string `json:"path"`
	Sha256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}

func inventoryTree(root, prefix string) ([]serveBundleInventoryFile, error) {
	var out []serveBundleInventoryFile
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is a link or special file", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		out = append(out, serveBundleInventoryFile{Path: prefix + "/" + filepath.ToSlash(rel), Sha256: sha256Of(data), Bytes: len(data)})
		return nil
	})
	slices.SortFunc(out, func(a, b serveBundleInventoryFile) int { return strings.Compare(a.Path, b.Path) })
	return out, err
}

func trimmedCommand(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

// TestLiveServeBundleEvidenceIdentity records, once per native run and BEFORE
// TestMain removes its temporary home, who ran: the runner, the real go env, the
// observed Node and CLI versions, the actual launched flBinary (hash of that
// path, with its own build metadata) and the checked-out head and event. It
// needs the oracle like the other live tests; without the evidence directory it
// records nothing.
func TestLiveServeBundleEvidenceIdentity(t *testing.T) {
	oracle.Skip(t)
	dir := serveBundleEvidenceDir()
	if dir == "" {
		t.Logf("%s is not set: no native evidence is recorded by this run", serveBundleEvidenceEnv)
		return
	}
	node, cli := pinServeBundleOracle(t)
	nodeVersion, _, _ := runCLIHelpProcess(t, node, t.TempDir(), serveBundleEnv(), "--version")
	cliVersion, _, _ := runCLIHelpProcess(t, node, t.TempDir(), serveBundleEnv(), cli, "--version")

	id := serveBundleIdentity{
		Schema: serveBundleEvidenceSchema, RunnerOS: os.Getenv("RUNNER_OS"), RunnerArch: os.Getenv("RUNNER_ARCH"),
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, GoVersion: runtime.Version(),
		NodeVersion: strings.TrimSpace(nodeVersion), CLIVersion: strings.TrimSpace(cliVersion),
		Head: os.Getenv("FAULTLINE_EVIDENCE_HEAD"), Event: os.Getenv("FAULTLINE_EVIDENCE_EVENT"),
		EventHead: os.Getenv("FAULTLINE_EVIDENCE_EVENT_HEAD"), GithubSHA: os.Getenv("FAULTLINE_EVIDENCE_GITHUB_SHA"),
	}
	// The matrix leg's intended target is explicit workflow context, required and
	// never defaulted from the actual platform.
	intendedOS, intendedArch, err := evidenceTargetFromEnv()
	if err != nil {
		t.Fatalf("recording the identity needs the intended native target: %v", err)
	}
	id.IntendedGOOS, id.IntendedGOARCH = intendedOS, intendedArch
	for name, value := range map[string]*string{"GOOS": &id.GoEnvGOOS, "GOARCH": &id.GoEnvGOARCH, "CGO_ENABLED": &id.GoEnvCGO} {
		got, err := trimmedCommand("go", "env", name)
		if err != nil {
			t.Fatalf("go env %s: %v", name, err)
		}
		*value = got
	}
	flBytes, err := os.ReadFile(flBinary)
	if err != nil {
		t.Fatalf("reading the launched fl binary %s: %v", flBinary, err)
	}
	id.FlSHA256, id.FlBytes = sha256Of(flBytes), int64(len(flBytes))
	info, err := buildinfo.ReadFile(flBinary)
	if err != nil {
		t.Fatalf("reading build metadata of %s: %v", flBinary, err)
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "GOOS":
			id.FlBuildOS = setting.Value
		case "GOARCH":
			id.FlBuildArch = setting.Value
		case "CGO_ENABLED":
			id.FlBuildCGO = setting.Value
		}
	}
	if err := checkServeBundleIdentity(id, ""); err != nil {
		t.Fatalf("native identity is incomplete or inconsistent: %v", err)
	}
	if err := checkServeBundleTarget(id, intendedOS, intendedArch); err != nil {
		t.Fatalf("this run is not the intended native target: %v", err)
	}

	original, err := inventoryTree(serveBundleDir, evidenceOriginalCorpus)
	if err != nil {
		t.Fatal(err)
	}
	supplemental, err := inventoryTree(serveBundleBindingDir, evidenceSupplementalCorpus)
	if err != nil {
		t.Fatal(err)
	}
	inventory := struct {
		Schema string                     `json:"schema"`
		Files  []serveBundleInventoryFile `json:"files"`
	}{serveBundleEvidenceSchema, append(original, supplemental...)}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating the evidence directory: %v", err)
	}
	for name, value := range map[string]any{"identity.json": id, "inventory.json": inventory} {
		encoded, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := evidenceWrite(filepath.Join(dir, name), append(encoded, '\n')); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	t.Logf("native identity: %s/%s go %s node %s fl sha256 %s head %s", id.GOOS, id.GOARCH, id.GoVersion, id.NodeVersion, id.FlSHA256, id.Head)
}

var (
	evidenceHex40 = regexp.MustCompile(`^[0-9a-f]{40}$`)
	evidenceHex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// checkServeBundleIdentity requires every field, agreement between the runner,
// the toolchain's go env, the test process and the launched binary's own build
// metadata, the pinned Node, a cgo build on Linux, and (when given) the head
// the audit step observed.
func checkServeBundleIdentity(id serveBundleIdentity, wantHead string) error {
	osNames := map[string]string{"Linux": "linux", "macOS": "darwin", "Windows": "windows"}
	archNames := map[string]string{"X64": "amd64", "ARM64": "arm64"}
	switch {
	case id.Schema != serveBundleEvidenceSchema:
		return errors.New("wrong identity schema")
	case osNames[id.RunnerOS] == "" || archNames[id.RunnerArch] == "":
		return fmt.Errorf("unknown runner %q/%q", id.RunnerOS, id.RunnerArch)
	case osNames[id.RunnerOS] != id.GOOS || archNames[id.RunnerArch] != id.GOARCH:
		return fmt.Errorf("runner %s/%s does not match the test process %s/%s", id.RunnerOS, id.RunnerArch, id.GOOS, id.GOARCH)
	case id.GoEnvGOOS != id.GOOS || id.GoEnvGOARCH != id.GOARCH:
		return fmt.Errorf("go env %s/%s does not match the test process %s/%s", id.GoEnvGOOS, id.GoEnvGOARCH, id.GOOS, id.GOARCH)
	case id.FlBuildOS != id.GOOS || id.FlBuildArch != id.GOARCH:
		return fmt.Errorf("the launched fl was built for %s/%s, not %s/%s", id.FlBuildOS, id.FlBuildArch, id.GOOS, id.GOARCH)
	case id.GoVersion == "" || id.GoEnvCGO == "" || id.FlBuildCGO == "":
		return errors.New("missing Go version or cgo metadata")
	case id.NodeVersion != "v22.22.2" || id.CLIVersion != "FaultLine 0.1.4":
		return fmt.Errorf("observed Node %q and CLI %q are not the pinned v22.22.2 and FaultLine 0.1.4", id.NodeVersion, id.CLIVersion)
	case !evidenceHex64.MatchString(id.FlSHA256) || id.FlBytes <= 0:
		return errors.New("missing launched binary hash")
	case !evidenceHex40.MatchString(id.Head) || !evidenceHex40.MatchString(id.EventHead) || !evidenceHex40.MatchString(id.GithubSHA) || id.Event == "":
		return fmt.Errorf("missing checkout identity (head %q, event %q, event head %q, github sha %q)", id.Head, id.Event, id.EventHead, id.GithubSHA)
	case id.GOOS == "linux" && id.FlBuildCGO != "1":
		return fmt.Errorf("the Linux fl was built with CGO_ENABLED=%s, not 1", id.FlBuildCGO)
	case wantHead != "" && id.Head != wantHead:
		return fmt.Errorf("recorded head %s is not the observed head %s", id.Head, wantHead)
	}
	return nil
}

// ---- audit ----

type evidenceCase struct {
	Corpus, ID, Kind string
	Argv             []string
	Exit             int
	Stdout, Stderr   string // retained normalized process output
	PageSHA256       string // retained page, servers only

	// Provenance independently loaded from the corpus: the fixture, the base
	// root and retained attachment digests of the case, and (supplemental only)
	// the retained minimization input path. Empty/absent is a valid value.
	Fixture      string
	ExpectedRoot string
	Digests      map[string]string
	Input        string
}

type evidenceExpectation struct {
	Cases        []evidenceCase
	ErrorRows    int // original cli-error rows
	ServerRows   int // original cli-serve rows
	Supplemental int
	Head         string
	// TargetGOOS/TargetGOARCH are the independently supplied intended leg; both
	// are required and must be one of the six native targets.
	TargetGOOS, TargetGOARCH string
	// Inventory is the exact path/hash/byte set of both corpora as currently,
	// strictly loaded from disk; the recorded inventory must equal it.
	Inventory []serveBundleInventoryFile
}

// evidenceExpectationFor builds the expected set from the strictly loaded
// corpora; the row counts are the independently restated constants.
func evidenceExpectationFor(t *testing.T) evidenceExpectation {
	t.Helper()
	original := loadServeBundleCorpus(t)
	binding := loadServeBundleBinding(t, original)
	want := evidenceExpectation{ErrorRows: serveBundleCLIErrorRows, ServerRows: serveBundleCLIServerRows, Supplemental: evidenceSupplementalCases, Head: os.Getenv("FAULTLINE_EVIDENCE_HEAD")}
	goos, goarch, err := evidenceTargetFromEnv()
	if err != nil {
		t.Fatalf("the audit needs the intended native target: %v", err)
	}
	want.TargetGOOS, want.TargetGOARCH = goos, goarch
	for _, c := range original.Cases {
		if c.Kind != "cli-error" && c.Kind != "cli-serve" {
			continue
		}
		p := original.Goldens[c.ID].Process
		shell := serveBundleCaptureOf(c, "")
		ec := evidenceCase{Corpus: evidenceOriginalCorpus, ID: c.ID, Kind: c.Kind, Argv: c.Argv, Exit: *p.ExitCode, Stdout: p.Stdout, Stderr: p.Stderr,
			Fixture: c.Fixture, ExpectedRoot: shell.ExpectedRoot, Digests: shell.Digests}
		if c.Kind == "cli-serve" {
			page, err := original.Page(c.ID)
			if err != nil {
				t.Fatal(err)
			}
			ec.PageSHA256 = sha256Of(page)
		}
		want.Cases = append(want.Cases, ec)
	}
	for i, c := range binding.cases {
		p := binding.goldens[i].Process
		want.Cases = append(want.Cases, evidenceCase{Corpus: evidenceSupplementalCorpus, ID: c.ID, Kind: "cli-error", Argv: c.Argv, Exit: *p.ExitCode, Stdout: p.Stdout, Stderr: p.Stderr,
			Fixture: "coherent", ExpectedRoot: c.ExpectedRoot, Digests: map[string]string{"minimization": c.ExpectedMinimizationDigest}, Input: c.Input})
	}
	originalFiles, err := inventoryTree(serveBundleDir, evidenceOriginalCorpus)
	if err != nil {
		t.Fatal(err)
	}
	supplementalFiles, err := inventoryTree(serveBundleBindingDir, evidenceSupplementalCorpus)
	if err != nil {
		t.Fatal(err)
	}
	want.Inventory = append(originalFiles, supplementalFiles...)
	return want
}

// deriveEvidenceNormalized applies ONLY the declared substitutions to a raw
// stream, in the order the live comparisons apply them: the concrete server
// URL's port (stdout only, first occurrence), the case root, then the occupied
// port in 127.0.0.1:<port>. Nothing is inferred from the expected output.
func deriveEvidenceNormalized(raw string, n evidenceNormalization, stdout bool) string {
	text := raw
	if stdout && n.ServerURL != "" {
		text = strings.Replace(text, n.ServerURL, "http://127.0.0.1:{PORT}", 1)
	}
	if n.Root != "" {
		text = strings.ReplaceAll(text, n.Root, "{ROOT}")
	}
	if n.OccupiedPort != 0 {
		text = strings.ReplaceAll(text, "127.0.0.1:"+strconv.Itoa(n.OccupiedPort), "127.0.0.1:{PORT}")
	}
	return text
}

// checkEvidenceNormalization sanity-checks the recorded context: the root is
// absolute when present, the occupied port exists exactly for cases whose argv
// asks for one, and the server URL is a loopback URL with a valid port, only for
// servers.
func checkEvidenceNormalization(n evidenceNormalization, kind string, argv []string) error {
	if n.Root != "" && !filepath.IsAbs(n.Root) {
		return fmt.Errorf("normalization root %q is not absolute", n.Root)
	}
	wantsPort := slices.Contains(argv, "{PORT}")
	if (n.OccupiedPort != 0) != wantsPort || n.OccupiedPort < 0 || n.OccupiedPort > 65535 {
		return fmt.Errorf("occupied port %d does not match the argv (asks for one: %v)", n.OccupiedPort, wantsPort)
	}
	if n.ServerURL != "" {
		port, ok := strings.CutPrefix(n.ServerURL, "http://127.0.0.1:")
		number, err := strconv.Atoi(port)
		if kind != "cli-serve" || !ok || err != nil || number < 1 || number > 65535 || strconv.Itoa(number) != port {
			return fmt.Errorf("server URL %q is not a loopback URL of a server case", n.ServerURL)
		}
	}
	return nil
}

// evidenceWalk rejects a duplicate object member at any depth.
func evidenceWalk(dec *json.Decoder) error {
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
			if err := evidenceWalk(dec); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := evidenceWalk(dec); err != nil {
				return err
			}
		}
	}
	_, err = dec.Token()
	return err
}

// evidenceCheckType checks a generic JSON value against a Go type: every struct
// member present (none absent, none unknown), null only where the field is a
// pointer, and the JSON kind equal to the field's. Map keys are arbitrary (only
// the digest map uses a map); the value types are still checked.
func evidenceCheckType(value any, t reflect.Type, label string) error {
	if t.Kind() == reflect.Pointer {
		if value == nil {
			return nil
		}
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		members, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", label)
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
			if name != "" && name != "-" {
				fields[name] = t.Field(i).Type
			}
		}
		for name := range fields {
			if _, present := members[name]; !present {
				return fmt.Errorf("%s lacks the required member %q", label, name)
			}
		}
		for name, child := range members {
			ft, known := fields[name]
			if !known {
				return fmt.Errorf("%s has the unexpected member %q", label, name)
			}
			if err := evidenceCheckType(child, ft, label+"."+name); err != nil {
				return err
			}
		}
	case reflect.Map:
		members, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", label)
		}
		for name, child := range members {
			if err := evidenceCheckType(child, t.Elem(), label+"."+name); err != nil {
				return err
			}
		}
	case reflect.Slice:
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s must be an array", label)
		}
		for i, child := range items {
			if err := evidenceCheckType(child, t.Elem(), label+"["+strconv.Itoa(i)+"]"); err != nil {
				return err
			}
		}
	case reflect.String:
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s must be a string", label)
		}
	case reflect.Bool:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s must be a boolean", label)
		}
	case reflect.Int, reflect.Int64:
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("%s must be a number", label)
		}
	default:
		return fmt.Errorf("%s has the unsupported type %s", label, t)
	}
	return nil
}

// strictEvidenceDecode decodes one JSON file into v, rejecting a duplicate
// member at any depth, a missing or unknown member, a null where the field is
// not a pointer, a wrong kind, and any data after the value.
func strictEvidenceDecode(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fail := func(err error) error { return fmt.Errorf("%s: %w", path, err) }
	scan := json.NewDecoder(strings.NewReader(string(data)))
	if err := evidenceWalk(scan); err != nil {
		return fail(err)
	}
	if _, err := scan.Token(); err != io.EOF {
		return fail(errors.New("trailing data"))
	}
	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		return fail(err)
	}
	if err := evidenceCheckType(generic, reflect.TypeOf(v).Elem(), "$"); err != nil {
		return fail(err)
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fail(err)
	}
	return nil
}

// auditServeBundleEvidence validates a whole evidence directory against want.
func auditServeBundleEvidence(dir string, want evidenceExpectation) error {
	// 1. exactly the expected file set, no link or special entry.
	wantFiles := []string{"identity.json", "inventory.json"}
	original, supplemental := 0, 0
	seen := map[string]bool{}
	for _, c := range want.Cases {
		if seen[c.Corpus+"/"+c.ID] {
			return fmt.Errorf("expected set repeats %s/%s", c.Corpus, c.ID)
		}
		seen[c.Corpus+"/"+c.ID] = true
		if c.Corpus == evidenceOriginalCorpus {
			original++
		} else {
			supplemental++
		}
		for _, side := range []string{"ts", "go"} {
			base := "captures/" + c.Corpus + "/" + c.ID + "/" + side + "/"
			wantFiles = append(wantFiles, base+"record.json", base+"stdout", base+"stderr")
			if c.Kind == "cli-serve" {
				wantFiles = append(wantFiles, base+"page.html")
			}
		}
	}
	if original != want.ErrorRows+want.ServerRows || supplemental != want.Supplemental {
		return fmt.Errorf("expected set holds %d original and %d supplemental cases, want %d and %d", original, supplemental, want.ErrorRows+want.ServerRows, want.Supplemental)
	}
	var present []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is a link or special file", path)
		}
		rel, _ := filepath.Rel(dir, path)
		present = append(present, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return err
	}
	slices.Sort(present)
	slices.Sort(wantFiles)
	if !slices.Equal(present, wantFiles) {
		var missing, extra []string
		for _, f := range wantFiles {
			if !slices.Contains(present, f) {
				missing = append(missing, f)
			}
		}
		for _, f := range present {
			if !slices.Contains(wantFiles, f) {
				extra = append(extra, f)
			}
		}
		return fmt.Errorf("evidence files differ: missing %v, unexpected %v", missing, extra)
	}

	// 2. native identity.
	var id serveBundleIdentity
	if err := strictEvidenceDecode(filepath.Join(dir, "identity.json"), &id); err != nil {
		return err
	}
	if err := checkServeBundleIdentity(id, want.Head); err != nil {
		return err
	}
	if err := checkServeBundleTarget(id, want.TargetGOOS, want.TargetGOARCH); err != nil {
		return err
	}

	// 3. every record against its files and the retained expectation.
	byID := map[string]map[string]serveBundleEvidenceRecord{}
	for _, c := range want.Cases {
		for _, side := range []string{"ts", "go"} {
			base := filepath.Join(dir, "captures", c.Corpus, c.ID, side)
			var r serveBundleEvidenceRecord
			if err := strictEvidenceDecode(filepath.Join(base, "record.json"), &r); err != nil {
				return err
			}
			label := c.Corpus + "/" + c.ID + "/" + side
			if r.Schema != serveBundleEvidenceSchema || r.Corpus != c.Corpus || r.ID != c.ID || r.Kind != c.Kind || r.Side != side || !slices.Equal(r.Argv, c.Argv) {
				return fmt.Errorf("%s: record identity, kind or argv differs from the corpus", label)
			}
			if r.Fixture != c.Fixture || r.ExpectedRoot != c.ExpectedRoot || r.Input != c.Input || !maps.Equal(r.Digests, c.Digests) {
				return fmt.Errorf("%s: recorded fixture, expected root, digests or input differ from the corpus", label)
			}
			if err := checkEvidenceNormalization(r.Normalization, c.Kind, c.Argv); err != nil {
				return fmt.Errorf("%s: %w", label, err)
			}
			stdout, err := os.ReadFile(filepath.Join(base, "stdout"))
			if err != nil {
				return err
			}
			stderr, err := os.ReadFile(filepath.Join(base, "stderr"))
			if err != nil {
				return err
			}
			if sha256Of(stdout) != r.StdoutSHA256 || len(stdout) != r.StdoutBytes || sha256Of(stderr) != r.StderrSHA256 || len(stderr) != r.StderrBytes {
				return fmt.Errorf("%s: raw streams do not match the record", label)
			}
			if r.ExitCode == nil || *r.ExitCode != c.Exit || r.Signal != nil {
				return fmt.Errorf("%s: exit %v signal %v, want completed exit %d", label, r.ExitCode, r.Signal, c.Exit)
			}
			if c.Kind == "cli-serve" {
				page, err := os.ReadFile(filepath.Join(base, "page.html"))
				if err != nil {
					return err
				}
				if sha256Of(page) != r.PageSHA256 || len(page) != r.PageBytes || r.PageSHA256 != c.PageSHA256 {
					return fmt.Errorf("%s: served page is not the retained frozen page", label)
				}
			} else if r.PageSHA256 != "" {
				return fmt.Errorf("%s: a refusal records a page", label)
			}
			// The recorded normalized text must be what the declared substitutions
			// make of the raw bytes, so a coordinated raw edit cannot hide behind
			// untouched normalized fields.
			if r.NormalizedStdout != deriveEvidenceNormalized(string(stdout), r.Normalization, true) ||
				r.NormalizedStderr != deriveEvidenceNormalized(string(stderr), r.Normalization, false) {
				return fmt.Errorf("%s: the normalized streams are not the declared substitutions of the raw streams", label)
			}
			// Windows' refusal prose is not Linux's: the live test only checks the
			// retained stream text elsewhere (exit codes are always compared).
			if id.GOOS != "windows" || c.Kind == "cli-serve" {
				if r.NormalizedStdout != c.Stdout || r.NormalizedStderr != c.Stderr {
					return fmt.Errorf("%s: normalized streams differ from the retained capture", label)
				}
			}
			if byID[c.Corpus+"/"+c.ID] == nil {
				byID[c.Corpus+"/"+c.ID] = map[string]serveBundleEvidenceRecord{}
			}
			byID[c.Corpus+"/"+c.ID][side] = r
		}
		ts, goSide := byID[c.Corpus+"/"+c.ID]["ts"], byID[c.Corpus+"/"+c.ID]["go"]
		if ts.NormalizedStdout != goSide.NormalizedStdout || ts.NormalizedStderr != goSide.NormalizedStderr || *ts.ExitCode != *goSide.ExitCode || ts.PageSHA256 != goSide.PageSHA256 {
			return fmt.Errorf("%s/%s: the Go and TS captures differ", c.Corpus, c.ID)
		}
	}

	// 4. the inventory names the same corpora the cases came from.
	var inventory struct {
		Schema string                     `json:"schema"`
		Files  []serveBundleInventoryFile `json:"files"`
	}
	if err := strictEvidenceDecode(filepath.Join(dir, "inventory.json"), &inventory); err != nil {
		return err
	}
	if inventory.Schema != serveBundleEvidenceSchema || len(inventory.Files) == 0 {
		return errors.New("inventory.json is empty or has the wrong schema")
	}
	// Exactly the expected path/hash/byte set, in order: no missing, extra,
	// duplicate or edited row.
	if len(inventory.Files) != len(want.Inventory) {
		return fmt.Errorf("inventory lists %d files, want exactly %d", len(inventory.Files), len(want.Inventory))
	}
	for i, f := range inventory.Files {
		if f != want.Inventory[i] {
			return fmt.Errorf("inventory row %d is %+v, want %+v", i+1, f, want.Inventory[i])
		}
	}
	return nil
}

// TestServeBundleEvidenceAudit runs only in the native evidence step (with the
// audit and evidence variables set); elsewhere it records that it checked
// nothing. Its success marker is printed only after a complete audit.
func TestServeBundleEvidenceAudit(t *testing.T) {
	if os.Getenv(serveBundleEvidenceAuditEnv) != "1" {
		t.Logf("%s is not 1: no native evidence audit in this run", serveBundleEvidenceAuditEnv)
		return
	}
	dir := serveBundleEvidenceDir()
	if dir == "" {
		t.Fatalf("%s is set but %s is not", serveBundleEvidenceAuditEnv, serveBundleEvidenceEnv)
	}
	want := evidenceExpectationFor(t)
	if want.Head == "" {
		t.Fatal("FAULTLINE_EVIDENCE_HEAD is not set for the audit")
	}
	if err := auditServeBundleEvidence(dir, want); err != nil {
		t.Fatal(err)
	}
	t.Logf("SERVE-BUNDLE-EVIDENCE-AUDIT ok head=%s %s/%s records=%d original=%d supplemental=%d",
		want.Head, runtime.GOOS, runtime.GOARCH, 2*len(want.Cases), want.ErrorRows+want.ServerRows, want.Supplemental)
}

// ---- audit controls: synthetic fixtures for the validator only ----

func syntheticEvidence(t *testing.T) (string, evidenceExpectation) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "evidence")
	zero, one := 0, 1
	want := evidenceExpectation{ErrorRows: 1, ServerRows: 1, Supplemental: 1}
	page := []byte("<html>page</html>\n")
	cases := []struct {
		c    evidenceCase
		exit *int
	}{
		{evidenceCase{Corpus: evidenceOriginalCorpus, ID: "cli-error-x", Kind: "cli-error", Argv: []string{"serve"}, Exit: 1, Stderr: "refused\n"}, &one},
		{evidenceCase{Corpus: evidenceOriginalCorpus, ID: "cli-serve-x", Kind: "cli-serve", Argv: []string{"serve", "--bundle"}, Exit: 0, Stdout: "banner\n", PageSHA256: sha256Of(page)}, &zero},
		{evidenceCase{Corpus: evidenceSupplementalCorpus, ID: "binding-x", Kind: "cli-error", Argv: []string{"serve", "--port", "abc"}, Exit: 1, Stderr: "binding\n"}, &one},
	}
	for _, k := range cases {
		want.Cases = append(want.Cases, k.c)
		for _, side := range []string{"TS", "Go"} {
			capture := serveBundleCapture{Corpus: k.c.Corpus, ID: k.c.ID, Kind: k.c.Kind, Side: side, Argv: k.c.Argv, Exit: k.exit,
				Stdout: k.c.Stdout, Stderr: k.c.Stderr, NormStdout: k.c.Stdout, NormStderr: k.c.Stderr}
			if k.c.Kind == "cli-serve" {
				capture.Page = page
			}
			if err := writeServeBundleCapture(dir, capture); err != nil {
				t.Fatal(err)
			}
		}
	}
	head := strings.Repeat("a", 40)
	want.Head = head
	// The synthetic leg is independently Linux/AMD64, whatever the CI context.
	want.TargetGOOS, want.TargetGOARCH = "linux", "amd64"
	id := serveBundleIdentity{
		IntendedGOOS: "linux", IntendedGOARCH: "amd64",
		Schema: serveBundleEvidenceSchema, RunnerOS: "Linux", RunnerArch: "X64", GOOS: "linux", GOARCH: "amd64", GoVersion: "go1.27",
		GoEnvGOOS: "linux", GoEnvGOARCH: "amd64", GoEnvCGO: "1", NodeVersion: "v22.22.2", CLIVersion: "FaultLine 0.1.4",
		FlSHA256: strings.Repeat("b", 64), FlBytes: 10, FlBuildOS: "linux", FlBuildArch: "amd64", FlBuildCGO: "1",
		Head: head, Event: "push", EventHead: head, GithubSHA: head,
	}
	// The synthetic expectation carries its own independent inventory; the
	// recorded file below is the compact serialization of the same four rows.
	want.Inventory = []serveBundleInventoryFile{
		{Path: "serve-bundle/cases.jsonl", Sha256: strings.Repeat("c", 64), Bytes: 1},
		{Path: "serve-bundle/golden.jsonl", Sha256: strings.Repeat("c", 64), Bytes: 1},
		{Path: "serve-bundle-binding/cases.jsonl", Sha256: strings.Repeat("c", 64), Bytes: 1},
		{Path: "serve-bundle-binding/golden.jsonl", Sha256: strings.Repeat("c", 64), Bytes: 1},
	}
	inventory := `{"schema":"` + serveBundleEvidenceSchema + `","files":[{"path":"serve-bundle/cases.jsonl","sha256":"` + strings.Repeat("c", 64) + `","bytes":1},` +
		`{"path":"serve-bundle/golden.jsonl","sha256":"` + strings.Repeat("c", 64) + `","bytes":1},{"path":"serve-bundle-binding/cases.jsonl","sha256":"` + strings.Repeat("c", 64) + `","bytes":1},` +
		`{"path":"serve-bundle-binding/golden.jsonl","sha256":"` + strings.Repeat("c", 64) + `","bytes":1}]}`
	encoded, _ := json.Marshal(id)
	for name, data := range map[string][]byte{"identity.json": encoded, "inventory.json": []byte(inventory)} {
		if err := evidenceWrite(filepath.Join(dir, name), data); err != nil {
			t.Fatal(err)
		}
	}
	return dir, want
}

// Catches an audit that accepts incomplete, duplicated, extra, edited or
// unidentified evidence; the valid control proves each rejection comes from its
// mutation.
func TestServeBundleEvidenceAuditControls(t *testing.T) {
	record := func(dir string) string {
		return filepath.Join(dir, "captures", evidenceOriginalCorpus, "cli-error-x", "ts")
	}
	cases := []struct {
		name   string
		mutate func(t *testing.T, dir string, want *evidenceExpectation)
		reject bool
	}{
		{"valid control", func(t *testing.T, dir string, want *evidenceExpectation) {}, false},
		{"missing side", func(t *testing.T, dir string, want *evidenceExpectation) {
			if err := os.RemoveAll(filepath.Join(dir, "captures", evidenceSupplementalCorpus, "binding-x", "go")); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"extra file", func(t *testing.T, dir string, want *evidenceExpectation) {
			if err := os.WriteFile(filepath.Join(record(dir), "extra"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"tampered raw stream", func(t *testing.T, dir string, want *evidenceExpectation) {
			if err := os.WriteFile(filepath.Join(record(dir), "stderr"), []byte("other\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"unexpected extra case in the corpus set", func(t *testing.T, dir string, want *evidenceExpectation) {
			want.Cases = append(want.Cases, evidenceCase{Corpus: evidenceOriginalCorpus, ID: "cli-error-y", Kind: "cli-error", Argv: []string{"serve"}, Exit: 1})
			want.ErrorRows++
		}, true},
		{"duplicate expected case", func(t *testing.T, dir string, want *evidenceExpectation) {
			want.Cases = append(want.Cases, want.Cases[0])
			want.ErrorRows++
		}, true},
		{"retained exit differs", func(t *testing.T, dir string, want *evidenceExpectation) { want.Cases[0].Exit = 2 }, true},
		{"retained stream differs", func(t *testing.T, dir string, want *evidenceExpectation) { want.Cases[0].Stderr = "different\n" }, true},
		{"retained page differs", func(t *testing.T, dir string, want *evidenceExpectation) {
			want.Cases[1].PageSHA256 = strings.Repeat("d", 64)
		}, true},
		{"wrong observed head", func(t *testing.T, dir string, want *evidenceExpectation) { want.Head = strings.Repeat("e", 40) }, true},
		{"missing identity", func(t *testing.T, dir string, want *evidenceExpectation) {
			if err := os.Remove(filepath.Join(dir, "identity.json")); err != nil {
				t.Fatal(err)
			}
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, want := syntheticEvidence(t)
			c.mutate(t, dir, &want)
			err := auditServeBundleEvidence(dir, want)
			switch {
			case c.reject && err == nil:
				t.Fatal("audit accepted the mutated evidence")
			case !c.reject && err != nil:
				t.Fatalf("audit rejected the valid synthetic evidence: %v", err)
			}
		})
	}
	t.Run("a second write of the same capture is refused", func(t *testing.T) {
		dir, _ := syntheticEvidence(t)
		err := writeServeBundleCapture(dir, serveBundleCapture{Corpus: evidenceOriginalCorpus, ID: "cli-error-x", Kind: "cli-error", Side: "TS"})
		if err == nil {
			t.Fatal("an existing capture was overwritten")
		}
	})
	t.Run("identity must agree with the native run", func(t *testing.T) {
		_, want := syntheticEvidence(t)
		good := serveBundleIdentity{
			Schema: serveBundleEvidenceSchema, RunnerOS: "Linux", RunnerArch: "ARM64", GOOS: "linux", GOARCH: "arm64", GoVersion: "go1.27",
			GoEnvGOOS: "linux", GoEnvGOARCH: "arm64", GoEnvCGO: "1", NodeVersion: "v22.22.2", CLIVersion: "FaultLine 0.1.4",
			FlSHA256: strings.Repeat("b", 64), FlBytes: 1, FlBuildOS: "linux", FlBuildArch: "arm64", FlBuildCGO: "1",
			Head: want.Head, Event: "pull_request", EventHead: strings.Repeat("c", 40), GithubSHA: want.Head,
		}
		if err := checkServeBundleIdentity(good, want.Head); err != nil {
			t.Fatalf("valid identity rejected: %v", err)
		}
		mutations := map[string]func(*serveBundleIdentity){
			"cross-built binary":    func(i *serveBundleIdentity) { i.FlBuildArch = "amd64" },
			"wrong go env":          func(i *serveBundleIdentity) { i.GoEnvGOARCH = "amd64" },
			"runner mismatch":       func(i *serveBundleIdentity) { i.RunnerArch = "X64" },
			"Linux without cgo":     func(i *serveBundleIdentity) { i.FlBuildCGO = "0" },
			"wrong Node":            func(i *serveBundleIdentity) { i.NodeVersion = "v22.23.0" },
			"missing binary hash":   func(i *serveBundleIdentity) { i.FlSHA256 = "" },
			"missing event head":    func(i *serveBundleIdentity) { i.EventHead = "" },
			"head is not 40 hex":    func(i *serveBundleIdentity) { i.Head = "main" },
			"unknown runner system": func(i *serveBundleIdentity) { i.RunnerOS = "Plan9" },
		}
		for name, mutate := range mutations {
			bad := good
			mutate(&bad)
			if err := checkServeBundleIdentity(bad, want.Head); err == nil {
				t.Errorf("%s: accepted", name)
			}
		}
	})
}
