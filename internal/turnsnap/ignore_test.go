package turnsnap

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestIgnoreLastMatchWinsAndDescriptorsProtected(t *testing.T) {
	rules := ParseIgnoreLines("build/\n!build/keep.txt\nDockerfile\n")
	if !PathExcluded("build/out.o", rules) || PathExcluded("build/keep.txt", rules) {
		t.Fatal("last match must win")
	}
	if PathExcluded("Dockerfile", rules) {
		t.Fatal("descriptor excluded")
	}
}

func TestParseIgnoreLines(t *testing.T) {
	rules := ParseIgnoreLines(" # c\r\n\t\r\n!\n/\n!/\n  *.LOG  \r\n/src/**/gen?/\nweird\\name.{x}+(y)|$^[z]\n x　\n\\")
	got := []string{}
	for _, r := range rules {
		got = append(got, r.Raw+"|"+r.Source)
	}
	want := []string{
		`*.LOG|(?:^|/)[^/]*\.LOG$`,
		`/src/**/gen?/|^src/.*/gen[^/](?:/.*)?$`,
		`weird\name.{x}+(y)|$^[z]|(?:^|/)weird/name\.\{x\}\+\(y\)\|\$\^\[z\]$`,
		"x|(?:^|/)x$",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !rules[1].DirectoryOnly || rules[0].DirectoryOnly || rules[0].Negated {
		t.Fatal("flags")
	}
	// A body holding the placeholder itself becomes .* as in TS.
	if r := ParseIgnoreLines("a\x00DOUBLE\x00b"); r[0].Source != "(?:^|/)a.*b$" || !IsIgnored("a/x/b", r) {
		t.Fatalf("placeholder: %q", r[0].Source)
	}
}

func TestIgnoreMatching(t *testing.T) {
	cases := []struct {
		rules, path string
		want        bool
	}{
		{"*.pem", "a/b/key.PEM", true},
		{"*.pem", "a/b.pem/c", false},
		{"/top", "top", true},
		{"/top", "a/top", false},
		{"top", "a/top", true},
		{"top", "atop", false},
		{"dir/", "dir", true},
		{"dir/", "x/dir/y/z", true},
		{"dir/", "dir/a\nb", false},
		{"dir/", "dir/a b", false},
		{"a?c", "abc", true},
		{"a?c", "a/c", false},
		{"a*c", "ab/c", false},
		{"a**c", "ab/c", true},
		{"a**c", "ab\rc", false},
		{"a/**/b", "a/x/y/b", true},
		{"a/**/b", "a//b", true},
		{"a/**/b", "a/b", false},
		{"**/x", "x", false},
		{"**/x", "q/x", true},
		{"./a", "a", false},
		{".env", "./.env", true},
		{".env", "\\x\\.env", true},
		{"src\\gen", "src/gen", true},
		// Case folding is ECMAScript Canonicalize without u: K (U+212A)
		// and ſ (U+017F) do not fold to ASCII, µ and μ share Μ, ÿ and Ÿ fold.
		{"k", "K", false},
		{"s", "ſ", false},
		{"µ", "μ", true},
		{"ÿ", "Ÿ", true},
		{"straße", "STRASSE", false},
		{"İ", "i", false},
		{"\U0001F600", "\U0001F600", true},
		{"?", "\U0001F600", false},
		{"??", "\U0001F600", true},
		{"!x\nx", "x", true},
		{"x\n!x", "x", false},
		{"x\n!y", "x", true},
	}
	for _, c := range cases {
		if got := IsIgnored(c.path, ParseIgnoreLines(c.rules)); got != c.want {
			t.Errorf("rules %q path %q: got %v", c.rules, c.path, got)
		}
	}
}

func TestDefaultRulesAndDescriptors(t *testing.T) {
	rules := DefaultRules()
	for path, want := range map[string]bool{
		".faultline/x": true, "a/node_modules/b": true, ".env": true, "x/.env.local": true, "k.PEM": true,
		"id_rsa.pub": true, "src/main.go": false, "Build/x": true, "x.log": true, ".git/config": true, "envs": false,
	} {
		if got := IsIgnored(path, rules); got != want {
			t.Errorf("%s: %v", path, got)
		}
	}
	for path, want := range map[string]bool{
		"package.json": true, "a/b/go.sum": true, "dockerfile.dev": true, "x/DOCKERFILE": true, "Dockerfile.": false,
		"docker-compose.YML": true, "compose.yaml": true, "compose.json": false, "Package.json": false, "\\a\\yarn.lock": true,
	} {
		if got := IsProtectedDescriptor(path); got != want {
			t.Errorf("descriptor %s: %v", path, got)
		}
	}
	if PathExcluded("node_modules/package.json", rules) || !PathExcluded("node_modules/x.js", rules) {
		t.Fatal("protected descriptor under an ignored directory")
	}
}

// TestIgnoreAgainstRE2 compares the matcher with RE2 on ASCII rules and
// paths without CR (where RE2's (?i) and . agree with JS).
func TestIgnoreAgainstRE2(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 7))
	pieces := []string{"a", "B", "b", "/", "*", "**", "?", ".", "x", "-", "(", "[", "$"}
	for i := 0; i < 20000; i++ {
		var line strings.Builder
		if r.IntN(2) == 0 {
			line.WriteString(pieces[r.IntN(len(pieces))])
		}
		for n := 1 + r.IntN(5); n > 0; n-- {
			line.WriteString(pieces[r.IntN(len(pieces))])
		}
		rules := ParseIgnoreLines(line.String())
		if len(rules) == 0 {
			continue
		}
		re := regexp.MustCompile("(?i)" + rules[0].Source)
		for j := 0; j < 10; j++ {
			var path strings.Builder
			for n := r.IntN(8); n > 0; n-- {
				path.WriteString(string("aAbBx/./-\n"[r.IntN(10)]))
			}
			raw := path.String()
			if got, want := IsIgnored(raw, rules), re.MatchString(NormalizePath(raw)); got != want {
				t.Fatalf("rule %q (%s) path %q: got %v want %v", line.String(), rules[0].Source, raw, got, want)
			}
		}
	}
}

func TestLoadIgnoreRules(t *testing.T) {
	dir := t.TempDir()
	rules, err := LoadIgnoreRules(dir)
	if err != nil || len(rules) != len(DefaultIgnorePatterns) {
		t.Fatalf("defaults: %d %v", len(rules), err)
	}
	os.WriteFile(filepath.Join(dir, ".faultlineignore"), []byte("a\r\n!b\n"), 0o644)
	rules, err = LoadIgnoreRules(dir)
	if err != nil || len(rules) != len(DefaultIgnorePatterns)+2 || rules[len(rules)-1].Raw != "!b" {
		t.Fatalf("user rules: %v", err)
	}
	os.WriteFile(filepath.Join(dir, ".faultlineignore"), make([]byte, 256*1024+1), 0o644)
	if _, err := LoadIgnoreRules(dir); err == nil || err.Error() != ".faultlineignore exceeds FaultLine's 256 KiB parse limit." {
		t.Fatalf("oversize: %v", err)
	}
	os.Remove(filepath.Join(dir, ".faultlineignore"))
	os.Mkdir(filepath.Join(dir, ".faultlineignore"), 0o755)
	if _, err := LoadIgnoreRules(dir); err == nil || err.Error() != ".faultlineignore must be a regular non-symlink file." {
		t.Fatalf("directory: %v", err)
	}
	dir = t.TempDir()
	os.WriteFile(filepath.Join(dir, "t"), nil, 0o644)
	if os.Symlink("t", filepath.Join(dir, ".faultlineignore")) == nil {
		if _, err := LoadIgnoreRules(dir); err == nil || err.Error() != ".faultlineignore must be a regular non-symlink file." {
			t.Fatalf("symlink: %v", err)
		}
	}
	// chmod 000 denies reads only on POSIX: on Windows it sets the read-only
	// attribute, and the file stays readable (for Node too).
	if os.Getuid() != 0 && runtime.GOOS != "windows" {
		dir = t.TempDir()
		path := filepath.Join(dir, ".faultlineignore")
		os.WriteFile(path, []byte("x"), 0o000)
		if _, err := LoadIgnoreRules(dir); err == nil || err.Error() != "Could not read .faultlineignore: EACCES: permission denied, open '"+path+"'" {
			t.Fatalf("unreadable: %v", err)
		}
	}
}
