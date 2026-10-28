//go:build faultline_test

package difftest

import (
	"encoding/base64"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/gen"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/incident"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// stringifiedOr renders a Go result like the incident oracle ops:
// JSON.stringify (key order kept), or "THREW:<message>".
func stringifiedOr(v jsjson.Value, err error) string {
	if err != nil {
		return "THREW:" + jsexc.Message(err)
	}
	return jsjson.Stringify(v)
}

const (
	loneSurrogate = "\xed\xa0\x80" // U+D800 in WTF-8
	astralChar    = "\U0001F600"   // two UTF-16 units
)

// incidentOddOneIn is how rarely incidentPick picks an odd value: half the
// draft inputs are mostly valid (1 in 40 per field) so that enough of them
// make a draft, the rest are at a boundary often (1 in 5).
var incidentOddOneIn = 5

func incidentPick[T any](r *rand.Rand, valid T, odd ...T) T {
	if len(odd) == 0 || r.IntN(incidentOddOneIn) != 0 {
		return valid
	}
	return odd[r.IntN(len(odd))]
}

// incidentRare is a 1-in-n structural change, scaled like incidentPick.
func incidentRare(r *rand.Rand, n int) bool { return r.IntN(n*incidentOddOneIn/5) == 0 }

func liveRevision(r *rand.Rand) string {
	return incidentPick(r, []string{"HEAD", "HEAD~1", strings.Repeat("a", 40), strings.Repeat("b", 40), "main", "x-"}[r.IntN(6)],
		"", "-x", "--", "a\nb", "a\rb", "a\x00b", strings.Repeat("r", 512), strings.Repeat("r", 513),
		strings.Repeat(astralChar, 256), strings.Repeat(astralChar, 256)+"r", loneSurrogate, " ", "\u2028")
}

func liveRevisionValue(r *rand.Rand) jsjson.Value {
	if incidentRare(r, 40) {
		return []jsjson.Value{jsjson.MakeNull(), jsjson.MakeNumber(1), jsjson.MakeArray(nil)}[r.IntN(3)]
	}
	return jsjson.MakeString(liveRevision(r))
}

func liveLocalHead(r *rand.Rand) jsjson.Value {
	if incidentRare(r, 15) {
		return []jsjson.Value{jsjson.MakeNull(), jsjson.MakeString("HEAD")}[r.IntN(2)]
	}
	n := incidentPick(r, 1, 0, 2, 17, 16)
	parents := make([]jsjson.Value, n)
	for i := range parents {
		parents[i] = liveRevisionValue(r)
	}
	o := jsjson.MustObject("head", liveRevisionValue(r), "parents", jsjson.MakeArray(parents))
	if incidentRare(r, 8) {
		switch r.IntN(3) {
		case 0:
			o.Obj().Set("upstream", jsjson.MakeString("origin/main"))
		case 1:
			o.Obj().Delete("parents")
		default:
			o.Obj().Set("parents", jsjson.MakeString("a"))
		}
	}
	return o
}

func liveExplicitRange(r *rand.Rand) jsjson.Value {
	if incidentRare(r, 15) {
		return []jsjson.Value{jsjson.MakeNull(), jsjson.MakeArray(nil)}[r.IntN(2)]
	}
	ancestor := liveRevisionValue(r)
	descendant := liveRevisionValue(r)
	if incidentRare(r, 8) {
		descendant = ancestor
	}
	o := jsjson.MustObject("ancestor", ancestor, "descendant", descendant)
	if incidentRare(r, 12) {
		if r.IntN(2) == 0 {
			o.Obj().Set("base", jsjson.MakeString("origin/main")) // not "source": a resolved range is no draft input
		} else {
			o.Obj().Delete("descendant")
		}
	}
	return o
}

func liveIncidentImage(r *rand.Rand) jsjson.Value {
	digest := "@sha256:" + strings.Repeat(string("0123456789abcdef"[r.IntN(16)]), 64)
	return jsjson.MakeString(incidentPick(r, []string{"node", "ghcr.io/x/y", "a:5000/b", "x.y_z/w"}[r.IntN(4)]+digest,
		"node:22"+digest, "a:5000/b:1"+digest, "node", "", "-x"+digest, "node@sha256:"+strings.Repeat("A", 64), "node"+digest+"x"))
}

func liveRuntime(r *rand.Rand) jsjson.Value {
	if incidentRare(r, 15) {
		if r.IntN(2) == 0 {
			return jsjson.MakeNull()
		}
		return jsjson.MustObject("requested", jsjson.MakeString("node"), "image", jsjson.MakeNumber(1))
	}
	o := jsjson.MustObject(
		"requested", jsjson.MakeString(incidentPick(r, []string{"node", "python", "go", "explicit"}[r.IntN(4)], "", strings.Repeat("n", 256), strings.Repeat("n", 257), loneSurrogate)),
		"image", liveIncidentImage(r))
	if incidentRare(r, 25) {
		o.Obj().Set("alias", jsjson.MakeString("node"))
	}
	return o
}

func liveIncidentDigest(r *rand.Rand) jsjson.Value {
	return jsjson.MakeString(incidentPick(r, "sha256:"+strings.Repeat(string("0123456789abcdef"[r.IntN(16)]), 64),
		"sha256:"+strings.Repeat("A", 64), "sha256:zz", "", "sha256:"+strings.Repeat("a", 63)))
}

func liveProposal(r *rand.Rand, id, command string) jsjson.Value {
	if incidentRare(r, 30) {
		return jsjson.MakeNull()
	}
	proposalID := id
	if incidentRare(r, 8) {
		proposalID = incidentPick(r, "other-id", "", "../x")
	}
	commandDigest := jsjson.MakeString(gitproof.CommandDigest(command))
	if incidentRare(r, 8) {
		commandDigest = liveIncidentDigest(r)
	}
	o := jsjson.MustObject("proposalId", jsjson.MakeString(proposalID), "proposalDigest", liveIncidentDigest(r),
		"incidentPacketDigest", liveIncidentDigest(r), "commandDigest", commandDigest)
	if incidentRare(r, 25) {
		o.Obj().Delete("incidentPacketDigest")
	}
	return o
}

// liveDraftInput builds one createIncidentDraft input: the Go DraftInput
// and the same object for TS. Each field is valid most of the time and
// otherwise at or past a schema boundary.
func liveDraftInput(r *rand.Rand) (incident.DraftInput, jsjson.Value) {
	in := incident.DraftInput{
		ID: incidentPick(r, []string{"first-incident", "incident-20260716120000000-0123456789ab", "a", "A.b_c-d"}[r.IntN(4)],
			"", ".a", "-a", "a/b", "a b", strings.Repeat("i", 128), strings.Repeat("i", 129), "é", "a"+loneSurrogate),
		Repository: incidentPick(r, []string{"/work/example", `C:\work\example`, "relative/repo", "/r"}[r.IntN(4)],
			"", " ", "\u00a0\t", "a\nb", "a\rb", "a\x00b", strings.Repeat("r", 16_384), strings.Repeat("r", 16_385),
			strings.Repeat(astralChar, 8192), strings.Repeat(astralChar, 8192)+"r", loneSurrogate, "\u3000x"),
		Command: incidentPick(r, []string{"pnpm test -- --runInBand", "go test ./...", "node witness.mjs", "echo 'é' && exit 1"}[r.IntN(4)],
			"", " \t\n", "\u3000\ufeff", "a\x00b", strings.Repeat("c", 32_000), strings.Repeat("c", 32_001),
			strings.Repeat(astralChar, 16_000), strings.Repeat(astralChar, 16_000)+"c", loneSurrogate, "x\r\ny"),
		CreatedAt: incidentPick(r, "", "2026-07-16T14:00:00.000Z", "2026-07-16T14:00:00+05:30", "2026-07-16T14:00:00Z",
			"2024-02-29T00:00:00Z", "2026-07-16T14:00:00", "2026-02-30T00:00:00Z", "nope", "2026-07-16T14:00:00.123456789-0800"),
	}
	switch incidentPick(r, r.IntN(2), 2, 3) {
	case 0:
		in.Range = liveExplicitRange(r)
	case 1:
		in.LocalHead = liveLocalHead(r)
	case 2:
		in.Range, in.LocalHead = liveExplicitRange(r), liveLocalHead(r)
	default:
		// neither: LOCAL_HEAD_REQUIRED
	}
	if incidentRare(r, 3) {
		in.Runtime = liveRuntime(r)
	}
	if incidentRare(r, 2) {
		in.Proposal = liveProposal(r, in.ID, in.Command)
	}
	o := jsjson.NewObj()
	o.Set("draftId", jsjson.MakeString(in.ID))
	if in.CreatedAt != "" {
		o.Set("createdAt", jsjson.MakeString(in.CreatedAt))
	}
	o.Set("repository", jsjson.MakeString(in.Repository))
	o.Set("command", jsjson.MakeString(in.Command))
	for _, f := range []struct {
		key string
		v   jsjson.Value
	}{{"range", in.Range}, {"localHead", in.LocalHead}, {"runtime", in.Runtime}, {"proposal", in.Proposal}} {
		if f.v.Kind() != jsjson.Undefined {
			o.Set(f.key, f.v)
		}
	}
	return in, jsjson.MakeObject(o)
}

// resignDraft recomputes draftDigest over the rest of the draft, so a
// schema-valid change reaches the digest comparison as a valid draft.
func resignDraft(t *testing.T, text string) string {
	v, err := jsjson.Parse(text)
	if err != nil || v.Kind() != jsjson.Object {
		return text
	}
	unsigned := jsjson.NewObj()
	for _, k := range v.Obj().Keys() {
		if k != "draftDigest" {
			unsigned.Set(k, v.Obj().Field(k))
		}
	}
	digest, err := canonical.DigestJSON(jsjson.MakeObject(unsigned))
	if err != nil {
		t.Fatal(err)
	}
	v.Obj().Set("draftDigest", jsjson.MakeString(digest))
	return jsjson.Stringify(v)
}

// tamperReview rewrites the review member: the union's two options and
// the ways a value fails both.
func tamperReview(r *rand.Rand, text string) string {
	v, _ := jsjson.Parse(text)
	review := v.Get("review")
	if review.Kind() != jsjson.Object {
		return text
	}
	switch r.IntN(7) {
	case 0:
		review.Obj().Set("witnessState", jsjson.MakeString("PROPOSED"))
	case 1:
		review.Obj().Set("witnessState", jsjson.MakeString("NOT_PROPOSED"))
	case 2:
		review.Obj().Set("autoFreeze", jsjson.MakeBool(true))
	case 3:
		review.Obj().Set("proposal", jsjson.MustObject("proposalId", jsjson.MakeString("x")))
	case 4:
		review.Obj().Delete("proposal")
	case 5:
		review.Obj().Set("extra", jsjson.MakeNumber(1))
	default:
		v.Obj().Set("review", []jsjson.Value{jsjson.MakeNull(), jsjson.MakeString("x"), jsjson.MakeArray(nil), jsjson.MakeObject(jsjson.NewObj())}[r.IntN(4)])
	}
	return jsjson.Stringify(v)
}

func TestLiveIncidentDraft(t *testing.T) {
	const seed = "live-incident-draft"
	c := oracle.StartSeeded(t, seed)
	defer c.Close()
	clock.ResetForTest(seed)
	// tsx reads the seeded Date once while it starts, from an immediate, so
	// let it run before syncing the Go clock with the oracle's; seededTick's
	// own read is counted.
	oracleCall(t, c, "echo", "null")
	time.Sleep(time.Second)
	tick, err := strconv.ParseInt(oracleCall(t, c, "seededTick", "null"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	for range tick + 1 {
		clock.NowMillis()
	}
	syncCheck := func(where string) {
		if ts, goTick := oracleCall(t, c, "seededTick", "null"), clock.NowMillis()-seededEpochMillis; ts != strconv.FormatInt(goTick, 10) {
			t.Fatalf("%s: oracle clock at %s, Go clock at %d", where, ts, goTick)
		}
	}
	r := rand.New(rand.NewPCG(32, 2))

	var drafts []string // created drafts (JSON.stringify), for the later phases
	for i := 0; i < 1500; i++ {
		if i%100 == 0 {
			syncCheck(fmt.Sprintf("draft %d", i))
		}
		incidentOddOneIn = []int{5, 40}[i%2]
		in, raw := liveDraftInput(r)
		draft, err := incident.CreateDraft(in)
		got := stringifiedOr(draft, err)
		liveCheck(t, c, "createIncidentDraft", `{"input":`+jsjson.Stringify(raw)+"}", got)
		if err == nil {
			drafts = append(drafts, got)
		}
	}
	incidentOddOneIn = 5
	syncCheck("after drafts")
	if len(drafts) < 300 {
		t.Fatalf("only %d of 1500 inputs made a draft", len(drafts))
	}

	for i := 0; i < 300; i++ {
		text := drafts[r.IntN(len(drafts))]
		switch r.IntN(6) {
		case 0:
			text = gen.CorruptDeep(r, text)
		case 1:
			text = gen.CorruptLeaf(r, text)
		case 2:
			text = resignDraft(t, gen.SwapLeaf(r, text))
		case 3:
			text = tamperReview(r, text)
		case 4:
			text = resignDraft(t, gen.CorruptLeaf(r, text))
		}
		v, _ := jsjson.Parse(text)
		result, err := incident.VerifyDraft(v)
		liveCheck(t, c, "verifyIncidentDraft", `{"text":`+jsjson.Quote(text)+"}", stringifiedOr(result, err))
	}

	for i := 0; i < 300; i++ {
		head := liveRevision(r)
		n := []int{0, 1, 1, 1, 2, 17}[r.IntN(6)]
		parents := make([]string, n)
		for j := range parents {
			parents[j] = liveRevision(r)
		}
		from, to := "", ""
		if r.IntN(3) == 0 {
			from, to = liveRevision(r), liveRevision(r)
			if r.IntN(4) == 0 {
				to = from
			}
		}
		// The facts ResolveRange passes (src/cli-app.ts:619-621).
		facts := jsjson.NewObj()
		if from != "" || to != "" {
			facts.Set("range", jsjson.MustObject("ancestor", jsjson.MakeString(from), "descendant", jsjson.MakeString(to)))
		} else {
			facts.Set("localHead", incident.LocalHeadFacts(head, parents))
		}
		var explicit *incident.ExplicitRange
		if from != "" || to != "" {
			explicit = &incident.ExplicitRange{Ancestor: from, Descendant: to}
		}
		resolved, err := incident.ResolveRange(head, parents, explicit)
		liveCheck(t, c, "resolveIncidentRange", `{"facts":`+jsjson.Stringify(jsjson.MakeObject(facts))+"}", stringifiedOr(resolved, err))
	}
	// Facts the CLI never builds: neither member, and both.
	for _, facts := range []string{`{}`, `{"range":{"ancestor":"a","descendant":"b"},"localHead":{"head":"c","parents":[]}}`} {
		oracleText(t, c, "resolveIncidentRange", `{"facts":`+facts+"}")
	}

	liveIncidentStore(t, c, r, drafts)

	outputs := recordedOutputs(t)
	for _, file := range []string{"internal/incident/schemas.go", "internal/incident/draft.go", "internal/incident/store.go"} {
		requireReached(t, file, outputs)
	}
	liveSummary(t, "%d drafts created of 1500 inputs, 300 tampered drafts verified, 300 ranges resolved", len(drafts))
}

// incidentTree lists dir like the incidentStoreRoundTrip op: one line per
// entry, "path|kind|mode" plus "|base64" for files, in sorted order.
func incidentTree(dir, rel string, lines *[]string) {
	names, err := os.ReadDir(filepath.Join(dir, rel))
	if err != nil {
		return
	}
	var sorted []string
	for _, n := range names {
		sorted = append(sorted, n.Name())
	}
	sort.Strings(sorted)
	for _, name := range sorted {
		child := name
		if rel != "" {
			child = rel + "/" + name
		}
		info, err := os.Lstat(filepath.Join(dir, child))
		if err != nil {
			continue
		}
		kind := "f"
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			kind = "l"
		case info.IsDir():
			kind = "d"
		}
		line := fmt.Sprintf("%s|%s|%o", child, kind, info.Mode().Perm())
		if kind == "f" {
			data, _ := os.ReadFile(filepath.Join(dir, child))
			line += "|" + base64.StdEncoding.EncodeToString(data)
		}
		*lines = append(*lines, line)
		if kind == "d" {
			if _, err := os.ReadDir(filepath.Join(dir, child)); err != nil {
				*lines = append(*lines, child+"|unreadable")
			} else {
				incidentTree(dir, child, lines)
			}
		}
	}
}

type incidentStoreStep struct {
	write    bool
	store    string // relative to the case directory
	text, id string
}

// liveIncidentStore compares writeIncidentDraft and readIncidentDraft on
// generated store layouts: each case runs the same steps in a Go and a TS
// directory set up alike, and compares the outcomes and the trees.
func liveIncidentStore(t *testing.T, c *oracle.Client, r *rand.Rand, drafts []string) {
	root := t.TempDir()
	for i := 0; i < 250; i++ {
		text := drafts[r.IntN(len(drafts))]
		v, _ := jsjson.Parse(text)
		id := v.Get("draftId").Str()
		other := drafts[r.IntN(len(drafts))]
		setup := r.IntN(12)
		staleValue, _ := jsjson.Parse(text)
		staleValue.Obj().Set("draftDigest", jsjson.MakeString("sha256:"+strings.Repeat("f", 64)))
		stale := jsjson.Stringify(staleValue)
		var steps []incidentStoreStep
		store := ".faultline/incidents"
		if r.IntN(10) == 0 {
			store = "sub/../.faultline/incidents"
		}
		for n := 1 + r.IntN(3); n > 0; n-- {
			switch r.IntN(6) {
			case 0, 1:
				steps = append(steps, incidentStoreStep{write: true, store: store, text: text})
			case 2:
				tampered := text
				switch r.IntN(3) {
				case 0:
					tampered = gen.CorruptDeep(r, text)
				case 1:
					tampered = gen.CorruptLeaf(r, text)
				default:
					tampered = resignDraft(t, gen.CorruptLeaf(r, text))
				}
				steps = append(steps, incidentStoreStep{write: true, store: store, text: tampered})
			default:
				readID := incidentPick(r, id, "missing", "../x", "", "renamed", "link")
				if setup == 6 {
					readID = []string{id, "missing", "renamed", "link"}[r.IntN(4)]
				}
				steps = append(steps, incidentStoreStep{store: store, id: readID})
			}
		}
		var sides [2]string
		var writable []string
		for side := range sides {
			dir := filepath.Join(root, fmt.Sprintf("%d-%d", i, side))
			sides[side] = dir
			drafts := filepath.Join(dir, ".faultline", "incidents", "drafts")
			must := func(err error) {
				if err != nil {
					t.Fatal(err)
				}
			}
			must(os.MkdirAll(dir, 0o755))
			switch setup {
			case 1: // a symlinked .faultline
				must(os.Mkdir(filepath.Join(dir, "outside"), 0o755))
				must(os.Symlink("outside", filepath.Join(dir, ".faultline")))
			case 2: // the store is a file
				must(os.MkdirAll(filepath.Join(dir, ".faultline"), 0o755))
				must(os.WriteFile(filepath.Join(dir, ".faultline", "incidents"), []byte("x"), 0o644))
			case 3: // a symlinked drafts directory
				must(os.MkdirAll(filepath.Join(dir, ".faultline", "incidents", "real"), 0o755))
				must(os.Symlink("real", drafts))
			case 4: // the draft already exists
				must(os.MkdirAll(drafts, 0o755))
				must(os.WriteFile(filepath.Join(drafts, id+".json"), []byte(text), 0o644))
			case 5: // the draft path is a directory
				must(os.MkdirAll(filepath.Join(drafts, id+".json"), 0o755))
			case 6: // stored drafts under other names: a link, a renamed copy, bad JSON
				must(os.MkdirAll(drafts, 0o755))
				must(os.WriteFile(filepath.Join(drafts, "renamed.json"), []byte(other), 0o600))
				must(os.Symlink("renamed.json", filepath.Join(drafts, "link.json")))
				must(os.WriteFile(filepath.Join(drafts, "missing.json"), []byte(other[:len(other)/2]), 0o600))
			case 7: // a stored draft that fails verification: a schema-valid digest of something else
				must(os.MkdirAll(drafts, 0o755))
				must(os.WriteFile(filepath.Join(drafts, id+".json"), []byte(stale), 0o600))
			case 8: // an unwritable drafts directory
				if os.Getuid() != 0 {
					must(os.MkdirAll(drafts, 0o755))
					must(os.Chmod(drafts, 0o500))
					writable = append(writable, drafts)
				}
			case 9: // the .faultline segment is a dangling link
				must(os.Symlink("nowhere", filepath.Join(dir, ".faultline")))
			}
		}

		goDir := sides[0]
		real, err := filepath.EvalSymlinks(goDir)
		if err != nil {
			t.Fatal(err)
		}
		mask := func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, real, "<D>"), goDir, "<D>") }
		var lines []string
		var items []jsjson.Value
		for _, step := range steps {
			goStore := goDir + "/" + step.store
			tsStore := sides[1] + "/" + step.store
			if step.write {
				items = append(items, jsjson.MustObject("kind", jsjson.MakeString("write"), "store", jsjson.MakeString(tsStore), "text", jsjson.MakeString(step.text)))
				v, _ := jsjson.Parse(step.text)
				path, err := incident.WriteDraft(goStore, v)
				if err != nil {
					lines = append(lines, "THREW:"+mask(jsexc.Message(err)))
				} else {
					lines = append(lines, "OK:"+mask(path))
				}
				continue
			}
			items = append(items, jsjson.MustObject("kind", jsjson.MakeString("read"), "store", jsjson.MakeString(tsStore), "id", jsjson.MakeString(step.id)))
			draft, path, err := incident.ReadDraftPath(goStore, step.id)
			if err != nil {
				lines = append(lines, "THREW:"+mask(jsexc.Message(err)))
			} else {
				lines = append(lines, "OK:"+mask(path)+":"+canonicalOf(draft))
			}
		}
		incidentTree(goDir, "", &lines)
		args := `{"dir":` + jsjson.Quote(sides[1]) + `,"steps":` + jsjson.Stringify(jsjson.MakeArray(items)) + "}"
		liveCheck(t, c, "incidentStoreRoundTrip", args, strings.Join(lines, "\n"))
		for _, d := range writable {
			os.Chmod(d, 0o755)
		}
	}
}
