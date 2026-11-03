package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/bundle/demo"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/demofixture"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// builtBinaryEnv is the empty-PATH environment of the built binary. Windows
// retains only the system and temp entries the host requires.
func builtBinaryEnv() []string {
	env := []string{"PATH="}
	if runtime.GOOS == "windows" {
		for _, key := range []string{"SystemRoot", "SYSTEMROOT", "windir", "TEMP", "TMP"} {
			if value, ok := os.LookupEnv(key); ok {
				env = append(env, key+"="+value)
			}
		}
	}
	return env
}

// deterministicBuildEnv pins the offline, local-toolchain go build environment;
// the four pinned keys are never inherited from the host.
func deterministicBuildEnv() []string {
	pinned := map[string]string{"GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off", "GOFLAGS": ""}
	var env []string
	for _, kv := range os.Environ() {
		if key, _, _ := strings.Cut(kv, "="); strings.EqualFold(key, "GOTOOLCHAIN") || strings.EqualFold(key, "GOPROXY") ||
			strings.EqualFold(key, "GOSUMDB") || strings.EqualFold(key, "GOFLAGS") {
			continue
		}
		env = append(env, kv)
	}
	for _, key := range []string{"GOTOOLCHAIN", "GOPROXY", "GOSUMDB", "GOFLAGS"} {
		env = append(env, key+"="+pinned[key])
	}
	return env
}

func TestJudgeDemoBuiltBinaryNodeFree(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	fresh := t.TempDir()
	name := "fl"
	if runtime.GOOS == "windows" {
		name = "fl.exe"
	}
	binary := filepath.Join(fresh, name)
	build := exec.Command("go", "build", "-o", binary, "./cmd/fl")
	build.Dir = moduleRoot
	build.Env = deterministicBuildEnv()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	work := filepath.Join(fresh, "work")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, string) {
		t.Helper()
		var out, errb bytes.Buffer
		cmd := exec.Command(binary, args...)
		cmd.Dir = work
		cmd.Env = builtBinaryEnv()
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil || errb.Len() != 0 {
			t.Fatalf("%v: %v stderr=%q", args, err, errb.String())
		}
		return out.String(), errb.String()
	}

	proposal := demofixture.NativeWitnessProposal()
	wantProposal := jsjson.StringifyIndent(proposal, "  ") + "\n"
	if got, _ := run("judge-demo", "--native-proposal"); got != wantProposal {
		t.Fatalf("proposal differs:\n%s", got)
	}

	digest := proposal.Get("digest").Str()
	output := filepath.Join(work, ".faultline", "bundles", "native")
	stdout, _ := run("judge-demo", "--export-only", "--rerun-all", "--approve-native", digest, "--output", output)

	verification := demo.Verify(output, "", false)
	if !verification.Valid || verification.RootDigest == nil {
		t.Fatalf("bundle invalid: %+v", verification)
	}
	sidecar := output + ".native-execution.json"
	if !strings.Contains(stdout, "Bundle root: "+*verification.RootDigest+"\n") ||
		!strings.Contains(stdout, "VALID ("+strconv.Itoa(verification.CheckedFiles)+" declared files)") ||
		!strings.Contains(stdout, "Native execution evidence: "+sidecar+"\n") {
		t.Fatalf("stdout: %q", stdout)
	}

	read := func(path string) jsjson.Value {
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
	analysisDigest, err := canonical.DigestJSON(read(filepath.Join(output, "analysis.json")))
	if err != nil {
		t.Fatal(err)
	}
	evidence := read(sidecar)
	if evidence.Get("schemaVersion").Str() != "faultline.native-demo-cli-evidence.v1" ||
		evidence.Get("analysisDigest").Str() != analysisDigest ||
		evidence.Get("bundleRootDigest").Str() != *verification.RootDigest {
		t.Fatalf("sidecar linkage: %s", jsjson.Stringify(evidence))
	}
	provenance := evidence.Get("provenance")
	receipts := provenance.Get("invocations").Items()
	if provenance.Get("status").Str() != "COMPLETE" || len(receipts) != 39 ||
		provenance.Get("approval").Get("proposalDigest").Str() != digest {
		t.Fatalf("provenance: status=%q receipts=%d", provenance.Get("status").Str(), len(receipts))
	}
	for i, r := range receipts {
		if int(r.Get("ordinal").Num()) != i+1 {
			t.Fatalf("receipt %d ordinal %v", i, r.Get("ordinal").Num())
		}
	}
	t.Logf("NODE-FREE-EVIDENCE binary=%s bundle=%s sidecar=%s root=%s analysisDigest=%s checkedFiles=%d receipts=%d",
		binary, output, sidecar, *verification.RootDigest, analysisDigest, verification.CheckedFiles, len(receipts))
}
