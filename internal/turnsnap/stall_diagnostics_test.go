package turnsnap

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStallDiagnosticCancellationBeforeDeadline(t *testing.T) {
	var calls atomic.Int32
	stop := startStallCapture(time.Now().Add(time.Hour), func(context.Context) { calls.Add(1) })
	stop()
	if calls.Load() != 0 {
		t.Fatal("cancelled capture ran before its deadline")
	}
}

func TestStallDiagnosticCancellationWaitsForCollector(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	stop := startStallCapture(time.Now().Add(-time.Second), func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(cancelled)
	})
	t.Cleanup(stop)
	<-started
	stop()
	select {
	case <-cancelled:
	default:
		t.Fatal("stop returned without cancelling and joining the collector")
	}
}

func TestStallDiagnosticCaptureRunsOnce(t *testing.T) {
	var calls atomic.Int32
	finished := make(chan struct{})
	stop := startStallCapture(time.Now().Add(-time.Second), func(ctx context.Context) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("collector has no command deadline")
		}
		calls.Add(1)
		close(finished)
	})
	t.Cleanup(stop)
	<-finished
	stop()
	if calls.Load() != 1 {
		t.Fatalf("captures: got %d, want exactly one", calls.Load())
	}
}

func TestStallDiagnosticCommandOutputIsBounded(t *testing.T) {
	out := stallCommandOutput{limit: 5}
	for _, text := range []string{"abc", "defg", "more"} {
		if n, err := out.Write([]byte(text)); n != len(text) || err != nil {
			t.Fatalf("diagnostic output must drain: n=%d err=%v", n, err)
		}
	}
	if out.String() != "abcde" || !out.truncated {
		t.Fatalf("bounded evidence: text=%q truncated=%t", out.String(), out.truncated)
	}
}

func TestStallDiagnosticOwnedProcessTree(t *testing.T) {
	raw := "99 1 turnsnap.test\n100 99 git\n101 100 git-helper\n403 1 unrelated\ninvalid row\n"
	pids, lines := stallOwnedProcesses(raw, 99)
	if !slices.Equal(pids, []int{99, 100, 101}) {
		t.Fatalf("owned pids: got %v", pids)
	}
	if strings.Contains(lines, "unrelated") || strings.Contains(lines, "invalid") || !strings.Contains(lines, "101 100 git-helper") {
		t.Fatalf("owned process evidence: %q", lines)
	}
}

func TestStallDiagnosticCapturesSocketProcessStates(t *testing.T) {
	if runtime.GOOS != "darwin" {
		return
	}
	// Darwin's sockaddr_un path limit is shorter than some t.TempDir paths.
	dir, err := os.MkdirTemp("/tmp", "fl-stall-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(dir) })
	listener, err := net.Listen("unix", filepath.Join(dir, "peer.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	peer, err := net.Dial("unix", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	evidence := collectTurnsnapStall(ctx, os.Getpid())
	rows, _ := evidence["relatedSocketProcesses"].(string)
	if !strings.Contains(rows, strconv.Itoa(os.Getpid())) {
		t.Fatalf("socket-owner process state missing: %v", evidence)
	}
}

func TestStallDiagnosticReparentedSocketProcessState(t *testing.T) {
	processes := "99 1 S turnsnap.test\n403 1 S holder\n500 1 R unrelated\ninvalid row\n"
	sockets := "turnsnap 99 user 8u unix 0xaaa 0t0 ->0xbbb\n" +
		"holder 403 user 1u unix 0xbbb 0t0 ->0xaaa\n"
	if got, want := stallSocketProcesses(processes, sockets), "99 1 S turnsnap.test\n403 1 S holder"; got != want {
		t.Fatalf("socket-owner process states: got %q, want %q", got, want)
	}
	if got := stallSocketProcesses(processes, "holder 403 user 1u unix 0xbbb 0t0 ->"); got != "" {
		t.Fatalf("truncated socket row selected process evidence: %q", got)
	}
}

func TestStallDiagnosticMatchesReciprocalSocketOwners(t *testing.T) {
	raw := "COMMAND PID USER FD TYPE DEVICE SIZE/OFF NODE NAME\n" +
		"turnsnap 99 user 8u unix 0xaaa 0t0 ->0xbbb\n" +
		"holder 403 user 1u unix 0xbbb 0t0 ->0xaaa\n" +
		"unrelated 500 user 1u unix 0xccc 0t0 ->0xddd\n" +
		"misleading 501 user 1u unix 0xeee 0t0 /tmp/0xaaa\n"
	matched := stallRelatedSockets(raw, 99)
	if !strings.Contains(matched, "holder 403") || !strings.Contains(matched, "turnsnap 99") {
		t.Fatalf("lost reciprocal peer ownership: %q", matched)
	}
	if strings.Contains(matched, "unrelated") || strings.Contains(matched, "misleading") {
		t.Fatalf("included unrelated socket evidence: %q", matched)
	}
	if got := stallRelatedSockets(raw, 999); got != "" {
		t.Fatalf("missing root must not dump unrelated sockets: %q", got)
	}
}
