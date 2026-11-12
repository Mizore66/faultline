package cli

import (
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/Mizore66/faultline/internal/jsstr"
)

// nodeReadline is a statement-level port of the parts of Node 22.22.2's
// readline/promises Interface that one question() exercises on a terminal
// (lib/internal/readline/{interface,utils,emitKeypressEvents,callbacks}.js):
// the keypress decoder (witness_review_tty_keys.go), kTtyWrite, the edit
// operations, the kill ring, undo/redo, history and the line-refresh output.
// Its escape output is compared with the frozen TypeScript on a real
// pseudo-terminal for every key group in
// difftest/witness_review_pty_live_test.go.
//
// The line is held as UTF-16 code units, as JavaScript indexes it, so cursor
// arithmetic, surrogate pairs and display widths agree with Node.
type nodeReadline struct {
	out       io.Writer
	prompt    string
	columns   int        // 0 = Infinity (the output reports no column count)
	columnsFn func() int // when set, read on every use (the terminal can resize)
	suspend   func()     // Ctrl+Z

	line   []uint16
	cursor int

	history      [][]uint16
	historyIndex int
	subSearch    []uint16
	subActive    bool
	killRing     [][]uint16
	killCursor   int
	yanking      bool
	undo, redo   []nodeEdit
	prevRows     int

	writeErr error

	closed      bool // the interface was closed (keypress listener removed)
	prevKey     nodeKey
	sawReturnAt time.Time // zero = 0

	done   bool
	answer string
	abort  string // "Aborted with Ctrl+C" / "Aborted with Ctrl+D"
}

type nodeEdit struct {
	text   []uint16
	cursor int
}

const (
	nodeHistorySize = 30
	nodeKillRingMax = 32
	nodeUndoMax     = 2048
	nodeTabSize     = 8
	nodeInfinity    = 1 << 30
)

func newNodeReadline(out io.Writer, prompt string, columns int) *nodeReadline {
	return &nodeReadline{out: out, prompt: prompt, columns: columns, historyIndex: -1}
}

func (r *nodeReadline) write(s string) {
	if r.writeErr != nil || s == "" {
		return
	}
	if _, err := io.WriteString(r.out, s); err != nil {
		r.writeErr = err
	}
}

func u16String(u []uint16) string { return jsstr.FromUTF16(u) }

func csi(n int, c string) string { return "\x1b[" + strconv.Itoa(n) + c }

func cursorTo(x int) string { return csi(x+1, "G") }

func moveCursorStr(dx, dy int) string {
	var b strings.Builder
	if dx < 0 {
		b.WriteString(csi(-dx, "D"))
	} else if dx > 0 {
		b.WriteString(csi(dx, "C"))
	}
	if dy < 0 {
		b.WriteString(csi(-dy, "A"))
	} else if dy > 0 {
		b.WriteString(csi(dy, "B"))
	}
	return b.String()
}

const clearScreenDownSeq = "\x1b[0J"

// codePointAt is String.prototype.codePointAt over UTF-16 units.
func codePointAt(s []uint16, i int) int {
	u := s[i]
	if u >= 0xD800 && u <= 0xDBFF && i+1 < len(s) && s[i+1] >= 0xDC00 && s[i+1] <= 0xDFFF {
		return int((rune(u)-0xD800)<<10 | (rune(s[i+1]) - 0xDC00) + 0x10000)
	}
	return int(u)
}

// charLengthLeft and charLengthAt are internal/readline/utils.js.
func charLengthLeft(s []uint16, i int) int {
	if i <= 0 {
		return 0
	}
	if (i > 1 && codePointAt(s, i-2) >= 0x10000) || codePointAt(s, i-1) >= 0x10000 {
		return 2
	}
	return 1
}

func charLengthAt(s []uint16, i int) int {
	if len(s) <= i {
		return 1
	}
	if codePointAt(s, i) >= 0x10000 {
		return 2
	}
	return 1
}

// ansiPattern is the stripVTControlCharacters pattern of
// lib/internal/util/inspect.js.
var ansiPattern = regexp.MustCompile("[\u001B\u009B][[\\]()#;?]*" +
	"(?:(?:(?:(?:;[-a-zA-Z\\d/#&.:=?%@~_]+)*" +
	"|[a-zA-Z\\d]+(?:;[-a-zA-Z\\d/#&.:=?%@~_]*)*)?" +
	"(?:\u0007|\u001B\\\\|\u009C))" +
	"|(?:(?:\\d{1,4}(?:;\\d{0,4})*)?" +
	"[\\dA-PR-TZcf-nq-uy=><~]))")

// jsCharWidth is getStringWidth(char, false) bucketed as readline uses it: 0,
// 1, or 2 (every width of 2 or more).
func jsCharWidth(cp rune) int {
	switch {
	case cp < 0x20:
		return 0
	case cp < 0x7f:
		return 1
	}
	if inRanges(nodeZeroWidth, cp) {
		return 0
	}
	if inRanges(nodeWideWidth, cp) {
		return 2
	}
	return 1
}

func inRanges(rs [][2]rune, cp rune) bool {
	lo, hi := 0, len(rs)
	for lo < hi {
		m := (lo + hi) / 2
		switch {
		case cp < rs[m][0]:
			hi = m
		case cp > rs[m][1]:
			lo = m + 1
		default:
			return true
		}
	}
	return false
}

// u16runes iterates code points as SafeStringIterator does (a lone surrogate
// is its own element).
func u16runes(u []uint16) []rune {
	out := make([]rune, 0, len(u))
	for i := 0; i < len(u); i++ {
		if u[i] >= 0xD800 && u[i] <= 0xDBFF && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] <= 0xDFFF {
			out = append(out, utf16.DecodeRune(rune(u[i]), rune(u[i+1])))
			i++
			continue
		}
		out = append(out, rune(u[i]))
	}
	return out
}

type displayPos struct{ cols, rows int }

// displayPos is kGetDisplayPos.
func (r *nodeReadline) displayPos(str string) displayPos {
	offset, rows := 0, 0
	col := r.columns
	if r.columnsFn != nil {
		col = r.columnsFn()
	}
	str = ansiPattern.ReplaceAllString(str, "")
	for _, ch := range u16runes(jsstr.ToUTF16(str)) {
		if ch == '\n' {
			rows += max(ceilDiv(offset, col), 1)
			offset = 0
			continue
		}
		if ch == '\t' {
			offset += nodeTabSize - offset%nodeTabSize
			continue
		}
		w := jsCharWidth(ch)
		if w == 0 || w == 1 {
			offset += w
		} else {
			if col > 0 && (offset+1)%col == 0 {
				offset++
			}
			offset += 2
		}
	}
	if col == 0 { // Infinity columns
		return displayPos{cols: offset, rows: rows}
	}
	cols := offset % col
	rows += (offset - cols) / col
	return displayPos{cols, rows}
}

func ceilDiv(a, col int) int {
	if col == 0 {
		return 0 // Math.ceil(offset / Infinity)
	}
	return (a + col - 1) / col
}

func (r *nodeReadline) cursorPos() displayPos {
	return r.displayPos(r.prompt + u16String(r.line[:r.cursor]))
}

func (r *nodeReadline) refreshLine() {
	line := r.prompt + u16String(r.line)
	dp := r.displayPos(line)
	cp := r.cursorPos()
	if r.prevRows > 0 {
		r.write(moveCursorStr(0, -r.prevRows))
	}
	r.write(cursorTo(0))
	r.write(clearScreenDownSeq)
	r.write(line)
	if dp.cols == 0 {
		r.write(" ")
	}
	r.write(cursorTo(cp.cols))
	if diff := dp.rows - cp.rows; diff > 0 {
		r.write(moveCursorStr(0, -diff))
	}
	r.prevRows = cp.rows
}

func (r *nodeReadline) moveCursor(dx int) {
	if dx == 0 {
		return
	}
	old := r.cursorPos()
	r.cursor += dx
	if r.cursor < 0 {
		r.cursor = 0
	} else if r.cursor > len(r.line) {
		r.cursor = len(r.line)
	}
	nw := r.cursorPos()
	if old.rows == nw.rows {
		r.write(moveCursorStr(nw.cols-old.cols, 0))
	} else {
		r.refreshLine()
	}
}

func concat(parts ...[]uint16) []uint16 {
	var out []uint16
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func clone(u []uint16) []uint16 { return append([]uint16(nil), u...) }

func (r *nodeReadline) beforeEdit() {
	r.undo = append(r.undo, nodeEdit{clone(r.line), r.cursor})
	if len(r.undo) > nodeUndoMax {
		r.undo = r.undo[1:]
	}
}

// insertString is kInsertString. completion mirrors isCompletionEnabled, which
// the keypress decoder sets true only for the last character of a data chunk.
func (r *nodeReadline) insertString(c []uint16, completion bool) {
	r.beforeEdit()
	if !completion {
		r.line = concat(r.line[:r.cursor], c, r.line[r.cursor:])
		r.cursor += len(c)
		r.write(u16String(c))
		return
	}
	if r.cursor < len(r.line) {
		r.line = concat(r.line[:r.cursor], c, r.line[r.cursor:])
		r.cursor += len(c)
		r.refreshLine()
		return
	}
	old := r.cursorPos()
	r.line = concat(r.line, c)
	r.cursor += len(c)
	nw := r.cursorPos()
	if old.rows < nw.rows {
		r.refreshLine()
	} else {
		r.write(u16String(c))
	}
}

// JavaScript regexp classes used by the word operations: \s is the ECMAScript
// whitespace set, \w is [A-Za-z0-9_].
func isJSSpace(c rune) bool { return c < 0x10000 && jsstr.IsWhitespace(uint16(c)) }

func isJSWord(c rune) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// prefixUnits returns the UTF-16 length of the match of one of Node's word
// patterns at the start of s:
//
//	wordLeft   /^\s*(?:[^\w\s]+|\w+)?/   (run on the reversed leading text)
//	wordRight  /^(?:\s+|[^\w\s]+|\w+)\s*/
//	delRight   /^(?:\s+|\W+|\w+)\s*/
func prefixUnits(kind int, s []uint16) int {
	rs := u16runes(s)
	run := func(from int, pred func(rune) bool) int {
		k := from
		for k < len(rs) && pred(rs[k]) {
			k++
		}
		return k
	}
	notWordNotSpace := func(c rune) bool { return !isJSWord(c) && !isJSSpace(c) }
	n := 0
	switch kind {
	case 0:
		n = run(0, isJSSpace)
		if n < len(rs) {
			if isJSWord(rs[n]) {
				n = run(n, isJSWord)
			} else {
				n = run(n, notWordNotSpace)
			}
		}
	default:
		if len(rs) == 0 {
			return 0
		}
		switch {
		case isJSSpace(rs[0]):
			n = run(0, isJSSpace)
		case isJSWord(rs[0]):
			n = run(0, isJSWord)
		case kind == 1:
			n = run(0, notWordNotSpace)
		default: // \W+ also consumes whitespace
			n = run(0, func(c rune) bool { return !isJSWord(c) })
		}
		n = run(n, isJSSpace)
	}
	units := 0
	for _, c := range rs[:n] {
		if c >= 0x10000 {
			units += 2
		} else {
			units++
		}
	}
	return units
}

func reverseUnits(s []uint16) []uint16 {
	rs := u16runes(s)
	out := make([]uint16, 0, len(s))
	for i := len(rs) - 1; i >= 0; i-- {
		if rs[i] >= 0x10000 {
			a, b := utf16.EncodeRune(rs[i])
			out = append(out, uint16(a), uint16(b))
		} else {
			out = append(out, uint16(rs[i]))
		}
	}
	return out
}

func (r *nodeReadline) wordLeft() {
	if r.cursor > 0 {
		r.moveCursor(-prefixUnits(0, reverseUnits(r.line[:r.cursor])))
	}
}

func (r *nodeReadline) wordRight() {
	if r.cursor < len(r.line) {
		r.moveCursor(prefixUnits(1, r.line[r.cursor:]))
	}
}

func (r *nodeReadline) deleteLeft() {
	if r.cursor > 0 && len(r.line) > 0 {
		r.beforeEdit()
		size := charLengthLeft(r.line, r.cursor)
		r.line = concat(r.line[:r.cursor-size], r.line[r.cursor:])
		r.cursor -= size
		r.refreshLine()
	}
}

func (r *nodeReadline) deleteRight() {
	if r.cursor < len(r.line) {
		r.beforeEdit()
		end := min(r.cursor+charLengthAt(r.line, r.cursor), len(r.line))
		r.line = concat(r.line[:r.cursor], r.line[end:])
		r.refreshLine()
	}
}

func (r *nodeReadline) deleteWordLeft() {
	if r.cursor > 0 {
		r.beforeEdit()
		leading := clone(r.line[:r.cursor])
		m := prefixUnits(0, reverseUnits(leading))
		leading = leading[:len(leading)-m]
		r.line = concat(leading, r.line[r.cursor:])
		r.cursor = len(leading)
		r.refreshLine()
	}
}

func (r *nodeReadline) deleteWordRight() {
	if r.cursor < len(r.line) {
		r.beforeEdit()
		trailing := r.line[r.cursor:]
		m := prefixUnits(2, trailing)
		r.line = concat(r.line[:r.cursor], trailing[m:])
		r.refreshLine()
	}
}

func (r *nodeReadline) deleteLineLeft() {
	r.beforeEdit()
	del := clone(r.line[:r.cursor])
	r.line = clone(r.line[r.cursor:])
	r.cursor = 0
	r.pushKill(del)
	r.refreshLine()
}

func (r *nodeReadline) deleteLineRight() {
	r.beforeEdit()
	del := clone(r.line[r.cursor:])
	r.line = clone(r.line[:r.cursor])
	r.pushKill(del)
	r.refreshLine()
}

func equalUnits(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (r *nodeReadline) pushKill(del []uint16) {
	if len(del) == 0 || (len(r.killRing) > 0 && equalUnits(del, r.killRing[0])) {
		return
	}
	r.killRing = append([][]uint16{del}, r.killRing...)
	r.killCursor = 0
	for len(r.killRing) > nodeKillRingMax {
		r.killRing = r.killRing[:len(r.killRing)-1]
	}
}

func (r *nodeReadline) yank(completion bool) {
	if len(r.killRing) > 0 {
		r.yanking = true
		r.insertString(r.killRing[r.killCursor], completion)
	}
}

func (r *nodeReadline) yankPop() {
	if !r.yanking || len(r.killRing) <= 1 {
		return
	}
	last := r.killRing[r.killCursor]
	r.killCursor++
	if r.killCursor >= len(r.killRing) {
		r.killCursor = 0
	}
	cur := r.killRing[r.killCursor]
	from := max(r.cursor-len(last), 0)
	head := clone(r.line[:from])
	tail := clone(r.line[r.cursor:])
	r.line = concat(head, cur, tail)
	r.cursor = len(head) + len(cur)
	r.refreshLine()
}

func (r *nodeReadline) doUndo() {
	if len(r.undo) <= 0 {
		return
	}
	r.redo = append(r.redo, nodeEdit{clone(r.line), r.cursor})
	e := r.undo[len(r.undo)-1]
	r.undo = r.undo[:len(r.undo)-1]
	r.line, r.cursor = e.text, e.cursor
	r.refreshLine()
}

func (r *nodeReadline) doRedo() {
	if len(r.redo) <= 0 {
		return
	}
	r.undo = append(r.undo, nodeEdit{clone(r.line), r.cursor})
	e := r.redo[len(r.redo)-1]
	r.redo = r.redo[:len(r.redo)-1]
	r.line, r.cursor = e.text, e.cursor
	r.refreshLine()
}

func hasPrefixUnits(s, p []uint16) bool {
	return len(s) >= len(p) && equalUnits(s[:len(p)], p)
}

func (r *nodeReadline) search() []uint16 {
	if r.subActive {
		return r.subSearch
	}
	return nil
}

func (r *nodeReadline) historyNext() {
	if r.historyIndex >= 0 {
		r.beforeEdit()
		search := r.search()
		index := r.historyIndex - 1
		for index >= 0 && (!hasPrefixUnits(r.history[index], search) || equalUnits(r.line, r.history[index])) {
			index--
		}
		if index == -1 {
			r.line = clone(search)
		} else {
			r.line = clone(r.history[index])
		}
		r.historyIndex = index
		r.cursor = len(r.line)
		r.refreshLine()
	}
}

func (r *nodeReadline) historyPrev() {
	if r.historyIndex < len(r.history) && len(r.history) > 0 {
		r.beforeEdit()
		search := r.search()
		index := r.historyIndex + 1
		for index < len(r.history) && (!hasPrefixUnits(r.history[index], search) || equalUnits(r.line, r.history[index])) {
			index++
		}
		if index == len(r.history) {
			r.line = clone(search)
		} else {
			r.line = clone(r.history[index])
		}
		r.historyIndex = index
		r.cursor = len(r.line)
		r.refreshLine()
	}
}

func (r *nodeReadline) addHistory() []uint16 {
	if len(r.line) == 0 {
		return nil
	}
	if len(trimUnits(r.line)) == 0 {
		return r.line
	}
	if len(r.history) == 0 || !equalUnits(r.history[0], r.line) {
		r.history = append([][]uint16{clone(r.line)}, r.history...)
		if len(r.history) > nodeHistorySize {
			r.history = r.history[:len(r.history)-1]
		}
	}
	r.historyIndex = -1
	return r.history[0]
}

func trimUnits(u []uint16) []uint16 {
	s, e := 0, len(u)
	for s < e && jsstr.IsWhitespace(u[s]) {
		s++
	}
	for e > s && jsstr.IsWhitespace(u[e-1]) {
		e--
	}
	return u[s:e]
}

// finishLine is kLine: add to history, clear the line (move to its end, write
// CR LF) and settle the question with the line as it was.
func (r *nodeReadline) finishLine() {
	line := r.addHistory()
	r.undo, r.redo = nil, nil
	r.moveCursor(nodeInfinity)
	r.write("\r\n")
	r.line = nil
	r.cursor = 0
	r.prevRows = 0
	if !r.done {
		r.done = true
		r.answer = u16String(line)
	}
}

func (r *nodeReadline) abortWith(msg string) {
	if !r.done {
		r.done = true
		r.abort = msg
	}
}
