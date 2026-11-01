//go:build windows

package servefixture

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// createNewConsole is CREATE_NEW_CONSOLE: the child gets its own console so a
// CTRL_C_EVENT reaches only it. CREATE_NEW_PROCESS_GROUP alone would change
// Ctrl+C eligibility and is deliberately not used.
const createNewConsole = 0x10

// ownership is the child's process handle (os.Process); there is no group.
type ownership struct{}

func prepare(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewConsole} }

func takeOwnership(*exec.Cmd) ownership { return ownership{} }

// kill is TerminateProcess on the owned handle.
func (p *Started) kill() { _ = p.Cmd.Process.Kill() }

// Interrupt runs the test-only console helper (built natively from
// ./difftest/servefixture/console-helper) with the target PID. The helper frees
// its console, attaches to the target's new console, ignores Ctrl+C itself and
// generates CTRL_C_EVENT; a failure is returned with the helper's output, never
// replaced by Ctrl+Break or termination.
func (p *Started) Interrupt(ctx context.Context, consoleHelper string) error {
	if consoleHelper == "" {
		return errors.New("Windows interrupt requires the console helper executable")
	}
	if p.Exited() {
		return errors.New("fl has already exited")
	}
	var out bytes.Buffer
	helper := exec.CommandContext(ctx, consoleHelper, strconv.Itoa(p.Cmd.Process.Pid))
	helper.Stdout, helper.Stderr = &out, &out
	if err := helper.Run(); err != nil {
		return fmt.Errorf("console helper: %w\n%s", err, out.String())
	}
	return nil
}

// Terminate is TerminateProcess on the owned handle (Node's child kill), not a
// POSIX SIGTERM.
func (p *Started) Terminate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.Exited() {
		return errors.New("fl has already exited")
	}
	return p.Cmd.Process.Kill()
}

// exitStatus is the raw Windows exit status; there is no signal.
func exitStatus(state *os.ProcessState) (code *int, signal *string) {
	c := state.ExitCode()
	return &c, nil
}
