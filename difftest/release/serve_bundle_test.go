package release

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/servefixture"
)

// TestServeBundleStandalone runs the actual UNTAGGED release binary, built once
// and executed outside the repository with a controlled environment that has
// real trusted Git but no Node, npm, pnpm or tsx launcher on any PATH entry. The
// retained fixture bytes are copied as explicit inputs; the working directory
// has no src, dist, testdata or package manifest. Expected bytes are the
// frozen-TS captures of the Task 1 corpus, never what Go prints.
//
// It catches a release binary that needs Node or the source tree at runtime,
// serves different pages for any of the eight attachment combinations, ignores
// real Git, accepts an invalid attachment (or lets an occupied port outrank
// it), acts on --serve-ms/--browser/--output, or does not finish cleanly after
// one real interrupt.
func TestServeBundleStandalone(t *testing.T) {
	repo := repoRoot(t)
	corpus, err := servefixture.Load(filepath.Join(repo, "difftest", "testdata", "serve-bundle"))
	if err != nil {
		t.Fatal(err)
	}
	bin := buildCLIHelpRelease(t, "")
	tools := standaloneToolDirectory(t)
	helper := standaloneConsoleHelper(t, repo)

	t.Run("combinations", func(t *testing.T) {
		count := 0
		for _, bits := range []string{"000", "001", "010", "011", "100", "101", "110", "111"} {
			count++
			t.Run(bits, func(t *testing.T) {
				standaloneServe(t, corpus, bin, tools, helper, "cli-serve-"+bits, bits == "111")
			})
		}
		if count != 8 {
			t.Fatalf("ran %d combinations, want 8", count)
		}
	})
	t.Run("ignored-options", func(t *testing.T) {
		standaloneServe(t, corpus, bin, tools, helper, "cli-serve-ignored-flags", false)
	})
	t.Run("refusals-before-listen", func(t *testing.T) {
		for _, id := range []string{
			"cli-order-attachment-before-port", "cli-reject-view-min-expected-wrong",
			"cli-reject-view-repair-expected-wrong", "cli-reject-view-prevention-expected-wrong",
			"cli-options-min-path-only", "cli-order-pair-before-io",
		} {
			t.Run(id, func(t *testing.T) { standaloneRefusal(t, corpus, bin, tools, id) })
		}
	})
}

// ---- controlled environment ----

// standaloneLaunchers are the interpreter and package-manager launchers no PATH
// directory may contain.
var standaloneLaunchers = []string{"node", "npm", "npx", "pnpm", "tsx", "corepack", "yarn"}

// dirHasLauncher reports whether dir holds a launcher; on Windows the match is
// case-insensitive and covers the executable extensions.
func dirHasLauncher(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading PATH directory %s: %v", dir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if runtime.GOOS == "windows" {
			name = strings.ToLower(name)
			ext := filepath.Ext(name)
			switch ext {
			case ".exe", ".cmd", ".bat", ".com", ".ps1", ".vbs", ".js":
				name = strings.TrimSuffix(name, ext)
			}
		}
		if slices.Contains(standaloneLaunchers, name) {
			return e.Name()
		}
	}
	return ""
}

// standaloneToolDirectory resolves the REAL trusted Git first, then returns the
// single directory used as PATH. If Git's own directory contains no launcher it
// is used as is; otherwise (typical for /usr/bin) a test-owned directory holds
// symlinks to the real Git (and sh, which Git may spawn). Interpreter launchers
// are never copied or linked. Every resulting PATH entry is verified.
func standaloneToolDirectory(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("trusted Git is required: %v", err)
	}
	git, err = filepath.Abs(git)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(git)
	if dirHasLauncher(t, dir) == "" {
		return dir
	}
	if runtime.GOOS == "windows" {
		t.Fatalf("trusted Git directory %s contains a Node launcher (%s); Windows needs a Git-only directory", dir, dirHasLauncher(t, dir))
	}
	owned := t.TempDir()
	links := map[string]string{"git": git}
	if sh, err := exec.LookPath("sh"); err == nil {
		links["sh"] = sh
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(owned, name)); err != nil {
			t.Fatal(err)
		}
	}
	if found := dirHasLauncher(t, owned); found != "" {
		t.Fatalf("owned tool directory unexpectedly holds launcher %s", found)
	}
	return owned
}

// standaloneEnv is the complete child environment: only the tool directory on
// PATH, Git's real exec path and a hermetic Git configuration. It has no
// inherited FAULTLINE_* or package-manager variable.
func standaloneEnv(t *testing.T, tools string) []string {
	t.Helper()
	execPath, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Fatalf("git --exec-path: %v", err)
	}
	home, tmp := t.TempDir(), t.TempDir()
	emptyConfig := filepath.Join(home, "empty.gitconfig")
	if err := os.WriteFile(emptyConfig, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PATH=" + tools, "HOME=" + home, "USERPROFILE=" + home,
		"TMPDIR=" + tmp, "TEMP=" + tmp, "TMP=" + tmp,
		"GIT_EXEC_PATH=" + strings.TrimSpace(string(execPath)),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + emptyConfig, "GIT_DEFAULT_HASH=sha1",
		"LC_ALL=C.UTF-8",
	}
	for _, key := range []string{"SystemRoot", "WINDIR"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "FAULTLINE_") || strings.HasPrefix(kv, "npm_") {
			t.Fatalf("controlled environment leaks %s", kv)
		}
	}
	return env
}

// standaloneCopy copies the case's fixture bytes as explicit inputs into a fresh
// directory outside the repository and checks the runtime directory is clean.
func standaloneCopy(t *testing.T, corpus *servefixture.Corpus, c servefixture.Case) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "fixture")
	if err := corpus.CopyFixture(c.Fixture, root); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"src", "dist", "testdata", "package.json", "node_modules"} {
		if _, err := os.Lstat(filepath.Join(root, forbidden)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("runtime directory unexpectedly contains %s (%v)", forbidden, err)
		}
	}
	return root
}

func standaloneCase(t *testing.T, corpus *servefixture.Corpus, id string) servefixture.Case {
	t.Helper()
	for _, c := range corpus.Cases {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no retained case %s", id)
	return servefixture.Case{}
}

func standaloneContext(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func standaloneNormalize(text, root string) string { return strings.ReplaceAll(text, root, "{ROOT}") }

// standaloneFetch is one GET / with Connection: close over an owned client.
func standaloneFetch(t *testing.T, p *servefixture.Started) (int, []byte) {
	t.Helper()
	ctx := standaloneContext(t, 30*time.Second)
	conn, err := p.Dial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	res, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, body
}

func standaloneListing(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// standaloneServe launches the release binary on one retained serving row and
// checks the complete lifecycle against the retained frozen-TS expectations.
func standaloneServe(t *testing.T, corpus *servefixture.Corpus, bin, tools, helper, id string, fullHTTP bool) {
	c := standaloneCase(t, corpus, id)
	wantPage, err := corpus.Page(id)
	if err != nil {
		t.Fatal(err)
	}
	want := corpus.Goldens[id].Process
	root := standaloneCopy(t, corpus, c)
	argv, err := corpus.ResolveArgv(id, root, 0, c.Argv)
	if err != nil {
		t.Fatal(err)
	}
	before := standaloneListing(t, root)
	proc, err := servefixture.Start(standaloneContext(t, 30*time.Second), bin, nil, argv, standaloneEnv(t, tools), root)
	if err != nil {
		t.Fatalf("starting release binary: %v", err)
	}
	t.Cleanup(func() {
		if err := proc.Dispose(); err != nil {
			t.Errorf("teardown: %v", err)
		}
	})
	status, body := standaloneFetch(t, proc)
	if status != 200 || !bytes.Equal(body, wantPage) {
		t.Errorf("GET / = %d, page equal=%v (%d bytes, want %d)", status, bytes.Equal(body, wantPage), len(body), len(wantPage))
	}
	if fullHTTP {
		if err := servefixture.CheckHTTP(standaloneContext(t, 120*time.Second), proc.URL, wantPage); err != nil {
			t.Error(err)
		}
	}
	if proc.Exited() {
		t.Fatal("the release binary exited before it was interrupted (a timer or other stop acted)")
	}
	if err := proc.Interrupt(standaloneContext(t, 30*time.Second), helper); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	result, err := proc.Wait(standaloneContext(t, 30*time.Second))
	if err != nil {
		t.Fatalf("no exit after the interrupt: %v", err)
	}
	if result.Signal != nil || result.ExitCode == nil || *result.ExitCode != 0 {
		t.Errorf("exit code=%v signal=%v, want completed exit 0", result.ExitCode, result.Signal)
	}
	stdout := standaloneNormalize(strings.Replace(result.Stdout, proc.URL, "http://127.0.0.1:{PORT}", 1), root)
	if stdout != want.Stdout || standaloneNormalize(result.Stderr, root) != want.Stderr {
		t.Errorf("streams\nstdout %q want %q\nstderr %q want %q", stdout, want.Stdout, result.Stderr, want.Stderr)
	}
	if after := standaloneListing(t, root); !slices.Equal(after, before) {
		t.Errorf("serving changed the directory: %v -> %v (an output or side effect was produced)", before, after)
	}
}

// standaloneRefusal runs a retained refusal to completion, with a genuinely
// occupied port where the row asks for one or, for the other attachment
// rejections, appended: the retained refusal must win over the busy port.
func standaloneRefusal(t *testing.T, corpus *servefixture.Corpus, bin, tools, id string) {
	c := standaloneCase(t, corpus, id)
	want := corpus.Goldens[id].Process
	root := standaloneCopy(t, corpus, c)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	argv, err := corpus.ResolveArgv(id, root, port, c.Argv)
	if err != nil {
		t.Fatal(err)
	}
	// Rows whose retained argv carries --port abc or no port get the busy port.
	replaced := false
	for i, arg := range argv {
		if arg == "abc" {
			argv[i], replaced = strconv.Itoa(port), true
		}
	}
	occupied := replaced || slices.Contains(c.Argv, "{PORT}")
	if !occupied && strings.HasPrefix(id, "cli-reject-") {
		argv, occupied = append(argv, "--port", strconv.Itoa(port)), true
	}
	ctx := standaloneContext(t, 30*time.Second)
	cmd := exec.CommandContext(ctx, bin, argv...)
	cmd.Dir, cmd.Env = root, standaloneEnv(t, tools)
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatal(err)
	}
	if ctx.Err() != nil {
		t.Fatalf("release binary did not exit (it may have started listening): stdout %q stderr %q", stdout.String(), stderr.String())
	}
	normalize := func(text string) string {
		text = standaloneNormalize(text, root)
		if occupied {
			text = strings.ReplaceAll(text, "127.0.0.1:"+strconv.Itoa(port), "127.0.0.1:{PORT}")
		}
		return text
	}
	if got := normalize(stdout.String()); got != want.Stdout {
		t.Errorf("stdout %q, want %q", got, want.Stdout)
	}
	if got := normalize(stderr.String()); got != want.Stderr {
		t.Errorf("stderr\n got %q\nwant %q", got, want.Stderr)
	}
	if want.ExitCode == nil || cmd.ProcessState.ExitCode() != *want.ExitCode {
		t.Errorf("exit = %d, want %v", cmd.ProcessState.ExitCode(), want.ExitCode)
	}
}
