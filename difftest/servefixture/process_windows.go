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
	"sync"
	"syscall"
)

// createNewConsole is CREATE_NEW_CONSOLE: the child gets its own console so a
// CTRL_C_EVENT reaches only it. CREATE_NEW_PROCESS_GROUP alone would change
// Ctrl+C eligibility and is deliberately not used.
const createNewConsole = 0x10

// ownership is the child's process handle (os.Process); there is no group.
type ownership struct{}

// clearConsoleIgnore clears the CALLING TEST PROCESS's own Ctrl+C-ignore
// attribute, SetConsoleCtrlHandler(NULL, FALSE), exactly once before the first
// child is created; the outcome (including a failure) is cached. This is
// process-wide TEST preparation, not production behavior: the attribute is
// inherited by children created afterwards (existing processes are unaffected).
// Measured effect on hosted Windows before this preparation: launched children
// behaved as if the attribute were set and ignored the genuine CTRL_C_EVENT, and
// clearing it in the creating test process restored delivery; the ultimate
// ancestor that set it is unknown. The prior state is unknowable (there is no
// documented getter), so nothing is restored. There is no override or per-target
// injection.
var clearConsoleIgnore = sync.OnceValue(func() error {
	setHandler := syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleCtrlHandler")
	if ok, _, err := setHandler.Call(0, 0); ok == 0 {
		return fmt.Errorf("clearing the test process's Ctrl+C-ignore attribute, SetConsoleCtrlHandler(NULL, FALSE): %w", err)
	}
	return nil
})

// prepare gives the child its own new console only (no process-group flag, which
// would disable Ctrl+C for it) after the test process's inherited ignore
// attribute has been cleared.
func prepare(cmd *exec.Cmd) error {
	if err := clearConsoleIgnore(); err != nil {
		return err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewConsole}
	return nil
}

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
