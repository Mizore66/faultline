package jsre

import "testing"

func TestJSCaseFoldingAndWhitespace(t *testing.T) {
	re := MustCompile(`\bsecret\s*=\s*([^\s,;}&]+)`, "gi")
	if re.Test("ſecret=x") { // JS: ſ does not fold to s without the u flag
		t.Fatal("long s must not match")
	}
	m := re.FindAll("x SECRET =　v w")
	if len(m) != 1 || *m[0].Groups[0] != "v" || m[0].Start != 2 || m[0].End != 12 {
		t.Fatalf("%+v", m)
	}
}

func TestLoneSurrogateExtent(t *testing.T) {
	s := "token=a" + string([]byte{0xed, 0xa0, 0x80}) + "b c" // a, lone U+D800, b
	m := MustCompile(`token=([^\s,;}&]+)`, "g").FindAll(s)
	if len(m) != 1 || m[0].End != 9 { // the surrogate is one UTF-16 unit
		t.Fatalf("%+v", m)
	}
}

func TestUndefinedGroup(t *testing.T) {
	m := MustCompile(`a(b)|c(d)`, "g").FindAll("cd ab")
	if len(m) != 2 || m[0].Groups[0] != nil || *m[0].Groups[1] != "d" || *m[1].Groups[0] != "b" || m[1].Groups[1] != nil {
		t.Fatalf("%+v", m)
	}
}

func TestAstralOffsets(t *testing.T) {
	m := MustCompile(`x=([^\s]+)`, "").FindAll("\U0001F600 x=\U0001F600y z")
	if len(m) != 1 || m[0].Start != 3 || m[0].End != 8 || *m[0].Groups[0] != "\U0001F600y" {
		t.Fatalf("%+v", m)
	}
}

func TestKelvinAndDotlessI(t *testing.T) {
	re := MustCompile(`^(?:token|kit)$`, "i")
	for _, s := range []string{"Kit", "kıt", "İT"} {
		if re.Test(s) {
			t.Fatalf("%q matched", s)
		}
	}
	if !re.Test("KiT") || !re.Test("TOKEN") {
		t.Fatal("ASCII case folding")
	}
}

func TestUnsupportedPanics(t *testing.T) {
	for _, c := range []struct{ src, flags string }{
		{`a(?=b)`, ""}, {`a(?!b)`, ""}, {`(?<n>a)`, ""}, {`\1`, ""}, {`a`, "m"}, {`a`, "u"},
		{`a`, "s"}, {`a`, "y"}, {`é`, ""}, {`\x41`, ""}, {`a{`, ""}, {`[\b]`, ""},
		{`a*`, "g"}, {`(?:a|)`, ""}, {`..`, ""}, {`[^a]{2}`, ""}, {`\cA`, ""}, {`[z-a]`, ""}, {`(a`, ""},
		{`a)`, ""}, {`*a`, ""}, {`[a`, ""}, {`\p{L}`, ""}, {`a\`, ""}, {`[\ud800]`, ""},
		// Code units against code points (review round 1): a single wide atom
		// (JS can end or start inside a pair), a lazy wide run at the end, a
		// wide run followed by \B or by a wide atom.
		{`a.`, ""}, {`[^a]b`, ""}, {`x.+?`, ""}, {`x\S`, ""}, {`x.?y`, ""}, {`x.+\B`, ""}, {`x.+.`, ""},
		{`(?:\\.|x)*"`, ""}, {`(?:a.+)+`, ""},
		// Captures inside a repeated group: JS clears them at each iteration.
		{`(?:(a)|b)+`, ""}, {`(a)*b`, ""}, {`(?:x(a)){2}`, ""},
		// \B holds between the two units of a pair (review round 2).
		{`\B.+`, "g"}, {`\B.*x`, "g"}, {`\B(?:\\.|[^"])*x`, "g"}, {`(?:\B|y).+`, "g"}, {`\B[^a]+`, "g"},
		{`\B(?:\\[\s\S]|[^a])+`, "g"}, {`a\B`, ""},
		// A capture in a nullable group under ?: ECMAScript refuses the empty
		// iteration, so the group stays undefined.
		{`"("?)?`, "g"}, {`x(a)?`, ""},
		// A quantified body that can match the empty string (review round 3).
		{`\\(?:\\*|\s)?`, "g"}, {`\w(?:a?|"*)+`, "g"}, {`\\+(?:(?:x|)|\w)+`, "g"}, {`(?:.*?|\s?)?\\`, "g"},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("/%s/%s: no panic", c.src, c.flags)
				}
			}()
			MustCompile(c.src, c.flags)
		}()
	}
}

// Every pattern the slice runs compiles: redaction.ts 109, 144-151, 210;
// sandbox.ts 56-59, 83, 91, 270, 789; autonomous-session.ts 133-138.
func TestPortedPatternsCompile(t *testing.T) {
	for _, p := range [][2]string{
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
		{`\s`, ""},
	} {
		MustCompile(p[0], p[1])
	}
}
