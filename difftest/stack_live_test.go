package difftest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
)

// The V8 stack model (internal/jsjson/stack.go) is fitted per platform and
// call site to the Node release CI pins. Bisect the deepest value whose zod
// message still prints, per site and shape, through `node dist/cli.js
// verify` and through fl, and require the same depth. A failure prints the
// measured TS depths, which is what refitting needs.
func TestLiveStackThresholdsMatchNode(t *testing.T) {
	oracle.Start(t).Close() // skip unless FAULTLINE_NODE_ORACLE=1
	repo, _ := filepath.Abs("..")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	sites := []struct{ name, base, file, old string }{
		{"demo", "demo-replay", "analysis.json", `"schemaVersion": "faultline.demo.v1"`},
		{"witness", "demo-replay", "witness/witness.json", `"network": "disabled"`},
		{"prevention", "prevention-verified", "prevention.json", `"schemaVersion":"faultline.prevention-proof.v1"`},
		{"git", "git-fully-bound", "manifest.json", `"integrityScope":"complete-declared-file-set"`},
		{"ledger", "git-fully-bound", "lifecycle/ledger.json", `"schemaVersion":"faultline.codex-lifecycle-ledger.v1"`},
	}
	shapes := map[string]func(n int) string{
		"arr": func(n int) string { return strings.Repeat("[", n) + strings.Repeat("]", n) },
		"obj": func(n int) string { return strings.Repeat(`{"a":`, n) + "0" + strings.Repeat("}", n) },
		"idx": func(n int) string { return strings.Repeat(`{"0":`, n) + "0" + strings.Repeat("}", n) },
		"alt": func(n int) string {
			var open, closing strings.Builder
			for i := range n {
				if i%2 == 1 {
					open.WriteString(`{"a":`)
				} else {
					open.WriteString("[")
				}
			}
			for i := n - 1; i >= 0; i-- {
				if i%2 == 1 {
					closing.WriteString("}")
				} else {
					closing.WriteString("]")
				}
			}
			return open.String() + "0" + closing.String()
		},
	}
	var report []string
	for _, site := range sites {
		for _, shape := range []string{"arr", "obj", "idx", "alt"} {
			prints := func(argv []string, n int) bool {
				dir := filepath.Join(t.TempDir(), "b")
				copyTree(t, filepath.Join("testdata", "bases", site.base), dir)
				path := filepath.Join(dir, filepath.FromSlash(site.file))
				b, err := os.ReadFile(path)
				if err != nil || !strings.Contains(string(b), site.old) {
					t.Fatalf("%s: %s lacks %s", site.base, site.file, site.old)
				}
				key, _, _ := strings.Cut(site.old, ":")
				os.WriteFile(path, []byte(strings.Replace(string(b), site.old, key+":"+shapes[shape](n), 1)), 0o644)
				out, _ := exec.Command(argv[0], append(argv[1:], "verify", dir)...).Output()
				if !strings.Contains(string(out), "self-consistency") {
					t.Fatalf("%s %s depth %d: unexpected output %.300q", site.name, shape, n, out)
				}
				return !strings.Contains(string(out), "Maximum call stack size exceeded")
			}
			bisect := func(argv ...string) int {
				lo, hi := 500, 12000
				for hi-lo > 1 {
					m := (lo + hi) / 2
					if prints(argv, m) {
						lo = m
					} else {
						hi = m
					}
				}
				return lo
			}
			var ts, goLast int
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); ts = bisect(node, filepath.Join(repo, "dist", "cli.js")) }()
			go func() { defer wg.Done(); goLast = bisect(flBinary) }()
			wg.Wait()
			line := fmt.Sprintf("%s/%s %-10s %s: TS %d, Go %d", runtime.GOOS, runtime.GOARCH, site.name, shape, ts, goLast)
			t.Log(line)
			if ts != goLast {
				report = append(report, line)
			}
		}
	}
	if len(report) > 0 {
		t.Errorf("stack thresholds differ from Node:\n%s", strings.Join(report, "\n"))
	}
}
