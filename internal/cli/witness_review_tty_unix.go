//go:build unix

package cli

import (
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/Mizore66/faultline/internal/term"
)

// terminalQuestion asks the approval question on a real unix terminal: raw
// mode, the terminal's own width, SIGWINCH redraws and Ctrl+Z job control,
// as Node's readline does.
func terminalQuestion(in *os.File, out io.Writer, sizeFile *os.File, prompt string) (line string, answered, handled bool, err error) {
	restore, raw := term.MakeRaw(in)
	if !raw {
		return "", false, false, nil
	}
	defer restore()
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	cont := make(chan os.Signal, 1)
	signal.Notify(cont, syscall.SIGCONT)
	defer signal.Stop(cont)
	resize, cc := make(chan struct{}, 1), make(chan struct{}, 1)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-winch:
				select {
				case resize <- struct{}{}:
				default:
				}
			case <-cont:
				select {
				case cc <- struct{}{}:
				default:
				}
			case <-done:
				return
			}
		}
	}()
	line, answered, err = nodeReadlineQuestion(in, out, prompt, nodeReadlineOptions{
		Columns: func() int { return term.Columns(sizeFile) },
		Resize:  resize,
		SetRaw: func(on bool) {
			if on {
				term.MakeRawAgain(in)
			} else {
				restore()
			}
		},
		Suspend:  func() { _ = syscall.Kill(os.Getpid(), syscall.SIGTSTP) },
		Continue: cc,
	})
	return line, answered, true, err
}
