//go:build unix

package demo

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"testing"

	"github.com/Mizore66/faultline/internal/demofixture"
)

// physEntry is one lstat record of a tree that may hold special files. A
// regular file carries its bytes, a symlink its target; a FIFO is recorded by
// type alone and is never opened.
type physEntry struct {
	Path, Kind, Body, Target string
}

func physInventory(t *testing.T, root string) []physEntry {
	t.Helper()
	var out []physEntry
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		info, lerr := os.Lstat(path)
		if lerr != nil {
			return lerr
		}
		e := physEntry{Path: filepath.ToSlash(rel)}
		switch m := info.Mode(); {
		case m&fs.ModeSymlink != 0:
			e.Kind = "symlink"
			if e.Target, lerr = os.Readlink(path); lerr != nil {
				return lerr
			}
		case m.IsDir():
			e.Kind = "directory"
		case m.IsRegular():
			e.Kind = "file"
			b, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			e.Body = string(b)
		case m&fs.ModeNamedPipe != 0:
			e.Kind = "fifo" // never opened
		default:
			e.Kind = "other:" + m.Type().String()
		}
		out = append(out, e)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// diffInventories returns the first difference between two inventories, or "".
func diffInventories(got, want []physEntry) string {
	for i := 0; i < len(got) && i < len(want); i++ {
		if got[i] != want[i] {
			return fmt.Sprintf("entry %d: %q kind %q (%d body bytes, target %q) differs from %q kind %q (%d body bytes, target %q)",
				i, got[i].Path, got[i].Kind, len(got[i].Body), got[i].Target, want[i].Path, want[i].Kind, len(want[i].Body), want[i].Target)
		}
	}
	if len(got) != len(want) {
		return fmt.Sprintf("%d entries, want %d", len(got), len(want))
	}
	return ""
}

// The comparison must see a changed path or body even when the entry count
// is the same; the earlier count-only check would have passed both.
func TestPhysInventoryDetectsSameCountChanges(t *testing.T) {
	base := []physEntry{{"a", "file", "one", ""}, {"b", "fifo", "", ""}, {"c", "symlink", "", "a"}}
	if d := diffInventories(append([]physEntry(nil), base...), base); d != "" {
		t.Fatalf("identical inventories differ: %s", d)
	}
	for name, mutate := range map[string]func(e []physEntry){
		"body":     func(e []physEntry) { e[0].Body = "two" },
		"path":     func(e []physEntry) { e[0].Path = "aa" },
		"kind":     func(e []physEntry) { e[1].Kind = "file" },
		"target":   func(e []physEntry) { e[2].Target = "b" },
		"fifo->rm": func(e []physEntry) { e[1] = physEntry{"b2", "file", "", ""} },
	} {
		changed := append([]physEntry(nil), base...)
		mutate(changed)
		if len(changed) != len(base) {
			t.Fatal("counterexample must keep the count")
		}
		if d := diffInventories(changed, base); d == "" {
			t.Errorf("%s: same-count change not detected", name)
		}
	}
}

// A FIFO inside an existing output is refused by the link/special-file walk
// before anything is opened (a read of it would hang), and the complete tree
// (paths, kinds, regular-file bytes, link targets) is unchanged: no stage, no
// backup, no replacement. An unexpected Mkfifo failure is a test failure.
func TestDemoWriterSpecialFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "managed")
	out := filepath.Join(root, "demo")
	copyTree(t, basesDir+"demo-replay", out)
	fifo := filepath.Join(out, "zz-fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo in the fixture failed: %v", err)
	}
	before := physInventory(t, root)
	var sawFifo bool
	for _, e := range before {
		sawFifo = sawFifo || (e.Path == "demo/zz-fifo" && e.Kind == "fifo")
	}
	if !sawFifo {
		t.Fatal("fixture inventory lacks the fifo")
	}
	_, err := Write(out, demofixture.CreateReplayAnalysis(), WriteOptions{ProofRoot: &root})
	want := "Proof bundle destination contains a symbolic link or special file: " + fifo
	if err == nil || err.Error() != want {
		t.Fatalf("error %v, want %q", err, want)
	}
	if d := diffInventories(physInventory(t, root), before); d != "" {
		t.Fatalf("tree changed after a refused write: %s", d)
	}
}
