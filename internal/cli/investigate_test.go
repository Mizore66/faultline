package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidCodexThreadID(t *testing.T) {
	for _, ok := range []string{"a", "thr_01.draft", "thr:02", "A-b.c_d:e", strings.Repeat("x", 160)} {
		if !validCodexThreadID(ok) {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"", "-bad", ".x", "_x", ":x", "a b", "a/b", "é", "a\n", strings.Repeat("x", 161)} {
		if validCodexThreadID(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestInvestigateUsageAndGuards(t *testing.T) {
	usage := "FaultLine error: Usage: fl investigate --ci-log <file> --repo <directory> [...] | fl investigate --resume <id> --repo <directory> [...] | fl investigate turns --repo ... (--ledger ... | --latest) --proposal ... --expect-digest ... --image ... [--runtime-mapping <mapping.json>] [--output <dir>] | fl investigate git --repo <directory> --from <commit> --to <commit> --proposal <id> --expect-digest <sha256:...> --image <digest-pinned-image> [--runtime-mapping <mapping.json>] [--ledger <ledger.json>] [--output <managed-bundle-directory>]\nNext: pnpm fl help\n"
	guard := "FaultLine error: Guided investigate (--ci-log / --resume) cannot be combined with `fl investigate git` or `fl investigate turns`.\nNext: pnpm fl help\n"
	for _, c := range []struct {
		args   []string
		stderr string
	}{
		{[]string{"investigate"}, usage},
		{[]string{"investigate", "bisect"}, usage},
		{[]string{"investigate", "git", "--ci-log", "x.log"}, guard},
		{[]string{"investigate", "turns", "--resume", "id"}, guard},
		{[]string{"investigate", "--ci-log", "x.log"}, "FaultLine error: not yet ported: investigate --ci-log/--resume\nNext: pnpm fl help\n"},
		{[]string{"investigate", "turns"}, "FaultLine error: not yet ported: investigate turns\nNext: pnpm fl help\n"},
		{[]string{"investigate", "git"}, "FaultLine error: Missing required option: --proposal\nNext: pnpm fl help\n"},
		{[]string{"investigate", "git", "--proposal", "p", "--repair-thread-id", "-bad"},
			"FaultLine error: --repair-thread-id must be a Codex thread identifier (1-160 characters, [A-Za-z0-9._:-], starting with a letter or digit).\nNext: pnpm fl help\n"},
		{[]string{"investigate", "git", "--proposal", "p", "--repair-thread-id"},
			"FaultLine error: --repair-thread-id must be a Codex thread identifier (1-160 characters, [A-Za-z0-9._:-], starting with a letter or digit).\nNext: pnpm fl help\n"},
		{[]string{"investigate", "git", "--witness-draft-thread-id", "--proposal", "p"},
			"FaultLine error: --witness-draft-thread-id must be a Codex thread identifier (1-160 characters, [A-Za-z0-9._:-], starting with a letter or digit).\nNext: pnpm fl help\n"},
		{[]string{"investigate", "git", "--witness-draft-thread-id", "", "--repair-thread-id", "-bad"},
			"FaultLine error: --witness-draft-thread-id must be a Codex thread identifier (1-160 characters, [A-Za-z0-9._:-], starting with a letter or digit).\nNext: pnpm fl help\n"},
	} {
		stdout, stderr, code := run(c.args...)
		if code != 1 || stdout != "" || stderr != c.stderr {
			t.Errorf("%q: code=%d stdout=%q stderr=%q", c.args, code, stdout, stderr)
		}
	}
}

// A bad thread id is refused before anything is read or written: the
// session binding the git branch would write never appears.
func TestInvestigateBadThreadIDWritesNothing(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	_, stderr, code := run("investigate", "git", "--repo", ".", "--proposal", "demo-1", "--expect-digest", "sha256:"+strings.Repeat("a", 64),
		"--witness-draft-thread-id", "bad id")
	if code != 1 || !strings.Contains(stderr, "--witness-draft-thread-id must be") {
		t.Fatalf("%d %q", code, stderr)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir)); len(entries) != 0 {
		t.Fatalf("wrote %v", entries)
	}
}
