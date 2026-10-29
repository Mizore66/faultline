//go:build !windows

package codexloop

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var fakeScripts = map[string]string{
	modeNope: "echo nope >&2; exit 2\n",
	modeCRLF: "printf '  codex-cli 1.2.3\\r\\nbuild\\r\\n'\n",
	modeV1:   "echo v1\n",
	modeV2:   "echo codex-cli 2\n",
	modeExec: `if [ "$1" = --version ]; then echo codex-cli 9; exit 0; fi
printf '%s|' "$@"; pwd -P; cat; printf 'err\377' >&2; exit 7
`,
}

// writeCodex writes the fake codex for mode as dir/codex and returns its path.
func writeCodex(t *testing.T, dir, mode string) string {
	t.Helper()
	body, ok := fakeScripts[mode]
	if !ok {
		t.Fatalf("no script for mode %q", mode)
	}
	p := filepath.Join(dir, "codex")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

type fakeRun struct {
	out, err string
	status   int
}

func runScript(t *testing.T, mode string, args []string, dir, stdin string) fakeRun {
	t.Helper()
	cmd := exec.Command(writeCodex(t, t.TempDir(), mode), args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	status := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		status = ee.ExitCode()
	}
	return fakeRun{out.String(), errb.String(), status}
}

func runChild(mode string, args []string, cwd, stdin string) fakeRun {
	var out, errb bytes.Buffer
	status := runFakeCodex(mode, args, cwd, strings.NewReader(stdin), &out, &errb)
	return fakeRun{out.String(), errb.String(), status}
}

// The Windows child must behave exactly like the sh scripts the POSIX runs
// execute: same stdout and stderr bytes (CRLF, 0xFF) and status.
func TestFakeCodexChildMatchesScripts(t *testing.T) {
	cwd, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		mode  string
		args  []string
		stdin string
	}{
		{modeNope, []string{"--version"}, ""},
		{modeCRLF, []string{"--version"}, ""},
		{modeV1, []string{"--version"}, ""},
		{modeV2, []string{"--version"}, ""},
		{modeExec, []string{"--version"}, ""},
		{modeExec, []string{"exec", "--full-auto", "p é"}, "p é\n"},
		{modeExec, nil, ""},
	}
	for _, c := range cases {
		got, want := runChild(c.mode, c.args, cwd, c.stdin), runScript(t, c.mode, c.args, cwd, c.stdin)
		if got != want {
			t.Errorf("%s %q: child %+v, script %+v", c.mode, c.args, got, want)
		}
	}
	// Negative controls: the comparison tells different behaviours apart.
	a := runChild(modeExec, []string{"exec", "x"}, cwd, "in")
	for name, other := range map[string]fakeRun{
		"stdin": runScript(t, modeExec, []string{"exec", "x"}, cwd, "other"),
		"args":  runScript(t, modeExec, []string{"exec", "y"}, cwd, "in"),
		"cwd":   runScript(t, modeExec, []string{"exec", "x"}, t.TempDir(), "in"),
		"mode":  runScript(t, modeV1, []string{"exec", "x"}, cwd, "in"),
		"crlf":  runScript(t, modeCRLF, []string{"--version"}, cwd, ""),
	} {
		if a == other {
			t.Errorf("comparison cannot tell %s apart", name)
		}
	}
	if runChild(modeCRLF, nil, cwd, "") == runChild(modeV1, nil, cwd, "") {
		t.Error("crlf and v1 children are indistinguishable")
	}
}
