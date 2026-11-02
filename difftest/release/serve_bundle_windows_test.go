//go:build windows

package release

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// standaloneConsoleHelper builds the test-only console emitter natively into a
// test-owned path, from the explicit repository root (the target's cwd is
// elsewhere). The Started protocol launches the target in its own new console.
func standaloneConsoleHelper(t *testing.T, repo string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "console-helper.exe")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", out, "./difftest/servefixture/console-helper")
	build.Dir = repo
	build.WaitDelay = time.Second
	if combined, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building console helper: %v\n%s", err, combined)
	}
	return out
}
