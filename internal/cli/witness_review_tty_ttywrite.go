package cli

import (
	"time"

	"github.com/Mizore66/faultline/internal/jsstr"
)

const nodeCRLFDelay = 100 * time.Millisecond

// ttyWrite is Interface[kTtyWrite](s, key): one decoded keypress.
func (r *nodeReadline) ttyWrite(s string, hasS bool, key nodeKey, completion bool) {
	if r.closed {
		return // the keypress listener is removed on close
	}
	r.prevKey = key

	if !key.meta || key.name != "y" {
		r.yanking = false
	}

	// Activate or deactivate substring search.
	if (key.name == "up" || key.name == "down") && !key.ctrl && !key.meta && !key.shift {
		if !r.subActive {
			r.subActive = true
			r.subSearch = clone(r.line[:r.cursor])
		}
	} else if r.subActive {
		r.subActive = false
		r.subSearch = nil
		if len(r.history) == r.historyIndex {
			r.historyIndex = -1
		}
	}

	// Undo and redo.
	if key.sequence != "" {
		units := jsstr.ToUTF16(key.sequence)
		switch units[0] {
		case 0x1f:
			r.doUndo()
			return
		case 0x1e:
			r.doRedo()
			return
		}
	}

	if key.name == "escape" {
		return
	}

	switch {
	case key.ctrl && key.shift:
		switch key.name {
		case "backspace":
			r.deleteLineLeft()
		case "delete":
			r.deleteLineRight()
		}
	case key.ctrl:
		r.ctrlKey(key, completion)
	case key.meta:
		switch key.name {
		case "b":
			r.wordLeft()
		case "f":
			r.wordRight()
		case "d", "delete":
			r.deleteWordRight()
		case "backspace":
			r.deleteWordLeft()
		case "y":
			r.yankPop()
		}
	default:
		r.plainKey(s, hasS, key, completion)
	}
}

func (r *nodeReadline) ctrlKey(key nodeKey, completion bool) {
	switch key.name {
	case "c":
		// No SIGINT listener on a readline/promises interface: finished.
		r.closed = true
		r.abortWith("Aborted with Ctrl+C")
	case "h":
		r.deleteLeft()
	case "d":
		if r.cursor == 0 && len(r.line) == 0 {
			r.closed = true
			r.abortWith("Aborted with Ctrl+D")
		} else if r.cursor < len(r.line) {
			r.deleteRight()
		}
	case "u":
		r.deleteLineLeft()
	case "k":
		r.deleteLineRight()
	case "a":
		r.moveCursor(-nodeInfinity)
	case "e":
		r.moveCursor(nodeInfinity)
	case "b":
		r.moveCursor(-charLengthLeft(r.line, r.cursor))
	case "f":
		r.moveCursor(charLengthAt(r.line, r.cursor))
	case "l":
		r.write("\x1b[1;1H")
		r.write(clearScreenDownSeq)
		r.refreshLine()
	case "n":
		r.historyNext()
	case "p":
		r.historyPrev()
	case "y":
		r.yank(completion)
	case "z":
		if r.suspend != nil {
			r.suspend()
		}
	case "w", "backspace":
		r.deleteWordLeft()
	case "delete":
		r.deleteWordRight()
	case "left":
		r.wordLeft()
	case "right":
		r.wordRight()
	}
}

func (r *nodeReadline) plainKey(s string, hasS bool, key nodeKey, completion bool) {
	if !r.sawReturnAt.IsZero() && key.name != "enter" {
		r.sawReturnAt = time.Time{}
	}
	switch key.name {
	case "return":
		r.sawReturnAt = time.Now()
		r.finishLine()
	case "enter":
		if r.sawReturnAt.IsZero() || time.Since(r.sawReturnAt) > nodeCRLFDelay {
			r.finishLine()
		}
		r.sawReturnAt = time.Time{}
	case "backspace":
		r.deleteLeft()
	case "delete":
		r.deleteRight()
	case "left":
		r.moveCursor(-charLengthLeft(r.line, r.cursor))
	case "right":
		r.moveCursor(charLengthAt(r.line, r.cursor))
	case "home":
		r.moveCursor(-nodeInfinity)
	case "end":
		r.moveCursor(nodeInfinity)
	case "up":
		r.historyPrev()
	case "down":
		r.historyNext()
	default:
		// "tab" falls through here: no completer is configured.
		if hasS && s != "" {
			r.insertText(s, completion)
		}
	}
}

// insertText is the default branch of kTtyWrite: the string is split on line
// endings (/\r?\n|\r(?!\n)/); each piece is inserted and each ending ends the
// line.
func (r *nodeReadline) insertText(s string, completion bool) {
	units := jsstr.ToUTF16(s)
	start := 0
	for i := 0; i < len(units); {
		switch {
		case units[i] == '\r' && i+1 < len(units) && units[i+1] == '\n':
			r.insertString(clone(units[start:i]), completion)
			i += 2
			start = i
			r.finishLine()
		case units[i] == '\n' || units[i] == '\r':
			r.insertString(clone(units[start:i]), completion)
			i++
			start = i
			r.finishLine()
		default:
			i++
		}
	}
	r.insertString(clone(units[start:]), completion)
}
