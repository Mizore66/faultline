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
	"time"

	"github.com/Mizore66/faultline/internal/bundle/demo"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/demofixture"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// builtBinaryEnv is the empty-PATH environment of the built binary. Windows
// retains only the system and temp entries the host requires.
func builtBinaryEnv() []string {
	env := []string{"PATH=", "FAULTLINE_NO_BROWSER=1"}
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

// goVersionOfBinary is the Go version the built binary was compiled with,
// read from the same toolchain that built it.
func goVersionOfBinary(t *testing.T, binary string) string {
	t.Helper()
	cmd := exec.Command("go", "version", binary)
	cmd.Env = deterministicBuildEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go version: %v", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		t.Fatalf("go version output %q", out)
	}
	return fields[1]
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
	// The binary is built without the test tag, so it reads the real wall
	// clock even when this test process runs under a seeded one.
	started := clock.ISO(time.Now())
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

	analysisDigest := auditNativeSidecar(t, nativeAudit{
		sidecar: sidecar, directory: output, executable: binary, goVersion: goVersionOfBinary(t, binary),
		proposalDigest: digest, notBefore: started, notAfter: clock.ISO(time.Now()),
	})
	evidence := readValue(t, sidecar)
	receipts := evidence.Get("provenance").Get("invocations").Items()
	t.Logf("NODE-FREE-EVIDENCE binary=%s bundle=%s sidecar=%s root=%s analysisDigest=%s checkedFiles=%d receipts=%d",
		binary, output, sidecar, *verification.RootDigest, analysisDigest, verification.CheckedFiles, len(receipts))
}
