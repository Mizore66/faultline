//go:build faultline_test && linux

package difftest

import (
	"io"
	"os"

	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/oracle"
)

// shellExec is strace's line for exec()'s `/bin/sh -c <command>`.
var shellExec = regexp.MustCompile(`execve\("/bin/sh", \["/bin/sh", "-c", ("(?:[^"\\]|\\.)*")\]`)

// TestLiveOpenerShellCommand runs judge-proof with the browser enabled under
// strace -f -e trace=execve on both sides and compares the command line
// exec() hands to /bin/sh: the opener string itself, quoting included,
// which a fake xdg-open cannot see because the shell removes the quotes
// (round 7 #14, mutant P5). Linux only (strace, xdg-open).
func TestLiveOpenerShellCommand(t *testing.T) {
	oracle.Skip(t)
	requireStrace(t)
	repo := repoRoot(t)
	fake := filepath.Join(repo, "difftest", "testdata", "fakebrowser")
	got := map[string]string{}
	for _, side := range judgeSides(repo) {
		trace := filepath.Join(t.TempDir(), "trace")
		log := filepath.Join(t.TempDir(), "browser.log")
		env := judgeEnv("PATH="+fake+string(os.PathListSeparator)+os.Getenv("PATH"), "FAULTLINE_BROWSER_LOG="+log)
		strace := judgeSide{side.name, append([]string{"strace", "-f", "-qq", "-s", "4096", "-e", "trace=execve", "-o", trace}, side.argv...)}
		cmd, _, out, rest, stderr := startJudge(t, repo, strace, env, "--port", "0")
		done := make(chan struct{})
		go func() {
			tail, _ := io.ReadAll(rest)
			out.Write(tail)
			close(done)
		}()
		deadline := time.Now().Add(60 * time.Second)
		for {
			if _, err := os.Stat(log); err == nil {
				break
			}
			if time.Now().After(deadline) {
				cmd.Process.Kill()
				t.Fatalf("%s: the opener did not run\n%s", side.name, stderr.String())
			}
			time.Sleep(20 * time.Millisecond)
		}
		// SIGTERM to fl itself (strace's tracee), not to strace: strace
		// would detach and leave fl serving.
		pids, _ := os.ReadFile("/proc/" + strconv.Itoa(cmd.Process.Pid) + "/task/" + strconv.Itoa(cmd.Process.Pid) + "/children")
		for _, pid := range strings.Fields(string(pids)) {
			if n, err := strconv.Atoi(pid); err == nil {
				syscall.Kill(n, syscall.SIGTERM)
			}
		}
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			cmd.Process.Kill()
			t.Fatalf("%s: fl did not stop", side.name)
		}
		cmd.Wait()
		b, err := os.ReadFile(trace)
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		for _, m := range shellExec.FindAllStringSubmatch(string(b), -1) {
			lines = append(lines, loopbackPort.ReplaceAllString(m[1], "127.0.0.1:<PORT>"))
		}
		if len(lines) == 0 {
			t.Fatalf("%s: no /bin/sh -c in the trace\n%s", side.name, b)
		}
		got[side.name] = strings.Join(lines, "\n")
	}
	if got["Go"] != got["TS"] {
		t.Errorf("shell command lines differ:\nGo %s\nTS %s", got["Go"], got["TS"])
	}
	if want := `"xdg-open \"http://127.0.0.1:<PORT>\""`; got["TS"] != want {
		t.Errorf("TS ran %s, want %s", got["TS"], want)
	}
}
