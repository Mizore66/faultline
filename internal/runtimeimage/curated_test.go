package runtimeimage

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/hostcmd"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
)

type recordedCall struct {
	args    []string
	timeout time.Duration
	max     int
}

func scripted(results ...hostcmd.Result) (Runner, *[][]string) {
	var calls [][]string
	return func(args []string, _ time.Duration, _ int) hostcmd.Result {
		calls = append(calls, args)
		r := results[0]
		results = results[1:]
		return r
	}, &calls
}

// recording is scripted that also keeps the timeout and cap of each call.
func recording(results ...hostcmd.Result) (Runner, *[]recordedCall) {
	var calls []recordedCall
	return func(args []string, timeout time.Duration, max int) hostcmd.Result {
		calls = append(calls, recordedCall{args, timeout, max})
		r := results[0]
		results = results[1:]
		return r
	}, &calls
}

func code(n int) *int { return &n }

const nodeRepoDigest = "node@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func ok(stdout string) hostcmd.Result { return hostcmd.Result{ExitCode: code(0), Stdout: stdout} }

// wantCode asserts err is a ResolutionError with this code and message.
func wantCode(t *testing.T, err error, c, message string) {
	t.Helper()
	var re *ResolutionError
	if !errors.As(err, &re) || re.Code != c || re.Message != message || err.Error() != message {
		t.Fatalf("got %v, want %s: %s", err, c, message)
	}
}

func TestCatalogAndSelection(t *testing.T) {
	cat := Catalog()
	if len(cat) != 3 || cat[0].Tag != "node:22-alpine" || cat[1].Tag != "python:3.13-alpine" || cat[2].Tag != "golang:1.24-alpine" ||
		cat[0].DisplayName != "Node.js 22 (Alpine)" || cat[1].DisplayName != "Python 3.13 (Alpine)" || cat[2].DisplayName != "Go 1.24 (Alpine)" {
		t.Fatalf("%+v", cat)
	}
	for req, alias := range map[string]string{"node": "node", "python:3.13-alpine": "python", "golang:1.24-alpine": "go"} {
		rt, err := SelectCuratedRuntime(req)
		if err != nil || rt.Alias != alias {
			t.Fatalf("%s: %+v %v", req, rt, err)
		}
	}
	_, err := SelectCuratedRuntime("node:latest")
	wantCode(t, err, "UNKNOWN_RUNTIME", `Unknown curated runtime "node:latest". Choose node, python, or go.`)
	cat[0].Tag = "changed"
	if Catalog()[0].Tag != "node:22-alpine" {
		t.Fatal("Catalog shares its backing array")
	}
}

func TestResolveReturnsLocalRepoDigest(t *testing.T) {
	run, calls := recording(ok(`["` + nodeRepoDigest + `"]`))
	v, err := ResolveCuratedRuntime("node", run)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"runtime":{"alias":"node","displayName":"Node.js 22 (Alpine)","tag":"node:22-alpine"},"requested":"node","image":"` + nodeRepoDigest + `"}`
	if got := jsjson.Stringify(v); got != want {
		t.Fatal(got)
	}
	c := (*calls)[0]
	if strings.Join(c.args, " ") != "image inspect --format {{json .RepoDigests}} node:22-alpine" || c.timeout != 5*time.Second || c.max != 64*1024 {
		t.Fatalf("%+v", c)
	}
}

func TestResolveAcceptsDockerIOSpelling(t *testing.T) {
	d := "docker.io/library/node@sha256:" + strings.Repeat("a", 64)
	run, calls := scripted(hostcmd.Result{ExitCode: code(0), Stdout: `["` + d + `"]` + "\n"})
	v, err := ResolveCuratedRuntime("node", run)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join((*calls)[0], " "); got != "image inspect --format {{json .RepoDigests}} node:22-alpine" {
		t.Fatal(got)
	}
	if v.Get("image").Str() != d {
		t.Fatalf("%v", v)
	}
}

func TestUnknownRuntimeRejectedBeforeDocker(t *testing.T) {
	run, calls := scripted()
	_, err := ResolveCuratedRuntime("ruby", run)
	if err == nil || err.Error() != `Unknown curated runtime "ruby". Choose node, python, or go.` || len(*calls) != 0 {
		t.Fatalf("%v %v", err, *calls)
	}
	_, err = PrepareCuratedRuntime("ruby", run)
	wantCode(t, err, "UNKNOWN_RUNTIME", `Unknown curated runtime "ruby". Choose node, python, or go.`)
	if len(*calls) != 0 {
		t.Fatal(*calls)
	}
}

func TestNoImmutableRepoDigest(t *testing.T) {
	const msg = "Docker did not return a digest-pinned RepoDigest for node:22-alpine. Pull it explicitly, then try again."
	for _, stdout := range []string{`["node:22-alpine"]`, "node@sha256:not-a-real-digest", `["node:22-alpine@sha256:` + strings.Repeat("d", 64) + `"]`, "[]", "", `{"a":1}`, `["` + nodeRepoDigest + `",1]`} {
		run, _ := scripted(ok(stdout))
		_, err := ResolveCuratedRuntime("node", run)
		wantCode(t, err, "UNRESOLVED_REPO_DIGEST", msg)
	}
}

func TestWrongRepository(t *testing.T) {
	run, _ := scripted(ok(`["evil/node@sha256:` + strings.Repeat("c", 64) + `"]`))
	_, err := ResolveCuratedRuntime("node", run)
	wantCode(t, err, "UNEXPECTED_REPOSITORY", "Docker returned a RepoDigest for a repository other than curated node:22-alpine.")
}

func TestInspectTimeoutAndOutputLimit(t *testing.T) {
	run, _ := scripted(hostcmd.Result{TimedOut: true, OutputLimitExceeded: true})
	_, err := ResolveCuratedRuntime("go", run)
	wantCode(t, err, "DOCKER_INSPECT_TIMED_OUT", "Docker image inspection timed out for golang:1.24-alpine.")
	run, _ = scripted(hostcmd.Result{OutputLimitExceeded: true, ExitCode: code(0)})
	_, err = ResolveCuratedRuntime("node", run)
	wantCode(t, err, "DOCKER_INSPECT_OUTPUT_LIMIT", "Docker image inspection exceeded FaultLine's 65536-byte output limit for node:22-alpine.")
}

func TestInspectFailure(t *testing.T) {
	run, _ := scripted(hostcmd.Result{ExitCode: code(1), Stderr: "No such image"})
	_, err := ResolveCuratedRuntime("python", run)
	wantCode(t, err, "DOCKER_INSPECT_FAILED", "Docker could not inspect the local python:3.13-alpine image: No such image")
	// A nil exit code is a failure; blank stderr adds no detail.
	run, _ = scripted(hostcmd.Result{Stdout: `["` + nodeRepoDigest + `"]`, Stderr: " \r\n "})
	_, err = ResolveCuratedRuntime("node", run)
	wantCode(t, err, "DOCKER_INSPECT_FAILED", "Docker could not inspect the local node:22-alpine image")
	// A rejected runner.
	_, err = ResolveCuratedRuntime("node", func([]string, time.Duration, int) hostcmd.Result {
		jsexc.Throw(errors.New("boom"))
		return hostcmd.Result{}
	})
	wantCode(t, err, "DOCKER_INSPECT_FAILED", "FaultLine could not inspect the local node:22-alpine image: boom")
}

func TestFailureDetail(t *testing.T) {
	// CR/LF runs collapse to one space, the result is trimmed, then cut to
	// 320 UTF-16 units (an astral character at the cut leaves its lead
	// surrogate).
	stderr := "\n a\r\n\r\nb\n" + strings.Repeat("x", 315) + "😀z"
	run, _ := scripted(hostcmd.Result{ExitCode: code(2), Stderr: stderr})
	_, err := ResolveCuratedRuntime("node", run)
	want := "Docker could not inspect the local node:22-alpine image: a b " + strings.Repeat("x", 315) + "\xed\xa0\xbd"
	wantCode(t, err, "DOCKER_INSPECT_FAILED", want)
}

func TestPullThenResolve(t *testing.T) {
	run, calls := recording(hostcmd.Result{ExitCode: code(0)}, ok(`["`+nodeRepoDigest+`"]`))
	v, err := PrepareCuratedRuntime("node", run)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"runtime":{"alias":"node","displayName":"Node.js 22 (Alpine)","tag":"node:22-alpine"},"requested":"node","image":"` + nodeRepoDigest +
		`","pull":{"tag":"node:22-alpine","resolvedRepoDigest":"` + nodeRepoDigest + `"}}`
	if got := jsjson.Stringify(v); got != want {
		t.Fatal(got)
	}
	if len(*calls) != 2 {
		t.Fatal(*calls)
	}
	p, i := (*calls)[0], (*calls)[1]
	if strings.Join(p.args, " ") != "pull node:22-alpine" || p.timeout != 5*time.Minute || p.max != 64*1024 {
		t.Fatalf("%+v", p)
	}
	if strings.Join(i.args, " ") != "image inspect --format {{json .RepoDigests}} node:22-alpine" || i.timeout != 5*time.Second || i.max != 64*1024 {
		t.Fatalf("%+v", i)
	}
}

func TestPullFailure(t *testing.T) {
	run, calls := scripted(hostcmd.Result{ExitCode: code(1), Stderr: "daemon unavailable"})
	_, err := PrepareCuratedRuntime("node", run)
	wantCode(t, err, "DOCKER_PULL_FAILED", "Docker could not pull the reviewed node:22-alpine image: daemon unavailable")
	if len(*calls) != 1 {
		t.Fatal(*calls)
	}
	run, _ = scripted(hostcmd.Result{TimedOut: true})
	_, err = PrepareCuratedRuntime("node", run)
	wantCode(t, err, "DOCKER_PULL_TIMED_OUT", "Docker image pull timed out for node:22-alpine.")
	run, _ = scripted(hostcmd.Result{OutputLimitExceeded: true})
	_, err = PrepareCuratedRuntime("go", run)
	wantCode(t, err, "DOCKER_PULL_OUTPUT_LIMIT", "Docker image pull exceeded FaultLine's 65536-byte output limit for golang:1.24-alpine.")
	_, err = PrepareCuratedRuntime("python", func([]string, time.Duration, int) hostcmd.Result {
		jsexc.Throw(errors.New("no docker"))
		return hostcmd.Result{}
	})
	wantCode(t, err, "DOCKER_PULL_FAILED", "FaultLine could not pull the reviewed python:3.13-alpine image: no docker")
	run, _ = scripted(hostcmd.Result{ExitCode: code(0)}, ok("[]"))
	_, err = PrepareCuratedRuntime("node", run)
	wantCode(t, err, "UNRESOLVED_REPO_DIGEST", "Docker did not return a digest-pinned RepoDigest for node:22-alpine. Pull it explicitly, then try again.")
}

func TestRepoDigestHelpers(t *testing.T) {
	for in, want := range map[string]string{
		"node": "library/node", "docker.io/library/node": "library/node", "index.docker.io/node": "library/node",
		"index.docker.io/docker.io/node": "library/node", "docker.io/index.docker.io/node": "index.docker.io/node",
		"registry:5000/node": "registry:5000/node", "": "library/",
	} {
		if got := normalizeRepository(in); got != want {
			t.Errorf("normalizeRepository(%q) = %q, want %q", in, got, want)
		}
	}
	d := "@sha256:" + strings.Repeat("e", 64)
	for in, want := range map[string]bool{"node" + d: true, "registry:5000/node" + d: true, "node:1" + d: false, "a/b:1" + d: false, "-x" + d: false, "node@sha256:E": false} {
		if got := isDigestPinnedRepoDigest(in); got != want {
			t.Errorf("isDigestPinnedRepoDigest(%q) = %v", in, got)
		}
	}
	if got := parseRepoDigests(" [\"a\",\"b\"] "); strings.Join(got, ",") != "a,b" {
		t.Fatal(got)
	}
	for _, in := range []string{"", "  ", "[1]", "null", "{", `"a"`, `["a",null]`} {
		if got := parseRepoDigests(in); len(got) != 0 {
			t.Fatalf("%q: %v", in, got)
		}
	}
}

func TestMaxOutputBytesFor(t *testing.T) {
	for args, want := range map[string]int{"pull x": 65536, "build x": 131072, "image inspect": 65536, "": 65536} {
		if got := maxOutputBytesFor(strings.Fields(args)); got != want {
			t.Fatalf("%q: %d", args, got)
		}
	}
}
