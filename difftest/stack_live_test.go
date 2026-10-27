//go:build faultline_test

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
	"github.com/Mizore66/faultline/internal/jsjson"
)

// unstableTolerance is how far apart, in nesting levels, TS's and Go's
// last printing depths may be at an unstable site of
// TestLiveStackThresholdsMatchNode: twice the largest gap measured there
// (241, darwin/arm64), far below the bisection's 12,000-level ceiling.
const unstableTolerance = 512

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
	// A base under testdata/bases is verified; the cli site is a writer,
	// `witness propose --input` on the git-two-states scenario's proposal,
	// whose zod message formatCliFailure prints (past the limit TS dies of
	// the uncaught RangeError and Go prints it as a FaultLine error).
	//
	// Slice 3a adds two writer sites, run in a copy of a writer scenario
	// (content is the file written there): a project config whose
	// schemaVersion literal fails, read by `fl init` and printed by
	// formatCliFailure (src/config.ts:62), and a secret allowlist read by
	// `fl init --yes`'s pre-warm snapshot, whose message
	// captureTurnTreeSnapshot reads in its catch (src/turn-snapshot.ts:1171)
	// and init reports as snapshotPrewarm.reason (TS overflowing there
	// reports "Maximum call stack size exceeded" the same way). Both are
	// unstable: init reads them after awaiting child processes, and on Node
	// 22.22.2 the same depth prints in one run and overflows in the next.
	// TS's bisected depth lies up to 63 levels below Go's on linux/arm64
	// and up to 241 on darwin/arm64 (three runs each), and moves from run to
	// run, which Go cannot reproduce (KNOWN_DIFFERENCES.md). There Go must
	// stay within unstableTolerance levels of TS's bisected depth,
	// which catches a writer that prints at any depth or overflows far too
	// early. Go reads the config message at SiteCLI and the allowlist one at
	// the default site; both are within a few levels of the band.
	sites := []struct {
		name, base, file, old string
		scenario, content     string
		argv                  []string
		unstable              bool
	}{
		// cli first: its darwin/arm64 offset is the one still estimated, so
		// it is measured (and logged) before any other site can fail.
		{name: "cli", base: "scenarios/git-two-states/tree", file: "proposal.json", old: `"network": "disabled"`,
			argv: []string{"witness", "propose", "--input", "proposal.json"}},
		{name: "config", scenario: "init-fresh", file: ".faultline/config.json", old: `"schemaVersion":"faultline.config.v1"`,
			content: `{"schemaVersion":"faultline.config.v1","snapshot":{"trackedFilesOnly":false},"stores":{"incidents":"i","witnesses":"w","recordings":"r"}}`,
			argv:    []string{"init"}, unstable: true},
		{name: "allowlist", scenario: "init-fresh", file: ".faultline-secret-allowlist.json", old: `"schemaVersion":"faultline.secret-allowlist.v1"`,
			content: `{"schemaVersion":"faultline.secret-allowlist.v1","entries":[]}`,
			argv:    []string{"init", "--yes"}, unstable: true},
		// repair: `fl repair verify` of a package the repair-package scenario
		// holds (Task 18). VerifyBriefArtifact reads its zod messages at
		// SiteGitProof, measured equal to the git row on darwin/arm64 and
		// linux/arm64 (Task 9); linux/amd64 is first measured by this site in CI.
		{name: "repair", scenario: "repair-package", file: ".faultline/repair-briefs/pkg/manifest.json", old: `"schemaVersion":"faultline.repair-brief-artifact.v1"`,
			content: `{"schemaVersion":"faultline.repair-brief-artifact.v1"}`,
			argv:    []string{"repair", "verify", ".faultline/repair-briefs/pkg"}},
		{name: "demo", base: "demo-replay", file: "analysis.json", old: `"schemaVersion": "faultline.demo.v1"`},
		{name: "witness", base: "demo-replay", file: "witness/witness.json", old: `"network": "disabled"`},
		{name: "prevention", base: "prevention-verified", file: "prevention.json", old: `"schemaVersion":"faultline.prevention-proof.v1"`},
		{name: "git", base: "git-fully-bound", file: "manifest.json", old: `"integrityScope":"complete-declared-file-set"`},
		{name: "ledger", base: "git-fully-bound", file: "lifecycle/ledger.json", old: `"schemaVersion":"faultline.codex-lifecycle-ledger.v1"`},
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
		var tsLasts []int
		var shapeFns []func(int) string
		siteDiffers := false
		for _, shape := range []string{"arr", "obj", "idx", "alt"} {
			prints := func(argv []string, n int) bool {
				dir := filepath.Join(t.TempDir(), "b")
				switch {
				case site.scenario != "":
					copyScenario(t, repo, site.scenario, dir)
					if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, filepath.FromSlash(site.file))), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(site.file)), []byte(site.content), 0o644); err != nil {
						t.Fatal(err)
					}
				case site.argv != nil:
					if err := os.MkdirAll(dir, 0o755); err != nil {
						t.Fatal(err)
					}
					b, err := os.ReadFile(filepath.Join("testdata", site.base, site.file))
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(dir, site.file), b, 0o644); err != nil {
						t.Fatal(err)
					}
				default:
					copyTree(t, filepath.Join("testdata", "bases", site.base), dir)
				}
				path := filepath.Join(dir, filepath.FromSlash(site.file))
				b, err := os.ReadFile(path)
				if err != nil || !strings.Contains(string(b), site.old) {
					t.Fatalf("%s: %s lacks %s", site.base, site.file, site.old)
				}
				key, _, _ := strings.Cut(site.old, ":")
				os.WriteFile(path, []byte(strings.Replace(string(b), site.old, key+":"+shapes[shape](n), 1)), 0o644)
				var out []byte
				var expected bool
				if site.argv != nil {
					// The zod message (on stderr, or JSON-quoted in init's
					// snapshotPrewarm.reason, where its quotes are escaped),
					// or the overflow (TS's uncaught RangeError report, Go's
					// FaultLine error line, or the reason).
					cmd := exec.Command(argv[0], append(argv[1:], site.argv...)...)
					cmd.Dir = dir
					out, _ = cmd.CombinedOutput()
					expected = strings.Contains(string(out), `"received"`) || strings.Contains(string(out), `\"received\"`) ||
						strings.Contains(string(out), "Maximum call stack size exceeded")
				} else {
					out, _ = exec.Command(argv[0], append(argv[1:], "verify", dir)...).Output()
					expected = strings.Contains(string(out), "self-consistency")
				}
				if !expected {
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
			tsLasts, shapeFns = append(tsLasts, ts), append(shapeFns, shapes[shape])
			if site.unstable {
				if goLast < ts-unstableTolerance || goLast > ts+unstableTolerance {
					report = append(report, line+fmt.Sprintf(" (unstable site: more than %d levels apart)", unstableTolerance))
				}
				continue
			}
			if ts != goLast {
				report = append(report, line)
				siteDiffers = true
			}
		}
		// The cli offset is the one still estimated on some platforms: when
		// Go misses TS there, report the offsets that would reproduce TS's
		// depths (internal/jsjson/stack.go SiteCLI).
		if site.argv != nil && siteDiffers {
			if lo, hi, ok := jsjson.FitSiteOffsetForTest(shapeFns, tsLasts); ok {
				t.Logf("%s/%s %s refit: site offsets %d to %d reproduce TS", runtime.GOOS, runtime.GOARCH, site.name, lo, hi)
			} else {
				t.Logf("%s/%s %s refit: no site offset reproduces TS on all four shapes", runtime.GOOS, runtime.GOARCH, site.name)
			}
		}
	}
	if len(report) > 0 {
		t.Errorf("stack thresholds differ from Node:\n%s", strings.Join(report, "\n"))
	}
}
