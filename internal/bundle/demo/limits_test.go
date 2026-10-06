package demo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// demoWithHashes copies demo-replay and replaces hashes.txt with n lines
// that are not hash entries.
func demoWithHashes(t *testing.T, n int) string {
	t.Helper()
	src := filepath.Join("..", "..", "..", "difftest", "testdata", "bases", "demo-replay")
	dir := filepath.Join(t.TempDir(), "b")
	if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hashes.txt"), []byte(strings.Repeat("x\n", n)), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The V8 limits are reached only by huge hashes.txt files, so the call
// sites are checked with lowered limits (round 6: removing either call
// passed every test).
//
// .filter(Boolean) builds the kept lines one push at a time and throws
// inside the verifier's try at the limit; errors (one per invalid line plus
// the ROOT mismatch) then reaches it at the last line, and the catch
// block's own push throws too, so the RangeError leaves the verifier.
func TestGrownArrayLimitCallSites(t *testing.T) {
	// Above the ~150 other errors a demo bundle with no valid hash lines
	// gives (one per missing or undeclared file).
	const limit = 400
	defer jsjson.SetGrownLimitForTest(limit)()
	verify := func(lines int) (thrown error) {
		defer func() {
			if p := recover(); p != nil {
				thrown = p.(jsexc.Thrown).Err
			}
		}()
		Verify(demoWithHashes(t, lines), "", false)
		return nil
	}
	// limit lines: the filter throws, caught as failed safely.
	res := Verify(demoWithHashes(t, limit), "", false)
	if last := res.Errors[len(res.Errors)-1]; last != "bundle verification failed safely: Invalid array length" || len(res.Errors) != 2 {
		t.Fatalf("filter at the limit: %q", res.Errors)
	}
	// limit-1 lines: the filter passes; 1 + (limit-1) errors reach the
	// limit and the throw escapes.
	if err := verify(limit - 1); err != jsjson.ErrInvalidArrayLength {
		t.Fatalf("errors at the limit: thrown %v, want Invalid array length to escape", err)
	}
	// 10 lines: errors stay below the limit, no throw.
	res = Verify(demoWithHashes(t, 10), "", false)
	if len(res.Errors) < 20 || strings.Contains(strings.Join(res.Errors, "\n"), "Invalid array length") {
		t.Fatalf("below the limit: %d errors, %q", len(res.Errors), res.Errors[len(res.Errors)-1])
	}
}

// String.prototype.split aborts the process at the FixedArray limit, before
// any line is looked at; run that in a child process (a signal death on
// Unix, exit status 0x80000003 on Windows).
func TestSplitLimitCallSite(t *testing.T) {
	if os.Getenv("FL_SPLIT_CHILD") != "" {
		jsjson.SetSplitLimitForTest(10)
		Verify(demoWithHashes(t, 10), "", false)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSplitLimitCallSite$")
	cmd.Env = append(os.Environ(), "FL_SPLIT_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "# Fatal JavaScript invalid size error 10\n") {
		t.Fatalf("want the V8 abort for 10 parts, got %v:\n%s", err, out)
	}
}
