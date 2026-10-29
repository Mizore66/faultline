package codexloop

import (
	"io"
)

// The fake codex the runner tests execute. On POSIX it is the sh script of
// fakecodex_other_test.go; on Windows it is a copy of the test binary
// (fakecodex_windows_test.go) that plays one of these modes. Both must give
// the same stdout, stderr and status; TestFakeCodexChildMatchesScripts
// compares them byte for byte wherever sh exists.
const (
	modeNope = "nope" // stderr "nope", status 2
	modeCRLF = "crlf" // a padded, CRLF-ended --version
	modeV1   = "v1"   // "v1"
	modeV2   = "v2"   // "codex-cli 2"
	modeExec = "exec" // --version codex-cli 9, else echoes argv, cwd, stdin; err\xff; status 7
)

var fakeModes = []string{modeNope, modeCRLF, modeV1, modeV2, modeExec}

// runFakeCodex plays mode and returns the exit status. cwd is the physical
// working directory (pwd -P). Output is written raw, with no newline
// translation.
func runFakeCodex(mode string, args []string, cwd string, stdin io.Reader, stdout, stderr io.Writer) int {
	switch mode {
	case modeNope:
		io.WriteString(stderr, "nope\n")
		return 2
	case modeCRLF:
		io.WriteString(stdout, "  codex-cli 1.2.3\r\nbuild\r\n")
		return 0
	case modeV1:
		io.WriteString(stdout, "v1\n")
		return 0
	case modeV2:
		io.WriteString(stdout, "codex-cli 2\n")
		return 0
	case modeExec:
		if len(args) > 0 && args[0] == "--version" {
			io.WriteString(stdout, "codex-cli 9\n")
			return 0
		}
		// printf '%s|' "$@" reuses its format once when there are no arguments.
		for _, a := range append(args, "")[:max(len(args), 1)] {
			io.WriteString(stdout, a+"|")
		}
		io.WriteString(stdout, cwd+"\n")
		io.Copy(stdout, stdin)
		io.WriteString(stderr, "err\xff")
		return 7
	}
	io.WriteString(stderr, "unknown fake codex mode "+mode+"\n")
	return 99
}
