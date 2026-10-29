//go:build faultline_test

package difftest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/gen"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/ledger"
	"github.com/Mizore66/faultline/internal/schema"
	"github.com/Mizore66/faultline/internal/sidecar"
)

// sidecarGatedFiles are the files TestLiveSidecarHooks gates (they are in
// coverage_test.go's liveGatedFiles; their unreachable literals are in its
// unreachableLiterals).
var sidecarGatedFiles = []string{"internal/sidecar/hook.go", "internal/sidecar/receipt.go", "internal/sidecar/lock.go", "internal/sidecar/inspect.go"}

// sidecarOutcomeJSON is oracle-sidecar.ts's sidecarOutcome: {result, stderr}
// or {threw, zod, stderr}.
func sidecarOutcomeJSON(result jsjson.Value, err error, stderr string) string {
	o := jsjson.NewObj()
	if err != nil {
		var zod *schema.Error
		o.Set("threw", jsjson.MakeString(jsexc.Message(err)))
		o.Set("zod", jsjson.MakeBool(errors.As(err, &zod)))
	} else {
		o.Set("result", result)
	}
	o.Set("stderr", jsjson.MakeString(stderr))
	return jsjson.Stringify(jsjson.MakeObject(o))
}

// sidecarGit runs the test's own git with fixed identity and dates.
func sidecarGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := captureGitCmd(dir, args...).CombinedOutput(); err != nil {
		t.Errorf("git %q in %s: %v\n%s", args, dir, err, out)
	}
}

func sidecarWrite(path, content string) {
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte(content), 0o644)
}

// sidecarKinds are the repository shapes a sequence runs in.
var sidecarKinds = []string{"clean", "dirty", "no-head", "worktree", "subdir", "faultline-symlink", "faultline-file", "clean"}

// buildSidecarRepo builds work/repo (and work/wt) deterministically at the
// work path, so the TS and Go sides each build their own at the same path.
// It returns the hook cwd.
func buildSidecarRepo(t *testing.T, work, kind string) string {
	t.Helper()
	repo := filepath.Join(work, "repo")
	os.MkdirAll(repo, 0o755)
	sidecarGit(t, repo, "init", "-q")
	sidecarWrite(filepath.Join(repo, "tracked.txt"), "clean\n")
	sidecarWrite(filepath.Join(repo, "sub", "file.txt"), "sub\n")
	if kind != "no-head" {
		sidecarGit(t, repo, "add", "-A")
		sidecarGit(t, repo, "commit", "-q", "-m", "fixture")
	}
	cwd := repo
	switch kind {
	case "dirty":
		sidecarWrite(filepath.Join(repo, "tracked.txt"), "changed\n")
		sidecarWrite(filepath.Join(repo, "untracked.txt"), "new\n")
	case "worktree":
		cwd = filepath.Join(work, "wt")
		sidecarGit(t, repo, "worktree", "add", "-q", "-b", "wt-branch", cwd)
	case "subdir":
		cwd = filepath.Join(repo, "sub")
	case "faultline-symlink":
		os.MkdirAll(filepath.Join(work, "outside"), 0o755)
		os.Symlink(filepath.Join(work, "outside"), filepath.Join(repo, ".git", "faultline"))
	case "faultline-file":
		os.WriteFile(filepath.Join(repo, ".git", "faultline"), []byte("not a directory"), 0o644)
	}
	return cwd
}

const (
	sidecarFreshFuture = seededEpochMillis + 1_000_000_000 // a lock "from the future": never stale, its age is 0
	sidecarStaleAge    = 3_600_000                         // a lock an hour before the seeded clock's epoch
)

// applyHookOp applies one HookOp to work before a step. recordings is
// work/repo/.git/faultline/recordings (every kind shares it). It reports
// the path of a planted fresh lock or storage symlink, which the caller
// undoes after the step so later steps do not wait for it too.
func applyHookOp(t *testing.T, work, cwd, session string, seq, step int, op gen.HookOp, pid int) (cleanup string) {
	t.Helper()
	repo := filepath.Join(work, "repo")
	recordings := filepath.Join(repo, ".git", "faultline", "recordings")
	ledgerPath := filepath.Join(recordings, "codex-"+canonical.SHA256Hex(session)+".json")
	receiptPath := func(turn string) string {
		return filepath.Join(recordings, "codex-stop-"+canonical.SHA256Hex(session+"\x00"+turn)+".json")
	}
	haveRecordings := false
	if info, err := os.Lstat(recordings); err == nil && info.IsDir() {
		haveRecordings = true
	}
	root := repo
	if strings.HasSuffix(cwd, string(filepath.Separator)+"wt") {
		root = cwd
	}
	switch op.Kind {
	case "":
	case "dirty":
		sidecarWrite(filepath.Join(root, filepath.FromSlash(op.Arg)), "dirty "+strconv.Itoa(step)+"\n")
	case "clean":
		captureGitCmd(root, "reset", "--hard", "-q").Run()
		captureGitCmd(root, "clean", "-fdq").Run()
	case "commit":
		sidecarWrite(filepath.Join(root, "commit-"+strconv.Itoa(step)+".txt"), "commit\n")
		captureGitCmd(root, "add", "-A").Run()
		captureGitCmd(root, "commit", "-q", "-m", "step "+strconv.Itoa(step)).Run()
	case "plant-fresh-lock", "plant-stale-lock":
		if !haveRecordings {
			return ""
		}
		lock := ledgerPath + ".sidecar.lock"
		os.RemoveAll(lock)
		os.WriteFile(lock, []byte("planted\n"), 0o600)
		mtime := time.UnixMilli(seededEpochMillis - sidecarStaleAge)
		if op.Kind == "plant-fresh-lock" {
			mtime = time.UnixMilli(sidecarFreshFuture)
			cleanup = lock
		}
		os.Chtimes(lock, mtime, mtime)
	case "plant-receipt":
		if !haveRecordings {
			return ""
		}
		var body string
		switch seq % 3 {
		case 0: // schema-valid, with a checkpoint digest the ledger does not have
			body = `{"schemaVersion":"faultline.codex-sidecar-stop-receipt.v1","sessionId":` + jsjson.Quote(session) + `,"turnId":` + jsjson.Quote(op.Arg) +
				`,"status":"CHECKPOINT_RECORDED","checkpointDigest":"sha256:` + strings.Repeat("f", 64) + `"}`
		case 1: // a skipped receipt that contradicts a recorded checkpoint, or has no completed turn
			body = `{"schemaVersion":"faultline.codex-sidecar-stop-receipt.v1","sessionId":` + jsjson.Quote(session) + `,"turnId":` + jsjson.Quote(op.Arg) +
				`,"status":"CHECKPOINT_SKIPPED_DIRTY","reason":"DIRTY_WORKTREE"}`
		default: // not a receipt
			body = "{"
		}
		os.WriteFile(receiptPath(op.Arg), []byte(body+"\n"), 0o644)
	case "plant-stage":
		if !haveRecordings {
			return ""
		}
		receipt := receiptPath(op.Arg)
		sum := sha256.Sum256([]byte(receipt))
		stage := filepath.Join(recordings, "."+hex.EncodeToString(sum[:])[:24]+"."+strconv.Itoa(pid)+".tmp")
		os.WriteFile(stage, []byte("leftover\n"), 0o600)
	case "symlink-storage":
		if !haveRecordings {
			return ""
		}
		// The symlink stands for this one step; sidecarRestore puts the
		// directory back so later steps still reach the state machine.
		moved := recordings + ".moved"
		os.RemoveAll(moved)
		os.Rename(recordings, moved)
		os.Symlink("recordings.moved", recordings)
		cleanup = recordings
	case "remove-receipt":
		os.Remove(receiptPath(op.Arg))
	case "truncate-ledger":
		if n, err := strconv.Atoi(op.Arg); err == nil {
			if b, err := os.ReadFile(ledgerPath); err == nil {
				os.WriteFile(ledgerPath, b[:min(n, len(b))], 0o644)
			}
		}
	case "x:rebuild":
		rebuildSidecarLedger(t, ledgerPath, session, strings.Split(op.Arg, ","))
	case "x:receipt":
		turn, edit, _ := strings.Cut(op.Arg, "|")
		editSidecarReceipt(t, receiptPath(turn), session, turn, edit)
	case "x:receipt-symlink":
		turn := op.Arg
		real := receiptPath(turn) + ".real"
		os.Rename(receiptPath(turn), real)
		os.Symlink(filepath.Base(real), receiptPath(turn))
	case "x:ledger-symlink":
		real := ledgerPath + ".moved"
		os.Rename(ledgerPath, real)
		os.Symlink(filepath.Base(real), ledgerPath)
		cleanup = ledgerPath
	case "x:ro-lock":
		// A stale lock in a read-only recordings directory: open(O_EXCL)
		// reports EEXIST, then the unlink is refused (EACCES, any non-root user).
		lock := ledgerPath + ".sidecar.lock"
		os.WriteFile(lock, []byte("stale\n"), 0o600)
		mtime := time.UnixMilli(seededEpochMillis - sidecarStaleAge)
		os.Chtimes(lock, mtime, mtime)
		os.Chmod(recordings, 0o555)
		// A failing step must not leave the directory 0555 for the
		// temporary-directory removal (sidecarRestore does the same after
		// a step that completes).
		t.Cleanup(func() { os.Chmod(recordings, 0o700) })
		cleanup = "ro:" + recordings
	case "x:lock-dir":
		os.Mkdir(ledgerPath+".sidecar.lock", 0o700)
		cleanup = ledgerPath + ".sidecar.lock"
	default:
		t.Errorf("unknown hook op %q", op.Kind)
	}
	return cleanup
}

// sidecarRestore undoes a step's planted fresh lock, storage symlink,
// symlinked ledger or read-only recordings directory.
func sidecarRestore(target string) {
	if target == "" {
		return
	}
	if dir, ok := strings.CutPrefix(target, "ro:"); ok { // the planted read-only recordings directory
		os.Chmod(dir, 0o700)
		matches, _ := filepath.Glob(filepath.Join(dir, "*.sidecar.lock"))
		for _, m := range matches {
			os.Remove(m)
		}
		return
	}
	if info, err := os.Lstat(target); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		os.Remove(target)
		os.Rename(target+".moved", target)
		return
	}
	os.Remove(target)
}

// sidecarManifest lists every entry under dir like workManifest, with the
// content of every Git index left out (they keep each copy's inode stat data).
func sidecarManifest(dir string) string {
	var lines []string
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			lines = append(lines, "ERR "+err.Error())
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		info, err := d.Info()
		if err != nil {
			lines = append(lines, strconv.Quote(rel)+" ERR "+err.Error())
			return nil
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, _ := os.Readlink(path)
			lines = append(lines, strconv.Quote(rel)+" L "+target)
		case d.IsDir():
			lines = append(lines, strconv.Quote(rel)+" D "+info.Mode().Perm().String())
		default:
			b, _ := os.ReadFile(path)
			sum := sha256.Sum256(b)
			if filepath.Base(rel) == "index" && strings.Contains(rel, ".git") {
				sum = [32]byte{}
			}
			lines = append(lines, strconv.Quote(rel)+" F "+info.Mode().Perm().String()+" "+hex.EncodeToString(sum[:]))
		}
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// maskSidecarPID replaces a side's own process id in a receipt stage name
// (".<24 lowercase hex>.<pid>.tmp", the pid being its own), which differs
// between the two sides. Nothing else is touched: not another ".<pid>.tmp",
// not another pid, not a stage key that is not 24 lowercase hex digits.
func maskSidecarPID(s string, pid int) string {
	re := regexp.MustCompile(`(\.[a-f0-9]{24}\.)` + strconv.Itoa(pid) + `\.tmp`)
	return re.ReplaceAllString(s, "${1}<PID>.tmp")
}

type sidecarStep struct{ result, inspect, manifest string }

type sidecarSequence struct {
	kind, session string
	trackedOnly   bool
	steps         []gen.HookStep
}

// TestLiveSidecarHooks runs src/codex-sidecar.ts and internal/sidecar on
// the same hook sequences under one seed, TS first and then Go on each
// repository (so both read the same clock ticks and UUIDs). After every
// step it compares the result or error, the stderr, the whole work tree
// (the ledger bytes, receipts, lock, modes) and inspect; then 100
// multi-session stores for resolveLatestSidecarLedgerPath.
//
// FAULTLINE_SIDECAR_SEQS=first:end compares only sequences [first, end)
// (all are still generated up to end), for local runs in pieces; CI runs
// all 400. A planted fresh lock costs about 17 s per step and side, so at
// most 5 sequences plant one.
func TestLiveSidecarHooks(t *testing.T) {
	oracle.Skip(t)
	gitDir, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Fatal(err)
	}
	path := strings.TrimSpace(string(gitDir)) + ":/usr/bin:/bin"
	const random, randomStores = 400, 100
	const allStores = randomStores + 3 // plus three stores of empty valid ledgers
	directed := directedSequences()
	total := random + len(directed)
	n, first, stores, storesFrom := total, 0, allStores, 0
	if window := os.Getenv("FAULTLINE_SIDECAR_SEQS"); window != "" {
		a, b, ok := strings.Cut(window, ":")
		var errA, errB error
		first, errA = strconv.Atoi(a)
		n, errB = strconv.Atoi(b)
		if !ok || errA != nil || errB != nil || first < 0 || first > n || n > total || (first == n && n != total) {
			t.Fatalf("FAULTLINE_SIDECAR_SEQS=%q: want first:end with 0 <= first <= end <= %d", window, total)
		}
	}
	// FAULTLINE_SIDECAR_STORES=<k> runs only k of the 100 stores (default 0
	// when a window is set), for local runs in pieces.
	if os.Getenv("FAULTLINE_SIDECAR_SEQS") != "" {
		stores = 0
		if v := os.Getenv("FAULTLINE_SIDECAR_STORES"); v != "" {
			k, err := strconv.Atoi(v)
			if err != nil || k < 0 || k > allStores {
				t.Fatalf("FAULTLINE_SIDECAR_STORES=%q: want 0..%d", v, allStores)
			}
			stores = k
		}
		if v := os.Getenv("FAULTLINE_SIDECAR_STORES_FROM"); v != "" {
			m, err := strconv.Atoi(v)
			if err != nil || m < 0 || m > stores {
				t.Fatalf("FAULTLINE_SIDECAR_STORES_FROM=%q: want 0..%d", v, stores)
			}
			storesFrom = m
		}
	}
	fullRun := first == 0 && n == total && stores == allStores && storesFrom == 0

	r := rand.New(rand.NewPCG(73, 6))
	var seqs []sidecarSequence
	freshSequences := 0
	for i := 0; i < n; i++ {
		if i >= random {
			seqs = append(seqs, sidecarSequence{kind: "clean", session: directedSessionID, steps: directed[i-random]})
			continue
		}
		s := sidecarSequence{kind: sidecarKinds[i%len(sidecarKinds)], session: "sess-" + strconv.Itoa(i), trackedOnly: i%2 == 1}
		s.steps = gen.HookSequence(r, i, s.session)
		fresh := false
		for si := range s.steps {
			if s.steps[si].Op.Kind != "plant-fresh-lock" {
				continue
			}
			if fresh || freshSequences >= 5 {
				s.steps[si].Op = gen.HookOp{}
				continue
			}
			fresh = true
		}
		if fresh {
			freshSequences++
		}
		seqs = append(seqs, s)
	}

	const seed = "live-sidecar-hooks"
	c := oracle.StartSeeded(t, seed)
	defer c.Close()
	clock.ResetForTest(seed)
	oracleCall(t, c, "echo", "null")
	time.Sleep(time.Second)
	tick, err := strconv.ParseInt(oracleCall(t, c, "seededTick", "null"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	for range tick + 1 {
		clock.NowMillis()
	}
	t.Setenv("PATH", path)
	tsPid, err := strconv.Atoi(oracleCall(t, c, "processPid", "null"))
	if err != nil {
		t.Fatal(err)
	}

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	startCwd, _ := os.Getwd()

	// run replays sequence i on one side at root/w<i>. The Go side runs in a
	// worker while the oracle runs the next sequence's TS side: each side's
	// clock sequence stays sequential. Only the worker touches the Go
	// process's environment, working directory and clock.
	run := func(i int, side string) (out []sidecarStep) {
		s := seqs[i]
		work := filepath.Join(root, "w"+strconv.Itoa(i))
		cwd := buildSidecarRepo(t, work, s.kind)
		env := `{}`
		if s.trackedOnly {
			env = `{"FAULTLINE_TURN_SNAPSHOT_TRACKED_ONLY":"1"}`
		}
		if side == "go" {
			if s.trackedOnly {
				os.Setenv("FAULTLINE_TURN_SNAPSHOT_TRACKED_ONLY", "1")
				defer os.Unsetenv("FAULTLINE_TURN_SNAPSHOT_TRACKED_ONLY")
			}
			defer os.Chdir(startCwd)
		}
		pendingRestore := ""
		pid := tsPid
		if side == "go" {
			pid = os.Getpid()
		}
		for si, step := range s.steps {
			fresh := applyHookOp(t, work, cwd, s.session, i, si, step.Op, pid)
			if pendingRestore != "" && fresh == "" {
				fresh, pendingRestore = pendingRestore, ""
			}
			doc := step.Event.JSON(cwd)
			value, perr := jsjson.Parse(string(doc))
			if perr != nil {
				// A malformed document never reaches recordObservedCodexHook:
				// the CLI refuses it first (Task 8). Both sides skip it.
				out = append(out, sidecarStep{result: "unparseable"})
				// An op on a document the CLI refuses stays in place for the
				// next step, which is what it is there for.
				if fresh != "" {
					pendingRestore = fresh
				}
				continue
			}
			var res, ins string
			if side == "ts" {
				res = oracleText(t, c, "recordObservedCodexHook",
					`{"value":`+string(doc)+`,"path":`+jsjson.Quote(path)+`,"env":`+env+`,"chdir":`+jsjson.Quote(work)+`}`)
				ins = oracleText(t, c, "inspectObservedCodexSidecar", `{"cwd":`+jsjson.Quote(cwd)+`,"path":`+jsjson.Quote(path)+`}`)
			} else {
				os.Chdir(work)
				var stderr bytes.Buffer
				result, err := sidecar.RecordObservedHook(value, &stderr)
				var rv jsjson.Value
				if err == nil {
					rv = result.JSON()
				}
				res = sidecarOutcomeJSON(rv, err, jsstr.DecodeUTF8(stderr.Bytes()))
				os.Chdir(startCwd)
				inspection, err := sidecar.Inspect(cwd, "")
				var iv jsjson.Value
				if err == nil {
					iv = inspection.JSON()
				}
				ins = sidecarOutcomeJSON(iv, err, "")
			}
			manifest := maskSidecarPID(sidecarManifest(work), pid)
			res, ins = maskSidecarPID(res, pid), maskSidecarPID(ins, pid)
			sidecarRestore(fresh)
			out = append(out, sidecarStep{res, ins, manifest})
		}
		// The ledger path of the session and of an identifier the schema refuses.
		for _, id := range []string{s.session, "bad id"} {
			var res string
			if side == "ts" {
				res = oracleText(t, c, "codexSidecarLedgerPath", `{"cwd":`+jsjson.Quote(cwd)+`,"sessionId":`+jsjson.Quote(id)+`,"path":`+jsjson.Quote(path)+`}`)
			} else {
				p, err := sidecar.LedgerPath(cwd, id)
				res = sidecarOutcomeJSON(jsjson.MakeString(p), err, "")
			}
			out = append(out, sidecarStep{result: res})
		}
		if err := os.Rename(work, filepath.Join(root, side+"-"+strconv.Itoa(i))); err != nil {
			t.Error(err)
		}
		return out
	}

	total2 := n + stores
	var storePlans []storePlan
	for k := 0; k < allStores; k++ {
		if k < randomStores {
			storePlans = append(storePlans, randomStorePlan(r, k))
			continue
		}
		// One empty valid ledger, two, and one empty with a real one.
		empty := func(n int) storeSession {
			return storeSession{id: "store-" + strconv.Itoa(k) + "-" + strconv.Itoa(n), occurredAt: storeTimes[0], breakage: "empty"}
		}
		real := storeSession{id: "store-" + strconv.Itoa(k) + "-r", occurredAt: storeTimes[1]}
		plan := []storePlan{{[]storeSession{empty(0)}}, {[]storeSession{empty(0), empty(1)}}, {[]storeSession{empty(0), real}}}[k-randomStores]
		storePlans = append(storePlans, plan)
	}
	goResults := make([][]sidecarStep, total2)
	jobs := make(chan int, total2)
	done := make([]chan struct{}, total2)
	for i := range done {
		done[i] = make(chan struct{})
	}
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		for i := range jobs {
			if i < n {
				goResults[i] = run(i, "go")
			} else {
				goResults[i] = runSidecarStore(t, root, storePlans[i-n], i, "go", nil)
			}
			close(done[i])
		}
	}()
	defer func() { close(jobs); <-workerDone }()

	tsResults := make([][]sidecarStep, total2)
	outcomes := map[string]int{}
	steps := 0
	compare := func(i int) {
		<-done[i]
		got, want := goResults[i], tsResults[i]
		label := "store " + strconv.Itoa(i-n)
		if i < n {
			label = "sequence " + strconv.Itoa(i) + " (" + seqs[i].kind + ")"
		}
		if len(got) != len(want) {
			t.Fatalf("%s: %d Go steps, %d TS steps", label, len(got), len(want))
		}
		for si := range want {
			steps++
			if got[si].result != want[si].result {
				t.Fatalf("%s step %d:\ngot  %s\nwant %s", label, si, got[si].result, want[si].result)
			}
			if got[si].inspect != want[si].inspect {
				t.Fatalf("%s step %d: inspect differs:\ngot  %s\nwant %s", label, si, got[si].inspect, want[si].inspect)
			}
			if got[si].manifest != want[si].manifest {
				t.Fatalf("%s step %d: work directories differ:\ngo\n%s\nts\n%s", label, si, got[si].manifest, want[si].manifest)
			}
			v, _ := jsjson.Parse(want[si].result)
			kind := "unparseable"
			switch {
			case want[si].result == "unparseable":
			case v.Get("threw").Kind() == jsjson.String:
				kind = "threw: " + jsstr.ToUTF8(v.Get("threw").Str())
				if len(kind) > 60 {
					kind = kind[:60]
				}
			case v.Get("result", "status").Kind() == jsjson.String:
				kind = v.Get("result", "status").Str()
			default:
				kind = "value"
			}
			outcomes[kind]++
			recordOutput(t, want[si].result)
			recordOutput(t, want[si].inspect)
		}
		t.Logf("%s: %d steps equal", label, len(want))
		os.RemoveAll(filepath.Join(root, "ts-"+strconv.Itoa(i)))
		os.RemoveAll(filepath.Join(root, "go-"+strconv.Itoa(i)))
		if t.Failed() {
			t.FailNow()
		}
	}
	prev := -1
	for i := first; i < total2; i++ {
		if i >= n && i-n < storesFrom {
			continue
		}
		if i < n {
			tsResults[i] = run(i, "ts")
		} else {
			tsResults[i] = runSidecarStore(t, root, storePlans[i-n], i, "ts", c)
		}
		if t.Failed() {
			t.FailNow()
		}
		jobs <- i
		if prev >= 0 {
			compare(prev)
		}
		prev = i
	}
	if prev >= 0 {
		compare(prev)
	}

	if ts, goTick := oracleCall(t, c, "seededTick", "null"), clock.NowMillis()-seededEpochMillis; ts != strconv.FormatInt(goTick, 10) {
		t.Fatalf("oracle clock at %s, Go clock at %d", ts, goTick)
	}
	outputs := recordedOutputs(t)
	// FAULTLINE_SIDECAR_DUMP=<file> appends what this window compared, so
	// windowed local runs can be checked together by
	// TestSidecarReachedFromDump (CI runs all 400 in one test).
	if dump := os.Getenv("FAULTLINE_SIDECAR_DUMP"); dump != "" {
		f, err := os.OpenFile(dump, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString(outputs)
		f.Close()
	}
	reached := map[string]int{}
	for _, marker := range []string{
		`"status":"SESSION_STARTED"`, `"status":"TURN_STARTED"`, `"status":"TOOL_USE_STARTED"`, `"status":"TOOL_USE_COMPLETED"`,
		`"status":"CHECKPOINT_RECORDED"`, `"status":"TURN_SNAPSHOT_RECORDED"`, `"status":"CHECKPOINT_SKIPPED_DIRTY"`, `"status":"CHECKPOINT_SKIPPED_UNAVAILABLE"`,
		"Another FaultLine sidecar event is still recording", "EEXIST", "Contradictory sidecar stop receipt", "Invalid sidecar stop receipt",
		`"state":"STALE"`, `"state":"ACTIVE"`, "FaultLine sidecar storage must be a real directory", "FaultLine snapshot warning",
		"valid recordings share last-event time", "No valid Codex sidecar ledger found", `"validCandidateCount"`,
	} {
		reached[marker] = strings.Count(outputs, marker)
		if reached[marker] == 0 && fullRun {
			t.Errorf("no generated step reached %q", marker)
		}
	}
	// Any window that runs the interrupted-turn directed sequence must show
	// its status (the marker loop above only checks full runs).
	if at := unavailableDirected(random); first <= at && at < n && reached[`"status":"CHECKPOINT_SKIPPED_UNAVAILABLE"`] == 0 {
		t.Errorf("directed sequence %d did not reach CHECKPOINT_SKIPPED_UNAVAILABLE", at)
	}
	if fullRun {
		for _, f := range sidecarGatedFiles {
			sidecarRequireReached(t, f, outputs)
		}
	}
	liveSummary(t, "%d sequences and %d stores, %d steps compared; outcomes %v", n-first, stores, steps, outcomes)
}

// sidecarRequireReached is requireReached with the euid 0 exemption below.
func sidecarRequireReached(t *testing.T, file, outputs string) {
	t.Helper()
	repo := oracle.RepoRoot(t)
	outputs += goldenText(t, repo)
	for lit, pos := range errorLiterals(t, filepath.Join(repo, filepath.FromSlash(file))) {
		if _, ok := unreachableLiterals[lit]; ok {
			continue
		}
		// An unlink of a stale lock fails only for a user the read-only
		// recordings directory binds (EACCES); root unlinks it. The
		// x:ro-lock directed sequence reaches the refusal for any other user.
		if lit == "FaultLine could not recover a stale sidecar lock:" && os.Geteuid() == 0 {
			continue
		}
		reached := true
		for _, frag := range messageFragments(lit) {
			reached = reached && strings.Contains(outputs, jsjson.Quote(frag)[1:len(jsjson.Quote(frag))-1])
		}
		if !reached {
			t.Errorf("%s: no generated input reached %q", pos, lit)
		}
	}
}

// storePlan is one multi-session store: its ledgers and what is wrong with
// some of them.
type storePlan struct {
	sessions []storeSession
}

type storeSession struct {
	id         string
	occurredAt string // the SESSION_STARTED event's time
	breakage   string // "", "truncate", "garbage", "symlink", "lock", "bad-receipt"
}

var storeTimes = []string{"2026-07-16T12:00:00.000Z", "2026-07-16T12:00:01.000Z", "2026-07-17T08:30:00.000Z"}

func randomStorePlan(r *rand.Rand, k int) storePlan {
	count := []int{0, 1, 1, 2, 2, 3, 4}[r.IntN(7)]
	var plan storePlan
	for n := 0; n < count; n++ {
		s := storeSession{id: "store-" + strconv.Itoa(k) + "-" + strconv.Itoa(n), occurredAt: storeTimes[r.IntN(len(storeTimes))]}
		if r.IntN(4) == 0 {
			s.breakage = []string{"truncate", "garbage", "symlink", "lock", "bad-receipt"}[r.IntN(5)]
		}
		plan.sessions = append(plan.sessions, s)
	}
	return plan
}

// runSidecarStore builds plan's store at root/w<index>, calls the
// resolver and inspect on one side and returns the results.
func runSidecarStore(t *testing.T, root string, plan storePlan, index int, side string, c *oracle.Client) []sidecarStep {
	t.Helper()
	work := filepath.Join(root, "w"+strconv.Itoa(index))
	repo := filepath.Join(work, "repo")
	os.MkdirAll(repo, 0o755)
	sidecarGit(t, repo, "init", "-q")
	recordings := filepath.Join(repo, ".git", "faultline", "recordings")
	os.MkdirAll(recordings, 0o700)
	for _, s := range plan.sessions {
		l, err := ledger.Create(ledger.CreateOptions{LedgerID: "ledger-" + s.id, SessionID: s.id, CreatedAt: "2026-07-16T11:00:00.000Z"})
		if err != nil {
			t.Error(err)
			continue
		}
		created := l
		l, err = ledger.Append(l, jsjson.MustObject("type", jsjson.MakeString("SESSION_STARTED"), "payload", jsjson.MustObject(
			"transport", jsjson.MakeString("SIDE_CAR"), "workingDirectory", jsjson.MakeString(repo), "model", jsjson.MakeString("m"))),
			ledger.AppendOptions{EventID: "e-" + s.id, OccurredAt: s.occurredAt, HasEventID: true, HasOccurredAt: true})
		if err != nil {
			t.Error(err)
			continue
		}
		file := filepath.Join(recordings, "codex-"+canonical.SHA256Hex(s.id)+".json")
		if s.breakage == "empty" { // a ledger without events, which still verifies
			l = created
		}
		// Written directly: WriteAtomic would read a UUID from the seeded
		// clock on both phases and put the Go side's sequence out of step.
		if err := os.WriteFile(file, []byte(jsjson.StringifyIndent(l, "  ")+"\n"), 0o644); err != nil {
			t.Error(err)
			continue
		}
		switch s.breakage {
		case "truncate":
			b, _ := os.ReadFile(file)
			os.WriteFile(file, b[:len(b)/2], 0o644)
		case "garbage":
			os.WriteFile(file, []byte("not a ledger\n"), 0o644)
		case "symlink":
			os.Remove(file)
			os.Symlink(filepath.Join(work, "elsewhere.json"), file)
		case "lock":
			lock := file + ".sidecar.lock"
			os.WriteFile(lock, nil, 0o600)
			mtime := time.UnixMilli(sidecarFreshFuture)
			os.Chtimes(lock, mtime, mtime)
		case "bad-receipt":
			receipt := filepath.Join(recordings, "codex-stop-"+canonical.SHA256Hex(s.id+"\x00t-1")+".json")
			os.WriteFile(receipt, []byte(`{"schemaVersion":"faultline.codex-sidecar-stop-receipt.v1","sessionId":`+jsjson.Quote(s.id)+
				`,"turnId":"t-1","status":"CHECKPOINT_SKIPPED_DIRTY","reason":"DIRTY_WORKTREE"}`+"\n"), 0o644)
		}
	}
	var out []sidecarStep
	call := func(op string, args string, goFn func() (jsjson.Value, error)) {
		var res string
		if side == "ts" {
			res = oracleText(t, c, op, args)
		} else {
			v, err := goFn()
			res = sidecarOutcomeJSON(v, err, "")
		}
		out = append(out, sidecarStep{result: res, manifest: sidecarManifest(work)})
	}
	call("resolveLatestSidecarLedgerPath", `{"cwd":`+jsjson.Quote(repo)+`}`, func() (jsjson.Value, error) {
		l, err := sidecar.ResolveLatestLedgerPath(repo)
		return l.JSON(), err
	})
	call("inspectObservedCodexSidecar", `{"cwd":`+jsjson.Quote(repo)+`}`, func() (jsjson.Value, error) {
		i, err := sidecar.Inspect(repo, "")
		return i.JSON(), err
	})
	for _, s := range plan.sessions {
		id := s.id
		call("inspectObservedCodexSidecar", `{"cwd":`+jsjson.Quote(repo)+`,"sessionId":`+jsjson.Quote(id)+`}`, func() (jsjson.Value, error) {
			i, err := sidecar.Inspect(repo, id)
			return i.JSON(), err
		})
	}
	if err := os.Rename(work, filepath.Join(root, side+"-"+strconv.Itoa(index))); err != nil {
		t.Error(err)
	}
	return out
}

// TestSidecarReachedFromDump is requireReached over the outputs of several
// windowed TestLiveSidecarHooks runs (FAULTLINE_SIDECAR_DUMP files), for
// local runs; the full run does it itself. It needs no oracle.
func TestSidecarReachedFromDump(t *testing.T) {
	dump := os.Getenv("FAULTLINE_SIDECAR_REACHED_FROM")
	if dump == "" {
		t.Skip("set FAULTLINE_SIDECAR_REACHED_FROM=<dump file> to check windowed runs together")
	}
	b, err := os.ReadFile(dump)
	if err != nil {
		t.Fatal(err)
	}
	outputs := string(b)
	for _, f := range sidecarGatedFiles {
		sidecarRequireReached(t, f, outputs)
	}
}

// rebuildSidecarLedger rewrites the session's ledger through ledger.Append
// with every id and time explicit (no clock read), applying each variant:
// drop-snapshot, drop-checkpoint, fail-outcome, rename-start,
// empty (no events at all, which still verifies), snap-as-ckpt (the snapshot takes the checkpoint's id), ckpt-as-snap (the
// checkpoint takes the snapshot's id) and other-session.
func rebuildSidecarLedger(t *testing.T, path, session string, variants []string) {
	t.Helper()
	l, err := ledger.ReadVerified(path)
	if err != nil {
		t.Errorf("rebuild %v: %v", variants, err)
		return
	}
	has := func(v string) bool {
		for _, x := range variants {
			if x == v {
				return true
			}
		}
		return false
	}
	owner := session
	if has("other-session") {
		owner = "other-session"
	}
	out, err := ledger.Create(ledger.CreateOptions{LedgerID: l.Get("ledgerId").Str(), SessionID: owner, CreatedAt: l.Get("createdAt").Str()})
	if err != nil {
		t.Error(err)
		return
	}
	events := l.Get("events").Items()
	var ckptID, snapID string
	for _, e := range events {
		switch e.Get("event", "type").Str() {
		case "WORKTREE_CHECKPOINT":
			ckptID = e.Get("eventId").Str()
		case "TURN_TREE_SNAPSHOT":
			snapID = e.Get("eventId").Str()
		}
	}
	if has("empty") {
		events = nil
	}
	for _, e := range events {
		typ, id, event := e.Get("event", "type").Str(), e.Get("eventId").Str(), e.Get("event")
		switch {
		case typ == "TURN_TREE_SNAPSHOT" && (has("drop-snapshot") || has("ckpt-as-snap")):
			continue
		case typ == "WORKTREE_CHECKPOINT" && (has("drop-checkpoint") || has("snap-as-ckpt")):
			continue
		case typ == "TURN_TREE_SNAPSHOT" && has("snap-as-ckpt"):
			id = ckptID
		case typ == "WORKTREE_CHECKPOINT" && has("ckpt-as-snap"):
			id = snapID
		case typ == "SESSION_STARTED" && has("rename-start"):
			id = "renamed-session-start"
		case typ == "TURN_COMPLETED" && has("fail-outcome"):
			p := e.Get("event", "payload")
			event = jsjson.MustObject("type", jsjson.MakeString(typ), "payload", jsjson.MustObject(
				"turnId", p.Get("turnId"), "turnOrdinal", p.Get("turnOrdinal"), "outcome", jsjson.MakeString("FAILED")))
		}
		out, err = ledger.Append(out, event, ledger.AppendOptions{EventID: id, OccurredAt: e.Get("occurredAt").Str(), HasEventID: true, HasOccurredAt: true})
		if err != nil {
			t.Errorf("rebuild %v: %v", variants, err)
			return
		}
	}
	if err := os.WriteFile(path, []byte(jsstr.ToUTF8(jsjson.StringifyIndent(out, "  ")+"\n")), 0o600); err != nil {
		t.Error(err)
	}
}

// editSidecarReceipt rewrites a stop receipt: bad-snapdigest and
// bad-ckptdigest replace one digest, drop-snapdigest removes it,
// skipped-no-snap makes it a skipped-dirty receipt without digests,
// snaponly-no-digest a TURN_SNAPSHOT_RECORDED one without, other-session
// changes its session id.
func editSidecarReceipt(t *testing.T, path, session, turn, edit string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("receipt %s: %v", edit, err)
		return
	}
	v, err := jsjson.Parse(string(b))
	if err != nil {
		t.Error(err)
		return
	}
	o := jsjson.NewObj()
	for _, k := range v.Obj().Keys() {
		o.Set(k, v.Obj().Field(k))
	}
	bad := jsjson.MakeString("sha256:" + strings.Repeat("f", 64))
	switch edit {
	case "bad-snapdigest":
		o.Set("turnSnapshotDigest", bad)
	case "bad-ckptdigest":
		o.Set("checkpointDigest", bad)
	case "drop-snapdigest":
		o.Delete("turnSnapshotDigest")
	case "skipped-no-snap":
		o.Set("status", jsjson.MakeString("CHECKPOINT_SKIPPED_DIRTY"))
		o.Set("reason", jsjson.MakeString("DIRTY_WORKTREE"))
		o.Delete("checkpointDigest")
		o.Delete("turnSnapshotDigest")
	case "snaponly-no-digest":
		o.Set("status", jsjson.MakeString("TURN_SNAPSHOT_RECORDED"))
		o.Delete("checkpointDigest")
		o.Delete("turnSnapshotDigest")
	case "other-session":
		o.Set("sessionId", jsjson.MakeString("other-"+session))
	default:
		t.Errorf("unknown receipt edit %q", edit)
	}
	os.WriteFile(path, []byte(jsstr.ToUTF8(jsjson.StringifyIndent(jsjson.MakeObject(o), "  ")+"\n")), 0o600)
}

// directedSessionID is the session of every directed sequence.
const directedSessionID = "dir-s"

// directedSequences are fixed hook sequences that reach the states random
// ones cannot: redelivery with different facts, a ledger rebuilt without its
// snapshot or checkpoint, edited receipts, a symlinked ledger and receipt
// and a lock that is a directory. They run after the random ones, on clean
// repositories. An op step is an unparseable document (both sides skip the
// call) that only applies its op.
func directedSequences() [][]gen.HookStep {
	field := func(name string, kv ...string) gen.HookEvent {
		f := [][2]any{{"hook_event_name", name}, {"session_id", directedSessionID}, {"cwd", gen.HookCwd}, {"model", "gpt-5.6"}}
		for i := 0; i < len(kv); i += 2 {
			f = append(f, [2]any{kv[i], kv[i+1]})
		}
		return gen.HookEvent{Fields: f}
	}
	start := gen.HookStep{Event: field("SessionStart")}
	prompt := func(turn, text string) gen.HookStep {
		return gen.HookStep{Event: field("UserPromptSubmit", "turn_id", turn, "prompt", text)}
	}
	stop := func(turn string) gen.HookStep { return gen.HookStep{Event: field("Stop", "turn_id", turn)} }
	pre := func(tool, call string) gen.HookStep {
		return gen.HookStep{Event: field("PreToolUse", "turn_id", "t-1", "tool_name", tool, "tool_call_id", call)}
	}
	op := func(kind, arg string) gen.HookStep {
		return gen.HookStep{Event: gen.HookEvent{Raw: []byte("{")}, Op: gen.HookOp{Kind: kind, Arg: arg}}
	}
	base := func(more ...gen.HookStep) []gen.HookStep {
		return append([]gen.HookStep{start, prompt("t-1", "p1"), pre("shell", "call-1"), stop("t-1")}, more...)
	}
	return [][]gen.HookStep{
		{start, prompt("t-1", "p1"), pre("shell", "call-1"), pre("apply_patch", "call-1")},
		{start, prompt("t-1", "a"), prompt("t-1", "b")},
		base(op("x:rebuild", "other-session"), start),
		base(op("x:rebuild", "rename-start"), start),
		base(op("remove-receipt", "t-1"), op("x:rebuild", "fail-outcome,drop-checkpoint,drop-snapshot"), stop("t-1")),
		base(op("remove-receipt", "t-1"), op("x:rebuild", "snap-as-ckpt"), stop("t-1")),
		base(op("remove-receipt", "t-1"), op("x:rebuild", "ckpt-as-snap"), stop("t-1")),
		base(op("x:rebuild", "drop-snapshot"), stop("t-1")),
		base(op("x:receipt", "t-1|bad-snapdigest"), stop("t-1")),
		base(op("x:receipt", "t-1|bad-ckptdigest"), stop("t-1")),
		base(op("x:rebuild", "drop-snapshot"), op("x:receipt", "t-1|skipped-no-snap"), stop("t-1")),
		base(op("x:rebuild", "drop-checkpoint"), stop("t-1")),
		base(op("x:rebuild", "drop-snapshot,drop-checkpoint"), op("x:receipt", "t-1|snaponly-no-digest"), stop("t-1")),
		base(op("x:receipt", "t-1|other-session"), stop("t-1")),
		base(op("x:receipt-symlink", "t-1"), stop("t-1")),
		base(op("x:ledger-symlink", ""), prompt("t-2", "p2")),
		base(op("x:lock-dir", ""), prompt("t-2", "p2")),
		// TURN_SNAPSHOT_RECORDED: a completed turn with its snapshot, no
		// checkpoint and no receipt.
		base(op("remove-receipt", "t-1"), op("x:rebuild", "drop-checkpoint"), stop("t-1"), stop("t-1")),
		// Hooks on a valid ledger without events (Create returns one).
		{start, op("x:rebuild", "empty"), start, prompt("t-1", "p1"), stop("t-1"), pre("shell", "call-1")},
		// The unlink of a stale lock refused (EACCES) in a read-only recordings directory.
		{start, op("x:ro-lock", ""), prompt("t-1", "p1"), prompt("t-1", "p1")},
		// CHECKPOINT_SKIPPED_UNAVAILABLE (hook.go "interrupted transaction"):
		// a completed turn with neither snapshot, checkpoint nor receipt.
		// Appended last so that every earlier sequence keeps its index.
		base(op("remove-receipt", "t-1"), op("x:rebuild", "drop-snapshot,drop-checkpoint"), stop("t-1")),
	}
}

// unavailableDirected is the index, among the sequences, of the directed
// sequence that must reach CHECKPOINT_SKIPPED_UNAVAILABLE (the last one).
func unavailableDirected(random int) int { return random + len(directedSequences()) - 1 }

// maskSidecarPID masks only ".<24 lowercase hex>.<own pid>.tmp".
func TestMaskSidecarPID(t *testing.T) {
	key := strings.Repeat("ab12", 6) // 24 lowercase hex
	cases := []struct{ name, in, want string }{
		{"own pid", "x/." + key + ".4242.tmp", "x/." + key + ".<PID>.tmp"},
		{"twice", "." + key + ".4242.tmp ." + key + ".4242.tmp", "." + key + ".<PID>.tmp ." + key + ".<PID>.tmp"},
		{"wrong pid", "." + key + ".4243.tmp", "." + key + ".4243.tmp"},
		{"pid prefix of another", "." + key + ".42420.tmp", "." + key + ".42420.tmp"},
		{"unrelated .pid.tmp", "/tmp/data.4242.tmp", "/tmp/data.4242.tmp"},
		{"short key", ".abc123.4242.tmp", ".abc123.4242.tmp"},
		{"uppercase key", "." + strings.ToUpper(key) + ".4242.tmp", "." + strings.ToUpper(key) + ".4242.tmp"},
		{"key one too long", "." + key + "a.4242.tmp", "." + key + "a.4242.tmp"},
		{"different key stays comparable", "." + key + ".4242.tmp", "." + key + ".<PID>.tmp"},
	}
	for _, c := range cases {
		if got := maskSidecarPID(c.in, 4242); got != c.want {
			t.Errorf("%s: %q -> %q, want %q", c.name, c.in, got, c.want)
		}
	}
	other := strings.Repeat("cd34", 6)
	if maskSidecarPID("."+key+".4242.tmp", 4242) == maskSidecarPID("."+other+".4242.tmp", 4242) {
		t.Error("two stage keys compare equal after masking")
	}
}
