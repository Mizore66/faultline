package main

import (
	"io"
	"os"

	_ "github.com/Mizore66/faultline/internal/boot" // Node's signal state, first
	"github.com/Mizore66/faultline/internal/cli"
	"github.com/Mizore66/faultline/internal/sigexit"
)

func main() {
	args := nodeArgv()
	stdout := newStdoutWriter(os.Stdout)
	code := cli.Run(args, stdout, os.Stderr)
	sigexit.Hold()
	if stdout.err != nil {
		io.WriteString(os.Stderr, stdout.errorLine()+"\n")
		os.Exit(1)
	}
	os.Exit(code)
}
