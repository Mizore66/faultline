//go:build faultline_test

package difftest

import (
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/hostcmd"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/runtimeimage"
)

// runtimeStep is one scripted Docker result, or a runner rejection.
type runtimeStep struct {
	throw  string
	result hostcmd.Result
}

func (s runtimeStep) json() jsjson.Value {
	o := jsjson.NewObj()
	if s.throw != "" {
		o.Set("throw", jsjson.MakeString(s.throw))
		return jsjson.MakeObject(o)
	}
	exit := jsjson.MakeNull()
	if s.result.ExitCode != nil {
		exit = jsjson.MakeNumber(float64(*s.result.ExitCode))
	}
	o.Set("exitCode", exit)
	o.Set("stdout", jsjson.MakeString(s.result.Stdout))
	o.Set("stderr", jsjson.MakeString(s.result.Stderr))
	o.Set("timedOut", jsjson.MakeBool(s.result.TimedOut))
	o.Set("outputLimitExceeded", jsjson.MakeBool(s.result.OutputLimitExceeded))
	return jsjson.MakeObject(o)
}

var liveRuntimeRepos = []string{
	"node", "library/node", "docker.io/library/node", "index.docker.io/library/node", "docker.io/node", "index.docker.io/node",
	"python", "docker.io/library/python", "golang", "docker.io/library/golang", "go",
	"evil/node", "ghcr.io/x/node", "registry:5000/node", "docker.io/docker.io/node", "index.docker.io/docker.io/node",
	"node:22-alpine", "registry:5000/node:1", "-node", "Node", "",
}

func randomRepoDigestEntry(r *rand.Rand) string {
	repo := liveRuntimeRepos[r.IntN(len(liveRuntimeRepos))]
	hex := strings.Repeat(string("0123456789abcdef"[r.IntN(16)]), 64)
	switch r.IntN(12) {
	case 0:
		return repo + "@sha256:" + strings.ToUpper(hex)
	case 1:
		return repo + "@sha256:" + hex[:63]
	case 2:
		return repo
	case 3:
		return repo + "@sha256:" + hex + "\n"
	case 4:
		return repo + "@sha512:" + hex
	}
	return repo + "@sha256:" + hex
}

func randomRepoDigestStdout(r *rand.Rand) string {
	items := []jsjson.Value{}
	for n := r.IntN(4); n > 0; n-- {
		switch r.IntN(14) {
		case 0:
			items = append(items, jsjson.MakeNumber(1))
		case 1:
			items = append(items, jsjson.MakeNull())
		default:
			items = append(items, jsjson.MakeString(randomRepoDigestEntry(r)))
		}
	}
	text := jsjson.Stringify(jsjson.MakeArray(items))
	switch r.IntN(14) {
	case 0:
		return text[:r.IntN(len(text)+1)] // malformed
	case 1:
		return []string{"", " ", "null", "{}", `{"0":"node@sha256:` + strings.Repeat("a", 64) + `"}`, `"node@sha256:` + strings.Repeat("a", 64) + `"`, "3", "[]", "<no value>", "\ufeff[]"}[r.IntN(10)]
	case 2:
		return "\n\t " + text + " \r\n"
	case 5:
		// Whitespace JS trim removes and Go's TrimSpace does not.
		return "\ufeff\u00a0" + text + "\u2028\u3000"
	case 3:
		return text + "\n" + text
	case 4:
		return "\u00a0" + text + "\u2028"
	}
	return text
}

func randomDockerStderr(r *rand.Rand) string {
	pieces := []string{"a", "b", "x", " ", "\r", "\n", "\r\n", "\n\n\r", "\t", "\u00a0", "\u2028", "\ufeff", "😀", "é", "\xed\xa0\x80", "Error: ", "No such image"}
	var b strings.Builder
	switch r.IntN(4) {
	case 0:
		return ""
	case 1:
		// Long: around the 320-unit cut, an astral character near it.
		n := 310 + r.IntN(20)
		b.WriteString(strings.Repeat(" \n", r.IntN(3)))
		b.WriteString(strings.Repeat("y", n))
		for k := r.IntN(6); k > 0; k-- {
			b.WriteString(pieces[r.IntN(len(pieces))])
		}
		return b.String()
	}
	for k := r.IntN(30); k > 0; k-- {
		b.WriteString(pieces[r.IntN(len(pieces))])
	}
	return b.String()
}

func randomRuntimeStep(r *rand.Rand) runtimeStep {
	if r.IntN(20) == 0 {
		return runtimeStep{throw: []string{"spawn docker ENOENT", "boom", "😀 x", " "}[r.IntN(4)]}
	}
	var res hostcmd.Result
	switch k := r.IntN(10); {
	case k < 6:
		res.ExitCode = new(0)
	case k < 8:
		res.ExitCode = new([]int{1, 2, 125, 137}[r.IntN(4)])
	}
	res.TimedOut = r.IntN(10) == 0
	res.OutputLimitExceeded = r.IntN(10) == 0
	res.Stdout = randomRepoDigestStdout(r)
	if r.IntN(2) == 0 {
		res.Stderr = randomDockerStderr(r)
	}
	return runtimeStep{result: res}
}

// goCuratedRuntime renders the Go result like the curatedRuntime op.
func goCuratedRuntime(fn, requested string, steps []runtimeStep) string {
	calls := []jsjson.Value{}
	run := func(args []string, timeout time.Duration, _ int) hostcmd.Result {
		entry := []jsjson.Value{}
		for _, a := range args {
			entry = append(entry, jsjson.MakeString(a))
		}
		calls = append(calls, jsjson.MakeArray(append(entry, jsjson.MakeNumber(float64(timeout.Milliseconds())))))
		if len(steps) == 0 {
			jsexc.Throw(errors.New("script exhausted"))
		}
		step := steps[0]
		steps = steps[1:]
		if step.throw != "" {
			jsexc.Throw(errors.New(step.throw))
		}
		return step.result
	}
	var v jsjson.Value
	var err error
	if fn == "resolve" {
		v, err = runtimeimage.ResolveCuratedRuntime(requested, run)
	} else {
		v, err = runtimeimage.PrepareCuratedRuntime(requested, run)
	}
	outcome := ""
	if err != nil {
		code := "?"
		var re *runtimeimage.ResolutionError
		if errors.As(err, &re) {
			code = re.Code
		}
		outcome = "THREW:" + code + ":" + jsexc.Message(err)
	} else {
		outcome = canonicalOf(v)
	}
	return outcome + "|" + canonicalOf(jsjson.MakeArray(calls))
}

// TestLiveCuratedRuntime runs scripted Docker results through
// resolveCuratedRuntime/prepareCuratedRuntime and the Go ports, and compares
// the result (or THREW:<code>:<message>) and the argv/timeout of each call.
func TestLiveCuratedRuntime(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(31, 3))
	requests := []string{"node", "python", "go", "node:22-alpine", "python:3.13-alpine", "golang:1.24-alpine", "ruby", "node:latest", "", "NODE", "golang", "😀", "\xed\xa0\x80"}
	reached := map[string]int{}
	for i := 0; i < 1500; i++ {
		fn := "resolve"
		if r.IntN(2) == 0 {
			fn = "prepare"
		}
		requested := requests[r.IntN(6)]
		if r.IntN(12) == 0 {
			requested = requests[r.IntN(len(requests))]
		}
		var steps []runtimeStep
		for n := 1 + r.IntN(2); n > 0; n-- {
			steps = append(steps, randomRuntimeStep(r))
		}
		if r.IntN(3) == 0 {
			// A clean inspect whose RepoDigests may well match.
			entries := []jsjson.Value{}
			for n := 1 + r.IntN(3); n > 0; n-- {
				repo := []string{"node", "docker.io/library/node", "python", "index.docker.io/library/python", "golang", "docker.io/golang"}[r.IntN(6)]
				if r.IntN(4) == 0 {
					repo = liveRuntimeRepos[r.IntN(len(liveRuntimeRepos))]
				}
				entries = append(entries, jsjson.MakeString(repo+"@sha256:"+strings.Repeat(string("0123456789abcdef"[r.IntN(16)]), 64)))
			}
			stdout := jsjson.Stringify(jsjson.MakeArray(entries)) + "\n"
			if r.IntN(4) == 0 {
				stdout = "\ufeff" + stdout + "\u2028"
			}
			steps[len(steps)-1] = runtimeStep{result: hostcmd.Result{ExitCode: new(0), Stdout: stdout}}
		}
		if fn == "prepare" && r.IntN(2) == 0 {
			steps[0] = runtimeStep{result: hostcmd.Result{ExitCode: new(0), Stdout: "Status: Downloaded"}}
			if len(steps) == 1 {
				steps = append(steps, randomRuntimeStep(r))
			}
		}
		items := []jsjson.Value{}
		for _, s := range steps {
			items = append(items, s.json())
		}
		args := `{"fn":"` + fn + `","requested":` + jsjson.Quote(requested) + `,"steps":` + jsjson.Stringify(jsjson.MakeArray(items)) + "}"
		got := goCuratedRuntime(fn, requested, steps)
		liveCheck(t, c, "curatedRuntime", args, got)
		head, _, _ := strings.Cut(got, "|")
		if strings.HasPrefix(head, "THREW:") {
			code, _, _ := strings.Cut(strings.TrimPrefix(head, "THREW:"), ":")
			reached[code]++
		} else {
			reached["OK-"+fn]++
		}
	}
	t.Logf("outcomes: %v", reached)
	requireReached(t, "internal/runtimeimage/curated.go", recordedOutputs(t))
	for _, want := range []string{"OK-resolve", "OK-prepare", "UNKNOWN_RUNTIME", "DOCKER_PULL_FAILED", "DOCKER_PULL_TIMED_OUT", "DOCKER_PULL_OUTPUT_LIMIT",
		"DOCKER_INSPECT_FAILED", "DOCKER_INSPECT_TIMED_OUT", "DOCKER_INSPECT_OUTPUT_LIMIT", "UNRESOLVED_REPO_DIGEST", "UNEXPECTED_REPOSITORY"} {
		if reached[want] == 0 {
			t.Errorf("no case reached %s", want)
		}
	}
}

// TestLiveRepoDigestHelpers compares parseRepoDigests and
// normalizeRepository with the functions in the compiled dist/runtime.js.
func TestLiveRepoDigestHelpers(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(31, 4))
	var stdouts, repos []jsjson.Value
	var parsed, normalized []jsjson.Value
	for i := 0; i < 2000; i++ {
		s := randomRepoDigestStdout(r)
		stdouts = append(stdouts, jsjson.MakeString(s))
		items := []jsjson.Value{}
		for _, d := range runtimeimage.ParseRepoDigestsForTest(s) {
			items = append(items, jsjson.MakeString(d))
		}
		parsed = append(parsed, jsjson.MakeArray(items))
		repo := liveRuntimeRepos[r.IntN(len(liveRuntimeRepos))]
		if r.IntN(3) == 0 {
			repo = []string{"index.docker.io/", "docker.io/", "", "a/", "/"}[r.IntN(5)] + repo
		}
		repos = append(repos, jsjson.MakeString(repo))
		normalized = append(normalized, jsjson.MakeString(runtimeimage.NormalizeRepositoryForTest(repo)))
	}
	want := jsjson.NewObj()
	want.Set("parsed", jsjson.MakeArray(parsed))
	want.Set("normalized", jsjson.MakeArray(normalized))
	args := `{"stdouts":` + jsjson.Stringify(jsjson.MakeArray(stdouts)) + `,"repositories":` + jsjson.Stringify(jsjson.MakeArray(repos)) + "}"
	liveCheck(t, c, "repoDigestHelpers", args, canonicalOf(jsjson.MakeObject(want)))
}

// goProjectOutcome renders a Go project-image result like the oracle's
// projectOutcome: JSON.stringify, or THREW:<code>:<message>.
func goProjectOutcome(v jsjson.Value, err error) string {
	if err != nil {
		code := "?"
		var pe *runtimeimage.PreparationError
		if errors.As(err, &pe) {
			code = pe.Code
		}
		return "THREW:" + code + ":" + jsexc.Message(err)
	}
	return jsjson.Stringify(v)
}

func projectRequestJSON(in runtimeimage.ProjectBuildInput) *jsjson.Obj {
	o := jsjson.NewObj()
	o.Set("contextDirectory", jsjson.MakeString(in.ContextDirectory))
	o.Set("dockerfile", jsjson.MakeString(in.Dockerfile))
	o.Set("imageTag", jsjson.MakeString(in.ImageTag))
	o.Set("network", jsjson.MakeString(in.Network))
	return o
}

// liveDescribe compares describeProjectRuntimeImageBuild with
// DescribeProjectBuild for in, and returns the outcome.
func liveDescribe(t *testing.T, c *oracle.Client, in runtimeimage.ProjectBuildInput) string {
	t.Helper()
	got := goProjectOutcome(runtimeimage.DescribeProjectBuild(in))
	liveCheck(t, c, "describeProjectRuntimeImageBuild", `{"request":`+jsjson.Stringify(jsjson.MakeObject(projectRequestJSON(in)))+"}", got)
	return got
}

// liveFingerprint compares the unexported walk (from dist/runtime.js) with
// the Go walk over an already normalized context.
func liveFingerprint(t *testing.T, c *oracle.Client, contextDirectory, dockerfile string) string {
	t.Helper()
	got := goProjectOutcome(runtimeimage.FingerprintForTest(contextDirectory, dockerfile))
	liveCheck(t, c, "fingerprintProjectBuildContext", `{"contextDirectory":`+jsjson.Quote(contextDirectory)+`,"dockerfile":`+jsjson.Quote(dockerfile)+"}", got)
	return got
}

// Names that sort differently under localeCompare and byte order: case,
// NFC/NFD, digits, punctuation, astral characters.
var liveContextNames = []string{
	"a", "A", "b", "B", "z", "Z", "aa", "Ab", "aB", "é", "é", "E", "É", "résumé", "resume",
	"10", "9", "_x", "-x", ".hidden", "ä", "a b", "ß", "ss", "日本", "\U0001F600", "x.txt", "X.TXT", "Å", "Å",
}

var liveProjectTags = []string{
	"registry.example/faultline/demo:deps-20260717", "local/faultline:deps-1", "app:1", "registry:5000/app:v1.2", "a/b:_x",
	"registry.example/faultline/demo:latest", "app:LATEST", "--tag-escape:deps", "App:1", "app", "app:", "app:-x", "app@sha256:1",
	"app:1 ", "app:é", "é:1", "", "a/b:" + strings.Repeat("x", 128), "a/b:" + strings.Repeat("x", 129), "app:1 ", "app:1\x00",
	"registry:5000/app", "a:b/c",
}

// liveContext is one generated build context and the paths it chmod'ed.
type liveContext struct {
	dir     string
	locked  []string
	special string
}

// buildLiveContext writes a random tree under dir: nested and empty
// directories, random names and modes, and at most one special entry.
func buildLiveContext(t *testing.T, r *rand.Rand, dir string) *liveContext {
	t.Helper()
	lc := &liveContext{dir: dir}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	modes := []os.FileMode{0o644, 0o755, 0o600, 0o700, 0o444, 0o640, 0o751}
	var dirs []string
	var files []string
	var fill func(d string, depth int)
	fill = func(d string, depth int) {
		dirs = append(dirs, d)
		for n := r.IntN(6); n > 0; n-- {
			name := liveContextNames[r.IntN(len(liveContextNames))]
			if r.IntN(3) == 0 {
				name += liveContextNames[r.IntN(len(liveContextNames))]
			}
			p := filepath.Join(d, name)
			if depth < 3 && r.IntN(4) == 0 {
				if os.Mkdir(p, 0o755) == nil {
					fill(p, depth+1)
				}
				continue
			}
			content := make([]byte, r.IntN(65))
			for i := range content {
				content[i] = byte(r.IntN(256))
			}
			if os.WriteFile(p, content, 0o644) == nil {
				os.Chmod(p, modes[r.IntN(len(modes))])
				files = append(files, p)
			}
		}
	}
	fill(dir, 0)
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\nCOPY . /app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := dirs[r.IntN(len(dirs))]
	switch r.IntN(12) {
	case 0:
		lc.special = "symlink"
		os.Symlink("Dockerfile", filepath.Join(sub, "link"))
	case 1:
		if mkfifo(filepath.Join(sub, "fifo")) == nil {
			lc.special = "fifo"
		}
	case 2:
		p := filepath.Join(sub, "unreadable")
		if os.WriteFile(p, []byte("secret"), 0o644) == nil && os.Chmod(p, 0) == nil {
			lc.special = "unreadable-file"
			lc.locked = append(lc.locked, p)
		}
	case 3:
		p := filepath.Join(sub, "locked")
		if os.Mkdir(p, 0o755) == nil && os.WriteFile(filepath.Join(p, "f"), []byte("x"), 0o644) == nil && os.Chmod(p, 0) == nil {
			lc.special = "unreadable-dir"
			lc.locked = append(lc.locked, p)
		}
	case 4:
		// A name that is not valid UTF-8 (refused by APFS): readdir
		// decodes it with U+FFFD, so it no longer names the file.
		if os.WriteFile(filepath.Join(sub, "bad\xff"), []byte("x"), 0o644) == nil {
			lc.special = "invalid-utf8"
		}
	}
	t.Cleanup(func() {
		for _, p := range lc.locked {
			os.Chmod(p, 0o755)
		}
	})
	return lc
}

// randomProjectInput varies the request around a generated context.
func randomProjectInput(r *rand.Rand, lc *liveContext, outside string) runtimeimage.ProjectBuildInput {
	in := runtimeimage.ProjectBuildInput{ContextDirectory: lc.dir, Dockerfile: "Dockerfile", ImageTag: liveProjectTags[r.IntN(3)], Network: "none"}
	if r.IntN(2) == 0 {
		in.Network = "default"
	}
	if r.IntN(6) == 0 {
		in.ImageTag = liveProjectTags[r.IntN(len(liveProjectTags))]
	}
	if r.IntN(15) == 0 {
		in.Network = []string{"host", "", "None", "bridge"}[r.IntN(4)]
	}
	switch r.IntN(30) {
	case 0:
		in.ContextDirectory = filepath.Base(lc.dir) // relative
	case 1:
		in.ContextDirectory = lc.dir + "/"
	case 2:
		in.ContextDirectory = lc.dir + "/missing"
	case 3:
		in.ContextDirectory = filepath.Join(lc.dir, "Dockerfile")
	case 4:
		in.ContextDirectory = "/"
	case 5:
		in.ContextDirectory = lc.dir + []string{"\x00", "\r", "\n"}[r.IntN(3)]
	case 6:
		in.ContextDirectory = ""
	case 7:
		in.ContextDirectory = lc.dir + "/./x/.."
	}
	switch r.IntN(20) {
	case 0:
		in.Dockerfile = filepath.Join(lc.dir, "Dockerfile")
	case 1:
		in.Dockerfile = filepath.Join(outside, "Dockerfile")
	case 2:
		in.Dockerfile = "../" + filepath.Base(outside) + "/Dockerfile"
	case 3:
		in.Dockerfile = "missing"
	case 4:
		in.Dockerfile = "."
	case 5:
		in.Dockerfile = []string{"", "Docker\nfile", "Docker\x00file", "Dockerfile\r"}[r.IntN(4)]
	case 6:
		in.Dockerfile = "./Dockerfile"
	case 7:
		if lc.special == "symlink" {
			in.Dockerfile = "link" // only at the root: realpath resolves it
		}
	}
	return in
}

// TestLiveProjectContext compares describeProjectRuntimeImageBuild (and the
// unexported walk) with the Go ports over 200 generated build contexts, the
// 10,000-file limit and, on Linux, the 256 MiB byte limit.
func TestLiveProjectContext(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(31, 5))
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	os.Mkdir(outside, 0o755)
	os.WriteFile(filepath.Join(outside, "Dockerfile"), []byte("FROM scratch\n"), 0o644)
	reached := map[string]int{}
	note := func(outcome string) {
		if rest, ok := strings.CutPrefix(outcome, "THREW:"); ok {
			code, msg, _ := strings.Cut(rest, ":")
			reached[code+":"+msg[:min(len(msg), 40)]]++
			return
		}
		reached["OK"]++
	}
	for i := 0; i < 200; i++ {
		lc := buildLiveContext(t, r, filepath.Join(base, "ctx"+strconv.Itoa(i)))
		real, err := filepath.EvalSymlinks(lc.dir)
		if err != nil {
			t.Fatal(err)
		}
		note(liveFingerprint(t, c, real, filepath.Join(real, "Dockerfile")))
		note(liveDescribe(t, c, randomProjectInput(r, lc, outside)))
	}

	// The file-count limit: exactly 10,000 files (the Dockerfile and 9,999
	// one-byte files), then 10,001.
	many := filepath.Join(base, "many")
	os.Mkdir(many, 0o755)
	os.WriteFile(filepath.Join(many, "Dockerfile"), []byte("FROM scratch\n"), 0o644)
	for i := 0; i < 9_999; i++ {
		if err := os.WriteFile(filepath.Join(many, "f"+strconv.Itoa(i)), []byte{byte(i)}, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	in := runtimeimage.ProjectBuildInput{ContextDirectory: many, Dockerfile: "Dockerfile", ImageTag: "local/faultline:deps-1", Network: "none"}
	if got := liveDescribe(t, c, in); strings.HasPrefix(got, "THREW:") {
		t.Fatalf("10,000 files: %s", got)
	}
	os.WriteFile(filepath.Join(many, "z-extra"), []byte{1}, 0o644)
	if got := liveDescribe(t, c, in); !strings.Contains(got, "exceeds 10000 files") {
		t.Fatalf("10,001 files: %s", got)
	}

	t.Run("sparse256MiB", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("the sparse 256 MiB context runs on Linux (difftest-live)")
		}
		big := filepath.Join(base, "big")
		os.Mkdir(big, 0o755)
		dockerfile := []byte("FROM scratch\n")
		os.WriteFile(filepath.Join(big, "Dockerfile"), dockerfile, 0o644)
		f, err := os.Create(filepath.Join(big, "sparse"))
		if err != nil {
			t.Fatal(err)
		}
		// Exactly the limit with the Dockerfile, then one byte over.
		if err := f.Truncate(runtimeimage.ProjectRuntimeImageMaxContextBytes - int64(len(dockerfile))); err != nil {
			t.Fatal(err)
		}
		in := runtimeimage.ProjectBuildInput{ContextDirectory: big, Dockerfile: "Dockerfile", ImageTag: "local/faultline:deps-1", Network: "none"}
		if got := liveDescribe(t, c, in); strings.HasPrefix(got, "THREW:") {
			t.Fatalf("at the limit: %s", got)
		}
		if err := f.Truncate(runtimeimage.ProjectRuntimeImageMaxContextBytes - int64(len(dockerfile)) + 1); err != nil {
			t.Fatal(err)
		}
		f.Close()
		if got := liveDescribe(t, c, in); !strings.Contains(got, "exceeds 268435456 bytes") {
			t.Fatalf("over the limit: %s", got)
		}
	})
	t.Logf("outcomes: %v", reached)
	outputs := recordedOutputs(t)
	if runtime.GOOS != "linux" {
		// Linux only: the sparse 256 MiB context (difftest-live), and the
		// lstat failure of a name that is not valid UTF-8 (readdir decodes
		// it with U+FFFD; APFS refuses to create it).
		outputs += "Project image build context exceeds 268435456 bytes; use a smaller reviewed context.\n" +
			"FaultLine could not inspect build-context path\n"
	}
	requireReached(t, "internal/runtimeimage/context.go", outputs)
}

// goScriptedRunner replays steps like the oracle's scriptedDockerRunner and
// records [...argv, timeoutMs] per call.
func goScriptedRunner(steps []runtimeStep) (runtimeimage.Runner, *[]jsjson.Value) {
	calls := []jsjson.Value{}
	return func(args []string, timeout time.Duration, _ int) hostcmd.Result {
		entry := []jsjson.Value{}
		for _, a := range args {
			entry = append(entry, jsjson.MakeString(a))
		}
		calls = append(calls, jsjson.MakeArray(append(entry, jsjson.MakeNumber(float64(timeout.Milliseconds())))))
		if len(steps) == 0 {
			jsexc.Throw(errors.New("script exhausted"))
		}
		step := steps[0]
		steps = steps[1:]
		if step.throw != "" {
			jsexc.Throw(errors.New(step.throw))
		}
		return step.result
	}, &calls
}

func stepsJSON(steps []runtimeStep) string {
	items := []jsjson.Value{}
	for _, s := range steps {
		items = append(items, s.json())
	}
	return jsjson.Stringify(jsjson.MakeArray(items))
}

// randomProjectInspect is an inspect result whose RepoDigests may match a
// project tag's repository.
func randomProjectInspect(r *rand.Rand, tag string) runtimeStep {
	repo, _, _ := strings.Cut(tag, ":")
	if i := strings.LastIndexByte(tag, ':'); i > strings.LastIndexByte(tag, '/') {
		repo = tag[:i]
	}
	entries := []jsjson.Value{}
	for n := 1 + r.IntN(3); n > 0; n-- {
		candidate := []string{repo, "docker.io/" + repo, "index.docker.io/library/" + repo, "library/" + repo, "registry.example/faultline/demo", "local/faultline", "other/app"}[r.IntN(7)]
		if r.IntN(5) == 0 {
			candidate = liveRuntimeRepos[r.IntN(len(liveRuntimeRepos))]
		}
		entries = append(entries, jsjson.MakeString(candidate+"@sha256:"+strings.Repeat(string("0123456789abcdef"[r.IntN(16)]), 64)))
	}
	stdout := jsjson.Stringify(jsjson.MakeArray(entries)) + "\n"
	if r.IntN(6) == 0 {
		stdout = "[]"
	}
	return runtimeStep{result: hostcmd.Result{ExitCode: new(0), Stdout: stdout}}
}

// TestLiveProjectPrepare runs 500 scripted build and inspect sequences
// through prepareProjectRuntimeImage/resolveProjectRuntimeImage and the Go
// ports, and compares each outcome and the argv/timeout of each call.
func TestLiveProjectPrepare(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(31, 6))
	base := t.TempDir()
	var contexts []string
	for i := 0; i < 3; i++ {
		dir := filepath.Join(base, "ctx"+strconv.Itoa(i))
		os.MkdirAll(filepath.Join(dir, "src"), 0o755)
		os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM node:22-alpine\nCOPY . /app\n"), 0o644)
		os.WriteFile(filepath.Join(dir, "src", "app.js"), []byte("console.log("+strconv.Itoa(i)+")\n"), 0o644)
		contexts = append(contexts, dir)
	}
	reached := map[string]int{}
	for i := 0; i < 500; i++ {
		tag := liveProjectTags[r.IntN(5)]
		if r.IntN(10) == 0 {
			tag = liveProjectTags[r.IntN(len(liveProjectTags))]
		}
		fn := "prepare"
		if r.IntN(4) == 0 {
			fn = "resolve"
		}
		var steps []runtimeStep
		if fn == "prepare" {
			build := randomRuntimeStep(r)
			if r.IntN(2) == 0 {
				build = runtimeStep{result: hostcmd.Result{ExitCode: new(0), Stdout: "Successfully built", Stderr: "#1 DONE"}}
			}
			steps = append(steps, build)
		}
		if r.IntN(3) == 0 {
			steps = append(steps, randomRuntimeStep(r))
		} else {
			steps = append(steps, randomProjectInspect(r, tag))
		}

		var got, args string
		if fn == "resolve" {
			run, calls := goScriptedRunner(steps)
			v, err := runtimeimage.ResolveProjectImage(tag, run)
			got = goProjectOutcome(v, err) + "|" + canonicalOf(jsjson.MakeArray(*calls))
			args = `{"tag":` + jsjson.Quote(tag) + `,"steps":` + stepsJSON(steps) + "}"
			liveCheck(t, c, "resolveProjectRuntimeImage", args, got)
		} else {
			in := runtimeimage.ProjectBuildInput{ContextDirectory: contexts[r.IntN(len(contexts))], Dockerfile: "Dockerfile", ImageTag: tag, Network: "none"}
			if r.IntN(2) == 0 {
				in.Network = "default"
			}
			if r.IntN(15) == 0 {
				in.Dockerfile = "missing"
			}
			// The reviewed digest: usually this request's own plan.
			expected := liveDigest
			reviewedIn := in
			if r.IntN(8) == 0 {
				reviewedIn.ContextDirectory = contexts[r.IntN(len(contexts))]
			}
			if r.IntN(10) == 0 {
				reviewedIn.Network = map[string]string{"none": "default", "default": "none"}[in.Network]
			}
			if plan, err := runtimeimage.DescribeProjectBuild(reviewedIn); err == nil {
				expected = plan.Get("review", "planDigest").Str()
			}
			switch r.IntN(12) {
			case 0:
				expected = "not-a-digest"
			case 1:
				expected = ""
			case 2:
				expected = strings.ToUpper(expected)
			case 3:
				expected = liveDigest
			}
			confirmed := r.IntN(12) != 0
			confirmation := "BUILD_PROJECT_RUNTIME_IMAGE"
			if !confirmed {
				confirmation = []string{"NOT_CONFIRMED", "", "build_project_runtime_image", "BUILD_PROJECT_RUNTIME_IMAGE "}[r.IntN(4)]
			}
			run, calls := goScriptedRunner(steps)
			v, err := runtimeimage.PrepareProjectImage(in, runtimeimage.PrepareProjectOptions{Confirmed: confirmed, ExpectedPlanDigest: expected}, run)
			got = goProjectOutcome(v, err) + "|" + canonicalOf(jsjson.MakeArray(*calls))
			req := projectRequestJSON(in)
			req.Set("confirmation", jsjson.MakeString(confirmation))
			req.Set("expectedPlanDigest", jsjson.MakeString(expected))
			args = `{"request":` + jsjson.Stringify(jsjson.MakeObject(req)) + `,"steps":` + stepsJSON(steps) + "}"
			liveCheck(t, c, "prepareProjectRuntimeImage", args, got)
		}
		head, _, _ := strings.Cut(got, "|")
		if rest, ok := strings.CutPrefix(head, "THREW:"); ok {
			code, _, _ := strings.Cut(rest, ":")
			reached[code]++
		} else {
			reached["OK-"+fn]++
		}
	}
	t.Logf("outcomes: %v", reached)
	requireReached(t, "internal/runtimeimage/project.go", recordedOutputs(t))
	for _, want := range []string{"OK-resolve", "OK-prepare", "PROJECT_IMAGE_CONFIRMATION_REQUIRED", "PROJECT_IMAGE_PLAN_DIGEST_REQUIRED", "PROJECT_IMAGE_PLAN_MISMATCH",
		"PROJECT_IMAGE_INVALID_INPUT", "DOCKER_BUILD_FAILED", "DOCKER_BUILD_TIMED_OUT", "DOCKER_BUILD_OUTPUT_LIMIT", "DOCKER_INSPECT_FAILED",
		"DOCKER_INSPECT_TIMED_OUT", "DOCKER_INSPECT_OUTPUT_LIMIT", "UNRESOLVED_REPO_DIGEST", "UNEXPECTED_REPOSITORY"} {
		if reached[want] == 0 {
			t.Errorf("no case reached %s", want)
		}
	}
}
