package codexloop

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestResolveRunnerProbeTexts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAULTLINE_CODEX_BIN", "  "+filepath.Join(dir, "missing")+"  ")
	if _, err := ResolveRunner(); err == nil || err.Error() != "FAULTLINE_CODEX_BIN points to a missing binary: "+filepath.Join(dir, "missing") {
		t.Fatalf("%v", err)
	}
	bin := writeCodex(t, dir, modeNope)
	t.Setenv("FAULTLINE_CODEX_BIN", bin)
	if _, err := ResolveRunner(); err == nil || err.Error() != "Codex CLI --version failed for "+bin+": nope. Set FAULTLINE_CODEX_BIN or install Codex on PATH." {
		t.Fatalf("%v", err)
	}
	writeCodex(t, dir, modeCRLF)
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
	dir := t.TempDir()
	bin := writeCodex(t, dir, modeExec)
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
		t.Fatalf("%+v", r) // ExitCode prints as a pointer; the other fields say enough
	}
}

func TestCLIRunnerReturnsResolutionAndArgumentErrors(t *testing.T) {
	t.Setenv("FAULTLINE_CODEX_BIN", "/nonexistent/codex")
	run := CLIRunner(true, "refused", time.Second)
	if _, err := run([]string{"exec"}, t.TempDir(), "p"); err == nil || err.Error() != "FAULTLINE_CODEX_BIN points to a missing binary: /nonexistent/codex" {
		t.Fatalf("%v", err)
	}
	t.Setenv("FAULTLINE_CODEX_BIN", writeCodex(t, t.TempDir(), modeV1))
	if _, err := run([]string{"exec", "a\x00b"}, t.TempDir(), "p"); err == nil || err.Error() != `The argument 'args[1]' must be a string without null bytes. Received 'a\x00b'` {
		t.Fatalf("%v", err)
	}
}

func TestResolveRunnerOnPath(t *testing.T) {
	t.Setenv("FAULTLINE_CODEX_BIN", " \t")
	empty, dir := t.TempDir(), t.TempDir()
	writeCodex(t, dir, modeV2)
	// POSIX: empty entries, a trailing run of slashes, ":" separators and
	// the bare name codex. Windows (codex-runner.ts pathEntries/candidateNames,
	// delimiter ";"): empty entries, a trailing run of "\" and "/", and
	// codex.exe after codex.cmd is not found.
	path, want, bare, notFound := "::"+empty+":"+dir+"///:/bin", dir+"/codex", "codex", "ENOENT"
	if runtime.GOOS == "windows" {
		path, want, bare, notFound = ";;"+empty+";"+dir+`\/\`+";"+`C:\nowhere`, dir+`\codex.exe`, "codex.cmd", "EINVAL"
	}
	t.Setenv("PATH", path)
	r, err := ResolveRunner()
	if err != nil || r != (Resolution{Bin: want, Version: "codex-cli 2", Source: "PATH"}) {
		t.Fatalf("%+v %v", r, err)
	}
	t.Setenv("PATH", empty)
	if _, err := ResolveRunner(); err == nil || err.Error() != "Codex CLI probe failed for "+bare+": spawnSync "+bare+" "+notFound+". Set FAULTLINE_CODEX_BIN or install Codex on PATH." {
		t.Fatalf("%v", err)
	}
}
