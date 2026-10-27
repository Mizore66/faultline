//go:build faultline_test && unix

package difftest

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/repair"
)

// Slice 4c Task 9: the write-once repair brief package store and its
// verifier against src/repair-brief-store.ts. Unix only: the cases use
// umasks, FIFOs and symbolic links, and compare modes (describeTree).

// repairSourceArg is the TS source object for a repair.Source.
func repairSourceArg(src repair.Source) jsjson.Value {
	o := jsjson.NewObj()
	o.Set("kind", jsjson.MakeString(src.Kind))
	if src.HasModel {
		o.Set("model", jsjson.MakeString(src.Model))
	}
	return jsjson.MakeObject(o)
}

func randomRepairSource(r *rand.Rand) repair.Source {
	src := repair.Source{Kind: []string{"OFFLINE_INPUT", "GPT-5.6"}[r.IntN(2)]}
	if r.IntN(12) == 0 {
		src.Kind = []string{"gpt-5.6", "", "OFFLINE"}[r.IntN(3)]
	}
	if r.IntN(2) == 0 {
		src.HasModel = true
		switch r.IntN(8) {
		case 0:
			src.Model = ""
		case 1:
			src.Model = "m"
		case 2:
			src.Model = strings.Repeat("m", 256)
		case 3:
			src.Model = strings.Repeat(astralChar, 128) // 256 units
		case 4:
			src.Model = strings.Repeat("m", 257)
		case 5:
			src.Model = strings.Repeat(astralChar, 128) + "m" // 257 units
		default:
			src.Model = "gpt-5.6"
		}
	}
	return src
}

func repairWrittenView(w repair.WrittenBrief, err error) string {
	if err != nil {
		return "THREW:" + jsexc.Message(err)
	}
	return jsjson.Stringify(w.JSON())
}

// repairStoreWrite runs one write in lockstep: TS in ts, Go in gs, both
// with cwd at the workspace and the same umask (-1 for none). It returns
// TS's result with ts replaced by gs, and Go's.
func repairStoreWrite(t *testing.T, c *oracle.Client, ts, gs, output, packet, brief string, src repair.Source, umask int) (want, got string) {
	t.Helper()
	args := jsjson.NewObj()
	args.Set("cwd", jsjson.MakeString(ts))
	args.Set("output", jsjson.MakeString(output))
	args.Set("packet", jsjson.MakeString(packet))
	args.Set("brief", jsjson.MakeString(brief))
	args.Set("source", repairSourceArg(src))
	if umask >= 0 {
		args.Set("umask", jsjson.MakeNumber(float64(umask)))
	}
	want = strings.ReplaceAll(oracleText(t, c, "writeRepairBriefArtifact", jsjson.Stringify(jsjson.MakeObject(args))), ts, gs)
	cwd, _ := os.Getwd()
	if err := os.Chdir(gs); err != nil {
		t.Fatal(err)
	}
	old := 0
	if umask >= 0 {
		old = syscall.Umask(umask)
	}
	w, err := repair.WriteBriefArtifact(output, repairParse(t, packet), repairParse(t, brief), src)
	if umask >= 0 {
		syscall.Umask(old)
	}
	os.Chdir(cwd)
	return want, repairWrittenView(w, err)
}

// repairResign rewrites a manifest's manifestDigest over the rest.
func repairResign(manifest jsjson.Value) jsjson.Value {
	o := bundle.Without(manifest, "manifestDigest").Obj()
	o.Set("manifestDigest", jsjson.MakeString(jsexc.Must(canonical.DigestJSON(jsjson.MakeObject(o)))))
	return jsjson.MakeObject(o)
}

func repairCanonicalFile(t *testing.T, path string, v jsjson.Value) {
	t.Helper()
	if err := os.WriteFile(path, []byte(jsexc.Must(canonical.CanonicalJSON(v))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func repairReadJSON(t *testing.T, path string) jsjson.Value {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return repairParse(t, string(b))
}

// repairPackageMutation picks one mutation of a stored package; the result
// is applied to the TS and the Go copy alike.
func repairPackageMutation(t *testing.T, r *rand.Rand) (string, func(dir string)) {
	files := []string{"README.md", "evidence-packet.json", "repair-brief.json", "manifest.json"}
	jsonFiles := files[1:]
	file := files[r.IntN(len(files))]
	jsonFile := jsonFiles[r.IntN(len(jsonFiles))]
	write := func(dir, name, text string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	editJSON := func(name string, edit func(v jsjson.Value) jsjson.Value) func(string) {
		return func(dir string) {
			p := filepath.Join(dir, name)
			repairCanonicalFile(t, p, edit(repairReadJSON(t, p)))
		}
	}
	k := r.IntN(4) // the sub-case, drawn once for both copies
	switch r.IntN(26) {
	case 0:
		return "missing " + file, func(dir string) { os.Remove(filepath.Join(dir, file)) }
	case 1:
		name := []string{"zz.txt", "A.json", ".hidden", "manifest.json.bak", "é.json"}[r.IntN(5)]
		return "extra " + name, func(dir string) { write(dir, name, "x") }
	case 2:
		return "symlinked " + file, func(dir string) {
			p := filepath.Join(dir, file)
			b, _ := os.ReadFile(p)
			os.WriteFile(p+".target", b, 0o600)
			os.Remove(p)
			os.Symlink(filepath.Base(p)+".target", p)
		}
	case 3:
		return "dangling symlink", func(dir string) { os.Symlink("nowhere", filepath.Join(dir, "link")) }
	case 4:
		return "fifo", func(dir string) {
			if err := mkfifo(filepath.Join(dir, "fifo")); err != nil {
				t.Fatal(err)
			}
		}
	case 5:
		return "nested directory", func(dir string) {
			os.Mkdir(filepath.Join(dir, "sub"), 0o700)
			write(filepath.Join(dir, "sub"), "inner.json", "{}")
		}
	case 6:
		return "nested symlink", func(dir string) {
			os.Mkdir(filepath.Join(dir, "sub"), 0o700)
			os.Symlink("..", filepath.Join(dir, "sub", "up"))
		}
	case 7:
		return "artifact is a directory", func(dir string) {
			os.Remove(filepath.Join(dir, file))
			os.Mkdir(filepath.Join(dir, file), 0o700)
		}
	case 8:
		return "manifest limitation (unsigned)", editJSON("manifest.json", func(v jsjson.Value) jsjson.Value {
			v.Obj().Set("limitations", repairStrings("changed"))
			return v
		})
	case 9:
		return "manifest source (re-signed)", editJSON("manifest.json", func(v jsjson.Value) jsjson.Value {
			v.Obj().Set("source", jsjson.MustObject("kind", jsjson.MakeString("GPT-5.6"), "model", jsjson.MakeString("other")))
			return repairResign(v)
		})
	case 10:
		return "manifest packet digest (re-signed)", editJSON("manifest.json", func(v jsjson.Value) jsjson.Value {
			v.Get("evidencePacket").Obj().Set("digest", jsjson.MakeString("sha256:"+strings.Repeat("7", 64)))
			return repairResign(v)
		})
	case 11:
		return "manifest brief digest (re-signed)", editJSON("manifest.json", func(v jsjson.Value) jsjson.Value {
			v.Get("repairBrief").Obj().Set("digest", jsjson.MakeString("sha256:"+strings.Repeat("8", 64)))
			return repairResign(v)
		})
	case 12:
		return "manifest schema", editJSON("manifest.json", func(v jsjson.Value) jsjson.Value {
			switch k {
			case 0:
				v.Obj().Set("zzExtra", jsjson.MakeNull())
			case 1:
				v.Obj().Set("classification", jsjson.MakeString("EXECUTED"))
			case 2:
				v.Obj().Delete("source")
			default:
				v.Obj().Set("limitations", jsjson.MakeArray(nil))
			}
			return v
		})
	case 13:
		return "packet fact (unsigned)", editJSON("evidence-packet.json", func(v jsjson.Value) jsjson.Value {
			v.Get("facts").Items()[0].Obj().Set("statement", jsjson.MakeString("changed"))
			return v
		})
	case 14:
		return "packet fact (re-signed)", editJSON("evidence-packet.json", func(v jsjson.Value) jsjson.Value {
			v.Get("facts").Items()[0].Obj().Set("statement", jsjson.MakeString("changed"))
			repairSign(v)
			return v
		})
	case 15:
		return "legacy v1 packet", editJSON("evidence-packet.json", func(v jsjson.Value) jsjson.Value {
			v.Obj().Set("schemaVersion", jsjson.MakeString("faultline.repair-evidence.v1"))
			if k%2 == 0 {
				repairSign(v)
			}
			return v
		})
	case 16:
		return "packet schema", editJSON("evidence-packet.json", func(v jsjson.Value) jsjson.Value {
			switch k % 3 {
			case 0:
				v.Obj().Set("nativeCodexInterception", jsjson.MakeBool(true))
			case 1:
				v.Obj().Set("facts", jsjson.MakeArray(v.Get("facts").Items()[:1]))
			default:
				v.Obj().Set("schemaVersion", jsjson.MakeString("faultline.repair-evidence.v3"))
			}
			return v
		})
	case 17:
		return "brief statement", editJSON("repair-brief.json", func(v jsjson.Value) jsjson.Value {
			v.Get("proposedInvariant").Obj().Set("statement", jsjson.MakeString("changed"))
			return v
		})
	case 18:
		return "brief unknown id", editJSON("repair-brief.json", func(v jsjson.Value) jsjson.Value {
			v.Get("proposedInvariant").Obj().Set("evidenceIds", repairStrings("E99", "E1"))
			return v
		})
	case 19:
		return "brief other packet digest", editJSON("repair-brief.json", func(v jsjson.Value) jsjson.Value {
			v.Obj().Set("evidencePacketDigest", jsjson.MakeString("sha256:"+strings.Repeat("9", 64)))
			return v
		})
	case 20:
		return "brief schema", editJSON("repair-brief.json", func(v jsjson.Value) jsjson.Value {
			switch k % 3 {
			case 0:
				v.Obj().Set("zzExtra", jsjson.MakeNull())
			case 1:
				v.Obj().Set("repairDirections", jsjson.MakeArray(nil))
			default:
				v.Obj().Delete("uncertainties")
			}
			return v
		})
	case 21:
		text := []string{"{", "", "null", "[]", "\"x\"", "{\"a\":1,}", "1e999", "\ufeff{}"}[r.IntN(8)]
		return "invalid JSON " + jsjson.Quote(text) + " in " + jsonFile, func(dir string) { write(dir, jsonFile, text) }
	case 22:
		return "invalid UTF-8 in " + jsonFile, func(dir string) {
			p := filepath.Join(dir, jsonFile)
			b, _ := os.ReadFile(p)
			s := string(b)
			i := strings.Index(s, `":"`) + 3
			os.WriteFile(p, []byte(s[:i]+"\xff\xfe"+s[i:]), 0o600)
		}
	case 23:
		return "reordered keys " + jsonFile, func(dir string) {
			p := filepath.Join(dir, jsonFile)
			write(dir, jsonFile, jsjson.StringifyIndent(repairReadJSON(t, p), "  "))
		}
	case 24:
		return "unreadable " + file, func(dir string) { os.Chmod(filepath.Join(dir, file), 0o000) }
	}
	return "unchanged", func(string) {}
}

// TestLiveRepairBriefStore writes 400 generated packages with frozen TS's
// writeRepairBriefArtifact and the Go port, both seeded, in a fresh
// workspace each (valid and invalid packets, briefs and sources, models of
// 0/1/256/257 UTF-16 units; outputs equal to the root, outside it, nested,
// through a symbolic link or a file, existing; umasks that make the stage
// unwritable or unreadable), comparing the result (or THREW:) and the
// trees. Then it verifies 400 mutated packages with
// verifyRepairBriefArtifact and the port: missing, extra, symlinked and
// special files, nested directories, tampered or re-signed manifests,
// packets and briefs, the legacy v1 packet, invalid JSON and UTF-8, and
// every expected-digest kind.
func TestLiveRepairBriefStore(t *testing.T) {
	oracle.Skip(t)
	const seed = "live-repair-brief-store"
	c := oracle.StartSeeded(t, seed)
	defer c.Close()
	clock.ResetForTest(seed)
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer unlockTree(work)
	realPacket, err := repair.CreateEvidencePacket(repairParse(t, seedTexts(t, "git-unbound/investigation.json")[0]))
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewPCG(46, 9))
	var outcomes strings.Builder
	record := func(want string) {
		recordOutput(t, want)
		outcomes.WriteString(want)
		outcomes.WriteByte('\n')
	}

	root := filepath.Join(".faultline", "repair-briefs")
	written, refused := 0, 0
	for i := 0; i < 400; i++ {
		packet := repairClone(t, realPacket)
		if r.IntN(3) == 0 {
			packet = randomRepairPacket(r, r.IntN(3) == 0)
		}
		if r.IntN(25) == 0 {
			packet = []jsjson.Value{jsjson.MakeNull(), jsjson.MakeString("packet"), jsjson.MakeArray(nil)}[r.IntN(3)]
		}
		brief := randomRepairBrief(r, packet, r.IntN(3) == 0)
		src := randomRepairSource(r)
		ts, gs := filepath.Join(work, "w", "ts", strconv.Itoa(i)), filepath.Join(work, "w", "go", strconv.Itoa(i))
		output := filepath.Join(root, "out")
		umask := -1
		var setup func(dir string)
		switch r.IntN(16) {
		case 0:
			output = root
		case 1:
			output = []string{"elsewhere", filepath.Join(".faultline", "repair-briefs-x", "out"), filepath.Join(root, "..", "out")}[r.IntN(3)]
		case 2:
			output = filepath.Join(root, "a", "b", "out")
		case 3:
			setup = func(dir string) { os.MkdirAll(filepath.Join(dir, root, "out"), 0o755) }
		case 4:
			output = filepath.Join(root, "link", "out")
			setup = func(dir string) {
				os.MkdirAll(filepath.Join(dir, root), 0o755)
				os.Symlink("..", filepath.Join(dir, root, "link"))
			}
		case 5:
			output = filepath.Join(root, "file", "out")
			setup = func(dir string) {
				os.MkdirAll(filepath.Join(dir, root), 0o755)
				os.WriteFile(filepath.Join(dir, root, "file"), []byte("x"), 0o644)
			}
		case 6:
			setup = func(dir string) {
				os.MkdirAll(filepath.Join(dir, "real"), 0o755)
				os.Symlink("real", filepath.Join(dir, ".faultline"))
			}
		case 7, 8:
			// Under 0277 the stage is created 0500, so its first file cannot
			// be opened; under 0477 it is 0300, so the files are written but
			// the stage cannot be read back to verify it.
			umask = []int{0o277, 0o477, 0o077}[r.IntN(3)]
			setup = func(dir string) { os.MkdirAll(filepath.Join(dir, root), 0o700) }
		case 9:
			output = filepath.Join(root, "ro", "out")
			setup = func(dir string) {
				os.MkdirAll(filepath.Join(dir, root, "ro"), 0o755)
				os.Chmod(filepath.Join(dir, root, "ro"), 0o555)
			}
		case 10:
			output = filepath.Join(root, "x.y", "out.dir")
		}
		for _, dir := range []string{ts, gs} {
			os.MkdirAll(dir, 0o755)
			if setup != nil {
				setup(dir)
			}
		}
		want, got := repairStoreWrite(t, c, ts, gs, output, repairText(packet), repairText(brief), src, umask)
		record(want)
		if got != want {
			t.Fatalf("write %d (%s): packet %s\nbrief %s\ngot  %s\nwant %s", i, output, repairText(packet), repairText(brief), got, want)
		}
		if strings.HasPrefix(want, "THREW:") {
			refused++
		} else {
			written++
		}
		for _, dir := range []string{ts, gs} {
			unlockTree(dir)
		}
		if a, b := describeTree(gs), describeTree(ts); a != b {
			t.Fatalf("write %d: workspace trees differ:\ngo\n%s\nts\n%s", i, a, b)
		}
	}

	// Three template packages, written in lockstep (and compared) so the
	// seeded UUID sequences stay aligned.
	type template struct{ dir, digest string }
	var templates []template
	for i, tc := range []struct {
		packet jsjson.Value
		src    repair.Source
	}{
		{realPacket, repair.Source{Kind: "OFFLINE_INPUT"}},
		{realPacket, repair.Source{Kind: "GPT-5.6", Model: "gpt-5.6", HasModel: true}},
		{randomRepairPacket(r, false), repair.Source{Kind: "GPT-5.6", Model: astralChar + loneSurrogate, HasModel: true}},
	} {
		ts, gs := filepath.Join(work, "t", "ts", strconv.Itoa(i)), filepath.Join(work, "t", "go", strconv.Itoa(i))
		os.MkdirAll(ts, 0o755)
		os.MkdirAll(gs, 0o755)
		var brief jsjson.Value
		for {
			brief = randomRepairBrief(r, tc.packet, false)
			if repair.ValidateBrief(tc.packet, brief).Valid {
				break
			}
		}
		want, got := repairStoreWrite(t, c, ts, gs, filepath.Join(root, "pkg"), repairText(tc.packet), repairText(brief), tc.src, -1)
		if got != want || strings.HasPrefix(want, "THREW:") {
			t.Fatalf("template %d:\ngot  %s\nwant %s", i, got, want)
		}
		if a, b := describeTree(gs), describeTree(ts); a != b {
			t.Fatalf("template %d: trees differ:\ngo\n%s\nts\n%s", i, a, b)
		}
		dir := filepath.Join(ts, root, "pkg")
		templates = append(templates, template{dir, repairReadJSON(t, filepath.Join(dir, "manifest.json")).Get("manifestDigest").Str()})
	}

	valid := 0
	for i := 0; i < 400; i++ {
		tmpl := templates[r.IntN(len(templates))]
		ts, gs := filepath.Join(work, "v", "ts", strconv.Itoa(i)), filepath.Join(work, "v", "go", strconv.Itoa(i))
		label, mutate := repairPackageMutation(t, r)
		second := ""
		var mutate2 func(string)
		if r.IntN(4) == 0 {
			second, mutate2 = repairPackageMutation(t, r)
		}
		target := func(dir string) string { return dir }
		switch r.IntN(30) {
		case 0:
			label, mutate, mutate2 = "no directory", nil, nil
			target = func(dir string) string { return filepath.Join(dir, "absent") }
		case 1:
			label, mutate, mutate2 = "directory is a file", nil, nil
			target = func(dir string) string { return filepath.Join(dir, "README.md") }
		case 2:
			label = "directory is a symlink"
			target = func(dir string) string {
				link := dir + ".link"
				os.Symlink(filepath.Base(dir), link)
				return link
			}
		}
		for _, dir := range []string{ts, gs} {
			copyTree(t, tmpl.dir, dir)
			for _, name := range []string{"README.md", "evidence-packet.json", "repair-brief.json", "manifest.json"} {
				os.Chmod(filepath.Join(dir, name), 0o600)
			}
			if mutate != nil {
				mutate(dir)
			}
			if mutate2 != nil {
				mutate2(dir)
			}
		}
		var expected *string
		switch r.IntN(6) {
		case 0, 1:
			d := tmpl.digest
			expected = &d
		case 2:
			d := "sha256:" + strings.Repeat("6", 64)
			expected = &d
		case 3:
			d := []string{"", "not-a-digest", strings.ToUpper(tmpl.digest)}[r.IntN(3)]
			expected = &d
		}
		args := jsjson.NewObj()
		args.Set("directory", jsjson.MakeString(target(ts)))
		if expected != nil {
			args.Set("expected", jsjson.MakeString(*expected))
		}
		want := strings.ReplaceAll(oracleText(t, c, "verifyRepairBriefArtifact", jsjson.Stringify(jsjson.MakeObject(args))), ts, gs)
		got := jsjson.Stringify(repair.VerifyBriefArtifact(target(gs), expected).JSON())
		record(want)
		if got != want {
			t.Fatalf("verify %d (%s; %s; expected %v):\ngot  %s\nwant %s", i, label, second, expected, got, want)
		}
		if strings.HasPrefix(want, `{"valid":true`) {
			valid++
		}
		unlockTree(ts)
		unlockTree(gs)
	}
	liveSummary(t, "repair brief store: %d written, %d refused; %d verified packages valid of 400", written, refused, valid)

	// Every reachable store text and the verifier's outcomes must have been
	// compared. Not reachable: "Repair brief root is not a real directory"
	// (the filesystem root is always one), "... appeared during assembly"
	// (race only) and "Refusing to publish an invalid repair brief package"
	// (a stage just written verifies unless the filesystem fails in
	// between; an unreadable stage under umask 0477 also fails its removal,
	// and that error is thrown instead), as for the prevention writer.
	all := outcomes.String()
	for _, text := range []string{
		"Refusing to store an invalid inferred repair brief: ",
		"Repair brief output must be a child directory of ",
		"Repair brief output cannot traverse a symbolic link or non-directory: ",
		"Repair brief output already exists and will not be replaced: ",
		`"code": "invalid_enum_value"`,
		`"code": "too_big"`,
		"EACCES",
		"Repair brief directory does not exist",
		"Repair brief directory must be a real directory: ",
		"Repair brief directory contains a symbolic link or special file: ",
		"Repair brief package verification failed safely: ",
		"Repair brief package is missing ",
		"Repair brief package contains an unexpected artifact: ",
		"Repair brief artifact must be a regular non-symlink file: ",
		"Unable to read repair brief manifest: ",
		"Unable to read repair evidence packet: ",
		"Unable to read repair brief: ",
		"Repair brief manifest schema validation failed: ",
		"Repair brief manifest digest does not match its canonical contents.",
		"Repair brief manifest digest does not match the externally supplied digest.",
		"Repair evidence packet uses legacy faultline.repair-evidence.v1 witness binding",
		"Repair evidence packet schema validation failed: ",
		"Repair brief schema validation failed: ",
		"Repair brief manifest evidence packet digest does not match evidence-packet.json.",
		"Repair brief manifest digest does not match repair-brief.json.",
		"Stored repair brief is invalid: ",
		`"externalDigestStatus":"MATCH"`,
		`"externalDigestStatus":"MISMATCH"`,
		`"externalDigestStatus":"NOT_PROVIDED"`,
		`{"valid":true`,
	} {
		if !strings.Contains(all, jsjson.Quote(text)[1:len(jsjson.Quote(text))-1]) && !strings.Contains(all, text) {
			t.Errorf("no generated case reached %q", text)
		}
	}
}
