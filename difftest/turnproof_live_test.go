//go:build faultline_test && unix

package difftest

import (
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/bundle/turnproof"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// catalogBytes generates one hashes.txt covering the kinds of line, line end
// and name the parser must tell apart.
func catalogBytes(r *rand.Rand) []byte {
	hexDigits := func(n int, upper bool) string {
		const lower, up = "0123456789abcdef", "0123456789ABCDEF"
		set := lower
		if upper {
			set = up
		}
		b := make([]byte, n)
		for i := range b {
			b[i] = set[r.IntN(16)]
		}
		return string(b)
	}
	segments := []string{"a", "b.json", "runs", "x y", "é", "中文", "witness", "frozen.json", "..", ".", "", "a\\b", "n\x00l", "tab\t"}
	name := func() string {
		if r.IntN(100) < 70 {
			n := 1 + r.IntN(3)
			parts := make([]string, n)
			for i := range parts {
				parts[i] = fmt.Sprintf("%s%d", []string{"a", "b", "runs", "witness", "é", "中"}[r.IntN(6)], r.IntN(4))
			}
			return strings.Join(parts, "/")
		}
		n := 1 + r.IntN(3)
		parts := make([]string, n)
		for i := range parts {
			parts[i] = segments[r.IntN(len(segments))]
		}
		s := strings.Join(parts, "/")
		switch r.IntN(8) {
		case 0:
			s = "/" + s
		case 1:
			s += "\r"
		case 2:
			s += "\u2028"
		case 3:
			s = "a\u2029b" + s
		case 4:
			s += " \u00a0\ufeff"
		}
		return s
	}
	var pool []string
	line := func() string {
		digest := hexDigits(64, false)
		sep := "  "
		n := name()
		switch r.IntN(20) {
		case 0:
			digest = hexDigits(64, true)
		case 1:
			digest = hexDigits(63, false)
		case 2:
			digest = hexDigits(65, false)
		case 3:
			sep = " "
		case 4:
			sep = "   "
		case 5:
			if len(pool) > 0 { // a duplicate, often with another digest
				n = pool[r.IntN(len(pool))]
				if r.IntN(2) == 0 {
					digest = strings.Repeat("0", 64)
				}
			}
		case 6:
			return ""
		case 7:
			n = ""
		}
		pool = append(pool, n)
		return digest + sep + n
	}
	var lines []string
	count := r.IntN(8)
	switch r.IntN(40) {
	case 0:
		count = 2047 + r.IntN(54)
	case 1:
		count = 0
	}
	for range count {
		lines = append(lines, line())
	}
	end := "\n"
	switch r.IntN(10) {
	case 0:
		end = ""
	case 1:
		end = "\n\n \t\n"
	case 2:
		end = "\r\n"
	case 3:
		end = "\n\u2028\ufeff"
	}
	sep := "\n"
	if r.IntN(12) == 0 {
		sep = "\r\n"
	}
	out := []byte(strings.Join(lines, sep) + end)
	if r.IntN(15) == 0 && len(out) > 0 { // invalid UTF-8, lone surrogates included
		bad := [][]byte{{0xff}, {0xed, 0xa0, 0x80}, {0xc3}, {0xf0, 0x9f}, {0xed, 0xb0, 0x80}}
		at := r.IntN(len(out) + 1)
		out = append(out[:at:at], append(append([]byte{}, bad[r.IntN(len(bad))]...), out[at:]...)...)
	}
	return out
}

func TestLiveTurnHashCatalog(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(41, 1))
	var outputs strings.Builder
	threw, withEntries := 0, 0
	for i := 0; i < 5000; i++ {
		in := catalogBytes(r)
		var errs []string
		var got jsjson.Value
		if err := jsexc.Try(func() {
			cat := turnproof.ParseHashCatalog(in, &errs)
			entries := make([]jsjson.Value, 0, len(cat.Keys))
			for _, k := range cat.Keys {
				entries = append(entries, jsjson.MakeArray([]jsjson.Value{jsjson.MakeString(k), jsjson.MakeString(cat.Digests[k])}))
			}
			errValues := make([]jsjson.Value, len(errs))
			for j, e := range errs {
				errValues[j] = jsjson.MakeString(e)
			}
			o := jsjson.NewObj()
			o.Set("errors", jsjson.MakeArray(errValues))
			o.Set("entries", jsjson.MakeArray(entries))
			got = jsjson.MakeObject(o)
		}); err != nil {
			t.Fatalf("catalog %d: Go threw %v", i, err)
		}
		args := `{"hex":"` + hex.EncodeToString(in) + `"}`
		want := oracleText(t, c, "parseTurnHashCatalog", args)
		outputs.WriteString(want + "\n")
		if jsjson.Stringify(got) != want {
			t.Fatalf("catalog %d (%q):\ngot  %s\nwant %s", i, in, jsjson.Stringify(got), want)
		}
		threw += len(errs)
		if len(got.Get("entries").Items()) > 0 {
			withEntries++
		}
	}
	liveSummary(t, "5000 generated catalogs compared, %d errors, %d with entries", threw, withEntries)
	requireReached(t, "internal/bundle/turnproof/catalog.go", outputs.String())
}

// gitRepo is one generated repository with its object ids.
type gitRepo struct {
	dir                 string
	trees               []string // tree ids, the empty tree among them
	commit, blob, other string   // a commit id, a blob id, a missing id
}

func buildRepo(t *testing.T, r *rand.Rand, root string, i int, sha256 bool) gitRepo {
	dir := filepath.Join(root, fmt.Sprintf("repo%d", i))
	env := append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=a@b", "GIT_COMMITTER_NAME=a", "GIT_COMMITTER_EMAIL=a@b",
		"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z")
	git := func(stdin string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = env
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	initArgs := []string{"init", "-q"}
	if sha256 {
		initArgs = append(initArgs, "--object-format=sha256")
	}
	git("", initArgs...)
	shared := git("shared\n", "hash-object", "-w", "--stdin")
	g := gitRepo{dir: dir, blob: shared}
	g.trees = append(g.trees, git("", "mktree"))
	n := 2 + r.IntN(5)
	var previous string
	for k := 0; k < n-1; k++ {
		own := git(fmt.Sprintf("own %d %d\n", i, k), "hash-object", "-w", "--stdin")
		entries := fmt.Sprintf("100644 blob %s\ta.txt\n100644 blob %s\tb%d.txt\n", shared, own, k)
		if r.IntN(2) == 0 && previous != "" {
			entries += fmt.Sprintf("040000 tree %s\tnested\n", previous)
		}
		previous = git(entries, "mktree")
		g.trees = append(g.trees, previous)
	}
	g.commit = git("", "commit-tree", "-m", "c", previous)
	if i%7 == 3 {
		// A damaged loose blob: rev-list does not read it, pack-objects does.
		loose := filepath.Join(dir, ".git", "objects", shared[:2], shared[2:])
		if err := os.Chmod(loose, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(loose, []byte("not a zlib stream"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	g.other = strings.Repeat("ab", len(shared)/2)
	return g
}

func TestLiveTurnTreesPack(t *testing.T) {
	const seed = "live-turn-trees-pack"
	c := oracle.StartSeeded(t, seed)
	defer c.Close()
	clock.ResetForTest(seed)
	// tsx reads the seeded Date while it starts: let it settle, then put the
	// Go clock at the oracle's tick (as TestLiveInvestigate does).
	oracleCall(t, c, "echo", "null")
	time.Sleep(time.Second)
	var tick int64
	if _, err := fmt.Sscan(oracleCall(t, c, "seededTick", "null"), &tick); err != nil {
		t.Fatal(err)
	}
	for range tick + 1 {
		clock.NowMillis()
	}

	// FAULTLINE_TURNPACK_CASES=first:end compares only repositories
	// [first, end), for local runs in pieces; each has its own seed, so a
	// window reproduces the same cases. CI runs all 200.
	const total = 200
	n, first := total, 0
	if window := os.Getenv("FAULTLINE_TURNPACK_CASES"); window != "" {
		a, b, ok := strings.Cut(window, ":")
		var errA, errB error
		first, errA = strconv.Atoi(a)
		n, errB = strconv.Atoi(b)
		if !ok || errA != nil || errB != nil || first < 0 || first >= n || n > total {
			t.Fatalf("FAULTLINE_TURNPACK_CASES=%q: want first:end with 0 <= first < end <= %d", window, total)
		}
	}
	full := first == 0 && n == total
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	built := map[int]gitRepo{}
	repoAt := func(i int) gitRepo {
		if g, ok := built[i]; ok {
			return g
		}
		g := buildRepo(t, rand.New(rand.NewPCG(42, uint64(i))), root, i, i%10 == 9)
		built[i] = g
		return g
	}
	quoteList := func(ids []string) string {
		parts := make([]string, len(ids))
		for i, id := range ids {
			parts[i] = jsjson.Quote(id)
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	leftover := func() int {
		names, err := nodefs.ReadDirNames(nodefs.Tmpdir())
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, name := range names {
			if strings.HasPrefix(name, "faultline-turn-proof-pack-") {
				n++
			}
		}
		return n
	}
	var outputs strings.Builder
	packs, verified, withErrors := 0, 0, 0
	for i := first; i < n; i++ {
		g := repoAt(i)
		r := rand.New(rand.NewPCG(43, uint64(i)))
		// 2 to 6 trees: mostly trees, sometimes a commit, a blob or a missing id.
		count := 2 + r.IntN(5)
		var trees []string
		for range count {
			switch r.IntN(14) {
			case 0:
				trees = append(trees, g.commit)
			case 1:
				trees = append(trees, g.blob)
			case 2:
				trees = append(trees, g.other)
			default:
				trees = append(trees, g.trees[r.IntN(len(g.trees))])
			}
		}
		wantPack := oracleText(t, c, "packTurnTrees", `{"repository":`+jsjson.Quote(g.dir)+`,"trees":`+quoteList(trees)+`}`)
		outputs.WriteString(wantPack[:min(len(wantPack), 200)] + "\n")
		gotPack, err := turnproof.PackTurnTrees(g.dir, trees)
		gotText := ""
		if err != nil {
			gotText = "THREW:" + jsexc.Message(err)
		} else {
			gotText = hex.EncodeToString(gotPack)
		}
		if gotText != wantPack {
			t.Fatalf("repo %d trees %v:\ngot  %.300s\nwant %.300s", i, trees, gotText, wantPack)
		}
		packs++

		// The packs to verify: the real one, then damaged ones.
		var candidates [][]byte
		if err == nil {
			candidates = append(candidates, gotPack)
			cut := gotPack[:r.IntN(len(gotPack))]
			flipped := append([]byte{}, gotPack...)
			flipped[r.IntN(len(flipped))] ^= byte(1 + r.IntN(255))
			candidates = append(candidates, cut, flipped)
		}
		other := repoAt((i + 1) % total)
		if p, perr := turnproof.PackTurnTrees(other.dir, other.trees[:1]); perr == nil {
			candidates = append(candidates, p)
		}
		candidates = append(candidates, nil)
		verifyTrees := trees
		if r.IntN(3) == 0 {
			verifyTrees = g.trees[:1+r.IntN(len(g.trees))]
		}
		for _, pack := range candidates {
			want := oracleText(t, c, "verifyTreesPack", `{"hex":"`+hex.EncodeToString(pack)+`","trees":`+quoteList(verifyTrees)+`}`)
			before := leftover()
			outputs.WriteString(want + "\n")
			var errs []string
			verr := turnproof.VerifyTreesPack(pack, verifyTrees, &errs)
			errValues := make([]jsjson.Value, len(errs))
			for j, e := range errs {
				errValues[j] = jsjson.MakeString(e)
			}
			threw := jsjson.MakeNull()
			if verr != nil {
				threw = jsjson.MakeString(jsexc.Message(verr))
			}
			o := jsjson.NewObj()
			o.Set("errors", jsjson.MakeArray(errValues))
			o.Set("threw", threw)
			o.Set("leftover", jsjson.MakeNumber(float64(leftover()-before)))
			if got := jsjson.Stringify(jsjson.MakeObject(o)); got != want {
				t.Fatalf("repo %d verify (%d bytes, trees %v):\ngot  %s\nwant %s", i, len(pack), verifyTrees, got, want)
			}
			verified++
			if len(errs) > 0 {
				withErrors++
			}
		}
	}
	liveSummary(t, "%d generated repositories, %d packs compared, %d verifications (%d with errors)", n-first, packs, verified, withErrors)
	if full {
		requireReached(t, "internal/bundle/turnproof/pack.go", outputs.String())
	}
}
