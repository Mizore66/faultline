//go:build !unix && !windows

package cli

import (
	"io"
	"os"
)

// terminalQuestion has no raw terminal on this platform: handled is false and
// the caller falls back to one prompt and one line.
func terminalQuestion(in *os.File, out io.Writer, sizeFile *os.File, prompt string) (line string, answered, handled bool, err error) {
	return "", false, false, nil
}
