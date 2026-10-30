//go:build faultline_test

package difftest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCLIHelpBaseline(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "cli-help", "baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCLIHelpBaseline(raw); err != nil {
		t.Fatal(err)
	}
	var original map[string]string
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"schema", "sourceCommit", "manifestCommit", "manifestVersion", "nodeVersion"} {
		t.Run(key, func(t *testing.T) {
			changed := map[string]string{}
			for k, v := range original {
				changed[k] = v
			}
			changed[key] = "unexpected"
			bad, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if validateCLIHelpBaseline(bad) == nil {
				t.Fatalf("accepted changed %s", key)
			}
			delete(changed, key)
			bad, err = json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if validateCLIHelpBaseline(bad) == nil {
				t.Fatalf("accepted missing %s", key)
			}
		})
	}
	for _, bad := range []string{"null", "{}", "{", string(raw) + "{}"} {
		if validateCLIHelpBaseline([]byte(bad)) == nil {
			t.Fatalf("accepted metadata %q", bad)
		}
	}
	extra := map[string]string{}
	for k, v := range original {
		extra[k] = v
	}
	extra["unreviewed"] = "value"
	bad, err := json.Marshal(extra)
	if err != nil {
		t.Fatal(err)
	}
	if validateCLIHelpBaseline(bad) == nil {
		t.Fatal("accepted extra metadata field")
	}
}

func TestCLIHelpReplay(t *testing.T) {
	rows := loadCLIHelpCases(t)
	outside := t.TempDir()
	for _, row := range rows {
		t.Run(row.Case, func(t *testing.T) {
			stdout, stderr, code := runCLIHelpProcess(t, flBinary, outside, os.Environ(), row.Args...)
			if stdout != row.Stdout || stderr != row.Stderr || code != row.Exit {
				t.Fatalf("args=%q\nstdout=%q want=%q\nstderr=%q want=%q\nexit=%d want=%d",
					row.Args, stdout, row.Stdout, stderr, row.Stderr, code, row.Exit)
			}
		})
	}
}
