//go:build !windows

package runtimeimage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// NodeRunner spawns `docker` from PATH with the given argv and reports its
// output and exit code; a missing docker is an 'error' event (nil exit).
func TestNodeRunnerSpawnsDocker(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s|' \"$@\"\nprintf 'warn' >&2\nexit 3\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	r := NodeRunner()([]string{"image", "inspect", "--format", DockerRepoDigestsFormat, "node:22-alpine"}, 5*time.Second, 64)
	if r.Stdout != "image|inspect|--format|{{json .RepoDigests}}|node:22-alpine|" || r.Stderr != "warn" || r.ExitCode == nil || *r.ExitCode != 3 {
		t.Fatalf("%+v", r)
	}
	r = NodeRunner()([]string{"pull", "x"}, 5*time.Second, 4)
	if r.Stdout != "pull" || !r.OutputLimitExceeded {
		t.Fatalf("%+v", r)
	}
	t.Setenv("PATH", t.TempDir())
	r = NodeRunner()([]string{"pull", "x"}, time.Second, 64)
	if r.ExitCode != nil || !strings.Contains(r.Stderr, "spawn docker ENOENT") {
		t.Fatalf("%+v", r)
	}
}
