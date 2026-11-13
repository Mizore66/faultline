package witness

import (
	"encoding/base64"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/nodefs"
)

func overlayWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadModelOverlayInput(t *testing.T) {
	binary := []byte{0x00, 0xff, 0xfe, '\r', '\n', 'x', 0x80, '\n'}
	setup := func(t *testing.T) string {
		root := t.TempDir()
		overlayWrite(t, filepath.Join(root, "a", "b"), binary)
		overlayWrite(t, filepath.Join(root, "b"), []byte("top\r\n"))
		overlayWrite(t, filepath.Join(root, "..dots"), []byte("d"))
		if err := os.MkdirAll(filepath.Join(root, "dir"), 0o755); err != nil {
			t.Fatal(err)
		}
		return root
	}

	t.Run("accepted", func(t *testing.T) {
		root := setup(t)
		cases := []struct {
			name, path, want string
			bytes            []byte
		}{
			{"plain", "a/b", "a/b", binary},
			{"dot", "a/./b", "a/b", binary},
			{"dotdot", "a/../b", "b", []byte("top\r\n")},
			{"double slash", "a//b", "a/b", binary},
			{"absolute beneath root", filepath.Join(root, "b"), "b", []byte("top\r\n")},
		}
		for _, c := range cases {
			got, err := ReadModelOverlayInput(root, []string{c.path})
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			want := base64.StdEncoding.EncodeToString(c.bytes)
			if len(got) != 1 || got[0].Path != c.want || got[0].BytesBase64 != want {
				t.Fatalf("%s: got %+v", c.name, got)
			}
		}
		// Independent expectation for the binary/CRLF payload.
		got, _ := ReadModelOverlayInput(root, []string{"a/b"})
		if got[0].BytesBase64 != "AP/+DQp4gAo=" {
			t.Fatalf("base64 = %q", got[0].BytesBase64)
		}
	})

	t.Run("absolute beneath root accepted, outside refused", func(t *testing.T) {
		root := setup(t)
		got, err := ReadModelOverlayInput(root, []string{filepath.Join(root, "a", "b")})
		if err != nil || len(got) != 1 || got[0].Path != "a/b" {
			t.Fatalf("inside: %v %v", got, err)
		}
		out := filepath.Join(filepath.Dir(root), "elsewhere")
		_, err = ReadModelOverlayInput(root, []string{out})
		if err == nil || err.Error() != "Model proposed an unsafe overlay path: "+out {
			t.Fatalf("outside: %v", err)
		}
	})

	t.Run("empty selection", func(t *testing.T) {
		got, err := ReadModelOverlayInput(setup(t), nil)
		if err != nil || len(got) != 0 {
			t.Fatalf("got %v %v", got, err)
		}
	})

	t.Run("root ancestors are not rejected", func(t *testing.T) {
		base := t.TempDir()
		real := filepath.Join(base, "real")
		overlayWrite(t, filepath.Join(real, "sub", "f"), []byte("x"))
		link := filepath.Join(base, "link")
		overlayDirLink(t, real, link)
		got, err := ReadModelOverlayInput(filepath.Join(link, "sub"), []string{"f"})
		if err != nil || len(got) != 1 || got[0].Path != "f" {
			t.Fatalf("got %v %v", got, err)
		}
	})

	t.Run("unsafe paths", func(t *testing.T) {
		root := setup(t)
		outside := filepath.Join(filepath.Dir(root), "outside-"+filepath.Base(root), "f")
		for _, p := range []string{outside, "", ".", "a/..", "../x", "a/../../x", "..", "..dots", `a\b`, `a\..\b`} {
			_, err := ReadModelOverlayInput(root, []string{p})
			if err == nil || err.Error() != "Model proposed an unsafe overlay path: "+p {
				t.Fatalf("%q: err = %v", p, err)
			}
		}
	})

	t.Run("root refusals", func(t *testing.T) {
		base := t.TempDir()
		overlayWrite(t, filepath.Join(base, "file"), []byte("x"))
		_, err := ReadModelOverlayInput(filepath.Join(base, "missing"), []string{"a"})
		if e, ok := err.(*nodefs.Error); !ok || e.Code != "ENOENT" || e.Syscall != "lstat" {
			t.Fatalf("missing root: %v", err)
		}
		r := filepath.Join(base, "file")
		_, err = ReadModelOverlayInput(r, []string{"a"})
		if err == nil || err.Error() != "Overlay root must be a real directory: "+r {
			t.Fatalf("file root: %v", err)
		}
		real := filepath.Join(base, "realdir")
		if err := os.Mkdir(real, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(base, "rootlink")
		overlayDirLink(t, real, link)
		_, err = ReadModelOverlayInput(link, []string{"a"})
		if err == nil || err.Error() != "Overlay root must be a real directory: "+link {
			t.Fatalf("symlink root: %v", err)
		}
	})

	t.Run("parents and leaves", func(t *testing.T) {
		root := setup(t)
		trav := "Model overlay cannot traverse a symbolic link or non-directory: "
		leaf := "Model overlay must be a regular non-symlink file: "
		check := func(p, prefix string) {
			t.Helper()
			_, err := ReadModelOverlayInput(root, []string{p})
			if err == nil || err.Error() != prefix+p {
				t.Fatalf("%q: err = %v", p, err)
			}
		}
		check("b/x", trav)      // regular file parent
		check("dir", leaf)      // directory leaf
		check("a/../dir", leaf) // normalized directory leaf
		overlayDirLink(t, filepath.Join(root, "a"), filepath.Join(root, "dlink"))
		check("dlink/b", trav)
		check("dlink", leaf) // directory link as leaf
		if overlayHasFileLinks {
			overlayFileLink(t, filepath.Join(root, "b"), filepath.Join(root, "flink"))
			check("flink", leaf)
		}
		_, err := ReadModelOverlayInput(root, []string{"nope"})
		if e, ok := err.(*nodefs.Error); !ok || e.Code != "ENOENT" || e.Syscall != "lstat" {
			t.Fatalf("missing leaf: %v", err)
		}
		_, err = ReadModelOverlayInput(root, []string{"nodir/f"})
		if e, ok := err.(*nodefs.Error); !ok || e.Code != "ENOENT" {
			t.Fatalf("missing parent: %v", err)
		}
	})

	t.Run("duplicates and case aliases are all read", func(t *testing.T) {
		root := setup(t)
		got, err := ReadModelOverlayInput(root, []string{"a/b", "a/./b"})
		if err != nil || len(got) != 2 || got[0] != got[1] {
			t.Fatalf("duplicates: %v %v", got, err)
		}
		// Distinct case spellings that both exist are both returned for the
		// store's case-insensitive duplicate handling.
		overlayWrite(t, filepath.Join(root, "x", "Y"), []byte("1"))
		overlayWrite(t, filepath.Join(root, "x", "y2"), []byte("2"))
		got, err = ReadModelOverlayInput(root, []string{"x/Y", "x/y2"})
		if err != nil || len(got) != 2 || got[0].Path != "x/Y" || got[1].Path != "x/y2" {
			t.Fatalf("got %v %v", got, err)
		}
		// Probe the real filesystem: does "A/B" name the same file as "a/b"?
		_, probe := os.Lstat(filepath.Join(root, "A", "B"))
		if probe == nil { // case-insensitive: both alias spellings come back
			got, err = ReadModelOverlayInput(root, []string{"a/b", "A/B"})
			if err != nil || len(got) != 2 || got[0].Path != "a/b" || got[1].Path != "A/B" || got[0].BytesBase64 != got[1].BytesBase64 {
				t.Fatalf("alias: %v %v", got, err)
			}
		} else { // case-sensitive: distinct-case files are separate reads
			overlayWrite(t, filepath.Join(root, "A", "B"), []byte("upper"))
			got, err = ReadModelOverlayInput(root, []string{"a/b", "A/B"})
			if err != nil || len(got) != 2 || got[0].Path != "a/b" || got[1].Path != "A/B" || got[0].BytesBase64 == got[1].BytesBase64 {
				t.Fatalf("distinct case: %v %v", got, err)
			}
		}
	})

	t.Run("large and long inputs are read here", func(t *testing.T) {
		root := t.TempDir()
		big := make([]byte, 1_050_001) // base64 > 1,400,000 chars
		overlayWrite(t, filepath.Join(root, "big"), big)
		long := strings.Repeat("d/", 300) + "f" // normalized path > 512
		overlayWrite(t, filepath.Join(root, filepath.FromSlash(long)), []byte("z"))
		got, err := ReadModelOverlayInput(root, []string{"big", long})
		if err != nil || len(got) != 2 {
			t.Fatalf("err %v", err)
		}
		if len(got[0].BytesBase64) <= 1_400_000 || len(got[1].Path) <= 512 {
			t.Fatalf("lens %d %d", len(got[0].BytesBase64), len(got[1].Path))
		}
	})

	t.Run("sequential order and stop at first failure", func(t *testing.T) {
		root := setup(t)
		var calls []string
		rel := func(p string) string { return filepath.ToSlash(strings.TrimPrefix(p, root)) }
		ops := overlayOps{
			lstat: func(p string) (fs.FileInfo, error) {
				calls = append(calls, "lstat "+rel(p))
				return nodefs.Lstat(p)
			},
			read: func(p string) ([]byte, error) {
				calls = append(calls, "read "+rel(p))
				return nodefs.ReadBytes(p)
			},
		}
		_, err := readModelOverlayInput(ops, root, []string{"a/b", "b", "dir", "b"})
		if err == nil {
			t.Fatal("expected dir refusal")
		}
		want := "lstat |lstat /a|lstat /a/b|read /a/b|lstat /b|read /b|lstat /dir"
		if got := strings.Join(calls, "|"); got != want {
			t.Fatalf("calls:\n got %s\nwant %s", got, want)
		}
	})
}
