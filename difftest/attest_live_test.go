//go:build faultline_test && unix

package difftest

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/attest"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsdate"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// Slice 4c, Task 7: internal/attest (src/attestation.ts) and internal/jsdate
// against frozen TS.

var attestDigest = "sha256:" + strings.Repeat("a", 64)

// attestDigests are digest boundary values: valid ones and each way the
// sha256:<64-lowercase-hex> regex refuses.
var attestDigests = []string{
	attestDigest, "sha256:" + strings.Repeat("0", 64), "sha256:" + strings.Repeat("f", 64),
	"sha256:" + strings.Repeat("A", 64), "sha256:" + strings.Repeat("a", 63), "sha256:" + strings.Repeat("a", 65),
	"sha256:" + strings.Repeat("g", 64), strings.Repeat("a", 64), "", "sha256:", "SHA256:" + strings.Repeat("a", 64),
	" sha256:" + strings.Repeat("a", 64), "sha256:" + strings.Repeat("a", 64) + "\n",
}

// attestReceiptID is a receipt id at or around each boundary of
// ^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$.
func attestReceiptID(r *rand.Rand) string {
	return []string{
		"r1", "a", "Z", "0", strings.Repeat("a", 128), strings.Repeat("b", 129), "a" + strings.Repeat("_-", 63) + "x",
		"", "-a", "_a", "a.b", "a/b", "..", "../x", "a\\b", "a b", "é", "a" + loneSurrogate, "a\n", "receipt-" + strconv.Itoa(r.IntN(1000)),
		"A_b-C", "a:b", "\u0000",
	}[r.IntN(23)]
}

// attestIdentifier is a subject or issuer at the IdentifierSchema
// boundaries: min 1, max 512 UTF-16 units, the trim refine and the control
// character refine, alone and together.
func attestIdentifier(r *rand.Rand) string {
	pieces := []string{"x", "subject", "é", astralChar, loneSurrogate, " ", "\t", "\u00a0", "\u2028", "\ufeff", "\u3000",
		"\u0000", "\u001f", "\u007f", "\u0080", "\n", "a b", "\u180e", "\u200b"}
	switch r.IntN(12) {
	case 0:
		return ""
	case 1:
		return strings.Repeat("y", 511+r.IntN(3))
	case 2: // an astral character across the 512-unit limit
		return strings.Repeat("y", 510+r.IntN(2)) + astralChar
	case 3: // too long, untrimmed and with a control character: three issues
		return " " + strings.Repeat("z", 513) + "\u0001"
	case 4:
		return []string{" ", "\t", "\u0007", " \u0001", "\u00a0x", "x\u2029"}[r.IntN(6)]
	}
	var b strings.Builder
	for n := 1 + r.IntN(5); n > 0; n-- {
		b.WriteString(pieces[r.IntN(len(pieces))])
	}
	return b.String()
}

func attestOptionalDigest(r *rand.Rand) *string {
	if r.IntN(2) == 0 {
		return nil
	}
	d := attestDigests[r.IntN(len(attestDigests))]
	return &d
}

func attestInput(r *rand.Rand) attest.CreationInput {
	in := attest.CreationInput{ReceiptID: "r1", Subject: "subject", Issuer: "issuer", BundleRootDigest: attestDigest}
	if r.IntN(3) == 0 {
		return in
	}
	if r.IntN(3) == 0 {
		in.ReceiptID = attestReceiptID(r)
	}
	if r.IntN(3) == 0 {
		in.Subject = attestIdentifier(r)
	}
	if r.IntN(3) == 0 {
		in.Issuer = attestIdentifier(r)
	}
	if r.IntN(4) == 0 {
		in.BundleRootDigest = attestDigests[r.IntN(len(attestDigests))]
	}
	in.SourceDigest, in.GitCheckpointDigest, in.WitnessDigest = attestOptionalDigest(r), attestOptionalDigest(r), attestOptionalDigest(r)
	return in
}

// attestInputValue is the input object TS receives: an absent member is no key.
func attestInputValue(in attest.CreationInput) jsjson.Value {
	o := jsjson.NewObj()
	o.Set("receiptId", jsjson.MakeString(in.ReceiptID))
	o.Set("subject", jsjson.MakeString(in.Subject))
	o.Set("issuer", jsjson.MakeString(in.Issuer))
	o.Set("bundleRootDigest", jsjson.MakeString(in.BundleRootDigest))
	for _, f := range []struct {
		k string
		v *string
	}{{"sourceDigest", in.SourceDigest}, {"gitCheckpointDigest", in.GitCheckpointDigest}, {"witnessDigest", in.WitnessDigest}} {
		if f.v != nil {
			o.Set(f.k, jsjson.MakeString(*f.v))
		}
	}
	return jsjson.MakeObject(o)
}

// attestTimestamps are issuedAt values: canonical, and each way the regex or
// the round-trip refine (or both) refuses one.
var attestTimestamps = []string{
	"2026-07-16T12:00:00.000Z", "2024-02-29T23:59:59.999Z", "0000-01-01T00:00:00.000Z", "9999-12-31T23:59:59.999Z",
	"2026-02-29T00:00:00.000Z", "2026-02-30T00:00:00.000Z", "2026-01-01T24:00:00.000Z", "2026-01-31T23:59:60.000Z",
	"2026-13-01T00:00:00.000Z", "2026-07-16T12:00:00Z", "2026-07-16T12:00:00.000+00:00", "2026-07-16", "",
	"+275760-09-13T00:00:00.000Z", "+275760-09-13T00:00:00.001Z", "-271821-04-20T00:00:00.000Z", "+010000-01-01T00:00:00.000Z",
	"+002026-07-16T12:00:00.000Z", "-000000-01-01T00:00:00.000Z", "-000001-01-01T00:00:00.000Z", "2026-07-16T12:00:00.0000Z",
	"2026-07-16t12:00:00.000z", " 2026-07-16T12:00:00.000Z", "July 16 2026",
}

// mutateAttestation changes a receipt the ways a stored one can be edited.
func mutateAttestation(r *rand.Rand, record jsjson.Value) jsjson.Value {
	o := record.Obj()
	keys := o.Keys()
	switch r.IntN(14) {
	case 0:
		o.Delete(keys[r.IntN(len(keys))])
	case 1:
		o.Set([]string{"extra", "signature", "0", "__proto__"}[r.IntN(4)], jsjson.MakeString("x"))
	case 2:
		o.Set(keys[r.IntN(len(keys))], []jsjson.Value{jsjson.MakeNull(), jsjson.MakeNumber(1), jsjson.MakeBool(true), jsjson.MakeArray(nil), jsjson.MustObject()}[r.IntN(5)])
	case 3, 4:
		o.Set("issuedAt", jsjson.MakeString(attestTimestamps[r.IntN(len(attestTimestamps))]))
	case 5:
		o.Set([]string{"subject", "issuer"}[r.IntN(2)], jsjson.MakeString(attestIdentifier(r)))
	case 6:
		o.Set("receiptId", jsjson.MakeString(attestReceiptID(r)))
	case 7:
		o.Set([]string{"bundleRootDigest", "sourceDigest", "gitCheckpointDigest", "witnessDigest", "receiptDigest"}[r.IntN(5)],
			jsjson.MakeString(attestDigests[r.IntN(len(attestDigests))]))
	case 8:
		o.Set([]string{"schemaVersion", "attestationKind", "limitation"}[r.IntN(3)], jsjson.MakeString("other"))
	case 9: // an edit that recomputes the stored checksum
		o.Set("subject", jsjson.MakeString("edited"))
		unsigned := jsjson.NewObj()
		for _, k := range o.Keys() {
			if k != "receiptDigest" {
				unsigned.Set(k, o.Field(k))
			}
		}
		o.Set("receiptDigest", jsjson.MakeString(jsexc.Must(canonical.DigestJSON(jsjson.MakeObject(unsigned)))))
	case 10:
		return []jsjson.Value{jsjson.MakeNull(), jsjson.MakeString("x"), jsjson.MakeArray(nil), jsjson.MakeNumber(0)}[r.IntN(4)]
	}
	return record
}

// attestExpected is an externally supplied digest: absent, the right one, a
// wrong one, malformed, or "".
func attestExpected(r *rand.Rand, right string) *string {
	var v string
	switch r.IntN(6) {
	case 0, 1:
		return nil
	case 2:
		v = right
	case 3:
		v = "sha256:" + strings.Repeat("b", 64)
	case 4:
		v = []string{"abc", "sha256:" + strings.Repeat("A", 64), strings.Repeat("a", 64)}[r.IntN(3)]
	case 5:
		v = ""
	}
	return &v
}

func attestVerifyArgs(record jsjson.Value, expected *string) string {
	o := jsjson.NewObj()
	o.Set("record", record)
	if expected != nil {
		o.Set("expected", jsjson.MakeString(*expected))
	}
	return jsjson.Stringify(jsjson.MakeObject(o))
}

func attestVerification(v attest.Verification) string { return jsjson.Stringify(v.JSON()) }

// requireAttestReached fails for each internal/attest error text that no TS
// result in outputs contained (the live tests stand in for goldens until
// the CLI cases exist). Two texts cannot be reached: the receipt path
// escape (ReceiptIdSchema refuses every path first) and "Attestation path
// root must be a real directory" (the filesystem root always is one).
func requireAttestReached(t *testing.T, outputs string, texts ...string) {
	t.Helper()
	for _, text := range texts {
		if !strings.Contains(outputs, text) {
			t.Errorf("no generated input reached %q", text)
		}
	}
}

// syncAttestClock brings the Go seeded clock to the oracle's. tsx reads the
// seeded Date while it starts (from an immediate) and while it loads
// src/attestation.ts, so the module is imported first (an input refused
// before the clock read), given time, and then seededTick's count (its own
// read included) is replayed on the Go clock. The returned check fails when
// the two clocks are no longer in step.
func syncAttestClock(t *testing.T, c *oracle.Client) func(where string) {
	t.Helper()
	oracleCall(t, c, "createBundleAttestation", `{"input":null}`)
	time.Sleep(time.Second)
	tick, err := strconv.ParseInt(oracleCall(t, c, "seededTick", "null"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	for range tick + 1 {
		clock.NowMillis()
	}
	return func(where string) {
		t.Helper()
		if ts, goTick := oracleCall(t, c, "seededTick", "null"), clock.NowMillis()-seededEpochMillis; ts != strconv.FormatInt(goTick, 10) {
			t.Fatalf("%s: oracle clock at %s, Go clock at %d", where, ts, goTick)
		}
	}
}

// TestLiveAttestationCreate builds receipts from 2,000 generated inputs at
// every schema boundary (receipt id, both identifiers, the four digests)
// with frozen TS's createBundleAttestation and the Go port, both seeded,
// and compares the canonical receipt or THREW: text (the ZodError message).
// Each receipt is then edited and verified in memory with
// verifyBundleAttestation against an expected digest that is absent, right,
// wrong, malformed or "".
func TestLiveAttestationCreate(t *testing.T) {
	oracle.Skip(t)
	const seed = "live-attestation-create"
	c := oracle.StartSeeded(t, seed)
	defer c.Close()
	clock.ResetForTest(seed)
	syncCheck := syncAttestClock(t, c)
	// zod 3 runs a refinement unless a string check aborted, and min and
	// max do not abort: a subject that is too long, untrimmed and holds a
	// control character gets all three issues.
	pinned := attest.CreationInput{ReceiptID: "r1", Subject: " " + strings.Repeat("z", 513) + "\u0001", Issuer: "", BundleRootDigest: attestDigest}
	pinnedArgs := jsjson.Stringify(jsjson.MustObject("input", attestInputValue(pinned)))
	pinnedWant := oracleText(t, c, "createBundleAttestation", pinnedArgs)
	for _, text := range []string{`"too_big"`, `"too_small"`, "cannot start or end with whitespace", "cannot contain control characters"} {
		if !strings.Contains(pinnedWant, text) {
			t.Fatalf("TS result for the pinned subject lacks %s: %s", text, pinnedWant)
		}
	}
	if record, err := attest.Create(pinned); threwOr(record, err) != pinnedWant {
		t.Fatalf("pinned subject:\ngot  %s\nwant %s", threwOr(record, err), pinnedWant)
	}
	r := rand.New(rand.NewPCG(47, 7))
	var outputs strings.Builder
	created, refused, verified := 0, 0, 0
	var last jsjson.Value
	for i := 0; i < 2000; i++ {
		if i%200 == 0 {
			syncCheck(fmt.Sprintf("create %d", i))
		}
		in := attestInput(r)
		args := jsjson.Stringify(jsjson.MustObject("input", attestInputValue(in)))
		want := oracleText(t, c, "createBundleAttestation", args)
		outputs.WriteString(want + "\n")
		record, err := attest.Create(in)
		got := threwOr(record, err)
		if got != want {
			t.Fatalf("case %d createBundleAttestation(%s):\ngot  %s\nwant %s", i, args, got, want)
		}
		if err != nil {
			refused++
			if last.Kind() == jsjson.Undefined {
				continue
			}
			record = parseOrFatal(t, canonicalOf(last))
		} else {
			created++
			last = record
		}
		digest := record.Get("receiptDigest").Str()
		for k := 0; k < 2; k++ {
			mutated := parseOrFatal(t, canonicalOf(record))
			if r.IntN(4) != 0 {
				mutated = mutateAttestation(r, mutated)
			}
			expected := attestExpected(r, digest)
			vargs := attestVerifyArgs(mutated, expected)
			want := oracleText(t, c, "verifyBundleAttestation", vargs)
			outputs.WriteString(want + "\n")
			var got string
			if err := jsexc.Try(func() { got = attestVerification(attest.Verify(mutated, expected)) }); err != nil {
				got = "THREW:" + jsexc.Message(err)
			}
			if got != want {
				t.Fatalf("case %d verifyBundleAttestation(%s):\ngot  %s\nwant %s", i, vargs, got, want)
			}
			verified++
		}
	}
	requireAttestReached(t, outputs.String(),
		"must use only letters, numbers, underscores, or hyphens and cannot be a path",
		"cannot start or end with whitespace", "cannot contain control characters",
		"must be a sha256:<64-lowercase-hex> digest", "must be a canonical UTC ISO-8601 timestamp",
		"must be a valid canonical UTC ISO-8601 timestamp", "externally supplied receipt digest is not a sha256 digest",
		"attestation schema validation failed: ", "receiptDigest does not match the canonical receipt contents",
		"externally supplied receipt digest does not match", `"externalDigestStatus":"MATCH"`)
	liveSummary(t, "attestation create: %d created, %d refused; %d verified", created, refused, verified)
}

// attestWritten is TS's JSON.stringify of a WrittenBundleAttestation.
func attestWritten(w attest.Written) string {
	return jsjson.Stringify(jsjson.MustObject(
		"directory", jsjson.MakeString(w.Directory), "path", jsjson.MakeString(w.Path),
		"attestation", w.Attestation, "receiptDigest", jsjson.MakeString(w.ReceiptDigest)))
}

// attestTree is describeTree that does not read a FIFO (it would block):
// any entry that is not a directory, symlink or regular file is listed by
// its type and mode only.
func attestTree(root string) string {
	var lines []string
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || path == root {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(path)
			lines = append(lines, "L "+rel+" "+target)
		case info.IsDir():
			lines = append(lines, "D "+rel+" "+info.Mode().Perm().String())
		case info.Mode().IsRegular():
			b, _ := os.ReadFile(path)
			lines = append(lines, "F "+rel+" "+info.Mode().Perm().String()+" "+canonical.SHA256HexBytes(b))
		default:
			lines = append(lines, "S "+rel+" "+info.Mode().String())
		}
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// TestLiveAttestationStore writes and verifies receipts in 300 generated
// store layouts with frozen TS and the Go port, both seeded: a store equal
// to the root, below it or outside it; a symlinked root, store segment or
// .faultline; a file in the path; a read-only store; an existing receipt,
// or one that is a symlink (dangling or not), a directory or a FIFO; a
// stored receipt that is invalid JSON (invalid UTF-8 too), edited, or
// edited with a recomputed checksum; a missing store and an invalid
// receipt id; and an expected digest that is absent, right, wrong,
// malformed or "". It compares each result (or THREW: text) and the
// workspace trees.
func TestLiveAttestationStore(t *testing.T) {
	oracle.Skip(t)
	const seed = "live-attestation-store"
	c := oracle.StartSeeded(t, seed)
	defer c.Close()
	clock.ResetForTest(seed)
	syncCheck := syncAttestClock(t, c)
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	r := rand.New(rand.NewPCG(61, 3))
	var outputs strings.Builder
	written, refused := 0, 0
	const root = ".faultline/attestations"
	for i := 0; i < 300; i++ {
		if i%50 == 0 {
			syncCheck(fmt.Sprintf("store %d", i))
		}
		ts, gs := filepath.Join(work, "ts", strconv.Itoa(i)), filepath.Join(work, "go", strconv.Itoa(i))
		store := root
		in := attest.CreationInput{ReceiptID: "r" + strconv.Itoa(i%7), Subject: "bundle", Issuer: "ci", BundleRootDigest: attestDigest}
		if r.IntN(3) == 0 {
			w := attestDigests[1]
			in.WitnessDigest = &w
		}
		verifyID := in.ReceiptID
		var setup func(dir string)
		var edit func(dir string) // after the first write, before verifying
		writes := 1
		layout := r.IntN(22)
		switch layout {
		case 0: // the default layout
		case 1:
			store = root + "/team/a"
		case 2:
			store = "elsewhere"
		case 3:
			store = ".faultline"
		case 4:
			store = root + "/link/sub"
			setup = func(dir string) {
				os.MkdirAll(filepath.Join(dir, root), 0o755)
				os.Symlink("..", filepath.Join(dir, root, "link"))
			}
		case 5:
			setup = func(dir string) {
				os.MkdirAll(filepath.Join(dir, "real"), 0o755)
				os.Symlink("real", filepath.Join(dir, ".faultline"))
			}
		case 6:
			setup = func(dir string) {
				os.MkdirAll(filepath.Join(dir, ".faultline", "real"), 0o755)
				os.Symlink("real", filepath.Join(dir, root))
			}
		case 7:
			store = root + "/file/sub"
			setup = func(dir string) {
				os.MkdirAll(filepath.Join(dir, root), 0o755)
				os.WriteFile(filepath.Join(dir, root, "file"), []byte("x"), 0o644)
			}
		case 8:
			setup = func(dir string) {
				os.MkdirAll(filepath.Join(dir, ".faultline"), 0o755)
				os.WriteFile(filepath.Join(dir, root), []byte("x"), 0o644)
			}
		case 9:
			setup = func(dir string) {
				os.MkdirAll(filepath.Join(dir, root), 0o755)
				os.Chmod(filepath.Join(dir, root), 0o555)
			}
		case 10: // the same receipt twice: write-once
			writes = 2
		case 11, 12, 13:
			kind := layout
			setup = func(dir string) {
				os.MkdirAll(filepath.Join(dir, root), 0o755)
				p := filepath.Join(dir, root, in.ReceiptID+".json")
				switch kind {
				case 11:
					os.Symlink([]string{"elsewhere.json", "../../target.json"}[i%2], p)
					os.WriteFile(filepath.Join(dir, "target.json"), []byte("{}"), 0o644)
				case 12:
					os.Mkdir(p, 0o755)
				case 13:
					if err := mkfifo(p); err != nil {
						t.Fatal(err)
					}
				}
			}
		case 14, 15:
			bodies := []string{"{", "not json", "", "\xff\xfe", "{\"a\":1}x", "[]", "null", "{\"receiptId\":\"\xed\xa0\x80\"}"}
			body := bodies[r.IntN(len(bodies))]
			edit = func(dir string) { os.WriteFile(filepath.Join(dir, root, in.ReceiptID+".json"), []byte(body), 0o600) }
		case 16, 17:
			mutSeed := r.Uint64()
			edit = func(dir string) {
				p := filepath.Join(dir, root, in.ReceiptID+".json")
				b, err := os.ReadFile(p)
				if err != nil {
					return
				}
				v, err := jsjson.Parse(string(b))
				if err != nil {
					return
				}
				v = mutateAttestation(rand.New(rand.NewPCG(mutSeed, 1)), v)
				os.WriteFile(p, []byte(canonicalOf(v)+"\n"), 0o600)
			}
		case 18:
			verifyID = []string{"missing", "../r0", "", "a/b", strings.Repeat("a", 129)}[r.IntN(5)]
		case 19:
			store = []string{"", "   ", "\t"}[r.IntN(3)]
		case 20: // verify only, in a store that does not exist
			writes = 0
			store = root + "/never"
		case 21:
			in = attestInput(r)
			verifyID = in.ReceiptID
		}
		for _, dir := range []string{ts, gs} {
			os.MkdirAll(dir, 0o755)
			if setup != nil {
				setup(dir)
			}
		}
		storeArg := jsjson.MakeString(store)
		// TS's library call, in the workspace.
		call := func(op string, fields ...any) string {
			args := jsjson.MustObject(append([]any{"cwd", jsjson.MakeString(ts), "store", storeArg, "root", jsjson.MakeString(root)}, fields...)...)
			want := strings.ReplaceAll(oracleText(t, c, op, jsjson.Stringify(args)), ts, gs)
			outputs.WriteString(want + "\n")
			return want
		}
		inGo := func(f func()) {
			if err := os.Chdir(gs); err != nil {
				t.Fatal(err)
			}
			defer os.Chdir(cwd)
			f()
		}
		receiptDigest := ""
		for w := 0; w < writes; w++ {
			want := call("writeBundleAttestation", "input", attestInputValue(in))
			var got string
			inGo(func() {
				out, err := attest.Write(store, in, root)
				if err != nil {
					got = "THREW:" + jsexc.Message(err)
					refused++
				} else {
					got = attestWritten(out)
					receiptDigest = out.ReceiptDigest
					written++
				}
			})
			if got != want {
				t.Fatalf("case %d (layout %d) write %d:\ngot  %s\nwant %s", i, layout, w, got, want)
			}
		}
		if edit != nil {
			edit(ts)
			edit(gs)
		}
		if receiptDigest == "" {
			receiptDigest = "sha256:" + strings.Repeat("c", 64)
		}
		for k := 0; k < 2; k++ {
			expected := attestExpected(r, receiptDigest)
			fields := []any{"receiptId", jsjson.MakeString(verifyID)}
			if expected != nil {
				fields = append(fields, "expected", jsjson.MakeString(*expected))
			}
			want := call("verifyStoredBundleAttestation", fields...)
			var got string
			inGo(func() { got = attestVerification(attest.VerifyStored(store, verifyID, expected, root)) })
			if got != want {
				t.Fatalf("case %d (layout %d) verify %v:\ngot  %s\nwant %s", i, layout, expected, got, want)
			}
		}
		a, b := attestTree(gs), attestTree(ts)
		for _, dir := range []string{ts, gs} {
			unlockTree(dir)
		}
		if a != b {
			t.Fatalf("case %d (layout %d): workspace trees differ:\ngo\n%s\nts\n%s", i, layout, a, b)
		}
	}
	requireAttestReached(t, outputs.String(),
		"Attestation store does not exist: ",
		"Attestation store cannot traverse a symbolic link or non-directory: ", "Attestation store directory must be non-empty.",
		"Attestation store must be inside the managed root: ", "Attestation receipt already exists and cannot be overwritten: ",
		"attestation receipt does not exist", "attestation receipt is a symbolic link or non-regular file",
		"attestation verification failed safely: ", "EACCES: permission denied, open ",
		"externally supplied receipt digest does not match", `"externalDigestStatus":"MATCH"`)
	liveSummary(t, "attestation store: %d written, %d refused", written, refused)
}

// TestLiveCanonicalISO compares jsdate.IsCanonicalISO with `new Date(s)`'s
// round trip on 20,000 generated timestamps of toISOString's shapes: every
// month, day, hour, minute and second boundary, leap years (0000, 1900,
// 2000, 2100), and six-digit signed years up to the ±8.64e15 ms limit.
func TestLiveCanonicalISO(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(5, 20000))
	two := func(edge []int, max int) string {
		if r.IntN(3) == 0 {
			return fmt.Sprintf("%02d", r.IntN(max))
		}
		return fmt.Sprintf("%02d", edge[r.IntN(len(edge))])
	}
	years := []string{"0000", "0001", "0004", "0100", "0400", "1600", "1900", "1970", "1999", "2000", "2024", "2026", "2100", "2400", "9999",
		"+275760", "-271821", "+010000", "+002026", "-000000", "-000001", "+099999", "-100000", "+275759", "-271822", "+275761"}
	canonicalCount := 0
	for batch := 0; batch < 20; batch++ {
		values := make([]string, 1000)
		for i := range values {
			year := years[r.IntN(len(years))]
			if r.IntN(3) == 0 {
				year = fmt.Sprintf("%04d", r.IntN(10000))
			}
			ms := fmt.Sprintf("%03d", []int{0, 1, 999, r.IntN(1000)}[r.IntN(4)])
			values[i] = year + "-" + two([]int{0, 1, 2, 4, 9, 11, 12, 13, 99}, 100) + "-" + two([]int{0, 1, 28, 29, 30, 31, 32, 99}, 100) +
				"T" + two([]int{0, 23, 24, 25, 99}, 100) + ":" + two([]int{0, 59, 60, 61, 99}, 100) + ":" + two([]int{0, 59, 60, 61, 99}, 100) + "." + ms + "Z"
			if year == "+275760" && r.IntN(2) == 0 {
				values[i] = "+275760-09-13T00:00:00." + ms + "Z"
			}
			if year == "-271821" && r.IntN(2) == 0 {
				values[i] = "-271821-04-20T00:00:00." + ms + "Z"
			}
		}
		items := make([]jsjson.Value, len(values))
		for i, v := range values {
			items[i] = jsjson.MakeString(v)
		}
		want := oracleText(t, c, "isCanonicalIsoDate", jsjson.Stringify(jsjson.MustObject("values", jsjson.MakeArray(items))))
		for i, v := range values {
			got := jsdate.IsCanonicalISO(v)
			if got != (want[i] == '1') {
				t.Fatalf("IsCanonicalISO(%q) = %v, Node says %c", v, got, want[i])
			}
			if got {
				canonicalCount++
			}
		}
	}
	liveSummary(t, "canonical ISO: 20000 timestamps, %d canonical", canonicalCount)
}
