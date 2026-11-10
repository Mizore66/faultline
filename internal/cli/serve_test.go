package cli

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/bundle/demo"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/proofview"
)

type plainServeFake struct {
	options proofview.PlainDemoServerOptions
	port    float64
	starts  int
	closes  int
	url     string
}

func (f *plainServeFake) URL() string { return f.url }
func (f *plainServeFake) Close() error {
	f.closes++
	return nil
}

// installPlainServeFake replaces the starter with a capturing fixture and the
// interrupt wait with one start/close lifecycle.
func installPlainServeFake(t *testing.T) *plainServeFake {
	t.Helper()
	f := &plainServeFake{url: "http://127.0.0.1:43210"}
	oStart, oAwait := plainServeStartServer, plainServeAwaitInterrupt
	plainServeStartServer = func(o proofview.PlainDemoServerOptions, port float64) (plainServer, error) {
		f.starts++
		f.options, f.port = o, port
		return f, nil
	}
	plainServeAwaitInterrupt = func(start func() (func() error, error)) error {
		closeServer, err := start()
		if err != nil {
			return err
		}
		return closeServer()
	}
	t.Cleanup(func() { plainServeStartServer, plainServeAwaitInterrupt = oStart, oAwait })
	return f
}

const plainServeStartup = "FaultLine incident page: http://127.0.0.1:43210\nPress Ctrl+C to stop.\n"

func TestPlainServeDefaultStartupWritesNothing(t *testing.T) {
	t.Chdir(t.TempDir())
	f := installPlainServeFake(t)
	oWrite := plainServeWriteBundle
	plainServeWriteBundle = func(string, jsjson.Value, demo.WriteOptions) (demo.Written, error) {
		t.Error("startup must not write a bundle")
		return demo.Written{}, nil
	}
	t.Cleanup(func() { plainServeWriteBundle = oWrite })
	stdout, stderr, code := run("serve")
	if code != 0 || stderr != "" || stdout != plainServeStartup {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if f.starts != 1 || f.closes != 1 || f.port != 4173 {
		t.Fatalf("starts=%d closes=%d port=%v", f.starts, f.closes, f.port)
	}
	if mode := f.options.Analysis.Get("mode"); mode.Kind() != jsjson.String || mode.Str() != "REPLAY" {
		t.Fatalf("analysis mode = %s", jsjson.Stringify(mode))
	}
	entries, _ := os.ReadDir(".")
	for _, entry := range entries {
		if entry.Name() != ".faultline" {
			t.Errorf("unexpected startup entry %s", entry.Name())
		}
	}
	for _, path := range []string{
		filepath.Join(".faultline", "bundles", "judge-demo"),
		filepath.Join(".faultline", "bundles", "judge-demo.native-execution.json"),
	} {
		if _, err := os.Lstat(path); err == nil {
			t.Errorf("startup wrote %s", path)
		}
	}
}

func TestPlainServeOutputResolution(t *testing.T) {
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"default": {[]string{"serve"}, filepath.Join(".faultline", "bundles", "judge-demo")},
		"custom":  {[]string{"serve", "--output", ".faultline/bundles/mine"}, filepath.Join(".faultline", "bundles", "mine")},
		"nested":  {[]string{"serve", "--output", ".faultline/bundles/a/b/c"}, filepath.Join(".faultline", "bundles", "a", "b", "c")},
	} {
		t.Run(name, func(t *testing.T) {
			work := t.TempDir()
			t.Chdir(work)
			f := installPlainServeFake(t)
			var written []string
			oWrite := plainServeWriteBundle
			plainServeWriteBundle = func(out string, _ jsjson.Value, o demo.WriteOptions) (demo.Written, error) {
				written = append(written, out)
				if o.ProofRoot != nil {
					t.Error("default write options expected")
				}
				return demo.Written{}, nil
			}
			t.Cleanup(func() { plainServeWriteBundle = oWrite })
			stdout, stderr, code := run(c.args...)
			if code != 0 || stderr != "" || stdout != plainServeStartup {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if _, err := f.options.Rerun(); err != nil {
				t.Fatal(err)
			}
			want := nodefs.Resolve(c.want)
			if len(written) != 1 || written[0] != want {
				t.Fatalf("written=%q want %q", written, want)
			}
		})
	}
}

func TestPlainServeRerunCallback(t *testing.T) {
	t.Chdir(t.TempDir())
	f := installPlainServeFake(t)
	var events []string
	var gotAnalysis jsjson.Value
	oWrite := plainServeWriteBundle
	t.Cleanup(func() { plainServeWriteBundle = oWrite })
	plainServeWriteBundle = func(_ string, a jsjson.Value, _ demo.WriteOptions) (demo.Written, error) {
		events = append(events, "write")
		gotAnalysis = a
		return demo.Written{}, nil
	}
	if _, _, code := run("serve"); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	analysis, err := f.options.Rerun()
	if err != nil || len(events) != 1 {
		t.Fatalf("err=%v events=%v", err, events)
	}
	if analysis.Get("mode").Str() != "RERUN" || jsjson.Stringify(analysis) != jsjson.Stringify(gotAnalysis) {
		t.Fatalf("returned analysis is not the written RERUN analysis")
	}
	if f.options.Analysis.Get("mode").Str() != "REPLAY" {
		t.Fatal("served analysis was replaced by the callback itself")
	}
	boom := errors.New("disk full")
	plainServeWriteBundle = func(string, jsjson.Value, demo.WriteOptions) (demo.Written, error) {
		return demo.Written{}, boom
	}
	analysis, err = f.options.Rerun()
	if err != boom || analysis.Kind() != jsjson.Undefined {
		t.Fatalf("write failure: err=%v analysis=%s", err, jsjson.Stringify(analysis))
	}
}

func TestPlainServeOutputRefusalBeforeListening(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)
	f := installPlainServeFake(t)
	outside := filepath.Join(work, "elsewhere")
	stdout, stderr, code := run("serve", "--output", outside)
	if code != 1 || stdout != "" || f.starts != 0 ||
		!strings.HasPrefix(stderr, "FaultLine error: Proof bundle output must be a child directory of ") {
		t.Fatalf("exit=%d stdout=%q stderr=%q starts=%d", code, stdout, stderr, f.starts)
	}
	if _, err := os.Lstat(outside); err == nil {
		t.Error("refused output was created")
	}
	// An existing directory that is not a verified FaultLine bundle is refused.
	bad := filepath.Join(work, ".faultline", "bundles", "bad")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code = run("serve", "--output", bad)
	if code != 1 || stdout != "" || f.starts != 0 ||
		!strings.HasPrefix(stderr, "FaultLine error: Proof bundle output can only replace a verified FaultLine bundle: ") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestPlainServePortHandling(t *testing.T) {
	t.Chdir(t.TempDir())
	// Zero and explicit ports reach the starter as coerced numbers.
	f := installPlainServeFake(t)
	for arg, want := range map[string]float64{"0": 0, "4321": 4321} {
		if _, _, code := run("serve", "--port", arg); code != 0 || f.port != want {
			t.Fatalf("--port %s: exit=%d port=%v", arg, code, f.port)
		}
	}
	// Real listener behaviour: invalid and occupied ports fail with no output.
	plainServeStartServer = startPlainServer
	for _, port := range []string{"abc", "70000", "1.5", "-1"} {
		stdout, stderr, code := run("serve", "--port", port)
		if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "FaultLine error: options.port should be >= 0 and < 65536.") {
			t.Errorf("--port %s: exit=%d stdout=%q stderr=%q", port, code, stdout, stderr)
		}
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	taken := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	stdout, stderr, code := run("serve", "--port", taken)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "address already in use") {
		t.Errorf("occupied: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	// Port zero on the real server binds an ephemeral port and prints it.
	stdout, stderr, code = run("serve", "--port", "0")
	if code != 0 || stderr != "" || !strings.HasPrefix(stdout, "FaultLine incident page: http://127.0.0.1:") ||
		!strings.HasSuffix(stdout, "\nPress Ctrl+C to stop.\n") || strings.Contains(stdout, ":0\n") {
		t.Errorf("zero port: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
