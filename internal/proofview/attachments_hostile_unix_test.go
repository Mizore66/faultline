//go:build unix

package proofview

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A named pipe where a regular artifact belongs. Only built where mkfifo
// exists; Windows has no such special file, so nothing is skipped there.
func TestAttachmentHostileSpecialFile(t *testing.T) {
	f := loadFacts(t)
	for _, fam := range families() {
		t.Run(fam.name, func(t *testing.T) {
			path := fam.path(hostileCopy(t, fam.id))
			victim := path
			if !fam.isFile {
				victim = filepath.Join(path, "manifest.json")
			}
			if err := os.Remove(victim); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(victim, 0o600); err != nil {
				t.Fatal(err)
			}
			msg, ok := fam.load(t, f, path)
			if ok || !strings.HasPrefix(msg, fam.prefix) {
				t.Fatalf("ok=%v msg %q", ok, msg)
			}
		})
	}
}
