package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/bundle/demo"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/demofixture"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nativefixture"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// The native RERUN executor re-executes this test binary as its private child.
func TestMain(m *testing.M) {
	if code, handled := nativefixture.DispatchChild(os.Args[1:], os.Getenv(nativefixture.ChildEnvName), os.Stdout, os.Stderr); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

const (
	replayRetainedRoot = "sha256:fe85050935c1452f15c7cd8e3a17270ea3f541ae764aa6f4bed8cf4ed97554aa"
	demoNext           = "\nNext: pnpm fl help\n"
)

// emitDiagnostic logs the exact error string as one JSON string, the
// NATIVE-DIAGNOSTIC contract; %q would use Go escapes that JSON does not share.
func emitDiagnostic(t *testing.T, message string) {
	t.Helper()
	observed, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("NATIVE-DIAGNOSTIC %s", observed)
}

func makeDirectoryLink(t *testing.T, target, link string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		targetAbs, err := filepath.Abs(target)
		if err != nil {
			t.Fatal(err)
		}
		linkAbs, err := filepath.Abs(link)
		if err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", linkAbs, targetAbs).CombinedOutput(); err != nil {
			t.Fatalf("mklink /J: %v %s", err, out)
		}
		return
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func isSpecialLink(info fs.FileInfo) bool {
	if runtime.GOOS == "windows" {
		return !info.IsDir() && !info.Mode().IsRegular()
	}
	return info.Mode()&fs.ModeSymlink != 0
}

func makeSpecialFile(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		listener, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		listener.(*net.UnixListener).SetUnlinkOnClose(false)
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}
	if out, err := exec.Command("mkfifo", path).CombinedOutput(); err != nil {
		t.Fatalf("mkfifo: %v %s", err, out)
	}
}

func demoFail(message string) string { return "FaultLine error: " + message + demoNext }

func isolatedEmpty(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

func requireEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("working directory not empty: %v %v", entries, err)
	}
}

func TestJudgeDemoValidation(t *testing.T) {
	proposal := jsjson.StringifyIndent(demofixture.NativeWitnessProposal(), "  ") + "\n"
	combined := "Native proposal inspection cannot be combined with execution or output options."
	server := "not yet ported: judge-demo server"
	needsRerun := "--approve-native requires --rerun-all."
	needsApproval := "Native RERUN requires --approve-native <proposal-digest>."
	missing := "Missing required option: --approve-native"
	rejected := `native analysis APPROVAL (invocation 0, state ""): approval must match the proposal and record a canonical UTC millisecond timestamp no later than execution entry`
	digest := demofixture.NativeWitnessProposal().Get("digest").Str()
	cases := []struct {
		name   string
		args   []string
		stdout string
		stderr string
		code   int
	}{
		{"inspection", []string{"judge-demo", "--native-proposal"}, proposal, "", 0},
		{"inspection port ignored", []string{"judge-demo", "--native-proposal", "--port", "9"}, proposal, "", 0},
		{"inspect+rerun", []string{"judge-demo", "--native-proposal", "--rerun-all"}, "", demoFail(combined), 1},
		{"inspect+export", []string{"judge-demo", "--native-proposal", "--export-only"}, "", demoFail(combined), 1},
		{"inspect+approve", []string{"judge-demo", "--native-proposal", "--approve-native"}, "", demoFail(combined), 1},
		{"inspect+output", []string{"judge-demo", "--native-proposal", "--output"}, "", demoFail(combined), 1},
		{"server", []string{"judge-demo"}, "", demoFail(server), 1},
		{"server with rerun", []string{"judge-demo", "--rerun-all"}, "", demoFail(server), 1},
		{"server with approval", []string{"judge-demo", "--approve-native", "sha256:x"}, "", demoFail(server), 1},
		{"replay approval misuse", []string{"judge-demo", "--export-only", "--approve-native", "sha256:any-value"}, "", demoFail(needsRerun), 1},
		{"replay trailing approval misuse", []string{"judge-demo", "--export-only", "--approve-native"}, "", demoFail(needsRerun), 1},
		{"rerun absent approval", []string{"judge-demo", "--export-only", "--rerun-all"}, "", demoFail(needsApproval), 1},
		{"rerun trailing approval", []string{"judge-demo", "--export-only", "--rerun-all", "--approve-native"}, "", demoFail(missing), 1},
		{"rerun empty approval", []string{"judge-demo", "--export-only", "--rerun-all", "--approve-native", ""}, "", demoFail(missing), 1},
		{"rerun flag-like approval", []string{"judge-demo", "--export-only", "--rerun-all", "--approve-native", "--output"}, "", demoFail(missing), 1},
		{"inspection ignores unrelated tokens", []string{"judge-demo", "stray", "--bogus", "--native-proposal", "--port", "9"}, proposal, "", 0},
		{"rerun with replay still needs approval", []string{"judge-demo", "--export-only", "--replay", "--rerun-all"}, "", demoFail(needsApproval), 1},
		{"rerun with replay flag-like approval", []string{"judge-demo", "--replay", "--export-only", "--rerun-all", "--approve-native", "--replay"}, "", demoFail(missing), 1},
		{"duplicate approval first wrong wins", []string{"judge-demo", "--export-only", "--rerun-all", "--approve-native", "sha256:" + strings.Repeat("0", 64), "--approve-native", digest}, "", demoFail(rejected), 1},
		{"duplicate approval first empty wins", []string{"judge-demo", "--export-only", "--rerun-all", "--approve-native", "", "--approve-native", digest}, "", demoFail(missing), 1},
		{"duplicate approval first flag-like wins", []string{"judge-demo", "--export-only", "--rerun-all", "--approve-native", "--x", "--approve-native", digest}, "", demoFail(missing), 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := isolatedEmpty(t)
			var out, errb bytes.Buffer
			code := Run(c.args, &out, &errb)
			if out.String() != c.stdout || errb.String() != c.stderr || code != c.code {
				t.Fatalf("stdout=%q stderr=%q exit=%d", out.String(), errb.String(), code)
			}
			requireEmptyDir(t, dir)
		})
	}
}

// judgeDemoDiagnosticCase is one refusal: the complete result is asserted before
// the diagnostic is emitted, and nothing may be left in the working directory.
func TestJudgeDemoNativeDiagnostics(t *testing.T) {
	proposal := "Native proposal inspection cannot be combined with execution or output options."
	server := "not yet ported: judge-demo server"
	needsRerun := "--approve-native requires --rerun-all."
	needsApproval := "Native RERUN requires --approve-native <proposal-digest>."
	missing := "Missing required option: --approve-native"
	rejected := `native analysis APPROVAL (invocation 0, state ""): approval must match the proposal and record a canonical UTC millisecond timestamp no later than execution entry`
	cases := []struct {
		name, message string
		args          []string
	}{
		{"inspect-rerun", proposal, []string{"judge-demo", "--native-proposal", "--rerun-all"}},
		{"inspect-export", proposal, []string{"judge-demo", "--native-proposal", "--export-only"}},
		{"inspect-approve", proposal, []string{"judge-demo", "--native-proposal", "--approve-native"}},
		{"inspect-output", proposal, []string{"judge-demo", "--native-proposal", "--output"}},
		{"server", server, []string{"judge-demo"}},
		{"server-rerun", server, []string{"judge-demo", "--rerun-all"}},
		{"server-approval", server, []string{"judge-demo", "--approve-native", "sha256:x"}},
		{"replay-approval", needsRerun, []string{"judge-demo", "--export-only", "--approve-native", "sha256:any-value"}},
		{"replay-trailing-approval", needsRerun, []string{"judge-demo", "--export-only", "--approve-native"}},
		{"rerun-absent-approval", needsApproval, []string{"judge-demo", "--export-only", "--rerun-all"}},
		{"rerun-trailing-approval", missing, []string{"judge-demo", "--export-only", "--rerun-all", "--approve-native"}},
		{"rerun-empty-approval", missing, []string{"judge-demo", "--export-only", "--rerun-all", "--approve-native", ""}},
		{"rerun-flag-like-approval", missing, []string{"judge-demo", "--export-only", "--rerun-all", "--approve-native", "--output"}},
		{"rerun-unapproved-any-value", rejected, []string{"judge-demo", "--export-only", "--rerun-all", "--approve-native", "sha256:any-value"}},
		{"rerun-unapproved-zero-digest", rejected, []string{"judge-demo", "--export-only", "--rerun-all", "--approve-native", "sha256:" + strings.Repeat("0", 64)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := isolatedEmpty(t)
			var out, errb bytes.Buffer
			code := Run(c.args, &out, &errb)
			want := demoFail(c.message)
			if out.Len() != 0 || errb.String() != want || code != 1 {
				t.Fatalf("stdout=%q stderr=%q exit=%d", out.String(), errb.String(), code)
			}
			requireEmptyDir(t, dir)
			emitDiagnostic(t, errb.String())
		})
	}
	t.Run("generated-bundle-invalid", func(t *testing.T) {
		written := demo.Written{Directory: t.TempDir()}
		verification := demo.Verify(written.Directory, "", false)
		if verification.Valid || len(verification.Errors) == 0 {
			t.Fatalf("an empty directory must be an invalid bundle: %+v", verification)
		}
		_, err := verifyGeneratedBundle(written)
		want := "Generated proof bundle failed integrity verification: " + strings.Join(verification.Errors, "; ")
		if err == nil || err.Error() != want {
			t.Fatalf("err=%v want=%q", err, want)
		}
		emitDiagnostic(t, err.Error())
	})
	t.Run("generated-bundle-root-mismatch", func(t *testing.T) {
		isolatedEmpty(t)
		directory := nodefs.Resolve(".faultline/bundles/mismatch")
		if err := os.MkdirAll(filepath.Dir(directory), 0o755); err != nil {
			t.Fatal(err)
		}
		written, err := demo.Write(directory, demofixture.CreateReplayAnalysis(), demo.WriteOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := verifyGeneratedBundle(written); err != nil {
			t.Fatalf("a genuine bundle must verify against its own root: %v", err)
		}
		written.RootDigest = "sha256:" + strings.Repeat("1", 64)
		_, err = verifyGeneratedBundle(written)
		want := "Generated proof bundle root digest differs from verified root"
		if err == nil || err.Error() != want {
			t.Fatalf("err=%v want=%q", err, want)
		}
		emitDiagnostic(t, err.Error())
	})
}

func readValue(t *testing.T, path string) jsjson.Value {
	t.Helper()
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsjson.Parse(string(text))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func sidecarOf(directory string) string { return nodefs.Resolve(directory) + ".native-execution.json" }

// tree is every entry below root keyed by slash path: a regular file by its
// bytes, a directory by a marker, anything else by its mode string.
func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.Mode().IsRegular():
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[filepath.ToSlash(rel)] = "file:" + string(body)
		case info.IsDir():
			out[filepath.ToSlash(rel)] = "dir"
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			out[filepath.ToSlash(rel)] = "link:" + target
		default:
			out[filepath.ToSlash(rel)] = "special:" + info.Mode().String()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameTree(t *testing.T, label string, got, want map[string]string) {
	t.Helper()
	for k, w := range want {
		if g, ok := got[k]; !ok {
			t.Errorf("%s: missing %s", label, k)
		} else if g != w {
			t.Errorf("%s: %s differs", label, k)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("%s: unexpected %s", label, k)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
}

// requireSidecarExcluded proves a sidecar is outside the bundle's integrity set:
// the bundle holds exactly its declared files plus hashes.txt and ROOT.sha256,
// neither of which mentions or hashes the sidecar.
func requireSidecarExcluded(t *testing.T, directory, sidecar string, checkedFiles int) {
	t.Helper()
	body, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	sum := fmt.Sprintf("%x", sha256.Sum256(body))
	hashes, err := os.ReadFile(filepath.Join(directory, "hashes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.ReadFile(filepath.Join(directory, "ROOT.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	for label, text := range map[string]string{"hashes.txt": string(hashes), "ROOT.sha256": string(root)} {
		if strings.Contains(text, "native-execution") || strings.Contains(text, filepath.Base(sidecar)) || strings.Contains(text, sum) {
			t.Fatalf("%s mentions the sidecar", label)
		}
	}
	lines := strings.Split(strings.TrimSuffix(string(hashes), "\n"), "\n")
	if len(lines) != checkedFiles {
		t.Fatalf("hashes.txt has %d lines for %d checked files", len(lines), checkedFiles)
	}
	physical := 0
	for name, kind := range tree(t, directory) {
		if kind != "dir" {
			physical++
			if strings.Contains(name, "native-execution") {
				t.Fatalf("sidecar inside bundle: %s", name)
			}
		}
	}
	if physical != checkedFiles+2 {
		t.Fatalf("bundle holds %d files, want %d declared plus hashes.txt and ROOT.sha256", physical, checkedFiles)
	}
}

func requireKeys(t *testing.T, label string, v jsjson.Value, want ...string) {
	t.Helper()
	if v.Kind() != jsjson.Object || !slices.Equal(v.Obj().Keys(), want) {
		t.Fatalf("%s keys: %v want %v", label, v.Obj().Keys(), want)
	}
}

func requireString(t *testing.T, label string, v jsjson.Value, want string) {
	t.Helper()
	if v.Kind() != jsjson.String || v.Str() != want {
		t.Fatalf("%s = %s want %q", label, jsjson.Stringify(v), want)
	}
}

func requireNull(t *testing.T, label string, v jsjson.Value) {
	t.Helper()
	if v.Kind() != jsjson.Null {
		t.Fatalf("%s = %s want null", label, jsjson.Stringify(v))
	}
}

func requireInt(t *testing.T, label string, v jsjson.Value, want int) {
	t.Helper()
	if v.Kind() != jsjson.Number || v.Num() != float64(want) {
		t.Fatalf("%s = %s want %d", label, jsjson.Stringify(v), want)
	}
}

func requireDigest(t *testing.T, label string, v jsjson.Value) {
	t.Helper()
	if v.Kind() != jsjson.String || !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(v.Str()) {
		t.Fatalf("%s is not a sha256 digest: %s", label, jsjson.Stringify(v))
	}
}

func requireStamp(t *testing.T, label string, v jsjson.Value) string {
	t.Helper()
	if v.Kind() != jsjson.String {
		t.Fatalf("%s is not a string: %s", label, jsjson.Stringify(v))
	}
	parsed, err := time.Parse("2006-01-02T15:04:05.000Z", v.Str())
	if err != nil || clock.ISO(parsed) != v.Str() {
		t.Fatalf("%s is not a canonical UTC millisecond timestamp: %q", label, v.Str())
	}
	return v.Str()
}

func requireDigestOf(t *testing.T, label string, v jsjson.Value, want string) {
	t.Helper()
	got, err := canonical.DigestJSON(v)
	if err != nil || got != want {
		t.Fatalf("%s digest = %q (%v) want %q", label, got, err, want)
	}
}

// requireBlob checks a RawBlob: exact keys, complete, base64 that decodes to
// the bytes whose SHA-256 it declares. It returns the decoded bytes.
func requireBlob(t *testing.T, label string, v jsjson.Value) []byte {
	t.Helper()
	requireKeys(t, label, v, "encoding", "data", "sha256", "complete")
	requireString(t, label+".encoding", v.Get("encoding"), "base64")
	if v.Get("complete").Kind() != jsjson.Bool || !v.Get("complete").Bool() {
		t.Fatalf("%s is not complete", label)
	}
	data, err := base64.StdEncoding.DecodeString(v.Get("data").Str())
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	requireString(t, label+".sha256", v.Get("sha256"), fmt.Sprintf("sha256:%x", sha256.Sum256(data)))
	return data
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(body))
}

func mutateDigest(digest string) string {
	last := digest[len(digest)-1]
	flipped := byte('a')
	if last == 'a' {
		flipped = 'b'
	}
	return digest[:len(digest)-1] + string(flipped)
}

// sidecarLinkage is the independent detectability check of the sidecar's two
// links: its analysisDigest must be the digest of the bundle's analysis.json
// and its bundleRootDigest the independently verified root.
func sidecarLinkage(sidecar, directory string) error {
	text, err := os.ReadFile(sidecar)
	if err != nil {
		return err
	}
	evidence, err := jsjson.Parse(string(text))
	if err != nil {
		return err
	}
	analysisText, err := os.ReadFile(filepath.Join(directory, "analysis.json"))
	if err != nil {
		return err
	}
	analysis, err := jsjson.Parse(string(analysisText))
	if err != nil {
		return err
	}
	digest, err := canonical.DigestJSON(analysis)
	if err != nil {
		return err
	}
	verification := demo.Verify(directory, "", false)
	if !verification.Valid || verification.RootDigest == nil {
		return errors.New("bundle does not verify")
	}
	if evidence.Get("analysisDigest").Str() != digest {
		return errors.New("analysisDigest does not match analysis.json")
	}
	if evidence.Get("bundleRootDigest").Str() != *verification.RootDigest {
		return errors.New("bundleRootDigest does not match the verified bundle root")
	}
	return nil
}

// requireLinkageDetectsMutation mutates each link in a test copy and requires
// the independent check to reject exactly that mismatch.
func requireLinkageDetectsMutation(t *testing.T, sidecar, directory string) {
	t.Helper()
	if err := sidecarLinkage(sidecar, directory); err != nil {
		t.Fatalf("genuine sidecar rejected: %v", err)
	}
	original, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"analysisDigest", "bundleRootDigest"} {
		evidence, err := jsjson.Parse(string(original))
		if err != nil {
			t.Fatal(err)
		}
		genuine := evidence.Get(field).Str()
		needle := `"` + field + `": "` + genuine + `"`
		if strings.Count(string(original), needle) != 1 {
			t.Fatalf("%s not found exactly once", field)
		}
		copyPath := filepath.Join(t.TempDir(), "mutated.native-execution.json")
		mutated := strings.Replace(string(original), needle, `"`+field+`": "`+mutateDigest(genuine)+`"`, 1)
		if err := os.WriteFile(copyPath, []byte(mutated), 0o600); err != nil {
			t.Fatal(err)
		}
		err = sidecarLinkage(copyPath, directory)
		if err == nil || !strings.HasPrefix(err.Error(), field+" does not match") {
			t.Fatalf("mutated %s: err=%v", field, err)
		}
	}
	after, err := os.ReadFile(sidecar)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatal("the genuine sidecar changed while mutated copies were checked")
	}
}

// nativeAudit is the expectation context of auditNativeSidecar. ExecutedWindow
// bounds every observed timestamp: from ISO(before the CLI call) to
// ISO(after it).
type nativeAudit struct {
	sidecar, directory  string
	executable          string
	goVersion           string
	proposalDigest      string
	notBefore, notAfter string
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// auditNativeSidecar independently validates the complete sidecar: its bytes,
// exact key sets at every level, the proposal/program/approval/environment
// observations, all 39 receipts with blobs, digests, ID preimages and the
// links to the analysis catalog. It returns the analysis digest.
func auditNativeSidecar(t *testing.T, a nativeAudit) string {
	t.Helper()
	text, err := os.ReadFile(a.sidecar)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := jsjson.Parse(string(text))
	if err != nil {
		t.Fatal(err)
	}
	if string(text) != jsjson.StringifyIndent(evidence, "  ")+"\n" {
		t.Fatal("sidecar is not two-space-indented JSON with one final LF")
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Lstat(a.sidecar); err != nil || info.Mode().Perm() != 0o600 || !info.Mode().IsRegular() {
			t.Fatalf("sidecar mode: %v %v", info, err)
		}
	}
	requireKeys(t, "sidecar", evidence, "schemaVersion", "analysisDigest", "bundleRootDigest", "provenance")
	requireString(t, "schemaVersion", evidence.Get("schemaVersion"), "faultline.native-demo-cli-evidence.v1")

	analysisText, err := os.ReadFile(filepath.Join(a.directory, "analysis.json"))
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := jsjson.Parse(string(analysisText))
	if err != nil {
		t.Fatal(err)
	}
	analysisDigest, err := canonical.DigestJSON(analysis)
	if err != nil {
		t.Fatal(err)
	}
	verification := demo.Verify(a.directory, "", false)
	if !verification.Valid || verification.RootDigest == nil {
		t.Fatalf("bundle invalid: %+v", verification)
	}
	requireString(t, "analysisDigest", evidence.Get("analysisDigest"), analysisDigest)
	requireString(t, "bundleRootDigest", evidence.Get("bundleRootDigest"), *verification.RootDigest)
	requireSidecarExcluded(t, a.directory, a.sidecar, verification.CheckedFiles)
	requireLinkageDetectsMutation(t, a.sidecar, a.directory)

	p := evidence.Get("provenance")
	requireKeys(t, "provenance", p, "schemaVersion", "status", "proposal", "program", "programDigest", "approval",
		"environmentPreimage", "environmentDigest", "nonce", "generatedAt", "invocations")
	requireString(t, "provenance.schemaVersion", p.Get("schemaVersion"), "faultline.native-demo-execution.v1")
	requireString(t, "provenance.status", p.Get("status"), "COMPLETE")
	proposal := demofixture.NativeWitnessProposal()
	if jsjson.Stringify(p.Get("proposal")) != jsjson.Stringify(proposal) {
		t.Fatal("provenance.proposal is not the current native proposal")
	}
	requireString(t, "proposal.digest", p.Get("proposal").Get("digest"), a.proposalDigest)
	if jsjson.Stringify(p.Get("program")) != jsjson.Stringify(nativefixture.Program()) {
		t.Fatal("provenance.program is not the reviewed program")
	}
	requireDigestOf(t, "program", p.Get("program"), p.Get("programDigest").Str())
	requireString(t, "programDigest", p.Get("programDigest"), proposal.Get("programDigest").Str())

	approval := p.Get("approval")
	requireKeys(t, "approval", approval, "proposalDigest", "approvedAt")
	requireString(t, "approval.proposalDigest", approval.Get("proposalDigest"), a.proposalDigest)
	approvedAt := requireStamp(t, "approvedAt", approval.Get("approvedAt"))
	generatedAt := requireStamp(t, "generatedAt", p.Get("generatedAt"))
	if approvedAt < a.notBefore || generatedAt > a.notAfter || approvedAt > generatedAt {
		t.Fatalf("approval/generation outside the call window: %s %s [%s,%s]", approvedAt, generatedAt, a.notBefore, a.notAfter)
	}

	env := p.Get("environmentPreimage")
	requireKeys(t, "environmentPreimage", env, "schemaVersion", "runner", "runtime", "goos", "goarch", "executableSHA256",
		"environment", "network", "credentials", "note")
	requireString(t, "env.schemaVersion", env.Get("schemaVersion"), "faultline.native-demo-environment.v1")
	requireString(t, "env.runner", env.Get("runner"), nativefixture.Runner)
	requireString(t, "env.runtime", env.Get("runtime"), a.goVersion)
	requireString(t, "env.goos", env.Get("goos"), runtime.GOOS)
	requireString(t, "env.goarch", env.Get("goarch"), runtime.GOARCH)
	requireString(t, "env.executableSHA256", env.Get("executableSHA256"), fileSHA256(t, a.executable))
	requireString(t, "env.network", env.Get("network"), "disabled")
	requireString(t, "env.credentials", env.Get("credentials"), "not-inherited")
	requireString(t, "env.note", env.Get("note"), "Reviewed fixed program behavior; not OS isolation or remote attestation.")
	wantEnv := []string{nativefixture.ChildEnvName + "=" + nativefixture.ChildEnvValue}
	if runtime.GOOS == "windows" {
		wantEnv = append(wantEnv, "SYSTEMROOT="+os.Getenv("SYSTEMROOT"))
	}
	var gotEnv []string
	for _, item := range env.Get("environment").Items() {
		gotEnv = append(gotEnv, item.Str())
	}
	slices.Sort(gotEnv)
	slices.Sort(wantEnv)
	if !slices.Equal(gotEnv, wantEnv) {
		t.Fatalf("environment = %q want %q", gotEnv, wantEnv)
	}
	requireDigestOf(t, "environmentPreimage", env, p.Get("environmentDigest").Str())
	nonce := p.Get("nonce").Str()
	if p.Get("nonce").Kind() != jsjson.String || !uuidPattern.MatchString(nonce) {
		t.Fatalf("nonce %q", nonce)
	}

	witness := analysis.Get("witness")
	requireString(t, "analysis.mode", analysis.Get("mode"), "RERUN")
	requireString(t, "analysis.generatedAt", analysis.Get("generatedAt"), generatedAt)
	requireString(t, "witness.approvedAt", witness.Get("approvedAt"), approvedAt)

	exe, err := filepath.EvalSymlinks(a.executable)
	if err != nil {
		t.Fatal(err)
	}
	catalog := analysis.Get("runCatalog").Items()
	receipts := p.Get("invocations").Items()
	if len(receipts) != 39 || len(catalog) != 39 {
		t.Fatalf("receipts=%d catalog=%d", len(receipts), len(catalog))
	}
	catalogByID := map[string]string{}
	for _, c := range catalog {
		catalogByID[c.Get("id").Str()] = jsjson.Stringify(c)
	}
	if len(catalogByID) != 39 {
		t.Fatal("catalog ids are not unique")
	}
	linked, previousExecuted := map[string]bool{}, a.notBefore
	for i, r := range receipts {
		label := fmt.Sprintf("receipt %d", i+1)
		requireKeys(t, label, r, "ordinal", "stateId", "executionAttempt", "config", "expectedPartial", "executable", "argv",
			"cwd", "environment", "startedAt", "executedAt", "durationMs", "status", "signal", "stdout", "stderr",
			"accepted", "runId", "runRecord", "runIdPreimage", "failureKind", "cleanupError")
		requireInt(t, label+".ordinal", r.Get("ordinal"), i+1)
		if attempt := r.Get("executionAttempt"); attempt.Kind() != jsjson.Number || attempt.Num() < 0 || attempt.Num() > 3 || attempt.Num() != float64(int(attempt.Num())) {
			t.Fatalf("%s attempt %s", label, jsjson.Stringify(attempt))
		}
		record := r.Get("runRecord")
		requireKeys(t, label+".runRecord", record, "id", "stateId", "witnessDigest", "environmentDigest", "verdict", "reasonCode",
			"stdout", "stderr", "durationMs", "executionKind", "executedAt")
		requireString(t, label+".stateId", r.Get("stateId"), record.Get("stateId").Str())
		requireString(t, label+".witnessDigest", record.Get("witnessDigest"), witness.Get("digest").Str())
		requireString(t, label+".environmentDigest", record.Get("environmentDigest"), p.Get("environmentDigest").Str())
		requireString(t, label+".executionKind", record.Get("executionKind"), "EXECUTED")
		if r.Get("accepted").Kind() != jsjson.Bool || !r.Get("accepted").Bool() {
			t.Fatalf("%s not accepted", label)
		}
		requireNull(t, label+".failureKind", r.Get("failureKind"))
		requireNull(t, label+".cleanupError", r.Get("cleanupError"))
		requireNull(t, label+".signal", r.Get("signal"))

		partial := r.Get("expectedPartial")
		if partial.Kind() != jsjson.Bool {
			t.Fatalf("%s expectedPartial %s", label, jsjson.Stringify(partial))
		}
		config := requireBlob(t, label+".config", r.Get("config"))
		if partial.Bool() == json.Valid(config) {
			t.Fatalf("%s: expectedPartial=%v but config validity is %v", label, partial.Bool(), json.Valid(config))
		}
		stdout := requireBlob(t, label+".stdout", r.Get("stdout"))
		stderr := requireBlob(t, label+".stderr", r.Get("stderr"))
		if string(stdout) != record.Get("stdout").Str() || string(stderr) != record.Get("stderr").Str() {
			t.Fatalf("%s: blobs differ from the run record", label)
		}
		wantStatus := map[string]int{"PASS": 0, "FAIL": 1, "ERROR": 3}[record.Get("verdict").Str()]
		if (record.Get("verdict").Str() == "ERROR") != partial.Bool() {
			t.Fatalf("%s: ERROR verdict and partial configuration disagree", label)
		}
		requireInt(t, label+".status", r.Get("status"), wantStatus)

		if r.Get("executable").Kind() != jsjson.String {
			t.Fatalf("%s executable", label)
		}
		got, err := filepath.EvalSymlinks(r.Get("executable").Str())
		if err != nil || got != exe {
			t.Fatalf("%s executable %q (%v) want %q", label, r.Get("executable").Str(), err, exe)
		}
		argv := r.Get("argv").Items()
		if len(argv) != 2 || argv[0].Str() != r.Get("executable").Str() || argv[1].Str() != nativefixture.ChildArg {
			t.Fatalf("%s argv %s", label, jsjson.Stringify(r.Get("argv")))
		}
		cwd := r.Get("cwd").Str()
		if !filepath.IsAbs(cwd) || !strings.HasPrefix(filepath.Base(cwd), "faultline-native-demo-") {
			t.Fatalf("%s cwd %q", label, cwd)
		}
		if _, err := os.Lstat(cwd); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s: child directory was not cleaned up: %v", label, err)
		}
		if jsjson.Stringify(r.Get("environment")) != jsjson.Stringify(env.Get("environment")) {
			t.Fatalf("%s environment differs from the preimage", label)
		}
		startedAt := requireStamp(t, label+".startedAt", r.Get("startedAt"))
		executedAt := requireStamp(t, label+".executedAt", r.Get("executedAt"))
		if startedAt < previousExecuted || startedAt > executedAt || executedAt > generatedAt || startedAt < approvedAt {
			t.Fatalf("%s timestamps out of order: %s %s (previous %s, generated %s)", label, startedAt, executedAt, previousExecuted, generatedAt)
		}
		previousExecuted = executedAt
		if d := r.Get("durationMs"); d.Kind() != jsjson.Number || d.Num() < 0 || d.Num() != float64(int(d.Num())) {
			t.Fatalf("%s durationMs %s", label, jsjson.Stringify(d))
		}
		requireString(t, label+".record.executedAt", record.Get("executedAt"), executedAt)
		if jsjson.Stringify(record.Get("durationMs")) != jsjson.Stringify(r.Get("durationMs")) {
			t.Fatalf("%s duration differs from the run record", label)
		}

		preimage := r.Get("runIdPreimage")
		requireKeys(t, label+".preimage", preimage, "schemaVersion", "nonce", "ordinal", "executionAttempt", "startedAt",
			"configDigest", "status", "signal", "record")
		requireString(t, label+".preimage.schemaVersion", preimage.Get("schemaVersion"), "faultline.native-demo-run-id.v1")
		requireString(t, label+".preimage.nonce", preimage.Get("nonce"), nonce)
		requireInt(t, label+".preimage.ordinal", preimage.Get("ordinal"), i+1)
		if jsjson.Stringify(preimage.Get("executionAttempt")) != jsjson.Stringify(r.Get("executionAttempt")) {
			t.Fatalf("%s preimage attempt", label)
		}
		requireString(t, label+".preimage.startedAt", preimage.Get("startedAt"), startedAt)
		requireString(t, label+".preimage.configDigest", preimage.Get("configDigest"), r.Get("config").Get("sha256").Str())
		requireInt(t, label+".preimage.status", preimage.Get("status"), wantStatus)
		requireNull(t, label+".preimage.signal", preimage.Get("signal"))
		body := jsjson.NewObj()
		for _, key := range record.Obj().Keys() {
			if key != "id" {
				body.Set(key, record.Get(key))
			}
		}
		if jsjson.Stringify(preimage.Get("record")) != jsjson.Stringify(jsjson.MakeObject(body)) {
			t.Fatalf("%s preimage record differs from the run record", label)
		}
		id := record.Get("id").Str()
		requireDigestOf(t, label+".preimage", preimage, id)
		requireString(t, label+".runId", r.Get("runId"), id)
		if linked[id] {
			t.Fatalf("%s duplicates id %s", label, id)
		}
		linked[id] = true
		if catalogByID[id] != jsjson.Stringify(record) {
			t.Fatalf("%s is not linked to its catalog entry", label)
		}
	}
	if len(linked) != 39 {
		t.Fatalf("linked %d of 39 catalog entries", len(linked))
	}
	return analysisDigest
}

// goVersionOf is the runtime version of an in-process native child.
func goVersionOf() string { return runtime.Version() }

func TestJudgeDemoExport(t *testing.T) {
	bundleRel := filepath.Join(".faultline", "bundles", "judge-demo")
	replayBase, err := filepath.Abs(filepath.Join("..", "..", "difftest", "testdata", "bases", "demo-replay"))
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	digest := demofixture.NativeWitnessProposal().Get("digest").Str()

	t.Run("replay", func(t *testing.T) {
		isolatedEmpty(t)
		directory := nodefs.Resolve(".faultline/bundles/judge-demo")
		if err := os.MkdirAll(filepath.Dir(directory), 0o755); err != nil {
			t.Fatal(err)
		}
		stale := sidecarOf(directory)
		if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
		// A trailing --output keeps the default; --port has no effect.
		var out, errb bytes.Buffer
		code := Run([]string{"judge-demo", "--export-only", "--port", "1", "--output"}, &out, &errb)
		if code != 0 || errb.Len() != 0 {
			t.Fatalf("exit=%d stderr=%q", code, errb.String())
		}
		verification := demo.Verify(directory, "", false)
		if !verification.Valid || verification.RootDigest == nil || *verification.RootDigest != replayRetainedRoot {
			t.Fatalf("%+v", verification)
		}
		want := "FaultLine cached replay investigation prepared.\n" +
			"Stable sample boundary: not certified from cached replay\n" +
			"Proof bundle self-consistency: VALID (" + strconv.Itoa(verification.CheckedFiles) + " declared files)\n" +
			"Bundle root: " + replayRetainedRoot + "\n" +
			"Proof bundle: " + bundleRel + "\n"
		if out.String() != want {
			t.Fatalf("stdout=%q want=%q", out.String(), want)
		}
		if _, err := os.Lstat(stale); !os.IsNotExist(err) {
			t.Fatalf("stale sidecar survived: %v", err)
		}
		// The complete retained 69-file REPLAY tree, byte for byte, and nothing else.
		wantTree := tree(t, replayBase)
		files := 0
		for _, kind := range wantTree {
			if kind != "dir" {
				files++
			}
		}
		if files != 69 {
			t.Fatalf("retained REPLAY base has %d files", files)
		}
		sameTree(t, "replay bundle", tree(t, directory), wantTree)
		siblings, err := os.ReadDir(filepath.Dir(directory))
		if err != nil || len(siblings) != 1 || siblings[0].Name() != "judge-demo" {
			t.Fatalf("bundle parent holds %v (%v), want only the bundle", siblings, err)
		}
	})

	nativeRuns := []struct {
		name   string
		output string // relative; empty selects the default
		args   func(output string) []string
	}{
		{"native", filepath.Join(".faultline", "bundles", "native"), func(output string) []string {
			return []string{"judge-demo", "--export-only", "--rerun-all", "--approve-native", digest, "--output", output}
		}},
		// The default output, --replay beside --rerun-all (RERUN still wins), and
		// an ignored port and stray tokens.
		{"native-default-replay-precedence", "", func(string) []string {
			return []string{"judge-demo", "stray", "--replay", "--export-only", "--bogus", "--port", "9", "--rerun-all", "--approve-native", digest}
		}},
		// A nested output, a duplicate approval whose first occurrence wins, and
		// a stray token.
		{"native-nested-duplicate-approval", filepath.Join(".faultline", "bundles", "outer", "inner", "native"), func(output string) []string {
			return []string{"judge-demo", "--approve-native", digest, "--rerun-all", "junk", "--approve-native", "sha256:" + strings.Repeat("0", 64), "--output", output, "--export-only"}
		}},
	}
	for _, c := range nativeRuns {
		t.Run(c.name, func(t *testing.T) {
			isolatedEmpty(t)
			output := c.output
			if output == "" {
				output = bundleRel
			}
			before := clock.ISO(clock.Now())
			var out, errb bytes.Buffer
			code := Run(c.args(c.output), &out, &errb)
			after := clock.ISO(clock.Now())
			if code != 0 || errb.Len() != 0 {
				t.Fatalf("exit=%d stderr=%q", code, errb.String())
			}
			directory := nodefs.Resolve(output)
			verification := demo.Verify(directory, "", false)
			if !verification.Valid || verification.RootDigest == nil {
				t.Fatalf("%+v", verification)
			}
			analysis := readValue(t, filepath.Join(directory, "analysis.json"))
			var boundaryBefore, boundaryAfter string
			for _, transition := range analysis.Get("transitions").Items() {
				if transition.Get("kind").Str() == "PASS_TO_FAIL" && transition.Get("stable").Bool() {
					boundaryBefore, boundaryAfter = transition.Get("beforeStateId").Str(), transition.Get("afterStateId").Str()
					break
				}
			}
			if boundaryBefore == "" || boundaryAfter == "" {
				t.Fatal("no stable PASS_TO_FAIL transition")
			}
			sidecar := sidecarOf(directory)
			want := "FaultLine executed investigation prepared.\n" +
				"Stable sample boundary: " + boundaryBefore + " → " + boundaryAfter + "\n" +
				"Proof bundle self-consistency: VALID (" + strconv.Itoa(verification.CheckedFiles) + " declared files)\n" +
				"Bundle root: " + *verification.RootDigest + "\n" +
				"Proof bundle: " + output + "\n" +
				"Native execution evidence: " + sidecar + "\n"
			if out.String() != want {
				t.Fatalf("stdout=%q want=%q", out.String(), want)
			}
			siblings, err := os.ReadDir(filepath.Dir(directory))
			var names []string
			for _, e := range siblings {
				names = append(names, e.Name())
			}
			if base := filepath.Base(directory); err != nil || !slices.Equal(names, []string{base, base + ".native-execution.json"}) {
				t.Fatalf("bundle parent holds %v (%v)", names, err)
			}
			analysisDigest := auditNativeSidecar(t, nativeAudit{
				sidecar: sidecar, directory: directory, executable: executable, goVersion: goVersionOf(),
				proposalDigest: digest, notBefore: before, notAfter: after,
			})
			evidence := readValue(t, sidecar)
			t.Logf("NATIVE-EVIDENCE sidecar=%s root=%s analysisDigest=%s checkedFiles=%d receipts=%d approvedAt=%s boundary=%s->%s",
				sidecar, *verification.RootDigest, analysisDigest, verification.CheckedFiles, len(evidence.Get("provenance").Get("invocations").Items()),
				evidence.Get("provenance").Get("approval").Get("approvedAt").Str(), boundaryBefore, boundaryAfter)
		})
	}
}

// Invalid and special existing outputs fail with the exact writer diagnostic,
// no stdout, and no change to the working tree (including a stale sidecar).
func TestJudgeDemoOutputRefusal(t *testing.T) {
	digest := demofixture.NativeWitnessProposal().Get("digest").Str()
	replay := func(output string) []string { return []string{"judge-demo", "--export-only", "--output", output} }
	native := func(output string) []string {
		return []string{"judge-demo", "--export-only", "--rerun-all", "--approve-native", digest, "--output", output}
	}
	for _, c := range []struct {
		name    string
		args    func(string) []string
		prepare func(t *testing.T, path string)
		message string
	}{
		{"replay-unverified-directory", replay, func(t *testing.T, path string) {
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(path, "notes.txt"), []byte("mine"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "Proof bundle output can only replace a verified FaultLine bundle: "},
		{"native-unverified-directory", native, func(t *testing.T, path string) {
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(path, "notes.txt"), []byte("mine"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "Proof bundle output can only replace a verified FaultLine bundle: "},
		{"replay-regular-file", replay, func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("mine"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "Proof bundle output must be a directory: "},
		{"replay-symlink", replay, func(t *testing.T, path string) {
			if err := os.Mkdir(path+"-target", 0o755); err != nil {
				t.Fatal(err)
			}
			makeDirectoryLink(t, path+"-target", path)
		}, "Proof bundle output cannot traverse a symbolic link: "},
		{"replay-fifo", replay, func(t *testing.T, path string) {
			makeSpecialFile(t, path)
		}, "Proof bundle output must be a directory: "},
		{"native-fifo", native, func(t *testing.T, path string) {
			makeSpecialFile(t, path)
		}, "Proof bundle output must be a directory: "},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := isolatedEmpty(t)
			relative := filepath.Join(".faultline", "bundles", "target")
			path := nodefs.Resolve(relative)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			c.prepare(t, path)
			stale := sidecarOf(path)
			if err := os.WriteFile(stale, []byte("stale evidence"), 0o600); err != nil {
				t.Fatal(err)
			}
			before := tree(t, dir)
			var out, errb bytes.Buffer
			code := Run(c.args(relative), &out, &errb)
			want := demoFail(c.message + path)
			if code != 1 || out.Len() != 0 || errb.String() != want {
				t.Fatalf("stdout=%q stderr=%q want=%q exit=%d", out.String(), errb.String(), want, code)
			}
			sameTree(t, "working tree", tree(t, dir), before)
		})
	}
}

func TestJudgeDemoSidecarFailure(t *testing.T) {
	digest := demofixture.NativeWitnessProposal().Get("digest").Str()
	for _, c := range []struct {
		name    string
		args    []string
		symlink bool
	}{
		{"replay-directory", []string{"judge-demo", "--export-only", "--output", ".faultline/bundles/bundle"}, false},
		{"native-directory", []string{"judge-demo", "--export-only", "--rerun-all", "--approve-native", digest, "--output", ".faultline/bundles/bundle"}, false},
		{"replay-symlink", []string{"judge-demo", "--export-only", "--output", ".faultline/bundles/bundle"}, true},
		{"native-symlink", []string{"judge-demo", "--export-only", "--rerun-all", "--approve-native", digest, "--output", ".faultline/bundles/bundle"}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			isolatedEmpty(t)
			directory := nodefs.Resolve(".faultline/bundles/bundle")
			if err := os.MkdirAll(filepath.Dir(directory), 0o755); err != nil {
				t.Fatal(err)
			}
			// A valid prior bundle, to be replaced by a native bundle that differs.
			prior := demofixture.CreateReplayAnalysis()
			priorWritten, err := demo.Write(directory, prior, demo.WriteOptions{})
			if err != nil {
				t.Fatal(err)
			}
			reserved := sidecarOf(directory)
			keepDir := filepath.Join(t.TempDir(), "target")
			if c.symlink {
				if err := os.Mkdir(keepDir, 0o700); err != nil {
					t.Fatal(err)
				}
				makeDirectoryLink(t, keepDir, reserved)
			} else {
				if err := os.Mkdir(reserved, 0o755); err != nil {
					t.Fatal(err)
				}
				keepDir = reserved
			}
			keep := filepath.Join(keepDir, "keep")
			if err := os.WriteFile(keep, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			var out, errb bytes.Buffer
			code := Run(c.args, &out, &errb)
			want := demoFail("Native evidence destination must be a regular non-symlink file: " + reserved)
			if code != 1 || out.Len() != 0 || errb.String() != want {
				t.Fatalf("stdout=%q stderr=%q want=%q exit=%d", out.String(), errb.String(), want, code)
			}
			emitDiagnostic(t, errb.String())
			verification := demo.Verify(directory, "", false)
			if !verification.Valid || verification.RootDigest == nil {
				t.Fatalf("%+v", verification)
			}
			if strings.HasPrefix(c.name, "native") {
				if *verification.RootDigest == priorWritten.RootDigest || *verification.RootDigest == replayRetainedRoot {
					t.Fatalf("bundle was not replaced by the native bundle: %s", *verification.RootDigest)
				}
				got, err := canonical.DigestJSON(readValue(t, filepath.Join(directory, "analysis.json")))
				if err != nil {
					t.Fatal(err)
				}
				priorDigest, err := canonical.DigestJSON(prior)
				if err != nil {
					t.Fatal(err)
				}
				if got == priorDigest {
					t.Fatal("native analysis equals the prior REPLAY analysis")
				}
			} else if *verification.RootDigest != priorWritten.RootDigest {
				t.Fatalf("replay root %s differs from prior %s", *verification.RootDigest, priorWritten.RootDigest)
			}
			if body, err := os.ReadFile(keep); err != nil || string(body) != "keep" {
				t.Fatalf("reserved entry content was disturbed: %v", err)
			}
			info, err := os.Lstat(reserved)
			if err != nil || (c.symlink && !isSpecialLink(info)) || (!c.symlink && !info.IsDir()) {
				t.Fatalf("reserved entry replaced: %v", err)
			}
		})
	}
}
