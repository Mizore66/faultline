package cli

import (
	"iter"
	"regexp"
	"strings"

	"github.com/Mizore66/faultline/internal/jsstr"
)

// nodeKey is the `key` object of a Node keypress event. nameDefined is false
// where Node leaves key.name undefined.
type nodeKey struct {
	name              string
	nameDefined       bool
	ctrl, meta, shift bool
	sequence          string
	escaped           bool
}

// keyParser is lib/internal/readline/utils.js emitKeys, ported statement for
// statement. The JavaScript generator is a coroutine; iter.Pull gives the same
// control flow, so every `yield` below is one get() and the code reads like the
// original. feed("") is the escape-code timeout (the original's next("")).
type keyParser struct {
	next func() (struct{}, bool)
	stop func()
	fed  string
	// emit is stream.emit('keypress', s, key); hasS is false where the original
	// passes undefined.
	emit func(s string, hasS bool, key nodeKey)
}

type keyParserStopped struct{}

func newKeyParser(emit func(s string, hasS bool, key nodeKey)) *keyParser {
	p := &keyParser{emit: emit}
	p.next, p.stop = iter.Pull(func(yield func(struct{}) bool) {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(keyParserStopped); !ok {
					panic(r)
				}
			}
		}()
		p.run(yield)
	})
	p.next() // run to the first `yield`
	return p
}

func (p *keyParser) feed(ch string) {
	p.fed = ch
	p.next()
}

func (p *keyParser) close() { p.stop() }

func isDigitString(ch string) bool { return len(ch) == 1 && ch[0] >= '0' && ch[0] <= '9' }

var (
	csiTilde  = regexp.MustCompile(`^(?:(\d\d?)(?:;(\d))?([~^$])|(\d{3}~))$`)
	csiLetter = regexp.MustCompile(`^((\d;)?(\d))?([A-Za-z])$`)
)

func (p *keyParser) run(yield func(struct{}) bool) {
	get := func() string {
		if !yield(struct{}{}) {
			panic(keyParserStopped{})
		}
		return p.fed
	}
	for {
		ch := get()
		s := ch
		escaped := false
		var key nodeKey

		if ch == "\x1b" {
			escaped = true
			ch = get()
			s += ch
			if ch == "\x1b" {
				ch = get()
				s += ch
			}
		}

		if escaped && (ch == "O" || ch == "[") {
			code := ch
			modifier := 0
			if ch == "O" {
				ch = get()
				s += ch
				if isDigitString(ch) {
					modifier = int(ch[0]-'0') - 1
					ch = get()
					s += ch
				}
				code += ch
			} else {
				ch = get()
				s += ch
				if ch == "[" {
					code += ch
					ch = get()
					s += ch
				}
				cmdStart := len(jsstr.ToUTF16(s)) - 1
				if isDigitString(ch) {
					ch = get()
					s += ch
					if isDigitString(ch) {
						ch = get()
						s += ch
						if isDigitString(ch) {
							ch = get()
							s += ch
						}
					}
				}
				if ch == ";" {
					ch = get()
					s += ch
					if isDigitString(ch) {
						s += get()
					}
				}
				units := jsstr.ToUTF16(s)
				cmd := ""
				if cmdStart >= 0 && cmdStart <= len(units) {
					cmd = jsstr.FromUTF16(units[cmdStart:])
				}
				if m := csiTilde.FindStringSubmatch(cmd); m != nil {
					if m[4] != "" {
						code += m[4]
					} else {
						code += m[1] + m[3]
						mod := 1
						if m[2] != "" {
							mod = int(m[2][0] - '0')
						}
						modifier = mod - 1
					}
				} else if m := csiLetter.FindStringSubmatch(cmd); m != nil {
					code += m[4]
					mod := 1
					if m[3] != "" {
						mod = int(m[3][0] - '0')
					}
					modifier = mod - 1
				} else {
					code += cmd
				}
			}
			key.ctrl = modifier&4 != 0
			key.meta = modifier&10 != 0
			key.shift = modifier&1 != 0
			applyEscapeCode(&key, code)
		} else if ch == "\r" {
			key.name, key.nameDefined, key.meta = "return", true, escaped
		} else if ch == "\n" {
			key.name, key.nameDefined, key.meta = "enter", true, escaped
		} else if ch == "\t" {
			key.name, key.nameDefined, key.meta = "tab", true, escaped
		} else if ch == "\b" || ch == "\x7f" {
			key.name, key.nameDefined, key.meta = "backspace", true, escaped
		} else if ch == "\x1b" {
			key.name, key.nameDefined, key.meta = "escape", true, escaped
		} else if ch == " " {
			key.name, key.nameDefined, key.meta = "space", true, escaped
		} else if !escaped && (ch == "" || len(ch) == 1 && ch[0] <= 0x1a) {
			c := 0
			if ch != "" {
				c = int(ch[0])
			}
			key.name, key.nameDefined = string(rune(c+'a'-1)), true
			key.ctrl = true
		} else if len(ch) == 1 && (ch[0] >= '0' && ch[0] <= '9' || ch[0] >= 'a' && ch[0] <= 'z' || ch[0] >= 'A' && ch[0] <= 'Z') {
			key.name, key.nameDefined = strings.ToLower(ch), true
			key.shift = ch[0] >= 'A' && ch[0] <= 'Z'
			key.meta = escaped
		} else if escaped {
			// Escape sequence timeout
			if ch == "" {
				key.name, key.nameDefined = "escape", true
			}
			key.meta = true
		}

		key.sequence = s
		key.escaped = escaped
		units := jsstr.ToUTF16(s)
		if len(units) != 0 && (key.nameDefined || escaped) {
			// Named character or sequence.
			if escaped {
				p.emit("", false, key)
			} else {
				p.emit(s, true, key)
			}
		} else if charLengthAtUnits(units, 0) == len(units) {
			// Single unnamed character, e.g. ".".
			p.emit(s, true, key)
		}
		// Unrecognized or broken escape sequence: emit nothing.
	}
}

func charLengthAtUnits(u []uint16, i int) int { return charLengthAt(u, i) }

func applyEscapeCode(k *nodeKey, code string) {
	set := func(n string) { k.name, k.nameDefined = n, true }
	switch code {
	case "[P", "OP", "[11~", "[[A":
		set("f1")
	case "[Q", "OQ", "[12~", "[[B":
		set("f2")
	case "[R", "OR", "[13~", "[[C":
		set("f3")
	case "[S", "OS", "[14~", "[[D":
		set("f4")
	case "[200~":
		set("paste-start")
	case "[201~":
		set("paste-end")
	case "[[E", "[15~":
		set("f5")
	case "[17~":
		set("f6")
	case "[18~":
		set("f7")
	case "[19~":
		set("f8")
	case "[20~":
		set("f9")
	case "[21~":
		set("f10")
	case "[23~":
		set("f11")
	case "[24~":
		set("f12")
	case "[A", "OA":
		set("up")
	case "[B", "OB":
		set("down")
	case "[C", "OC":
		set("right")
	case "[D", "OD":
		set("left")
	case "[E", "OE":
		set("clear")
	case "[F", "OF", "[4~", "[8~":
		set("end")
	case "[H", "OH", "[1~", "[7~":
		set("home")
	case "[2~":
		set("insert")
	case "[3~":
		set("delete")
	case "[5~", "[[5~":
		set("pageup")
	case "[6~", "[[6~":
		set("pagedown")
	case "[a":
		set("up")
		k.shift = true
	case "[b":
		set("down")
		k.shift = true
	case "[c":
		set("right")
		k.shift = true
	case "[d":
		set("left")
		k.shift = true
	case "[e":
		set("clear")
		k.shift = true
	case "[2$":
		set("insert")
		k.shift = true
	case "[3$":
		set("delete")
		k.shift = true
	case "[5$":
		set("pageup")
		k.shift = true
	case "[6$":
		set("pagedown")
		k.shift = true
	case "[7$":
		set("home")
		k.shift = true
	case "[8$":
		set("end")
		k.shift = true
	case "Oa":
		set("up")
		k.ctrl = true
	case "Ob":
		set("down")
		k.ctrl = true
	case "Oc":
		set("right")
		k.ctrl = true
	case "Od":
		set("left")
		k.ctrl = true
	case "Oe":
		set("clear")
		k.ctrl = true
	case "[2^":
		set("insert")
		k.ctrl = true
	case "[3^":
		set("delete")
		k.ctrl = true
	case "[5^":
		set("pageup")
		k.ctrl = true
	case "[6^":
		set("pagedown")
		k.ctrl = true
	case "[7^":
		set("home")
		k.ctrl = true
	case "[8^":
		set("end")
		k.ctrl = true
	case "[Z":
		set("tab")
		k.shift = true
	default:
		set("undefined")
	}
}
