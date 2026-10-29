//go:build faultline_test && !windows

package difftest

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/canonical"
)

// stagePerSide is the receipt stage of the case rooted at root for the
// receipt at receiptRel (relative to the root of the case it was discovered
// in): writeJsonAtomic's ".<sha256(receipt path)[0:24]>.<pid>.tmp" beside it
// (src/codex-sidecar.ts:529), with <PID> for the process id.
//
// root must be the canonical (symlink-resolved) case root, as makeWriterRoots
// makes it with EvalSymlinks: the writer hashes the realpath of the receipt,
// so with any other root the key differs, strace -P matches nothing and the
// run injects zero faults (the "made no call to fail" guard then fails).
func stagePerSide(root, receiptRel string) string {
	abs := root + "/" + receiptRel
	return path.Join(path.Dir(receiptRel), "."+canonical.SHA256Hex(abs)[:24]+".<PID>.tmp")
}

// stagedPid is the process id in a ledger binding's staged name
// (src/ledger-binding.ts:540 `.${basename}.${pid}.${uuid}.tmp`).
var stagedPid = regexp.MustCompile(`(\.[^./]+\.json)\.[0-9]+(\.[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.tmp)`)

// maskReceiptStage masks the key and pid of the receipt stage stageRel (as
// stagePerSide gives it for this side's root) in text and renames the file
// of that exact name and directory, so the sides' trees compare: the key
// differs between the sides, the pid between runs. It touches nothing else,
// and does nothing without a receipt stage (stageRel "").
func maskReceiptStage(t *testing.T, r writerRoots, stageRel, stdout, stderr string) (string, string) {
	t.Helper()
	if stageRel == "" {
		return stdout, stderr
	}
	pattern := strings.Replace(regexp.QuoteMeta(path.Base(stageRel)), "<PID>", "[0-9]+", 1)
	name, whole := regexp.MustCompile(pattern), regexp.MustCompile("^"+pattern+"$")
	const masked = ".<STAGEKEY>.<PID>.tmp"
	wantDir := path.Dir(stageRel)
	filepath.WalkDir(r.root, func(p string, d os.DirEntry, err error) error {
		if err != nil || !whole.MatchString(d.Name()) {
			return nil
		}
		if rel, err := filepath.Rel(r.root, filepath.Dir(p)); err != nil || filepath.ToSlash(rel) != wantDir {
			return nil
		}
		if err := os.Rename(p, filepath.Join(filepath.Dir(p), masked)); err != nil {
			t.Fatal(err)
		}
		return nil
	})
	return name.ReplaceAllString(stdout, masked), name.ReplaceAllString(stderr, masked)
}

// maskStagedPid masks the pid in staged names in text and renames any such
// file the invocation left in the workspace, so TS and Go trees compare.
// The receipt stage (stageRel, see maskReceiptStage) is masked on top.
func maskStagedPid(t *testing.T, r writerRoots, stageRel, stdout, stderr string) (string, string) {
	t.Helper()
	filepath.WalkDir(r.ws, func(p string, d os.DirEntry, err error) error {
		if err == nil && stagedPid.MatchString(d.Name()) {
			if err := os.Rename(p, filepath.Join(filepath.Dir(p), stagedPid.ReplaceAllString(d.Name(), "$1.<PID>$2"))); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	})
	stdout, stderr = stagedPid.ReplaceAllString(stdout, "$1.<PID>$2"), stagedPid.ReplaceAllString(stderr, "$1.<PID>$2")
	return maskReceiptStage(t, r, stageRel, stdout, stderr)
}

func TestMaskReceiptStage(t *testing.T) {
	r := writerRoots{root: t.TempDir()}
	r.ws = filepath.Join(r.root, "ws")
	stageRel := stagePerSide(r.root, "ws/.git/faultline/recordings/codex-stop-1.json")
	dir := filepath.Join(r.root, path.Dir(stageRel))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stageName := strings.Replace(path.Base(stageRel), "<PID>", "4242", 1)
	// Same shape, other key or other directory: not the receipt stage.
	other := "." + strings.Repeat("a", 24) + ".4242.tmp"
	for _, f := range []string{filepath.Join(dir, stageName), filepath.Join(dir, other), filepath.Join(r.ws, other)} {
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, errText := maskReceiptStage(t, r, stageRel, "wrote "+stageName+" and "+other, other)
	if want := "wrote .<STAGEKEY>.<PID>.tmp and " + other; out != want || errText != other {
		t.Errorf("text = %q, %q; want %q, %q", out, errText, want, other)
	}
	for _, want := range []string{filepath.Join(dir, ".<STAGEKEY>.<PID>.tmp"), filepath.Join(dir, other), filepath.Join(r.ws, other)} {
		if _, err := os.Stat(want); err != nil {
			t.Errorf("want %s: %v", want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, stageName)); err == nil {
		t.Errorf("receipt stage %s was not renamed", stageName)
	}
}

// Without a receipt stage (every ordinary fault case) nothing that merely
// looks like one is masked, so a differing key or pid still fails the
// comparison.
func TestMaskStagedPidLeavesOrdinaryCases(t *testing.T) {
	r := writerRoots{root: t.TempDir()}
	r.ws = filepath.Join(r.root, "ws")
	if err := os.MkdirAll(r.ws, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "." + strings.Repeat("b", 24) + ".777.tmp"
	if err := os.WriteFile(filepath.Join(r.ws, name), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out, errText := maskStagedPid(t, r, "", "left "+name, name)
	if out != "left "+name || errText != name {
		t.Errorf("text masked: %q, %q", out, errText)
	}
	if _, err := os.Stat(filepath.Join(r.ws, name)); err != nil {
		t.Errorf("file renamed: %v", err)
	}
	// A ledger binding's staged name is still masked.
	led := ".ledger.json.55.0123abcd-0123-0123-0123-0123456789ab.tmp"
	if out, _ := maskStagedPid(t, r, "", led, ""); out != ".ledger.json.<PID>.0123abcd-0123-0123-0123-0123456789ab.tmp" {
		t.Errorf("ledger binding stage not masked: %q", out)
	}
}
