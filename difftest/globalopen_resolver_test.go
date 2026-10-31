//go:build faultline_test

package difftest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Test-only resolution of the globalopen helper and its adversarial fixture
// (difftest/testdata/globalopen, difftest/testdata/globalopen-fixture): ptrace
// tools used by the ordinary first-open fault rows. Neither is a production
// command and neither ships in a release.
//
// Interface (the same for both tools):
//
//	FAULTLINE_GLOBALOPEN_BINARY          absolute path of a prebuilt helper
//	FAULTLINE_GLOBALOPEN_FIXTURE_BINARY  absolute path of a prebuilt fixture
//
// When set, the value must be an absolute path of an existing, executable,
// regular file; anything else fails the test loudly (it is never replaced by a
// build, and a prebuilt binary is never deleted). This is how a Go-less Linux
// container runs cross-built DEFAULT tools. When unset, the tool is built ONCE
// per test process with the DEFAULT tag set (no faultline_test, no
// globalopen_hooks) into a process-lifetime temp root that TestMain removes
// (globalOpenCleanup). A missing Go toolchain, a failed build or a non-Linux
// platform is a loud failure. There is never a fallback to stock strace.

var globalOpenTools = struct {
	mu    sync.Mutex
	root  string
	paths map[string]string // tool name -> built path
	errs  map[string]string // tool name -> cached build failure
}{paths: map[string]string{}, errs: map[string]string{}}

// globalOpenCleanup removes the resolver-owned build root. Called by TestMain
// after the tests; explicit override binaries are never touched.
func globalOpenCleanup() {
	globalOpenTools.mu.Lock()
	defer globalOpenTools.mu.Unlock()
	if globalOpenTools.root != "" {
		os.RemoveAll(globalOpenTools.root)
		globalOpenTools.root = ""
		globalOpenTools.paths = map[string]string{}
		globalOpenTools.errs = map[string]string{}
	}
}

// checkGlobalOpenOverride validates an explicit prebuilt-binary override.
func checkGlobalOpenOverride(envName, path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s must be an absolute path (got %q)", envName, path)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s: %v", envName, err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s=%s is not a regular file", envName, path)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s=%s is not executable", envName, path)
	}
	return nil
}

// globalOpenTool returns the path of one tool: the validated override or a
// once-per-process default build.
func globalOpenTool(t *testing.T, repo, envName, name, pkg string) string {
	t.Helper()
	if v := os.Getenv(envName); v != "" {
		if err := checkGlobalOpenOverride(envName, v); err != nil {
			t.Fatalf("%v", err)
		}
		return v
	}
	if runtime.GOOS != "linux" {
		t.Fatalf("the globalopen helper is a Linux ptrace tool; %s is unsupported and there is no fallback", runtime.GOOS)
	}
	globalOpenTools.mu.Lock()
	defer globalOpenTools.mu.Unlock()
	if msg, bad := globalOpenTools.errs[name]; bad {
		t.Fatalf("%s", msg)
	}
	if p, ok := globalOpenTools.paths[name]; ok {
		return p
	}
	if _, err := exec.LookPath("go"); err != nil {
		msg := fmt.Sprintf("no Go toolchain to build %s and %s is unset: set %s to an absolute prebuilt default binary (%v)", pkg, envName, envName, err)
		globalOpenTools.errs[name] = msg
		t.Fatalf("%s", msg)
	}
	if globalOpenTools.root == "" {
		root, err := os.MkdirTemp("", "faultline-globalopen-")
		if err != nil {
			t.Fatalf("creating the globalopen build root: %v", err)
		}
		globalOpenTools.root = root
	}
	out := filepath.Join(globalOpenTools.root, name)
	cmd := exec.Command("go", "build", "-o", out, pkg)
	cmd.Dir = repo
	b, err := cmd.CombinedOutput()
	if err != nil {
		msg := fmt.Sprintf("go build %s failed: %v\n%s", pkg, err, b)
		globalOpenTools.errs[name] = msg
		t.Fatalf("%s", msg)
	}
	globalOpenTools.paths[name] = out
	return out
}

// globalOpenBinary is the helper (default build, no regression hooks).
func globalOpenBinary(t *testing.T, repo string) string {
	t.Helper()
	return globalOpenTool(t, repo, "FAULTLINE_GLOBALOPEN_BINARY", "globalopen", "./difftest/testdata/globalopen")
}

// globalOpenFixtureBinary is the adversarial fixture and real verifier.
func globalOpenFixtureBinary(t *testing.T, repo string) string {
	t.Helper()
	return globalOpenTool(t, repo, "FAULTLINE_GLOBALOPEN_FIXTURE_BINARY", "globalopen-fixture", "./difftest/testdata/globalopen-fixture")
}

func TestGlobalOpenOverrideValidation(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "tool")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" { // a Windows file mode has no execute bit to check
		if err := checkGlobalOpenOverride("X", exe); err != nil {
			t.Fatalf("an absolute executable regular file was rejected: %v", err)
		}
		if err := checkGlobalOpenOverride("X", plain); err == nil || !strings.Contains(err.Error(), "not executable") {
			t.Errorf("a non-executable file: %v", err)
		}
	}
	for name, path := range map[string]string{
		"relative":  "tool",
		"missing":   filepath.Join(dir, "absent"),
		"directory": dir,
	} {
		if err := checkGlobalOpenOverride("X", path); err == nil {
			t.Errorf("%s override %q was accepted", name, path)
		}
	}
}
