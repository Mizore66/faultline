package nodeproc

import (
	"bytes"
	"os"
	"sync"
	"testing"
)

// TestMain lets the test binary act as the child: with
// FAULTLINE_PUMP_CHILD set it writes 1,048,482 bytes to stderr and then a
// line to stdout, and exits.
func TestMain(m *testing.M) {
	if os.Getenv("FAULTLINE_PUMP_CHILD") == "1" {
		os.Stderr.Write(bytes.Repeat([]byte{'e'}, 1048482))
		os.Stdout.Write([]byte("line\n"))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// Data the child writes to stderr before stdout reaches the listeners in
// that order, as libuv's completion-port loop delivers it on Windows
// (round 7 #7). Runs only on Windows (CI's windows jobs).
func TestSpawnDeliversStreamsInArrivalOrderWindows(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for run := 0; run < 20; run++ {
		var mu sync.Mutex
		var order []string
		var errBytes, outBytes int
		record := func(name string) func([]byte) {
			return func(b []byte) {
				mu.Lock()
				defer mu.Unlock()
				if name == "stderr" {
					errBytes += len(b)
				} else {
					outBytes += len(b)
				}
				order = append(order, name)
			}
		}
		exit := Spawn(exe, nil, SpawnOptions{
			Env:    append(os.Environ(), "FAULTLINE_PUMP_CHILD=1"),
			Stdout: StdioPipe, Stderr: StdioPipe, OnStdout: record("stdout"), OnStderr: record("stderr"),
		}).Wait()
		if exit.Code == nil || *exit.Code != 0 {
			t.Fatalf("%+v", exit)
		}
		mu.Lock()
		if errBytes != 1048482 || outBytes != 5 || order[len(order)-1] != "stdout" {
			last := order[max(0, len(order)-3):]
			mu.Unlock()
			t.Fatalf("run %d: stderr %d, stdout %d bytes, last chunks %v", run, errBytes, outBytes, last)
		}
		mu.Unlock()
	}
}

// A child that writes little and exits ends both streams (EOF through the
// broken pipe).
func TestSpawnPipesEndWindows(t *testing.T) {
	exe, _ := os.Executable()
	exit := Spawn(exe, []string{"-test.run=^$"}, SpawnOptions{Stdout: StdioPipe, Stderr: StdioPipe,
		OnStdout: func([]byte) {}, OnStderr: func([]byte) {}}).Wait()
	if exit.Code == nil || *exit.Code != 0 {
		t.Fatalf("%+v", exit)
	}
}
