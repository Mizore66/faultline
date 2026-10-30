package cli

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCLIHelpFastPaths(t *testing.T) {
	file, err := os.Open(filepath.Join("..", "..", "difftest", "testdata", "cli-help", "golden.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	seen := map[string]bool{}
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
		if row.Case == "" || seen[row.Case] {
			t.Fatalf("duplicate/empty case %q", row.Case)
		}
		seen[row.Case] = true
		t.Run(row.Case, func(t *testing.T) {
			stdout, stderr, code := run(row.Args...)
			if stdout != row.Stdout || stderr != row.Stderr || code != row.Exit {
				t.Fatalf("args=%q\nstdout=%q want=%q\nstderr=%q want=%q\nexit=%d want=%d",
					row.Args, stdout, row.Stdout, stderr, row.Stderr, code, row.Exit)
			}
		})
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 67 {
		t.Fatalf("cases=%d, want 67", len(seen))
	}
}
