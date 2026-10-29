package codexloop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain lets the test binary act as the fake codex: ResolveRunner and
// SpawnCodex give the child only PATH, ComSpec and SystemRoot (codexEnv), so
// the mode cannot travel in the environment. writeCodex copies the test
// binary to dir\codex.exe and the mode to dir\codex.mode; started under the
// name codex.exe, the binary reads the mode next to itself.
func TestMain(m *testing.M) {
	exe, err := os.Executable()
	if err == nil && strings.EqualFold(filepath.Base(exe), "codex.exe") {
		mode, err := os.ReadFile(filepath.Join(filepath.Dir(exe), "codex.mode"))
		if err != nil {
			os.Stderr.WriteString("fake codex: " + err.Error() + "\n")
			os.Exit(98)
		}
		cwd, _ := os.Getwd()
		if real, err := filepath.EvalSymlinks(cwd); err == nil {
			cwd = real
		}
		os.Exit(runFakeCodex(string(mode), os.Args[1:], cwd, os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

// writeCodex installs the fake codex for mode as dir\codex.exe and returns
// its path. Called again for the same dir, it changes the mode.
func writeCodex(t *testing.T, dir, mode string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "codex.exe")
	if err := os.WriteFile(p, body, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "codex.mode"), []byte(mode), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
