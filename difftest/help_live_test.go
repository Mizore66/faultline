//go:build faultline_test

package difftest

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
)

func TestLiveCLIHelp(t *testing.T) {
	oracle.Skip(t)
	rows := loadCLIHelpCases(t)
	repo := oracle.RepoRoot(t)
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	outside, env := t.TempDir(), os.Environ()
	stdout, stderr, code := runCLIHelpProcess(t, node, outside, env, "--version")
	if !oracleNodeVersionOutputMatches(runtime.GOOS, stdout, stderr, code) {
		t.Fatalf("oracle Node drift: stdout=%q stderr=%q exit=%d", stdout, stderr, code)
	}
	raw, err := os.ReadFile(filepath.Join(repo, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version any `json:"version"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "0.1.4" {
		t.Fatalf("oracle manifest drift: %#v", manifest.Version)
	}
	cli := filepath.Join(repo, "dist", "cli.js")
	stdout, stderr, code = runCLIHelpProcess(t, node, outside, env, cli, "--version")
	if stdout != "FaultLine 0.1.4\n" || stderr != "" || code != 0 {
		t.Fatalf("oracle CLI-version drift: stdout=%q stderr=%q exit=%d", stdout, stderr, code)
	}
	for _, row := range rows {
		t.Run(row.Case, func(t *testing.T) {
			tsOut, tsErr, tsCode := runCLIHelpProcess(t, node, outside, env, append([]string{cli}, row.Args...)...)
			if tsOut != row.Stdout || tsErr != row.Stderr || tsCode != row.Exit {
				t.Fatalf("TS oracle drift: stdout=%q want=%q stderr=%q want=%q exit=%d want=%d",
					tsOut, row.Stdout, tsErr, row.Stderr, tsCode, row.Exit)
			}
			goOut, goErr, goCode := runCLIHelpProcess(t, flBinary, outside, env, row.Args...)
			if goOut != tsOut || goErr != tsErr || goCode != tsCode {
				t.Fatalf("Go/TS: stdout=%q want=%q stderr=%q want=%q exit=%d want=%d",
					goOut, tsOut, goErr, tsErr, goCode, tsCode)
			}
		})
	}
}
