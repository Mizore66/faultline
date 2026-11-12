//go:build faultline_test && (linux || darwin)

package difftest

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/oracle"
)

// TestLiveWitnessReviewPTY runs the frozen TypeScript and Go `witness review
// p1` on a REAL pseudo-terminal (stdin and stdout are the slave, which is also
// the controlling terminal) and answers the prompt through the master. Pipes
// would prove only the non-TTY paths.
//
// Compared exactly between TS and Go, for every answer:
//   - the review text and prompt printed before any input (the PTY transcript up
//     to and including "[y/N] ");
//   - the resulting store tree, byte for byte, including modes, and the
//     independent verifier's verdict against an expectation written here;
//   - the exit class for the answers whose behavior is identical.
//
// Terminal-level behavior that differs (Node's readline puts the terminal in
// raw mode and redraws the prompt line; Go leaves the line discipline alone) is
// NOT normalized away: after the prompt each side is checked against its own
// independently written expectation, and the observed differences are asserted
// as differences below and recorded in difftest/KNOWN_DIFFERENCES.md.

type ptyRun struct {
	head     string // transcript through the first "[y/N] "
	tail     string // transcript after it
	stderr   string
	exit     *int
	signal   *string
	timedOut bool
}

func runLivePTY(t *testing.T, im liveImpl, root string, env []string, input string, wantExit bool) ptyRun {
	t.Helper()
	master, slave := openReplayPTY(t)
	defer master.Close()
	cmd := exec.Command(im.exe, append(slicesClone(im.prefix), "witness", "review", liveReviewID)...)
	cmd.Dir, cmd.Env = root, im.environ(root, env...)
	cmd.Stdin, cmd.Stdout = slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	var errb lockedBuffer
	cmd.Stderr = &errb
	var out lockedBuffer
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&out, master) // ends with EIO once every slave closes
	}()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Prompt-readiness barrier, never a sleep.
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(out.String(), "[y/N] ") {
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("%s: no prompt: %q stderr %q", im.name, out.String(), errb.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	head, _, _ := strings.Cut(out.String(), "[y/N] ")
	head += "[y/N] "
	if _, err := master.WriteString(input); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	run := ptyRun{head: head}
	wait := 20 * time.Second
	if !wantExit {
		wait = 4 * time.Second
	}
	select {
	case <-done:
	case <-time.After(wait):
		run.timedOut = true
		_ = cmd.Process.Kill()
		<-done
	}
	_ = slave.Close()
	wg.Wait()
	full := out.String()
	run.tail = strings.TrimPrefix(full, head)
	run.stderr = errb.String()
	if !run.timedOut {
		code, sig := exitOf(cmd.ProcessState)
		run.exit, run.signal = code, sig
	}
	return run
}

func exitOf(state *os.ProcessState) (*int, *string) {
	ws := state.Sys().(syscall.WaitStatus)
	if ws.Signaled() {
		s := ws.Signal().String()
		return nil, &s
	}
	c := ws.ExitStatus()
	return &c, nil
}

func TestLiveWitnessReviewPTY(t *testing.T) {
	oracle.Skip(t)
	node, cli := pinServeBundleOracle(t)
	impls := liveReviewImpls(t, node, cli)
	configured := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(configured, []byte("[user]\n\tname = PTY Reviewer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []ptyCase{
		{name: "y", input: "y\r", identity: "local-reviewer", approves: true, same: true},
		{name: "yes", input: "yes\r", identity: "local-reviewer", approves: true, same: true},
		{name: "yes with LF", input: "yes\n", identity: "local-reviewer", approves: true, same: true},
		{name: "padded upper YES", input: " YES \r", identity: "local-reviewer", approves: true, same: true},
		{name: "configured git user.name", input: "y\r", env: []string{"GIT_CONFIG_GLOBAL=" + configured}, identity: "PTY Reviewer", approves: true, same: true},
		{name: "backspace then n", input: "y\x7fn\r", same: true},
		{name: "n", input: "n\r", same: true},
		{name: "empty line", input: "\r", same: true},
		{name: "yep", input: "yep\r", same: true},
		{name: "no", input: "no\r", same: true},
		{name: "EOF Ctrl+D", input: "\x04"},
		{name: "Ctrl+C", input: "\x03"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			parent := t.TempDir()
			var runs [2]ptyRun
			var snaps [2]map[string]string
			for i, im := range impls {
				root := liveWorkdir(t, parent)
				im.propose(t, root, liveReviewPropose)
				runs[i] = runLivePTY(t, im, root, c.env, c.input, true)
				if runs[i].timedOut {
					t.Fatalf("%s: did not exit: %+v", im.name, runs[i])
				}
				want := "none"
				if c.approves {
					want = "frozen"
				}
				state, err := verifyWitnessStore(root, liveReviewID)
				if err != nil || state != want {
					t.Fatalf("%s: store state %q err %v, want %q", im.name, state, err, want)
				}
				if c.approves {
					rec, _, err := recordFile(filepath.Join(root, ".faultline", "witnesses", "approvals", liveReviewID+".json"))
					if err != nil || asString(rec["approvedBy"]) != c.identity {
						t.Fatalf("%s: approval %v err %v, want approvedBy %q", im.name, rec, err, c.identity)
					}
					frozen, _, _ := recordFile(filepath.Join(root, ".faultline", "witnesses", "frozen", liveReviewID+".json"))
					line := fmt.Sprintf("Frozen %s · approvedBy %s · frozenDigest %s\n", liveReviewID, c.identity, asString(frozen["frozenDigest"]))
					if !strings.Contains(strings.ReplaceAll(runs[i].tail, "\r\n", "\n"), line) {
						t.Fatalf("%s: transcript tail %q lacks %q", im.name, runs[i].tail, line)
					}
					if runs[i].exit == nil || *runs[i].exit != 0 {
						t.Fatalf("%s: exit %v signal %v, want 0", im.name, runs[i].exit, runs[i].signal)
					}
				}
				snaps[i], _ = storeSnapshot(root)
			}
			ts, goRun := runs[0], runs[1]
			if !strings.Contains(ts.head, `Approve & freeze as "`+c.identity+`"? [y/N] `) && c.identity != "" {
				t.Errorf("TS prompt lacks identity %q: %q", c.identity, ts.head[max(0, len(ts.head)-80):])
			}
			// Exact parity: no transform, no accepted alternative.
			if ts.head != goRun.head {
				t.Errorf("review text and prompt differ\n%s", serveBundleDiff([]byte(ts.head), []byte(goRun.head)))
			}
			if ts.tail != goRun.tail {
				t.Errorf("transcript after the answer differs\nTS %q\nGo %q", ts.tail, goRun.tail)
			}
			if d := diffSnapshots(snaps[0], snaps[1]); d != "" {
				t.Errorf("store trees differ:\n%s", d)
			}
			tsExit, goExit := fmt.Sprint(ptrInt(ts.exit), ptrStr(ts.signal)), fmt.Sprint(ptrInt(goRun.exit), ptrStr(goRun.signal))
			if tsExit != goExit {
				t.Errorf("exit differs: TS %s Go %s", tsExit, goExit)
			}
			if ts.stderr != goRun.stderr {
				t.Errorf("stderr differs: TS %q Go %q", ts.stderr, goRun.stderr)
			}
			t.Logf("TS : exit=%s stderr=%q tail=%q", tsExit, ts.stderr, ts.tail)
			t.Logf("Go : exit=%s stderr=%q tail=%q", goExit, goRun.stderr, goRun.tail)
		})
	}
	// Unterminated final line: Node's readline question never settles at EOF
	// (the Task 4 correction); with the terminal still open it simply waits.
	t.Run("unterminated yes then Ctrl+D waits", func(t *testing.T) {
		parent := t.TempDir()
		for _, im := range impls {
			root := liveWorkdir(t, parent)
			im.propose(t, root, liveReviewPropose)
			run := runLivePTY(t, im, root, nil, "yes\x04", false)
			if !run.timedOut {
				t.Errorf("%s exited: %+v", im.name, run)
			}
			if state, err := verifyWitnessStore(root, liveReviewID); err != nil || state != "none" {
				t.Errorf("%s: state %q err %v, an unterminated yes must not approve", im.name, state, err)
			}
		}
	})
}

type ptyCase struct {
	name, input string
	env         []string
	identity    string
	approves    bool
	same        bool // exit/signal (and stderr when declining) identical between TS and Go
}

func (c ptyCase) identityOrDefault() string {
	if c.identity == "" {
		return "local-reviewer"
	}
	return c.identity
}

func ptrInt(p *int) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprint(*p)
}

func ptrStr(p *string) string {
	if p == nil {
		return "-"
	}
	return *p
}
