//go:build faultline_test && unix

package difftest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
)

// TestLiveBase64 compares jsstr.DecodeBase64Lenient with Node's
// Buffer.from(s, "base64") over random strings mixing both alphabets,
// padding, whitespace, other ASCII, Latin-1, wider BMP characters and lone
// surrogates.
func TestLiveBase64(t *testing.T) {
	o := oracle.Start(t)
	defer o.Close()
	r := rand.New(rand.NewPCG(11, 0))
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/-_"
	unit := func() uint16 {
		switch n := r.IntN(100); {
		case n < 70:
			return uint16(alphabet[r.IntN(len(alphabet))])
		case n < 80:
			return '='
		case n < 85:
			return uint16(" \t\n\r"[r.IntN(4)])
		case n < 90:
			return uint16(r.IntN(0x80))
		case n < 94:
			return uint16(0x80 + r.IntN(0x80))
		case n < 97:
			return uint16(0x100 + r.IntN(0xd700))
		default:
			return uint16(0xd800 + r.IntN(0x800))
		}
	}
	const total, batch = 20000, 500
	for start := 0; start < total; start += batch {
		texts := make([]jsjson.Value, batch)
		for i := range texts {
			units := make([]uint16, r.IntN(24))
			for j := range units {
				units[j] = unit()
			}
			texts[i] = jsjson.MakeString(jsstr.FromUTF16(units))
		}
		args := jsjson.NewObj()
		args.Set("texts", jsjson.MakeArray(texts))
		raw, err := o.Call("base64Lenient", jsjson.Stringify(jsjson.MakeObject(args)))
		if err != nil {
			t.Fatal(err)
		}
		want, err := jsjson.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		for i, text := range texts {
			if got, w := string(jsstr.DecodeBase64Lenient(text.Str())), string(latin1(want.Items()[i].Str())); got != w {
				t.Fatalf("%q: got %x, want %x", text.Str(), got, w)
			}
		}
	}
}

// outputView is the oracle's view of Git output.
type outputView struct {
	Length int     `json:"length"`
	SHA256 string  `json:"sha256"`
	Text   *string `json:"text"`
}

// hardenedRun is the oracle's runHardenedGit result.
type hardenedRun struct {
	ExitCode *int       `json:"exitCode"`
	Stdout   outputView `json:"stdout"`
	Stderr   outputView `json:"stderr"`
	Error    *string    `json:"error"`
}

func viewOf(b []byte) outputView {
	sum := sha256.Sum256(b)
	v := outputView{Length: len(b), SHA256: hex.EncodeToString(sum[:])}
	if len(b) <= 65536 {
		s := string(b)
		v.Text = &s
	}
	return v
}

func viewEqual(a, b outputView) bool {
	return a.Length == b.Length && a.SHA256 == b.SHA256 && (a.Text == nil) == (b.Text == nil)
}

// liveRepo is a repository with a nested file, an executable, a symlink
// and an empty-blob file, at two commits; it returns the directory and
// both commits.
func liveRepo(t *testing.T) (string, []string) {
	dir := t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	os.WriteFile(filepath.Join(dir, "state.txt"), []byte("good\n"), 0o644)
	git("add", ".")
	git("commit", "-q", "-m", "a")
	os.MkdirAll(filepath.Join(dir, "src", "deep"), 0o755)
	os.WriteFile(filepath.Join(dir, "src", "deep", "a.js"), []byte("export const a = 1;\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\necho hi\n"), 0o755)
	os.WriteFile(filepath.Join(dir, "empty"), nil, 0o644)
	os.Symlink("state.txt", filepath.Join(dir, "link"))
	git("add", ".")
	git("commit", "-q", "-m", "b")
	return dir, []string{git("rev-parse", "HEAD~1"), git("rev-parse", "HEAD")}
}

// TestLiveHardenedGit runs the same argv sets through the oracle's
// runHardenedGit and gitinv.RunHardenedGit and compares exit code, both
// outputs and error.
func TestLiveHardenedGit(t *testing.T) {
	o := oracle.Start(t)
	defer o.Close()
	repo, commits := liveRepo(t)
	fake := func(name string) string {
		return filepath.Join(oracle.RepoRoot(t), "difftest", "testdata", "fakegit", name) + string(os.PathListSeparator) + os.Getenv("PATH")
	}
	gitDir := filepath.Join(repo, ".git")
	index := filepath.Join(t.TempDir(), "x.index")
	patch := "diff --git a/state.txt b/state.txt\n--- a/state.txt\n+++ b/state.txt\n@@ -1 +1 @@\n-good\n+bad\n"
	type hardenedCase struct {
		name  string
		args  []string
		opts  gitinv.HardenedOptions
		path  string
		async bool // run Go alongside the oracle (the 30 s timeout)
	}
	cases := []hardenedCase{
		{name: "success", args: []string{"rev-parse", "HEAD"}, opts: gitinv.HardenedOptions{Repository: repo}},
		{name: "exit 1", args: []string{"config", "--local", "--get-regexp", `^filter\.`}, opts: gitinv.HardenedOptions{Repository: repo}},
		{name: "exit 128", args: []string{"rev-parse", "--verify", "nope"}, opts: gitinv.HardenedOptions{Repository: repo}},
		{name: "missing repository", args: []string{"status"}, opts: gitinv.HardenedOptions{Repository: "/faultline-no-such-repo"}},
		{name: "relative repository", args: []string{"rev-parse", "--show-prefix"}, opts: gitinv.HardenedOptions{Repository: "."}},
		{name: "ls-tree", args: []string{"ls-tree", "-r", commits[1]}, opts: gitinv.HardenedOptions{Repository: repo}},
		{name: "stdin patch apply", args: []string{"apply", "--check", "-"}, opts: gitinv.HardenedOptions{Repository: repo, Stdin: []byte(patch), HasStdin: true}},
		{name: "stdin garbage", args: []string{"apply", "--check", "-"}, opts: gitinv.HardenedOptions{Repository: repo, Stdin: []byte("garbage\x00\xff\n"), HasStdin: true}},
		{name: "empty stdin", args: []string{"hash-object", "--stdin"}, opts: gitinv.HardenedOptions{Repository: repo, HasStdin: true}},
		{name: "stdin unread", args: []string{"rev-parse", "HEAD"}, opts: gitinv.HardenedOptions{Repository: repo, Stdin: []byte(strings.Repeat("x", 1<<20)), HasStdin: true}},
		{name: "external index", args: []string{"read-tree", "--reset", commits[1]},
			opts: gitinv.HardenedOptions{Repository: repo, GitDir: gitDir, WorkTree: repo, IndexFile: index}},
		{name: "external index listing", args: []string{"ls-files", "--stage"},
			opts: gitinv.HardenedOptions{Repository: repo, GitDir: gitDir, WorkTree: repo, IndexFile: index}},
		{name: "git-dir and work-tree", args: []string{"rev-parse", "--git-dir", "--show-toplevel"},
			opts: gitinv.HardenedOptions{Repository: repo, GitDir: gitDir, WorkTree: repo}},
		{name: "NUL in argument", args: []string{"rev-parse", "a\x00b"}, opts: gitinv.HardenedOptions{Repository: repo}},
		{name: "git missing", args: []string{"status"}, opts: gitinv.HardenedOptions{Repository: repo}, path: t.TempDir()},
		{name: "fake eacces", args: []string{"rev-parse", "HEAD"}, opts: gitinv.HardenedOptions{Repository: repo}, path: fake("eacces")},
		{name: "fake eacces only", args: []string{"rev-parse", "HEAD"}, opts: gitinv.HardenedOptions{Repository: repo},
			path: filepath.Join(oracle.RepoRoot(t), "difftest", "testdata", "fakegit", "eacces")},
		{name: "fake filter-config", args: []string{"config", "--local", "--get-regexp", `^filter\.`}, opts: gitinv.HardenedOptions{Repository: repo}, path: fake("filter-config")},
		{name: "fake filter-config passthrough", args: []string{"rev-parse", "HEAD"}, opts: gitinv.HardenedOptions{Repository: repo}, path: fake("filter-config")},
		{name: "fake attrs-filter", args: []string{"grep", "-n", "filter", commits[1]}, opts: gitinv.HardenedOptions{Repository: repo}, path: fake("attrs-filter")},
		{name: "fake flood", args: []string{"cat-file", "-p", commits[1]}, opts: gitinv.HardenedOptions{Repository: repo}, path: fake("flood")},
		{name: "fake slow", args: []string{"status"}, opts: gitinv.HardenedOptions{Repository: repo}, path: fake("slow"), async: true},
	}
	cwd, _ := os.Getwd()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.path != "" {
				t.Setenv("PATH", tc.path)
			}
			args := map[string]any{"repository": tc.opts.Repository, "args": tc.args}
			if tc.opts.Repository == "." {
				// The oracle runs from the repository root, like this package's parent.
				args["repository"] = cwd
				tc.opts.Repository = cwd
			}
			if tc.opts.HasStdin {
				args["stdin"] = toLatin1(string(tc.opts.Stdin))
			}
			for k, v := range map[string]string{"gitDir": tc.opts.GitDir, "worktree": tc.opts.WorkTree, "indexFile": tc.opts.IndexFile, "path": tc.path} {
				if v != "" {
					args[k] = v
				}
			}
			req, _ := json.Marshal(args)
			var got gitinv.GitRun
			var wg sync.WaitGroup
			if tc.async {
				wg.Add(1)
				go func() { defer wg.Done(); got = gitinv.RunHardenedGit(tc.args, tc.opts) }()
			}
			res, err := o.Call("runHardenedGit", string(req))
			if err != nil {
				t.Fatal(err)
			}
			var want hardenedRun
			if err := json.Unmarshal([]byte(res), &want); err != nil {
				t.Fatal(err)
			}
			if tc.async {
				wg.Wait()
			} else {
				got = gitinv.RunHardenedGit(tc.args, tc.opts)
			}
			gotErr := deref(want.Error)
			if !equalPtr(got.ExitCode, want.ExitCode) || got.Error != gotErr ||
				!viewEqual(viewOf(got.Stdout), want.Stdout) || !viewEqual(viewOf(got.Stderr), want.Stderr) {
				t.Fatalf("got  exit %v error %q stdout %d %.200q stderr %d %.400q\nwant exit %v error %q stdout %d %.200q stderr %d %.400q",
					ptrText(got.ExitCode), got.Error, len(got.Stdout), got.Stdout, len(got.Stderr), got.Stderr,
					ptrText(want.ExitCode), gotErr, want.Stdout.Length, deref(want.Stdout.Text), want.Stderr.Length, deref(want.Stderr.Text))
			}
			if want.Stdout.Text != nil && string(latin1(*want.Stdout.Text)) != string(got.Stdout) ||
				want.Stderr.Text != nil && string(latin1(*want.Stderr.Text)) != string(got.Stderr) {
				t.Fatal("output text differs")
			}
			recordOutput(t, gotErr+"\n"+deref(want.Stderr.Text))
		})
	}
	// The bounded-run reasons (listed in unreachableLiterals for the
	// materialization gate, which cannot make a real git slow or flood).
	for _, reason := range []string{"Git command exceeded bounded execution time", "Git command exceeded bounded output limit"} {
		if !strings.Contains(recordedOutputs(t), reason) {
			t.Errorf("no case reached %q", reason)
		}
	}
}

func ptrText(p *int) string {
	if p == nil {
		return "null"
	}
	return fmt.Sprint(*p)
}

// treeListing lists a worktree: one line per entry, sorted by path, with
// its kind, permission bits and content digest or link target.
func treeListing(t *testing.T, root string) string {
	var lines []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, _ := os.Readlink(path)
			lines = append(lines, fmt.Sprintf("%s link %s", rel, target))
		case info.IsDir():
			lines = append(lines, fmt.Sprintf("%s dir %o", rel, info.Mode().Perm()))
		default:
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(b)
			lines = append(lines, fmt.Sprintf("%s file %o %x", rel, info.Mode().Perm(), sum))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n")
}

func liveOverlayWitness(overlays ...[3]string) string {
	items := make([]jsjson.Value, len(overlays))
	for i, ov := range overlays {
		o := jsjson.NewObj()
		o.Set("path", jsjson.MakeString(ov[0]))
		o.Set("bytesBase64", jsjson.MakeString(ov[1]))
		o.Set("bytesDigest", jsjson.MakeString(ov[2]))
		items[i] = jsjson.MakeObject(o)
	}
	w := jsjson.NewObj()
	w.Set("overlays", jsjson.MakeArray(items))
	p := jsjson.NewObj()
	p.Set("witness", jsjson.MakeObject(w))
	f := jsjson.NewObj()
	f.Set("proposal", jsjson.MakeObject(p))
	return jsjson.Stringify(jsjson.MakeObject(f))
}

func bytesDigest(b string) string {
	sum := sha256.Sum256([]byte(b))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// TestLiveMaterialize materializes the same commits through the oracle's
// materializeGitTree and gitinv.MaterializeGitTree, in turn under the same
// temporary root and name, and compares thrown messages, the worktree
// listings before and after frozen overlays, the overlay facts and the
// cleanup.
func TestLiveMaterialize(t *testing.T) {
	o := oracle.Start(t)
	defer o.Close()
	repo, commits := liveRepo(t)

	filtered, _ := liveRepo(t)
	if out, err := exec.Command("git", "-C", filtered, "config", "filter.x.clean", "cat").CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	attrs, _ := liveRepo(t)
	os.WriteFile(filepath.Join(attrs, ".gitattributes"), []byte("*.txt filter=x\n"), 0o644)
	exec.Command("git", "-C", attrs, "add", ".").Run()
	exec.Command("git", "-C", attrs, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "c").Run()
	attrsHead, _ := exec.Command("git", "-C", attrs, "rev-parse", "HEAD").Output()

	good := "export const a = 2;\n"
	overlaySets := map[string]string{
		"new file in new directory": liveOverlayWitness([3]string{"fresh/dir/x.mjs", "ZXhwb3J0IGNvbnN0IGEgPSAyOwo", bytesDigest(good)}),
		"existing path":             liveOverlayWitness([3]string{"src/deep/a.js", "ZXhwb3J0IGNvbnN0IGEgPSAyOwo=", bytesDigest(good)}, [3]string{"state.txt", "", bytesDigest("")}),
		"symlinked target":          liveOverlayWitness([3]string{"state.txt", "", bytesDigest("")}, [3]string{"link", "", bytesDigest("")}),
		"bad digest":                liveOverlayWitness([3]string{"state.txt", "eA==", bytesDigest("")}),
		"escape":                    liveOverlayWitness([3]string{"../escape.mjs", "", bytesDigest("")}),
		"parent is a file":          liveOverlayWitness([3]string{"state.txt/x", "", bytesDigest("")}),
		"backslash":                 liveOverlayWitness([3]string{`a\b`, "", bytesDigest("")}),
	}
	// A commit holding A.txt and a.txt (git mktree): on a case-insensitive
	// filesystem (APFS's default, macOS) checkout-index writes one file over
	// the other and update-index --refresh fails, so materialization
	// refuses with "Could not verify materialized Git tree". On Linux both
	// files check out.
	collide := func() string {
		gitIn := func(input string, args ...string) string {
			cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
			cmd.Stdin = strings.NewReader(input)
			cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("git %v: %v", args, err)
			}
			return strings.TrimSpace(string(out))
		}
		upper, lower := gitIn("upper\n", "hash-object", "-w", "--stdin"), gitIn("lower\n", "hash-object", "-w", "--stdin")
		tree := gitIn("100644 blob "+upper+"\tA.txt\n100644 blob "+lower+"\ta.txt\n", "mktree")
		return gitIn("", "commit-tree", tree, "-p", commits[1], "-m", "case collision")
	}()
	type materializeCase struct {
		name, repo, commit, overlays string
		withIndex, existing          bool
	}
	cases := []materializeCase{
		{name: "first commit", repo: repo, commit: commits[0]},
		{name: "second commit", repo: repo, commit: commits[1]},
		{name: "abbreviated commit", repo: repo, commit: commits[1][:12]},
		{name: "unknown commit", repo: repo, commit: strings.Repeat("0", 40)},
		{name: "missing repository", repo: filepath.Join(repo, "nope"), commit: commits[1]},
		{name: "filter config", repo: filtered, commit: commits[1]},
		{name: "attributes filter", repo: attrs, commit: strings.TrimSpace(string(attrsHead))},
		{name: "existing worktree", repo: repo, commit: commits[1], existing: true},
		{name: "case collision", repo: repo, commit: collide},
	}
	for name := range overlaySets {
		cases = append(cases, materializeCase{name: "overlay " + name, repo: repo, commit: commits[1], overlays: name, withIndex: true})
		cases = append(cases, materializeCase{name: "overlay without index " + name, repo: repo, commit: commits[1], overlays: name})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			const name = "state-0000-0123456789abcdef"
			if tc.existing {
				os.MkdirAll(filepath.Join(root, "worktrees", name), 0o755)
			}
			// TS first.
			req, _ := json.Marshal(map[string]any{"repository": tc.repo, "commit": tc.commit, "tempRoot": root, "name": name})
			res, err := o.Call("materializeGitTree", string(req))
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			var tsID *int
			if strings.HasPrefix(res, `"`) {
				var msg string
				json.Unmarshal([]byte(res), &msg)
				want = append(want, msg)
			} else {
				var tree struct {
					ID       int    `json:"id"`
					Worktree string `json:"worktree"`
				}
				json.Unmarshal([]byte(res), &tree)
				tsID = &tree.ID
				want = append(want, tree.Worktree, treeListing(t, tree.Worktree))
				if tc.overlays != "" {
					args := map[string]any{"worktree": tree.Worktree, "witness": overlaySets[tc.overlays]}
					if tc.withIndex {
						args["id"] = tree.ID
					}
					req, _ := json.Marshal(args)
					want = append(want, oracleText(t, o, "materializeFrozenOverlays", string(req)), treeListing(t, tree.Worktree))
				}
				req, _ := json.Marshal(map[string]any{"id": tree.ID})
				res, err := o.Call("cleanupGitTree", string(req))
				if err != nil {
					t.Fatal(err)
				}
				_, statErr := os.Lstat(tree.Worktree)
				want = append(want, res, fmt.Sprint(os.IsNotExist(statErr)), indexesLeft(t, root))
			}

			// Then Go, under the same root.
			var got []string
			tree, err := gitinv.MaterializeGitTree(tc.repo, tc.commit, root, name)
			if err != nil {
				got = append(got, "THREW:"+err.Error())
			} else {
				got = append(got, tree.Worktree, treeListing(t, tree.Worktree))
				if tc.overlays != "" {
					witness, _ := jsjson.Parse(overlaySets[tc.overlays])
					var index gitinv.OverlayIndexInspector
					if tc.withIndex {
						index = tree
					}
					got = append(got, threwOr(gitinv.MaterializeFrozenOverlays(tree.Worktree, witness, index)), treeListing(t, tree.Worktree))
				}
				result := "null"
				if err := tree.Cleanup(); err != nil {
					result = jsjson.Stringify(jsjson.MakeString(err.Error()))
				}
				_, statErr := os.Lstat(tree.Worktree)
				got = append(got, `{"result":`+result+`}`, fmt.Sprint(os.IsNotExist(statErr)), indexesLeft(t, root))
			}
			if (tsID == nil) != (len(got) == 1) || strings.Join(got, "\n---\n") != strings.Join(want, "\n---\n") {
				t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n---\n"), strings.Join(want, "\n---\n"))
			}
			recordOutput(t, strings.Join(want, "\n"))
		})
	}
	requireReached(t, "internal/gitinv/hardened.go", recordedOutputs(t))
	requireReached(t, "internal/gitinv/overlay.go", recordedOutputs(t))
	// Listed in unreachableLiterals for the platforms whose filesystem
	// tells A.txt from a.txt; on macOS this test must reach it.
	if runtime.GOOS == "darwin" && !strings.Contains(recordedOutputs(t), "Could not verify materialized Git tree") {
		t.Error(`the case collision did not reach "Could not verify materialized Git tree" on macOS`)
	}
}

// indexesLeft lists what cleanup left in <root>/indexes.
func indexesLeft(t *testing.T, root string) string {
	entries, err := os.ReadDir(filepath.Join(root, "indexes"))
	if err != nil {
		return "ERR:" + err.Error()
	}
	return fmt.Sprint(len(entries))
}
