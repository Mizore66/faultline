// Command fakebox is the owned fake toolchain of the guided-investigate
// oracle. It is copied to <dir>/docker, <dir>/git and <dir>/node (with .exe
// on Windows); its behavior depends only on its own file name. Every call is
// appended to <dir>/<tool>.log as one record: terminated by 0x1e, arguments
// joined by 0x1f. It models nothing beyond the calls the oracle cases need
// and refuses everything else with exit 125.
//
//	docker --version   fixed version line
//	docker info ...    fixed server version
//	docker run ...     reads <bind src>/state.txt: "bad" fails the predicate,
//	                   anything else passes (the play-back of
//	                   difftest/testdata/fakedocker/pass-fail)
//	docker rm ...      exit 0
//	docker image inspect --format F <ref>
//	                   one RepoDigest of the ref without its tag whose hex is
//	                   the SHA-256 of the ref text (the runtime-digest play-back)
//	node --version     the text of <dir>/node-version, else v22.22.2 (only
//	                   that call; the real runtime is never used)
//	docker info        exits 1 with a daemon-down message when <dir>/docker-down exists
//	git ... --version  fixed version line
//	git anything else  the real git whose path is the text of <dir>/real-git
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakebox:", err)
		os.Exit(126)
	}
	dir := filepath.Dir(exe)
	tool := strings.TrimSuffix(strings.ToLower(filepath.Base(exe)), ".exe")
	args := os.Args[1:]
	record := strings.Join(args, "\x1f") + "\x1e"
	if f, err := os.OpenFile(filepath.Join(dir, tool+".log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		f.WriteString(record) // one write per record
		f.Close()
	}
	switch tool {
	case "docker":
		docker(dir, args)
	case "node":
		if len(args) == 1 && args[0] == "--version" {
			version := "v22.22.2\n"
			if b, err := os.ReadFile(filepath.Join(dir, "node-version")); err == nil {
				version = string(b)
			}
			fmt.Print(version)
			return
		}
		fmt.Fprintln(os.Stderr, "fake node: unsupported call")
		os.Exit(125)
	case "git":
		realGit(dir, args)
	default:
		fmt.Fprintf(os.Stderr, "fakebox: unknown personality %q\n", tool)
		os.Exit(126)
	}
}

func docker(dir string, args []string) {
	if len(args) == 0 {
		os.Exit(125)
	}
	switch args[0] {
	case "--version":
		fmt.Print("Docker version 27.0.0, build fake\n")
	case "info":
		if _, err := os.Stat(filepath.Join(dir, "docker-down")); err == nil {
			fmt.Fprint(os.Stderr, "Cannot connect to the Docker daemon (fake)\n")
			os.Exit(1)
		}
		fmt.Print("27.0.0\n")
	case "rm":
	case "image":
		if len(args) != 5 || args[1] != "inspect" {
			fmt.Fprintln(os.Stderr, "fake docker: unsupported image command")
			os.Exit(125)
		}
		ref := args[4]
		repo := ref
		if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
			repo = ref[:i]
		}
		sum := sha256.Sum256([]byte(ref))
		fmt.Printf("[\"%s@sha256:%s\"]\n", repo, hex.EncodeToString(sum[:]))
	case "run":
		src, prev := "", ""
		for _, a := range args {
			if prev == "--mount" && strings.HasPrefix(a, "type=bind,src=") {
				src = strings.SplitN(strings.TrimPrefix(a, "type=bind,src="), ",", 2)[0]
			}
			prev = a
		}
		b, _ := os.ReadFile(filepath.Join(src, "state.txt"))
		state := strings.TrimRight(string(b), "\n")
		if state == "bad" {
			fmt.Printf("state=%s\nFAULTLINE_WITNESS_RESULT={\"protocol\":\"faultline.witness-result.v1\",\"outcome\":\"PREDICATE_FAIL\"}\n", state)
			fmt.Fprint(os.Stderr, "witness failed")
			os.Exit(1)
		}
		fmt.Printf("state=%s\nFAULTLINE_WITNESS_RESULT={\"protocol\":\"faultline.witness-result.v1\",\"outcome\":\"PREDICATE_PASS\"}\n", state)
	default:
		fmt.Fprintf(os.Stderr, "fake docker: unsupported command: %s\n", args[0])
		os.Exit(125)
	}
}

func realGit(dir string, args []string) {
	if _, err := os.Stat(filepath.Join(dir, "show-fails")); err == nil {
		for _, a := range args {
			if a == "show" {
				fmt.Fprint(os.Stderr, "fake: HEAD unreadable\n")
				os.Exit(128)
			}
		}
	}
	if len(args) > 0 && args[len(args)-1] == "--version" {
		fmt.Print("git version 2.99.0\n")
		return
	}
	b, err := os.ReadFile(filepath.Join(dir, "real-git"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake git: no real-git:", err)
		os.Exit(126)
	}
	cmd := exec.Command(strings.TrimSpace(string(b)), args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "fake git:", err)
		os.Exit(126)
	}
}
