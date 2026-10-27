//go:build faultline_test && unix

package difftest

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/nodefs"
)

type cpAbort struct{ what string }

var cpNames = []string{"a", "b.json", "with space", "é", "line\nbreak", "z", "runs", "Ünï", "x y"}

// buildCpTree fills dir with a random tree: directories to depth, files of
// mode 0400-0777 (empty ones included), a mode 0000 file, relative and
// absolute symlinks (the absolute ones name root) and a FIFO. Entries are
// created in a fixed order so both sibling roots list alike.
func buildCpTree(r *rand.Rand, dir, root string, depth int, special bool) {
	n := r.IntN(5)
	used := map[string]bool{}
	var files []string
	for i := 0; i < n; i++ {
		name := cpNames[r.IntN(len(cpNames))]
		if used[name] {
			continue
		}
		used[name] = true
		path := filepath.Join(dir, name)
		switch k := r.IntN(10); {
		case k < 3 && depth > 0:
			mode := []os.FileMode{0o700, 0o755, 0o750, 0o500, 0o775}[r.IntN(5)]
			os.Mkdir(path, 0o755)
			buildCpTree(r, path, root, depth-1, special)
			os.Chmod(path, mode)
		case k < 7:
			body := ""
			if r.IntN(4) != 0 {
				body = fmt.Sprintf("body %d\n", r.IntN(1000))
			}
			os.WriteFile(path, []byte(body), 0o600)
			os.Chmod(path, os.FileMode(0o400+r.IntN(0o400)))
			files = append(files, name)
		case k == 7 && special:
			n := cpNames[r.IntN(len(cpNames))]
			target := []string{n, n + "/", n + "/..", n + "/.", "nope/..", "nope/.", "nope/", name, "..", "../s", ".", "./" + n, "loopb", "loopa"}[r.IntN(14)]
			os.Symlink(target, path)
		case k == 8 && special:
			os.Symlink(filepath.Join(root, "s", cpNames[r.IntN(len(cpNames))]), path)
		case k == 9 && special && r.IntN(3) == 0:
			mkfifo(path)
		case k == 9 && special && r.IntN(2) == 0:
			os.WriteFile(path, []byte("secret"), 0o600)
			os.Chmod(path, 0)
		default:
			os.WriteFile(path, []byte("d\n"), 0o644)
		}
	}
}

// cpFixedCases are the deterministic cases run after the random ones. Each
// builds its fixture under root; the source is root/s and the destination
// root/out. They pin libstdc++'s weakly_canonical fast path (canonical() of a
// path that exists drops a trailing "/", "." and ".."), and the abort text
// when status() itself throws.
var cpFixedCases = []struct {
	name  string
	setup func(root string)
}{
	{"link to dir with slash", func(root string) {
		s := filepath.Join(root, "s")
		os.Mkdir(filepath.Join(s, "d"), 0o755)
		os.WriteFile(filepath.Join(s, "d", "f"), []byte("f\n"), 0o644)
		os.Symlink("d/", filepath.Join(s, "a"))
	}},
	{"link ./", func(root string) { os.Symlink("./", filepath.Join(root, "s", "a")) }},
	{"link d/./", func(root string) {
		s := filepath.Join(root, "s")
		os.Mkdir(filepath.Join(s, "d"), 0o755)
		os.Symlink("d/./", filepath.Join(s, "a"))
	}},
	{"link e/ where e -> d", func(root string) {
		s := filepath.Join(root, "s")
		os.Mkdir(filepath.Join(s, "d"), 0o755)
		os.Symlink("d", filepath.Join(s, "e"))
		os.Symlink("e/", filepath.Join(s, "a"))
	}},
	{"absolute link to dir with slash", func(root string) {
		s := filepath.Join(root, "s")
		os.Mkdir(filepath.Join(s, "d"), 0o755)
		os.Symlink(filepath.Join(s, "d")+"/", filepath.Join(s, "a"))
	}},
	{"abort through a self-loop", func(root string) {
		os.Symlink("loop", filepath.Join(root, "loop"))
		os.Symlink("../loop/x", filepath.Join(root, "s", "a"))
	}},
	{"permission denied under a non-searchable dir", func(root string) {
		os.MkdirAll(filepath.Join(root, "nos", "in"), 0o755)
		os.Chmod(filepath.Join(root, "nos"), 0)
		os.Symlink("../nos/in/x", filepath.Join(root, "s", "a"))
	}},
}

func describeCp(root string) string {
	var lines []string
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(path)
			lines = append(lines, "L "+rel+" "+strings.ReplaceAll(target, root, "<R>"))
		case info.IsDir():
			lines = append(lines, "D "+rel+" "+fmt.Sprintf("%04o", uint32(info.Mode().Perm())))
			if info.Mode().Perm()&0o500 != 0o500 {
				return filepath.SkipDir
			}
		case info.Mode().IsRegular():
			b, _ := os.ReadFile(path)
			lines = append(lines, "F "+rel+" "+fmt.Sprintf("%04o", uint32(info.Mode().Perm()))+" "+fmt.Sprintf("%q", b))
		default:
			lines = append(lines, "O "+rel+" "+info.Mode().String())
		}
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func TestLiveCpSyncTree(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Node's cpSync copies directories in C++ over libstdc++ on Linux; darwin's libc++ differs (errors, aborts)")
	}
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)
	c := oracle.Start(t)
	defer c.Close()
	oldAbort := nodefs.CpAbort
	defer func() { nodefs.CpAbort = oldAbort }()
	nodefs.CpAbort = func(what string) { panic(cpAbort{what}) }
	counts := map[string]int{}
	cases := 400
	if v := os.Getenv("FAULTLINE_CP_CASES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			t.Fatalf("FAULTLINE_CP_CASES: %v", err)
		}
		cases = n
	}
	for i := 0; i < cases+len(cpFixedCases); i++ {
		fixed := i - cases
		seed := uint64(i)
		var roots [2]string
		for j := range roots {
			roots[j] = t.TempDir()
			roots[j], _ = filepath.EvalSymlinks(roots[j])
			defer unlockTree(roots[j])
			r := rand.New(rand.NewPCG(seed, 77))
			s := filepath.Join(roots[j], "s")
			os.Mkdir(s, 0o755)
			if fixed >= 0 {
				if cpFixedCases[fixed].name == "permission denied under a non-searchable dir" && os.Geteuid() == 0 {
					roots[j] = roots[j] + "\x00" + filepath.Join(roots[j], "out")
					continue // root searches every directory
				}
				cpFixedCases[fixed].setup(roots[j])
				roots[j] = roots[j] + "\x00" + filepath.Join(roots[j], "out")
				continue
			}
			depth := r.IntN(5)
			buildCpTree(r, s, roots[j], depth, r.IntN(2) == 0)
			if r.IntN(3) == 0 && i%7 == 0 {
				os.Chmod(s, 0o750)
			}
			// every tenth source is not a directory: a FIFO, a file, a link
			switch i % 10 {
			case 3, 6, 9:
				os.RemoveAll(s)
				switch i % 10 {
				case 3:
					mkfifo(s)
				case 6:
					os.WriteFile(s, []byte("single\n"), 0o640)
				case 9:
					os.Mkdir(filepath.Join(roots[j], "t"), 0o755)
					os.Symlink("t", s)
				}
			}
			// the destination
			out := filepath.Join(roots[j], "out")
			switch kind := r.IntN(20); kind {
			case 0:
				os.WriteFile(out, []byte("old"), 0o644)
			case 1:
				os.Mkdir(out, 0o755)
			case 2:
				os.Mkdir(out, 0o755)
				entries, _ := os.ReadDir(s)
				for _, e := range entries {
					if e.Type().IsRegular() {
						os.WriteFile(filepath.Join(out, e.Name()), []byte("old"), 0o644)
						break
					}
				}
			case 9:
				// a destination entry that links to itself (a loop)
				os.Mkdir(out, 0o755)
				entries, _ := os.ReadDir(s)
				for _, e := range entries {
					os.Symlink(e.Name(), filepath.Join(out, e.Name()))
				}
			case 10:
				os.Symlink("out", out) // dest itself is a self-loop
			case 11:
				os.Symlink("out2", out)
				os.Symlink("out", filepath.Join(roots[j], "out2"))
			case 6, 7, 8:
				// a destination that already holds the source's links: as a
				// link to the source's own tree, a stale link or a file
				os.Mkdir(out, 0o755)
				entries, _ := os.ReadDir(s)
				for _, e := range entries {
					if e.Type()&os.ModeSymlink != 0 {
						switch kind {
						case 6:
							os.Symlink(filepath.Join(roots[j], "s"), filepath.Join(out, e.Name()))
						case 7:
							os.Symlink("stale", filepath.Join(out, e.Name()))
						default:
							os.WriteFile(filepath.Join(out, e.Name()), []byte("old"), 0o644)
						}
					}
				}
			case 3:
				out = filepath.Join(s, "out")
			case 4:
				out = filepath.Join(s, "deep", "er", "out")
			case 5:
				out = filepath.Join(roots[j], "new", "nested", "out")
			}
			roots[j] = roots[j] + "\x00" + out
		}
		goRoot, goOut, _ := strings.Cut(roots[0], "\x00")
		nodeRoot, nodeOut, _ := strings.Cut(roots[1], "\x00")
		mask := func(root, text string) string { return strings.ReplaceAll(text, root, "<R>") }

		var got string
		func() {
			defer func() {
				if p := recover(); p != nil {
					a, ok := p.(cpAbort)
					if !ok {
						panic(p)
					}
					// the libstdc++ terminate handler's stderr, as nodefs.CpAbort prints it
					got = "ABORT:terminate called after throwing an instance of 'std::filesystem::__cxx11::filesystem_error'\n  what():  filesystem error: " + a.what + "\n"
				}
			}()
			if err := nodefs.CpSyncTree(filepath.Join(goRoot, "s"), goOut); err != nil {
				got = "THREW:" + err.Error()
			} else {
				got = "OK"
			}
		}()
		got = mask(goRoot, got)
		want := oracleText(t, c, "cpSyncTree", fmt.Sprintf(`{"src":%q,"dest":%q,"cwd":%q}`, filepath.Join(nodeRoot, "s"), nodeOut, nodeRoot))
		want = mask(nodeRoot, want)
		kind := "OK"
		if strings.HasPrefix(want, "ABORT:") {
			kind = "ABORT"
		} else if want != "OK" {
			kind = want
			if k := strings.Index(kind, ":"); k > 0 && strings.HasPrefix(kind, "THREW") {
				kind = "THREW:" + strings.SplitN(strings.TrimPrefix(kind, "THREW:"), " ", 2)[0]
			}
		}
		counts[kind]++
		if fixed >= 0 {
			t.Logf("fixed %q: %.60q", cpFixedCases[fixed].name, want)
		}
		if got != want {
			t.Fatalf("case %d: message\n go:   %q\n node: %q\n%s", i, got, want, describeCp(nodeRoot))
		}
		gt, nt := describeCp(goRoot), describeCp(nodeRoot)
		if gt != nt {
			t.Fatalf("case %d (%s): trees differ\n--- go\n%s\n--- node\n%s", i, want, gt, nt)
		}
	}
	t.Logf("outcomes: %v", counts)
}
