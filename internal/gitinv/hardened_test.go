//go:build faultline_test && unix

package gitinv

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsjson"
)

func gitRepo(t *testing.T) (dir, head string) {
	dir = t.TempDir()
	for _, a := range [][]string{{"init", "-q"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "a"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, a...)...).CombinedOutput(); err != nil {
			t.Fatalf("%s", out)
		}
	}
	os.WriteFile(filepath.Join(dir, "state.txt"), []byte("good\n"), 0o644)
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "b").Run()
	out, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	return dir, string(out[:40])
}

// fakeGitDir resolves difftest/testdata/fakegit/<name>.
func fakeGitDir(t *testing.T, name string) string {
	_, here, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(here), "..", "..", "difftest", "testdata", "fakegit", name)
}

// endsWith reports whether stderr's last line starts with prefix (the
// line FaultLine appends after stopping Git).
func endsWith(stderr, prefix string) bool {
	i := strings.LastIndex(stderr, "\nFaultLine stopped Git: ")
	return i >= 0 && strings.HasPrefix(stderr[i:], prefix)
}

func TestMaterializeAndCleanUp(t *testing.T) {
	clock.ResetForTest("materialize")
	repo, head := gitRepo(t)
	root := t.TempDir()
	tree, err := MaterializeGitTree(repo, head, root, "state-0000-"+head[:16])
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(tree.Worktree, "state.txt")); string(b) != "good\n" {
		t.Fatalf("worktree content %q", b)
	}
	if err := tree.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tree.Worktree); !os.IsNotExist(err) {
		t.Fatal("worktree survived cleanup")
	}
}

func TestHardenedGitTimeoutMessage(t *testing.T) {
	t.Setenv("PATH", fakeGitDir(t, "slow")+string(os.PathListSeparator)+os.Getenv("PATH"))
	restore := SetGitTimeoutForTest(200) // ms
	defer restore()
	r := RunHardenedGit([]string{"status"}, HardenedOptions{Repository: t.TempDir()})
	if r.ExitCode != nil || r.Error == "" || !endsWith(string(r.Stderr), "\nFaultLine stopped Git: ") {
		t.Fatalf("%+v", r)
	}
	if r.Error != "Git command exceeded bounded execution time" {
		t.Fatal(r.Error)
	}
}

func TestHardenedGitArgvAndText(t *testing.T) {
	repo, head := gitRepo(t)
	got, err := HardenedGitText([]string{"rev-parse", "HEAD"}, HardenedOptions{Repository: repo})
	if err != nil || got != head {
		t.Fatal(got, err)
	}
	_, err = HardenedGitText([]string{"rev-parse", "--verify", "nope"}, HardenedOptions{Repository: repo})
	if err == nil || !strings.HasPrefix(err.Error(), "Git rev-parse --verify nope failed: fatal: ") {
		t.Fatal(err)
	}
	r := RunHardenedGit([]string{"apply", "--check"}, HardenedOptions{Repository: repo, Stdin: []byte("garbage\n"), HasStdin: true})
	if r.ExitCode == nil || *r.ExitCode == 0 || r.Error != "" {
		t.Fatalf("%+v", r)
	}
	env := HardenedGitEnvironment(HardenedOptions{GitDir: "/g", WorkTree: "/w", IndexFile: "/i"})
	if env[0] != "PATH="+os.Getenv("PATH") || env[2] != "GIT_CONFIG_GLOBAL=/dev/null" || env[len(env)-1] != "GIT_INDEX_FILE=/i" || len(env) != 12 {
		t.Fatal(env)
	}
}

func TestHardenedGitSpawnFailure(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	r := RunHardenedGit([]string{"status"}, HardenedOptions{Repository: t.TempDir()})
	if r.ExitCode != nil || r.Error != "spawn git ENOENT" || len(r.Stderr) != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestSafeMaterializationRefusals(t *testing.T) {
	repo, head := gitRepo(t)
	path := os.Getenv("PATH")
	t.Setenv("PATH", fakeGitDir(t, "filter-config")+string(os.PathListSeparator)+path)
	if err := AssertSafeGitMaterialization(repo, []string{head}); err == nil || err.Error() != "Refusing host tree materialization: repository local Git filter configuration is present." {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeGitDir(t, "attrs-filter")+string(os.PathListSeparator)+path)
	if err := AssertSafeGitMaterialization(repo, []string{head}); err == nil || err.Error() != "Refusing host tree materialization: "+head+" declares a Git filter attribute." {
		t.Fatal(err)
	}
}

func overlayWitness(path string, bytes []byte, b64 string) jsjson.Value {
	o := jsjson.NewObj()
	o.Set("path", jsjson.MakeString(path))
	o.Set("bytesBase64", jsjson.MakeString(b64))
	o.Set("bytesDigest", jsjson.MakeString("sha256:"+canonical.SHA256HexBytes(bytes)))
	w := jsjson.NewObj()
	w.Set("overlays", jsjson.MakeArray([]jsjson.Value{jsjson.MakeObject(o)}))
	p := jsjson.NewObj()
	p.Set("witness", jsjson.MakeObject(w))
	f := jsjson.NewObj()
	f.Set("proposal", jsjson.MakeObject(p))
	return jsjson.MakeObject(f)
}

func TestMaterializeFrozenOverlays(t *testing.T) {
	clock.ResetForTest("overlay")
	dir := t.TempDir()
	facts, err := MaterializeFrozenOverlays(dir, overlayWitness("a/b.txt", []byte("hi"), "aG k"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := jsjson.Stringify(facts); got != `[{"path":"a/b.txt","bytesDigest":"sha256:`+canonical.SHA256HexBytes([]byte("hi"))+`","bytesLength":2}]` {
		t.Fatal(got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a", "b.txt")); string(b) != "hi" {
		t.Fatalf("%q", b)
	}
	if left, _ := os.ReadDir(filepath.Join(dir, "a")); len(left) != 1 {
		t.Fatal(left)
	}
	_, err = MaterializeFrozenOverlays(dir, overlayWitness("../x", []byte("hi"), "aGk="), nil)
	if err == nil || err.Error() != "Frozen overlay path is unsafe: ../x" {
		t.Fatal(err)
	}
	os.Symlink("b.txt", filepath.Join(dir, "a", "link"))
	_, err = MaterializeFrozenOverlays(dir, overlayWitness("a/link", []byte("hi"), "aGk="), nil)
	if err == nil || err.Error() != "Frozen overlay target is a symbolic link: a/link" {
		t.Fatal(err)
	}
	_, err = MaterializeFrozenOverlays(dir, overlayWitness("a/c", []byte("x"), "aGk="), nil)
	if err == nil || err.Error() != "Frozen overlay digest does not match its bytes: a/c" {
		t.Fatal(err)
	}
}

func TestOverlayTargetsNotGitSymlinks(t *testing.T) {
	clock.ResetForTest("symlink")
	repo, _ := gitRepo(t)
	os.Symlink("state.txt", filepath.Join(repo, "link"))
	exec.Command("git", "-C", repo, "add", ".").Run()
	exec.Command("git", "-C", repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "c").Run()
	out, _ := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	tree, err := MaterializeGitTree(repo, string(out[:40]), t.TempDir(), "x")
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Cleanup()
	if err := AssertOverlayTargetsNotGitSymlinks(tree, []string{"state.txt", "missing"}); err != nil {
		t.Fatal(err)
	}
	if err := AssertOverlayTargetsNotGitSymlinks(tree, []string{"link"}); err == nil || err.Error() != "Frozen overlay target is a symbolic link: link" {
		t.Fatal(err)
	}
	if err := AssertOverlayTargetsNotGitSymlinks(tree, []string{"/abs"}); err == nil || err.Error() != "Frozen overlay path is unsafe: /abs" {
		t.Fatal(err)
	}
}
