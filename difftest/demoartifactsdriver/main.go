//go:build faultline_test

// Command demoartifactsdriver is the test-only one-request adapter around
// difftest/demoartifactstest. It accepts no arbitrary command or script and
// is not part of cmd/fl.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/Mizore66/faultline/difftest/demoartifactstest"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "demoartifactsdriver:", err)
		os.Exit(2)
	}
}

// run returns only adapter failures; the real call's error is data.
func run() (err error) {
	root := flag.String("root", "", "absolute case root")
	repo := flag.String("repo", "", "absolute checkout")
	request := flag.String("request", "", "request JSON file")
	cli := flag.String("cli", "", "actual tagged fl binary (CLI requests only)")
	noInventory := flag.Bool("no-inventory", false, "do not inventory the case root before and after the call (traced fault rows)")
	flag.Parse()
	if *root == "" || *repo == "" || *request == "" || flag.NArg() != 0 {
		return fmt.Errorf("usage: --root <dir> --repo <dir> --request <file> [--cli <fl>]")
	}
	defer func() {
		if r := recover(); r != nil {
			a, ok := r.(demoartifactstest.AdapterError)
			if !ok {
				panic(r)
			}
			err = a
		}
	}()
	body, err := os.ReadFile(*request)
	if err != nil {
		return err
	}
	var req demoartifactstest.Request
	if err := json.Unmarshal(body, &req); err != nil {
		return err
	}
	if err := os.Chdir(*root); err != nil {
		return err
	}
	// Test-driver determinism only: strace counts when= per OS thread, and
	// GOMAXPROCS=1 does not bind a goroutine to one thread. Every filesystem
	// call being compared is made synchronously by this goroutine, so pin it
	// for the whole invocation. Production code never pins threads.
	runtime.LockOSThread()
	invoke := demoartifactstest.Invoke
	if *noInventory {
		invoke = demoartifactstest.InvokeBare
	}
	capture := invoke(*root, *repo, *cli, req)
	runtime.UnlockOSThread()
	out, err := json.Marshal(capture)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(out)
	return err
}
