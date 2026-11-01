//go:build unix

package servefixture

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// ownership is the child's own process group: fl is its leader, so cleanup can
// never reach the test runner's group.
type ownership struct{ pgid int }

func prepare(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func takeOwnership(cmd *exec.Cmd) ownership { return ownership{pgid: cmd.Process.Pid} }

// kill force-terminates the owned group; only Start's failure path and Dispose
// use it, never a normal assertion path.
func (p *Started) kill() { _ = syscall.Kill(-p.own.pgid, syscall.SIGKILL) }

// Signal sends sig to the child process itself (not its group).
func (p *Started) Signal(sig syscall.Signal) error {
	if p.Exited() {
		return errors.New("fl has already exited")
	}
	return p.Cmd.Process.Signal(sig)
}

// Interrupt is a real SIGINT delivered to the child. consoleHelper is the
// Windows-only emitter and is unused here.
func (p *Started) Interrupt(ctx context.Context, consoleHelper string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.Signal(syscall.SIGINT)
}

// Terminate is a real SIGTERM delivered to the child.
func (p *Started) Terminate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.Signal(syscall.SIGTERM)
}

var signalNames = map[syscall.Signal]string{
	syscall.SIGINT: "SIGINT", syscall.SIGTERM: "SIGTERM", syscall.SIGKILL: "SIGKILL",
	syscall.SIGHUP: "SIGHUP", syscall.SIGQUIT: "SIGQUIT", syscall.SIGPIPE: "SIGPIPE",
}

// exitStatus records the raw wait signal and the exit code separately; a signal
// death is never turned into 128+sig.
func exitStatus(state *os.ProcessState) (code *int, signal *string) {
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok {
		return nil, nil
	}
	if status.Signaled() {
		name, known := signalNames[status.Signal()]
		if !known {
			name = status.Signal().String()
		}
		return nil, &name
	}
	c := status.ExitStatus()
	return &c, nil
}
