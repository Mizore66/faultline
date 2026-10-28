//go:build faultline_test && unix

package difftest

import (
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/gen"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/bundle/turnproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/schema"
	"github.com/Mizore66/faultline/internal/turninv"
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

// turnSemanticsInputs are the parsed (investigation, frozen witness, ledger)
// texts of one of Task 2's two turn bases.
type turnBaseTexts struct{ result, frozen, ledger string }

// resignTurnRuns rewrites runId (the digest of the run without it) on each
// run the way the recorder signs it, so a mutated run still passes the
// runId check and reaches the checks behind it.
func resignTurnRuns(result jsjson.Value) {
	for _, run := range result.Get("runs").Items() {
		if run.Kind() != jsjson.Object {
			continue
		}
		d, err := canonical.DigestJSON(bundle.Without(run, "runId"))
		if err == nil {
			run.Obj().Set("runId", jsjson.MakeString(d))
		}
	}
}

// setTurnEnum sets one schema-valid alternative of a field the semantics
// check: status, repository or ledgerDigest null, executionTrust, the
// homogeneity (with a runtimeMapping entry dropped).
func setTurnEnum(r *rand.Rand, text string) string {
	v, err := jsjson.Parse(text)
	if err != nil || v.Kind() != jsjson.Object {
		return text
	}
	set := func(o jsjson.Value, key string, value jsjson.Value) {
		if o.Kind() == jsjson.Object {
			o.Obj().Set(key, value)
		}
	}
	str := jsjson.MakeString
	switch r.IntN(6) {
	case 0:
		set(v, "status", str([]string{"INVALID_WITNESS", "NO_TURN_SNAPSHOTS", "EXECUTION_ERROR", "ENVIRONMENT_CHANGED", "DUPLICATE_TURN_SNAPSHOTS", "SANDBOX_UNAVAILABLE", "COMPLETED"}[r.IntN(7)]))
	case 1:
		set(v, "repository", jsjson.MakeNull())
	case 2:
		set(v, "ledgerDigest", jsjson.MakeNull())
	case 3:
		set(v.Get("proof"), "executionTrust", str([]string{"INJECTED_RUNNER", "UNSAFE_LOCAL"}[r.IntN(2)]))
	case 4:
		set(v, "environmentHomogeneity", str([]string{"HETEROGENEOUS", "EMPTY"}[r.IntN(2)]))
		if m := v.Get("runtimeMapping"); m.Kind() == jsjson.Object && r.IntN(2) == 0 {
			if keys := m.Obj().Keys(); len(keys) > 0 {
				m.Obj().Delete(keys[r.IntN(len(keys))])
			}
		}
	case 5:
		set(v.Get("proof"), "dockerIsolated", jsjson.MakeBool(r.IntN(2) == 0))
	}
	return jsjson.Stringify(v)
}

// mutateTurnResult applies one of: a leaf corrupted or swapped, runs or
// states reordered, runs re-signed.
func mutateTurnResult(r *rand.Rand, text string) string {
	switch r.IntN(8) {
	case 6, 7:
		return setTurnEnum(r, text)
	case 0, 1:
		return gen.CorruptLeaf(r, text)
	case 2, 3:
		return gen.SwapLeaf(r, text)
	}
	v, err := jsjson.Parse(text)
	if err != nil {
		return text
	}
	field := "runs"
	if r.IntN(2) == 0 {
		field = "states"
	}
	if items := v.Get(field).Items(); len(items) > 1 {
		i, j := r.IntN(len(items)), r.IntN(len(items))
		items[i], items[j] = items[j], items[i]
	}
	if r.IntN(2) == 0 {
		resignTurnRuns(v)
	}
	return jsjson.Stringify(v)
}

// TestLiveTurnSemantics compares validateTurnInvestigationProofSemantics and
// bindTurnLifecycle on 3,000 mutations of Task 2's two turn bases. Only
// inputs the schemas accept are compared (the verifier never gets others).
func TestLiveTurnSemantics(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(47, 7))
	var bases []turnBaseTexts
	for _, b := range []string{"turn-two-states", "turn-non-monotonic"} {
		s := seedTexts(t, b+"/investigation.json", b+"/witness/frozen.json", b+"/lifecycle/ledger.json")
		bases = append(bases, turnBaseTexts{s[0], s[1], s[2]})
	}
	var outputs strings.Builder
	compared, errorsSeen, bound, threw := 0, 0, 0, 0
	for i := 0; compared < 3000 && i < 30000; i++ {
		base := bases[r.IntN(len(bases))]
		resultText, frozenText := base.result, base.frozen
		for m := r.IntN(3); m >= 0; m-- {
			resultText = mutateTurnResult(r, resultText)
		}
		if r.IntN(4) == 0 {
			frozenText = gen.CorruptLeaf(r, frozenText)
		} else if r.IntN(8) == 0 {
			frozenText = gen.SwapLeaf(r, frozenText)
		}
		resultValue, err := jsjson.Parse(resultText)
		if err != nil {
			continue
		}
		result, _, ok := schema.Parse(turninv.ResultSchema, resultValue)
		if !ok {
			continue
		}
		frozenValue, err := jsjson.Parse(frozenText)
		if err != nil {
			continue
		}
		frozen, _, ok := schema.Parse(gitproof.FrozenWitnessSchema, frozenValue)
		if !ok {
			continue
		}
		compared++
		got := turnOutcome(semanticsValue(result, frozen), nil)
		want := oracleText(t, c, "validateTurnSemantics", `{"result":`+jsjson.Stringify(result)+`,"frozen":`+jsjson.Stringify(frozen)+`}`)
		if got != want {
			t.Fatalf("validateTurnSemantics(%s, %s):\ngot  %s\nwant %s", jsjson.Stringify(result), jsjson.Stringify(frozen), got, want)
		}
		outputs.WriteString(want + "\n")
		errorsSeen += len(semanticsValue(result, frozen).Items())

		// bindTurnLifecycle on the same result and a possibly corrupted ledger.
		ledgerText := base.ledger
		switch r.IntN(4) {
		case 0:
			ledgerText = gen.CorruptLeaf(r, ledgerText)
		case 1:
			ledgerText = gen.SwapLeaf(r, ledgerText)
		}
		ledgerValue, err := jsjson.Parse(ledgerText)
		if err != nil {
			continue
		}
		binding, ledger, bindErr := turnproof.BindTurnLifecycle(ledgerValue, result)
		if bindErr != nil {
			threw++
		} else {
			bound++
		}
		var gotBind jsjson.Value
		if bindErr == nil {
			gotBind = jsjson.MustObject("binding", binding, "ledger", ledger)
		}
		gotText := turnOutcome(gotBind, bindErr)
		wantBind := oracleText(t, c, "bindTurnLifecycle", `{"ledger":`+jsjson.Stringify(ledgerValue)+`,"result":`+jsjson.Stringify(result)+`}`)
		if gotText != wantBind {
			t.Fatalf("bindTurnLifecycle:\ngot  %s\nwant %s", gotText, wantBind)
		}
		outputs.WriteString(wantBind + "\n")
	}
	if compared < 3000 {
		t.Fatalf("only %d parsing inputs", compared)
	}
	liveSummary(t, "%d turn semantics inputs (%d messages), bindTurnLifecycle %d bound %d threw", compared, errorsSeen, bound, threw)
	requireReached(t, "internal/bundle/turnproof/semantics.go", outputs.String())
}

func semanticsValue(result, frozen jsjson.Value) jsjson.Value {
	errs := turnproof.ValidateProofSemantics(result, frozen)
	items := make([]jsjson.Value, len(errs))
	for i, e := range errs {
		items[i] = jsjson.MakeString(e)
	}
	return jsjson.MakeArray(items)
}

// tpwBuilt is one generated, promoted turn investigation with its frozen
// witness, ledger and the repository holding its trees.
type tpwBuilt struct {
	label            string
	observed, result jsjson.Value // the injected-runner result, and its nativeTurnFixture promotion
	frozen, ledger   jsjson.Value
	repository       string
}

// tpwSpec is TestLiveInvestigateTurns's generator (randomTurnSessionSpec)
// narrowed to sessions that complete under the injected runner: no
// refusals, materialization errors or heterogeneous environments.
func tpwSpec(i int) turnSessionSpec {
	r := rand.New(rand.NewPCG(83, uint64(i)))
	for {
		s := randomTurnSessionSpec(r)
		s.fake, s.injected, s.witness, s.image = "", true, "", gitinv.PinnedImageForTest
		s.noLedger, s.duplicate, s.missingAt, s.symlinkAt, s.filter = false, -1, -1, -1, false
		s.changeAt, s.mapping = 0, ""
		for k, v := range s.verdicts {
			if v == "broken" {
				s.verdicts[k] = "bad"
			}
		}
		// Only a result with a transition is a proof the writer accepts;
		// every eighth case keeps whatever pattern was drawn, to refuse.
		if len(s.verdicts) >= 2 && (i%8 == 7 || s.label == "pass-to-fail" || s.label == "non-monotonic") {
			return s
		}
	}
}

// tpwListing lists a tree: each entry's type, permission bits and content
// digest (a symlink's target).
func tpwListing(root string) string {
	var lines []string
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		rel, _ := filepath.Rel(root, path)
		info, lerr := os.Lstat(path)
		if err != nil || lerr != nil {
			lines = append(lines, rel+" ERR")
			return nil
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(path)
			lines = append(lines, fmt.Sprintf("%s L %o %s", rel, info.Mode().Perm(), target))
		case info.IsDir():
			lines = append(lines, fmt.Sprintf("%s D %o", rel, info.Mode().Perm()))
		case info.Mode().IsRegular():
			b, _ := os.ReadFile(path)
			lines = append(lines, fmt.Sprintf("%s F %o %s", rel, info.Mode().Perm(), canonical.SHA256HexBytes(b)))
		default:
			lines = append(lines, fmt.Sprintf("%s S %o", rel, info.Mode()))
		}
		return nil
	})
	return strings.Join(lines, "\n")
}

// tpwCase is one writer call, run on both sides.
type tpwCase struct {
	label, fake    string
	result, frozen jsjson.Value
	ledger         jsjson.Value // Undefined: the option is omitted
	repository     string
	generatedAt    *string
	codex          *gitproof.CodexThreadIDs
	// build creates the case's fixtures under a side's directory and
	// returns that side's output, proof root ("" for the default) and
	// working directory ("" to keep the current one).
	build func(dir string) (output, proofRoot, cwd string)
}

// TestLiveTurnProofWriter writes 80 generated turn investigations (the
// investigateTurnTrees generator, promoted to native Docker by the frozen
// fixtures.ts nativeTurnFixture) with frozen TS's
// writeTurnInvestigationProofBundle and the Go port, both seeded, into twin
// directories (ts/, go/). Case i runs variant i%16: proof and non-proof
// results with and without Codex ids and a pinned generatedAt, an existing
// output, an output outside the root, through a symlink or under a file,
// ledgers that do not bind (another ledger, a damaged one, none, null), a
// repository without the trees (the stage is removed), a fake git failing
// during assembly, invalid inputs and the default root under the working
// directory. It compares the result or error, the whole tree written (bytes
// and modes) and the recorded git argv (the roots normalized).
// FAULTLINE_TURNWRITE_CASES=first:end compares only cases [first, end) (each
// has its own generator seed); the counts and requireReached need all 80.
func TestLiveTurnProofWriter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the recorders and fake gits are sh scripts")
	}
	root := oracle.RepoRoot(t)
	oracle.Skip(t)
	setWriterUmask(t) // before the oracles start: they inherit the umask
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	const total = 80
	first, end := 0, total
	if window := os.Getenv("FAULTLINE_TURNWRITE_CASES"); window != "" {
		a, b, ok := strings.Cut(window, ":")
		var errA, errB error
		first, errA = strconv.Atoi(a)
		end, errB = strconv.Atoi(b)
		if !ok || errA != nil || errB != nil || first < 0 || first >= end || end > total {
			t.Fatalf("FAULTLINE_TURNWRITE_CASES=%q: want first:end with 0 <= first < end <= %d", window, total)
		}
	}
	full := first == 0 && end == total

	builder := oracle.Start(t) // unseeded: the ledgers and promotions read no clock
	defer builder.Close()
	clock.ResetForTest("live-turn-writer-witnesses")
	witnesses := []jsjson.Value{gitinv.FrozenWitnessForTest(t), gitinv.FrozenWitnessWithOverlaysForTest(t, []string{"notes.txt"}, []string{"overlay notes\n"})}
	clock.ResetForTest("live-turn-writer-investigations")
	built := map[int]*tpwBuilt{}
	for i := first; i < end; i++ {
		spec := tpwSpec(i)
		_, request := buildTurnSession(t, builder, spec, witnesses)
		request.Runner, request.Injected = turnStateRunner, true
		observed, err := turninv.InvestigateTurnTrees(request)
		if err != nil || observed.Get("status").Str() != "COMPLETED" {
			t.Fatalf("case %d (%s): the generated session must complete (%v)", i, spec.label, err)
		}
		ledgerText, err := os.ReadFile(request.LedgerPath)
		if err != nil {
			t.Fatal(err)
		}
		promoted := oracleText(t, builder, "promoteNativeTurn", `{"observed":`+jsjson.Stringify(observed)+`}`)
		if strings.HasPrefix(promoted, "THREW:") {
			t.Fatalf("case %d: %s", i, promoted)
		}
		built[i] = &tpwBuilt{label: fmt.Sprintf("%s %s n=%d %s", spec.label, spec.format, len(spec.verdicts), strings.Join(spec.verdicts, ",")),
			observed: observed, result: mustParseText(t, promoted), frozen: request.FrozenWitness, ledger: mustParseText(t, string(ledgerText)),
			repository: request.Repository}
	}

	const seed = "live-turn-writer"
	c := oracle.StartSeeded(t, seed)
	defer c.Close()
	clock.ResetForTest(seed)
	oracleCall(t, c, "echo", "null") // tsx's startup Date read (see TestLiveInvestigate)
	time.Sleep(time.Second)
	tick, err := strconv.ParseInt(oracleCall(t, c, "seededTick", "null"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	for range tick + 1 {
		clock.NowMillis()
	}
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tsRoot, goRoot := filepath.Join(work, "ts"), filepath.Join(work, "go")
	logs := filepath.Join(work, "logs")
	for _, d := range []string{tsRoot, goRoot, logs} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	emptyRepo := filepath.Join(work, "empty-repo") // holds none of the trees; both sides read it
	if err := os.Mkdir(emptyRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	turnGit(t, emptyRepo, "init", "-q")
	recorderTemplate, err := os.ReadFile(filepath.Join(root, "difftest/testdata/recorder/git"))
	if err != nil {
		t.Fatal(err)
	}
	// recorder copies the git recorder with its log= line naming logPath
	// (hardened git keeps only PATH from the environment).
	recorder := func(dir, logPath string) string {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		script := strings.Replace(string(recorderTemplate), "log=$FAULTLINE_ARGV_LOG\n", "log='"+logPath+"'\n", 1)
		if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	pathWith := func(rec, fake string) string {
		parts := []string{rec}
		if fake != "" {
			parts = append(parts, filepath.Join(root, "difftest/testdata/fakegit", fake))
		}
		return strings.Join(append(parts, filepath.Dir(gitPath), "/usr/bin", "/bin"), ":")
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
	n := 0
	statuses := map[string]int{}
	compare := func(i int, tc tpwCase) {
		name := strconv.Itoa(i) + "-" + strconv.Itoa(n)
		n++
		tsDir, goDir := filepath.Join(tsRoot, name), filepath.Join(goRoot, name)
		for _, d := range []string{tsDir, goDir} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		tsLog, goLog := filepath.Join(logs, name+"-ts.log"), filepath.Join(logs, name+"-go.log")
		tsPath := pathWith(recorder(filepath.Join(logs, name+"-ts-rec"), tsLog), tc.fake)
		goPath := pathWith(recorder(filepath.Join(logs, name+"-go-rec"), goLog), tc.fake)
		tsOutput, tsProofRoot, tsCwd := tc.build(tsDir)
		goOutput, goProofRoot, goCwd := tc.build(goDir)
		toGo := func(s string) string { return strings.ReplaceAll(s, tsRoot, goRoot) }
		if ts, goTick := oracleCall(t, c, "seededTick", "null"), clock.NowMillis()-seededEpochMillis; ts != strconv.FormatInt(goTick, 10) {
			t.Fatalf("case %s (%s): oracle clock at %s, Go clock at %d", name, tc.label, ts, goTick)
		}

		options := jsjson.NewObj()
		if tsProofRoot != "" {
			options.Set("proofRoot", jsjson.MakeString(tsProofRoot))
		}
		if tc.generatedAt != nil {
			options.Set("generatedAt", jsjson.MakeString(*tc.generatedAt))
		}
		options.Set("lifecycleLedger", tc.ledger)
		options.Set("repository", jsjson.MakeString(tc.repository))
		if tc.codex != nil {
			codex := jsjson.NewObj()
			if tc.codex.WitnessDraftThreadID != nil {
				codex.Set("witnessDraftThreadId", jsjson.MakeString(*tc.codex.WitnessDraftThreadID))
			}
			if tc.codex.RepairThreadID != nil {
				codex.Set("repairThreadId", jsjson.MakeString(*tc.codex.RepairThreadID))
			}
			options.Set("codex", jsjson.MakeObject(codex))
		}
		args := `{"output":` + jsjson.Quote(tsOutput) + `,"result":` + jsjson.Stringify(tc.result) + `,"frozen":` + jsjson.Stringify(tc.frozen) +
			`,"options":` + jsjson.Stringify(jsjson.MakeObject(options)) + `,"path":` + jsjson.Quote(tsPath) + `,"argvLog":` + jsjson.Quote(tsLog)
		if tsCwd != "" {
			args += `,"cwd":` + jsjson.Quote(tsCwd)
		}
		want := oracleText(t, c, "writeTurnInvestigationProofBundle", args+`}`)

		o := turnproof.WriteOptions{ProofRoot: goProofRoot, GeneratedAt: tc.generatedAt, LifecycleLedger: tc.ledger, Repository: tc.repository, Codex: tc.codex}
		t.Setenv("PATH", goPath)
		previous, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if goCwd != "" {
			if err := os.Chdir(goCwd); err != nil {
				t.Fatal(err)
			}
		}
		before := leftover()
		w, werr := turnproof.Write(goOutput, tc.result, tc.frozen, o)
		if err := os.Chdir(previous); err != nil {
			t.Fatal(err)
		}
		if after := leftover(); after != before {
			t.Fatalf("case %s (%s): Go left %d faultline-turn-proof-pack-* directories", name, tc.label, after-before)
		}
		var got string
		if werr != nil {
			got = "THREW:" + werr.Error()
		} else {
			view := jsjson.NewObj()
			view.Set("directory", jsjson.MakeString(w.Directory))
			view.Set("manifestDigest", jsjson.MakeString(w.ManifestDigest))
			view.Set("rootDigest", jsjson.MakeString(w.RootDigest))
			view.Set("manifest", w.Manifest)
			got = jsjson.Stringify(jsjson.MakeObject(view))
		}
		outputs.WriteString(jsjson.Quote(want) + "\n")
		if norm := toGo(want); got != norm {
			t.Fatalf("case %s (%s):\ngot  %s\nwant %s", name, tc.label, got, norm)
		}
		tsRecords, _ := os.ReadFile(tsLog)
		goRecords, _ := os.ReadFile(goLog)
		if norm := toGo(string(tsRecords)); norm != string(goRecords) {
			t.Fatalf("case %s (%s): git argv differs:\ngo %q\nts %q", name, tc.label, goRecords, norm)
		}
		tsList, goList := toGo(tpwListing(tsDir)), tpwListing(goDir)
		if tsList != goList {
			t.Fatalf("case %s (%s): trees differ:\ngo\n%s\nts\n%s", name, tc.label, goList, tsList)
		}
		kind := "written non-proof"
		if werr != nil {
			kind = got[:min(len(got), 70)]
			if i := strings.Index(kind, tsRoot); i >= 0 {
				kind = kind[:i]
			}
		} else if tc.result.Get("proof", "isProof").Bool() {
			kind = "written proof"
		}
		statuses[kind]++
		if tc.codex != nil && werr == nil && w.Manifest.Get("codex").Kind() == jsjson.Object {
			statuses["written with codex"]++
		}
		t.Logf("case %s (%s): %.110s", name, tc.label, kind)
	}

	for i := first; i < end; i++ {
		b := built[i]
		r := rand.New(rand.NewPCG(89, uint64(i)))
		variant := i % 16
		tc := tpwCase{label: fmt.Sprintf("v%d %s", variant, b.label), result: b.result, frozen: b.frozen, ledger: b.ledger, repository: b.repository}
		plain := func(dir string) (string, string, string) {
			return filepath.Join(dir, "root", "out"), filepath.Join(dir, "root"), ""
		}
		tc.build = plain
		stamp := "2026-07-16T11:04:00.000Z"
		switch variant {
		case 0: // the clock supplies generatedAt
		case 1:
			tc.generatedAt = &stamp
			draft, repair := "thread-draft", "thread:repair-1"
			tc.codex = &gitproof.CodexThreadIDs{WitnessDraftThreadID: &draft, RepairThreadID: &repair}
		case 2:
			tc.generatedAt = &stamp
			repair := "thread-repair"
			if r.IntN(4) == 0 {
				repair = "bad thread id!"
			}
			tc.codex = &gitproof.CodexThreadIDs{RepairThreadID: &repair}
		case 3:
			tc.codex = &gitproof.CodexThreadIDs{}
		case 4: // an existing output
			tc.build = func(dir string) (string, string, string) {
				out, rootDir, _ := plain(dir)
				if err := os.MkdirAll(out, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(out, "keep"), []byte("keep\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				return out, rootDir, ""
			}
		case 5: // outside the root
			tc.build = func(dir string) (string, string, string) {
				return filepath.Join(dir, "elsewhere", "out"), filepath.Join(dir, "root"), ""
			}
		case 6: // through a symbolic link
			tc.build = func(dir string) (string, string, string) {
				if err := os.MkdirAll(filepath.Join(dir, "root", "real"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("real", filepath.Join(dir, "root", "link")); err != nil {
					t.Fatal(err)
				}
				return filepath.Join(dir, "root", "link", "out"), filepath.Join(dir, "root"), ""
			}
		case 7: // under a file
			tc.build = func(dir string) (string, string, string) {
				if err := os.MkdirAll(filepath.Join(dir, "root"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "root", "file"), []byte("file\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				return filepath.Join(dir, "root", "file", "out"), filepath.Join(dir, "root"), ""
			}
		case 8: // a ledger that does not bind
			tc.generatedAt = &stamp
			switch r.IntN(4) {
			case 0: // another session's ledger
				for _, j := range []int{i + 16, i - 16, i + 1, i - 1} {
					if other := built[j]; other != nil {
						tc.ledger = other.ledger
						break
					}
				}
			case 1: // snapshots reordered or edited
				tc.ledger = cloneJSON(b.ledger)
				mutateTurnLedger(r, tc.ledger)
			case 2: // omitted
				tc.ledger = jsjson.Value{}
			case 3:
				tc.ledger = jsjson.MakeNull()
			}
		case 9: // the repository lacks the trees: the pack fails
			tc.repository = emptyRepo
		case 10: // the staged pack does not verify
			tc.fake, tc.generatedAt = "fail-unpack-index", &stamp
		case 11: // unpack-objects fails: index-pack verifies instead
			tc.fake, tc.generatedAt = "fail-unpack", &stamp
		case 12: // the verifier's temporary repository cannot be created
			tc.fake = "fail-init"
		case 13: // invalid inputs
			switch r.IntN(4) {
			case 0:
				tc.result = b.observed
			case 1:
				text := jsjson.Stringify(b.result)
				for m := r.IntN(2); m >= 0; m-- {
					text = mutateTurnResult(r, text)
				}
				tc.result = mustParseOrSelf(text, b.result)
			case 2:
				tc.frozen = mustParseOrSelf(gen.CorruptLeaf(r, jsjson.Stringify(b.frozen)), b.frozen)
			case 3:
				tc.frozen = witnesses[1]
			}
		case 14: // generatedAt that is not a timestamp (or an offset one)
			stamps := []string{"yesterday", "2026-07-16T11:04:00", "2026-13-45T00:00:00Z", "2026-07-16T11:04:00.000+02:00"}
			s := stamps[r.IntN(len(stamps))]
			tc.generatedAt = &s
		case 15: // the default root under the working directory
			tc.build = func(dir string) (string, string, string) {
				cwd := filepath.Join(dir, "work")
				if err := os.MkdirAll(cwd, 0o755); err != nil {
					t.Fatal(err)
				}
				return filepath.Join(cwd, ".faultline", "turn-proof-bundles", "out"), "", cwd
			}
			tc.generatedAt = &stamp
		}
		compare(i, tc)
	}
	liveSummary(t, "%d cases [%d, %d) compared on both sides, outcomes %v", end-first, first, end, statuses)
	if !full {
		// A window keeps the TS results it compared when
		// FAULTLINE_TURNWRITE_KEEP names a directory; the final run with
		// FAULTLINE_TURNWRITE_AGGREGATE=<that directory> checks that the
		// windows cover all 80 cases and runs requireReached on them.
		if dir := os.Getenv("FAULTLINE_TURNWRITE_KEEP"); dir != "" {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%02d-%02d.txt", first, end)), []byte(outputs.String()), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	for _, want := range []string{"written proof", "written with codex"} {
		if statuses[want] == 0 {
			t.Errorf("no case reached %q", want)
		}
	}
	requireReached(t, "internal/bundle/turnproof/write.go", outputs.String())
}

// TestLiveTurnProofWriterAggregate is the aggregate requireReached of
// TestLiveTurnProofWriter windows run apart (each under the 5 minute
// command limit): it needs FAULTLINE_TURNWRITE_AGGREGATE=<directory of the
// windows' FAULTLINE_TURNWRITE_KEEP files> and fails unless they cover
// cases [0, 80) exactly once.
func TestLiveTurnProofWriterAggregate(t *testing.T) {
	dir := os.Getenv("FAULTLINE_TURNWRITE_AGGREGATE")
	if dir == "" {
		t.Skip("set FAULTLINE_TURNWRITE_AGGREGATE=<directory of window outputs>")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var outputs strings.Builder
	next := 0
	for _, e := range entries { // sorted by name: first-end, zero padded
		var a, b int
		if _, err := fmt.Sscanf(e.Name(), "%02d-%02d.txt", &a, &b); err != nil || a != next {
			t.Fatalf("window file %s: want a window starting at case %d", e.Name(), next)
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		outputs.Write(raw)
		next = b
	}
	if next != 80 {
		t.Fatalf("the windows cover cases [0, %d), not [0, 80)", next)
	}
	requireReached(t, "internal/bundle/turnproof/write.go", outputs.String())
}
