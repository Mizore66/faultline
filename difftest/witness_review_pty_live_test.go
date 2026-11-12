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
// the controlling terminal) and types the answer through the master. Pipes
// would prove only the non-TTY paths.
//
// Everything is compared EXACTLY between TS and Go, with no transform and no
// accepted alternative, for every case:
//   - the review text and prompt printed before any input;
//   - the whole transcript after the input, byte for byte (Node's readline
//     redraws, cursor moves and echo included);
//   - stderr, and the exit status or signal;
//   - the resulting store tree, byte for byte including modes, and the
//     independent verifier's verdict against an expectation written here
//     (approving inputs must freeze, all others must write nothing).
//
// The cases cover the readline question's complete editing surface: line
// editing keys, cursor movement, kill ring, undo and redo, history keys,
// arrows and function keys, escape sequences (complete, split across writes,
// timed out, unknown), wide/combining/astral text, and terminal width and
// wrapping. A behavior is not covered until a case here runs it.

type ptyRun struct {
	head     string // transcript through the first "[y/N] "
	tail     string // transcript after it
	stderr   string
	exit     *int
	signal   *string
	timedOut bool
}

// Pause markers inside a PTY input string. Each separates two separate writes
// to the terminal (two data chunks for the reader): ptyChunk waits long enough
// for the program to process the first, ptyEscWait outlasts readline's 500 ms
// escape-sequence timeout.
const (
	ptyChunk   = "\uE001"
	ptyEscWait = "\uE002"
)

func runLivePTY(t *testing.T, im liveImpl, root string, env []string, input string, wantExit bool) ptyRun {
	t.Helper()
	return runLivePTYSized(t, im, root, env, input, wantExit, 0)
}

func runLivePTYSized(t *testing.T, im liveImpl, root string, env []string, input string, wantExit bool, cols int) ptyRun {
	t.Helper()
	master, slave := openReplayPTY(t)
	defer master.Close()
	if cols > 0 {
		setPTYColumns(t, master, cols)
	}
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
	for _, step := range ptySteps(input) {
		if step.pause > 0 {
			time.Sleep(step.pause)
			continue
		}
		if _, err := master.WriteString(step.write); err != nil {
			t.Fatal(err)
		}
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
	cases = append(cases, ptyEditingCases()...)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			parent := t.TempDir()
			var runs [2]ptyRun
			var snaps [2]map[string]string
			for i, im := range impls {
				root := liveWorkdir(t, parent)
				im.propose(t, root, liveReviewPropose)
				runs[i] = runLivePTYSized(t, im, root, c.env, c.input, true, c.cols)
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
	cols        int  // terminal width; 0 leaves the pseudo-terminal's default
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

// ptyEditingCases are the readline editing behaviors of Node 22.22.2, each
// compared byte for byte between the frozen TypeScript and Go. Inputs that end
// in an approving answer must write the approval; the others must not. Every
// case ends the line (CR) or aborts, so the process exits and the transcript is
// complete. "same" is true for all of them: exit status, stderr and store tree
// are compared exactly like the basic cases.
func ptyEditingCases() []ptyCase {
	approve := func(name, input string) ptyCase {
		return ptyCase{name: name, input: input, identity: "local-reviewer", approves: true, same: true}
	}
	decline := func(name, input string) ptyCase { return ptyCase{name: name, input: input, same: true} }
	return []ptyCase{
		approve("Ctrl+A then Ctrl+K", "es\x01y\x05\x01\x06\x0b\r"),
		approve("Ctrl+W deletes the previous word", "n\x17yes\r"),
		approve("Ctrl+W after a space", "no yes\x17\x17y\r"),
		decline("Ctrl+U clears left", "yes\x15n\r"),
		approve("Ctrl+U then y", "no\x15y\r"),
		approve("Ctrl+K kills right", "yxx\x02\x02\x0b\r"),
		approve("Ctrl+B Ctrl+F", "ye\x02\x02\x06\x06s\r"),
		approve("Ctrl+E after Ctrl+A", "es\x01y\x05\r"),
		approve("Ctrl+H deletes", "yn\x08\r"),
		approve("Ctrl+D deletes right", "yx\x02\x04\r"),
		approve("Ctrl+D on empty line aborts only when empty", "x\x04\x08y\r"),
		approve("Ctrl+L refreshes", "y\x0c\r"),
		approve("Left and Right arrows", "es\x1b[D\x1b[Dy\x1b[C\x1b[C\r"),
		approve("Home and End", "es\x1b[Hy\x1b[F\r"),
		approve("Home and End SS3", "es\x1bOHy\x1bOF\r"),
		approve("Delete key", "yx\x1b[D\x1b[3~\r"),
		approve("Insert in the middle", "ys\x1b[Dè\x7fe\r"),
		approve("History up after nothing", "\x1b[Ay\r"),
		approve("History down after nothing", "\x1b[By\r"),
		approve("Unknown escape sequence is not text", "y\x1b[99~\r"),
		approve("Unknown escape then y", "\x1b[99~y\r"),
		approve("Function key is not text", "\x1bOPy\r"),
		decline("ESC y is Alt+y and inserts nothing", "\x1by\r"),
		approve("Alt+B Alt+F word moves", "yes no\x1bb\x1bf\x15y\r"),
		approve("Alt+D deletes forward word", "xx yes\x01\x1bd\r"),
		approve("Alt+Backspace deletes word", "no yes\x1b\x7f\x1b\x7fy\r"),
		approve("Ctrl+Y yanks the killed text", "yes\x15\x19\r"),
		decline("Ctrl+_ undoes one insert", "yes no\x1f\r"),
		approve("Ctrl+_ then redo", "yes\x1f\x1e\r"),
		decline("Ctrl+N Ctrl+P do nothing without history", "y\x0e\x10\x0en\r"),
		approve("Tab is inserted as text", "y\t\x08\r"),
		approve("wide character echo", "中\x7fy\r"),
		approve("combining character", "e\u0301\x7f\x7fy\r"),
		approve("emoji surrogate pair", "😀\x7fy\r"),
		approve("emoji with Left and Delete", "😀\x1b[D\x1b[3~y\r"),
		// Separate data chunks: completion mode (a refresh instead of a plain
		// echo for the last character of a chunk) and the escape timeout.
		approve("Separate chunks per key", "y"+ptyChunk+"e"+ptyChunk+"s"+ptyChunk+"\r"),
		approve("Escape sequence split across chunks", "ys\x1b"+ptyChunk+"[D"+ptyChunk+"e\r"),
		approve("Escape sequence split after CSI", "ys\x1b["+ptyChunk+"De\r"),
		decline("Lone ESC times out then text", "\x1b"+ptyEscWait+"n\r"),
		approve("Lone ESC timeout then y", "\x1b"+ptyEscWait+"y\r"),
		approve("Double ESC", "\x1b\x1b[Dy\r"),
		approve("Ctrl+Left and Ctrl+Right word moves", "yes no\x1b[1;5D\x0b\x1b[1;5C\r"),
		approve("Shift+Home is not a plain Home", "es\x1b[1;2Hy\r"),
		approve("Rxvt Home and End", "es\x1b[7~y\x1b[8~\r"),
		approve("Page keys do nothing", "y\x1b[5~\x1b[6~\r"),
		approve("Insert key does nothing", "y\x1b[2~\r"),
		approve("Function keys F1 to F12", "\x1bOQ\x1b[15~\x1b[24~\x1b[[Ay\r"),
		approve("Bracketed paste markers", "\x1b[200~y\x1b[201~\r"),
		approve("Ctrl+Delete deletes a word", "no yes\x01\x1b[3;5~\r"),
		approve("Ctrl+Shift+Delete kills to the end", "yes xx\x1b[D\x1b[D\x1b[D\x1b[3;6~\r"),
		approve("Alt+Left and Alt+Right", "yes no\x1b[1;3D\x1b[1;3C\x15y\r"),
		approve("Alt+Delete", "xx yes\x01\x1b[3;3~\r"),
		approve("Up then Down restore", "\x1b[A\x1b[By\r"),
		approve("Ctrl+P and Ctrl+N with text", "y\x10\x0e\r"),
		approve("Up and Down with text", "y\x1b[A\x1b[B\r"),
		decline("Kill ring yank pop", "no\x15yes\x15\x19\x1by\r"),
		approve("Yank the killed text", "yes\x15\x19\r"),
		approve("Undo after kill", "yes\x15\x1f\r"),
		approve("Redo after undo", "yes\x1f\x1e\r"),
		approve("Ctrl+L then text", "n\x0cy\x7f\x7f\x0cy\r"),
		decline("Control keys with no binding are inserted as text", "y\x07\x0f\x11\x12\x13\x14\x16\x18\x1c\x1d\r"),
		approve("Vertical tab and form feed", "y\x0b\x0c\r"),
		approve("NUL byte", "y\x00\r"),
		{name: "Wrap at 40 columns", input: "yes yes yes yes yes yes yes yes yes\x15y\r", identity: "local-reviewer", approves: true, cols: 40, same: true},
		{name: "Wrap with backspaces at 40 columns", input: "yes yes yes yes yes yes yes yes yes\x7f\x7f\x7f\x15y\r", identity: "local-reviewer", approves: true, cols: 40, same: true},
		{name: "Cursor moves across a wrapped line", input: "yes yes yes yes yes yes yes yes yes\x01\x05\x15y\r", identity: "local-reviewer", approves: true, cols: 40, same: true},
		{name: "Wide characters at the wrap edge", input: "中中中中中中中中中中中中中中中\x15y\r", identity: "local-reviewer", approves: true, cols: 40, same: true},
		{name: "Very narrow terminal", input: "y\r", identity: "local-reviewer", approves: true, cols: 10, same: true},
		{name: "Terminal narrower than the prompt", input: "xyes\x01\x04\r", identity: "local-reviewer", approves: true, cols: 5, same: true},
		approve("Tab in the width calculation", "\t\x7fy\r"),
		approve("Latin text with an accent", "caf\u00e9\x7f\x7f\x7f\x7f\x7fy\r"),
		approve("Regional indicator pair", "\U0001F1FA\U0001F1F8\x7f\x7fy\r"),
		approve("Zero width joiner emoji", "\U0001F468\u200d\U0001F469\x15y\r"),
		approve("Text after Enter is ignored", "y\rignored\r"),
		approve("Ctrl+D deletes the character under the cursor", "yxes\x01\x06\x04\r"),
		approve("CRLF as one enter", "y\r\n"),
		approve("bare LF enter", "y\n"),
	}
}

type ptyStep struct {
	write string
	pause time.Duration
}

// ptySteps splits an input string at its pause markers into separate writes
// and sleeps.
func ptySteps(input string) []ptyStep {
	var steps []ptyStep
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			steps = append(steps, ptyStep{write: cur.String()})
			cur.Reset()
		}
	}
	for _, r := range input {
		switch string(r) {
		case ptyChunk:
			flush()
			steps = append(steps, ptyStep{pause: 150 * time.Millisecond})
		case ptyEscWait:
			flush()
			steps = append(steps, ptyStep{pause: 800 * time.Millisecond})
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return steps
}
