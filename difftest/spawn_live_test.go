//go:build faultline_test && unix

package difftest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/hostcmd"
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
		// Round 9 (r9-2).
		{name: "huge stderr then exit", file: "/bin/sh", args: []string{"-c", "head -c 3000000 /dev/zero | tr '\\000' e >&2; printf out"}, stdout: true},
		{name: "grandchild holds pipes", file: "/bin/sh", args: []string{"-c", "(sleep 1; printf late; printf lateerr >&2) & printf early"}, stdout: true},
		{name: "close stdout early", file: "/bin/sh", args: []string{"-c", "exec 1>&-; sleep 0.2; printf err >&2"}, stdout: true},
		{name: "kill, grandchild holds pipes", file: "/bin/sh", args: []string{"-c", "(sleep 1; printf gc) & printf go; exec sleep 30"}, stdout: true, kill: "SIGKILL"},
		{name: "huge both", file: "/bin/sh", args: []string{"-c", "head -c 2000000 /dev/zero | tr '\\000' o & head -c 2000000 /dev/zero | tr '\\000' e >&2; wait"}, stdout: true},
		{name: "self SIGTERM after output", file: "/bin/sh", args: []string{"-c", "head -c 200000 /dev/zero | tr '\\000' o; kill -TERM $$"}, stdout: true},
		{name: "fds", file: "/bin/sh", args: []string{"-c", "ls /dev/fd/ | sort -n | tr '\\n' ' '"}, stdout: true},
		{name: "fds with stdin", file: "/bin/sh", args: []string{"-c", "ls /dev/fd/ | sort -n | tr '\\n' ' '"}, stdin: str("x"), stdout: true},
		{name: "real-time signal", file: "/bin/sh", args: []string{"-c", "printf x; kill -40 $$"}, stdout: true},
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

// hostCmdScript runs the TS host-command runners on the cases read from
// stdin and prints their results. "doctor" and "runtime" are the compiled
// bodies of createNodeDoctorRunner (dist/doctor.js) and
// createNodeDockerImageRunner (dist/runtime.js) with their output-cap
// constants replaced by the case's cap; "doctorExport" is the exported
// createNodeDoctorRunner itself (DOCTOR_MAX_OUTPUT_BYTES).
const hostCmdScript = `
import { readFileSync } from "node:fs";
import { spawn } from "node:child_process";
const dist = process.argv[2];
const extract = (file, name, caps) => {
  const src = readFileSync(dist + "/" + file, "utf8");
  const start = src.indexOf("function " + name + "() {");
  const end = src.indexOf("\n}\n", start);
  if (start < 0 || end < 0) throw new Error(name + " not found in " + file);
  let body = src.slice(start, end + 2);
  for (const cap of caps) {
    if (!body.includes(cap)) throw new Error(cap + " not in " + name);
    body = body.split(cap).join("CAP");
  }
  return new Function("spawn", "CAP", body + "\nreturn " + name + "();");
};
const make = {
  doctor: extract("doctor.js", "createNodeDoctorRunner", ["DOCTOR_MAX_OUTPUT_BYTES"]),
  runtime: extract("runtime.js", "createNodeDockerImageRunner",
    ["DOCKER_IMAGE_PULL_MAX_OUTPUT_BYTES", "DOCKER_PROJECT_IMAGE_BUILD_MAX_OUTPUT_BYTES", "DOCKER_IMAGE_INSPECT_MAX_OUTPUT_BYTES"]),
};
const { createNodeDoctorRunner } = await import(dist + "/doctor.js");
const cases = JSON.parse(readFileSync(0, "utf8"));
const out = [];
for (const c of cases) {
  const runner = c.runner === "doctorExport" ? createNodeDoctorRunner() : make[c.runner](spawn, c.cap);
  out.push(await runner.run({ executable: c.file, arguments: c.args, timeoutMs: c.timeoutMs }));
}
process.stdout.write(JSON.stringify(out));
`

type hostCmdCase struct {
	Runner    string   `json:"runner"`
	File      string   `json:"file"`
	Args      []string `json:"args"`
	Cap       int      `json:"cap"`
	TimeoutMs int      `json:"timeoutMs"`
}

type hostCmdResult struct {
	ExitCode            *int   `json:"exitCode"`
	Stdout              string `json:"stdout"`
	Stderr              string `json:"stderr"`
	TimedOut            bool   `json:"timedOut"`
	OutputLimitExceeded bool   `json:"outputLimitExceeded"`
}

// TestLiveHostCmd runs the same commands through the TS host-command
// runners and hostcmd.Run and compares the results: 30 generated sh
// programs that write random UTF-8 (4-byte characters, invalid bytes) to
// stdout and stderr in random chunks, cut inside code points, under caps
// of 1 to 200 bytes, ending in an exit code, a signal or a timeout well
// after the last write; each
// runs under doctor's runner ("" error join) and runtime's ("\n"). Writes
// are 60 ms apart so each is one chunk on both sides, and programs outlive
// an overflow so the SIGKILL decides the exit code on both sides.
func TestLiveHostCmd(t *testing.T) {
	oracle.Skip(t)
	repo := oracle.RepoRoot(t)
	dist := filepath.Join(repo, "dist")
	if _, err := os.Stat(filepath.Join(dist, "doctor.js")); err != nil {
		t.Fatalf("build dist/ first (pnpm build): %v", err)
	}
	r := rand.New(rand.NewPCG(73, 1))
	chars := []string{"a", "z", " ", "\n", "é", "€", "\U0001F600", "\U00010348"}
	octal := func(b []byte) string {
		var s strings.Builder
		for _, c := range b {
			fmt.Fprintf(&s, `\%03o`, c)
		}
		return s.String()
	}
	var cases []hostCmdCase
	for i := range 30 {
		var text []byte
		for n := 5 + r.IntN(60); n > 0; n-- {
			text = append(text, chars[r.IntN(len(chars))]...)
		}
		if r.IntN(4) == 0 {
			text = slices.Insert(text, r.IntN(len(text)), 0xff, 0x80)
		}
		var script []string
		for len(text) > 0 {
			k := min(len(text), 1+r.IntN(40))
			redirect := ""
			if r.IntN(3) == 0 {
				redirect = " >&2"
			}
			script = append(script, "printf '"+octal(text[:k])+"'"+redirect, "sleep 0.06")
			text = text[k:]
		}
		timeoutMs := 5000
		switch i % 5 {
		case 0, 1:
			script = append(script, "sleep 0.2", fmt.Sprintf("exit %d", r.IntN(4)))
		case 2:
			script = append(script, "sleep 0.2", "kill -TERM $$")
		case 3:
			script = append(script, "sleep 0.2", "kill -KILL $$")
		case 4:
			// The timeout falls 2 s after the last write is due and 2 s
			// before the final sleep ends, so the output the SIGKILL
			// leaves is all of it on both sides. (With 400 ms of slack, a
			// loaded macOS runner was still forking the sleeps when the
			// timer fired: Node kept the last chunk in one case and Go in
			// its twin, CI round 1.) Both sides read until EOF after the
			// kill, as 'close' waits for the streams to end; output racing
			// the kill is timing, not runner behaviour, so it is not
			// compared.
			timeoutMs = 60*len(script)/2 + 2000
			script = append(script, "sleep 4")
		}
		cp := 1 + r.IntN(200)
		args := []string{"-c", strings.Join(script, "; ")}
		for _, runner := range []string{"doctor", "runtime"} {
			cases = append(cases, hostCmdCase{runner, "sh", args, cp, timeoutMs})
		}
	}
	for _, runner := range []string{"doctor", "runtime"} {
		cases = append(cases,
			hostCmdCase{runner, "faultline-no-such-tool", []string{}, 64, 1000},
			hostCmdCase{runner, "sh", []string{"-c", "true", "a\x00b"}, 64, 1000}, // spawn throws
			hostCmdCase{runner, "sh", []string{"-c", "printf abc; printf def >&2; exit 2"}, 200, 5000},
			// After the timeout's SIGKILL a grandchild still holds the
			// pipes and writes: both runners keep reading until the
			// streams end ('close'), so "late" is kept.
			hostCmdCase{runner, "sh", []string{"-c", "printf early; (sleep 1.5; printf late; printf err >&2) & sleep 5"}, 200, 500},
		)
	}
	// The real export, around DOCTOR_MAX_OUTPUT_BYTES (64 KiB).
	big := func(ascii int, tail string, rest string) []string {
		return []string{"-c", fmt.Sprintf("head -c %d /dev/zero | tr '\\000' a; sleep 0.1; printf '%s'; sleep 0.1; %s", ascii, octal([]byte(tail)), rest)}
	}
	cases = append(cases,
		hostCmdCase{"doctorExport", "sh", big(65534, "\U0001F600", "sleep 0.3; exit 0"), 65536, 5000},
		hostCmdCase{"doctorExport", "sh", big(65532, "\U0001F600", "sleep 0.3; exit 1"), 65536, 5000},
		hostCmdCase{"doctorExport", "sh", big(65536, "x", "sleep 0.3; exit 0"), 65536, 5000},
	)

	req, _ := json.Marshal(cases)
	script := filepath.Join(t.TempDir(), "hostcmd.mjs")
	if err := os.WriteFile(script, []byte(hostCmdScript), 0o644); err != nil {
		t.Fatal(err)
	}
	// Node works through its cases while Go works through the same ones
	// (both mostly sleep).
	var raw bytes.Buffer
	cmd := exec.Command("node", script, dist)
	cmd.Stdin = bytes.NewReader(req)
	cmd.Stdout = &raw
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	gots := make([]hostcmd.Result, len(cases))
	for i, c := range cases {
		join := map[string]string{"runtime": "\n"}[c.Runner]
		got, err := hostcmd.Run(c.File, c.Args, hostcmd.Options{Timeout: time.Duration(c.TimeoutMs) * time.Millisecond, MaxOutputBytes: c.Cap, ErrorJoin: join})
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		gots[i] = got
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	var want []hostCmdResult
	if err := json.Unmarshal(raw.Bytes(), &want); err != nil || len(want) != len(cases) {
		t.Fatalf("node: %v (%d results for %d cases)", err, len(want), len(cases))
	}
	limited, timedOut, signalled := 0, 0, 0
	for i, c := range cases {
		got, w := gots[i], want[i]
		if !equalPtr(w.ExitCode, got.ExitCode) || w.Stdout != got.Stdout || w.Stderr != got.Stderr ||
			w.TimedOut != got.TimedOut || w.OutputLimitExceeded != got.OutputLimitExceeded {
			t.Errorf("case %d (%s, cap %d, %.200q):\nnode %v %q %q timedOut=%v limit=%v\ngo   %v %q %q timedOut=%v limit=%v",
				i, c.Runner, c.Cap, c.Args, fmtPtr(w.ExitCode), w.Stdout, w.Stderr, w.TimedOut, w.OutputLimitExceeded,
				fmtPtr(got.ExitCode), got.Stdout, got.Stderr, got.TimedOut, got.OutputLimitExceeded)
		}
		if strings.Contains(strings.Join(c.Args, " "), "printf late") && (w.Stdout != "earlylate" || w.Stderr != "err" || !w.TimedOut) {
			t.Errorf("case %d: node did not read past the kill: %q %q timedOut=%v", i, w.Stdout, w.Stderr, w.TimedOut)
		}
		if w.OutputLimitExceeded {
			limited++
		}
		if w.TimedOut {
			timedOut++
		}
		if w.ExitCode == nil && !w.TimedOut && !w.OutputLimitExceeded {
			signalled++
		}
	}
	t.Logf("%d cases: %d over the cap, %d timed out, %d with a null exit code otherwise", len(cases), limited, timedOut, signalled)
}
