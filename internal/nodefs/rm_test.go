//go:build unix

package nodefs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Expectations were captured from Node 22's rmSync(dir, { recursive: true,
// force: true }) run as an unprivileged user on the same tree.
func TestRemoveAllMatchesNodeRimraf(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	if err := RemoveAll(missing); err != nil {
		t.Fatalf("ENOENT must be ignored: %v", err)
	}
	a := filepath.Join(dir, "a")
	for _, d := range []string{"a/locked", "a/é"} {
		os.MkdirAll(filepath.Join(dir, d), 0o755)
	}
	os.WriteFile(filepath.Join(a, "locked", "f"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(a, "z"), []byte("x"), 0o644)
	if os.Geteuid() != 0 {
		os.Chmod(filepath.Join(a, "locked"), 0o500)
		defer os.Chmod(filepath.Join(a, "locked"), 0o700)
		err := RemoveAll(a)
		if err == nil || err.Error() != "EACCES: permission denied, unlink '"+a+"/locked/f'" {
			t.Fatalf("got %v", err)
		}
		// Children after the failing one are left alone, as in Node.
		if _, err := os.Stat(filepath.Join(a, "z")); err != nil {
			t.Fatalf("z was removed: %v", err)
		}
		os.Chmod(filepath.Join(a, "locked"), 0o700)
	}
	if err := RemoveAll(a); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(a); !os.IsNotExist(err) {
		t.Fatalf("tree left behind: %v", err)
	}
	// A file and a dangling symlink are unlinked.
	f := filepath.Join(dir, "file")
	os.WriteFile(f, nil, 0o644)
	os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(dir, "link"))
	for _, p := range []string{f, filepath.Join(dir, "link")} {
		if err := RemoveAll(p); err != nil {
			t.Fatal(err)
		}
	}
}

// V8's recursive rimrafSync overflows its stack on a deep enough tree; the
// throw leaves the tree in place.
func TestRemoveAllDepth(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(filepath.Join(root, "a", "b", "c"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAllDepth(root, 2); err != ErrTooDeep {
		t.Fatalf("depth 3 with limit 2: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "a", "b", "c")); err != nil {
		t.Fatalf("the tree must survive the overflow: %v", err)
	}
	if err := RemoveAllDepth(root, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("root still exists: %v", err)
	}
}

// Expectations were captured from Node 22's fs/promises.rm(dir, { recursive:
// true, force: true }) as an unprivileged user on the same trees: it lstats
// the path first (rmSync unlinks instead), and it goes on with the other
// children after one fails (round 7 #12).
func TestRemoveAllAsyncMatchesNodePromisesRm(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission errors need an unprivileged user")
	}
	dir := t.TempDir()
	if err := RemoveAllAsync(filepath.Join(dir, "missing")); err != nil {
		t.Fatalf("ENOENT must be ignored: %v", err)
	}
	// An unsearchable parent: lstat fails, nothing else is tried.
	p := filepath.Join(dir, "p")
	os.MkdirAll(filepath.Join(p, "x"), 0o755)
	os.Chmod(p, 0)
	err := RemoveAllAsync(filepath.Join(p, "x"))
	if err == nil || err.Error() != "EACCES: permission denied, lstat '"+p+"/x'" {
		t.Errorf("unsearchable parent: %v", err)
	}
	if err := RemoveAll(filepath.Join(p, "x")); err == nil || err.Error() != "EACCES: permission denied, unlink '"+p+"/x'" {
		t.Errorf("rmSync form: %v", err)
	}
	os.Chmod(p, 0o755)
	// A child that cannot be removed: its siblings still go.
	q := filepath.Join(dir, "q")
	for _, d := range []string{"q/a/locked", "q/b"} {
		os.MkdirAll(filepath.Join(dir, d), 0o755)
	}
	for _, f := range []string{"q/a/locked/f", "q/b/y", "q/z"} {
		os.WriteFile(filepath.Join(dir, f), nil, 0o644)
	}
	os.Chmod(filepath.Join(q, "a", "locked"), 0o500)
	defer os.Chmod(filepath.Join(q, "a", "locked"), 0o700)
	err = RemoveAllAsync(q)
	if err == nil || err.Error() != "EACCES: permission denied, unlink '"+q+"/a/locked/f'" {
		t.Errorf("locked child: %v", err)
	}
	var left []string
	filepath.WalkDir(q, func(path string, _ os.DirEntry, _ error) error {
		rel, _ := filepath.Rel(dir, path)
		left = append(left, rel)
		return nil
	})
	if strings.Join(left, " ") != "q q/a q/a/locked q/a/locked/f" {
		t.Errorf("left behind: %v", left)
	}
	os.Chmod(filepath.Join(q, "a", "locked"), 0o700)
	if err := RemoveAllAsync(q); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(q); !os.IsNotExist(err) {
		t.Fatalf("tree left behind: %v", err)
	}
}

func TestRemoveAllRetry(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	setup := func() (dir, locked string) {
		dir = t.TempDir()
		locked = filepath.Join(dir, "d")
		if err := os.MkdirAll(locked, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(locked, "f"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(locked, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(locked, 0o700) })
		return dir, locked
	}
	// No retry: the first error is returned.
	_, locked := setup()
	if err := RemoveAllRetry(locked, 0, 0); err == nil || !strings.Contains(err.Error(), "EACCES") {
		t.Fatalf("got %v", err)
	}
	// Node's loop also retries a non-retry code at once until the last
	// try, so a directory that is read-only for a short window is removed.
	go func() {
		time.Sleep(20 * time.Millisecond)
		os.Chmod(locked, 0o700)
	}()
	if err := RemoveAllRetry(locked, 100_000_000, 0); err != nil {
		t.Fatalf("retry after the window: %v", err)
	}
	if _, err := os.Stat(locked); !os.IsNotExist(err) {
		t.Fatalf("directory remains: %v", err)
	}
	// The sleep branch: a retry code (EBUSY) twice, then success, sleeps
	// 1*delay and 2*delay; the last try's error is returned as is; ENOENT
	// ends the loop quietly.
	r := remover{maxRetries: 2, retryDelay: 40 * time.Millisecond}
	calls := 0
	start := time.Now()
	err := r.retryLoop(func() error {
		calls++
		if calls < 3 {
			return &Error{Code: "EBUSY"}
		}
		return nil
	})
	if err != nil || calls != 3 || time.Since(start) < 120*time.Millisecond {
		t.Fatalf("err %v calls %d after %v", err, calls, time.Since(start))
	}
	calls = 0
	if err := r.retryLoop(func() error { calls++; return &Error{Code: "EPERM"} }); err == nil || calls != 3 {
		t.Fatalf("err %v calls %d", err, calls)
	}
	if err := r.retryLoop(func() error { return &Error{Code: "ENOENT"} }); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := RemoveAllRetry(filepath.Join(dir, "missing"), 3, time.Millisecond); err != nil {
		t.Fatal(err)
	}
}
