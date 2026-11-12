package cli

import (
	"io"
	"time"
	"unicode/utf8"

	"github.com/Mizore66/faultline/internal/jsstr"
)

// nodeReadlineOptions are the terminal facilities the question needs. The
// zero value is a terminal of unknown width with nothing to suspend (what the
// unit tests use).
type nodeReadlineOptions struct {
	// Columns is the output's column count; nil or 0 means unknown (Infinity).
	Columns func() int
	// Resize delivers one value per terminal size change (SIGWINCH).
	Resize <-chan struct{}
	// SetRaw switches the input terminal to raw (true) or cooked (false) mode,
	// as readline's setRawMode does for Ctrl+Z.
	SetRaw func(bool)
	// Suspend raises SIGTSTP on this process (process.kill(pid, "SIGTSTP")).
	Suspend func()
	// Continue delivers one value per SIGCONT received after Suspend.
	Continue <-chan struct{}
}

const nodeEscapeTimeout = 500 * time.Millisecond

type nodeChunk struct {
	data []byte
	err  error
}

// nodeReadlineQuestion asks one question exactly as Node 22.22.2's
// readline/promises Interface does on a terminal: the keypress decoder,
// editing keys, history, kill ring and undo of the original, with the
// original's output bytes. It returns the answer line; ok is false when the
// input ended without settling the question (EOF, or the input was paused by
// a continued Ctrl+Z). An abort (Ctrl+C or Ctrl+D on an empty line) is
// returned as a WitnessReviewTTYError carrying Node's AbortError message.
func nodeReadlineQuestion(in io.Reader, out io.Writer, prompt string, opts ...nodeReadlineOptions) (line string, ok bool, err error) {
	var o nodeReadlineOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	r := newNodeReadline(out, prompt, 0)
	if o.Columns != nil {
		r.columnsFn = o.Columns
	}
	completion := false
	parser := newKeyParser(func(s string, hasS bool, key nodeKey) {
		r.ttyWrite(s, hasS, key, completion)
		if !r.closed && key.sequence != "" {
			// A half surrogate pair is refreshed so it displays properly.
			if cp := codePointAt(jsstr.ToUTF16(key.sequence), 0); cp >= 0xD800 && cp <= 0xDFFF {
				r.refreshLine()
			}
		}
	})
	defer parser.close()

	r.refreshLine()

	chunks := make(chan nodeChunk)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		buf := make([]byte, 65536)
		for {
			n, rerr := in.Read(buf)
			var data []byte
			if n > 0 {
				data = append([]byte(nil), buf[:n]...)
			}
			select {
			case chunks <- nodeChunk{data, rerr}:
			case <-stop:
				return
			}
			if rerr != nil {
				return
			}
		}
	}()

	var pending []byte
	var escTimer *time.Timer
	var escC <-chan time.Time
	paused := false
	cont := o.Continue

	r.suspend = func() {
		if o.Suspend == nil {
			return
		}
		if o.SetRaw != nil {
			o.SetRaw(false)
		}
		o.Suspend()
	}

	finish := func() (string, bool, error) {
		if r.writeErr != nil {
			return "", false, r.writeErr
		}
		if r.abort != "" {
			return "", false, &WitnessReviewTTYError{"ABORTED", r.abort}
		}
		return r.answer, r.done, nil
	}

	for {
		select {
		case c := <-chunks:
			if len(c.data) > 0 && !paused {
				if escTimer != nil {
					escTimer.Stop()
					escC = nil
				}
				pending = append(pending, c.data...)
				// StringDecoder: only complete characters are decoded now.
				var chars []rune
				i := 0
				for i < len(pending) {
					if !utf8.FullRune(pending[i:]) {
						break
					}
					rn, size := utf8.DecodeRune(pending[i:])
					chars = append(chars, rn)
					i += size
				}
				pending = pending[i:]
				total := 0
				for _, ch := range chars {
					total += jsUnits(ch)
				}
				length := 0
				for _, ch := range chars {
					length += jsUnits(ch)
					completion = length == total
					parser.feed(string(ch))
					if length == total && ch == 0x1b {
						if escTimer == nil {
							escTimer = time.NewTimer(nodeEscapeTimeout)
						} else {
							escTimer.Reset(nodeEscapeTimeout)
						}
						escC = escTimer.C
					}
				}
				if r.done {
					return finish()
				}
			}
			if c.err != nil {
				return finish()
			}
		case <-escC:
			escC = nil
			completion = true
			parser.feed("")
			if r.done {
				return finish()
			}
		case <-o.Resize:
			r.refreshLine()
		case <-cont:
			// SIGCONT after Ctrl+Z: pause the input, raw mode again, refresh.
			paused = true
			if o.SetRaw != nil {
				o.SetRaw(true)
			}
			r.refreshLine()
			return finish()
		}
		if r.writeErr != nil {
			return finish()
		}
	}
}

// jsUnits is the UTF-16 length of one code point.
func jsUnits(r rune) int {
	if r >= 0x10000 {
		return 2
	}
	return 1
}
