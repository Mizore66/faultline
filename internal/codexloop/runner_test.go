package codexloop

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func writeScript(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "codex")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResolveRunnerProbeTexts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh")
	}
	dir := t.TempDir()
	t.Setenv("FAULTLINE_CODEX_BIN", "  "+filepath.Join(dir, "missing")+"  ")
	if _, err := ResolveRunner(); err == nil || err.Error() != "FAULTLINE_CODEX_BIN points to a missing binary: "+filepath.Join(dir, "missing") {
		t.Fatalf("%v", err)
	}
	bin := writeScript(t, dir, "echo nope >&2; exit 2\n")
	t.Setenv("FAULTLINE_CODEX_BIN", bin)
	if _, err := ResolveRunner(); err == nil || err.Error() != "Codex CLI --version failed for "+bin+": nope. Set FAULTLINE_CODEX_BIN or install Codex on PATH." {
		t.Fatalf("%v", err)
	}
	writeScript(t, dir, "printf '  codex-cli 1.2.3\\r\\nbuild\\r\\n'\n")
	r, err := ResolveRunner()
	if err != nil || r.Version != "codex-cli 1.2.3" || r.Source != "ENV" {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestCLIRunnerChecksAllowFlagFirst(t *testing.T) {
	t.Setenv("FAULTLINE_CODEX_BIN", "/nonexistent/codex")
	run := CLIRunner(false, "Codex repair drafting requires explicit --allow-codex-full-auto in addition to --with-codex.", 180*time.Second)
	if _, err := run([]string{"exec"}, t.TempDir(), "p"); err == nil || err.Error() != "Codex repair drafting requires explicit --allow-codex-full-auto in addition to --with-codex." {
		t.Fatalf("%v", err)
	}
}

func TestCLIRunnerExec(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh")
	}
	dir := t.TempDir()
	bin := writeScript(t, dir, `if [ "$1" = --version ]; then echo codex-cli 9; exit 0; fi
printf '%s|' "$@"; pwd -P; cat; printf 'err\377' >&2; exit 7
`)
	t.Setenv("FAULTLINE_CODEX_BIN", bin)
	cwd, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run := CLIRunner(true, "refused", 120*time.Second)
	r, err := run([]string{"exec", "--full-auto", "p é"}, cwd, "p é\n")
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode == nil || *r.ExitCode != 7 || r.Stdout != "exec|--full-auto|p é|"+cwd+"\np é\n" || r.Stderr != "err�" || r.ThreadID != nil {
		t.Fatalf("%+v %v", r, *r.ExitCode)
	}
}

func TestCLIRunnerReturnsResolutionAndArgumentErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh")
	}
	t.Setenv("FAULTLINE_CODEX_BIN", "/nonexistent/codex")
	run := CLIRunner(true, "refused", time.Second)
	if _, err := run([]string{"exec"}, t.TempDir(), "p"); err == nil || err.Error() != "FAULTLINE_CODEX_BIN points to a missing binary: /nonexistent/codex" {
		t.Fatalf("%v", err)
	}
	t.Setenv("FAULTLINE_CODEX_BIN", writeScript(t, t.TempDir(), "echo v1\n"))
	if _, err := run([]string{"exec", "a\x00b"}, t.TempDir(), "p"); err == nil || err.Error() != `The argument 'args[1]' must be a string without null bytes. Received 'a\x00b'` {
		t.Fatalf("%v", err)
	}
}

func TestResolveRunnerOnPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh")
	}
	t.Setenv("FAULTLINE_CODEX_BIN", " \t")
	empty, dir := t.TempDir(), t.TempDir()
	writeScript(t, dir, "echo codex-cli 2\n")
	t.Setenv("PATH", "::"+empty+":"+dir+"///:/bin")
	r, err := ResolveRunner()
	if err != nil || r != (Resolution{Bin: dir + "/codex", Version: "codex-cli 2", Source: "PATH"}) {
		t.Fatalf("%+v %v", r, err)
	}
	t.Setenv("PATH", empty)
	if _, err := ResolveRunner(); err == nil || err.Error() != "Codex CLI probe failed for codex: spawnSync codex ENOENT. Set FAULTLINE_CODEX_BIN or install Codex on PATH." {
		t.Fatalf("%v", err)
	}
}
