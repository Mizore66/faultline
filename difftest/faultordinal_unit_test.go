//go:build faultline_test

package difftest

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestInjectedOrdinal pins the check that keeps an ordinal fault row on the
// operation it names (synthetic traces, not oracle output).
func TestInjectedOrdinal(t *testing.T) {
	ok := "1 close(18) = 0\n1 close(18) = 0\n1 close(18) = -1 EIO (Input/output error) (INJECTED)\n"
	reader := "1 close(4) = -1 EIO (Input/output error) (INJECTED)\n1 --- SIGABRT {si_signo=SIGABRT} ---\n"
	early := "1 close(18) = -1 EIO (Input/output error) (INJECTED)\n"
	for name, c := range map[string]struct {
		trace string
		want  bool
	}{
		"third close":       {ok, true},
		"first close":       {early, false},
		"reader abort":      {reader, false},
		"nothing failed":    {"1 close(18) = 0\n", false},
		"empty":             {"", false},
		"signal lines skip": {"1 --- SIGCHLD {} ---\n" + ok, true},
		"foreign tid closes before injected third": {"2 close(9) = 0\n2 close(9) = 0\n2 close(9) = 0\n1 close(18) = 0\n1 close(18) = 0\n1 close(18) = -1 EIO (Input/output error) (INJECTED)\n", true},
		"foreign tid interleaved after injection":  {"1 close(18) = 0\n2 close(9) = 0\n1 close(18) = 0\n1 close(18) = -1 EIO (Input/output error) (INJECTED)\n2 close(9) = 0\n", true},
		"unfinished resumed third":                 {"1 close(18) = 0\n1 close(18) = 0\n1 close(18 <unfinished ...>\n2 close(9) = 0\n1 <... close resumed>) = -1 EIO (Input/output error) (INJECTED)\n", true},
		"orphan resumed":                           {"1 <... close resumed>) = -1 EIO (Input/output error) (INJECTED)\n", false},
		"unterminated unfinished":                  {"1 close(18) = 0\n1 close(18) = 0\n1 close(18 <unfinished ...>\n", false},
		"first close then abort":                   {"1 close(18) = -1 EIO (Input/output error) (INJECTED)\n1 --- SIGABRT {si_signo=SIGABRT} ---\n", false},
		"abort after third":                        {ok + "1 --- SIGABRT {si_signo=SIGABRT} ---\n", false},
		"garbage line":                             {"garbage\n" + ok, false},
	} {
		if got := injectedOrdinal(c.trace, 3); got != c.want {
			t.Errorf("%s: injectedOrdinal = %v, want %v", name, got, c.want)
		}
	}
	if o := faultOrdinals["turns-proof/turn-binding/close"]; o.ts != 3 || o.goSide != 3 || !o.onlyCalls {
		t.Errorf("binding close ordinal wiring changed: %+v", o)
	}
	if o := faultOrdinals["turns-proof/turn-pack-verify-tmp/mkdir"]; !o.from || o.ts != 19 {
		t.Errorf("verifier mkdir wiring changed: %+v", o)
	}
}

// TestWriterFaultThreadAffinity pins the test-only thread-lock hook: the CLI
// links it only under faultline_test on linux, and only the Go side of an
// onlyCalls row exports its switch.
func TestWriterFaultThreadAffinity(t *testing.T) {
	b, err := os.ReadFile("../cmd/fl/thread_affinity_faultline_linux.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if !strings.HasPrefix(src, "//go:build faultline_test && linux\n") {
		t.Errorf("hook is not restricted to faultline_test && linux:\n%s", src)
	}
	for _, want := range []string{"FAULTLINE_TEST_LOCK_OS_THREAD", "runtime.LockOSThread()"} {
		if !strings.Contains(src, want) {
			t.Errorf("hook lacks %q", want)
		}
	}
	main, err := os.ReadFile("../cmd/fl/main.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(main), "FAULTLINE_TEST_LOCK_OS_THREAD") {
		t.Error("untagged CLI source references the hook")
	}
	const hook = "thread_affinity_faultline_linux.go"
	for _, c := range []struct {
		goos, tags string
		want       bool
	}{
		{"linux", "faultline_test", true},
		{"linux", "", false},
		{"darwin", "faultline_test", false},
	} {
		args := []string{"list"}
		if c.tags != "" {
			args = append(args, "-tags", c.tags)
		}
		args = append(args, "-f", "{{.GoFiles}}", "./cmd/fl")
		cmd := exec.Command("go", args...)
		cmd.Dir = ".."
		cmd.Env = append(os.Environ(), "GOOS="+c.goos, "CGO_ENABLED=0")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go %v (GOOS=%s): %v\n%s", args, c.goos, err, out)
		}
		if got := strings.Contains(string(out), hook); got != c.want {
			t.Errorf("GOOS=%s tags=%q: hook in GoFiles = %v, want %v\n%s", c.goos, c.tags, got, c.want, out)
		}
	}
	for _, c := range []struct {
		tsSide, onlyCalls bool
		want              string
	}{
		{false, true, "export FAULTLINE_TEST_LOCK_OS_THREAD=1; "},
		{true, true, ""},
		{false, false, ""},
		{true, false, ""},
	} {
		if got := threadAffinityPrefix(c.tsSide, c.onlyCalls); got != c.want {
			t.Errorf("threadAffinityPrefix(ts=%v, onlyCalls=%v) = %q, want %q", c.tsSide, c.onlyCalls, got, c.want)
		}
	}
}
