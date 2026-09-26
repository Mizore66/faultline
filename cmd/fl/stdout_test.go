//go:build linux

package main

import (
	"os"
	"os/signal"
	"syscall"
	"testing"
)

// Node prints "Error: ENOSPC: no space left on device, write" for
// `fl verify … >/dev/full` and "Error: write EPIPE" for a closed pipe.
func TestWriteErrorLineMatchesNode(t *testing.T) {
	full, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
	if err != nil {
		t.Skip(err)
	}
	defer full.Close()
	_, err = full.Write([]byte("x"))
	if got := writeErrorLine(full, err); got != "Error: ENOSPC: no space left on device, write" {
		t.Errorf("file: %q", got)
	}
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
	defer signal.Reset(syscall.SIGPIPE)
	r, w, _ := os.Pipe()
	r.Close()
	_, err = w.Write([]byte("x"))
	if got := writeErrorLine(w, err); got != "Error: write EPIPE" {
		t.Errorf("pipe: %q", got)
	}
	w.Close()
}
