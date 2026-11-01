package proofview

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// These tests drive the real adapters with realAttachmentDependencies (the
// real verifiers) against hostile copies of the retained attachments. The
// base artifacts come from one real base load; only the genuine public
// precedence case in attachments_recheck_test.go reloads a whole bundle.

const (
	invalidMin    = "Refusing to attach an invalid minimization record: "
	invalidRepair = "Refusing to attach an invalid repair brief: "
	invalidPrev   = "Refusing to attach an invalid prevention proof: "
)

func hostileCopy(t *testing.T, id string) (inputs AttachmentOptions) {
	t.Helper()
	corpus := serveBundleCorpus(t)
	_, _, inputs = attachmentInputs(t, corpus, serveBundleCase(t, corpus, id))
	return inputs
}

type hostileLoad func(t *testing.T, f baseFacts, path string) (string, bool)

func loadMin(t *testing.T, f baseFacts, path string) (string, bool) {
	return attempt(func() {
		loadMinimization(path, nil, f.view.Investigation, f.view.FrozenWitness, realAttachmentDependencies())
	})
}

func loadRep(t *testing.T, f baseFacts, path string) (string, bool) {
	return attempt(func() {
		loadRepair(path, nil, f.view.Investigation, f.view.FrozenWitness, realAttachmentDependencies())
	})
}

func loadPrev(t *testing.T, f baseFacts, path string) (string, bool) {
	return attempt(func() {
		loadPrevention(path, nil, f.view.Investigation, f.view.FrozenWitness, f.view.RootDigest, realAttachmentDependencies())
	})
}

type family struct {
	name   string
	id     string
	path   func(AttachmentOptions) string
	load   hostileLoad
	prefix string
	isFile bool
}

func families() []family {
	return []family{
		{"minimization", "view-combination-100", func(o AttachmentOptions) string { return *o.MinimizationFile }, loadMin, invalidMin, true},
		{"repair", "view-combination-010", func(o AttachmentOptions) string { return *o.RepairDirectory }, loadRep, invalidRepair, false},
		{"prevention", "view-prevention-verified", func(o AttachmentOptions) string { return *o.PreventionDirectory }, loadPrev, invalidPrev, false},
	}
}

// The unmodified copy loads: every hostile case below differs from this
// baseline only by the damage, so a rejection cannot be a setup failure.
func TestAttachmentHostileBaselineCopyLoads(t *testing.T) {
	f := loadFacts(t)
	for _, fam := range families() {
		t.Run(fam.name, func(t *testing.T) {
			if msg, ok := fam.load(t, f, fam.path(hostileCopy(t, fam.id))); !ok {
				t.Fatal(msg)
			}
		})
	}
}

// Portable regular-file matrix: runs on every platform.
func TestAttachmentHostileWrongPathType(t *testing.T) {
	f := loadFacts(t)
	for _, fam := range families() {
		t.Run(fam.name, func(t *testing.T) {
			path := fam.path(hostileCopy(t, fam.id))
			if fam.isFile {
				// A directory where a result file belongs.
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				// A regular file where a package directory belongs.
				if err := os.RemoveAll(path); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			msg, ok := fam.load(t, f, path)
			if ok || !strings.HasPrefix(msg, fam.prefix) {
				t.Fatalf("ok=%v msg %q, want prefix %q", ok, msg, fam.prefix)
			}
		})
	}
}

func TestAttachmentHostileMissingPath(t *testing.T) {
	f := loadFacts(t)
	for _, fam := range families() {
		t.Run(fam.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "absent")
			msg, ok := fam.load(t, f, path)
			if ok || !strings.HasPrefix(msg, fam.prefix) {
				t.Fatalf("ok=%v msg %q", ok, msg)
			}
		})
	}
}

func TestAttachmentHostileMissingAndExtraArtifact(t *testing.T) {
	f := loadFacts(t)
	for _, fam := range families() {
		if fam.isFile {
			continue
		}
		t.Run(fam.name+"/missing", func(t *testing.T) {
			dir := fam.path(hostileCopy(t, fam.id))
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) == 0 {
				t.Fatalf("setup: %v", err)
			}
			if err := os.Remove(filepath.Join(dir, "manifest.json")); err != nil {
				t.Fatal(err)
			}
			msg, ok := fam.load(t, f, dir)
			if ok || !strings.HasPrefix(msg, fam.prefix) {
				t.Fatalf("ok=%v msg %q", ok, msg)
			}
		})
		t.Run(fam.name+"/extra", func(t *testing.T) {
			dir := fam.path(hostileCopy(t, fam.id))
			if err := os.WriteFile(filepath.Join(dir, "unexpected.txt"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			msg, ok := fam.load(t, f, dir)
			if ok || !strings.HasPrefix(msg, fam.prefix) {
				t.Fatalf("ok=%v msg %q", ok, msg)
			}
		})
	}
}

func TestAttachmentHostileMalformedAndDigestMismatch(t *testing.T) {
	f := loadFacts(t)
	t.Run("minimization/malformed", func(t *testing.T) {
		path := *hostileCopy(t, "view-combination-100").MinimizationFile
		if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		if msg, ok := loadMin(t, f, path); ok || !strings.HasPrefix(msg, invalidMin) {
			t.Fatalf("ok=%v msg %q", ok, msg)
		}
	})
	// The retained digest is read from the corpus; flipping one byte of the
	// artifact must break it. Without a supplied digest the same damage is only
	// caught by the verifier's own integrity checks, which is why the corpus
	// digest is passed explicitly.
	t.Run("minimization/retained-digest-mismatch", func(t *testing.T) {
		in := hostileCopy(t, "view-combination-100")
		path := *in.MinimizationFile
		other := dg('0')
		if *in.ExpectedMinimizationDigest == other {
			other = dg('1')
		}
		msg, ok := attempt(func() {
			loadMinimization(path, &other, f.view.Investigation, f.view.FrozenWitness, realAttachmentDependencies())
		})
		want := invalidMin + "externally supplied minimization digest does not match"
		if ok || msg != want {
			t.Fatalf("ok=%v\n got %q\nwant %q", ok, msg, want)
		}
	})
	t.Run("repair/malformed-manifest", func(t *testing.T) {
		dir := *hostileCopy(t, "view-combination-010").RepairDirectory
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		if msg, ok := loadRep(t, f, dir); ok || !strings.HasPrefix(msg, invalidRepair) {
			t.Fatalf("ok=%v msg %q", ok, msg)
		}
	})
	t.Run("prevention/malformed-manifest", func(t *testing.T) {
		dir := *hostileCopy(t, "view-prevention-verified").PreventionDirectory
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		if msg, ok := loadPrev(t, f, dir); ok || !strings.HasPrefix(msg, invalidPrev) {
			t.Fatalf("ok=%v msg %q", ok, msg)
		}
	})
}

// Oversized minimization: a sparse file just past the 128 MiB verifier limit.
func TestAttachmentHostileOversizedMinimization(t *testing.T) {
	f := loadFacts(t)
	path := *hostileCopy(t, "view-combination-100").MinimizationFile
	if err := os.Truncate(path, 128*1024*1024+1); err != nil {
		t.Fatal(err)
	}
	if msg, ok := loadMin(t, f, path); ok || !strings.HasPrefix(msg, invalidMin) {
		t.Fatalf("ok=%v msg %q", ok, msg)
	}
}

// Symlinks. Creating one needs a privilege on some Windows hosts; that is the
// only eligibility condition, and it is reported with its exact reason rather
// than silently passing.
func makeSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		// Only Windows' documented "privilege not held" refusal
		// (ERROR_PRIVILEGE_NOT_HELD, 1314) makes the case inapplicable. Any
		// other error, on any platform, is a real failure.
		var errno syscall.Errno
		if runtime.GOOS == "windows" && errors.As(err, &errno) && errno == 1314 {
			t.Skipf("symlink creation needs a privilege this Windows host lacks (ERROR_PRIVILEGE_NOT_HELD): %v", err)
		}
		t.Fatal(err)
	}
}

func TestAttachmentHostileRootSymlink(t *testing.T) {
	f := loadFacts(t)
	for _, fam := range families() {
		t.Run(fam.name, func(t *testing.T) {
			real := fam.path(hostileCopy(t, fam.id))
			link := filepath.Join(t.TempDir(), "link")
			makeSymlink(t, real, link)
			msg, ok := fam.load(t, f, link)
			if ok || !strings.HasPrefix(msg, fam.prefix) {
				t.Fatalf("ok=%v msg %q", ok, msg)
			}
		})
	}
}

func TestAttachmentHostileChildSymlink(t *testing.T) {
	f := loadFacts(t)
	for _, fam := range families() {
		if fam.isFile {
			continue
		}
		t.Run(fam.name, func(t *testing.T) {
			dir := fam.path(hostileCopy(t, fam.id))
			victim := filepath.Join(dir, "manifest.json")
			saved := filepath.Join(t.TempDir(), "manifest.json")
			if err := os.Rename(victim, saved); err != nil {
				t.Fatal(err)
			}
			makeSymlink(t, saved, victim)
			msg, ok := fam.load(t, f, dir)
			if ok || !strings.HasPrefix(msg, fam.prefix) {
				t.Fatalf("ok=%v msg %q", ok, msg)
			}
		})
	}
}
