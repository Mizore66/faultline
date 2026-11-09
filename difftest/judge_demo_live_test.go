//go:build faultline_test && unix

package difftest

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/oracle"
)

// startJudgeDemo starts one side's `judge-demo` in a fresh workspace and
// reads its stdout up to the launch line; url is the page URL.
func startJudgeDemo(t *testing.T, repo string, side judgeSide, env []string, args ...string) (cmd *exec.Cmd, url string, out *strings.Builder, rest *bufio.Reader, stderr *lockedBuffer) {
	cmd, url, out, rest, stderr = startJudgeCommand(t, repo, side, env, "judge-demo", "🚀 Launching FaultLine Judge Demo at ", args...)
	return cmd, strings.TrimSuffix(url, "..."), out, rest, stderr
}

// judgeDemoStopPrefix starts the initial-root stop summary both sides print.
const judgeDemoStopPrefix = "FaultLine judge-demo stopped. Bundle root: "

// TestLiveJudgeDemoSignalStop stops each side's judge-demo with SIGINT and
// SIGTERM and compares stdout, stderr and the process result with the
// frozen TypeScript (only the port masked).
func TestLiveJudgeDemoSignalStop(t *testing.T) {
	oracle.Skip(t)
	repo := repoRoot(t)
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			got := map[string]string{}
			for _, side := range judgeSides(repo) {
				cmd, _, out, rest, stderr := startJudgeDemo(t, repo, side, judgeEnv("FAULTLINE_NO_BROWSER=1"), "--port", "0")
				line, _ := rest.ReadString('\n')
				out.WriteString(line)
				time.Sleep(300 * time.Millisecond)
				if err := cmd.Process.Signal(sig); err != nil {
					t.Fatal(err)
				}
				tail, _ := io.ReadAll(rest)
				out.Write(tail)
				cmd.Wait()
				got[side.name] = loopbackPort.ReplaceAllString(out.String(), "127.0.0.1:<PORT>") + "--- stderr\n" + stderr.String() + "--- " + cmd.ProcessState.String()
			}
			if got["Go"] != got["TS"] {
				t.Errorf("Go\n%s\nTS\n%s", got["Go"], got["TS"])
			}
			if !strings.Contains(got["Go"], judgeDemoStopPrefix) || !strings.HasSuffix(got["Go"], "exit status 0") || strings.Contains(got["Go"], "ELIFECYCLE") {
				t.Errorf("not a clean stop:\n%s", got["Go"])
			}
		})
	}
}

// TestLiveJudgeDemoBrowserOpen runs judge-demo with the fake openers first
// on PATH, succeeding and failing, and compares output and what the opener
// was run with against the frozen TypeScript.
func TestLiveJudgeDemoBrowserOpen(t *testing.T) {
	oracle.Skip(t)
	repo := repoRoot(t)
	fake := filepath.Join(repo, "difftest", "testdata", "fakebrowser")
	for _, exit := range []string{"0", "1"} {
		t.Run("exit-"+exit, func(t *testing.T) {
			got := map[string]string{}
			for _, side := range judgeSides(repo) {
				log := filepath.Join(t.TempDir(), "browser.log")
				env := judgeEnv("PATH="+fake+string(os.PathListSeparator)+os.Getenv("PATH"), "FAULTLINE_BROWSER_LOG="+log, "FAULTLINE_BROWSER_EXIT="+exit)
				cmd, _, out, rest, stderr := startJudgeDemo(t, repo, side, env, "--port", "0")
				done := make(chan struct{})
				go func() {
					tail, _ := io.ReadAll(rest)
					out.Write(tail)
					close(done)
				}()
				deadline := time.Now().Add(60 * time.Second)
				for {
					_, err := os.Stat(log)
					if err == nil && (exit == "0" || strings.Contains(stderr.String(), "could not open a browser")) {
						break
					}
					if time.Now().After(deadline) {
						cmd.Process.Kill()
						t.Fatalf("%s: the opener did not run or report\n%s", side.name, stderr.String())
					}
					time.Sleep(20 * time.Millisecond)
				}
				time.Sleep(200 * time.Millisecond)
				cmd.Process.Signal(syscall.SIGTERM)
				<-done
				werr := cmd.Wait()
				logged, _ := os.ReadFile(log)
				got[side.name] = loopbackPort.ReplaceAllString("--- stdout\n"+out.String()+"--- stderr\n"+stderr.String()+"--- opener\n"+string(logged)+"--- "+cmd.ProcessState.String(), "127.0.0.1:<PORT>")
				if werr != nil {
					t.Errorf("%s: %v\n%s", side.name, werr, got[side.name])
				}
			}
			if got["Go"] != got["TS"] {
				t.Errorf("Go\n%s\nTS\n%s", got["Go"], got["TS"])
			}
			if (exit == "1") != strings.Contains(got["Go"], "could not open a browser") {
				t.Errorf("exit %s: unexpected browser failure report\n%s", exit, got["Go"])
			}
			if !strings.Contains(got["Go"], judgeDemoStopPrefix) || strings.Contains(got["Go"], "ELIFECYCLE") {
				t.Errorf("not a clean stop:\n%s", got["Go"])
			}
		})
	}
}
