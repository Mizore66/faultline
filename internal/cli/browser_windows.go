package cli

import (
	"io"
	"os"
	"os/exec"
	"syscall"
)

// startShell starts child_process.exec(command)'s child on Windows:
// process.env.ComSpec (or cmd.exe) with the verbatim arguments
// `/d /s /c "<command>"`, which nodeproc cannot pass (it quotes each
// argument), so os/exec runs it with an explicit command line. wait reports
// whether exec's callback gets an error.
func startShell(command string) (wait func() bool) {
	shell := processEnv("ComSpec")
	if shell == "" {
		shell = "cmd.exe"
	}
	cmd := exec.Command(shell)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: shell + ` /d /s /c "` + command + `"`, HideWindow: true}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	cmd.Stdin = nil
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return func() bool { return true }
	}
	return func() bool { return cmd.Wait() != nil }
}
