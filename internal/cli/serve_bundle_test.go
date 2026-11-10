package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/bundle/demo"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// Direct Run tests for `fl serve --bundle` argument precedence. Every case
// names a bundle directory that does not exist: any expected refusal below
// must therefore come from parsing alone, before a single byte of I/O, and a
// handler that loaded the bundle first would report a different error.

const (
	bundleNext          = "Next: pnpm fl help\n"
	minimizationPairErr = "A shareable minimization attachment requires both --minimization <result.json> and --expect-minimization <retained-digest>."
	repairPairErr       = "A shareable repair attachment requires both --repair <repair-brief-directory> and --expect-repair <retained-artifact-digest>."
	preventionPairErr   = "A shareable prevention attachment requires both --prevention <prevention-proof-directory> and --expect-prevention <retained-root-digest>."
)

func requireServeBundleFailure(t *testing.T, message string, args ...string) {
	t.Helper()
	stdout, stderr, code := run(args...)
	want := "FaultLine error: " + message + "\n" + bundleNext
	if code != 1 || stdout != "" || stderr != want {
		t.Errorf("%q: exit=%d stdout=%q stderr=%q, want exit 1, empty stdout, stderr %q", args, code, stdout, stderr, want)
	}
}

// Catches a handler that validates pairs before reading every value flag (the
// source reads all six values first, so a trailing valueless flag wins).
func TestServeBundleParsesAllValuesBeforePairs(t *testing.T) {
	requireServeBundleFailure(t, "Missing required option: --expect-repair",
		"serve", "--bundle", "missing", "--minimization", "result.json", "--expect-repair")
}

// Catches a handler that lets a later valueless flag be hidden by an earlier
// complete-looking pair, or reads flags in a different order than the source.
func TestServeBundleValueOrderBeforePairs(t *testing.T) {
	requireServeBundleFailure(t, "Missing required option: --repair",
		"serve", "--bundle", "missing", "--minimization", "m.json", "--repair")
	requireServeBundleFailure(t, "Missing required option: --expect-minimization",
		"serve", "--bundle", "missing", "--expect-minimization")
	requireServeBundleFailure(t, "Missing required option: --prevention",
		"serve", "--bundle", "missing", "--prevention")
}

// Catches a missing --bundle value being treated as the not-ported fallback or
// as a bundle named by the next flag.
func TestServeBundleRequiresBundleValue(t *testing.T) {
	requireServeBundleFailure(t, "Missing required option: --bundle", "serve", "--bundle")
	requireServeBundleFailure(t, "Missing required option: --bundle", "serve", "--bundle", "--port", "0")
}

// Catches a missing, swapped or mis-ordered pair check, and a check that runs
// after bundle I/O (the bundle does not exist).
func TestServeBundlePairFailures(t *testing.T) {
	for _, c := range []struct {
		name    string
		message string
		args    []string
	}{
		{"minimization path only", minimizationPairErr, []string{"--minimization", "m.json"}},
		{"minimization digest only", minimizationPairErr, []string{"--expect-minimization", "sha256:x"}},
		{"repair path only", repairPairErr, []string{"--repair", "r"}},
		{"repair digest only", repairPairErr, []string{"--expect-repair", "sha256:x"}},
		{"prevention path only", preventionPairErr, []string{"--prevention", "p"}},
		{"prevention digest only", preventionPairErr, []string{"--expect-prevention", "sha256:x"}},
		// First incomplete pair in minimization, repair, prevention order wins.
		{"minimization before repair", minimizationPairErr, []string{"--minimization", "m.json", "--repair", "r"}},
		{"repair before prevention", repairPairErr, []string{"--minimization", "m.json", "--expect-minimization", "sha256:x", "--repair", "r", "--prevention", "p"}},
		{"prevention last", preventionPairErr, []string{"--minimization", "m.json", "--expect-minimization", "sha256:x", "--repair", "r", "--expect-repair", "sha256:y", "--prevention", "p"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			requireServeBundleFailure(t, c.message, append([]string{"serve", "--bundle", "missing"}, c.args...)...)
		})
	}
}

// Catches the dispatcher claiming plain serve or the unsupported equals form
// (hasFlag is exact), which must keep the prior explicit failure.
func TestServeBundleOnlyExactFlagSelectsHandler(t *testing.T) {
	// Bare serve, the unsupported equals form and a lone --port all select the
	// plain handler (hasFlag is exact), which would otherwise wait for SIGINT.
	// Drive each through a controlled idle start/close and require the exact
	// startup and no writes.
	for _, args := range [][]string{{"serve"}, {"serve", "--bundle=missing"}, {"serve", "--port", "0"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Chdir(t.TempDir())
			installPlainServeFake(t)
			oWrite := plainServeWriteBundle
			plainServeWriteBundle = func(string, jsjson.Value, demo.WriteOptions) (demo.Written, error) {
				t.Error("startup must not write a bundle")
				return demo.Written{}, nil
			}
			t.Cleanup(func() { plainServeWriteBundle = oWrite })
			stdout, stderr, code := run(args...)
			if code != 0 || stderr != "" || stdout != plainServeStartup {
				t.Errorf("%q: exit=%d stdout=%q stderr=%q", args, code, stdout, stderr)
			}
			for _, path := range []string{
				".faultline/bundles/judge-demo",
				".faultline/bundles/judge-demo.native-execution.json",
			} {
				if _, err := os.Lstat(path); err == nil {
					t.Errorf("startup wrote %s", path)
				}
			}
		})
	}
}
