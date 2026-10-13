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

// Every pattern the ported modules use translates (redaction.ts 109, 144-151,
// 210; sandbox.ts 56, 83, 91, 270, 789; autonomous-session.ts 133-138).
func TestPortedPatternsCompile(t *testing.T) {
	for _, p := range [][2]string{
		{`^<FAULTLINE_REDACTED:[A-Z_]+:sha256:[a-f0-9]{64}>$`, ""},
		{`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z0-9 ]*PRIVATE KEY-----`, "g"},
		{`\bBearer[ \t]+([A-Za-z0-9\-._~+/]+=*)`, "gi"},
		{`^(?:null|undefined|true|false)$`, "i"},
		{`(?:^|\b)(?:run|suggested|try|command)\s*[:\-]\s*(.+)$`, "i"},
		{"`([^`]+)`", ""},
		{`\r?\n`, "g"},
		{`(?:SyntaxError|TS\d{4}\s*:|error\s+TS\d{4}|failed to compile|error\[E\d{4}\]|could not compile ` + "`" + `|cargo:.*error|rustc.*error:|ModuleNotFoundError|ImportError:\s*No module named)`, "i"},
	} {
		MustCompile(p[0], p[1])
	}
}
