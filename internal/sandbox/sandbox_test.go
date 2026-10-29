//go:build faultline_test

package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsjson"
)

func TestDockerArgvAndClassification(t *testing.T) {
	clock.ResetForTest("sandbox")
	src := t.TempDir()
	req, _ := jsjson.Parse(`{"mode":"DOCKER_ISOLATED","sourceDirectory":` + jsjson.Quote(src) + `,
		"image":"node@sha256:` + strings.Repeat("a", 64) + `",
		"witness":{"digest":"sha256:` + strings.Repeat("b", 64) + `","command":"node witness.mjs"}}`)
	p, err := CreatePlan(req)
	if err != nil {
		t.Fatal(err)
	}
	// The brief wrote "args"; the TS plan key is "arguments".
	args := p.Value.Get("arguments").Items()
	if args[0].Str() != "run" || args[1].Str() != "--rm" || args[2].Str() != "--name" || !strings.HasPrefix(args[3].Str(), "faultline-") || len(args[3].Str()) != 42 {
		t.Fatalf("%v", args[:4])
	}
	code := 1
	got := Classify(p, RunResult{ExitCode: &code, Stdout: "state=bad\n{\"protocol\":\"faultline.witness-result.v1\",\"outcome\":\"PREDICATE_FAIL\"}\n"})
	if got.Get("verdict").Str() != "FAIL" || got.Get("reason").Str() == "" {
		t.Fatalf("%s", jsjson.Stringify(got))
	}
}

func TestTruncateOutputReplacesCutCodePoint(t *testing.T) {
	out, errOut, cut := TruncateOutput([]byte("ab\xe2\x82\xac"), []byte("zz"), 3)
	if out != "ab�" || errOut != "" || !cut {
		t.Fatalf("%q %q %v", out, errOut, cut)
	}
}

// TestDockerArgvIsolation pins the whole docker argv of one plan (the
// template at src/sandbox.ts:466) and the environment redaction, so a
// weakened isolation flag fails without the oracle.
func TestDockerArgvIsolation(t *testing.T) {
	clock.ResetForTest("sandbox-argv")
	src, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	image := "registry.example/fl@sha256:" + strings.Repeat("c", 64)
	req, _ := jsjson.Parse(`{"sourceDirectory":` + jsjson.Quote(src) + `,"image":"` + image + `",
		"witness":{"digest":"sha256:` + strings.Repeat("b", 64) + `","command":"node witness.mjs"},
		"environment":{"ZED":"z","API_TOKEN":"secret","FOO":"bar","UNLISTED":"u","HOME":"/tmp","SESSION_ID":"s"},
		"allowedEnvironment":["ZED","FOO","ZED"],
		"limits":{"timeoutMs":5000,"maxOutputBytes":4096,"cpuCount":2,"memoryBytes":268435456,"pidsLimit":64,"tmpfsBytes":1048576}}`)
	p, err := CreatePlan(req)
	if err != nil {
		t.Fatal(err)
	}
	name := p.Value.Get("containerName").Str()
	if len(name) != 42 || !strings.HasPrefix(name, "faultline-") || strings.Contains(name[10:], "-") {
		t.Fatalf("container name %q", name)
	}
	want := []string{
		"run", "--rm", "--name", name, "--init", "--pull=never", "--network", "none", "--ipc", "none", "--read-only",
		"--user", "65534:65534", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true",
		"--ulimit", "fsize=4096:4096", "--pids-limit", "64", "--memory", "268435456", "--memory-swap", "268435456", "--cpus", "2",
		"--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=1048576",
		"--mount", "type=bind,src=" + src + ",dst=/workspace/src,readonly", "--workdir", "/workspace/src",
		"--env", "CI=1", "--env", "FOO=bar", "--env", "HOME=/tmp", "--env", "LANG=C.UTF-8", "--env", "LC_ALL=C.UTF-8",
		"--env", "SOURCE_DATE_EPOCH=0", "--env", "TZ=UTC", "--env", "ZED=z",
		"--entrypoint", "/bin/sh", image, "-lc", "node witness.mjs",
	}
	var got []string
	for _, a := range p.Value.Get("arguments").Items() {
		got = append(got, a.Str())
	}
	if !slices.Equal(got, want) {
		t.Fatalf("argv:\ngot  %q\nwant %q", got, want)
	}
	if p.Value.Get("executable").Str() != "docker" || p.Value.Get("sourceDirectory").Str() != src {
		t.Fatalf("executable/sourceDirectory: %s", jsjson.Stringify(p.Value))
	}
	digest := func(s string) string { h := sha256.Sum256([]byte(s)); return "sha256:" + hex.EncodeToString(h[:]) }
	audit := AuditPlan(p)
	env := jsjson.Stringify(audit.Get("environment"))
	wantEnv := `{"fixedKeys":["CI","HOME","LANG","LC_ALL","SOURCE_DATE_EPOCH","TZ"],"allowedKeys":["FOO","ZED"],` +
		`"passed":[{"key":"FOO","valueDigest":"` + digest("bar") + `"},{"key":"ZED","valueDigest":"` + digest("z") + `"}],` +
		`"redactedKeys":["API_TOKEN","SESSION_ID","UNLISTED"]}`
	if env != wantEnv {
		t.Fatalf("audit environment:\ngot  %s\nwant %s", env, wantEnv)
	}
	runtime := jsjson.Stringify(audit.Get("runtime"))
	wantRuntime := `{"image":"` + image + `","entrypoint":"/bin/sh","network":"none","rootFilesystemReadOnly":true,` +
		`"user":"65534:65534","capDropAll":true,"noNewPrivileges":true,"pull":"never","limits":{"timeoutMs":5000,` +
		`"maxOutputBytes":4096,"cpuCount":2,"memoryBytes":268435456,"pidsLimit":64,"tmpfsBytes":1048576}}`
	if runtime != wantRuntime {
		t.Fatalf("audit runtime:\ngot  %s\nwant %s", runtime, wantRuntime)
	}
	// Redacted values reach neither the container environment nor the argv.
	values := jsjson.Stringify(p.Value.Get("environment"))
	for _, secret := range []string{"secret", "API_TOKEN", "SESSION_ID", "UNLISTED"} {
		if strings.Contains(values, secret) || slices.ContainsFunc(got, func(a string) bool { return strings.Contains(a, secret) }) {
			t.Fatalf("%s leaked: %s %q", secret, values, got)
		}
	}
}

// outputBudget sequences below are hand-derived from the frozen TS capture
// rule (src/sandbox.ts:870), not generated by the oracle. They run the real
// outputBudget used by runNative.
type budgetChunk struct {
	data     string
	toStdout bool
}

type budgetStep struct {
	kill           bool
	stdout, stderr string
	observed       int
}

func runBudget(t *testing.T, limit int, chunks []budgetChunk, want []budgetStep) {
	t.Helper()
	if len(chunks) != len(want) {
		t.Fatalf("test table: %d chunks, %d steps", len(chunks), len(want))
	}
	b := outputBudget{max: limit}
	kills := 0
	for i, c := range chunks {
		kill := b.add([]byte(c.data), c.toStdout)
		if kill {
			kills++
		}
		w := want[i]
		// The state is checked at every step, including the one that
		// returns the kill: capture must precede the kill transition.
		if kill != w.kill || string(b.stdout) != w.stdout || string(b.stderr) != w.stderr || b.observed != w.observed || b.overflow != (kills > 0) {
			t.Fatalf("step %d: kill=%v stdout=%q stderr=%q observed=%d overflow=%v; want %+v",
				i, kill, b.stdout, b.stderr, b.observed, b.overflow, w)
		}
	}
	if kills > 1 {
		t.Fatalf("%d kill transitions", kills)
	}
}

func TestOutputBudgetAlternatingCap5(t *testing.T) {
	runBudget(t, 5, []budgetChunk{{"ab", false}, {"cd", true}, {"ef", false}, {"gh", true}, {"ij", false}, {"kl", true}},
		[]budgetStep{
			{false, "", "ab", 2},
			{false, "cd", "ab", 4},
			{true, "cd", "abe", 6},
			{false, "cd", "abe", 8},
			{false, "cd", "abe", 10},
			{false, "cd", "abe", 12},
		})
}

func TestOutputBudgetCoalescedCap5(t *testing.T) {
	// The cap is crossed by the second chunk.
	runBudget(t, 5, []budgetChunk{{"abef", false}, {"cdgh", true}, {"x", false}, {"y", true}},
		[]budgetStep{
			{false, "", "abef", 4},
			{true, "c", "abef", 8},
			{false, "c", "abef", 9},
			{false, "c", "abef", 10},
		})
}

func TestOutputBudgetStdoutTakesRemainder(t *testing.T) {
	runBudget(t, 5, []budgetChunk{{"ab", false}, {"cdgh", true}, {"z", false}},
		[]budgetStep{
			{false, "", "ab", 2},
			{true, "cdg", "ab", 6},
			{false, "cdg", "ab", 7},
		})
}

func TestOutputBudgetSingleStreamMidChunkCuts(t *testing.T) {
	for _, toStdout := range []bool{true, false} {
		b := outputBudget{max: 5}
		if b.add([]byte("abcdefgh"), toStdout) != true {
			t.Fatalf("stdout=%v: no kill on first chunk", toStdout)
		}
		got, other := b.stdout, b.stderr
		if !toStdout {
			got, other = b.stderr, b.stdout
		}
		if string(got) != "abcde" || len(other) != 0 || b.observed != 8 || !b.overflow {
			t.Fatalf("stdout=%v: %q %q observed=%d overflow=%v", toStdout, b.stdout, b.stderr, b.observed, b.overflow)
		}
	}
}

func TestOutputBudgetExactCapThenSixthByte(t *testing.T) {
	runBudget(t, 5, []budgetChunk{{"abc", true}, {"de", false}, {"f", true}, {"g", false}},
		[]budgetStep{
			{false, "abc", "", 3},
			{false, "abc", "de", 5},
			{true, "abc", "de", 6},
			{false, "abc", "de", 7},
		})
}

func TestOutputBudgetLaterChunksCountedNotCaptured(t *testing.T) {
	runBudget(t, 3, []budgetChunk{{"abcd", true}, {"efg", false}, {"hi", true}, {"j", false}},
		[]budgetStep{
			{true, "abc", "", 4},
			{false, "abc", "", 7},
			{false, "abc", "", 9},
			{false, "abc", "", 10},
		})
}
