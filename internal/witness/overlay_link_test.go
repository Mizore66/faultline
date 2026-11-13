//go:build !windows

package witness

import (
	"os"
	"testing"
)

// overlayDirLink makes a directory symlink.
func overlayDirLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// overlayFileLink makes a file symlink.
func overlayFileLink(t *testing.T, target, link string) {
	t.Helper()
	overlayDirLink(t, target, link)
}

const overlayHasFileLinks = true
