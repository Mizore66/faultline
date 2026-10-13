package schema

import "regexp"

// Matcher tests a JS string (WTF-8).
type Matcher func(string) bool

// Pattern compiles an ASCII-only JS regex source that has an identical RE2
// meaning. Anything RE2 cannot express (lookahead, UTF-16 counted classes) must
// be written as a Go Matcher instead.
func Pattern(source string) Matcher {
	re := regexp.MustCompile(source)
	return re.MatchString
}

// datetimePattern is zod's datetimeRegex({ precision: null, offset: true, local: false }).
var datetimePattern = regexp.MustCompile(`^((\d\d[2468][048]|\d\d[13579][26]|\d\d0[48]|[02468][048]00|[13579][26]00)-02-29|\d{4}-((0[13578]|1[02])-(0[1-9]|[12]\d|3[01])|(0[469]|11)-(0[1-9]|[12]\d|30)|(02)-(0[1-9]|1\d|2[0-8])))T([01]\d|2[0-3]):[0-5]\d(:[0-5]\d(\.\d+)?)?(Z|([+-]\d{2}:?\d{2}))$`)

// uuidPattern is zod's uuidRegex (flag i).
var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}\b-[0-9a-f]{4}\b-[0-9a-f]{4}\b-[0-9a-f]{4}\b-[0-9a-f]{12}$`)
