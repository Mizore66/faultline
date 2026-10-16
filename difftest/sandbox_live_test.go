//go:build faultline_test && unix

package difftest

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/sandbox"
)

func pickOf[T any](r *rand.Rand, xs []T) T { return xs[r.IntN(len(xs))] }

func hex64(c string) string { return strings.Repeat(c, 64) }

// sandboxPlanFixture builds the directories, files and links the plan
// requests point at, under a physical temporary directory.
func sandboxPlanFixture(t *testing.T) (root string, valid, invalid []string) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"src", "src/sub", "comma,dir", "cr\rdir", "lf\ndir", "dir with space", "é-dir"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := mkfifo(filepath.Join(root, "fifo")); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{
		"link-dir": filepath.Join(root, "src"), "rel-link": "./src", "chain": "link-dir", "link-file": "file.txt",
		"dangling": "missing", "loop": "loop", "link-comma": "comma,dir", "link-fifo": "fifo", "up": "..",
		"src/back": "../link-dir",
	} {
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []string{"src", "src/sub", "dir with space", "é-dir", "link-dir", "rel-link", "chain", "up/" + filepath.Base(root) + "/src",
		"src/back", "src/back/sub", "link-dir/sub", "src/../src", "src/", "link-dir//sub/"} {
		valid = append(valid, root+"/"+rel)
	}
	for _, rel := range []string{"comma,dir", "cr\rdir", "lf\ndir", "file.txt", "fifo", "link-file", "dangling", "loop", "loop/x", "link-comma",
		"link-fifo", "missing", "missing/x", "file.txt/x", "\xed\xa0\x80"} {
		invalid = append(invalid, root+"/"+rel)
	}
	invalid = append(invalid, "/", "", "relative/src", "src", "/tmp/a\x00b", root+"/src\x00", "//")
	return root, valid, invalid
}

// validOr is valid most of the time and otherwise one of the invalid choices.
func validOr[T any](r *rand.Rand, valid []T, invalid []T) T {
	if r.IntN(12) == 0 {
		return pickOf(r, invalid)
	}
	return pickOf(r, valid)
}

// randomSandboxRequest is a SandboxPlanRequest as JSON text. Each field is
// usually valid, so most requests reach the end of the plan.
func randomSandboxRequest(r *rand.Rand, valid, invalid []string) string {
	o := jsjson.NewObj()
	witness := jsjson.NewObj()
	witness.Set("digest", jsjson.MakeString(validOr(r, []string{"sha256:" + hex64("b"), "sha256:" + hex64("e")},
		[]string{"sha256:" + hex64("B"), "sha1:" + hex64("b"), "sha256:" + strings.Repeat("b", 63), ""})))
	witness.Set("command", jsjson.MakeString(validOr(r, []string{"node witness.mjs", "npm test -- --grep 'a b'", "echo é",
		"printf '%s\\n' \"$HOME\"", "\xed\xa0\x80", " x "},
		[]string{"", "   ", "\u00a0\ufeff", "\u2028", "a\x00b"})))
	o.Set("witness", jsjson.MakeObject(witness))
	if r.IntN(40) != 0 {
		o.Set("sourceDirectory", jsjson.MakeString(validOr(r, valid, invalid)))
	}
	if r.IntN(40) != 0 {
		o.Set("image", jsjson.MakeString(validOr(r, []string{"node@sha256:" + hex64("a"), "registry.example/team/fl:1.2@sha256:" + hex64("c")},
			[]string{"node:22", "node@sha256:" + hex64("A"), "-node@sha256:" + hex64("a"), "node@sha256:" + strings.Repeat("a", 63), "",
				"node@sha256:" + hex64("a") + "\n"})))
	}
	if r.IntN(2) == 0 {
		limits := jsjson.NewObj()
		for _, l := range gitproof.MaxSandboxLimits() {
			if r.IntN(3) != 0 {
				continue
			}
			v := validOr(r, []jsjson.Value{jsjson.MakeNumber(l.Maximum), jsjson.MakeNumber(1), jsjson.MakeNumber(float64(r.IntN(int(l.Maximum)) + 1))},
				[]jsjson.Value{jsjson.MakeNumber(l.Maximum + 1), jsjson.MakeNumber(0), jsjson.MakeNumber(-1), jsjson.MakeNumber(1.5),
					jsjson.MakeString("5"), jsjson.MakeNull(), jsjson.MakeNumber(1e21)})
			limits.Set(l.Name, v)
		}
		if r.IntN(10) == 0 {
			limits.Set("extra", jsjson.MakeNumber(7))
		}
		o.Set("limits", jsjson.MakeObject(limits))
	}
	if r.IntN(2) == 0 {
		var items []jsjson.Value
		for n := r.IntN(4); n > 0; n-- {
			items = append(items, jsjson.MakeString(validOr(r, []string{"FOO", "BAR", "FOO", "ZED", "_X", "PATH_ONE", "Z"},
				[]string{"lower", "CI", "API_KEY", "MY_TOKEN", "1BAD", "é", "AUTHOR"})))
		}
		o.Set("allowedEnvironment", jsjson.MakeArray(items))
	}
	if r.IntN(2) == 0 {
		env := jsjson.NewObj()
		for n := r.IntN(5); n > 0; n-- {
			key := validOr(r, []string{"FOO", "BAR", "ZED", "_X", "Z", "HOME", "API_KEY", "SESSION_ID", "UNLISTED", "TZ"},
				[]string{"CI", "LANG", "lower", "__proto__", "PRIVATE-KEY"})
			value := pickOf(r, []string{"x", "/tmp", "1", "UTC", "C.UTF-8", "é", "a,b=c", "\xed\xa0\x80", "", "two\nlines"})
			env.Set(key, jsjson.MakeString(value))
		}
		o.Set("environment", jsjson.MakeObject(env))
	}
	switch r.IntN(30) {
	case 0:
		o.Set("mode", jsjson.MakeString("UNSAFE_LOCAL"))
	case 1:
		o.Set("mode", jsjson.MakeString("SOMETHING_ELSE"))
	case 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15:
		o.Set("mode", jsjson.MakeString("DOCKER_ISOLATED"))
	}
	switch r.IntN(30) {
	case 0:
		o.Set("allowUnsafeLocal", jsjson.MakeBool(true))
	case 1:
		o.Set("allowUnsafeLocal", jsjson.MakeBool(false))
	}
	return jsjson.Stringify(jsjson.MakeObject(o))
}

func goPlanText(t *testing.T, text string) (string, sandbox.Plan, bool) {
	req, err := jsjson.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	p, err := sandbox.CreatePlan(req)
	if err != nil {
		return "THREW:" + err.Error(), p, false
	}
	return jsjson.Stringify(p.Value), p, true
}

// TestLiveSandboxPlan compares createSandboxPlan with CreatePlan on
// generated requests: the whole plan (argv with the container name, which
// both draw from the same seeded UUID sequence) or the thrown message.
func TestLiveSandboxPlan(t *testing.T) {
	const seed = "live-sandbox-plan"
	c := oracle.StartSeeded(t, seed)
	defer c.Close()
	clock.ResetForTest(seed)
	_, valid, invalid := sandboxPlanFixture(t)
	r := rand.New(rand.NewPCG(12, 1))
	messages := map[string]int{}
	plans := 0
	var outputs strings.Builder
	for i := 0; i < 2000; i++ {
		text := randomSandboxRequest(r, valid, invalid)
		want := oracleText(t, c, "createSandboxPlan", `{"request":`+text+`}`)
		got, p, ok := goPlanText(t, text)
		if got != want {
			t.Fatalf("createSandboxPlan(%s):\ngot  %s\nwant %s", text, got, want)
		}
		outputs.WriteString(want)
		if !ok {
			messages[got]++
			continue
		}
		plans++
		if errs := gitproof.ValidateSandboxPlanAudit(sandbox.AuditPlan(p)); len(errs) != 0 {
			t.Fatalf("audit of %s: %v", text, errs)
		}
	}
	t.Logf("%d plans, %d distinct errors", plans, len(messages))
	if plans < 200 || len(messages) < 20 {
		t.Fatalf("generator too narrow: %d plans, %d errors: %v", plans, len(messages), messages)
	}
	for m := range messages {
		t.Log(strings.ReplaceAll(m, "\n", `\n`))
	}
	requireReached(t, "internal/sandbox/plan.go", outputs.String())
}

// classifyPlanStub has the plan fields classifySandboxResult reads.
func classifyPlanStub(r *rand.Rand, maxOutputBytes int) string {
	kind := "DOCKER_ISOLATED"
	if r.IntN(20) == 0 {
		kind = "UNSAFE_LOCAL"
	}
	return `{"kind":"` + kind + `","limits":{"maxOutputBytes":` + jsjson.FormatNumber(float64(maxOutputBytes)) +
		`},"policyDigest":"sha256:` + hex64("1") + `","environmentPolicyDigest":"sha256:` + hex64("2") + `"}`
}

var classifyStdoutPieces = []string{
	"", "state=bad", "state=good", "é", "\xed\xa0\x80", "😀", "SyntaxError: x", "\u00a0",
	`FAULTLINE_WITNESS_RESULT={"protocol":"faultline.witness-result.v1","outcome":"PREDICATE_PASS"}`,
	`FAULTLINE_WITNESS_RESULT={"protocol":"faultline.witness-result.v1","outcome":"PREDICATE_FAIL"}`,
	`FAULTLINE_WITNESS_RESULT={"protocol":"faultline.witness-result.v1","outcome":"INCOMPATIBLE_STATE","detail":"d"}`,
	`FAULTLINE_WITNESS_RESULT={"protocol":"faultline.witness-result.v1","outcome":"HARNESS_ERROR"}`,
	`FAULTLINE_WITNESS_RESULT={"protocol":"faultline.witness-result.v1","outcome":"INFRASTRUCTURE_ERROR"}`,
	`{"protocol":"faultline.witness-result.v1","outcome":"PREDICATE_FAIL"}`,
	`FAULTLINE_WITNESS_RESULT={"protocol":"faultline.witness-result.v1","outcome":"PREDICATE_PASS","extra":1}`,
	"error TS2304: Cannot find name", "failed to compile",
}

var classifyStderrPieces = []string{
	"", "witness failed", "Cannot connect to the Docker daemon at unix:///var/run/docker.sock.", "IS THE DOCKER DAEMON RUNNING?",
	"error during connect", "docker: Error response from daemon: x", "Unable to find image 'x' locally", "pull access denied",
	"No such image: x", "docker daemon is not\nrunning", "sh: node: command not found", "x: not found", "No such file or directory",
	"Error: Cannot find module 'x'", "Permission denied", "SyntaxError: Unexpected token", "src/a.ts(1,2): error TS2304: x",
	"TS1005\u00a0:", "TS1005\u2028:", "error\u3000TS1005", "error[E0425]: x", "could not compile `crate`", "cargo: build error",
	"cargo:\nerror", "rustc 1.80 error: x", "ModuleNotFoundError: No module named 'x'", "ImportError:\ufeffNo module named x",
	"importerror: no module named x", "SYNTAXERROR", "é", "\xed\xa0\x80",
}

func joinPieces(r *rand.Rand, pieces []string) string {
	var parts []string
	for n := r.IntN(4); n > 0; n-- {
		parts = append(parts, pickOf(r, pieces))
	}
	return strings.Join(parts, pickOf(r, []string{"\n", "\r\n", " "}))
}

// TestLiveClassify compares classifySandboxResult with Classify on
// generated runner results.
func TestLiveClassify(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(12, 2))
	reasons := map[string]int{}
	for i := 0; i < 5000; i++ {
		maxOutputBytes := pickOf(r, []int{1_048_576, 1_048_576, 1_048_576, 1 + r.IntN(40), 1 + r.IntN(400)})
		planText := classifyPlanStub(r, maxOutputBytes)
		var run sandbox.RunResult
		res := jsjson.NewObj()
		if code := r.IntN(10); code < 8 {
			v := pickOf(r, []int{0, 0, 1, 1, 2, 125, 126, 127, 137})
			run.ExitCode = &v
			res.Set("exitCode", jsjson.MakeNumber(float64(v)))
		} else {
			res.Set("exitCode", jsjson.MakeNull())
		}
		switch r.IntN(3) {
		case 0:
			s := pickOf(r, []string{"SIGKILL", "SIGTERM"})
			run.Signal = &s
			res.Set("signal", jsjson.MakeString(s))
		case 1:
			res.Set("signal", jsjson.MakeNull())
		}
		run.Stdout, run.Stderr = joinPieces(r, classifyStdoutPieces), joinPieces(r, classifyStderrPieces)
		res.Set("stdout", jsjson.MakeString(run.Stdout))
		res.Set("stderr", jsjson.MakeString(run.Stderr))
		if r.IntN(10) == 0 {
			run.TimedOut = true
			res.Set("timedOut", jsjson.MakeBool(true))
		}
		if r.IntN(10) == 0 {
			run.OutputLimitExceeded = true
			res.Set("outputLimitExceeded", jsjson.MakeBool(true))
		}
		resText := jsjson.Stringify(jsjson.MakeObject(res))
		want := oracleText(t, c, "classifySandboxResult", `{"plan":`+planText+`,"result":`+resText+`}`)
		planValue, _ := jsjson.Parse(planText)
		got := jsjson.Stringify(sandbox.Classify(sandbox.Plan{Value: planValue}, run))
		if got != want {
			t.Fatalf("classify(%s, %s):\ngot  %s\nwant %s", planText, resText, got, want)
		}
		v, _ := jsjson.Parse(got)
		reasons[v.Get("reason").Str()]++
	}
	t.Logf("reasons: %v", reasons)
	for _, reason := range []string{"UNSAFE_LOCAL_NOT_PROOF", "TIMEOUT", "OUTPUT_LIMIT_EXCEEDED", "RUNNER_FAILURE", "SANDBOX_UNAVAILABLE",
		"WITNESS_SETUP_ERROR", "HARNESS_ERROR", "PREDICATE_PASS", "PREDICATE_FAIL", "INCOMPATIBLE_STATE", "EXIT_ZERO_UNSTRUCTURED",
		"EXIT_NONZERO_UNSTRUCTURED"} {
		if reasons[reason] == 0 {
			t.Errorf("no case reached %s", reason)
		}
	}
}

// randomUTF8ish is mostly valid UTF-8 with cut and invalid sequences.
func randomUTF8ish(r *rand.Rand) []byte {
	var b []byte
	for n := r.IntN(12); n > 0; n-- {
		piece := pickOf(r, []string{"a", "é", "中", "😀", "\xe2\x82", "\xf0\x9f", "\xff", "\xc3", "\x80", "\n", "\x00"})
		b = append(b, piece...)
	}
	return b
}

// TestLiveTruncate compares truncateOutput (through classifySandboxResult,
// which applies it when outputLimitExceeded is set) with TruncateOutput.
func TestLiveTruncate(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(12, 3))
	for i := 0; i < 3000; i++ {
		stdout, stderr := jsstr.DecodeUTF8(randomUTF8ish(r)), jsstr.DecodeUTF8(randomUTF8ish(r))
		if r.IntN(8) == 0 {
			stdout += "\xed\xa0\x80"
		}
		if r.IntN(8) == 0 {
			stderr = "\xed\xb0\x80" + stderr
		}
		outBytes, errBytes := []byte(jsstr.ToUTF8(stdout)), []byte(jsstr.ToUTF8(stderr))
		budget := r.IntN(len(outBytes) + len(errBytes) + 3)
		args := `{"stdout":` + jsjson.Quote(stdout) + `,"stderr":` + jsjson.Quote(stderr) + `,"max":` + jsjson.FormatNumber(float64(budget)) + `}`
		raw, err := c.Call("truncateOutput", args)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := jsjson.Parse(raw)
		gotOut, gotErr, cut := sandbox.TruncateOutput(outBytes, errBytes, budget)
		if gotOut != want.Get("stdout").Str() || gotErr != want.Get("stderr").Str() {
			t.Fatalf("truncate(%s): got %q %q, want %s", args, gotOut, gotErr, raw)
		}
		if cut != (len(outBytes)+len(errBytes) > budget) {
			t.Fatalf("truncate(%s): cut %v", args, cut)
		}
	}
}

// TestLiveNativeRunner runs one plan per fake Docker through
// executeSandboxPlan's native runner and through Execute(NativeRunner()),
// with the docker recorder first on PATH, and compares the argv, the result
// and the recorded docker invocations (the run and any rm --force cleanup).
func TestLiveNativeRunner(t *testing.T) {
	const seed = "live-native-runner"
	c := oracle.StartSeeded(t, seed)
	defer c.Close()
	clock.ResetForTest(seed)
	repo := oracle.RepoRoot(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(root, "empty-path")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, fake, state         string
		timeoutMs, maxOutputBytes int
		reason                    string
	}{
		{"pass", "pass-fail", "good", 60_000, 1_048_576, "PREDICATE_PASS"},
		{"fail", "pass-fail", "bad", 60_000, 1_048_576, "PREDICATE_FAIL"},
		{"odd-bad", "odd-bad", "bad", 60_000, 1_048_576, "PREDICATE_FAIL"},
		{"timeout", "timeout", "", 1_500, 1_048_576, "TIMEOUT"},
		// docker exits 0 while its child still holds the pipes: TIMEOUT with
		// exit code 0, and only timedOut forces the cleanup (round 7 mutant S5).
		{"timeout-detached", "timeout-detached", "", 1_500, 1_048_576, "TIMEOUT"},
		// The force-removal hangs past the 5 s cleanup limit.
		{"rm-hang", "rm-hang", "", 1_500, 1_048_576, "TIMEOUT"},
		// docker deletes itself and dies: rm --force cannot be spawned.
		{"vanish", "vanish", "", 60_000, 1_048_576, "RUNNER_FAILURE"},
		{"flood", "flood", "", 60_000, 1_006, "OUTPUT_LIMIT_EXCEEDED"},
		{"daemon-down", "daemon-down", "", 60_000, 1_048_576, "SANDBOX_UNAVAILABLE"},
		{"missing-image", "missing-image", "", 60_000, 1_048_576, "SANDBOX_UNAVAILABLE"},
		{"crash", "crash", "", 60_000, 1_048_576, "RUNNER_FAILURE"},
		{"setup-127", "setup-127", "", 60_000, 1_048_576, "WITNESS_SETUP_ERROR"},
		{"forged-pass", "forged-pass", "", 60_000, 1_048_576, "HARNESS_ERROR"},
		{"compile-error", "compile-error", "", 60_000, 1_048_576, "INCOMPATIBLE_STATE"},
		{"no-docker", "", "", 60_000, 1_048_576, "RUNNER_FAILURE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(root, "src-"+tc.name)
			if err := os.Mkdir(src, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(src, "state.txt"), []byte(tc.state+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			path := empty
			if tc.fake != "" {
				path = strings.Join([]string{filepath.Join(repo, "difftest/testdata/recorder"), filepath.Join(repo, "difftest/testdata/fakedocker", tc.fake),
					"/usr/bin", "/bin"}, ":")
			}
			// "vanish" deletes itself, so each side runs a private copy,
			// put back before the Go side runs.
			vanishDir := filepath.Join(root, "vanish-"+tc.name)
			restoreVanish := func() {
				script, err := os.ReadFile(filepath.Join(repo, "difftest/testdata/fakedocker/vanish/docker"))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(vanishDir, "docker"), script, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if tc.fake == "vanish" {
				if err := os.Mkdir(vanishDir, 0o755); err != nil {
					t.Fatal(err)
				}
				restoreVanish()
				path = strings.Join([]string{filepath.Join(repo, "difftest/testdata/recorder"), vanishDir, "/usr/bin", "/bin"}, ":")
			}
			request := `{"mode":"DOCKER_ISOLATED","sourceDirectory":` + jsjson.Quote(src) + `,"image":"node@sha256:` + hex64("a") +
				`","witness":{"digest":"sha256:` + hex64("b") + `","command":"node witness.mjs"},"environment":{"FOO":"bar","API_TOKEN":"s"},` +
				`"allowedEnvironment":["FOO"],"limits":{"timeoutMs":` + jsjson.FormatNumber(float64(tc.timeoutMs)) +
				`,"maxOutputBytes":` + jsjson.FormatNumber(float64(tc.maxOutputBytes)) + `}}`
			tsLog, goLog := filepath.Join(root, tc.name+"-ts.log"), filepath.Join(root, tc.name+"-go.log")
			want := oracleText(t, c, "executeSandboxPlan", `{"request":`+request+`,"path":`+jsjson.Quote(path)+`,"argvLog":`+jsjson.Quote(tsLog)+`}`)
			if tc.fake == "vanish" {
				restoreVanish()
			}
			t.Setenv("PATH", path)
			t.Setenv("FAULTLINE_ARGV_LOG", goLog)
			req, _ := jsjson.Parse(request)
			p, err := sandbox.CreatePlan(req)
			if err != nil {
				t.Fatal(err)
			}
			o := jsjson.NewObj()
			o.Set("arguments", p.Value.Get("arguments"))
			o.Set("result", sandbox.Execute(p, sandbox.NativeRunner(), false))
			got := jsjson.Stringify(jsjson.MakeObject(o))
			if got != want {
				t.Fatalf("got  %s\nwant %s", got, want)
			}
			recordOutput(t, want)
			if reason := o.Field("result").Get("reason").Str(); reason != tc.reason {
				t.Fatalf("reason %s, want %s: %s", reason, tc.reason, got)
			}
			tsRecords, _ := os.ReadFile(tsLog)
			goRecords, _ := os.ReadFile(goLog)
			if string(tsRecords) != string(goRecords) {
				t.Fatalf("docker argv differ:\ngo %q\nts %q", goRecords, tsRecords)
			}
			records := strings.Split(strings.TrimSuffix(string(goRecords), "\x1e"), "\x1e")
			cleanup := slices.Contains([]string{"timeout", "timeout-detached", "rm-hang", "vanish", "flood", "crash"}, tc.name)
			if tc.fake != "" && (len(records) != 1+map[bool]int{true: 1}[cleanup] ||
				cleanup && records[1] != "docker rm\x1f--force\x1f"+p.Value.Get("containerName").Str()) {
				t.Fatalf("docker records %q", records)
			}
			if stdout := o.Field("result").Get("stdout").Str(); tc.name == "flood" && !strings.HasSuffix(stdout, "flood \ufffd") {
				t.Fatalf("flood stdout should end in a cut é: %q", stdout[max(0, len(stdout)-20):])
			}
		})
	}
	requireReached(t, "internal/sandbox/run.go", recordedOutputs(t))
}
