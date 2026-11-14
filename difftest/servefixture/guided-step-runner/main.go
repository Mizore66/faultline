// Command guided-step-runner is test-only capture support, not part of fl. It runs ONE scripted step of
// the frozen Node guided CLI under the servefixture process primitives, so that on Windows the child gets
// its own console, is owned by its handle, and receives a genuine CTRL_C_EVENT through the existing
// console helper; plain child.kill("SIGINT") from Node is not equivalent there. Expected output is
// whatever the Node child does; this program only observes and relays.
//
//	guided-step-runner --helper H --cwd D --env-file F --exe NODE -- ARGS...
//
// It prints "READY <json>" once the five review-readiness stderr lines were seen, reads "SIGINT" and
// "SIGTERM" lines on stdin, and prints "DONE <json>" with the complete raw streams (base64) and the raw
// exit status (exit code, or signal name on POSIX; never a synthesized value).
//
// Failure safety: every path returns through run(), so the deferred Dispose always kills and joins the
// owned child before the process exits nonzero (os.Exit is only called by main, after run returned). The
// step has a deadline, and EOF on stdin (the parent died or closed the pipe) cancels the step. Remaining
// limitation: a hard kill of THIS runner (TerminateProcess/SIGKILL) cannot run any cleanup, and there is no
// job object, so the Node child can then survive until its own deadline; the generator only kills the
// runner after it has already asked this process to stop, and reports the case as failed.
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/Mizore66/faultline/difftest/servefixture"
)

var ready = regexp.MustCompile(`^FaultLine guided investigate: PREFLIGHT\nFaultLine guided investigate: INTAKE \((?:ci-log|resume [^\n]*)\)\nFaultLine guided investigate: WITNESS_REVIEW\nFaultLine local witness review: (http://127\.0\.0\.1:[1-9][0-9]*)\nApprove, then Freeze \(separate clicks\)\. This command continues automatically after freeze\. Press Ctrl\+C to cancel and resume later with --resume <id>\.\n$`)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// config is one step.
type config struct {
	helper, cwd, exe string
	env, args        []string
	limit            time.Duration
}

// usageError marks a bad command line (exit 2); every other error exits 3.
type usageError struct{ error }

func main() {
	helper := flag.String("helper", "", "console helper (Windows)")
	cwd := flag.String("cwd", "", "child working directory")
	envFile := flag.String("env-file", "", "JSON array of KEY=VALUE for the complete child environment")
	exe := flag.String("exe", "", "child executable")
	limit := flag.Duration("timeout", 90*time.Second, "step deadline")
	flag.Parse()
	cfg, err := loadConfig(*helper, *cwd, *envFile, *exe, *limit, flag.Args())
	if err == nil {
		err = run(cfg, os.Stdin, os.Stdout, nil)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "guided-step-runner:", err)
		var u usageError
		if errors.As(err, &u) {
			os.Exit(2)
		}
		os.Exit(3)
	}
}

func loadConfig(helper, cwd, envFile, exe string, limit time.Duration, args []string) (config, error) {
	if cwd == "" || envFile == "" || exe == "" || len(args) == 0 {
		return config{}, usageError{errors.New("--cwd, --env-file, --exe and child arguments are required")}
	}
	raw, err := os.ReadFile(envFile)
	var env []string
	if err == nil {
		err = json.Unmarshal(raw, &env)
	}
	if err != nil {
		return config{}, usageError{fmt.Errorf("env file: %w", err)}
	}
	return config{helper: helper, cwd: cwd, exe: exe, env: env, args: args, limit: limit}, nil
}

// run executes one step. Every return path has already disposed (killed and joined) the child. onStart, when
// non-nil, receives the started command so a test can observe liveness.
func run(cfg config, in io.Reader, out io.Writer, onStart func(*exec.Cmd)) error {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.limit)
	defer cancel()
	var readyStderr string
	p, err := servefixture.StartWithOptions(ctx, cfg.exe, nil, cfg.args, cfg.env, cfg.cwd, servefixture.StartOptions{
		StartupLines: 5, StartupDescription: "guided review readiness", ReadyStream: "stderr",
		ValidateStartup: func(raw []byte) (string, error) {
			m := ready.FindStringSubmatch(string(raw))
			if m == nil {
				return "", fmt.Errorf("not the exact readiness stderr: %q", raw)
			}
			readyStderr = string(raw)
			return m[1], nil
		},
	})
	if err != nil {
		// StartWithOptions already killed and joined the child; its error text carries the streams.
		return err
	}
	if onStart != nil {
		onStart(p.Cmd)
	}
	// Runs before cancel (LIFO): the owned child is killed and joined on every path below.
	defer p.Dispose() //nolint:errcheck // a forced kill is reported by Wait's error path below
	if _, err := fmt.Fprintf(out, "READY %s\n", mustJSON(map[string]any{"url": p.URL, "stderr": b64(readyStderr)})); err != nil {
		return fmt.Errorf("writing READY: %w", err)
	}
	signalErr := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(in)
		for sc.Scan() {
			var err error
			switch strings.TrimSpace(sc.Text()) {
			case "SIGINT":
				err = p.Interrupt(ctx, cfg.helper)
			case "SIGTERM":
				err = p.Terminate(ctx)
			default:
				err = errors.New("unknown request " + sc.Text())
			}
			if err != nil {
				signalErr <- fmt.Errorf("signal: %w", err)
				cancel()
				return
			}
		}
		// EOF or a read error: the controlling parent is gone or closed the pipe; stop the step.
		if !p.Exited() {
			signalErr <- errors.New("control pipe closed before the step finished")
			cancel()
		}
	}()
	res, err := p.Wait(ctx)
	if err != nil {
		select {
		case se := <-signalErr:
			return errors.Join(err, se)
		default:
			return err
		}
	}
	if _, err := fmt.Fprintf(out, "DONE %s\n", mustJSON(map[string]any{"exit": res.ExitCode, "signal": res.Signal, "stdout": b64(res.Stdout), "stderr": b64(res.Stderr)})); err != nil {
		return fmt.Errorf("writing DONE: %w", err)
	}
	return nil
}
