//go:build faultline_test && windows

package difftest

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/oracle"
)

// TestLiveWitnessReviewConsole is the Windows twin of TestLiveWitnessReviewPTY:
// the frozen TypeScript and Go `witness review p1` each run in a console of
// their own (CREATE_NEW_CONSOLE) hosted by console-session, which types real
// console key events and reads the console screen buffer. Compared exactly,
// per answer: the final screen text, the stderr text, the exit status, and the
// resulting witness store tree, checked by the independent verifier.
//
// It cannot run on a non-Windows machine. It is compiled and vetted for
// windows/amd64 and windows/arm64 in this repository's cross-compilation gate;
// the hosted Windows runs are the execution gate (see the Task 6 report).
func TestLiveWitnessReviewConsole(t *testing.T) {
	oracle.Skip(t)
	node, cli := pinServeBundleOracle(t)
	impls := liveReviewImpls(t, node, cli)
	session := buildConsoleSession(t)
	cases := []struct {
		name, keys string
		approves   bool
		exit       int
	}{
		{"y", "y<enter>", true, 0},
		{"yes", "yes<enter>", true, 0},
		{"padded upper YES", " YES <enter>", true, 0},
		{"n", "n<enter>", false, 1},
		{"empty line", "<enter>", false, 1},
		{"yep", "yep<enter>", false, 1},
		{"backspace then n", "y<bs>n<enter>", false, 1},
		{"Ctrl+W deletes the word", "n<ctrl-w>yes<enter>", true, 0},
		{"Ctrl+U clears the line", "no<ctrl-u>y<enter>", true, 0},
		{"Left then insert", "es<left><left>y<right><right><enter>", false, 1},
		{"Home and End", "es<home>y<end><enter>", true, 0},
		{"Delete key", "yx<left><del><enter>", true, 0},
		{"Ctrl+D aborts", "<ctrl-d>", false, 1},
		{"Ctrl+C aborts", "<ctrl-c>", false, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			parent := t.TempDir()
			var screens, stderrs [2]string
			var exits [2]int
			var snaps [2]map[string]string
			for i, im := range impls {
				root := liveWorkdir(t, parent)
				im.propose(t, root, liveReviewPropose)
				screens[i], stderrs[i], exits[i] = runConsoleSession(t, session, im, root, c.keys)
				want := "none"
				if c.approves {
					want = "frozen"
				}
				if state, err := verifyWitnessStore(root, liveReviewID); err != nil || state != want {
					t.Fatalf("%s: store state %q err %v, want %q", im.name, state, err, want)
				}
				if exits[i] != c.exit {
					t.Errorf("%s: exit %d, want %d (stderr %q)", im.name, exits[i], c.exit, stderrs[i])
				}
				snaps[i], _ = storeSnapshot(root)
			}
			if screens[0] != screens[1] {
				t.Errorf("console screens differ\n--- TS\n%s\n--- Go\n%s", screens[0], screens[1])
			}
			if stderrs[0] != stderrs[1] {
				t.Errorf("stderr differs: TS %q Go %q", stderrs[0], stderrs[1])
			}
			if d := diffSnapshots(snaps[0], snaps[1]); d != "" {
				t.Errorf("store trees differ:\n%s", d)
			}
		})
	}
}

func buildConsoleSession(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "console-session.exe")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", out, "github.com/Mizore66/faultline/difftest/servefixture/console-session")
	build.Dir = oracle.RepoRoot(t)
	build.WaitDelay = time.Second
	if combined, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building console-session: %v\n%s", err, combined)
	}
	return out
}

// runConsoleSession runs one side in its own new console and returns the final
// screen text, the stderr text and the exit status.
func runConsoleSession(t *testing.T, session string, im liveImpl, root, keys string) (screen, stderr string, exit int) {
	t.Helper()
	dir := t.TempDir()
	args := append([]string{"host", dir, im.exe}, append(slicesClone(im.prefix), "witness", "review", liveReviewID)...)
	cmd := exec.Command(session, args...)
	cmd.Dir, cmd.Env = root, im.environ(root)
	cmd.SysProcAttr = newConsoleAttr()
	var hostOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &hostOut, &hostOut
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	pid := strconv.Itoa(cmd.Process.Pid)
	run := func(args ...string) string {
		out, err := exec.Command(session, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("console-session %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	// Prompt-readiness barrier: read the screen until the prompt is shown.
	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(run("screen", pid), "[y/N] ") {
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("%s: no prompt on the console; host output %q", im.name, hostOut.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	run("send", pid, keys)
	exitFile := filepath.Join(dir, "exit")
	for {
		if _, err := os.Stat(exitFile); err == nil {
			break
		}
		if time.Now().After(deadline.Add(30 * time.Second)) {
			_ = cmd.Process.Kill()
			t.Fatalf("%s: the program did not exit; screen:\n%s", im.name, run("screen", pid))
		}
		time.Sleep(100 * time.Millisecond)
	}
	screen = run("screen", pid)
	raw, err := os.ReadFile(exitFile)
	if err != nil {
		t.Fatal(err)
	}
	if exit, err = strconv.Atoi(string(raw)); err != nil {
		t.Fatalf("exit file %q: %v", raw, err)
	}
	errBytes, _ := os.ReadFile(filepath.Join(dir, "stderr"))
	stderr = strings.ReplaceAll(string(errBytes), "\r\n", "\n")
	if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	<-done
	return screen, stderr, exit
}
