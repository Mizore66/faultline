//go:build !windows

package nativefixture

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func processTestBlockingCommand(t *testing.T, ctx context.Context, exe string, p processTestPipes) *exec.Cmd {
	t.Helper()
	cmd := exec.CommandContext(ctx, exe, processTestArg, "block", "3", "4")
	cmd.ExtraFiles = []*os.File{p.readyWrite, p.blockRead}
	return cmd
}

func processTestDrainHelper(args []string) int {
	if len(args) != 1 {
		return 9
	}
	fd, err := strconv.Atoi(args[0])
	if err != nil {
		return 9
	}
	if err := syscall.Sendmsg(fd, []byte{'D'}, syscall.UnixRights(int(os.Stdout.Fd())), nil, 0); err != nil {
		return 9
	}
	return 0
}

func newProcessDrainFixture(t *testing.T) processDrainFixture {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	syscall.CloseOnExec(fds[0])
	syscall.CloseOnExec(fds[1])
	parent, child := os.NewFile(uintptr(fds[0]), "owned-drain-parent"), os.NewFile(uintptr(fds[1]), "owned-drain-child")
	t.Cleanup(func() { parent.Close(); child.Close() })
	connection, err := net.FileConn(parent)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	socket := connection.(*net.UnixConn)
	return processDrainFixture{
		command: func(ctx context.Context, exe string, status int) *exec.Cmd {
			cmd := exec.CommandContext(ctx, exe, processTestArg, "pipe-drain", "3", strconv.Itoa(status))
			cmd.ExtraFiles = []*os.File{child}
			return cmd
		},
		receive: func() (*os.File, error) {
			if err := socket.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				return nil, err
			}
			var data [1]byte
			ancillary := make([]byte, syscall.CmsgSpace(4))
			n, count, flags, _, err := socket.ReadMsgUnix(data[:], ancillary)
			if err != nil {
				return nil, err
			}
			messages, err := syscall.ParseSocketControlMessage(ancillary[:count])
			if err != nil {
				return nil, err
			}
			var rights []int
			for _, message := range messages {
				fds, err := syscall.ParseUnixRights(&message)
				if err != nil {
					for _, fd := range rights {
						syscall.Close(fd)
					}
					return nil, err
				}
				rights = append(rights, fds...)
			}
			if n != 1 || data[0] != 'D' || flags&syscall.MSG_CTRUNC != 0 || len(rights) != 1 {
				for _, fd := range rights {
					syscall.Close(fd)
				}
				return nil, fmt.Errorf("invalid owned descriptor transfer")
			}
			syscall.CloseOnExec(rights[0])
			return os.NewFile(uintptr(rights[0]), "owned-held-stdout"), nil
		},
	}
}

func TestNativeProcessObservedUnixSignal(t *testing.T) {
	d := testProcessDeps()
	var cmd *exec.Cmd
	d.command = func(ctx context.Context, exe string, args ...string) *exec.Cmd {
		cmd = exec.CommandContext(ctx, exe, processTestArg, "signal")
		return cmd
	}
	c, err := testExecutor(t, d).Run(context.Background(), testMaterialized(t))
	requireProcessError(t, c, err, "SIGNAL")
	requireReaped(t, cmd)
	if c.Status != nil || c.Signal != "killed" || string(c.Stdout) != "before signal\n" || len(c.Stderr) != 0 || c.Incomplete {
		t.Fatalf("not observed Unix signal evidence: %+v", c)
	}
}
