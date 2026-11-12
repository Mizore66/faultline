package cli

import (
	"io"
	"strconv"
	"unicode/utf8"
)

// nodeReadlineQuestion reproduces, byte for byte, what Node's
// readline/promises question() writes to a raw-mode terminal for the
// supported editing keys, and how it settles (traced against Node 22.22.2 on
// a real PTY):
//
//   - the prompt is (re)drawn as ESC[1G ESC[0J prompt line ESC[<col>G
//     (Interface._refreshLine) at the start and after every deletion;
//   - an inserted character at the end of the line is echoed as-is;
//   - Enter (CR or LF) writes CR LF (the terminal's ONLCR makes it CR CR LF)
//     and answers with the line;
//   - Backspace or DEL deletes the previous character;
//   - Ctrl+D on an empty line aborts with "Aborted with Ctrl+D" and is
//     ignored on a non-empty line;
//   - Ctrl+C aborts with "Aborted with Ctrl+C";
//   - EOF settles nothing (ok is false).
//
// Cursor movement, history, word and line kill keys and escape sequences are
// NOT implemented (an open gap recorded in known differences); they are
// ignored here.
func nodeReadlineQuestion(in io.Reader, out io.Writer, prompt string) (line string, ok bool, err error) {
	var text []rune
	refresh := func() error {
		col := utf8.RuneCountInString(prompt) + len(text) + 1
		_, err := io.WriteString(out, "\x1b[1G\x1b[0J"+prompt+string(text)+"\x1b["+strconv.Itoa(col)+"G")
		return err
	}
	if err := refresh(); err != nil {
		return "", false, err
	}
	buf := make([]byte, 4096)
	var pending []byte
	skipLF := false
	for {
		n, rerr := in.Read(buf)
		pending = append(pending, buf[:n]...)
		for len(pending) > 0 && utf8.FullRune(pending) {
			r, size := utf8.DecodeRune(pending)
			pending = pending[size:]
			if skipLF && r == '\n' {
				skipLF = false
				continue
			}
			skipLF = false
			switch {
			case r == '\r' || r == '\n':
				skipLF = r == '\r'
				if _, err := io.WriteString(out, "\r\n"); err != nil {
					return "", false, err
				}
				return string(text), true, nil
			case r == 0x03:
				return "", false, &WitnessReviewTTYError{"ABORTED", "Aborted with Ctrl+C"}
			case r == 0x04:
				if len(text) == 0 {
					return "", false, &WitnessReviewTTYError{"ABORTED", "Aborted with Ctrl+D"}
				}
			case r == 0x7f || r == 0x08:
				if len(text) > 0 {
					text = text[:len(text)-1]
					if err := refresh(); err != nil {
						return "", false, err
					}
				}
			case r < 0x20:
				// unimplemented control key
			default:
				text = append(text, r)
				if _, err := io.WriteString(out, string(r)); err != nil {
					return "", false, err
				}
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				return "", false, nil
			}
			return "", false, rerr
		}
	}
}
