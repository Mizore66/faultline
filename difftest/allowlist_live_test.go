//go:build faultline_test

package difftest

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/gen"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/allowlist"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/redaction"
)

var (
	allowlistPaths  = []string{"a.txt", "A.txt", "src/a.txt", "café.env", "\U0001F600/x", "a.txt ", "./a.txt"}
	allowlistKinds  = []string{"OPENAI_API_KEY", "GITHUB_TOKEN", "AWS_ACCESS_KEY_ID", "HIGH_ENTROPY_TOKEN", "PRIVATE_KEY_BLOCK", "openai_api_key"}
	allowlistTokens = []string{"tok-one", "tok-two", "Tök", "", "tok-one "}
)

func tokenDigest(token string) string { return "sha256:" + canonical.SHA256Hex(token) }

// allowlistDigestPick is a digest an entry or a query may carry: one of the
// tokens' digests mostly, sometimes malformed.
func allowlistDigestPick(r *rand.Rand, bad bool) string {
	if !bad {
		return tokenDigest(allowlistTokens[r.IntN(len(allowlistTokens))])
	}
	switch r.IntN(6) {
	case 0:
		return "sha256:" + strings.Repeat("A", 64)
	case 1:
		return "sha256:" + strings.Repeat("a", 63)
	case 2:
		return ""
	}
	return tokenDigest(allowlistTokens[r.IntN(len(allowlistTokens))])
}

// allowlistEntry is one entry; a bad one may break any schema rule.
func allowlistEntry(r *rand.Rand, bad bool) jsjson.Value {
	e := jsjson.NewObj()
	odd := func(n int) bool { return bad && r.IntN(n) == 0 }
	set := func(k string, v jsjson.Value) {
		if !odd(10) { // drop a key
			e.Set(k, v)
		}
	}
	path := allowlistPaths[r.IntN(len(allowlistPaths))]
	switch {
	case odd(3):
		switch r.IntN(3) {
		case 0:
			path = ""
		case 1:
			path = strings.Repeat("p", 4096+r.IntN(2))
		case 2:
			path = strings.Repeat("\U0001F600", 2048+r.IntN(2)) // 4,096 or 4,098 UTF-16 units
		}
	case r.IntN(30) == 0:
		path = strings.Repeat("p", 4096)
	}
	set("path", jsjson.MakeString(path))
	kind := allowlistKinds[r.IntN(len(allowlistKinds))]
	if odd(5) {
		kind = strings.Repeat("K", pick3(r, 0, 129, 128))
	}
	set("kind", jsjson.MakeString(kind))
	set("occurrenceDigest", jsjson.MakeString(allowlistDigestPick(r, odd(4))))
	if r.IntN(2) == 0 {
		e.Set("fileDigest", jsjson.MakeString(allowlistDigestPick(r, odd(4))))
	}
	if r.IntN(3) == 0 {
		n := pick3(r, 1, 512, 20)
		if odd(4) {
			n = pick3(r, 0, 513, 513)
		}
		e.Set("note", jsjson.MakeString(strings.Repeat("n", n)))
	}
	if odd(10) {
		e.Set("extra", jsjson.MakeBool(true))
	}
	return jsjson.MakeObject(e)
}

// allowlistDocument is the text of a generated .faultline-secret-allowlist.json.
func allowlistDocument(r *rand.Rand) string {
	bad := r.IntN(3) == 0
	doc := jsjson.NewObj()
	keys := []string{"schemaVersion", "entries"}
	if r.IntN(4) == 0 {
		keys = []string{"entries", "schemaVersion"}
	}
	for _, k := range keys {
		switch k {
		case "schemaVersion":
			if !bad || r.IntN(5) != 0 {
				doc.Set(k, jsjson.MakeString(pick(r, allowlist.SchemaVersion, allowlist.SchemaVersion, allowlist.SchemaVersion, map[bool]string{true: "faultline.secret-allowlist.v2", false: allowlist.SchemaVersion}[bad])))
			}
		case "entries":
			n := r.IntN(6)
			if r.IntN(60) == 0 {
				n = 2048 + r.IntN(2)
			}
			entries := make([]jsjson.Value, n)
			for i := range entries {
				entries[i] = allowlistEntry(r, bad)
			}
			doc.Set(k, jsjson.MakeArray(entries))
		}
	}
	if bad && r.IntN(8) == 0 {
		doc.Set("extra", jsjson.MakeNull())
	}
	text := jsjson.StringifyIndent(jsjson.MakeObject(doc), pick(r, "", "  "))
	switch r.IntN(30) {
	case 0:
		return gen.CorruptDeep(r, text)
	case 1:
		return pick(r, "", "[]", "null", "{", "\ufeff{}", "{\"schemaVersion\":\"faultline.secret-allowlist.v1\",\"entries\":[]}\n\xff", "1e999")
	}
	return text
}

// loadedAllowlist is the Go counterpart of the loadSecretAllowlist op.
func pick3(r *rand.Rand, a, b, c int) int { return []int{a, b, c}[r.IntN(3)] }

func loadedAllowlist(dir string) (allowlist.Allowlist, string) {
	a, err := allowlist.Load(dir)
	if err != nil {
		return a, "THREW:" + jsexc.Message(err)
	}
	return a, allowlist.ConfigDigest(a) + " " + jsjson.Stringify(a.Value())
}

// writeAllowlistFile places the document, or one of the refused shapes.
func writeAllowlistFile(t *testing.T, r *rand.Rand, dir string) {
	path := filepath.Join(dir, allowlist.Filename)
	switch r.IntN(25) {
	case 0:
		return // missing
	case 1:
		os.WriteFile(filepath.Join(dir, "target.json"), []byte(allowlistDocument(r)), 0o644)
		os.Symlink("target.json", path)
	case 2:
		os.Mkdir(path, 0o755)
	case 3:
		os.WriteFile(path, []byte(strings.Repeat(" ", 256*1024-1+r.IntN(3))+"{}"), 0o644)
	case 4:
		if os.Getuid() != 0 {
			os.WriteFile(path, []byte("{}"), 0o000)
			return
		}
		fallthrough
	default:
		if err := os.WriteFile(path, []byte(allowlistDocument(r)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLiveAllowlist(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(73, 7))
	parsed, queries := 0, 0
	for i := 0; i < 1000; i++ {
		dir := t.TempDir()
		writeAllowlistFile(t, r, dir)
		a, got := loadedAllowlist(dir)
		liveCheck(t, c, "loadSecretAllowlist", `{"dir":`+jsjson.Quote(dir)+"}", got)
		os.Chmod(filepath.Join(dir, allowlist.Filename), 0o644)
		if strings.HasPrefix(got, "THREW:") {
			continue
		}
		parsed++
		doc := jsjson.Stringify(a.Value())
		for q := 0; q < 4; q++ {
			queries++
			path := allowlistPaths[r.IntN(len(allowlistPaths))]
			fileDigest := allowlistDigestPick(r, r.IntN(10) == 0)
			var occ []redaction.Occurrence
			occJSON := []jsjson.Value{}
			for n := r.IntN(6); n > 0; n-- {
				o := redaction.Occurrence{
					Kind:       allowlistKinds[r.IntN(len(allowlistKinds))],
					Confidence: pick(r, "HIGH", "HIGH", "LIMITED"),
					Digest:     allowlistDigestPick(r, r.IntN(10) == 0),
				}
				occ = append(occ, o)
				occJSON = append(occJSON, jsjson.MustObject("kind", jsjson.MakeString(o.Kind), "confidence", jsjson.MakeString(o.Confidence), "digest", jsjson.MakeString(o.Digest)))
			}
			left := allowlist.FilterHighOccurrences(occ, fileDigest, path, a)
			high := 0
			for _, o := range occ {
				if o.Confidence == "HIGH" {
					high++
				}
			}
			kinds := []jsjson.Value{}
			for _, k := range allowlist.Kinds(left) {
				kinds = append(kinds, jsjson.MakeString(k))
			}
			result := jsjson.NewObj()
			result.Set("remainingKinds", jsjson.MakeArray(kinds))
			result.Set("allowlistedCount", jsjson.MakeNumber(float64(high-len(left))))
			common := `"allowlist":` + doc + `,"path":` + jsjson.Quote(path) + `,"fileDigest":` + jsjson.Quote(fileDigest)
			liveCheck(t, c, "filterAllowlistedHighOccurrences", "{"+common+`,"occurrences":`+jsjson.Stringify(jsjson.MakeArray(occJSON))+"}", canonicalOf(jsjson.MakeObject(result)))
			token := allowlistTokens[r.IntN(len(allowlistTokens))]
			want := "false"
			if allowlist.IsEntropyTokenAllowlisted(token, fileDigest, path, a) {
				want = "true"
			}
			liveCheck(t, c, "isEntropyTokenAllowlisted", "{"+common+`,"token":`+jsjson.Quote(token)+"}", want)
		}
	}
	outputs := recordedOutputs(t)
	for _, msg := range []string{
		"must be a regular non-symlink file.", "exceeds FaultLine's 256 KiB parse limit.", "EACCES", "Unexpected",
		`"code": "too_big"`, `"code": "too_small"`, `"code": "unrecognized_keys"`, `"code": "invalid_literal"`, "\ntrue\n", "\nfalse\n",
	} {
		if !strings.Contains(outputs, msg) {
			t.Errorf("no generated case produced %q", msg)
		}
	}
	liveSummary(t, "%d allowlist files (%d parsed), %d match queries compared", 1000, parsed, queries)
}
