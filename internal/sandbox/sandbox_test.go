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
