// Package turnsnap ports the capture side of src/turn-snapshot.ts: the
// path policy (built-in and .faultlineignore rules, protected environment
// descriptors) and the pre-blob secret scan here, the capture itself in
// capture.go.
package turnsnap

import (
	"os"
	"slices"
	"strings"

	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsre"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// Port of src/turn-snapshot.ts:51-54.
const (
	// SnapshotObjectDir is relative to the repository .git directory.
	SnapshotObjectDir = "faultline/objects"
	// SnapshotAlternatesEntry is relative to .git/objects.
	SnapshotAlternatesEntry = "../faultline/objects"
	SnapshotVersion         = "faultline.turn-tree-snapshot.v1"
)

// Hard caps enforced before any blob is written (src/turn-snapshot.ts:91-94).
const (
	MaxFileBytes    = 1_048_576
	MaxTotalBytes   = 32 * 1_048_576
	MaxFileCount    = 2_000
	SecretScanBytes = 256 * 1024
)

// Port of src/turn-snapshot.ts:152-158.
const (
	QuiescenceDelayMs     = 50
	MaxQuiescenceAttempts = 4
)

// StatusArgs ports src/turn-snapshot.ts:158 STATUS_ARGS.
var StatusArgs = []string{"status", "--porcelain=v1", "--untracked-files=all", "-z"}

// DefaultIgnorePatterns ports src/turn-snapshot.ts:100
// TURN_SNAPSHOT_DEFAULT_IGNORE_PATTERNS (gitignore semantics, applied before
// .faultlineignore).
var DefaultIgnorePatterns = []string{
	".faultline/",
	"node_modules/",
	".env",
	".env.*",
	"*.pem",
	"*.key",
	"*.pfx",
	"id_rsa",
	"id_rsa.*",
	"dist/",
	"build/",
	"out/",
	".next/",
	".nuxt/",
	".turbo/",
	".cache/",
	"coverage/",
	"target/",
	"__pycache__/",
	"*.pyc",
	".venv/",
	"venv/",
	"vendor/",
	"tmp/",
	"temp/",
	"*.log",
	".git/",
}

// ignoreParseLimit is the 256 KiB cap of src/turn-snapshot.ts:668.
const ignoreParseLimit = 256 * 1024

// Error is TurnSnapshotError (src/turn-snapshot.ts:83).
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

// Rule is one parsed ignore line (src/turn-snapshot.ts:354 IgnoreRule).
// Source is the JS regular expression source TS builds (flag i).
type Rule struct {
	Raw           string
	Negated       bool
	DirectoryOnly bool
	Source        string
	anchored      bool
	body          []token
}

// A token of a rule body: one literal code unit (compared after case
// folding), [^/], [^/]* or .*.
type tokenKind uint8

const (
	tLit tokenKind = iota
	tOneNonSlash
	tNonSlashRun
	tAnyRun
)

type token struct {
	kind tokenKind
	unit uint16 // tLit: canonical (folded) unit
}

// NormalizePath ports src/turn-snapshot.ts:368 normalizeRepoRelativePath.
func NormalizePath(value string) string {
	value = strings.ReplaceAll(value, `\`, "/")
	if strings.HasPrefix(value, "./") {
		value = strings.TrimLeft(value[1:], "/")
	}
	return strings.TrimLeft(value, "/")
}

// splitLines is text.split(/\r?\n/).
func splitLines(text string) []string {
	var out []string
	for {
		i := strings.IndexByte(text, '\n')
		if i < 0 {
			return append(out, text)
		}
		out = append(out, strings.TrimSuffix(text[:i], "\r"))
		text = text[i+1:]
	}
}

// ParseIgnoreLines parses .faultlineignore text: the lines of
// text.split(/\r?\n/) through src/turn-snapshot.ts:372 parseIgnoreLines.
func ParseIgnoreLines(text string) []Rule {
	return parseLines(splitLines(text))
}

// Port of src/turn-snapshot.ts:372 parseIgnoreLines.
func parseLines(lines []string) []Rule {
	var rules []Rule
	for _, rawLine := range lines {
		trimmed := jsstr.Trim(rawLine)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		negated := false
		body := trimmed
		if strings.HasPrefix(body, "!") {
			negated = true
			body = body[1:]
		}
		body = strings.ReplaceAll(body, `\`, "/")
		directoryOnly := strings.HasSuffix(body, "/")
		if directoryOnly {
			body = body[:len(body)-1]
		}
		if body == "" {
			continue
		}
		anchored := strings.HasPrefix(body, "/")
		if anchored {
			body = body[1:]
		}
		escaped := escapeGlob(body)
		prefix, suffix := "(?:^|/)", "$"
		if anchored {
			prefix = "^"
		}
		if directoryOnly {
			suffix = "(?:/.*)?$"
		}
		rules = append(rules, Rule{
			Raw:           trimmed,
			Negated:       negated,
			DirectoryOnly: directoryOnly,
			Source:        prefix + escaped + suffix,
			anchored:      anchored,
			body:          tokenize(escaped),
		})
	}
	return rules
}

// escapeGlob is the replace chain of src/turn-snapshot.ts:389-394, step by
// step (a body that itself holds "\0DOUBLE\0" turns into .* as in TS).
func escapeGlob(body string) string {
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		if strings.IndexByte(`.+^${}()|[]\`, body[i]) >= 0 {
			b.WriteByte('\\')
		}
		b.WriteByte(body[i])
	}
	s := strings.ReplaceAll(b.String(), "**", "\x00DOUBLE\x00")
	s = strings.ReplaceAll(s, "*", "[^/]*")
	s = strings.ReplaceAll(s, "?", "[^/]")
	return strings.ReplaceAll(s, "\x00DOUBLE\x00", ".*")
}

// tokenize reads the escaped body back: every special character the user
// wrote is escaped, so an unescaped [ or . can only come from the glob
// replacements.
func tokenize(escaped string) []token {
	units := jsstr.ToUTF16(escaped)
	var out []token
	for i := 0; i < len(units); i++ {
		switch {
		case units[i] == '\\' && i+1 < len(units):
			i++
			out = append(out, token{tLit, canonicalize(units[i])})
		case hasUnits(units[i:], "[^/]*"):
			out = append(out, token{kind: tNonSlashRun})
			i += 4
		case hasUnits(units[i:], "[^/]"):
			out = append(out, token{kind: tOneNonSlash})
			i += 3
		case hasUnits(units[i:], ".*"):
			out = append(out, token{kind: tAnyRun})
			i++
		default:
			out = append(out, token{tLit, canonicalize(units[i])})
		}
	}
	return out
}

func hasUnits(units []uint16, ascii string) bool {
	if len(units) < len(ascii) {
		return false
	}
	for i := 0; i < len(ascii); i++ {
		if units[i] != uint16(ascii[i]) {
			return false
		}
	}
	return true
}

// canonicalize is ECMAScript Canonicalize for the i flag without u
// (fold_table.go is generated from V8).
func canonicalize(u uint16) uint16 {
	if c, ok := canonicalUnit[u]; ok {
		return c
	}
	return u
}

// isLineTerminator is what . does not match.
func isLineTerminator(u uint16) bool {
	return u == '\n' || u == '\r' || u == 0x2028 || u == 0x2029
}

// test is rule.regex.test(path) for the regex in r.Source: whether some
// match exists, which a set-of-states scan over the UTF-16 units decides
// without backtracking.
//
// V8 compiles the regex at its first use and throws "Regular expression
// too large" (more than 32,767 units of fixed-width text in a row) or
// "Stack overflow" (about 1,300 wildcards) for extreme lines; Go matches
// those rules instead (KNOWN_DIFFERENCES.md).
func (r *Rule) test(path []uint16) bool {
	n, m := len(path), len(r.body)
	// suffixOK[q]: the suffix matches path[q:].
	suffixOK := make([]bool, n+1)
	suffixOK[n] = true
	if r.DirectoryOnly {
		clean := true // no line terminator in path[q+1:]
		for q := n - 1; q >= 0; q-- {
			suffixOK[q] = clean && path[q] == '/'
			if isLineTerminator(path[q]) {
				clean = false
			}
		}
	}
	cur := make([]bool, m+1)
	next := make([]bool, m+1)
	// closure adds the states reachable by skipping runs.
	closure := func(states []bool) {
		for i := 0; i < m; i++ {
			if states[i] && (r.body[i].kind == tNonSlashRun || r.body[i].kind == tAnyRun) {
				states[i+1] = true
			}
		}
	}
	for q := 0; ; q++ {
		// A match may start here: at the beginning, or (unanchored) just
		// after a slash.
		if q == 0 || !r.anchored && path[q-1] == '/' {
			cur[0] = true
		}
		closure(cur)
		if cur[m] && suffixOK[q] {
			return true
		}
		if q == n {
			return false
		}
		u := path[q]
		clear(next)
		alive := false
		for i := 0; i < m; i++ {
			if !cur[i] {
				continue
			}
			t := r.body[i]
			switch t.kind {
			case tLit:
				if canonicalize(u) == t.unit {
					next[i+1], alive = true, true
				}
			case tOneNonSlash:
				if u != '/' {
					next[i+1], alive = true, true
				}
			case tNonSlashRun:
				if u != '/' {
					next[i], alive = true, true
				}
			case tAnyRun:
				if !isLineTerminator(u) {
					next[i], alive = true, true
				}
			}
		}
		cur, next = next, cur
		if !alive && r.anchored {
			return false
		}
	}
}

// IsIgnored ports src/turn-snapshot.ts:407 isIgnoredByRules: the last
// matching rule wins.
func IsIgnored(relativePath string, rules []Rule) bool {
	path := jsstr.ToUTF16(NormalizePath(relativePath))
	ignored := false
	for i := range rules {
		if rules[i].test(path) {
			ignored = !rules[i].Negated
		}
	}
	return ignored
}

var (
	dockerfilePattern = jsre.MustCompile(`^Dockerfile(?:\..+)?$`, "i")
	composePattern    = jsre.MustCompile(`^(?:docker-)?compose\.(?:ya?ml)$`, "i")
)

// IsProtectedDescriptor ports src/turn-snapshot.ts:421
// isProtectedEnvironmentDescriptorPath: environment descriptors stay
// snapshot-eligible even when an ignore rule lists them.
func IsProtectedDescriptor(rel string) bool {
	normalized := NormalizePath(rel)
	base := normalized[strings.LastIndexByte(normalized, '/')+1:]
	if slices.Contains(gitinv.EnvironmentDescriptorFiles, base) {
		return true
	}
	return dockerfilePattern.Test(base) || composePattern.Test(base)
}

// PathExcluded ports src/turn-snapshot.ts:430 pathIsSnapshotExcluded.
func PathExcluded(rel string, rules []Rule) bool {
	if IsProtectedDescriptor(rel) {
		return false
	}
	return IsIgnored(rel, rules)
}

// DefaultRules is parseIgnoreLines(TURN_SNAPSHOT_DEFAULT_IGNORE_PATTERNS).
func DefaultRules() []Rule { return parseLines(DefaultIgnorePatterns) }

// LoadIgnoreRules ports src/turn-snapshot.ts:659 loadFaultlineIgnoreRules:
// the built-in rules, then .faultlineignore's. Failures are *Error.
func LoadIgnoreRules(root string) ([]Rule, error) {
	defaults := DefaultRules()
	ignorePath := nodefs.Join(root, ".faultlineignore")
	if !nodefs.Exists(ignorePath) {
		return defaults, nil
	}
	stat, err := nodefs.Lstat(ignorePath)
	if err != nil {
		return nil, readFailure(err)
	}
	if stat.Mode()&os.ModeSymlink != 0 || !stat.Mode().IsRegular() {
		return nil, &Error{Message: ".faultlineignore must be a regular non-symlink file."}
	}
	if stat.Size() > ignoreParseLimit {
		return nil, &Error{Message: ".faultlineignore exceeds FaultLine's 256 KiB parse limit."}
	}
	text, err := nodefs.ReadText(ignorePath)
	if err != nil {
		return nil, readFailure(err)
	}
	return append(defaults, ParseIgnoreLines(text)...), nil
}

func readFailure(err error) error {
	return &Error{Message: "Could not read .faultlineignore: " + jsexc.Message(err)}
}
