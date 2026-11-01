//go:build faultline_test

package difftest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type cliHelpCase struct {
	Case   string   `json:"case"`
	Args   []string `json:"args"`
	Stdout string   `json:"stdout"`
	Stderr string   `json:"stderr"`
	Exit   int      `json:"exit"`
}

func validateCLIHelpBaseline(raw []byte) error {
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		return err
	}
	want := map[string]string{
		"schema":          "faultline.cli-help-oracle.v1",
		"sourceCommit":    "ce99a793af0b020bf1b9cf21114f8d7897b37207",
		"manifestCommit":  "6f2450549acb002f04a73dd3c1e3091299d7d59f",
		"manifestVersion": "0.1.4",
		"nodeVersion":     "22.22.2",
	}
	if len(got) != len(want) {
		return fmt.Errorf("CLI-help metadata fields: got %d, want %d", len(got), len(want))
	}
	for k, v := range want {
		if got[k] != v {
			return fmt.Errorf("CLI-help metadata %s: got %q, want %q", k, got[k], v)
		}
	}
	return nil
}

func loadCLIHelpCases(t *testing.T) []cliHelpCase {
	t.Helper()
	directory := filepath.Join("testdata", "cli-help")
	raw, err := os.ReadFile(filepath.Join(directory, "baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCLIHelpBaseline(raw); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filepath.Join(directory, "golden.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var rows []cliHelpCase
	seen := map[string]bool{}
	for scanner.Scan() {
		var row cliHelpCase
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		if row.Case == "" || seen[row.Case] || row.Args == nil || row.Exit < 0 || row.Exit > 1 {
			t.Fatalf("invalid/duplicate CLI-help case %q", row.Case)
		}
		seen[row.Case] = true
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 67 {
		t.Fatalf("CLI-help cases=%d, want 67", len(rows))
	}
	return rows
}

// oracleNodeVersionOutputMatches reports whether a `node --version` run is the
// pinned oracle's: the exact version line, empty stderr and exit 0. goos names
// the platform the process ran on. The version line is exact per platform:
// Node's text-mode stdout on Windows ends it with CRLF, on every other platform
// with LF. Nothing is trimmed, stripped or accepted as an alternative.
func oracleNodeVersionOutputMatches(goos, stdout, stderr string, code int) bool {
	want := "v22.22.2\n"
	if goos == "windows" {
		want = "v22.22.2\r\n"
	}
	return stdout == want && stderr == "" && code == 0
}

func runCLIHelpProcess(t *testing.T, binary, dir string, env []string, args ...string) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir, cmd.Env = dir, env
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("CLI-help process timeout: %q %q", binary, args)
	}
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("CLI-help process: %v", err)
	}
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}
