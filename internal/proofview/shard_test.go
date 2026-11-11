package proofview

import (
	"bytes"
	"context"
	"flag"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The attachment view tests are dominated by waiting on the real git child
// processes the verifiers spawn (about seven per load). They cannot run as
// parallel subtests inside one process: the production stack-offset state in
// internal/jsjson (siteOffset, siteCosts) is process-global, and the race
// detector reports it. Each test binary process has its own copy of that
// state, so the cases are sharded across child processes of this same test
// binary instead. Every case still runs the unchanged body, with the same
// assertions, in a process of its own; nothing is skipped or weakened.
const shardCount = 6

const shardChildEnv = "FAULTLINE_PROOFVIEW_SHARD_CHILD"

// runShardedChildren runs the subtests ids of the calling top-level test in
// shards child processes and reports whether it did. It returns false, and the
// caller then runs its own body inline, when this process is itself a shard
// child or when the caller asked for specific subtests with -run (a focused
// run keeps the ordinary single-process behaviour).
func runShardedChildren(t *testing.T, ids []string, shards int) bool {
	t.Helper()
	if os.Getenv(shardChildEnv) != "" {
		return false
	}
	if f := flag.Lookup("test.run"); f != nil && strings.Contains(f.Value.String(), "/") {
		return false
	}
	if shards > len(ids) {
		shards = len(ids)
	}
	buckets := make([][]string, shards)
	for i, id := range ids {
		buckets[i%shards] = append(buckets[i%shards], id)
	}
	ctx := context.Background()
	if deadline, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}
	type outcome struct {
		out []byte
		err error
	}
	results := make([]outcome, shards)
	var wg sync.WaitGroup
	for i, bucket := range buckets {
		quoted := make([]string, len(bucket))
		for j, id := range bucket {
			quoted[j] = regexp.QuoteMeta(id)
		}
		args := []string{
			"-test.run=^" + regexp.QuoteMeta(t.Name()) + "$/^(" + strings.Join(quoted, "|") + ")$",
			"-test.v", "-test.count=1",
		}
		if deadline, ok := t.Deadline(); ok {
			args = append(args, "-test.timeout="+strconv.FormatInt(int64(time.Until(deadline)/time.Millisecond)+1, 10)+"ms")
		}
		wg.Add(1)
		go func(i int, args []string) {
			defer wg.Done()
			cmd := exec.CommandContext(ctx, os.Args[0], args...)
			cmd.Env = append(os.Environ(), shardChildEnv+"=1")
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			err := cmd.Run()
			results[i] = outcome{out: out.Bytes(), err: err}
		}(i, args)
	}
	wg.Wait()
	for i, r := range results {
		if r.err != nil {
			t.Errorf("shard %d (%v) failed: %v\n%s", i, buckets[i], r.err, r.out)
			continue
		}
		// A child that matched nothing exits 0; every case must be seen passing.
		for _, id := range buckets[i] {
			if !bytes.Contains(r.out, []byte("--- PASS: "+t.Name()+"/"+id+" (")) {
				t.Errorf("shard %d: case %s did not report PASS\n%s", i, id, r.out)
			}
		}
	}
	return true
}
