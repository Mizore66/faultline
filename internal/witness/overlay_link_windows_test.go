//go:build windows

package witness

import (
	"os/exec"
	"testing"
)

// overlayDirLink makes a junction, which libuv's lstat reports as a symbolic
// link and which needs no symlink privilege.
func overlayDirLink(t *testing.T, target, link string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v %s", err, out)
	}
}

// File symlinks need a privilege that is not guaranteed; the Windows leaf
// case is covered by the junction test instead.
func overlayFileLink(t *testing.T, target, link string) {
	t.Helper()
	t.Fatal("file symlinks are not exercised on Windows")
}

const overlayHasFileLinks = false
