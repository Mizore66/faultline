//go:build windows

package nativefixture

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
)

func processTestDrainHelper(args []string) int {
	if len(args) != 2 {
		return 9
	}
	ready, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		return 9
	}
	pid, err := strconv.ParseUint(args[1], 10, 32)
	if err != nil {
		return 9
	}
	parent, err := syscall.OpenProcess(0x0040, false, uint32(pid))
	if err != nil {
		return 9
	}
	defer syscall.CloseHandle(parent)
	current, err := syscall.GetCurrentProcess()
	if err != nil {
		return 9
	}
	var held syscall.Handle
	if err := syscall.DuplicateHandle(current, syscall.Handle(os.Stdout.Fd()), parent, &held, 0, false, syscall.DUPLICATE_SAME_ACCESS); err != nil {
		return 9
	}
	file := os.NewFile(uintptr(ready), "owned-drain-ready")
	defer file.Close()
	var data [8]byte
	binary.LittleEndian.PutUint64(data[:], uint64(held))
	if n, err := file.Write(data[:]); err != nil || n != len(data) {
		var local syscall.Handle
		if err := syscall.DuplicateHandle(parent, held, current, &local, 0, false, syscall.DUPLICATE_SAME_ACCESS|syscall.DUPLICATE_CLOSE_SOURCE); err == nil {
			syscall.CloseHandle(local)
		}
		return 9
	}
	return 0
}

func newProcessDrainFixture(t *testing.T) processDrainFixture {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { read.Close(); write.Close() })
	handle := syscall.Handle(write.Fd())
	if err := syscall.SetHandleInformation(handle, syscall.HANDLE_FLAG_INHERIT, syscall.HANDLE_FLAG_INHERIT); err != nil {
		t.Fatal(err)
	}
	return processDrainFixture{
		command: func(ctx context.Context, exe string, status int) *exec.Cmd {
			cmd := exec.CommandContext(ctx, exe, processTestArg, "pipe-drain", strconv.FormatUint(uint64(handle), 10), strconv.Itoa(os.Getpid()), strconv.Itoa(status))
			cmd.SysProcAttr = &syscall.SysProcAttr{AdditionalInheritedHandles: []syscall.Handle{handle}}
			return cmd
		},
		receive: func() (*os.File, error) {
			var data [8]byte
			if _, err := io.ReadFull(read, data[:]); err != nil {
				return nil, err
			}
			held := binary.LittleEndian.Uint64(data[:])
			if held == 0 {
				return nil, fmt.Errorf("invalid owned descriptor transfer")
			}
			return os.NewFile(uintptr(held), "owned-held-stdout"), nil
		},
	}
}

func processTestBlockingCommand(t *testing.T, ctx context.Context, exe string, p processTestPipes) *exec.Cmd {
	t.Helper()
	handles := []syscall.Handle{syscall.Handle(p.readyWrite.Fd()), syscall.Handle(p.blockRead.Fd())}
	for _, handle := range handles {
		if err := syscall.SetHandleInformation(handle, syscall.HANDLE_FLAG_INHERIT, syscall.HANDLE_FLAG_INHERIT); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(ctx, exe, processTestArg, "block", strconv.FormatUint(uint64(handles[0]), 10), strconv.FormatUint(uint64(handles[1]), 10))
	cmd.SysProcAttr = &syscall.SysProcAttr{AdditionalInheritedHandles: handles}
	return cmd
}
