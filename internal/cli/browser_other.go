//go:build !windows

package cli

import "github.com/Mizore66/faultline/internal/nodeproc"

// startShell starts child_process.exec(command)'s child on POSIX: /bin/sh
// -c command, stdout and stderr piped (exec buffers them; the openers print
// far below its maxBuffer). wait reports whether exec's callback gets an
// error: a spawn failure, a non-zero exit or a signal.
func startShell(command string) (wait func() bool) {
	discard := func([]byte) {}
	child := nodeproc.Spawn("/bin/sh", []string{"-c", command}, nodeproc.SpawnOptions{
		Stdout: nodeproc.StdioPipe, Stderr: nodeproc.StdioPipe, OnStdout: discard, OnStderr: discard,
	})
	return func() bool {
		exit := child.Wait()
		return exit.Err != nil || exit.Signal != "" || exit.Code == nil || *exit.Code != 0
	}
}
