//go:build windows

package cli

import (
	"io"
	"os"

	"github.com/Mizore66/faultline/internal/term"
)

// terminalQuestion asks the approval question on a Windows console as Node's
// readline does: the console input handle in libuv's raw mode (no echo, no
// line input, Ctrl+C as byte 0x03), key events translated to libuv's byte
// stream, output with virtual-terminal processing, and the byte-exact readline
// port above. Ctrl+Z has no job control on Windows, so no Suspend is given.
func terminalQuestion(in *os.File, out io.Writer, sizeFile *os.File, prompt string) (line string, answered, handled bool, err error) {
	restore, raw := term.MakeRaw(in)
	if !raw {
		return "", false, false, nil
	}
	defer restore()
	if sizeFile != nil {
		defer term.EnableVT(sizeFile)()
	}
	reader := term.NewConsoleReader(in)
	defer reader.Close()
	line, answered, err = nodeReadlineQuestion(reader, out, prompt, nodeReadlineOptions{
		Columns: func() int { return term.Columns(sizeFile) },
		Resize:  reader.Resize,
	})
	return line, answered, true, err
}
