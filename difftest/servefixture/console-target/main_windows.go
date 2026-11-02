//go:build windows

// Command console-target is TEST-ONLY Windows diagnostic support, not part of
// fl and not a production signal fix. It is the smallest process that behaves
// like `fl serve --bundle` at its signal boundary: it subscribes to
// os.Interrupt, binds an ephemeral loopback listener, prints the two startup
// lines, waits for the first interrupt and exits 0.
//
// It takes either no argument (baseline) or exactly --clear-ignore. The only
// difference between the two is one call before the subscription:
// SetConsoleCtrlHandler(NULL, FALSE), which clears THIS process's own
// "ignore Ctrl+C" attribute. The baseline never makes that call.
package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	switch {
	case len(os.Args) == 1:
	case len(os.Args) == 2 && os.Args[1] == "--clear-ignore":
		setHandler := syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleCtrlHandler")
		if ok, _, err := setHandler.Call(0, 0); ok == 0 {
			fmt.Fprintf(os.Stderr, "console-target: SetConsoleCtrlHandler(NULL, FALSE) failed: %v\n", err)
			os.Exit(2)
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: console-target [--clear-ignore]")
		os.Exit(2)
	}
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "console-target: listen: %v\n", err)
		os.Exit(2)
	}
	if _, err := os.Stdout.WriteString("FaultLine read-only Git proof page: http://" + listener.Addr().String() + "\nPress Ctrl+C to stop.\n"); err != nil {
		fmt.Fprintf(os.Stderr, "console-target: stdout: %v\n", err)
		os.Exit(2)
	}
	<-interrupts
	listener.Close()
}
