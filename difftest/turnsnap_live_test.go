//go:build faultline_test

package difftest

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/allowlist"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsre"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/redaction"
	"github.com/Mizore66/faultline/internal/turnsnap"
)

// The ignore rules and the secret scan are private in turn-snapshot.ts, so
// both live tests go through its exported planTurnSnapshotPaths with a stub
// Git runner that lists the given paths as tracked (trackedFilesOnly), and
// goPlan runs the Go planner (turnsnap.PlanForTest) with the same stub.
func goPlan(root string, paths []string, skipScan bool) string {
	kept, warnings, err := turnsnap.PlanForTest(root, paths, true, skipScan)
	if err != nil {
		return "THREW:" + jsexc.Message(err)
	}
	return canonicalOf(jsjson.MustObject("paths", stringValues(kept), "warnings", stringValues(warnings)))
}

func stringValues(items []string) jsjson.Value {
	out := make([]jsjson.Value, len(items))
	for i, s := range items {
		out[i] = jsjson.MakeString(s)
	}
	return jsjson.MakeArray(out)
}

func planArgs(root string, paths []string, skipScan bool) string {
	items := make([]jsjson.Value, len(paths))
	for i, p := range paths {
		items[i] = jsjson.MakeString(p)
	}
	return canonicalOf(jsjson.MustObject("root", jsjson.MakeString(root), "paths", jsjson.MakeArray(items), "skipSecretScan", jsjson.MakeBool(skipScan)))
}

// Pieces of ignore rules and paths: ASCII letters, letters whose JS
// case folding differs from RE2's or Go's (K U+212A, ſ, ß, İ, ı, µ/μ/Μ,
// σ/ς/Σ, ÿ/Ÿ), an astral character, regex metacharacters, separators.
var ignorePieces = []string{
	"a", "A", "b", "B", "x", "é", "É", "k", "K", "K", "s", "S", "ſ", "ß", "ss", "SS", "İ", "i", "ı", "I",
	"µ", "μ", "Μ", "σ", "ς", "Σ", "ÿ", "Ÿ", "\U0001F600", ".", "-", "_", "+", "(", ")", "[", "]", "{", "}", "$", "^", "|",
	"\\", "#", " ", "!", "1",
}

var ignoreNames = []string{
	"build", "node_modules", ".env", "id_rsa", "key.pem", "out", "dist", "package.json", "Dockerfile", "Dockerfile.dev",
	"compose.yml", "docker-compose.YAML", "go.sum", "src", "lib", "a.log", ".faultline", "target", "Build", ".git",
}

func ignoreSegment(r *rand.Rand, globs bool) string {
	if r.IntN(4) == 0 {
		return ignoreNames[r.IntN(len(ignoreNames))]
	}
	var b strings.Builder
	for n := 1 + r.IntN(4); n > 0; n-- {
		if globs && r.IntN(4) == 0 {
			b.WriteString(pick(r, "*", "**", "?", "*", "?"))
			continue
		}
		b.WriteString(ignorePieces[r.IntN(len(ignorePieces))])
	}
	return b.String()
}

// ignoreRuleLine is one .faultlineignore line; it adds its segments to
// used, which paths draw from so they meet the rules.
func ignoreRuleLine(r *rand.Rand, used *[]string) string {
	var b strings.Builder
	b.WriteString(pick(r, "", "", "", "", "!", "/", "!/", "#", " ", "\t", "**/"))
	for n := 1 + r.IntN(3); n > 0; n-- {
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "/") && r.IntN(2) == 0 {
			b.WriteString(pick(r, "/", "/", "\\"))
		}
		seg := ignoreSegment(r, true)
		*used = append(*used, seg, strings.NewReplacer("*", "x", "?", "Q").Replace(seg))
		b.WriteString(seg)
	}
	b.WriteString(pick(r, "", "", "", "/", " ", "\r", "/**", "　"))
	return b.String()
}

func ignorePath(r *rand.Rand, used []string) string {
	var b strings.Builder
	b.WriteString(pick(r, "", "", "", "", "./", "/", ".//", "\\"))
	for n := 1 + r.IntN(4); n > 0; n-- {
		if n > 0 && b.Len() > 0 && !strings.HasSuffix(b.String(), "/") {
			b.WriteString(pick(r, "/", "/", "/", "\\"))
		}
		seg := ignoreSegment(r, false)
		if len(used) > 0 && r.IntN(2) == 0 {
			seg = used[r.IntN(len(used))]
		}
		if r.IntN(15) == 0 {
			seg += pick(r, "\n", "\r", " ", "*", "?")
		}
		b.WriteString(seg)
	}
	return b.String()
}

func TestLiveTurnSnapshotIgnore(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(73, 8))
	pairs := 0
	for i := 0; i < 220; i++ {
		dir := t.TempDir()
		ignore := filepath.Join(dir, ".faultlineignore")
		var used []string
		switch r.IntN(40) {
		case 0:
			os.Mkdir(ignore, 0o755)
		case 1:
			os.WriteFile(filepath.Join(dir, "rules"), []byte("x\n"), 0o644)
			os.Symlink("rules", ignore)
		case 2:
			os.WriteFile(ignore, []byte(strings.Repeat("#", 256*1024-1+r.IntN(3))), 0o644)
		case 3:
			if os.Getuid() != 0 {
				os.WriteFile(ignore, []byte("x\n"), 0o000)
				break
			}
			fallthrough
		case 4:
			// no .faultlineignore: the built-in rules alone
		default:
			var lines []string
			for n := 1 + r.IntN(6); n > 0; n-- {
				lines = append(lines, ignoreRuleLine(r, &used))
			}
			sep := pick(r, "\n", "\r\n", "\n")
			os.WriteFile(ignore, []byte(strings.Join(lines, sep)+pick(r, "", sep)), 0o644)
		}
		paths := make([]string, 10)
		for j := range paths {
			paths[j] = ignorePath(r, used)
		}
		pairs += len(paths)
		liveCheck(t, c, "planTurnSnapshotPaths", planArgs(dir, paths, true), goPlan(dir, paths, true))
		os.Chmod(ignore, 0o644)
	}
	outputs := recordedOutputs(t)
	for _, msg := range []string{
		".faultlineignore must be a regular non-symlink file.", ".faultlineignore exceeds FaultLine's 256 KiB parse limit.",
		"Could not read .faultlineignore: ", "Protected environment descriptor remains snapshot-eligible despite .faultlineignore: ",
	} {
		if !strings.Contains(outputs, msg) {
			t.Errorf("no generated case produced %q", msg)
		}
	}
	requireReached(t, "internal/turnsnap/ignore.go", outputs)
	liveSummary(t, "%d (rules, path) pairs in %d rule sets compared", pairs, 220)
}

var entropyTokenSource = jsre.MustCompile(`[A-Za-z0-9_+\-\/=]{40,}`, "g")

// nearEntropyToken is a token whose Shannon entropy is near 4.5: random
// draws from 20 to 32 symbols, or a shuffled 64-character token at exactly
// 4.5 (8 symbols four times, 16 symbols twice) or just off it.
func nearEntropyToken(r *rand.Rand) string {
	const symbols = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_+-="
	perm := r.Perm(len(symbols))
	var b []byte
	if r.IntN(3) == 0 {
		for i := 0; i < 8; i++ {
			b = append(b, strings.Repeat(string(symbols[perm[i]]), 4)...)
		}
		for i := 8; i < 24; i++ {
			b = append(b, strings.Repeat(string(symbols[perm[i]]), 2)...)
		}
		switch r.IntN(3) {
		case 1:
			b[0] = symbols[perm[30]] // one more symbol: just above 4.5
		case 2:
			b[len(b)-1] = b[0] // one fewer pair: just below
		}
		r.Shuffle(len(b), func(i, j int) { b[i], b[j] = b[j], b[i] })
	} else {
		k := 20 + r.IntN(13)
		for n := 40 + r.IntN(30); n > 0; n-- {
			b = append(b, symbols[perm[r.IntN(k)]])
		}
	}
	tok := string(b)
	switch r.IntN(12) {
	case 0:
		tok = pick(r, "sha512-", "SHA256-", "sha1-") + tok
	case 1:
		tok = tok[:20] + "/" + tok[20:]
	case 2:
		tok = randomRun(r, pick(r, "0123456789abcdef", "0123456789ABCDEF", "0123456789abcdefg"), 40+r.IntN(30))
	}
	return tok
}

// scanFile is the content of one generated file.
func scanFile(r *rand.Rand) []byte {
	switch r.IntN(10) {
	case 0: // binary: a NUL in the first 8 KiB, or just after it
		at := pick3(r, 0, 8191, 8192)
		body := []byte(strings.Repeat("x", at) + "\x00" + secretText(r))
		return body
	case 1: // past 256 KiB, a multibyte character straddling the cut
		filler := strings.Repeat(pick(r, "x", "y\n", "é"), turnsnap.SecretScanBytes)
		cut := turnsnap.SecretScanBytes - len(pick(r, "", "é", "\U0001F600", "€"))/2 - 1
		head := []byte(filler)[:max(cut-len("pad "), 0)]
		tail := pick(r, "é", "\U0001F600", "€") + pick(r, " ", "\n") + secretText(r)
		if r.IntN(2) == 0 {
			tail = " " + secretText(r) + " " + tail
		}
		return append(append(head, "pad "...), tail...)
	case 2:
		return nil
	case 3, 4:
		return []byte(pick(r, "value: ", "x = ", "", "\"") + nearEntropyToken(r) + pick(r, "\n", " ", "\"", "") + redactionInput(r))
	}
	return []byte(redactionInput(r))
}

// scanAllowlist builds entries for some of the HIGH occurrences and
// entropy tokens Go finds in the files, digest-bound or not, sometimes
// with a wrong field.
func scanAllowlist(r *rand.Rand, dir string, names []string) string {
	entries := []jsjson.Value{}
	for _, name := range names {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := jsstr.DecodeUTF8(data[:min(len(data), turnsnap.SecretScanBytes)])
		fileDigest, _ := allowlist.FileContentDigest(path, turnsnap.SecretScanBytes)
		type occ struct{ kind, digest string }
		var found []occ
		_, report := redaction.RedactText(text, name)
		for _, o := range report.Occurrences {
			if o.Confidence == "HIGH" {
				found = append(found, occ{o.Kind, o.Digest})
			}
		}
		units := jsstr.ToUTF16(text)
		for _, m := range entropyTokenSource.FindAll(text) {
			found = append(found, occ{pick(r, "HIGH_ENTROPY_TOKEN", "HIGH_ENTROPY_TOKEN", "OPENAI_API_KEY"), "sha256:" + canonical.SHA256Hex(jsstr.FromUTF16(units[m.Start:m.End]))})
		}
		for _, o := range found {
			if r.IntN(5) == 0 {
				continue
			}
			e := jsjson.NewObj()
			e.Set("path", jsjson.MakeString(pick(r, name, name, name, name, "./"+name, strings.ToUpper(name))))
			e.Set("kind", jsjson.MakeString(o.kind))
			e.Set("occurrenceDigest", jsjson.MakeString(o.digest))
			switch r.IntN(4) {
			case 0:
				e.Set("fileDigest", jsjson.MakeString(fileDigest))
			case 1:
				e.Set("fileDigest", jsjson.MakeString("sha256:"+strings.Repeat("0", 64)))
			}
			entries = append(entries, jsjson.MakeObject(e))
		}
	}
	return jsjson.Stringify(jsjson.MustObject("schemaVersion", jsjson.MakeString(allowlist.SchemaVersion), "entries", jsjson.MakeArray(entries)))
}

func TestLiveTurnSnapshotScan(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(73, 9))
	for i := 0; i < 500; i++ {
		dir := t.TempDir()
		var names []string
		for n := 1 + r.IntN(3); n > 0; n-- {
			name := pick(r, "a.txt", "src/b.js", "C.md", "config/app.yaml", "notes", "é.txt") + pick(r, "", "", "2")
			names = append(names, name)
			os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755)
			os.WriteFile(filepath.Join(dir, name), scanFile(r), 0o644)
		}
		switch r.IntN(25) {
		case 0:
			os.Symlink("a.txt", filepath.Join(dir, "link"))
			names = append(names, "link")
		case 1:
			os.WriteFile(filepath.Join(dir, allowlist.Filename), []byte("{"), 0o644)
		case 2:
			os.WriteFile(filepath.Join(dir, ".faultlineignore"), []byte(names[0]+"\n"), 0o644)
		case 3:
			if os.Getuid() != 0 {
				os.Chmod(filepath.Join(dir, names[0]), 0o000)
			}
		}
		if r.IntN(2) == 0 && !nodefs.Exists(filepath.Join(dir, allowlist.Filename)) {
			os.WriteFile(filepath.Join(dir, allowlist.Filename), []byte(scanAllowlist(r, dir, names)), 0o644)
		}
		paths := append(append([]string{}, names...), pick(r, "missing.txt", "src", "a.txt"))
		r.Shuffle(len(paths), func(i, j int) { paths[i], paths[j] = paths[j], paths[i] })
		liveCheck(t, c, "planTurnSnapshotPaths", planArgs(dir, paths, false), goPlan(dir, paths, false))
		os.Chmod(filepath.Join(dir, names[0]), 0o644)
	}
	outputs := recordedOutputs(t)
	counts := map[string]int{}
	for _, msg := range []string{
		"high-confidence secret material detected", "high-entropy secret-like token detected", `{"paths":[`,
		"symbolic link is not snapshot-eligible", "Could not load secret allowlist: ", "EACCES",
		"(rules/kinds: OPENAI_API_KEY", "PRIVATE_KEY_BLOCK", "AUTHORIZATION_BEARER", "AUTHORIZATION_BASIC", "GITHUB_TOKEN",
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
	} {
		counts[msg] = strings.Count(outputs, msg)
		if counts[msg] == 0 {
			t.Errorf("no generated case produced %q", msg)
		}
	}
	requireReached(t, "internal/turnsnap/scan.go", outputs)
	liveSummary(t, "%d repositories compared: %d high-confidence refusals, %d entropy refusals, %d accepted",
		500, counts["high-confidence secret material detected"], counts["high-entropy secret-like token detected"], counts[`{"paths":[`])
}
