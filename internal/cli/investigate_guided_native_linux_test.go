//go:build linux && faultline_native_docker

package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/servefixture"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// Native Linux/Docker qualification of the ordinary guided investigate flow.
// It drives the ordinary `fl` built from ./cmd/fl (no build tags, no injected
// runner, no fake Docker, no provider) against an owned two-commit Git
// repository, a real loopback human review/freeze, the real Docker daemon and
// the pinned image, then re-verifies the exported bundle in a second ordinary
// `fl verify` process. This file is compiled only under the explicit
// faultline_native_docker tag; under that tag a missing prerequisite is a hard
// failure, never a skip.

const (
	// ngpImage is the CI-approved node:22-alpine image index (difftest/docker_live_test.go).
	ngpImage       = "node:22-alpine@sha256:0a7108bf6c7bf5de370ffb1a3ed6be93d405b43ff159f681a8d18c0e2bc2e402"
	ngpImageDigest = "sha256:0a7108bf6c7bf5de370ffb1a3ed6be93d405b43ff159f681a8d18c0e2bc2e402"
	ngpEvidenceEnv = "FAULTLINE_NATIVE_PROOF_EVIDENCE"
	ngpIncident    = "native-1"
	ngpReviewer    = "Native Proof Reviewer"
	// ngpTotal bounds the driver's own work; ngpCleanup is reserved for process and
	// container cleanup before the outer `go test -timeout=195s`.
	ngpTotal   = 150 * time.Second
	ngpCleanup = 30 * time.Second
	// executionsPerState is gitproof.StableExecutionCount (3); two states give six runs.
	ngpPerState = 3
	ngpRuns     = 6
)

// ngpPredicate is the self-contained witness: no overlay, exit code and structured result agree.
const ngpPredicate = `if [ "$(cat state.txt)" = bad ]; then
  echo 'FAULTLINE_WITNESS_RESULT={"protocol":"faultline.witness-result.v1","outcome":"PREDICATE_FAIL"}'
  exit 1
fi
echo 'FAULTLINE_WITNESS_RESULT={"protocol":"faultline.witness-result.v1","outcome":"PREDICATE_PASS"}'`

var (
	ngpSHA256      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	ngpCommit      = regexp.MustCompile(`^[0-9a-f]{40}$`)
	ngpOwnedName   = regexp.MustCompile(`^faultline-[0-9a-f]{32}$`)
	ngpTokenField  = regexp.MustCompile(`name="token" value="([0-9a-f]{64})"`)
	ngpDigestField = regexp.MustCompile(`name="reviewDigest" value="(sha256:[0-9a-f]{64})"`)
	ngpReadyStderr = regexp.MustCompile(`^` + regexp.QuoteMeta(guidedProcessPhasePreflight+"FaultLine guided investigate: INTAKE (ci-log)\n"+guidedProcessPhaseReview) +
		`FaultLine local witness review: (http://127\.0\.0\.1:[1-9][0-9]*)` + regexp.QuoteMeta(guidedProcessReadyText) + `$`)
)

// ngpEvidenceDir collects everything retained for diagnosis. Files are created
// exclusively; nothing is overwritten or deleted.
type ngpEvidenceDir struct {
	t    *testing.T
	root string
}

func (e *ngpEvidenceDir) put(rel string, data []byte) {
	e.t.Helper()
	path := filepath.Join(e.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Errorf("evidence mkdir %s: %v", rel, err)
		return
	}
	for n := 0; ; n++ {
		name := path
		if n > 0 {
			name = path + "." + strconv.Itoa(n)
		}
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			e.t.Errorf("evidence create %s: %v", rel, err)
			return
		}
		if _, err := f.Write(data); err != nil {
			e.t.Errorf("evidence write %s: %v", rel, err)
		}
		if err := f.Close(); err != nil {
			e.t.Errorf("evidence close %s: %v", rel, err)
		}
		return
	}
}

// ngpCopyTree copies regular files and directories into a destination that must not hold the files yet.
func ngpCopyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case info.Mode().IsRegular():
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			defer in.Close()
			out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, in); err != nil {
				out.Close()
				return err
			}
			return out.Close()
		default:
			return os.WriteFile(target+".nonregular.txt", []byte(info.Mode().String()+"\n"), 0o644)
		}
	})
}

// ngpChildEnv is the real environment (real PATH, Node, Docker) minus every
// FaultLine test/seed/clock/recorder override, provider credential and Git
// override. Only the scrubbed key names are returned for the receipt.
func ngpChildEnv() (env, scrubbed []string) {
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		up := strings.ToUpper(key)
		if strings.HasPrefix(up, "FAULTLINE_") || strings.HasPrefix(up, "OPENAI_") || strings.HasPrefix(up, "ANTHROPIC_") ||
			strings.HasPrefix(up, "CODEX_") || strings.HasPrefix(up, "GIT_") {
			scrubbed = append(scrubbed, key)
			continue
		}
		env = append(env, kv)
	}
	return env, scrubbed
}

type ngpResult struct {
	stdout, stderr string
	exit           int
	err            error
}

// ngpExec runs argv to completion under ctx and retains its streams and status.
func ngpExec(ctx context.Context, ev *ngpEvidenceDir, label, dir string, env []string, argv ...string) ngpResult {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir, cmd.Env = dir, env
	cmd.WaitDelay = 5 * time.Second
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	r := ngpResult{stdout: out.String(), stderr: errb.String(), exit: -1}
	if cmd.ProcessState != nil {
		r.exit = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		if _, isExit := err.(*exec.ExitError); !isExit {
			r.err = err
		}
	}
	// A signal, kill or context cancellation reports exit -1: never a clean status.
	if r.err == nil && (r.exit < 0 || ctx.Err() != nil) {
		r.err = fmt.Errorf("terminated abnormally (exit %d, context: %v, run: %v)", r.exit, ctx.Err(), err)
	}
	ev.put("receipts/"+label+".stdout", []byte(r.stdout))
	ev.put("receipts/"+label+".stderr", []byte(r.stderr))
	ev.put("receipts/"+label+".exit", []byte(fmt.Sprintf("argv: %q\ndir: %q\nexit: %d\nerror: %v\n", argv, dir, r.exit, r.err)))
	return r
}

type ngpContainer struct{ id, name, image string }

func ngpContainers(ctx context.Context, ev *ngpEvidenceDir, label, docker string, env []string) (map[string]ngpContainer, error) {
	r := ngpExec(ctx, ev, label, "", env, docker, "ps", "-a", "--no-trunc", "--format", "{{.ID}}|{{.Names}}|{{.Image}}")
	if r.err != nil || r.exit != 0 {
		return nil, fmt.Errorf("docker ps -a: exit %d: %v: %s", r.exit, r.err, r.stderr)
	}
	out := map[string]ngpContainer{}
	for _, line := range strings.Split(strings.TrimSpace(r.stdout), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("unparsable docker ps line %q", line)
		}
		out[parts[0]] = ngpContainer{parts[0], parts[1], parts[2]}
	}
	return out, nil
}

func ngpNeed(t *testing.T, v jsjson.Value, path ...string) jsjson.Value {
	t.Helper()
	got := v.Get(path...)
	if got.Kind() == jsjson.Undefined {
		t.Fatalf("missing field %s", strings.Join(path, "."))
	}
	return got
}

func ngpStr(t *testing.T, v jsjson.Value, path ...string) string {
	t.Helper()
	got := ngpNeed(t, v, path...)
	if got.Kind() != jsjson.String {
		t.Fatalf("field %s is not a string", strings.Join(path, "."))
	}
	return got.Str()
}

func ngpNum(t *testing.T, v jsjson.Value, path ...string) float64 {
	t.Helper()
	got := ngpNeed(t, v, path...)
	if got.Kind() != jsjson.Number {
		t.Fatalf("field %s is not a number", strings.Join(path, "."))
	}
	return got.Num()
}

func ngpBool(t *testing.T, v jsjson.Value, path ...string) bool {
	t.Helper()
	got := ngpNeed(t, v, path...)
	if got.Kind() != jsjson.Bool {
		t.Fatalf("field %s is not a boolean", strings.Join(path, "."))
	}
	return got.Bool()
}

func ngpArr(t *testing.T, v jsjson.Value, path ...string) []jsjson.Value {
	t.Helper()
	got := ngpNeed(t, v, path...)
	if got.Kind() != jsjson.Array {
		t.Fatalf("field %s is not an array", strings.Join(path, "."))
	}
	return got.Items()
}

func ngpWantStr(t *testing.T, v jsjson.Value, want string, path ...string) {
	t.Helper()
	if got := ngpStr(t, v, path...); got != want {
		t.Fatalf("%s = %q, want %q", strings.Join(path, "."), got, want)
	}
}

func ngpWantBool(t *testing.T, v jsjson.Value, want bool, path ...string) {
	t.Helper()
	if got := ngpBool(t, v, path...); got != want {
		t.Fatalf("%s = %v, want %v", strings.Join(path, "."), got, want)
	}
}

func ngpWantNum(t *testing.T, v jsjson.Value, want float64, path ...string) {
	t.Helper()
	if got := ngpNum(t, v, path...); got != want {
		t.Fatalf("%s = %v, want %v", strings.Join(path, "."), got, want)
	}
}

func ngpReadJSON(t *testing.T, path string) jsjson.Value {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsjson.Parse(string(b))
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return v
}

func ngpCanonical(t *testing.T, v jsjson.Value) string {
	t.Helper()
	text, err := canonical.CanonicalJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func ngpDigest(t *testing.T, v jsjson.Value) string {
	t.Helper()
	d, err := canonical.DigestJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func ngpExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// TestNativeGuidedInvestigateDocker is the actual native Linux/Docker proof of the guided flow.
func TestNativeGuidedInvestigateDocker(t *testing.T) {
	started := time.Now()
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Fatalf("native guided proof requires a real Docker CLI: %v", err)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("native guided proof requires git: %v", err)
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("native guided proof requires Node on PATH: %v", err)
	}
	goPath, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("native guided proof requires go to build the ordinary fl: %v", err)
	}
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	evidenceRoot := os.Getenv(ngpEvidenceEnv)
	if evidenceRoot == "" || !filepath.IsAbs(evidenceRoot) {
		t.Fatalf("%s must name an absolute evidence directory", ngpEvidenceEnv)
	}
	if err := os.MkdirAll(evidenceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(evidenceRoot); err != nil || len(entries) != 0 {
		t.Fatalf("%s must be an empty directory (never reused): entries=%d err=%v", ngpEvidenceEnv, len(entries), err)
	}
	ev := &ngpEvidenceDir{t: t, root: evidenceRoot}

	deadline := started.Add(ngpTotal)
	if d, ok := t.Deadline(); ok {
		if limit := d.Add(-ngpCleanup - 5*time.Second); limit.Before(deadline) {
			deadline = limit
		}
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	// Deterministic file modes for the materialized tree the unprivileged
	// container user (65534) reads through a read-only bind mount.
	oldUmask := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(oldUmask) })

	childEnv, scrubbed := ngpChildEnv()

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if repo, err = filepath.EvalSymlinks(repo); err != nil {
		t.Fatal(err)
	}
	if rel, err := filepath.Rel(repo, evidenceRoot); err == nil && !strings.HasPrefix(rel, "..") {
		t.Fatalf("evidence root %s must be outside the target repository %s", evidenceRoot, repo)
	}
	ciLog := filepath.Join(work, "ci.log")
	binDir := filepath.Join(work, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(binDir, "fl")

	var (
		flResult      *servefixture.ProcessResult
		streamsStored bool
		bundleDir     string
		bundleRoot    string
		removed       []string
		newContainers []string
		beforeTaken   bool
		before        map[string]ngpContainer
		process       *servefixture.Started
	)

	// Registered first, so it runs last: receipt and retained fixture, after the
	// process and container cleanups below.
	t.Cleanup(func() {
		finished := time.Now()
		if err := ngpCopyTree(repo, filepath.Join(evidenceRoot, "fixture-repo")); err != nil {
			t.Errorf("retain fixture repository: %v", err)
		}
		if b, err := os.ReadFile(ciLog); err == nil {
			ev.put("fixture/ci.log", b)
		}
		if bundleDir != "" && ngpExists(bundleDir) {
			if err := ngpCopyTree(bundleDir, filepath.Join(evidenceRoot, "proof-bundle")); err != nil {
				t.Errorf("retain proof bundle: %v", err)
			}
		}
		head := os.Getenv("GITHUB_SHA")
		if head == "" {
			if r := ngpExec(context.Background(), ev, "source-head", moduleRoot, childEnv, gitPath, "rev-parse", "HEAD"); r.exit == 0 {
				head = strings.TrimSpace(r.stdout)
			}
		}
		receipt := map[string]any{
			"test": "TestNativeGuidedInvestigateDocker", "passed": !t.Failed(), "sourceHead": head,
			"image": ngpImage, "startedAt": started.UTC().Format(time.RFC3339Nano), "finishedAt": finished.UTC().Format(time.RFC3339Nano),
			"durationMs": finished.Sub(started).Milliseconds(), "dockerPath": docker, "flStreamsStored": streamsStored,
			"bundleDirectory": bundleDir, "bundleRoot": bundleRoot, "containersNew": newContainers, "containersRemoved": removed,
			"scrubbedEnvKeys": scrubbed, "deadline": deadline.UTC().Format(time.RFC3339Nano),
		}
		if flResult != nil {
			receipt["flExitCode"], receipt["flSignal"] = flResult.ExitCode, flResult.Signal
		}
		b, err := json.MarshalIndent(receipt, "", "  ")
		if err != nil {
			t.Errorf("receipt: %v", err)
			return
		}
		ev.put("outcome-receipt.json", append(b, '\n'))
	})

	// Prerequisite receipts (real versions, daemon, exact image identity).
	setup, setupCancel := context.WithDeadline(ctx, started.Add(40*time.Second))
	for _, c := range [][]string{
		{"docker-cli-version", docker, "--version"},
		{"git-version", gitPath, "--version"},
		{"go-version", goPath, "version"},
		{"node-version", "node", "--version"},
		{"uname", "uname", "-a"},
	} {
		if r := ngpExec(setup, ev, c[0], "", childEnv, c[1:]...); r.err != nil || r.exit != 0 {
			setupCancel()
			t.Fatalf("%s failed: exit %d err %v: %s", c[0], r.exit, r.err, r.stderr)
		}
	}
	dockerVersion := ngpExec(setup, ev, "docker-version", "", childEnv, docker, "version")
	if dockerVersion.err != nil || dockerVersion.exit != 0 {
		setupCancel()
		t.Fatalf("the Docker daemon is not reachable: exit %d err %v\n%s%s", dockerVersion.exit, dockerVersion.err, dockerVersion.stdout, dockerVersion.stderr)
	}
	if strings.Contains(strings.ToLower(dockerVersion.stdout+dockerVersion.stderr), "podman") {
		setupCancel()
		t.Fatalf("Podman is not Docker; native proof requires a real Docker daemon:\n%s", dockerVersion.stdout)
	}
	if r := ngpExec(setup, ev, "docker-info", "", childEnv, docker, "info"); r.err != nil || r.exit != 0 {
		setupCancel()
		t.Fatalf("docker info failed: exit %d err %v: %s", r.exit, r.err, r.stderr)
	}
	if r := ngpExec(setup, ev, "node-version-check", "", childEnv, "node", "--version"); !strings.HasPrefix(r.stdout, "v22.") {
		setupCancel()
		t.Fatalf("Node 22 is required on PATH, got %q", r.stdout)
	}
	inspect := ngpExec(setup, ev, "image-inspect", "", childEnv, docker, "image", "inspect", ngpImage)
	if inspect.err != nil || inspect.exit != 0 {
		setupCancel()
		t.Fatalf("image %s is not present (pull it first; investigate runs --pull=never): exit %d: %s", ngpImage, inspect.exit, inspect.stderr)
	}
	image, parseErr := jsjson.Parse(inspect.stdout)
	if parseErr != nil || image.Kind() != jsjson.Array || len(image.Items()) != 1 {
		setupCancel()
		t.Fatalf("unexpected docker image inspect output: %v\n%s", parseErr, inspect.stdout)
	}
	configImageID := ngpStr(t, image.Items()[0], "Id")
	if !ngpSHA256.MatchString(configImageID) {
		setupCancel()
		t.Fatalf("image config ID %q is not a sha256 digest", configImageID)
	}
	bound := false
	for _, d := range ngpArr(t, image.Items()[0], "RepoDigests") {
		if d.Kind() == jsjson.String && strings.HasSuffix(d.Str(), "@"+ngpImageDigest) {
			bound = true
		}
	}
	if !bound {
		setupCancel()
		t.Fatalf("the local image is not bound to the pinned index digest %s (RepoDigests=%s)", ngpImageDigest, ngpCanonical(t, ngpNeed(t, image.Items()[0], "RepoDigests")))
	}
	ngpWantStr(t, image.Items()[0], "linux", "Os")
	before, err = ngpContainers(setup, ev, "containers-before", docker, childEnv)
	setupCancel()
	if err != nil {
		t.Fatal(err)
	}
	beforeTaken = true

	// Container inventory: only new, positively identified FaultLine containers are ever removed.
	t.Cleanup(func() {
		if !beforeTaken {
			return
		}
		cctx, cc := context.WithTimeout(context.Background(), ngpCleanup-5*time.Second)
		defer cc()
		after, err := ngpContainers(cctx, ev, "containers-after", docker, childEnv)
		if err != nil {
			t.Errorf("container inventory after the test: %v", err)
			return
		}
		var lines []string
		for id, c := range after {
			if _, existed := before[id]; existed {
				continue
			}
			newContainers = append(newContainers, c.id)
			lines = append(lines, c.id+"|"+c.name+"|"+c.image)
			if ngpOwnedName.MatchString(c.name) {
				if r := ngpExec(cctx, ev, "cleanup-rm-"+c.id[:12], "", childEnv, docker, "rm", "--force", c.id); r.err == nil && r.exit == 0 {
					removed = append(removed, c.id)
				}
			}
		}
		slices.Sort(lines)
		ev.put("containers-new.txt", []byte(strings.Join(lines, "\n")+"\n"))
		if len(newContainers) > 0 {
			t.Errorf("containers created during the test remained afterwards (owned ones were removed and recorded): %v", lines)
		}
	})

	// Build the ordinary fl (no tags, no GOFLAGS) outside the target repository.
	buildCtx, buildCancel := context.WithDeadline(ctx, started.Add(100*time.Second))
	build := ngpExec(buildCtx, ev, "go-build-fl", moduleRoot, deterministicBuildEnv(), goPath, "build", "-p", "1", "-o", binary, "./cmd/fl")
	buildCancel()
	if build.err != nil || build.exit != 0 {
		t.Fatalf("go build ./cmd/fl: exit %d err %v\n%s", build.exit, build.err, build.stderr)
	}

	// Owned two-commit fixture with fixed dates; only state.txt changes. .faultline/ is
	// ignored in both states so the worktree stays clean for the doctor preflight.
	gitEnv := func(date string) []string {
		return append(slices.Clone(childEnv), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=FaultLine Native Proof", "GIT_AUTHOR_EMAIL=native-proof@example.invalid",
			"GIT_COMMITTER_NAME=FaultLine Native Proof", "GIT_COMMITTER_EMAIL=native-proof@example.invalid",
			"GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	}
	gitCount := 0
	git := func(date string, args ...string) string {
		t.Helper()
		gitCount++
		r := ngpExec(ctx, ev, fmt.Sprintf("git-%02d", gitCount), repo, gitEnv(date), append([]string{gitPath}, args...)...)
		if r.err != nil || r.exit != 0 {
			t.Fatalf("git %v: exit %d err %v\n%s", args, r.exit, r.err, r.stderr)
		}
		return strings.TrimSpace(r.stdout)
	}
	const goodDate, badDate = "2026-07-16T10:00:00Z", "2026-07-16T10:01:00Z"
	git(goodDate, "init", "-q", "-b", "main")
	git(goodDate, "config", "--local", "commit.gpgsign", "false")
	git(goodDate, "config", "--local", "core.autocrlf", "false")
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", ".faultline/\n")
	write("state.txt", "good\n")
	git(goodDate, "add", ".gitignore", "state.txt")
	git(goodDate, "commit", "-q", "-m", "good")
	good := git(goodDate, "rev-parse", "HEAD")
	write("state.txt", "bad\n")
	git(badDate, "add", "state.txt")
	git(badDate, "commit", "-q", "-m", "bad")
	bad := git(badDate, "rev-parse", "HEAD")
	if !ngpCommit.MatchString(good) || !ngpCommit.MatchString(bad) || good == bad {
		t.Fatalf("fixture commits: good=%q bad=%q", good, bad)
	}
	if n := git(badDate, "rev-list", "--count", "HEAD"); n != "2" {
		t.Fatalf("fixture must have exactly two commits, has %s", n)
	}
	if changed := git(badDate, "diff", "--name-only", good, bad); changed != "state.txt" {
		t.Fatalf("fixture commits must differ only in state.txt, differ in %q", changed)
	}
	if dirty := git(badDate, "status", "--porcelain=v1", "--untracked-files=all"); dirty != "" {
		t.Fatalf("fixture worktree is not clean: %q", dirty)
	}
	if err := os.WriteFile(ciLog, []byte("FAIL state.txt expected good received bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Start the ordinary guided flow; it stops at the real review readiness.
	args := []string{"investigate", "--ci-log", ciLog, "--repo", repo, "--from", good, "--to", bad, "--id", ngpIncident,
		"--port", "0", "--command", ngpPredicate, "--image", ngpImage}
	ev.put("fl-argv.txt", []byte(fmt.Sprintf("%q\n", append([]string{binary}, args...))))
	startCtx, startCancel := context.WithDeadline(ctx, started.Add(120*time.Second))
	process, err = servefixture.StartWithOptions(startCtx, binary, nil, args, childEnv, repo, servefixture.StartOptions{
		StartupLines: 5, StartupDescription: "guided review readiness", ReadyStream: "stderr",
		ValidateStartup: func(out []byte) (string, error) {
			m := ngpReadyStderr.FindStringSubmatch(string(out))
			if m == nil {
				return "", fmt.Errorf("unexpected readiness stderr %q", out)
			}
			return m[1], nil
		},
	})
	startCancel()
	if err != nil {
		ev.put("fl-start-error.txt", []byte(err.Error()+"\n"))
		t.Fatalf("start guided investigate: %v", err)
	}
	t.Cleanup(func() {
		if err := process.Dispose(); err != nil {
			t.Errorf("teardown: %v", err)
		}
		if streamsStored {
			return
		}
		wctx, wc := context.WithTimeout(context.Background(), 10*time.Second)
		defer wc()
		if res, err := process.Wait(wctx); err == nil {
			ev.put("fl-investigate.stdout", []byte(res.Stdout))
			ev.put("fl-investigate.stderr", []byte(res.Stderr))
			flResult = &res
			streamsStored = true
		}
	})

	// Pending chain: draft and proposal only, nothing approved, frozen, bound or exported.
	faultline := filepath.Join(repo, ".faultline")
	draftPath := filepath.Join(faultline, "incidents", "drafts", ngpIncident+".json")
	proposalPath := filepath.Join(faultline, "witnesses", "proposals", ngpIncident+".json")
	approvalPath := filepath.Join(faultline, "witnesses", "approvals", ngpIncident+".json")
	frozenPath := filepath.Join(faultline, "witnesses", "frozen", ngpIncident+".json")
	draftBefore, err1 := os.ReadFile(draftPath)
	proposalBefore, err2 := os.ReadFile(proposalPath)
	if err1 != nil || err2 != nil {
		t.Fatalf("pending draft/proposal missing: %v %v", err1, err2)
	}
	ev.put("pending/draft.json", draftBefore)
	ev.put("pending/proposal.json", proposalBefore)
	for _, p := range []string{approvalPath, frozenPath, filepath.Join(faultline, "incidents", "bindings", ngpIncident+".json"), filepath.Join(faultline, "git-proof-bundles")} {
		if ngpExists(p) {
			t.Fatalf("%s exists before any human action", p)
		}
	}

	// Real review page and explicit fixture human action over HTTP.
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second}
	getReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, process.URL+"/", nil)
	getResp, err := client.Do(getReq)
	if err != nil {
		t.Fatal(err)
	}
	pageBytes, err := io.ReadAll(getResp.Body)
	getResp.Body.Close()
	page := string(pageBytes)
	if err != nil || getResp.StatusCode != http.StatusOK || !strings.Contains(page, `action="/approve-freeze"`) || !strings.Contains(page, "NOT APPROVED") {
		t.Fatalf("review page: status %d err %v\n%s", getResp.StatusCode, err, page)
	}
	ev.put("review-page.html", pageBytes)
	token, reviewDigest := ngpTokenField.FindStringSubmatch(page), ngpDigestField.FindStringSubmatch(page)
	if token == nil || reviewDigest == nil {
		t.Fatalf("review form token/digest missing:\n%s", page)
	}
	form := url.Values{"token": {token[1]}, "reviewDigest": {reviewDigest[1]}, "approvedBy": {ngpReviewer}, "acknowledged": {"exact-witness-reviewed"}}
	postReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, process.URL+"/approve-freeze", strings.NewReader(form.Encode()))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postResp, err := client.Do(postReq)
	if err != nil {
		t.Fatal(err)
	}
	postBody, _ := io.ReadAll(postResp.Body)
	postResp.Body.Close()
	ev.put("approve-freeze-response.html", postBody)
	if postResp.StatusCode != http.StatusOK || !strings.Contains(string(postBody), "Approved and frozen as a write-once record.") {
		t.Fatalf("approve-freeze: status %d\n%s", postResp.StatusCode, postBody)
	}

	// Join the process: the flow continues by itself through localization and export.
	waitCtx, waitCancel := context.WithDeadline(ctx, started.Add(140*time.Second))
	res, err := process.Wait(waitCtx)
	waitCancel()
	if err != nil {
		t.Fatalf("guided investigate did not finish: %v", err)
	}
	flResult, streamsStored = &res, true
	ev.put("fl-investigate.stdout", []byte(res.Stdout))
	ev.put("fl-investigate.stderr", []byte(res.Stderr))
	wantStderr := guidedProcessPhasePreflight + "FaultLine guided investigate: INTAKE (ci-log)\n" + guidedProcessPhaseReview +
		"FaultLine local witness review: " + process.URL + guidedProcessReadyText + guidedProcessPhaseRuntime +
		"FaultLine guided investigate: LOCALIZATION\nFaultLine guided investigate: PROOF_EXPORT\n"
	if res.ExitCode == nil || *res.ExitCode != 0 || res.Signal != nil {
		t.Fatalf("fl exit: code=%v signal=%v\nstdout=%s\nstderr=%s", res.ExitCode, res.Signal, res.Stdout, res.Stderr)
	}
	if res.Stderr != wantStderr {
		t.Fatalf("stderr phases differ:\n--- got\n%s\n--- want\n%s", res.Stderr, wantStderr)
	}
	result, err := jsjson.Parse(res.Stdout)
	if err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, res.Stdout)
	}

	// Guided envelope.
	ngpWantStr(t, result, "GUIDED_PROOF_BUNDLE_READY", "status")
	ngpWantBool(t, result, true, "proofReady")
	ngpWantStr(t, result, "READY", "doctor", "dockerInvestigationPreflight")
	for _, d := range ngpArr(t, result, "doctor", "diagnostics") {
		if ngpStr(t, d, "id") != "runtime" {
			ngpWantStr(t, d, "READY", "status")
		}
	}
	ngpWantStr(t, result, process.URL, "reviewUrl")
	retained := ngpStr(t, result, "retainedFrozenDigest")
	if !ngpSHA256.MatchString(retained) {
		t.Fatalf("retainedFrozenDigest %q", retained)
	}
	ngpWantStr(t, result, ngpIncident, "draft", "draftId")
	ngpWantStr(t, result, good, "draft", "range", "ancestor")
	ngpWantStr(t, result, bad, "draft", "range", "descendant")
	ngpWantStr(t, result, "EXPLICIT", "draft", "range", "source")
	ngpWantStr(t, result, strings.TrimSpace(ngpPredicate), "draft", "command")
	ngpWantStr(t, result, draftPath, "draftPath")

	cont := ngpNeed(t, result, "continuation")
	ngpWantStr(t, cont, "PROOF_BUNDLE_READY", "status")
	ngpWantStr(t, cont, ngpIncident, "incident", "id")
	ngpWantStr(t, cont, retained, "incident", "frozenDigest")
	ngpWantStr(t, cont, "MATCH", "incident", "frozenDigestExternalStatus")
	ngpWantStr(t, cont, "explicit", "incident", "runtime", "requested")
	ngpWantStr(t, cont, ngpImage, "incident", "runtime", "image")
	if len(ngpArr(t, cont, "limitations")) != 2 {
		t.Fatalf("limitations: %s", ngpCanonical(t, ngpNeed(t, cont, "limitations")))
	}

	// Investigation: every field, every run.
	inv := ngpNeed(t, cont, "investigation")
	ngpWantStr(t, inv, "faultline.git-investigation.v1", "schemaVersion")
	ngpWantStr(t, inv, "git-commit-range-replay", "recorder")
	ngpWantStr(t, inv, "COMPLETED", "status")
	ngpWantNum(t, inv, ngpPerState, "executionsPerState")
	ngpWantBool(t, inv, true, "witness", "valid")
	ngpWantStr(t, inv, retained, "witness", "frozenDigest")
	ngpWantStr(t, inv, "MATCH", "witness", "externalDigestStatus")
	witnessDigest := ngpStr(t, inv, "witness", "witnessDigest")
	ngpWantStr(t, inv, ngpReviewer, "witness", "approval", "actor")
	ngpWantStr(t, inv, good, "resolvedRange", "ancestor", "commit")
	ngpWantStr(t, inv, bad, "resolvedRange", "descendant", "commit")
	if errs := ngpArr(t, inv, "errors"); len(errs) != 0 {
		t.Fatalf("investigation errors: %s", ngpCanonical(t, ngpNeed(t, inv, "errors")))
	}
	ngpWantStr(t, inv, "HOMOGENEOUS", "environment", "homogeneity")
	ngpWantBool(t, inv, false, "nonMonotonic")
	ngpWantBool(t, inv, true, "proof", "requiresDockerIsolation")
	ngpWantBool(t, inv, true, "proof", "dockerIsolated")
	ngpWantStr(t, inv, "NATIVE_DOCKER", "proof", "executionTrust")
	ngpWantBool(t, inv, true, "proof", "isProof")
	ngpWantNum(t, inv, 1, "proof", "proofTransitions")
	ngpWantStr(t, inv, "COMMIT_PROOF", "proof", "evidenceGrade")

	states := ngpArr(t, inv, "states")
	if len(states) != 2 {
		t.Fatalf("states: %d", len(states))
	}
	ngpWantStr(t, states[0], good, "commit")
	ngpWantStr(t, states[1], bad, "commit")

	runs := ngpArr(t, inv, "runs")
	if len(runs) != ngpRuns {
		t.Fatalf("runs: got %d, want %d (3 stable executions x 2 states)", len(runs), ngpRuns)
	}
	attempts := map[int]map[int]bool{0: {}, 1: {}}
	executionIDs, runIDs := map[string]bool{}, map[string]bool{}
	for i, run := range runs {
		state := int(ngpNum(t, run, "stateIndex"))
		if state != 0 && state != 1 {
			t.Fatalf("run %d: stateIndex %d", i, state)
		}
		attempt := int(ngpNum(t, run, "executionAttempt"))
		if attempt < 1 || attempt > ngpPerState || attempts[state][attempt] {
			t.Fatalf("run %d: bad or repeated attempt %d for state %d", i, attempt, state)
		}
		attempts[state][attempt] = true
		executionID, runID := ngpStr(t, run, "executionId"), ngpStr(t, run, "runId")
		if !ngpSHA256.MatchString(executionID) || !ngpSHA256.MatchString(runID) || executionIDs[executionID] || runIDs[runID] {
			t.Fatalf("run %d: invalid or repeated identifiers %s %s", i, executionID, runID)
		}
		executionIDs[executionID], runIDs[runID] = true, true
		wantCommit, verdict, reason, exit := good, "PASS", "PREDICATE_PASS", 0.0
		if state == 1 {
			wantCommit, verdict, reason, exit = bad, "FAIL", "PREDICATE_FAIL", 1.0
		}
		ngpWantStr(t, run, wantCommit, "commit")
		ngpWantStr(t, run, retained, "frozenDigest")
		ngpWantStr(t, run, witnessDigest, "witnessDigest")
		if len(ngpArr(t, run, "overlays")) != 0 {
			t.Fatalf("run %d: unexpected overlays", i)
		}
		ngpWantStr(t, run, "DOCKER_ISOLATED", "result", "kind")
		ngpWantStr(t, run, "NATIVE_DOCKER", "result", "executor")
		ngpWantStr(t, run, verdict, "result", "verdict")
		ngpWantStr(t, run, reason, "result", "reason")
		ngpWantNum(t, run, exit, "result", "exitCode")
		if ngpNeed(t, run, "result", "signal").Kind() != jsjson.Null {
			t.Fatalf("run %d: unexpected signal", i)
		}
		ngpWantBool(t, run, false, "result", "outputTruncated")
		if ngpNum(t, run, "result", "stdoutBytes") <= 0 {
			t.Fatalf("run %d: empty witness stdout", i)
		}
		ngpWantStr(t, run, "DOCKER_ISOLATED", "sandbox", "kind")
		// The sandbox audit binds the frozen record digest (gitinv planRequestForState), not the bare witness digest.
		ngpWantStr(t, run, retained, "sandbox", "witnessDigest")
		ngpWantStr(t, run, ngpImage, "sandbox", "runtime", "image")
		ngpWantStr(t, run, "/bin/sh", "sandbox", "runtime", "entrypoint")
		ngpWantStr(t, run, "none", "sandbox", "runtime", "network")
		ngpWantBool(t, run, true, "sandbox", "runtime", "rootFilesystemReadOnly")
		ngpWantStr(t, run, "65534:65534", "sandbox", "runtime", "user")
		ngpWantBool(t, run, true, "sandbox", "runtime", "capDropAll")
		ngpWantBool(t, run, true, "sandbox", "runtime", "noNewPrivileges")
		ngpWantStr(t, run, "never", "sandbox", "runtime", "pull")
	}
	for state := 0; state < 2; state++ {
		if len(attempts[state]) != ngpPerState {
			t.Fatalf("state %d has attempts %v, want %d", state, attempts[state], ngpPerState)
		}
	}

	stable := ngpArr(t, inv, "stableStates")
	if len(stable) != 2 {
		t.Fatalf("stableStates: %d", len(stable))
	}
	ngpWantStr(t, stable[0], "PASS", "verdict")
	ngpWantStr(t, stable[1], "FAIL", "verdict")
	transitions := ngpArr(t, inv, "transitions")
	if len(transitions) != 1 {
		t.Fatalf("transitions: %d", len(transitions))
	}
	ngpWantStr(t, transitions[0], "PASS_TO_FAIL", "kind")
	ngpWantStr(t, transitions[0], good, "before", "commit")
	ngpWantStr(t, transitions[0], bad, "after", "commit")

	// Exported bundle located under the managed root, root bound, hash chain consistent.
	ngpWantStr(t, cont, "MATCH", "proofBundle", "externalRootStatus")
	bundleDir = ngpStr(t, cont, "proofBundle", "directory")
	bundleRoot = ngpStr(t, cont, "proofBundle", "rootDigest")
	if !ngpSHA256.MatchString(bundleRoot) {
		t.Fatalf("root digest %q", bundleRoot)
	}
	managed := filepath.Join(repo, ".faultline", "git-proof-bundles")
	if rel, err := filepath.Rel(managed, bundleDir); err != nil || strings.HasPrefix(rel, "..") || strings.ContainsRune(rel, filepath.Separator) ||
		!strings.HasPrefix(rel, "incident-"+ngpIncident+"-"+bad[:12]+"-") {
		t.Fatalf("bundle %s is not a direct child of the managed root %s (rel %q, err %v)", bundleDir, managed, rel, err)
	}
	ev.put("ROOT.expected.txt", []byte(bundleRoot+"\n"))
	if got, err := os.ReadFile(filepath.Join(bundleDir, "ROOT.sha256")); err != nil || string(got) != bundleRoot+"\n" {
		t.Fatalf("ROOT.sha256 = %q (%v), want %q", got, err, bundleRoot)
	}
	hashes, err := os.ReadFile(filepath.Join(bundleDir, "hashes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(hashes)
	if "sha256:"+hex.EncodeToString(sum[:]) != bundleRoot {
		t.Fatalf("hashes.txt does not hash to the root %s", bundleRoot)
	}
	manifest := ngpReadJSON(t, filepath.Join(bundleDir, "manifest.json"))
	ngpWantStr(t, manifest, "faultline.git-proof-bundle.v1", "schemaVersion")
	ngpWantStr(t, manifest, "complete-declared-file-set", "integrityScope")
	ngpWantStr(t, manifest, retained, "frozenDigest")
	ngpWantStr(t, manifest, witnessDigest, "witnessDigest")
	invDigest := ngpDigest(t, inv)
	ngpWantStr(t, manifest, invDigest, "investigationDigest")
	if ngpDigest(t, ngpReadJSON(t, filepath.Join(bundleDir, "investigation.json"))) != invDigest {
		t.Fatal("bundle investigation.json differs from the reported investigation")
	}
	for _, run := range runs {
		path := filepath.Join(bundleDir, "runs", strings.TrimPrefix(ngpStr(t, run, "runId"), "sha256:")+".json")
		if ngpCanonical(t, ngpReadJSON(t, path)) != ngpCanonical(t, run) {
			t.Fatalf("bundle run artifact %s differs from the reported run", path)
		}
	}
	if entries, err := os.ReadDir(filepath.Join(bundleDir, "runs")); err != nil || len(entries) != ngpRuns {
		t.Fatalf("bundle runs directory: %d entries (%v), want %d", len(entries), err, ngpRuns)
	}
	if ngpCanonical(t, ngpReadJSON(t, filepath.Join(bundleDir, "transitions", "0000.json"))) != ngpCanonical(t, transitions[0]) {
		t.Fatal("bundle transition artifact differs from the reported transition")
	}
	wantNext := "fl serve --bundle " + bundleDir + " --expect-root " + bundleRoot
	if next := ngpArr(t, cont, "next"); len(next) != 2 || next[0].Str() != wantNext {
		t.Fatalf("next commands: %s", ngpCanonical(t, ngpNeed(t, cont, "next")))
	}

	// Real approval and frozen records link the fixture reviewer to the retained digest.
	approval, frozen := ngpReadJSON(t, approvalPath), ngpReadJSON(t, frozenPath)
	proposal := ngpReadJSON(t, proposalPath)
	ngpWantStr(t, approval, "HUMAN", "reviewerType")
	ngpWantStr(t, approval, ngpReviewer, "approvedBy")
	ngpWantStr(t, approval, ngpIncident, "proposalId")
	ngpWantStr(t, approval, ngpStr(t, proposal, "proposalDigest"), "proposalDigest")
	ngpWantStr(t, frozen, retained, "frozenDigest")
	ngpWantStr(t, frozen, witnessDigest, "witnessDigest")
	ngpWantStr(t, frozen, ngpStr(t, approval, "approvalDigest"), "approval", "approvalDigest")
	ngpWantStr(t, frozen, strings.TrimSpace(ngpPredicate), "proposal", "witness", "command")
	ngpWantStr(t, ngpReadJSON(t, filepath.Join(bundleDir, "witness", "frozen.json")), retained, "frozenDigest")
	if got, _ := os.ReadFile(draftPath); string(got) != string(draftBefore) {
		t.Fatal("the immutable draft changed during the flow")
	}
	if got, _ := os.ReadFile(proposalPath); string(got) != string(proposalBefore) {
		t.Fatal("the immutable proposal changed during the flow")
	}

	// The target repository is untouched.
	if head := git(badDate, "rev-parse", "HEAD"); head != bad {
		t.Fatalf("HEAD moved to %s", head)
	}
	if dirty := git(badDate, "status", "--porcelain=v1", "--untracked-files=all"); dirty != "" {
		t.Fatalf("target repository dirty after the flow: %q", dirty)
	}

	// Second ordinary verifier process, plus a wrong-root negative control.
	verifyCtx, verifyCancel := context.WithDeadline(ctx, started.Add(148*time.Second))
	defer verifyCancel()
	verify := ngpExec(verifyCtx, ev, "fl-verify", work, childEnv, binary, "verify", bundleDir, "--expect-root", bundleRoot)
	if verify.err != nil || verify.exit != 0 || verify.stderr != "" || !strings.HasPrefix(verify.stdout, "Integrity: VALID\n") ||
		!strings.Contains(verify.stdout, "\nExternal root: MATCH\n") || !strings.Contains(verify.stdout, "\nBundle root: "+bundleRoot+"\n") {
		t.Fatalf("second verifier: exit %d err %v\nstdout=%s\nstderr=%s", verify.exit, verify.err, verify.stdout, verify.stderr)
	}
	wrong := bundleRoot[:len(bundleRoot)-1] + map[bool]string{true: "1", false: "0"}[bundleRoot[len(bundleRoot)-1] == '0']
	negative := ngpExec(verifyCtx, ev, "fl-verify-wrong-root", work, childEnv, binary, "verify", bundleDir, "--expect-root", wrong)
	if negative.err != nil || negative.exit != 1 || negative.stderr != "" ||
		!strings.Contains(negative.stdout, "\nExternal root: MISMATCH\n") || !strings.Contains(negative.stdout, "\nBundle root: "+bundleRoot+"\n") ||
		strings.Contains(negative.stdout, "External root: MATCH") || strings.HasPrefix(negative.stdout, "Integrity: VALID") {
		t.Fatalf("wrong-root control not rejected as an external-root mismatch: exit %d err %v\nstdout=%s\nstderr=%s", negative.exit, negative.err, negative.stdout, negative.stderr)
	}
}
