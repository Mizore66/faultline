//go:build faultline_test

package difftest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/snapgc"
)

// snapgcTree records how one generated tree is built, so it can be built
// twice (one copy per side): the entries, then the permission changes.
type snapgcTree struct {
	r       *rand.Rand
	ops     []func(root string)
	chmods  []func(root string)
	ids     []string // the tree's object ids
	nonRoot bool
	// manyRefs: ids[0] is stored and more than five files name it, so a
	// refusal lists "(+n more)".
	manyRefs bool
}

func (g *snapgcTree) at(root, rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

func (g *snapgcTree) dir(rel string) {
	g.ops = append(g.ops, func(root string) { os.MkdirAll(g.at(root, rel), 0o755) })
}

// writable reports whether path may be opened for writing: not a FIFO or
// other special file a path drawn twice may already be (opening a FIFO
// blocks), nor a link.
func writable(path string) bool {
	info, err := os.Lstat(path)
	return err != nil || info.Mode().IsRegular()
}

func (g *snapgcTree) file(rel string, data []byte) {
	g.ops = append(g.ops, func(root string) {
		os.MkdirAll(filepath.Dir(g.at(root, rel)), 0o755)
		if writable(g.at(root, rel)) {
			os.WriteFile(g.at(root, rel), data, 0o644)
		}
	})
}

// sparse is a file of size bytes that starts with prefix.
func (g *snapgcTree) sparse(rel string, prefix []byte, size int64) {
	g.ops = append(g.ops, func(root string) {
		os.MkdirAll(filepath.Dir(g.at(root, rel)), 0o755)
		if writable(g.at(root, rel)) {
			os.WriteFile(g.at(root, rel), prefix, 0o644)
			os.Truncate(g.at(root, rel), size)
		}
	})
}

func (g *snapgcTree) link(rel, target string) {
	g.ops = append(g.ops, func(root string) {
		os.MkdirAll(filepath.Dir(g.at(root, rel)), 0o755)
		os.Symlink(target, g.at(root, rel))
	})
}

func (g *snapgcTree) fifo(rel string) {
	g.ops = append(g.ops, func(root string) {
		os.MkdirAll(filepath.Dir(g.at(root, rel)), 0o755)
		mkfifo(g.at(root, rel))
	})
}

// chmod is applied after every entry exists; only a non-root user is
// refused by the modes it sets.
func (g *snapgcTree) chmod(rel string, mode os.FileMode) {
	if !g.nonRoot {
		return
	}
	g.chmods = append(g.chmods, func(root string) { os.Chmod(g.at(root, rel), mode) })
}

func (g *snapgcTree) build(root string) {
	for _, op := range g.ops {
		op(root)
	}
	for _, op := range g.chmods {
		op(root)
	}
}

func (g *snapgcTree) pick(items ...string) string { return items[g.r.IntN(len(items))] }

// caseOf writes hex in lower, upper or mixed case.
func (g *snapgcTree) caseOf(s string) string {
	switch g.r.IntN(5) {
	case 0:
		return strings.ToUpper(s)
	case 1:
		b := []byte(s)
		for i := range b {
			if g.r.IntN(2) == 0 {
				b[i] = strings.ToUpper(string(b[i]))[0]
			}
		}
		return string(b)
	}
	return s
}

// store fills an object store at rel: info/ and pack/ (with files, a nested
// directory), buckets of every kind and name shape, and loose names of
// every shape and kind.
func (g *snapgcTree) store(rel string) {
	g.dir(rel)
	if g.manyRefs {
		g.file(rel+"/"+g.ids[0][:2]+"/"+g.ids[0][2:], []byte("zlib"))
	}
	for _, sub := range []string{"info", "pack"} {
		if g.r.IntN(4) == 0 {
			continue
		}
		g.dir(rel + "/" + sub)
		for n := g.r.IntN(3); n > 0; n-- {
			g.file(rel+"/"+sub+"/"+g.pick("packs", "x.pack", "x.idx", "nested/deep/f"), []byte("p"))
		}
		if g.r.IntN(40) == 0 {
			g.dir(rel + "/" + sub + "/locked")
			g.file(rel+"/"+sub+"/locked/f", []byte("x"))
			g.chmod(rel+"/"+sub+"/locked", 0o000) // countEntriesRecursive throws
		}
	}
	for n := g.r.IntN(6); n > 0; n-- {
		id := g.ids[g.r.IntN(len(g.ids))]
		name := g.caseOf(id[:2])
		switch g.r.IntN(14) {
		case 0:
			name = g.pick("zz", "abc", "a", "g0", "info", "pack", "Info", "0x", "é0")
		case 1:
			name = id[:2] + g.pick(".tmp", " ", "")
		}
		bucket := rel + "/" + name
		switch g.r.IntN(20) {
		case 0: // a link to a real bucket elsewhere: not a directory by type
			g.dir("outside/bucket-" + name)
			g.file("outside/bucket-"+name+"/"+id[2:], []byte("o"))
			g.link(bucket, "../../../../outside/bucket-"+name)
			continue
		case 1:
			g.file(bucket, []byte("not a bucket"))
			continue
		case 2:
			g.fifo(bucket)
			continue
		}
		g.dir(bucket)
		for k := g.r.IntN(4); k > 0; k-- {
			other := g.ids[g.r.IntN(len(g.ids))]
			loose := g.caseOf(other[2:])
			switch g.r.IntN(12) {
			case 0:
				loose += g.pick(".tmp", ".TMP", ".tmpx")
			case 1:
				loose = g.caseOf(strings.Repeat(other, 2)[:62]) // a SHA-256 name
			case 2:
				loose = loose[:37]
			case 3:
				loose += "0"
			case 4:
				loose = loose[:36] + g.pick("zz", "é", "-_")
			}
			path := bucket + "/" + loose
			switch g.r.IntN(12) {
			case 0:
				g.dir(path)
				g.file(path+"/inner", []byte("i"))
			case 1:
				g.link(path, g.pick("missing", "../"+name))
			case 2:
				g.fifo(path)
			default:
				g.file(path, []byte("zlib"))
			}
		}
		switch g.r.IntN(50) {
		case 0:
			g.chmod(bucket, 0o000) // listing throws
		case 1:
			g.chmod(bucket, 0o555) // removal throws
		}
	}
}

// alternates writes .git/objects/info/alternates: absent, text of every
// shape (CRLF, blank and whitespace-only lines, JS whitespace JS trims, the
// entry repeated, other entries, invalid UTF-8), read-only, a link (to a
// file or dangling) or a directory.
func (g *snapgcTree) alternates() {
	const rel = "repo/.git/objects/info/alternates"
	switch g.r.IntN(12) {
	case 0:
		return
	case 1:
		g.file("outside/alternates", []byte("../faultline/objects\n"))
		g.link(rel, "../../../../outside/alternates")
		return
	case 2:
		g.link(rel, "missing-alternates")
		return
	case 3:
		g.dir(rel)
		return
	}
	var lines []string
	for n := g.r.IntN(5); n > 0; n-- {
		lines = append(lines, g.pick("../faultline/objects", "../faultline/objects", " ../faultline/objects\t", " ../faultline/objects\ufeff",
			"/x/objects", "  /y/objects ", "", "   ", "../faultline/objects/", "../FAULTLINE/objects", " ../faultline/objects", "é\xff"))
	}
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteString(g.pick("\n", "\r\n", "\n", "\r"))
		}
		b.WriteString(line)
	}
	b.WriteString(g.pick("", "\n", "\r\n", "\n\n"))
	g.file(rel, []byte(b.String()))
	if g.r.IntN(15) == 0 {
		g.chmod(rel, 0o444) // the rewrite's open throws
	}
}

// reference is a text that names id at a word boundary or not: a 64-hex
// run, a word character next to it, upper case, non-ASCII neighbours.
func (g *snapgcTree) reference(id string) string {
	id = g.caseOf(id)
	switch g.r.IntN(10) {
	case 0:
		return id + strings.Repeat("0", 24)
	case 1:
		return g.pick("_", "x", "0", "Z") + id
	case 2:
		return id + g.pick("g", "_", "1")
	case 3:
		return g.pick("é", "-", "\"", ":", "\n", " ") + id + g.pick("é", ".", "")
	}
	return id
}

// faultline fills repo/.faultline with files on every filter branch.
func (g *snapgcTree) faultline() {
	switch g.r.IntN(30) {
	case 0:
		return
	case 1:
		g.file("repo/.faultline", []byte("not a directory"))
		return
	case 2:
		g.dir("outside/fl")
		g.file("outside/fl/recordings/a.json", []byte(g.ids[0]))
		g.link("repo/.faultline", "../outside/fl")
		return
	}
	g.dir("repo/.faultline")
	if g.manyRefs {
		for n := 6 + g.r.IntN(5); n > 0; n-- {
			g.file("repo/.faultline/recordings/r"+strconv.Itoa(n)+g.pick(".json", ".txt", ""), []byte(g.pick("", " ", "\"")+g.caseOf(g.ids[0])))
		}
	}
	for n := g.r.IntN(9); n > 0; n-- {
		var parts []string
		for d := g.r.IntN(3); d > 0; d-- {
			parts = append(parts, g.pick("recordings", "turn-proof-bundles", "git-proof-bundles", "bundles", "proofs", "turn-proofs", "git-proofs",
				"Recordings", "x", "recordingsx", "sub", "PROOFS"))
		}
		parts = append(parts, g.pick("ledger", "LEDGER", "a-ledger", "ledger.txt", "investigation", "manifest", "MANIFEST", "ROOT.sha256", "root.SHA256",
			"hashes.txt", "HASHES.TXT", "a.json", "a.JSON", "notes.txt", "é.json", "b.ndjson", "x.json"))
		rel := "repo/.faultline/" + strings.Join(parts, "/")
		var body strings.Builder
		for k := g.r.IntN(4); k > 0; k-- {
			id := g.ids[g.r.IntN(len(g.ids))]
			if g.r.IntN(5) == 0 {
				id = randomRun(g.r, "0123456789abcdef", 40)
			}
			body.WriteString(g.pick("", " ", "{\"treeDigest\":\"", "tree ") + g.reference(id) + g.pick("", "\"}", " ", "\n"))
		}
		switch g.r.IntN(25) {
		case 0:
			g.link(rel, g.pick("../missing", "recordings"))
		case 1:
			g.fifo(rel)
		case 2:
			g.file(rel, []byte(body.String()))
			g.chmod(rel, 0o000) // unreadable: skipped
		case 3:
			if g.r.IntN(4) == 0 { // over (or at) 8 MiB, sparse
				g.sparse(rel, []byte(body.String()), 8*1024*1024+int64(g.r.IntN(2)))
				break
			}
			fallthrough
		default:
			g.file(rel, []byte(body.String()))
		}
	}
	if g.r.IntN(30) == 0 {
		g.file("repo/.faultline/locked/recordings/a.json", []byte(g.ids[0]))
		g.chmod("repo/.faultline/locked", 0o000) // a nested unreadable directory: ignored
	}
	if g.r.IntN(60) == 0 {
		g.chmod("repo/.faultline", 0o000) // the top one: thrown
	}
}

// snapgcTreeListing is oracle-snapgc.ts treeListing.
func snapgcTreeListing(root string) []string {
	var lines []string
	var walk func(rel string)
	walk = func(rel string) {
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			lines = append(lines, "X "+rel)
			return
		}
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		slices.SortFunc(names, jsstr.CompareUTF16)
		for _, name := range names {
			r := name
			if rel != "" {
				r = rel + "/" + name
			}
			path := filepath.Join(root, filepath.FromSlash(r))
			info, err := os.Lstat(path)
			if err != nil {
				lines = append(lines, "? "+r)
				continue
			}
			mode := strconv.FormatUint(uint64(info.Mode().Perm()), 8)
			switch {
			case info.Mode()&os.ModeSymlink != 0:
				target, _ := os.Readlink(path)
				lines = append(lines, "L "+r+" "+target)
			case info.IsDir():
				lines = append(lines, "D "+r+" "+mode)
				walk(r)
			case !info.Mode().IsRegular():
				lines = append(lines, "S "+r+" "+mode)
			default:
				digest := "-"
				if info.Size() <= 1024*1024 {
					if b, err := os.ReadFile(path); err == nil {
						sum := sha256.Sum256(b)
						digest = hex.EncodeToString(sum[:])
					} else {
						digest = "?"
					}
				}
				lines = append(lines, fmt.Sprintf("F %s %s %d %s", r, mode, info.Size(), digest))
			}
		}
	}
	walk("")
	return lines
}

// snapgcOutcome is oracle-snapgc.ts's JSON.stringify({ value } | { threw },
// tree?).
func snapgcOutcome(value jsjson.Value, err error, tree []string) string {
	o := jsjson.NewObj()
	if err != nil {
		o.Set("threw", jsjson.MakeString(jsexc.Message(err)))
	} else {
		o.Set("value", value)
	}
	if tree != nil {
		o.Set("tree", stringValues(tree))
	}
	return jsjson.Stringify(jsjson.MakeObject(o))
}

func TestLiveSnapshotGC(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(73, 54))
	nonRoot := os.Getuid() != 0
	counts := map[string]int{}
	const trees = 300
	for i := 0; i < trees; i++ {
		g := &snapgcTree{r: r, nonRoot: nonRoot, manyRefs: r.IntN(8) == 0}
		for n := 2 + r.IntN(5); n > 0; n-- {
			id := randomRun(r, "0123456789abcdef", 40)
			if len(g.ids) > 0 && r.IntN(3) == 0 { // same bucket as another id
				id = g.ids[0][:2] + id[2:]
			}
			g.ids = append(g.ids, id)
		}
		g.dir("repo")
		g.dir("outside")
		switch r.IntN(20) {
		case 0: // no store
			g.dir("repo/.git/objects")
		case 1:
			g.file("repo/.git/faultline/objects", []byte("not a store"))
		case 2:
			g.store("outside/store")
			g.dir("repo/.git/faultline")
			g.link("repo/.git/faultline/objects", "../../../outside/store")
		case 3: // a linked worktree: .git is a file
			g.file("repo/.git", []byte("gitdir: ../main/.git/worktrees/wt\n"))
		default:
			g.store("repo/.git/faultline/objects")
		}
		g.alternates()
		g.faultline()

		roots := [2]string{}
		for side := range roots {
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			roots[side] = dir
			t.Cleanup(func() { restoreWritable(dir) })
			g.build(dir)
		}
		ts, gor := roots[0], roots[1]
		check := func(op, args, got string) {
			t.Helper()
			want := strings.ReplaceAll(oracleText(t, c, op, strings.ReplaceAll(args, "<R>", ts)), ts, "<R>")
			got = strings.ReplaceAll(got, gor, "<R>")
			if got != want {
				t.Fatalf("tree %d: %s(%s):\ngot  %s\nwant %s", i, op, args, got, want)
			}
			recordOutput(t, want)
			if strings.HasPrefix(want, `{"threw"`) {
				counts[op+" threw"]++
			} else {
				counts[op]++
			}
		}

		listDir := pick(r, "repo/.git/faultline/objects", "repo/.git/faultline/objects", "repo/.git/faultline/objects/info", "outside/store", "missing")
		ids, err := snapgc.ListQuarantinedObjectIDs(filepath.Join(gor, filepath.FromSlash(listDir)))
		check("listQuarantinedObjectIds", jsjson.Stringify(jsjson.MustObject("dir", jsjson.MakeString("<R>/"+listDir))), snapgcOutcome(stringValues(ids), err, nil))

		var asked []string
		if r.IntN(8) != 0 {
			for _, id := range g.ids {
				switch r.IntN(6) {
				case 0:
				case 1:
					asked = append(asked, strings.ToUpper(id))
				default:
					asked = append(asked, id)
				}
			}
		}
		set := map[string]bool{}
		for _, id := range asked {
			set[id] = true
		}
		repo := pick(r, "repo", "repo", "repo", "repo/", "repo/.faultline", "repo/missing")
		refs, err := snapgc.FindReferences(filepath.Join(gor, filepath.FromSlash(repo)), set)
		check("findQuarantinedObjectReferences", jsjson.Stringify(jsjson.MustObject("repository", jsjson.MakeString("<R>/"+repo), "ids", stringValues(asked))),
			snapgcOutcome(stringValues(refs), err, nil))

		force := r.IntN(3) == 0
		res, err := snapgc.Purge(filepath.Join(gor, filepath.FromSlash(repo)), force)
		value := jsjson.MustObject("repositoryRoot", jsjson.MakeString(res.RepositoryRoot), "objectDirectory", jsjson.MakeString(res.ObjectDirectory),
			"removedEntries", jsjson.MakeNumber(float64(res.RemovedEntries)), "status", jsjson.MakeString(res.Status), "referencingPaths", stringValues(res.ReferencingPaths))
		args := jsjson.Stringify(jsjson.MustObject("repository", jsjson.MakeString("<R>/"+repo), "force", jsjson.MakeBool(force), "root", jsjson.MakeString("<R>")))
		// Both trees are listed after both purges ran.
		want := strings.ReplaceAll(oracleText(t, c, "purgeFaultlineSnapshotObjects", strings.ReplaceAll(args, "<R>", ts)), ts, "<R>")
		got := strings.ReplaceAll(snapgcOutcome(value, err, snapgcTreeListing(gor)), gor, "<R>")
		if got != want {
			t.Fatalf("tree %d: purgeFaultlineSnapshotObjects(%s):\ngot  %s\nwant %s", i, args, got, want)
		}
		recordOutput(t, want)
		switch {
		case strings.HasPrefix(want, `{"threw"`):
			counts["purge threw"]++
		case strings.Contains(want, `"status":"PURGED"`):
			counts["PURGED"]++
		default:
			counts["ALREADY_EMPTY"]++
		}
	}
	outputs := recordedOutputs(t)
	for _, msg := range []string{
		"Refusing to purge quarantined turn-snapshot objects", "(+", "Refusing to edit non-regular Git alternates file: ", `"status":"PURGED"`, `"status":"ALREADY_EMPTY"`,
		"ENOTDIR: not a directory, scandir",
	} {
		if !strings.Contains(outputs, msg) {
			t.Errorf("no generated tree produced %q", msg)
		}
	}
	if nonRoot {
		for _, msg := range []string{"EACCES: permission denied, scandir", "EACCES: permission denied, open"} {
			if !strings.Contains(outputs, msg) {
				t.Errorf("no generated tree produced %q", msg)
			}
		}
	}
	// gc.go is live-gated: Task 9 adds it to liveGatedFiles (with
	// internal/snapgc to gatedDirs); until then this test registers it for
	// its own requireReached only.
	const gated = "internal/snapgc/gc.go"
	if liveGatedFiles[gated] == "" {
		liveGatedFiles[gated] = "TestLiveSnapshotGC"
		defer delete(liveGatedFiles, gated)
	}
	requireReached(t, gated, outputs)
	liveSummary(t, "%d trees compared (each copied twice): %v", trees, counts)
}
