//go:build faultline_test && unix

package difftest

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/nodeproc"
)

// spawnProbe is the oracle's record of one child_process.spawn.
type spawnProbe struct {
	Events []string `json:"events"`
	Code   *int     `json:"code"`
	Signal *string  `json:"signal"`
	Error  *string  `json:"error"`
	Close  []any    `json:"close"`
	Stdout string   `json:"stdout"`
	Stderr string   `json:"stderr"`
}

// TestLiveSpawn runs the same children through Node's child_process.spawn
// and nodeproc.Spawn and compares the code, signal and error the call
// sites settle on, both outputs, and the event order. It also checks what
// Node emits after a failed spawn: 'error', then 'close' with the negative
// errno as the code.
func TestLiveSpawn(t *testing.T) {
	o := oracle.Start(t)
	defer o.Close()
	dir := t.TempDir()
	noexec := filepath.Join(dir, "noexec")
	if err := os.WriteFile(noexec, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	type spawnCase struct {
		name   string
		file   string
		args   []string
		cwd    string
		env    map[string]string
		stdin  *string
		stdout bool // "pipe", else "ignore"
		kill   string
	}
	str := func(s string) *string { return &s }
	big := strings.Repeat("a", 1<<20)
	cases := []spawnCase{
		{name: "exit 0", file: "/bin/sh", args: []string{"-c", "printf out; printf err >&2"}, stdout: true},
		{name: "exit 3", file: "/bin/sh", args: []string{"-c", "printf x; exit 3"}, stdout: true},
		{name: "stdout ignored", file: "/bin/sh", args: []string{"-c", "printf x; printf e >&2"}},
		{name: "missing executable", file: "faultline-no-such-binary", stdout: true},
		{name: "not executable", file: noexec, stdout: true},
		{name: "missing cwd", file: "/bin/sh", args: []string{"-c", "true"}, cwd: "/faultline-no-such-dir", stdout: true},
		{name: "missing cwd, PATH search", file: "sh", args: []string{"-c", "true"}, cwd: "/faultline-no-such-dir", stdout: true},
		{name: "cwd", file: "/bin/sh", args: []string{"-c", "pwd -P"}, cwd: cwd, stdout: true},
		{name: "env and PATH from env", file: "sh", args: []string{"-c", `printf "$FL_A|$PATH"`},
			env: map[string]string{"FL_A": "a", "PATH": "/usr/bin:/bin"}, stdout: true},
		{name: "stdin", file: "/bin/sh", args: []string{"-c", "cat; printf done >&2"}, stdin: str("in\x00\xff"), stdout: true},
		{name: "stdin closed early", file: "/bin/sh", args: []string{"-c", "exec 0<&-; sleep 0.2; printf x"}, stdin: &big, stdout: true},
		{name: "kill SIGTERM", file: "/bin/sh", args: []string{"-c", "printf go; exec sleep 30"}, stdout: true, kill: "SIGTERM"},
		{name: "kill SIGKILL", file: "/bin/sh", args: []string{"-c", "printf go; exec sleep 30"}, stdout: true, kill: "SIGKILL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdio := map[bool]string{true: "pipe", false: "ignore"}
			args := map[string]any{"file": tc.file, "args": tc.args, "stdout": stdio[tc.stdout], "stderr": "pipe"}
			if tc.args == nil {
				args["args"] = []string{}
			}
			if tc.cwd != "" {
				args["cwd"] = tc.cwd
			}
			if tc.env != nil {
				args["env"] = tc.env
			}
			if tc.stdin != nil {
				args["stdin"] = toLatin1(*tc.stdin)
			}
			if tc.kill != "" {
				args["kill"] = tc.kill
			}
			req, _ := json.Marshal(args)
			res, err := o.Call("spawnProbe", string(req))
			if err != nil {
				t.Fatal(err)
			}
			var want spawnProbe
			if err := json.Unmarshal([]byte(res), &want); err != nil {
				t.Fatal(err)
			}
			want.Stdout, want.Stderr = string(latin1(want.Stdout)), string(latin1(want.Stderr))

			opts := nodeproc.SpawnOptions{Cwd: tc.cwd, Stderr: nodeproc.StdioPipe}
			for _, k := range sortedKeys(tc.env) {
				opts.Env = append(opts.Env, k+"="+tc.env[k])
			}
			if tc.stdin != nil {
				opts.StdinPipe, opts.Stdin = true, []byte(*tc.stdin)
			}
			var stdout, stderr bytes.Buffer
			var child *nodeproc.Child
			if tc.stdout {
				opts.Stdout = nodeproc.StdioPipe
				opts.OnStdout = func(b []byte) {
					stdout.Write(b)
					if tc.kill != "" && stdout.Len() == len(b) {
						child.Kill(tc.kill)
					}
				}
			}
			opts.OnStderr = func(b []byte) { stderr.Write(b) }
			child = nodeproc.Spawn(tc.file, tc.args, opts)
			exit := child.Wait()

			events := []string{"exit", "close"}
			if exit.Err != nil {
				events = []string{"error", "close"}
			}
			if !slices.Equal(want.Events, events) {
				t.Errorf("events: node %q, go %q", want.Events, events)
			}
			if !equalPtr(want.Code, exit.Code) || deref(want.Signal) != exit.Signal {
				t.Errorf("code/signal: node %v/%q, go %v/%q", fmtPtr(want.Code), deref(want.Signal), fmtPtr(exit.Code), exit.Signal)
			}
			goErr := ""
			if exit.Err != nil {
				goErr = exit.Err.Error()
			}
			if deref(want.Error) != goErr {
				t.Errorf("error: node %q, go %q", deref(want.Error), goErr)
			}
			if want.Stdout != stdout.String() || want.Stderr != stderr.String() {
				t.Errorf("output: node %q/%q, go %q/%q", want.Stdout, want.Stderr, stdout.String(), stderr.String())
			}
			if want.Error != nil {
				// 'close' after a failed spawn carries -errno and no signal.
				code := (*want.Error)[strings.LastIndexByte(*want.Error, ' ')+1:]
				errno := map[string]syscall.Errno{"ENOENT": syscall.ENOENT, "EACCES": syscall.EACCES}[code]
				if len(want.Close) != 2 || want.Close[0] != float64(-int(errno)) || want.Close[1] != nil {
					t.Errorf("close after %s: %v", code, want.Close)
				}
			}
		})
	}
}

// toLatin1 carries arbitrary bytes through the oracle's JSON as one char
// per byte; latin1 (cases.go) undoes it.
func toLatin1(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		b.WriteRune(rune(s[i]))
	}
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func equalPtr(a, b *int) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func fmtPtr(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
