package ledger

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

func TestCaptureRefusesDirtyAndSigns(t *testing.T) {
	calls := []string{}
	run := func(repo string, args []string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		switch strings.Join(args, " ") {
		case "rev-parse --show-toplevel":
			return "/r", nil
		case "ls-files --others --exclude-standard":
			return "new.txt", nil
		}
		return "", nil
	}
	_, err := CaptureGitCleanCheckpoint("/r", CaptureOptions{RunGit: run})
	if err == nil || err.Error() != ErrDirtyWorktree || len(calls) != 3 {
		t.Fatalf("%v %q", err, calls)
	}
	var ie *IntegrityError
	if !errors.As(err, &ie) {
		t.Fatalf("%T", err)
	}
}

// The scripted runner's outputs are used as they are (the runner trims),
// the clock is read once after every Git call, and a runner error passes
// through unchanged.
func TestCaptureScriptedRunner(t *testing.T) {
	head, tree := strings.Repeat("a", 40), strings.Repeat("b", 40)
	var calls []string
	run := func(repo string, args []string) (string, error) {
		calls = append(calls, repo+" "+strings.Join(args, " "))
		switch strings.Join(args, " ") {
		case "rev-parse --show-toplevel":
			return "/r/top", nil
		case "rev-parse HEAD":
			return head, nil
		case "rev-parse HEAD^{tree}":
			return tree, nil
		}
		return "", nil
	}
	nows := 0
	now := func() time.Time { nows++; return time.UnixMilli(1784203200123) }
	cp, err := CaptureGitCleanCheckpoint("/r/top/sub", CaptureOptions{RunGit: run, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	// resolve() keeps these POSIX paths; on Windows it adds the drive.
	sub, top := nodefs.Resolve("/r/top/sub"), nodefs.Resolve("/r/top")
	if nows != 1 || len(calls) != 5 || calls[0] != sub+" rev-parse --show-toplevel" || calls[1] != top+" diff-index --ignore-cr-at-eol HEAD" {
		t.Fatalf("%d %q", nows, calls)
	}
	if cp.Get("repositoryRoot").Str() != top || cp.Get("capturedAt").Str() != "2026-07-16T12:00:00.123Z" {
		t.Fatal(jsjson.Stringify(cp))
	}
	if errs := gitproof.VerifyGitCheckpoint(cp); len(errs) != 0 {
		t.Fatal(errs)
	}
	keys := strings.Join(cp.Obj().Keys(), ",")
	if keys != "schemaVersion,repositoryRoot,headCommit,treeDigest,capturedAt,clean,digest" {
		t.Fatal(keys)
	}

	boom := errors.New("Git checkpoint capture failed for rev-parse HEAD: spawnSync git ETIMEDOUT")
	failing := func(repo string, args []string) (string, error) {
		if strings.Join(args, " ") == "rev-parse HEAD" {
			return "", boom
		}
		return run(repo, args)
	}
	if _, err := CaptureGitCleanCheckpoint("/r", CaptureOptions{RunGit: failing, Now: now}); err != boom || nows != 1 {
		t.Fatalf("%v %d", err, nows)
	}
	padded := func(repo string, args []string) (string, error) {
		out, err := run(repo, args)
		if strings.Join(args, " ") == "rev-parse HEAD" {
			out += "\n"
		}
		return out, err
	}
	if _, err := CaptureGitCleanCheckpoint("/r", CaptureOptions{RunGit: padded, Now: now}); err == nil || !strings.Contains(err.Error(), "Expected a Git object id") {
		t.Fatalf("%v", err)
	}
}

func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=FaultLine test", "-c", "user.email=faultline@example.invalid",
		"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %q: %v\n%s", args, err, out)
	}
	return string(out)
}

var objectID = regexp.MustCompile(`^[a-f0-9]{40}$`)

// tests/ledger.test.ts:197.
func TestCaptureRealCleanCheckpoint(t *testing.T) {
	repository := t.TempDir()
	testGit(t, repository, "init", "-q")
	os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("stable\n"), 0o644)
	testGit(t, repository, "add", "tracked.txt")
	testGit(t, repository, "commit", "-q", "-m", "checkpoint fixture")

	capturedAt := "2026-07-16T00:00:01.500Z"
	cp, err := CaptureGitCleanCheckpoint(repository, CaptureOptions{Now: func() time.Time { return time.UnixMilli(1784160001500) }})
	if err != nil {
		t.Fatal(err)
	}
	if !cp.Get("clean").Bool() || !objectID.MatchString(cp.Get("headCommit").Str()) || !objectID.MatchString(cp.Get("treeDigest").Str()) || cp.Get("capturedAt").Str() != capturedAt {
		t.Fatal(jsjson.Stringify(cp))
	}
	if errs := gitproof.VerifyGitCheckpoint(cp); len(errs) != 0 {
		t.Fatal(errs)
	}

	l, _ := Create(CreateOptions{LedgerID: "ledger-git", SessionID: "session-git", CreatedAt: createdAt})
	l = appendAt(t, l, `{"type":"SESSION_STARTED","payload":{"transport":"CODEX_CLI","workingDirectory":`+jsjson.Quote(repository)+`}}`, 0)
	l, err = Append(l, mustJSON(t, `{"type":"WORKTREE_CHECKPOINT","payload":{"checkpoint":`+jsjson.Stringify(cp)+`,"afterTurnOrdinal":0}}`), AppendOptions{EventID: "event-2", OccurredAt: times[1]})
	if err != nil || !Verify(l).Valid {
		t.Fatalf("%v", err)
	}

	os.WriteFile(filepath.Join(repository, "untracked.txt"), []byte("dirty\n"), 0o644)
	if _, err := CaptureGitCleanCheckpoint(repository, CaptureOptions{}); err == nil || err.Error() != ErrDirtyWorktree {
		t.Fatalf("%v", err)
	}
}

// A capture outside a repository fails with the runner's plain error.
func TestCaptureNotARepository(t *testing.T) {
	dir := t.TempDir()
	_, err := CaptureGitCleanCheckpoint(dir, CaptureOptions{})
	var ie *IntegrityError
	if err == nil || errors.As(err, &ie) || !strings.HasPrefix(err.Error(), "Git checkpoint capture failed for rev-parse --show-toplevel: fatal: not a git repository") {
		t.Fatalf("%v", err)
	}
}

// tests/fl-record-checkpoint-fsmonitor.test.ts:29: a repository-local
// core.fsmonitor that would write a sentinel never runs.
func TestCaptureNeverRunsRepositoryFsmonitor(t *testing.T) {
	root := t.TempDir()
	sentinel := filepath.Join(root, "faultline-security-sentinel")
	repo := filepath.Join(root, "repo")
	testGit(t, root, "init", "-q", repo)
	testGit(t, repo, "checkout", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("ok\n"), 0o644)
	testGit(t, repo, "add", "tracked.txt")
	testGit(t, repo, "commit", "-qm", "init")
	var monitor string
	if runtime.GOOS == "windows" {
		monitor = filepath.Join(root, "fsmonitor.cmd")
		os.WriteFile(monitor, []byte("@echo off\r\necho fsmonitor-fired> \""+sentinel+"\"\r\nexit /b 0\r\n"), 0o644)
	} else {
		monitor = filepath.Join(root, "fsmonitor.sh")
		os.WriteFile(monitor, []byte("#!/bin/sh\nprintf fsmonitor-fired > \""+sentinel+"\"\nexit 0\n"), 0o755)
	}
	testGit(t, repo, "config", "core.fsmonitor", monitor)

	cp, err := CaptureGitCleanCheckpoint(repo, CaptureOptions{})
	if err != nil || !cp.Get("clean").Bool() {
		t.Fatalf("%v", err)
	}
	if _, err := os.Lstat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("fsmonitor ran: %v", err)
	}
}
