//go:build linux

// Command globalopen-fixture is the test-owned adversarial target (and the
// real verifier) for the globalopen helper (../globalopen). It is a TEST-ONLY
// tool: it is NOT FaultLine, ships in no release, and builds only on linux
// (the open-group syscalls it issues are raw Linux syscalls). Its OS-thread
// locking exists ONLY here, to force distinct tids for the adversarial
// scenarios; it is not FaultLine thread pinning, and the helper never aligns a
// tracee's threads, timing or CPU.
//
//	prepare  -dir D                 create D/target (content "keep") and D/other
//	run      -scenario S -dir D [-n N] [-exit C] [-die SIG]
//	verify   -scenario S -dir D -log L -out O -errno E [-n N] [-exit C] [-die SIG]
//
// Scenarios:
//
//	family  N>=3 locked OS threads, one barrier release, each opens D/target
//	        once through a DIFFERENT form: openat absolute, openat relative to
//	        cwd, openat relative to a dirfd, openat2 absolute, open(2) (amd64)
//	        or openat again (arm64). One extra unmatched open of D/other.
//	pid     opens D/f.<PID> (absent) twice; first must see the injected errno,
//	        the second the natural ENOENT.
//	create  D/fresh absent: open O_CREAT|O_WRONLY fails (file must not exist),
//	        the second identical open creates it.
//	trunc   D/target holds "keep": open O_WRONLY|O_TRUNC fails (content must be
//	        intact), the second truncates it.
//	child   a re-exec of this binary opens D/target once (child process
//	        tracee); then the parent opens it once.
//	alias   opens a symlink to the target (must be REFUSED, not injected),
//	        and a path with ".." that reaches it (must be injected).
//	marker  opens D/m(INJECTED) twice (a path containing the literal marker).
//
// The target path template for the tracer is D/target (D/f.<PID> for pid,
// D/fresh for create, D/m(INJECTED) for marker, D/target for the rest; alias
// uses D/target too).
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

func init() {
	// Fixture only: the main goroutine stays on the main OS thread, so the tid
	// printed by Gettid is the tid that makes the single-threaded scenarios'
	// raw syscalls.
	runtime.LockOSThread()
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: prepare|run|verify|child")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "prepare":
		os.Exit(prepare(os.Args[2:]))
	case "run":
		os.Exit(run(os.Args[2:]))
	case "child":
		os.Exit(child(os.Args[2:]))
	case "verify":
		os.Exit(verify(os.Args[2:]))
	}
	fmt.Fprintln(os.Stderr, "unknown subcommand", os.Args[1])
	os.Exit(2)
}

func prepare(args []string) int {
	fs := flag.NewFlagSet("prepare", flag.ExitOnError)
	dir := fs.String("dir", "", "")
	fs.Parse(args)
	if err := os.WriteFile(filepath.Join(*dir, "target"), []byte("keep"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := os.WriteFile(filepath.Join(*dir, "other"), []byte("other"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// A target whose name contains the literal injected marker.
	if err := os.WriteFile(filepath.Join(*dir, markerName), []byte("marker"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

const markerName = "m(INJECTED)"

func errName(err error) string {
	if err == nil {
		return "OK"
	}
	e, _ := err.(syscall.Errno)
	switch e {
	case syscall.ENOSPC:
		return "ENOSPC"
	case syscall.EIO:
		return "EIO"
	case syscall.EACCES:
		return "EACCES"
	case syscall.ENOENT:
		return "ENOENT"
	}
	return "E" + strconv.Itoa(int(e))
}

func cstr(s string) *byte {
	p, err := syscall.BytePtrFromString(s)
	if err != nil {
		panic(err)
	}
	return p
}

// raw issues the three members of the open group directly.
func rawOpenat(dirfd int, path string, flags, mode int) (int, error) {
	fd, _, e := syscall.Syscall6(syscall.SYS_OPENAT, uintptr(dirfd), uintptr(unsafe.Pointer(cstr(path))), uintptr(flags), uintptr(mode), 0, 0)
	if e != 0 {
		return -1, e
	}
	return int(fd), nil
}

type openHow struct{ flags, mode, resolve uint64 }

func rawOpenat2(dirfd int, path string, flags, mode int) (int, error) {
	how := openHow{flags: uint64(flags), mode: uint64(mode)}
	fd, _, e := syscall.Syscall6(437, uintptr(dirfd), uintptr(unsafe.Pointer(cstr(path))), uintptr(unsafe.Pointer(&how)), unsafe.Sizeof(how), 0, 0)
	if e != 0 {
		return -1, e
	}
	return int(fd), nil
}

const atFDCWD = -100

type record struct {
	label string
	tid   int
	err   error
}

func emit(r record) {
	fmt.Printf("RESULT label=%s tid=%d errno=%s\n", r.label, r.tid, errName(r.err))
}

func closeFd(fd int, err error) {
	if err == nil {
		syscall.Close(fd)
	}
}

func run(args []string) int {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	scenario := fs.String("scenario", "family", "")
	dir := fs.String("dir", "", "")
	n := fs.Int("n", 5, "locked threads for family (>= 3)")
	exitCode := fs.Int("exit", 0, "exit status")
	die := fs.Int("die", 0, "signal to kill ourselves with at the end")
	fs.Parse(args)
	target := filepath.Join(*dir, "target")
	switch *scenario {
	case "family":
		if *n < 3 {
			fmt.Fprintln(os.Stderr, "need -n >= 3")
			return 2
		}
		runFamily(*dir, target, *n)
	case "pid":
		p := filepath.Join(*dir, "f."+strconv.Itoa(os.Getpid()))
		for i := range 2 {
			fd, err := rawOpenat(atFDCWD, p, syscall.O_RDONLY, 0)
			closeFd(fd, err)
			emit(record{label: "pid-" + strconv.Itoa(i), tid: syscall.Gettid(), err: err})
		}
	case "create":
		p := filepath.Join(*dir, "fresh")
		fd, err := rawOpenat(atFDCWD, p, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_CLOEXEC, 0o600)
		closeFd(fd, err)
		_, serr := os.Lstat(p)
		fmt.Printf("SIDEEFFECT after-first exists=%t\n", serr == nil)
		emit(record{label: "create-0", tid: syscall.Gettid(), err: err})
		fd, err = rawOpenat(atFDCWD, p, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_CLOEXEC, 0o600)
		closeFd(fd, err)
		_, serr = os.Lstat(p)
		fmt.Printf("SIDEEFFECT after-second exists=%t\n", serr == nil)
		emit(record{label: "create-1", tid: syscall.Gettid(), err: err})
	case "trunc":
		fd, err := rawOpenat(atFDCWD, target, syscall.O_WRONLY|syscall.O_TRUNC|syscall.O_CLOEXEC, 0)
		closeFd(fd, err)
		b, _ := os.ReadFile(filepath.Join(*dir, "other")) // unmatched
		_ = b
		st, serr := os.Stat(target)
		size := int64(-1)
		if serr == nil {
			size = st.Size()
		}
		fmt.Printf("SIDEEFFECT after-first size=%d\n", size)
		emit(record{label: "trunc-0", tid: syscall.Gettid(), err: err})
		fd, err = rawOpenat(atFDCWD, target, syscall.O_WRONLY|syscall.O_TRUNC|syscall.O_CLOEXEC, 0)
		closeFd(fd, err)
		st, serr = os.Stat(target)
		size = -1
		if serr == nil {
			size = st.Size()
		}
		fmt.Printf("SIDEEFFECT after-second size=%d\n", size)
		emit(record{label: "trunc-1", tid: syscall.Gettid(), err: err})
	case "child":
		self, _ := os.Executable()
		cmd := exec.Command(self, "child", "-dir", *dir)
		out, err := cmd.Output()
		// The child's own RESULT line is passed through verbatim so the
		// verifier parses the actual child result and tid.
		fmt.Print(string(out))
		if err != nil {
			fmt.Printf("CHILD-ERR %v\n", err)
		}
		fd, oerr := rawOpenat(atFDCWD, target, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
		closeFd(fd, oerr)
		emit(record{label: "parent", tid: syscall.Gettid(), err: oerr})
	case "alias":
		link := filepath.Join(*dir, "alias-link")
		os.Remove(link)
		if err := os.Symlink(target, link); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fd, err := rawOpenat(atFDCWD, link, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
		closeFd(fd, err)
		emit(record{label: "symlink-final", tid: syscall.Gettid(), err: err})
		// Built by concatenation: filepath.Join would clean the ".." away
		// before the syscall. The verifier asserts the raw ENTRY has "/../".
		dotted := *dir + "/../" + filepath.Base(*dir) + "/target"
		fd, err = rawOpenat(atFDCWD, dotted, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
		closeFd(fd, err)
		emit(record{label: "dotdot", tid: syscall.Gettid(), err: err})
	case "marker":
		p := filepath.Join(*dir, markerName)
		for i := range 2 {
			fd, err := rawOpenat(atFDCWD, p, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
			closeFd(fd, err)
			emit(record{label: "marker-" + strconv.Itoa(i), tid: syscall.Gettid(), err: err})
		}
	default:
		fmt.Fprintln(os.Stderr, "unknown scenario", *scenario)
		return 2
	}
	if *die != 0 {
		syscall.Kill(os.Getpid(), syscall.Signal(*die))
		// Block in the kernel until the signal ends us. select{} let Go's
		// deadlock detector print "all goroutines are asleep" before a
		// ptrace-delayed delivery. The signal is neither registered nor
		// swallowed; rt_sigsuspend with an empty mask just waits.
		sigsuspendForever()
	}
	return *exitCode
}

// sigsuspendForever calls rt_sigsuspend with an empty 8-byte kernel sigset
// (sigsetsize 8 on both supported arches) in a loop; it returns EINTR only if
// a handler runs and returns, in which case it waits again. Go's runtime
// handler for an un-notified SIGTERM re-raises the default action and ends
// the process, so this normally never returns.
func sigsuspendForever() {
	var mask [8]byte
	for {
		syscall.Syscall(sysRtSigsuspend, uintptr(unsafe.Pointer(&mask[0])), 8, 0)
	}
}

func child(args []string) int {
	fs := flag.NewFlagSet("child", flag.ExitOnError)
	dir := fs.String("dir", "", "")
	fs.Parse(args)
	fd, err := rawOpenat(atFDCWD, filepath.Join(*dir, "target"), syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	closeFd(fd, err)
	fmt.Printf("RESULT label=child tid=%d errno=%s\n", syscall.Gettid(), errName(err))
	return 0
}

func runFamily(dir, target string, n int) {
	if err := os.Chdir(dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	dfd, err := syscall.Open(dir, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	other := filepath.Join(dir, "other")
	res := make([]record, n)
	tids := make([]int, n)
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	ready.Add(n)
	done.Add(n)
	for i := range n {
		go func() {
			defer done.Done()
			runtime.LockOSThread() // adversarial fixture only
			tids[i] = syscall.Gettid()
			ready.Done()
			<-start
			var fd int
			var err error
			label := ""
			switch i % 5 {
			case 0:
				label, fd, err = "openat-abs", 0, nil
				fd, err = rawOpenat(atFDCWD, target, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
			case 1:
				label = "openat-cwd-relative"
				fd, err = rawOpenat(atFDCWD, "target", syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
			case 2:
				label = "openat-dirfd-relative"
				fd, err = rawOpenat(dfd, "target", syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
			case 3:
				label = "openat2-abs"
				fd, err = rawOpenat2(atFDCWD, target, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
			case 4:
				label = "open-or-openat-abs"
				fd, err = rawOpenOrOpenat(target)
			}
			closeFd(fd, err)
			if i == 0 {
				ofd, oerr := rawOpenat(atFDCWD, other, syscall.O_RDONLY|syscall.O_CLOEXEC, 0) // unmatched
				closeFd(ofd, oerr)
			}
			res[i] = record{label: label, tid: tids[i], err: err}
		}()
	}
	ready.Wait()
	seen := map[int]bool{}
	for _, t := range tids {
		seen[t] = true
	}
	if len(seen) != n {
		fmt.Fprintf(os.Stderr, "fixture: %d workers but %d distinct tids\n", n, len(seen))
		close(start)
		done.Wait()
		os.Exit(2)
	}
	close(start)
	done.Wait()
	sort.Slice(res, func(a, b int) bool { return res[a].tid < res[b].tid })
	fmt.Printf("FIXTURE threads=%d distinct=%d\n", n, len(seen))
	for _, r := range res {
		emit(r)
	}
}

func kv(line string) map[string]string {
	m := map[string]string{}
	for _, f := range strings.Fields(line) {
		if k, v, ok := strings.Cut(f, "="); ok {
			m[k] = v
		}
	}
	return m
}

// quoted returns the value of key= when it is a Go-quoted string.
func quoted(line, key string) string {
	i := strings.Index(line, " "+key+"=\"")
	if i < 0 {
		return ""
	}
	rest := line[i+len(key)+2:]
	q, err := strconv.QuotedPrefix(rest)
	if err != nil {
		return ""
	}
	s, _ := strconv.Unquote(q)
	return s
}

func verify(args []string) int {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	scenario := fs.String("scenario", "family", "")
	dir := fs.String("dir", "", "")
	logPath := fs.String("log", "", "")
	outPath := fs.String("out", "", "")
	errno := fs.String("errno", "ENOSPC", "")
	n := fs.Int("n", 5, "")
	exitCode := fs.Int("exit", 0, "")
	die := fs.Int("die", 0, "")
	fs.Parse(args)

	logb, err := os.ReadFile(*logPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	outb, err := os.ReadFile(*outPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	var fails []string
	failf := func(f string, a ...any) { fails = append(fails, fmt.Sprintf(f, a...)) }
	logText := string(logb)

	if c := strings.Count(logText, "(INJECTED)"); c != 1 {
		failf("raw (INJECTED) count %d, want exactly 1", c)
	}
	type entry struct {
		tid                        int
		syscall, decision, raw, rs string
	}
	type exit struct {
		tid      int
		errno    string
		injected bool
		ret, raw string
	}
	entries := map[int]entry{}
	exits := map[int]exit{}
	var entryOrder []int // seq in log order: must be exactly 1..N
	var exitOrder []int
	errnoNum := map[string]string{"ENOSPC": "-28", "EIO": "-5", "EACCES": "-13", "ENOENT": "-2"}
	var health, summary map[string]string
	nHealth, nSummary, nHeader := 0, 0, 0
	rootPid := ""
	for _, line := range strings.Split(logText, "\n") {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "GLOBALOPEN ") {
			failf("log line without the GLOBALOPEN label: %.80q", line)
			continue
		}
		body := strings.TrimPrefix(line, "GLOBALOPEN ")
		switch {
		case strings.HasPrefix(body, "FATAL"):
			failf("tracer logged: %s", line)
		case strings.HasPrefix(body, "HEADER"):
			nHeader++
		case strings.HasPrefix(body, "TARGET"):
			rootPid = kv(body)["root_pid"]
		case strings.HasPrefix(body, "ENTRY "):
			m := kv(body)
			seq, e1 := strconv.Atoi(m["seq"])
			tid, e2 := strconv.Atoi(m["tid"])
			if e1 != nil || e2 != nil {
				failf("unparsable ENTRY: %s", line)
				continue
			}
			if _, dup := entries[seq]; dup {
				failf("duplicate ENTRY seq %d", seq)
			}
			entries[seq] = entry{tid, m["syscall"], m["decision"], quoted(body, "raw"), quoted(body, "resolved")}
			entryOrder = append(entryOrder, seq)
			if !strings.Contains(body, " flags=0x") || !strings.Contains(body, " mode=0") || quoted(body, "raw") == "" && m["decision"] != "refused" {
				failf("ENTRY lacks raw path, flags or mode: %s", line)
			}
		case strings.HasPrefix(body, "EXIT "):
			m := kv(body)
			seq, e1 := strconv.Atoi(m["seq"])
			tid, e2 := strconv.Atoi(m["tid"])
			if e1 != nil || e2 != nil {
				failf("unparsable EXIT: %s", line)
				continue
			}
			if _, dup := exits[seq]; dup {
				failf("duplicate EXIT seq %d", seq)
			}
			exits[seq] = exit{tid, m["errno"], m["injected"] == "true", m["ret"], m["raw"]}
			exitOrder = append(exitOrder, seq)
			if m["injected"] == "true" {
				if m["raw_errno"] != "ENOSYS" || m["raw"] != "-38" || !strings.HasSuffix(body, "(INJECTED)") {
					failf("injected EXIT lacks raw -ENOSYS (raw=%q raw_errno=%q) or the marker: %s", m["raw"], m["raw_errno"], line)
				}
				if want := errnoNum[*errno]; m["ret"] != want {
					failf("injected EXIT ret=%q, want the rewritten %s for %s", m["ret"], want, *errno)
				}
			}
		case strings.HasPrefix(body, "HEALTH"):
			nHealth++
			health = kv(body)
		case strings.HasPrefix(body, "SUMMARY"):
			nSummary++
			summary = kv(body)
		}
	}
	if nHeader != 1 || nHealth != 1 || nSummary != 1 {
		failf("header/health/summary lines: %d/%d/%d, want 1/1/1", nHeader, nHealth, nSummary)
	}
	wantStatus := strconv.Itoa(*exitCode)
	if *die != 0 {
		wantStatus = strconv.Itoa(128 + *die)
	}
	if summary["root_status"] != wantStatus {
		failf("root_status %q, want %q", summary["root_status"], wantStatus)
	}
	if (*die != 0) != (summary["root_signaled"] == "true") {
		failf("root_signaled %q with die=%d", summary["root_signaled"], *die)
	}
	wantOK := "true"
	if *scenario == "alias" {
		wantOK = "false" // the refused symlink open makes HEALTH not ok, by design
	}
	if health["ok"] != wantOK {
		failf("HEALTH ok=%q, want %s (scenario %s): %v", health["ok"], wantOK, *scenario, health)
	}
	if health["spawned"] != health["reaped"] {
		failf("HEALTH spawned=%s reaped=%s", health["spawned"], health["reaped"])
	}
	if health["candidates"] != strconv.Itoa(len(entries)) {
		failf("HEALTH candidates=%s, ENTRY records=%d", health["candidates"], len(entries))
	}
	// ENTRY seqs must be exactly 1..N in log order (monotonic, contiguous);
	// EXIT lines are one per seq. Entry and exit order across tids may interleave.
	for i, seq := range entryOrder {
		if seq != i+1 {
			failf("ENTRY order: position %d has seq %d, want %d", i+1, seq, i+1)
			break
		}
	}
	if len(exitOrder) != len(entryOrder) {
		failf("%d EXIT lines, %d ENTRY lines", len(exitOrder), len(entryOrder))
	}
	// HEALTH passed/refused/unresolved derived from the records.
	nPass, nRef, nUnres := 0, 0, 0
	for _, e := range entries {
		switch e.decision {
		case "pass":
			nPass++
		case "refused":
			nRef++
		case "unresolved":
			nUnres++
		}
	}
	for k, v := range map[string]int{"passed": nPass, "refused": nRef, "unresolved": nUnres} {
		if health[k] != strconv.Itoa(v) {
			failf("HEALTH %s=%s, derived from ENTRY decisions %d", k, health[k], v)
		}
	}
	realDir, rerr := filepath.EvalSymlinks(*dir)
	if rerr != nil {
		failf("cannot resolve -dir: %v", rerr)
	}
	// expectResolved states the exact resolved path every non-refused candidate
	// of the scenario must carry.
	expectResolved := func(want string) {
		for seq, e := range entries {
			if e.decision == "refused" || e.decision == "unresolved" {
				continue
			}
			if e.rs != want {
				failf("seq %d resolved %q, want exactly %q", seq, e.rs, want)
			}
		}
	}
	injSeqs := []int{}
	for seq, e := range entries {
		x, ok := exits[seq]
		if !ok {
			failf("seq %d has no paired EXIT", seq)
			continue
		}
		if x.tid != e.tid {
			failf("seq %d: EXIT tid %d != ENTRY tid %d", seq, x.tid, e.tid)
		}
		if x.injected {
			injSeqs = append(injSeqs, seq)
			if x.errno != *errno || e.decision != "inject" {
				failf("seq %d injected errno=%s decision=%s", seq, x.errno, e.decision)
			}
		}
	}
	if len(exits) != len(entries) {
		failf("%d EXIT records for %d ENTRY records", len(exits), len(entries))
	}
	if health["injected"] != strconv.Itoa(len(injSeqs)) {
		failf("HEALTH injected=%s, injected EXIT records=%d", health["injected"], len(injSeqs))
	}
	sort.Ints(injSeqs)
	// The first candidate is the first injectable one, except in alias, where
	// the refused symlink open is candidate 1 and is never injected.
	wantSeq := 1
	if *scenario == "alias" {
		wantSeq = 2
	}
	if len(injSeqs) != 1 || injSeqs[0] != wantSeq {
		failf("injected seqs %v, want exactly [%d] (the first injectable global matching entry)", injSeqs, wantSeq)
	}

	var results []map[string]string
	var side []string
	for _, line := range strings.Split(string(outb), "\n") {
		switch {
		case strings.HasPrefix(line, "RESULT "):
			results = append(results, kv(line))
		case strings.HasPrefix(line, "SIDEEFFECT "):
			side = append(side, line)
		}
	}
	byLabel := map[string]map[string]string{}
	seenTid := map[string]bool{}
	nInjectedResults := 0
	for _, r := range results {
		byLabel[r["label"]] = r
		if r["errno"] == *errno {
			nInjectedResults++
		}
		if *scenario == "family" {
			if seenTid[r["tid"]] {
				failf("duplicate worker tid %s", r["tid"])
			}
			seenTid[r["tid"]] = true
		}
	}
	if nInjectedResults != 1 {
		failf("%d results carry the injected errno %s, want 1", nInjectedResults, *errno)
	}
	// The injected ENTRY's tid must be the tid of the result that saw the errno.
	if len(injSeqs) == 1 {
		itid := strconv.Itoa(entries[injSeqs[0]].tid)
		for _, r := range results {
			if r["errno"] == *errno && r["tid"] != itid {
				failf("injected tid %s but the errno was seen by tid %s (%s)", itid, r["tid"], r["label"])
			}
		}
	}

	switch *scenario {
	case "family":
		if len(results) != *n {
			failf("%d results, want %d", len(results), *n)
		}
		if len(entries) != *n {
			failf("%d candidate entries, want %d (one per worker; the unmatched open of other must not log)", len(entries), *n)
		}
		tids := map[int]bool{}
		sys := map[string]bool{}
		for _, e := range entries {
			tids[e.tid] = true
			sys[e.syscall] = true
			if e.rs == "" {
				failf("a candidate lacks a resolved path")
			}
		}
		if len(tids) != *n || len(tids) < 3 {
			failf("%d distinct tids among candidates, want %d (>= 3)", len(tids), *n)
		}
		for _, w := range []string{"openat", "openat2"} {
			if !sys[w] {
				failf("no %s candidate in the log", w)
			}
		}
		if rawHasOpen() && !sys["open"] {
			failf("amd64 run without an open candidate")
		}
		expectResolved(filepath.Join(realDir, "target"))
		rawKinds := map[string]bool{}
		for _, e := range entries {
			switch {
			case e.raw == filepath.Join(*dir, "target"):
				rawKinds["abs"] = true
			case e.raw == "target":
				rawKinds["relative"] = true // cwd- or dirfd-relative
			}
		}
		if !rawKinds["abs"] || !rawKinds["relative"] {
			failf("raw path forms seen %v, want absolute and relative", rawKinds)
		}
	case "pid":
		if r := byLabel["pid-0"]; r["errno"] != *errno {
			failf("first open errno %q, want the injected %s", r["errno"], *errno)
		}
		if r := byLabel["pid-1"]; r["errno"] != "ENOENT" {
			failf("second open errno %q, want natural ENOENT", r["errno"])
		}
		expectResolved(filepath.Join(realDir, "f."+rootPid))
		if rootPid == "" || len(entries) != 2 || !strings.HasSuffix(entries[1].raw, ".f."+rootPid) && !strings.HasSuffix(entries[1].raw, "/f."+rootPid) {
			failf("raw path %q does not carry root pid %q (entries=%d)", entries[1].raw, rootPid, len(entries))
		}
	case "create":
		if len(side) != 2 || side[0] != "SIDEEFFECT after-first exists=false" || side[1] != "SIDEEFFECT after-second exists=true" {
			failf("create side effects: %v", side)
		}
		if byLabel["create-0"]["errno"] != *errno || byLabel["create-1"]["errno"] != "OK" {
			failf("create results: %v", results)
		}
		expectResolved(filepath.Join(realDir, "fresh"))
	case "trunc":
		if len(side) != 2 || side[0] != "SIDEEFFECT after-first size=4" || side[1] != "SIDEEFFECT after-second size=0" {
			failf("trunc side effects: %v", side)
		}
		if byLabel["trunc-0"]["errno"] != *errno || byLabel["trunc-1"]["errno"] != "OK" {
			failf("trunc results: %v", results)
		}
		expectResolved(filepath.Join(realDir, "target"))
	case "child":
		if byLabel["child"]["errno"] != *errno || byLabel["parent"]["errno"] != "OK" {
			failf("child/parent results: %v", results)
		}
		// Exactly two results (the child's own, passed through, and the
		// parent's), on two different tids, each equal to its ENTRY's tid.
		c, p := byLabel["child"], byLabel["parent"]
		if len(results) != 2 || c == nil || p == nil || c["tid"] == p["tid"] {
			failf("child/parent results not exactly two on distinct tids: %v", results)
		}
		if len(entries) != 2 {
			failf("%d candidates, want 2 (child then parent)", len(entries))
		} else if c != nil && p != nil {
			if strconv.Itoa(entries[1].tid) != c["tid"] {
				failf("ENTRY 1 tid %d != child result tid %s", entries[1].tid, c["tid"])
			}
			if strconv.Itoa(entries[2].tid) != p["tid"] {
				failf("ENTRY 2 tid %d != parent result tid %s", entries[2].tid, p["tid"])
			}
			if entries[1].decision != "inject" || entries[2].decision != "pass" {
				failf("decisions %s/%s, want inject/pass", entries[1].decision, entries[2].decision)
			}
		}
		expectResolved(filepath.Join(realDir, "target"))
	case "marker":
		// The traced path itself contains the literal marker. The log must
		// escape it everywhere (HEADER, TARGET, ENTRY), so the raw count of
		// the marker is still the single injected EXIT.
		want := filepath.Join(*dir, markerName)
		if len(entries) != 2 || entries[1].raw != want || entries[2].raw != want {
			failf("marker raw paths: %+v", entries)
		}
		if byLabel["marker-0"]["errno"] != *errno || byLabel["marker-1"]["errno"] != "OK" {
			failf("marker results: %v", results)
		}
		expectResolved(filepath.Join(realDir, markerName))
	case "alias":
		refused, dotted := 0, 0
		for _, e := range entries {
			if e.decision == "refused" {
				refused++
			}
			if strings.Contains(e.raw, "/../") {
				dotted++
				if e.decision != "inject" {
					failf("the '..' candidate decision %s, want inject", e.decision)
				}
				if e.rs != filepath.Join(realDir, "target") {
					failf("the '..' candidate resolved %q, want %q", e.rs, filepath.Join(realDir, "target"))
				}
			}
		}
		if refused != 1 {
			failf("%d refused candidates, want 1 (the final-component symlink)", refused)
		}
		if dotted != 1 {
			failf("%d ENTRY records whose raw path contains /../, want 1 (a cleaned path would hide the case)", dotted)
		}
		if byLabel["symlink-final"]["errno"] != "OK" {
			failf("the refused symlink open must run untouched, got %s", byLabel["symlink-final"]["errno"])
		}
		if byLabel["dotdot"]["errno"] != *errno {
			failf("the '..' form must be injected, got %s", byLabel["dotdot"]["errno"])
		}
	default:
		failf("unknown scenario %q", *scenario)
	}

	fmt.Printf("OBSERVED scenario=%s candidates=%d injections=%d health=%v\n", *scenario, len(entries), len(injSeqs), health)
	if len(fails) > 0 {
		for _, f := range fails {
			fmt.Println("FAIL", f)
		}
		return 1
	}
	fmt.Println("PASS")
	return 0
}
