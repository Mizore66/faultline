//go:build faultline_test

package difftest

import (
	"errors"
	"math/rand/v2"
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
