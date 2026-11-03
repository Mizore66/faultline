package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

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

func TestJudgeDemoNativeDiagnostics(t *testing.T) {
	for _, digest := range []string{"sha256:any-value", "sha256:" + strings.Repeat("0", 64)} {
		dir := isolatedEmpty(t)
		var out, errb bytes.Buffer
		code := Run([]string{"judge-demo", "--export-only", "--rerun-all", "--approve-native", digest}, &out, &errb)
		want := demoFail(`native analysis APPROVAL (invocation 0, state ""): approval must match the proposal and record a canonical UTC millisecond timestamp no later than execution entry`)
		t.Logf("NATIVE-DIAGNOSTIC %q", errb.String())
		if out.Len() != 0 || errb.String() != want || code != 1 {
			t.Fatalf("%s: stdout=%q stderr=%q exit=%d", digest, out.String(), errb.String(), code)
		}
		requireEmptyDir(t, dir)
	}
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

func TestJudgeDemoExport(t *testing.T) {
	bundleRel := filepath.Join(".faultline", "bundles", "judge-demo")

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
	})

	t.Run("native", func(t *testing.T) {
		isolatedEmpty(t)
		digest := demofixture.NativeWitnessProposal().Get("digest").Str()
		output := filepath.Join(".faultline", "bundles", "native")
		var out, errb bytes.Buffer
		code := Run([]string{"judge-demo", "--export-only", "--rerun-all", "--approve-native", digest, "--output", output}, &out, &errb)
		if code != 0 || errb.Len() != 0 {
			t.Fatalf("exit=%d stderr=%q", code, errb.String())
		}
		directory := nodefs.Resolve(output)
		verification := demo.Verify(directory, "", false)
		if !verification.Valid || verification.RootDigest == nil {
			t.Fatalf("%+v", verification)
		}
		analysis := readValue(t, filepath.Join(directory, "analysis.json"))
		var before, after string
		for _, transition := range analysis.Get("transitions").Items() {
			if transition.Get("kind").Str() == "PASS_TO_FAIL" && transition.Get("stable").Bool() {
				before, after = transition.Get("beforeStateId").Str(), transition.Get("afterStateId").Str()
				break
			}
		}
		if before == "" || after == "" {
			t.Fatal("no stable PASS_TO_FAIL transition")
		}
		sidecar := sidecarOf(directory)
		want := "FaultLine executed investigation prepared.\n" +
			"Stable sample boundary: " + before + " → " + after + "\n" +
			"Proof bundle self-consistency: VALID (" + strconv.Itoa(verification.CheckedFiles) + " declared files)\n" +
			"Bundle root: " + *verification.RootDigest + "\n" +
			"Proof bundle: " + output + "\n" +
			"Native execution evidence: " + sidecar + "\n"
		if out.String() != want {
			t.Fatalf("stdout=%q want=%q", out.String(), want)
		}
		evidence := readValue(t, sidecar)
		analysisDigest, err := canonical.DigestJSON(analysis)
		if err != nil {
			t.Fatal(err)
		}
		if evidence.Get("schemaVersion").Str() != "faultline.native-demo-cli-evidence.v1" ||
			evidence.Get("analysisDigest").Str() != analysisDigest ||
			evidence.Get("bundleRootDigest").Str() != *verification.RootDigest {
			t.Fatalf("sidecar linkage: %s", jsjson.Stringify(evidence))
		}
		provenance := evidence.Get("provenance")
		receipts := provenance.Get("invocations").Items()
		if provenance.Get("status").Str() != "COMPLETE" || len(receipts) != 39 {
			t.Fatalf("status=%q receipts=%d", provenance.Get("status").Str(), len(receipts))
		}
		for i, r := range receipts {
			if int(r.Get("ordinal").Num()) != i+1 {
				t.Fatalf("receipt %d ordinal %v", i, r.Get("ordinal").Num())
			}
		}
		approval := provenance.Get("approval")
		approvedAt := approval.Get("approvedAt").Str()
		if approval.Get("proposalDigest").Str() != digest || approvedAt == "" || approvedAt > clock.ISO(clock.Now()) {
			t.Fatalf("approval: %s", jsjson.Stringify(approval))
		}
		t.Logf("NATIVE-EVIDENCE sidecar=%s root=%s analysisDigest=%s checkedFiles=%d receipts=%d approvedAt=%s boundary=%s->%s",
			sidecar, *verification.RootDigest, analysisDigest, verification.CheckedFiles, len(receipts), approvedAt, before, after)
	})
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
			if c.symlink && runtime.GOOS == "windows" {
				t.Skip("symlink creation needs privileges on Windows")
			}
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
			keep := filepath.Join(t.TempDir(), "keep")
			if c.symlink {
				if err := os.WriteFile(keep, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(keep, reserved); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(reserved, 0o755); err != nil {
					t.Fatal(err)
				}
				keep = filepath.Join(reserved, "keep")
				if err := os.WriteFile(keep, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var out, errb bytes.Buffer
			code := Run(c.args, &out, &errb)
			want := demoFail("Native evidence destination must be a regular non-symlink file: " + reserved)
			if code != 1 || out.Len() != 0 || errb.String() != want {
				t.Fatalf("stdout=%q stderr=%q want=%q exit=%d", out.String(), errb.String(), want, code)
			}
			t.Logf("NATIVE-DIAGNOSTIC %q", errb.String())
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
			if err != nil || (c.symlink && info.Mode()&os.ModeSymlink == 0) || (!c.symlink && !info.IsDir()) {
				t.Fatalf("reserved entry replaced: %v", err)
			}
		})
	}
}
