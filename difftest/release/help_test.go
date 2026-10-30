package release

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func buildCLIHelpRelease(t *testing.T, stamp string) string {
	t.Helper()
	name := "fl"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(t.TempDir(), name)
	args := []string{"build", "-o", bin}
	if stamp != "" {
		args = append(args, "-ldflags=-X=github.com/Mizore66/faultline/internal/buildinfo.Version="+stamp)
	}
	args = append(args, "./cmd/fl")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = repoRoot(t)
	cmd.WaitDelay = time.Second
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build CLI-help release: %v\n%s", err, out)
	}
	return bin
}

func runCLIHelpRelease(t *testing.T, bin, cwd string, env []string, args ...string) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir, cmd.Env = cwd, env
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("CLI-help release timeout: %q", args)
	}
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("CLI-help release: %v", err)
	}
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}

func cliHelpEmptyPathEnv(t *testing.T) []string {
	t.Helper()
	empty, home, tmp := t.TempDir(), t.TempDir(), t.TempDir()
	entries, err := os.ReadDir(empty)
	if err != nil || len(entries) != 0 {
		t.Fatalf("controlled PATH is not empty: %v", err)
	}
	env := []string{"PATH=" + empty, "HOME=" + home, "USERPROFILE=" + home,
		"TMPDIR=" + tmp, "TEMP=" + tmp, "TMP=" + tmp,
		"FAULTLINE_VERSION=not-a-runtime-version", "npm_package_version=not-a-runtime-version"}
	for _, key := range []string{"SystemRoot", "WINDIR"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func TestCLIHelpStandalone(t *testing.T) {
	bin := buildCLIHelpRelease(t, "")
	file, err := os.Open(filepath.Join(repoRoot(t), "difftest", "testdata", "cli-help", "golden.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	cwd, env := t.TempDir(), cliHelpEmptyPathEnv(t)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	count := 0
	for scanner.Scan() {
		var row struct {
			Case   string   `json:"case"`
			Args   []string `json:"args"`
			Stdout string   `json:"stdout"`
			Stderr string   `json:"stderr"`
			Exit   int      `json:"exit"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		count++
		t.Run(row.Case, func(t *testing.T) {
			stdout, stderr, code := runCLIHelpRelease(t, bin, cwd, env, row.Args...)
			if stdout != row.Stdout || stderr != row.Stderr || code != row.Exit {
				t.Fatalf("args=%q\nstdout=%q want=%q\nstderr=%q want=%q\nexit=%d want=%d",
					row.Args, stdout, row.Stdout, stderr, row.Stderr, code, row.Exit)
			}
		})
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 67 {
		t.Fatalf("CLI-help cases=%d, want 67", count)
	}
}

func TestCLIHelpVersionStamp(t *testing.T) {
	for _, build := range []struct{ name, stamp, want string }{
		{"default", "", "FaultLine 0.1.4\n"},
		{"stamped", "0.0.0-slice5a-test", "FaultLine 0.0.0-slice5a-test\n"},
	} {
		t.Run(build.name, func(t *testing.T) {
			bin := buildCLIHelpRelease(t, build.stamp)
			root := t.TempDir()
			cwd := filepath.Join(root, "work")
			if err := os.Mkdir(cwd, 0o755); err != nil {
				t.Fatal(err)
			}
			env := cliHelpEmptyPathEnv(t)
			for _, manifest := range []struct{ name, body string }{
				{"missing", ""}, {"unrelated", `{"version":"9.9.9-unrelated"}`},
				{"malformed", "{"}, {"non-string", `{"version":42}`},
			} {
				t.Run(manifest.name, func(t *testing.T) {
					for _, dir := range []string{root, cwd, filepath.Dir(bin)} {
						path := filepath.Join(dir, "package.json")
						if manifest.name == "missing" {
							if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
								t.Fatal(err)
							}
						} else if err := os.WriteFile(path, []byte(manifest.body), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					for _, alias := range []string{"--version", "-V"} {
						stdout, stderr, code := runCLIHelpRelease(t, bin, cwd, env, alias, "ignored")
						if stdout != build.want || stderr != "" || code != 0 {
							t.Fatalf("%s: stdout=%q want=%q stderr=%q exit=%d", alias, stdout, build.want, stderr, code)
						}
					}
				})
			}
		})
	}
}
