package difftest

import (
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsre"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/redaction"
)

// livePatterns is every regular expression the ported modules run:
// redaction.ts 109, 144-151, 210; sandbox.ts 56-59, 83, 91, 270, 789;
// autonomous-session.ts 133-138.
var livePatterns = [][2]string{
	{`^<FAULTLINE_REDACTED:[A-Z_]+:sha256:[a-f0-9]{64}>$`, ""},
	{`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z0-9 ]*PRIVATE KEY-----`, "g"},
	{`\bBearer[ \t]+([A-Za-z0-9\-._~+/]+=*)`, "gi"},
	{`\bBasic[ \t]+([A-Za-z0-9+/]{4,}={0,2})`, "gi"},
	{`\bsk-(?:proj-|svcacct-)?[A-Za-z0-9_-]{16,}\b`, "g"},
	{`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}\b|\bgithub_pat_[A-Za-z0-9_]{20,}\b`, "g"},
	{`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`, "g"},
	{`\b(?:aws[-_])?secret[-_]?access[-_]?key\s*(?:=|:)\s*(?:"((?:\\.|[^"\\\r\n])*)"|'((?:\\.|[^'\\\r\n])*)'|([^\s,;}&]+))`, "gi"},
	{`\b(?:api[-_]?key|apikey|secret(?:[-_]?key)?|token|access[-_]?token|refresh[-_]?token|id[-_]?token|password|passwd|pwd|credential(?:s)?|authorization|access[-_]?key|private[-_]?key|client[-_]?secret|session[-_]?token)\b\s*(?:=|:)\s*(?:"((?:\\.|[^"\\\r\n])*)"|'((?:\\.|[^'\\\r\n])*)'|([^\s,;}&]+))`, "gi"},
	{`^(?:null|undefined|true|false)$`, "i"},
	{`(api[_-]?key|auth|bearer|cookie|credential|passwd|password|private[_-]?key|secret|session|token)`, "i"},
	{`^[A-Z_][A-Z0-9_]*$`, ""},
	{`^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[a-f0-9]{64}$`, ""},
	{`^sha256:[a-f0-9]{64}$`, ""},
	{`(?:cannot connect to the docker daemon|is the docker daemon running|error during connect|docker daemon is not running|error response from daemon|unable to find image|pull access denied|no such image)`, "i"},
	{`(?:SyntaxError|TS\d{4}\s*:|error\s+TS\d{4}|failed to compile|error\[E\d{4}\]|could not compile ` + "`" + `|cargo:.*error|rustc.*error:|ModuleNotFoundError|ImportError:\s*No module named)`, "i"},
	{`[\r\n,]`, ""},
	{`(?:command not found|not found|no such file|cannot find module|permission denied)`, "i"},
	{`\r?\n`, "g"},
	{`(?:^|\b)(?:run|suggested|try|command)\s*[:\-]\s*(.+)$`, "i"},
	{"`([^`]+)`", ""},
}

// Pieces that the patterns care about, and the characters where JS and RE2
// semantics differ: Unicode spaces (\s), ſ, K (U+212A), İ and ı (case
// folding), lone surrogates and an astral character (code units vs code
// points), CRLF and U+2028 (.).
var regexPieces = []string{
	"secret", "SECRET", "Secret", "ſecret", "to\u212aen", "token", "TOKEN", "İ", "ı", "aws_secret_access_key",
	"AWS-SECRET-ACCESS-KEY", "secretaccesskey", "api-key", "API_KEY", "apikey", "password", "pwd", "credentials",
	"authorization", "client_secret", "session-token", "Bearer", "bearer", "BASIC", "Basic", "sk-", "sk-proj-",
	"sk-svcacct-", "ghp_", "github_pat_", "AKIA", "ASIA", "-----BEGIN RSA PRIVATE KEY-----", "-----END RSA PRIVATE KEY-----",
	"-----BEGIN PRIVATE KEY-----", "-----END PRIVATE KEY-----", "=", ":", "-", "\"", "'", "\\", "\\\"", ",", ";", "}", "&", "/", "+", "==",
	" ", "  ", "\t", "\r\n", "\n", "\r", "\u2028", "\u2029", "\u00a0", "\u3000", "\ufeff", "\u1680", "\u2005", "\u202f",
	"\u205f", "\u0085", "\u200b", "\U0001F600", "\ufffd", "\xed\xa0\x80", "\xed\xb0\x80", "null", "TRUE", "undefined",
	"run:", "Try -", "command:", "suggested :", "`", "TS2345:", "error TS1005", "error[E0425]", "cargo: x error",
	"rustc: error:", "ModuleNotFoundError", "ImportError: No module named", "SyntaxError", "auth", "cookie",
	"private_key", "Docker daemon is not running", "Error response from daemon", "command not found", "Permission denied",
	"@sha256:", "sha256:", "<FAULTLINE_REDACTED:OPENAI_API_KEY:sha256:", ">", "_", "A_B9",
}

func randomRun(r *rand.Rand, alphabet string, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[r.IntN(len(alphabet))]
	}
	return string(b)
}

const (
	alnum   = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	upperD  = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	hexDigs = "0123456789abcdef"
)

// anchoredInput is a whole-string candidate for the anchored patterns,
// mutated half the time by inserting a piece.
func anchoredInput(r *rand.Rand) string {
	s := pick(r,
		"<FAULTLINE_REDACTED:"+pick(r, "OPENAI_API_KEY", "GENERIC_SECRET_ASSIGNMENT", "Kind", "")+":sha256:"+randomRun(r, hexDigs, 63+r.IntN(3))+">",
		pick(r, "ghcr.io/o/img:1.2", "node", "_x", "a/b-c")+"@sha256:"+randomRun(r, hexDigs, 63+r.IntN(3)),
		"sha256:"+randomRun(r, hexDigs, 63+r.IntN(3)),
		randomRun(r, "ABCXYZ_019", 1+r.IntN(12)),
		pick(r, "null", "Undefined", "TRUE", "false", "nuLL"),
		"`"+pick(r, "npm test", "x", "go  vet ./...", "")+"`",
	)
	if r.IntN(2) == 0 {
		at := r.IntN(len(s) + 1)
		s = s[:at] + regexPieces[r.IntN(len(regexPieces))] + s[at:]
	}
	return s
}

func regexInput(r *rand.Rand) string {
	if r.IntN(4) == 0 {
		return jsstr.FromUTF16(jsstr.ToUTF16(anchoredInput(r)))
	}
	var b strings.Builder
	for n := 1 + r.IntN(12); n > 0; n-- {
		switch r.IntN(8) {
		case 6:
			b.WriteString(secretText(r))
		case 7:
			b.WriteString(anchoredInput(r))
		case 0:
			b.WriteString(randomRun(r, alnum+"_-.~+/", r.IntN(30)))
		case 1:
			b.WriteString(randomRun(r, upperD, 12+r.IntN(8)))
		case 2:
			b.WriteString(randomRun(r, hexDigs, 60+r.IntN(8)))
		default:
			b.WriteString(regexPieces[r.IntN(len(regexPieces))])
		}
	}
	// Adjacent lone halves that form a pair become one code point.
	return jsstr.FromUTF16(jsstr.ToUTF16(b.String()))
}

func matchesJSON(ms []jsre.Match) string {
	items := make([]jsjson.Value, len(ms))
	for i, m := range ms {
		o := jsjson.NewObj()
		o.Set("start", jsjson.MakeNumber(float64(m.Start)))
		o.Set("end", jsjson.MakeNumber(float64(m.End)))
		groups := make([]jsjson.Value, len(m.Groups))
		for g, s := range m.Groups {
			groups[g] = jsjson.MakeNull()
			if s != nil {
				groups[g] = jsjson.MakeString(*s)
			}
		}
		o.Set("groups", jsjson.MakeArray(groups))
		items[i] = jsjson.MakeObject(o)
	}
	return jsjson.Stringify(jsjson.MakeArray(items))
}

func TestLiveJSRegex(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	for pi, p := range livePatterns {
		re := jsre.MustCompile(p[0], p[1])
		r := rand.New(rand.NewPCG(41, uint64(pi)))
		matched := 0
		for i := 0; i < 5000; i++ {
			text := regexInput(r)
			got := re.FindAll(text)
			args := `{"source":` + jsjson.Quote(p[0]) + `,"flags":` + jsjson.Quote(p[1]) + `,"text":` + jsjson.Quote(text) + "}"
			want, err := c.Call("jsreExec", args)
			if err != nil {
				t.Fatal(err)
			}
			if g := matchesJSON(got); g != want {
				t.Fatalf("/%s/%s on %s:\ngot  %s\nwant %s", p[0], p[1], jsjson.Quote(text), g, want)
			}
			if len(got) > 0 {
				matched++
			}
		}
		t.Logf("/%s/%s: %d of 5000 inputs matched", p[0], p[1], matched)
		if matched == 0 {
			t.Errorf("/%s/%s: no generated input matched", p[0], p[1])
		}
	}
}

// secretText builds one credential in one of the forms the detectors know,
// or a near miss.
func secretText(r *rand.Rand) string {
	quoteBody := func() string {
		s := randomRun(r, alnum+"!@#$%^*()", 4+r.IntN(20))
		if r.IntN(3) == 0 {
			s += `\"` + randomRun(r, alnum, 3)
		}
		return s
	}
	switch r.IntN(16) {
	case 0:
		return "sk-" + pick(r, "", "proj-", "svcacct-") + randomRun(r, alnum+"_-", 12+r.IntN(30))
	case 1:
		return pick(r, "ghp", "gho", "ghu", "ghs", "ghr") + "_" + randomRun(r, alnum, 16+r.IntN(20))
	case 2:
		return "github_pat_" + randomRun(r, alnum+"_", 16+r.IntN(30))
	case 3:
		return pick(r, "AKIA", "ASIA", "akia") + randomRun(r, upperD, 14+r.IntN(4))
	case 4:
		return pick(r, "Authorization: ", "", "proxy-authorization: ") + pick(r, "Bearer", "bearer", "BEARER") + pick(r, " ", "\t", "  ") +
			randomRun(r, alnum+"-._~+/", 1+r.IntN(40)) + strings.Repeat("=", r.IntN(3))
	case 5:
		return pick(r, "Basic", "basic") + pick(r, " ", "\t") + randomRun(r, alnum+"+/", 2+r.IntN(30)) + strings.Repeat("=", r.IntN(4))
	case 6:
		nl := pick(r, "\n", "\r\n")
		kind := pick(r, "RSA ", "", "EC ", "OPENSSH ")
		block := "-----BEGIN " + kind + "PRIVATE KEY-----" + nl + randomRun(r, alnum+"+/", 20+r.IntN(40)) + nl
		if r.IntN(5) > 0 {
			block += "-----END " + kind + "PRIVATE KEY-----"
		}
		return block
	case 7: // JSON
		return `{"` + pick(r, "password", "apiKey", "api_key", "token", "client_secret", "aws_secret_access_key", "name") + `": "` + quoteBody() + `"}`
	case 8: // YAML
		return pick(r, "password", "secret_key", "token", "credentials", "id-token") + ": '" + randomRun(r, alnum, 4+r.IntN(16)) + "'"
	case 9: // env
		return pick(r, "export ", "") + pick(r, "API_KEY", "SECRET", "AWS_SECRET_ACCESS_KEY", "SESSION_TOKEN", "PWD") + "=" + randomRun(r, alnum+"/+", 1+r.IntN(30))
	case 10: // URL
		return "https://example.test/cb?" + pick(r, "token", "access_token", "apikey", "q") + "=" + randomRun(r, alnum, 1+r.IntN(20)) + "&next=" + randomRun(r, alnum, 3)
	case 11: // overlapping detectors
		return pick(r, "token=", "password: ", "secret = \"", "Bearer ") + "sk-proj-" + randomRun(r, alnum, 16+r.IntN(10))
	case 12:
		return pick(r, "password", "token", "secret") + pick(r, "=", ": ") + pick(r, "null", "TRUE", "undefined", `""`, "''", "false")
	case 13:
		return "token=<FAULTLINE_REDACTED:OPENAI_API_KEY:sha256:" + randomRun(r, hexDigs, 64) + ">"
	case 14:
		return "aws_secret_access_key = \"" + quoteBody() + "\"" + pick(r, "\r\n", "\n", " ")
	}
	return regexInput(r)
}

func redactionInput(r *rand.Rand) string {
	var b strings.Builder
	for n := 1 + r.IntN(6); n > 0; n-- {
		if r.IntN(2) == 0 {
			b.WriteString(secretText(r))
		} else {
			b.WriteString(regexPieces[r.IntN(len(regexPieces))])
			b.WriteString(randomRun(r, alnum+" ", r.IntN(10)))
		}
	}
	return jsstr.FromUTF16(jsstr.ToUTF16(b.String()))
}

var fieldNames = []string{
	"password", "Password", "API-KEY", "x-api-key", "apiKey", "secret", "client.secret", "AWS_SECRET_ACCESS_KEY",
	"awsAccessKeyId", "access_key_id", "accessKey", "sessionToken", "myToken", "privateKey", "credentials", "pwd",
	"authorization", "Proxy-Authorization", "İtoken", "toKen", "name", "id", "__proto__", "constructor", "0", "10",
	"2", "note", "ſecret", "data", "items", "a.b", "é", "\xed\xa0\x80key",
}

func redactionValue(r *rand.Rand, depth int) string {
	k := r.IntN(10)
	if depth > 3 {
		k = 9
	}
	switch {
	case k < 3:
		var parts []string
		for n := r.IntN(6); n > 0; n-- {
			parts = append(parts, jsjson.Quote(fieldNames[r.IntN(len(fieldNames))])+":"+redactionValue(r, depth+1))
		}
		return "{" + strings.Join(parts, ",") + "}"
	case k == 3:
		var parts []string
		for n := r.IntN(4); n > 0; n-- {
			parts = append(parts, redactionValue(r, depth+1))
		}
		return "[" + strings.Join(parts, ",") + "]"
	case k == 4:
		return pick(r, "null", "true", "false", "0", "-1.5", "1e21")
	case k == 5:
		return jsjson.Quote("<FAULTLINE_REDACTED:GENERIC_SECRET_ASSIGNMENT:sha256:" + randomRun(r, hexDigs, 64) + ">")
	case k == 6:
		return jsjson.Quote(randomRun(r, alnum, r.IntN(12)))
	}
	return jsjson.Quote(redactionInput(r))
}

func TestLiveRedaction(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(43, 1))
	kinds := map[string]int{}
	for i := 0; i < 3000; i++ {
		text := redactionInput(r)
		location := pick(r, "$", "$.run.stdout", "corpus:a")
		out, report := redaction.RedactText(text, location)
		for _, k := range report.Kinds {
			kinds[k]++
		}
		o := jsjson.NewObj()
		o.Set("value", jsjson.MakeString(out))
		o.Set("report", jsjson.MakeString(canonicalOf(report.JSON())))
		want, err := c.Call("redactText", `{"text":`+jsjson.Quote(text)+`,"location":`+jsjson.Quote(location)+"}")
		if err != nil {
			t.Fatal(err)
		}
		if got := jsjson.Stringify(jsjson.MakeObject(o)); got != want {
			t.Fatalf("redactText(%s):\ngot  %s\nwant %s", jsjson.Quote(text), got, want)
		}
	}
	t.Logf("redactText kinds: %v", kinds)
	if len(kinds) != 8 {
		t.Errorf("not every detector fired: %v", kinds)
	}
	fields := 0
	for i := 0; i < 1000; i++ {
		text := redactionValue(r, 0)
		value, err := jsjson.Parse(text)
		if err != nil {
			t.Fatalf("%s: %v", text, err)
		}
		out, report := redaction.RedactValue(value, "$")
		for _, oc := range report.Occurrences {
			if !strings.HasSuffix(oc.Location, "]") {
				fields++
			}
		}
		o := jsjson.NewObj()
		o.Set("value", jsjson.MakeString(jsjson.Stringify(out)))
		o.Set("report", jsjson.MakeString(canonicalOf(report.JSON())))
		want, err := c.Call("redactValue", `{"text":`+jsjson.Quote(text)+"}")
		if err != nil {
			t.Fatal(err)
		}
		if got := jsjson.Stringify(jsjson.MakeObject(o)); got != want {
			t.Fatalf("redactValue(%s):\ngot  %s\nwant %s", text, got, want)
		}
	}
	t.Logf("redactValue: %d whole-field redactions", fields)
	if fields == 0 {
		t.Error("no whole-field redaction was reached")
	}
}

func pick(r *rand.Rand, items ...string) string { return items[r.IntN(len(items))] }
