//go:build unix

package nodeproc

import (
	"bytes"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/jsexc"
)

// Yield lets the child and the dispatcher goroutine run.
func Yield() { time.Sleep(time.Millisecond) }

// lockedBuffer is a bytes.Buffer the dispatcher goroutine writes while the
// test goroutine reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Write(p)
}

func (b *lockedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buf.Bytes())
}

func (b *lockedBuffer) String() string { return string(b.Bytes()) }

func TestSpawnStreamsAndKills(t *testing.T) {
	var out lockedBuffer
	c := Spawn("/bin/sh", []string{"-c", "cat; printf done; exec sleep 30"}, SpawnOptions{
		StdinPipe: true, Stdin: []byte("in-"), Stdout: StdioPipe, Stderr: StdioPipe,
		OnStdout: func(b []byte) { out.Write(b) },
	})
	for !bytes.Contains(out.Bytes(), []byte("done")) {
		Yield()
	}
	if !c.Kill("SIGKILL") {
		t.Fatal("Kill returned false on a live child")
	}
	exit := c.Wait()
	if exit.Code != nil || exit.Signal != "SIGKILL" || out.String() != "in-done" {
		t.Fatalf("%+v %q", exit, out.String())
	}
	if c.Kill("SIGKILL") {
		t.Fatal("Kill after exit must return false")
	}
}

func TestSpawnMissingExecutable(t *testing.T) {
	exit := Spawn("faultline-no-such-binary", nil, SpawnOptions{}).Wait()
	if exit.Code != nil || exit.Err == nil || exit.Err.Error() != "spawn faultline-no-such-binary ENOENT" {
		t.Fatalf("%+v", exit)
	}
}

func TestSpawnExitCodeAndStreams(t *testing.T) {
	var out, errOut lockedBuffer
	exit := Spawn("/bin/sh", []string{"-c", `printf "$FL_X"; printf e >&2; exit 3`}, SpawnOptions{
		Env: []string{"FL_X=x"}, Stdout: StdioPipe, Stderr: StdioPipe,
		OnStdout: func(b []byte) { out.Write(b) }, OnStderr: func(b []byte) { errOut.Write(b) },
	}).Wait()
	if exit.Code == nil || *exit.Code != 3 || exit.Signal != "" || exit.Err != nil || out.String() != "x" || errOut.String() != "e" {
		t.Fatalf("%+v %q %q", exit, out.String(), errOut.String())
	}
}

func TestSpawnStdinEPIPESwallowed(t *testing.T) {
	exit := Spawn("/bin/sh", []string{"-c", "exec 0<&-; sleep 0.1"}, SpawnOptions{
		StdinPipe: true, Stdin: bytes.Repeat([]byte("a"), 1<<20),
	}).Wait()
	if exit.Code == nil || *exit.Code != 0 || exit.Err != nil {
		t.Fatalf("%+v", exit)
	}
}

func TestSpawnMissingCwd(t *testing.T) {
	exit := Spawn("/bin/sh", []string{"-c", "true"}, SpawnOptions{Cwd: "/faultline-no-such-dir"}).Wait()
	if exit.Code != nil || exit.Err == nil || exit.Err.Error() != "spawn /bin/sh ENOENT" {
		t.Fatalf("%+v", exit)
	}
}

func TestSpawnThrowsSynchronously(t *testing.T) {
	for _, tc := range []struct {
		file string
		args []string
		o    SpawnOptions
		want string
	}{
		{"/bin/sh", []string{"a\x00b"}, SpawnOptions{}, `The argument 'args[0]' must be a string without null bytes. Received 'a\x00b'`},
		{"/bin/s\x00h", nil, SpawnOptions{}, `The argument 'file' must be a string without null bytes. Received '/bin/s\x00h'`},
		{"", nil, SpawnOptions{}, `The argument 'file' cannot be empty. Received ''`},
		{"/bin/sh", nil, SpawnOptions{Env: []string{"A=x\x00y"}}, `The property 'options.env['A']' must be a string without null bytes. Received 'x\x00y'`},
		{"/bin/sh", nil, SpawnOptions{Cwd: "/tm\x00p"}, `The property 'options.cwd' must be a string, Uint8Array, or URL without null bytes. Received '/tm\x00p'`},
		{"/bin/sh", []string{"-c", "true"}, SpawnOptions{Cwd: "/bin/sh"}, "spawn ENOTDIR"},
	} {
		got := ""
		func() {
			defer jsexc.Catch(func(err error) { got = err.Error() })
			Spawn(tc.file, tc.args, tc.o)
		}()
		if got != tc.want {
			t.Errorf("Spawn(%q, %q): got %q, want %q", tc.file, tc.args, got, tc.want)
		}
	}
}

func TestInspectString(t *testing.T) {
	for in, want := range map[string]string{
		"a\x00b":         `'a\x00b'`,
		"it's":           `"it's"`,
		`it's "x"`:       "`it's \"x\"`",
		"it's \"x\" `y`": `'it\'s "x" ` + "`y`'",
		"\b\t\n\v\f\r\\": `'\b\t\n\x0B\f\r\\'`,
		"\u0085\x7f":     `'\x85\x7F'`,
		"\xed\xa0\x80":   `'\ud800'`,
	} {
		if got := inspectString(in); got != want {
			t.Errorf("inspectString(%q) = %s, want %s", in, got, want)
		}
	}
}

// Data a child writes to stderr before stdout reaches the listeners in that
// order, as libuv's loop delivers it: Node's docker runner splits its
// output budget between the streams in arrival order (round 7 #7, where
// two reader goroutines let a stdout chunk overtake the last stderr one).
func TestSpawnDeliversStreamsInArrivalOrder(t *testing.T) {
	for run := 0; run < 30; run++ {
		var mu sync.Mutex
		var order []string
		var errBytes int
		record := func(name string) func([]byte) {
			return func(b []byte) {
				mu.Lock()
				defer mu.Unlock()
				if name == "stderr" {
					errBytes += len(b)
				}
				order = append(order, name)
			}
		}
		exit := Spawn("/bin/sh", []string{"-c", "head -c 1048482 /dev/zero >&2; printf 'line\\n'"}, SpawnOptions{
			Stdout: StdioPipe, Stderr: StdioPipe, OnStdout: record("stdout"), OnStderr: record("stderr"),
		}).Wait()
		if exit.Code == nil || *exit.Code != 0 {
			t.Fatalf("%+v", exit)
		}
		mu.Lock()
		if errBytes != 1048482 || len(order) == 0 || order[len(order)-1] != "stdout" {
			last := order[max(0, len(order)-3):]
			mu.Unlock()
			t.Fatalf("run %d: stderr %d bytes, last chunks %v", run, errBytes, last)
		}
		mu.Unlock()
	}
}

// A signal Node's signo_string cannot name (a real-time signal; SIGEMT on
// macOS) gives no signal to 'exit', so the child exits with code 0, as
// Node 22 reports it on Linux and macOS.
func TestSpawnUnnamedSignalExitsZero(t *testing.T) {
	sig := "40"
	if runtime.GOOS != "linux" {
		sig = "7"
	}
	exit := Spawn("/bin/sh", []string{"-c", "kill -" + sig + " $$"}, SpawnOptions{}).Wait()
	if exit.Code == nil || *exit.Code != 0 || exit.Signal != "" {
		t.Fatalf("%+v", exit)
	}
	exit = Spawn("/bin/sh", []string{"-c", "kill -TERM $$"}, SpawnOptions{}).Wait()
	if exit.Code != nil || exit.Signal != "SIGTERM" {
		t.Fatalf("SIGTERM: %+v", exit)
	}
}
