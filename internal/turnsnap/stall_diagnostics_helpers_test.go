package turnsnap

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Near-deadline evidence only: never interrupt the test or change its timeout.
func recordTurnsnapStall(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		return
	}
	deadline, ok := t.Deadline()
	if !ok {
		return
	}
	t.Cleanup(startStallCapture(deadline.Add(-20*time.Second), func(ctx context.Context) {
		evidence := collectTurnsnapStall(ctx, os.Getpid())
		evidence["test"] = t.Name()
		evidence["deadline"] = deadline.UTC().Format(time.RFC3339Nano)
		evidence["observedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
		evidence["platform"] = runtime.GOOS + "/" + runtime.GOARCH
		evidence["kind"] = "near-deadline observation, not a cause or failure verdict"
		if raw, err := json.Marshal(evidence); err == nil && ctx.Err() != context.Canceled {
			fmt.Fprintf(os.Stderr, "FL-DIAG-TURNSNAP-STALL %s\n", raw)
		}
	}))
}

func startStallCapture(at time.Time, collect func(context.Context)) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	timer := time.NewTimer(time.Until(at))
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if ctx.Err() != nil {
			return
		}
		capture, finish := context.WithTimeout(ctx, 12*time.Second)
		defer finish()
		collect(capture)
	}()
	return func() {
		cancel()
		timer.Stop()
		<-done
	}
}

func collectTurnsnapStall(ctx context.Context, root int) map[string]any {
	errors := map[string]string{}
	run := func(name string, args ...string) string {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.WaitDelay = time.Second
		out := stallCommandOutput{limit: 1 << 20}
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			errors[name] = err.Error()
		}
		if out.truncated {
			errors[name] += "; output exceeded 1 MiB; snapshot incomplete"
		}
		return out.String()
	}
	// Do not collect argv, environment, file contents or unrelated process rows.
	processSnapshot := run("/bin/ps", "-axo", "pid=,ppid=,stat=,comm=")
	pids, processes := stallOwnedProcesses(processSnapshot, root)
	// The peer holder may have been reparented. Inspect unix sockets, retaining
	// only rows sharing an endpoint with this test process's own sockets.
	sockets := stallRelatedSockets(run("/usr/sbin/lsof", "-nP", "-U"), root)
	return map[string]any{"rootPid": root, "ownedPids": pids, "ownedProcesses": processes,
		"relatedUnixSockets": sockets, "relatedSocketProcesses": stallSocketProcesses(processSnapshot, sockets),
		"commandErrors": errors,
		"limits": "snapshot only; permissions, process exits and unknown lsof formats may omit holders; " +
			"process and socket snapshots are not atomic; shutdown outcome and process age are not observed; " +
			"sockets may belong to other parallel tests; collector forks can perturb descriptor ownership; " +
			"a blocked fork or ForkLock can prevent capture and cleanup despite the command context; " +
			"a missing record does not establish absence of a stall or holder"}
}

type stallCommandOutput struct {
	strings.Builder
	limit     int
	truncated bool
}

func (out *stallCommandOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := out.limit - out.Len(); n > remaining {
		p = p[:remaining]
		out.truncated = true
	}
	out.Builder.Write(p)
	return n, nil
}

func stallOwnedProcesses(raw string, root int) ([]int, string) {
	parents := map[int][]int{}
	rows := map[int]string{}
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, errPID := strconv.Atoi(fields[0])
		ppid, errParent := strconv.Atoi(fields[1])
		if errPID != nil || errParent != nil || pid <= 0 || ppid < 0 {
			continue
		}
		parents[ppid] = append(parents[ppid], pid)
		rows[pid] = line
	}
	queue := []int{root}
	seen := map[int]bool{}
	var pids []int
	for len(queue) != 0 {
		pid := queue[0]
		queue = queue[1:]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		if _, ok := rows[pid]; ok {
			pids = append(pids, pid)
		}
		queue = append(queue, parents[pid]...)
	}
	slices.Sort(pids)
	var selected []string
	for _, pid := range pids {
		selected = append(selected, rows[pid])
	}
	return pids, strings.Join(selected, "\n")
}

var stallSocketRow = regexp.MustCompile(`^\S+\s+([0-9]+)\s+\S+\s+[0-9]+[a-z]+\s+unix\s+(0x[0-9a-fA-F]+)\s+.*?->(0x[0-9a-fA-F]+)(?:\s|$)`)

func stallSocketProcesses(processes, sockets string) string {
	owners := map[string]bool{}
	for _, line := range strings.Split(sockets, "\n") {
		if match := stallSocketRow.FindStringSubmatch(line); match != nil {
			owners[match[1]] = true
		}
	}
	var selected []string
	for _, line := range strings.Split(processes, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && owners[fields[0]] {
			selected = append(selected, line)
		}
	}
	return strings.Join(selected, "\n")
}

func stallRelatedSockets(raw string, root int) string {
	type socketRow struct {
		line, own, peer string
		pid             int
	}
	var rows []socketRow
	owned := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		m := stallSocketRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		pid, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		row := socketRow{line, strings.ToLower(m[2]), strings.ToLower(m[3]), pid}
		rows = append(rows, row)
		if pid == root {
			owned[row.own], owned[row.peer] = true, true
		}
	}
	var selected []string
	for _, row := range rows {
		if owned[row.own] || owned[row.peer] {
			selected = append(selected, row.line)
		}
	}
	return strings.Join(selected, "\n")
}
