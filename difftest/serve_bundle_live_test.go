//go:build faultline_test

package difftest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/difftest/servefixture"
)

// TestLiveServeBundle runs the frozen TypeScript `node dist/cli.js serve
// --bundle ...` and the Go fl on separate copies of the identical retained
// fixture and compares complete streams, exit status, served pages and the
// shared HTTP matrix. It needs explicit oracle enablement (oracle.Skip, first);
// once enabled there is no further availability skip: Node 22.22.2, manifest
// 0.1.4 and the built dist/cli.js 0.1.4 are pinned independently and any drift
// fails.
//
// Only the declared normalizations apply: each side's own copied root becomes
// {ROOT}, the selected occupied port and each side's own startup port become
// {PORT}. Platform diagnostics are compared native Node against native Go on
// the running platform; the retained Linux-captured goldens are compared as an
// oracle-drift check everywhere except the stream text of refusals on Windows,
// whose filesystem prose is not Linux's (exit codes are still compared there).
func TestLiveServeBundle(t *testing.T) {
	oracle.Skip(t)
	node, cli := pinServeBundleOracle(t)
	corpus := loadServeBundleCorpus(t)
	helper := serveBundleConsoleHelper(t)
	errorRows, serverRows, executed := 0, 0, 0
	for _, c := range corpus.Cases {
		switch c.Kind {
		case "cli-error":
			errorRows++
			t.Run(c.ID, func(t *testing.T) {
				executed++
				liveServeBundleError(t, corpus, c, node, cli)
			})
		case "cli-serve":
			serverRows++
			t.Run(c.ID, func(t *testing.T) {
				executed++
				liveServeBundleServer(t, corpus, c, node, cli, helper)
			})
		}
	}
	if errorRows != serveBundleCLIErrorRows || serverRows != serveBundleCLIServerRows {
		t.Fatalf("enumerated %d cli-error and %d cli-serve rows, want %d and %d", errorRows, serverRows, serveBundleCLIErrorRows, serveBundleCLIServerRows)
	}
	if executed != errorRows+serverRows && !subtestFilter() {
		t.Fatalf("executed %d of %d rows", executed, errorRows+serverRows)
	}
}

// pinServeBundleOracle pins the actual executables the comparison runs: Node
// by actual --version, the manifest version, and the built TS CLI's own version.
// Catches running the oracle against the wrong Node or a stale dist build.
func pinServeBundleOracle(t *testing.T) (node, cli string) {
	t.Helper()
	repo := oracle.RepoRoot(t)
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	outside, env := t.TempDir(), os.Environ()
	stdout, stderr, code := runCLIHelpProcess(t, node, outside, env, "--version")
	if stdout != "v22.22.2\n" || stderr != "" || code != 0 {
		t.Fatalf("oracle Node drift: stdout=%q stderr=%q exit=%d", stdout, stderr, code)
	}
	raw, err := os.ReadFile(filepath.Join(repo, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version any `json:"version"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "0.1.4" {
		t.Fatalf("oracle manifest drift: %#v", manifest.Version)
	}
	cli = filepath.Join(repo, "dist", "cli.js")
	stdout, stderr, code = runCLIHelpProcess(t, node, outside, env, cli, "--version")
	if stdout != "FaultLine 0.1.4\n" || stderr != "" || code != 0 {
		t.Fatalf("oracle CLI-version drift: stdout=%q stderr=%q exit=%d", stdout, stderr, code)
	}
	return node, cli
}

// liveSide is one implementation's launch recipe.
type liveSide struct {
	name   string
	exe    string
	prefix []string
}

func liveCopy(t *testing.T, corpus *servefixture.Corpus, c servefixture.Case) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "fixture")
	if err := corpus.CopyFixture(c.Fixture, root); err != nil {
		t.Fatal(err)
	}
	return root
}

// runLiveToExit runs one side to completion.
func runLiveToExit(t *testing.T, side liveSide, root string, argv []string) (stdout, stderr string, exit int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), serveBundleStartupTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, side.exe, append(slices.Clone(side.prefix), argv...)...)
	cmd.Dir = root
	cmd.Env = serveBundleChildEnv(root, "FAULTLINE_NO_BROWSER=1")
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("%s: %v", side.name, err)
	}
	if ctx.Err() != nil {
		t.Fatalf("%s did not exit before the test deadline\nstdout: %q\nstderr: %q", side.name, out.String(), errOut.String())
	}
	return out.String(), errOut.String(), cmd.ProcessState.ExitCode()
}

// Catches any difference between the frozen TS refusal and Go's: message,
// stream, exit status or validation order, including with an occupied port.
func liveServeBundleError(t *testing.T, corpus *servefixture.Corpus, c servefixture.Case, node, cli string) {
	sides := []liveSide{{"TS", node, []string{cli}}, {"Go", flBinary, nil}}
	port := 0
	if slices.Contains(c.Argv, "{PORT}") {
		port = occupyServeBundlePort(t)
	}
	type outcome struct {
		stdout, stderr string
		exit           int
	}
	results := make([]outcome, len(sides))
	for i, side := range sides {
		root := liveCopy(t, corpus, c)
		argv, err := corpus.ResolveArgv(c.ID, root, port, c.Argv)
		if err != nil {
			t.Fatal(err)
		}
		stdout, stderr, exit := runLiveToExit(t, side, root, argv)
		normalize := func(text string) string {
			text = serveBundleNormalize(text, root)
			if port != 0 {
				text = strings.ReplaceAll(text, "127.0.0.1:"+strconv.Itoa(port), "127.0.0.1:{PORT}")
			}
			return text
		}
		results[i] = outcome{normalize(stdout), normalize(stderr), exit}
		// Durable native evidence (a no-op without the evidence directory); it
		// records the complete raw and normalized outcome and compares nothing.
		capture := serveBundleCaptureOf(c, side.name)
		capture.Stdout, capture.Stderr, capture.Exit = stdout, stderr, &exit
		capture.NormStdout, capture.NormStderr = results[i].stdout, results[i].stderr
		capture.Root, capture.Port = root, port // the declared substitutions' context
		recordServeBundleCapture(t, capture)
	}
	ts, goSide := results[0], results[1]
	if goSide != ts {
		t.Errorf("Go differs from TS\n--- TS exit=%d\nstdout %q\nstderr %q\n--- Go exit=%d\nstdout %q\nstderr %q", ts.exit, ts.stdout, ts.stderr, goSide.exit, goSide.stdout, goSide.stderr)
	}
	want := corpus.Goldens[c.ID].Process
	if want.ExitCode == nil || ts.exit != *want.ExitCode {
		t.Errorf("TS oracle drift: exit=%d, retained %v", ts.exit, want.ExitCode)
	}
	if runtime.GOOS != "windows" && (ts.stdout != want.Stdout || ts.stderr != want.Stderr) {
		t.Errorf("TS oracle drift from the retained capture\nstdout %q want %q\nstderr %q want %q", ts.stdout, want.Stdout, ts.stderr, want.Stderr)
	}
}

// Catches a Go server that differs from the live TS server in startup output,
// page bytes, any HTTP response over the shared matrix, or the shutdown
// result of one real SIGINT.
func liveServeBundleServer(t *testing.T, corpus *servefixture.Corpus, c servefixture.Case, node, cli, helper string) {
	sides := []liveSide{{"TS", node, []string{cli}}, {"Go", flBinary, nil}}
	want := corpus.Goldens[c.ID].Process
	wantPage, err := corpus.Page(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		stdout, stderr string
		code           *int
		signal         *string
	}
	results := make([]outcome, len(sides))
	// Each side runs its complete lifecycle (start, full page, HTTP matrix, real
	// interrupt, wait, record) before the next side starts: rows that use the
	// default port (4173) cannot have two servers alive at once.
	for i, side := range sides {
		root := liveCopy(t, corpus, c)
		argv, err := corpus.ResolveArgv(c.ID, root, 0, c.Argv)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), serveBundleStartupTimeout)
		proc, err := servefixture.Start(ctx, side.exe, side.prefix, argv, serveBundleChildEnv(root, "FAULTLINE_NO_BROWSER=1"), root)
		cancel()
		if err != nil {
			t.Fatalf("starting %s: %v", side.name, err)
		}
		t.Cleanup(func() {
			if err := proc.Dispose(); err != nil {
				t.Errorf("teardown of %s: %v", side.name, err)
			}
		})
		_, body, err := fetchServeBundlePage(stepContext(t), proc)
		if err != nil {
			t.Fatalf("%s GET /: %v", side.name, err)
		}
		page := body // this side's fetched page, for the evidence record
		if !bytes.Equal(body, wantPage) {
			t.Errorf("%s page differs from the retained frozen page: %s", side.name, serveBundleDiff(body, wantPage))
		}
		ctx, cancel = context.WithTimeout(context.Background(), serveBundleHTTPTimeout)
		if side.name == "TS" {
			err = servefixture.CheckNodeHTTP(ctx, proc.URL, wantPage)
		} else {
			err = servefixture.CheckHTTP(ctx, proc.URL, wantPage)
		}
		cancel()
		if err != nil {
			t.Errorf("%s HTTP: %v", side.name, err)
		}
		if err := proc.Interrupt(stepContext(t), helper); err != nil {
			t.Fatalf("%s interrupt: %v", side.name, err)
		}
		res, err := proc.Wait(stepContext(t))
		if err != nil {
			t.Fatalf("%s did not return after the interrupt: %v", side.name, err)
		}
		stdout := serveBundleNormalize(strings.Replace(res.Stdout, proc.URL, "http://127.0.0.1:{PORT}", 1), root)
		results[i] = outcome{stdout, serveBundleNormalize(res.Stderr, root), res.ExitCode, res.Signal}
		// Durable native evidence (a no-op without the evidence directory): the
		// full raw streams, status and the page this side served. Recorded
		// before the comparisons below, which are unchanged.
		capture := serveBundleCaptureOf(c, side.name)
		capture.Stdout, capture.Stderr, capture.Exit, capture.Signal = res.Stdout, res.Stderr, res.ExitCode, res.Signal
		capture.NormStdout, capture.NormStderr, capture.Page = results[i].stdout, results[i].stderr, page
		capture.Root, capture.URL = root, proc.URL // the declared substitutions' context
		recordServeBundleCapture(t, capture)
	}
	for i, side := range sides {
		r := results[i]
		if r.signal != nil || r.code == nil || *r.code != 0 {
			t.Errorf("%s exit code=%v signal=%v, want completed exit 0", side.name, r.code, r.signal)
		}
		if r.stdout != want.Stdout || r.stderr != want.Stderr {
			t.Errorf("%s streams\nstdout %q want %q\nstderr %q want %q", side.name, r.stdout, want.Stdout, r.stderr, want.Stderr)
		}
	}
	if results[0].stdout != results[1].stdout || results[0].stderr != results[1].stderr {
		t.Errorf("Go streams differ from TS\nTS %q %q\nGo %q %q", results[0].stdout, results[0].stderr, results[1].stdout, results[1].stderr)
	}
}
