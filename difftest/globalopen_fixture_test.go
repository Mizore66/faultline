//go:build faultline_test && linux

package difftest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Native checks of the tracked, DEFAULT-built globalopen helper
// (difftest/testdata/globalopen) against its adversarial fixture and REAL
// verifier (difftest/testdata/globalopen-fixture). They need no oracle and no
// Node, are test-only, and run on Linux. The helper and fixture come from the
// shared resolver (globalopen_resolver_test.go): an explicit absolute
// FAULTLINE_GLOBALOPEN_BINARY / FAULTLINE_GLOBALOPEN_FIXTURE_BINARY, or a
// once-per-process default build. A missing tool, a failed build, an unhealthy
// trace or a verifier FAIL is a loud failure; there is no skip and no fallback
// to strace. The fixture's thread locking is its own adversarial setup, not
// FaultLine thread pinning; the helper's own tracer thread affinity is required
// ptrace ownership, not tracee alignment.

func requireGlobalOpenSources(t *testing.T, repo string) {
	t.Helper()
	for _, d := range []string{"globalopen", "globalopen-fixture"} {
		p := filepath.Join(repo, "difftest", "testdata", d)
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			t.Fatalf("tracked test-only helper adoption is missing: %s is not a directory (%v)", p, err)
		}
	}
}

// globalOpenExec runs a command and returns its stdout, stderr and exit code.
// Only a failure to start it (or a timeout) is fatal here; the caller judges
// the code.
func globalOpenExec(t *testing.T, dir, name string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return so.String(), se.String(), 0
	case errors.As(err, &ee):
		return so.String(), se.String(), ee.ExitCode()
	default:
		t.Fatalf("%s %v: %v", name, args, err)
		return "", "", -1
	}
}

// globalOpenScenario is one default-helper native scenario: the fixture scenario, the
// helper's target path template, the traced fixture's extra flags and the
// helper's expected exit status (the traced fixture's own).
type globalOpenScenario struct {
	label, scenario, target string
	extra                   []string
	status                  int
}

func globalOpenScenarios(errno string) []globalOpenScenario {
	var out []globalOpenScenario
	for _, s := range []string{"family", "pid", "create", "trunc", "child", "alias", "marker"} {
		target := "target"
		switch s {
		case "pid":
			target = "f.<PID>"
		case "create":
			target = "fresh"
		case "marker":
			target = "m(INJECTED)"
		}
		out = append(out, globalOpenScenario{s + "-" + errno, s, target, nil, 0})
	}
	out = append(out,
		globalOpenScenario{"exit-" + errno, "family", "target", []string{"-exit", "3"}, 3},
		globalOpenScenario{"signal-" + errno, "family", "target", []string{"-die", "15"}, 128 + 15},
		// A header-marker control: the family scenario (several threads, five
		// open forms) inside a directory whose own name contains the literal
		// injected marker, so the HEADER cmd= field, TARGET and every ENTRY
		// path carry it; the log must still hold exactly one raw (INJECTED).
		// The directory itself is created by runGlobalOpenScenario.
		globalOpenScenario{"header-marker-" + errno, "family", "target", nil, 0},
	)
	return out
}

// globalOpenMarkerDir is the directory name (under the scenario's temp root)
// that carries the literal injected marker for the header-marker control.
const globalOpenMarkerDir = "(INJECTED)-marker"

// retainGlobalOpenFixtureEvidence copies the scenario's existing raw trace.log
// and child.out, byte for byte, into a fresh exclusive subdirectory of
// FAULTLINE_GLOBALOPEN_EVIDENCE_DIR (when set), with quoted identifying
// metadata. It runs as a cleanup registered AFTER t.TempDir, i.e. before the
// temp directory is removed, so a failing scenario keeps its evidence too. A
// file that was never created (the helper never started) is skipped; any real
// stat/read/create/write/close error is fatal. Nothing is overwritten and no
// symlink is followed (non-regular files are refused).
func retainGlobalOpenFixtureEvidence(t *testing.T, sc globalOpenScenario, errno, d string) {
	t.Helper()
	root := os.Getenv("FAULTLINE_GLOBALOPEN_EVIDENCE_DIR")
	if root == "" {
		return
	}
	sub, err := os.MkdirTemp(root, "fixture-"+sc.label+"-*")
	if err != nil {
		t.Fatalf("creating the fixture evidence directory in %s: %v", root, err)
	}
	var copied []string
	for _, name := range []string{"trace.log", "child.out"} {
		src := filepath.Join(d, name)
		fi, err := os.Lstat(src)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("evidence stat %s: %v", src, err)
		}
		if !fi.Mode().IsRegular() {
			t.Fatalf("evidence source %s is not a regular file (%v): refused", src, fi.Mode())
		}
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("evidence read %s: %v", src, err)
		}
		writeExclusive(t, filepath.Join(sub, name), b)
		copied = append(copied, name)
	}
	meta := fmt.Sprintf("test=%q\nlabel=%q\nscenario=%q\nerrno=%q\ndir=%q\nfiles=%q\n", t.Name(), sc.label, sc.scenario, errno, d, strings.Join(copied, ","))
	writeExclusive(t, filepath.Join(sub, "meta"), []byte(meta))
}

// writeExclusive creates path (never overwriting) with b; every error is fatal.
func writeExclusive(t *testing.T, path string, b []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("evidence create %s: %v", path, err)
	}
	if n, err := f.Write(b); err != nil || n != len(b) {
		f.Close()
		t.Fatalf("evidence write %s: n=%d/%d err=%v", path, n, len(b), err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("evidence close %s: %v", path, err)
	}
}

// runGlobalOpenScenario runs prepare, the helper over the fixture, and the
// REAL fixture verifier; it returns the raw log, the run output and the dir.
// The raw trace and child output are retained (when configured) even if a later
// assertion fails.
func runGlobalOpenScenario(t *testing.T, helper, fixture, errno string, sc globalOpenScenario) (log, out, dir string) {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(sc.label, "header-marker-") {
		d = filepath.Join(d, globalOpenMarkerDir)
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// Registered after t.TempDir, so it runs before the directory is removed.
	t.Cleanup(func() { retainGlobalOpenFixtureEvidence(t, sc, errno, d) })
	if so, se, code := globalOpenExec(t, "", fixture, "prepare", "-dir", d); code != 0 {
		t.Fatalf("fixture prepare exited %d:\n%s%s", code, so, se)
	}
	logPath, outPath := filepath.Join(d, "trace.log"), filepath.Join(d, "child.out")
	target := filepath.Join(d, sc.target)
	run := append([]string{"-errno", errno, "-path", target, "-log", logPath, "--", fixture, "run", "-scenario", sc.scenario, "-dir", d}, sc.extra...)
	so, se, code := globalOpenExec(t, d, helper, run...)
	// The actual helper stdout is written BEFORE any status/stderr assertion, so
	// a failing run still retains it.
	if err := os.WriteFile(outPath, []byte(so), 0o600); err != nil {
		t.Fatal(err)
	}
	if code != sc.status {
		t.Fatalf("%s: helper exit %d, want the traced status %d (70 is a tool failure):\n%s%s", sc.label, code, sc.status, so, se)
	}
	if se != "" {
		t.Fatalf("%s: unexpected stderr (a deadlock message or tool error is a failure): %q", sc.label, se)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("%s: no GLOBALOPEN log: %v", sc.label, err)
	}
	verify := append([]string{"verify", "-scenario", sc.scenario, "-dir", d, "-log", logPath, "-out", outPath, "-errno", errno}, sc.extra...)
	v, ve, vcode := globalOpenExec(t, d, fixture, verify...)
	if vcode != 0 || ve != "" || !strings.HasSuffix(strings.TrimSpace(v), "PASS") {
		t.Fatalf("%s: the fixture verifier did not PASS (exit %d, stderr %q):\n%s", sc.label, vcode, ve, v)
	}
	if n := strings.Count(string(raw), "(INJECTED)"); n != 1 {
		t.Fatalf("%s: raw (INJECTED) count %d, want exactly 1", sc.label, n)
	}
	for _, want := range []string{" exec_retired=0 ", " exec_refused=0\n"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("%s: HEALTH lacks %q:\n%s", sc.label, want, raw)
		}
	}
	return string(raw), so, d
}

// TestGlobalOpenDefaultHelperScenarios runs 3 errnos x family, pid, create,
// trunc, child, alias, marker, exit3, signal15 and a header-marker control
// (30 cases) under the default helper and requires the real verifier to PASS.
// Before the tracked adoption this fails on the missing sources.
func TestGlobalOpenDefaultHelperScenarios(t *testing.T) {
	repo := repoRoot(t)
	requireGlobalOpenSources(t, repo)
	helper, fixture := globalOpenBinary(t, repo), globalOpenFixtureBinary(t, repo)
	n := 0
	for _, errno := range []string{"ENOSPC", "EIO", "EACCES"} {
		for _, sc := range globalOpenScenarios(errno) {
			n++
			t.Run(sc.label, func(t *testing.T) {
				log, _, _ := runGlobalOpenScenario(t, helper, fixture, errno, sc)
				if sc.scenario == "alias" && !strings.Contains(log, "GLOBALOPEN HEALTH ok=false") {
					t.Errorf("alias must report HEALTH ok=false (the refused symlink open):\n%s", log)
				}
				if sc.scenario != "alias" && !strings.Contains(log, "GLOBALOPEN HEALTH ok=true ") {
					t.Errorf("HEALTH is not ok=true:\n%s", log)
				}
			})
		}
	}
	if n != 30 {
		t.Fatalf("%d default scenarios, want 30", n)
	}
}

// TestGlobalOpenHeaderMarkerScenario: the header-marker control of each errno
// must be a case DISTINCT from the plain marker case. The historical control
// ran the family scenario (several threads, five open forms) inside a
// directory whose own name contains the literal injected marker, so that the
// HEADER cmd= field, TARGET and every ENTRY path carry it. A table item that
// repeats the marker scenario with the same m(INJECTED) target duplicates
// marker-<errno> and leaves the directory-component escape untested.
func TestGlobalOpenHeaderMarkerScenario(t *testing.T) {
	for _, errno := range []string{"ENOSPC", "EIO", "EACCES"} {
		var found []globalOpenScenario
		for _, sc := range globalOpenScenarios(errno) {
			if sc.label == "header-marker-"+errno {
				found = append(found, sc)
			}
		}
		if len(found) != 1 {
			t.Fatalf("%s: %d header-marker items in the table, want exactly 1", errno, len(found))
		}
		if sc := found[0]; sc.scenario != "family" || sc.target != "target" {
			t.Errorf("%s: the header-marker item runs scenario %q with target %q; want the family scenario with target %q (it must not duplicate the marker scenario's m(INJECTED) target)", errno, sc.scenario, sc.target, "target")
		}
	}
}

// TestGlobalOpenFixtureEvidenceRetention: with FAULTLINE_GLOBALOPEN_EVIDENCE_DIR
// set to a test-owned directory, a REAL default-helper family/ENOSPC scenario
// (existing resolver, real helper, real fixture, real verifier) must leave its
// raw helper log and child stdout in that directory, byte for byte, even after
// the scenario's own temp directory has been cleaned up. The comparison runs
// after the nested subtest returns, i.e. after its cleanups. No mocked verifier
// and no fabricated trace. No parallel Setenv: this test and its subtest are
// sequential.
func TestGlobalOpenFixtureEvidenceRetention(t *testing.T) {
	repo := repoRoot(t)
	requireGlobalOpenSources(t, repo)
	helper, fixture := globalOpenBinary(t, repo), globalOpenFixtureBinary(t, repo)
	evidence := t.TempDir()
	t.Setenv("FAULTLINE_GLOBALOPEN_EVIDENCE_DIR", evidence)
	var log, out, dir string
	ok := t.Run("scenario", func(t *testing.T) {
		sc := globalOpenScenario{"family-ENOSPC", "family", "target", nil, 0}
		log, out, dir = runGlobalOpenScenario(t, helper, fixture, "ENOSPC", sc)
	})
	if !ok {
		t.Fatalf("INVALID PROBE: the real family/ENOSPC scenario itself failed")
	}
	if log == "" || out == "" || dir == "" {
		t.Fatalf("INVALID PROBE: the scenario returned no raw log/stdout/dir")
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatalf("INVALID PROBE: the scenario's temp dir %s still exists, so cleanup had not run", dir)
	}
	var traces, outs []string
	err := filepath.WalkDir(evidence, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			switch d.Name() {
			case "trace.log":
				traces = append(traces, p)
			case "child.out":
				outs = append(outs, p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the evidence dir: %v", err)
	}
	if len(traces) != 1 || len(outs) != 1 {
		t.Fatalf("evidence dir %s holds %d trace.log and %d child.out files, want exactly 1 and 1: the default fixture scenario retained nothing", evidence, len(traces), len(outs))
	}
	for _, c := range []struct{ name, path, want string }{{"trace.log", traces[0], log}, {"child.out", outs[0], out}} {
		got, err := os.ReadFile(c.path)
		if err != nil {
			t.Fatalf("reading retained %s: %v", c.name, err)
		}
		if string(got) != c.want {
			t.Errorf("retained %s (%s) differs from the raw bytes the scenario returned (%d vs %d bytes)", c.name, c.path, len(got), len(c.want))
		}
	}
}

// A failing evidence descriptor is a tool error (exit 70) before the traced
// command runs: real OS behavior of /dev/full, not simulated.
func TestGlobalOpenDevFullIsToolFailure(t *testing.T) {
	repo := repoRoot(t)
	requireGlobalOpenSources(t, repo)
	helper := globalOpenBinary(t, repo)
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Fatalf("/dev/full is required for this control and must not be skipped: %v", err)
	}
	so, se, code := globalOpenExec(t, "", helper, "-errno", "ENOSPC", "-path", filepath.Join(t.TempDir(), "no-target"), "-log", "/dev/full", "--", "/bin/true")
	if code != 70 {
		t.Fatalf("helper exit %d with an unwritable log, want 70:\n%s%s", code, so, se)
	}
	if !strings.Contains(se, "log write failed") {
		t.Fatalf("stderr lacks the loud log-write failure:\n%s", se)
	}
}

// The nine corruption recipes of the fixture's real verifier. Each takes a
// real, passing trace and corrupts exactly one thing; the verifier must reject
// it with a FAIL line and exit 1.
func TestGlobalOpenVerifierRejectsCorruptions(t *testing.T) {
	repo := repoRoot(t)
	requireGlobalOpenSources(t, repo)
	helper, fixture := globalOpenBinary(t, repo), globalOpenFixtureBinary(t, repo)
	type control struct {
		name, scenario string
		trace, out     func(string) string
	}
	id := func(s string) string { return s }
	reorder := func(s string) string {
		lines := strings.SplitAfter(s, "\n")
		var idx []int
		for i, l := range lines {
			if strings.HasPrefix(l, "GLOBALOPEN ENTRY ") {
				idx = append(idx, i)
			}
		}
		if len(idx) < 2 {
			return s
		}
		lines[idx[0]], lines[idx[1]] = lines[idx[1]], lines[idx[0]]
		return strings.Join(lines, "")
	}
	resolvedRe := regexp.MustCompile(` resolved="[^"]+"`)
	dotdotRe := regexp.MustCompile(`( raw=")[^"]*/\.\./[^"]*(")`)
	childTidRe := regexp.MustCompile(`(RESULT label=child tid=)[0-9]+`)
	controls := []control{
		{"raw-not-ENOSYS", "family", func(s string) string {
			return strings.Replace(s, "raw=-38 raw_errno=ENOSYS", "raw=-1 raw_errno=ENOSYS", 1)
		}, id},
		{"wrong-rewritten-errno", "family", func(s string) string {
			return strings.Replace(s, "ret=-28 errno=ENOSPC injected=true", "ret=-5 errno=ENOSPC injected=true", 1)
		}, id},
		{"wrong-resolved-path", "family", func(s string) string { return resolvedRe.ReplaceAllString(s, ` resolved="/not/the/expected/target"`) }, id},
		{"entry-order-reversed", "family", reorder, id},
		{"false-passed-counter", "family", func(s string) string { return strings.Replace(s, " passed=4 ", " passed=999 ", 1) }, id},
		{"forged-marker-header", "marker", func(s string) string { return strings.Replace(s, `(INJECTED\x29`, "(INJECTED)", 1) }, id},
		{"child-result-removed", "child", id, func(s string) string {
			var keep []string
			for _, l := range strings.SplitAfter(s, "\n") {
				if !strings.HasPrefix(l, "RESULT label=child ") {
					keep = append(keep, l)
				}
			}
			return strings.Join(keep, "")
		}},
		{"child-tid-corrupt", "child", id, func(s string) string { return childTidRe.ReplaceAllString(s, "${1}99999999") }},
		{"dotdot-cleaned", "alias", func(s string) string { return dotdotRe.ReplaceAllString(s, `${1}/cleaned/target${2}`) }, id},
	}
	if len(controls) != 9 {
		t.Fatalf("%d corruption controls, want 9", len(controls))
	}
	const errno = "ENOSPC"
	for _, c := range controls {
		t.Run(c.name, func(t *testing.T) {
			sc := globalOpenScenario{c.name, c.scenario, "target", nil, 0}
			switch c.scenario {
			case "marker":
				sc.target = "m(INJECTED)"
			}
			log, out, dir := runGlobalOpenScenario(t, helper, fixture, errno, sc)
			newLog, newOut := c.trace(log), c.out(out)
			if newLog == log && newOut == out {
				t.Fatalf("INVALID CONTROL: the corruption changed nothing (the recipe no longer matches the trace)")
			}
			badLog, badOut := filepath.Join(dir, "bad.log"), filepath.Join(dir, "bad.out")
			if err := os.WriteFile(badLog, []byte(newLog), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(badOut, []byte(newOut), 0o600); err != nil {
				t.Fatal(err)
			}
			v, ve, code := globalOpenExec(t, dir, fixture, "verify", "-scenario", c.scenario, "-dir", dir, "-log", badLog, "-out", badOut, "-errno", errno)
			if code != 1 || ve != "" || !strings.Contains(v, "\nFAIL ") {
				t.Fatalf("the verifier accepted corruption %s (exit %d, stderr %q):\n%s", c.name, code, ve, v)
			}
		})
	}
}
