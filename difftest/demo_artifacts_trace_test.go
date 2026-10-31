//go:build faultline_test

package difftest

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/nodefs"
)

// These are parser/validator unit tests over synthetic strace text. They are
// evidence about the helpers only, never about native filesystem faults.

const (
	trOut    = "/r/.faultline/bundles/demo"
	trStage  = "/r/.faultline/bundles/demo.staging-aaaa"
	trBackup = "/r/.faultline/bundles/demo.backup-bbbb"
)

func TestParseStraceInjectedAndResolvedPaths(t *testing.T) {
	trace := strings.Join([]string{
		`100 openat(AT_FDCWD, "` + trStage + `/manifest.json", O_WRONLY|O_CREAT|O_TRUNC|O_CLOEXEC, 0666) = 7`,
		`100 write(7, "{\"a\":1}"..., 8) = -1 EIO (Input/output error) (INJECTED)`,
		`100 close(7) = 0`,
		`100 rename("` + trStage + `", "` + trOut + `") = 0`,
		`100 --- SIGCHLD {si_signo=SIGCHLD} ---`,
		`100 +++ exited with 0 +++`,
		``,
	}, "\n")
	calls, err := parseStrace(trace)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 4 {
		t.Fatalf("%d calls: %+v", len(calls), calls)
	}
	w := calls[1]
	if w.Name != "write" || !w.Injected || w.Errno != "EIO" || w.Path != trStage+"/manifest.json" || w.PID != "100" {
		t.Errorf("write: %+v", w)
	}
	if c := calls[2]; c.Name != "close" || c.Injected || c.Path != trStage+"/manifest.json" {
		t.Errorf("close: %+v", c)
	}
	if r := calls[3]; r.Path != trStage || r.Dest != trOut || r.Errno != "" {
		t.Errorf("rename: %+v", r)
	}
	// A write payload that looks like a path must never become the call's path.
	calls, err = parseStrace(`100 openat(AT_FDCWD, "/a/b", O_RDONLY) = 3` + "\n" + `100 write(3, "/not/a/path"..., 11) = 11` + "\n")
	if err != nil || calls[1].Path != "/a/b" {
		t.Errorf("payload confusion: %v %+v", err, calls)
	}
}

// strace pads the pid column (`[pid  100]`-style output becomes whitespace
// before the call name); the head pattern accepts any run of whitespace, so
// the arguments must come from the captured group, not an offset. Parser
// evidence on synthetic text only, not a real Linux fault.
func TestParseStracePaddedPidFdResolution(t *testing.T) {
	trace := strings.Join([]string{
		`  100   openat(AT_FDCWD, "` + trStage + `/ROOT.sha256", O_WRONLY|O_CREAT|O_TRUNC, 0666) = 9`,
		`  100   write(9, "x"..., 1) = -1 EIO (Input/output error) (INJECTED)`,
		`  100   close(9) = 0`,
		`  100   rename("` + trStage + `", "` + trOut + `") = 0`,
	}, "\n")
	calls, err := parseStrace(trace)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 4 {
		t.Fatalf("%d calls: %+v", len(calls), calls)
	}
	for i, want := range []string{trStage + "/ROOT.sha256", trStage + "/ROOT.sha256", trStage + "/ROOT.sha256", trStage} {
		if calls[i].Path != want {
			t.Errorf("call %d (%s) path %q, want %q", i, calls[i].Name, calls[i].Path, want)
		}
	}
	if calls[3].Dest != trOut || calls[0].PID != "100" || !calls[1].Injected || calls[1].Errno != "EIO" {
		t.Errorf("padded record fields: %+v", calls)
	}
	if err := validateInjections(calls, []wantFault{{Group: "write", Errno: "EIO", Path: trStage + "/ROOT.sha256"}}); err != nil {
		t.Errorf("padded injection: %v", err)
	}
}

// Ruling A. A recursive mkdir of an absent stage that fails with EIO is reported
// by Node (and the Go port) as the ENOENT of the follow-up stat; only EACCES,
// ENOSPC, ENOTDIR and EPERM are returned directly. The expectations below are
// hand-written literals, not a call into nodefs. The mapping is scoped to the
// precondition that the stage stayed absent; a one-time injection that a retry
// or a later ENOENT recovery could mask is not modeled.
func TestAbsentStageRecursiveMkdirText(t *testing.T) {
	for _, row := range []struct{ errno, want string }{
		{"EIO", "ENOENT: no such file or directory, mkdir '" + trStage + "'"},
		{"EACCES", "EACCES: permission denied, mkdir '" + trStage + "'"},
		{"ENOSPC", "ENOSPC: no space left on device, mkdir '" + trStage + "'"},
		{"ENOTDIR", "ENOTDIR: not a directory, mkdir '" + trStage + "'"},
		{"EPERM", "EPERM: operation not permitted, mkdir '" + trStage + "'"},
	} {
		if got := absentStageMkdirText(row.errno, trStage); got != row.want {
			t.Errorf("%s: %q, want %q", row.errno, got, row.want)
		}
	}
	// The former expectation (the injected code echoed back) is wrong for EIO.
	if got := absentStageMkdirText("EIO", trStage); got == "EIO: i/o error, mkdir '"+trStage+"'" {
		t.Errorf("the injected EIO was echoed back: %q", got)
	}
}

// Ruling B. The removal an error names is the operation actually traced:
// rmdir for rmdir(2) and for unlinkat with the AT_REMOVEDIR flag, unlink for a
// file removal, including a file whose quoted path contains "AT_REMOVEDIR".
func TestRemovalNameFollowsTheTracedOperation(t *testing.T) {
	parse := func(line string) straceCall {
		calls, err := parseStrace(line)
		if err != nil || len(calls) != 1 {
			t.Fatalf("%q: %v %d", line, err, len(calls))
		}
		return calls[0]
	}
	for _, row := range []struct{ line, want string }{
		{`5 rmdir("` + trBackup + `") = 0`, "rmdir"},
		{`5 unlinkat(AT_FDCWD, "` + trBackup + `", AT_REMOVEDIR) = 0`, "rmdir"},
		{`5 unlinkat(AT_FDCWD, "` + trBackup + `", AT_REMOVEDIR) = -1 EIO (Input/output error) (INJECTED)`, "rmdir"},
		{`5 unlinkat(AT_FDCWD, "` + trBackup + `/ROOT.sha256", 0) = 0`, "unlink"},
		{`5 unlink("` + trBackup + `/ROOT.sha256") = 0`, "unlink"},
		// A file whose name contains the flag text is still a file removal.
		{`5 unlinkat(AT_FDCWD, "` + trBackup + `/x-AT_REMOVEDIR-y", 0) = 0`, "unlink"},
		{`5 unlinkat(AT_FDCWD, "` + trBackup + `/AT_REMOVEDIR", 0) = 0`, "unlink"},
	} {
		if got := removalName(parse(row.line)); got != row.want {
			t.Errorf("%s: %q, want %q", row.line, got, row.want)
		}
	}
	// The failing call keeps its group and path for the injection proof.
	c := parse(`5 unlinkat(AT_FDCWD, "` + trBackup + `", AT_REMOVEDIR) = -1 EIO (Input/output error) (INJECTED)`)
	if err := validateInjections([]straceCall{c}, []wantFault{{Group: "unlink", Errno: "EIO", Path: trBackup}}); err != nil {
		t.Errorf("an AT_REMOVEDIR unlinkat is still the unlink group: %v", err)
	}
	if got := expectedErrorText("unlink", removalName(c), "EIO", trBackup, ""); got != "EIO: i/o error, rmdir '"+trBackup+"'" {
		t.Errorf("expected text for the failed directory removal: %q", got)
	}
}

// Two tracees (the driver and its CLI child, as in the hosted preview rows) hold
// the same descriptor number for different files. A descriptor belongs to the
// process that opened it: each call resolves to its own pid's path and one
// pid's close does not forget the other's descriptor.
func TestParseStraceFdTableIsPerPid(t *testing.T) {
	const a, b = "/r/a.html", "/r/b.html"
	calls, err := parseStrace(strings.Join([]string{
		`10 openat(AT_FDCWD, "` + a + `", O_RDONLY|O_CLOEXEC) = 4`,
		`20 openat(AT_FDCWD, "` + b + `", O_WRONLY|O_CREAT|O_TRUNC|O_CLOEXEC, 0644) = 4`,
		`10 close(4) = 0`,
		`20 write(4, "x", 1) = 1`,
		`20 close(4) = -1 EIO (Input/output error) (INJECTED)`,
		`10 close(4) = 0`,
	}, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ pid, name, path string }{
		{"10", "openat", a}, {"20", "openat", b}, {"10", "close", a},
		{"20", "write", b}, {"20", "close", b}, {"10", "close", ""},
	}
	if len(calls) != len(want) {
		t.Fatalf("%d calls, want %d", len(calls), len(want))
	}
	for i, w := range want {
		if calls[i].PID != w.pid || calls[i].Name != w.name || calls[i].Path != w.path {
			t.Errorf("call %d: pid %s %s path %q, want pid %s %s path %q", i, calls[i].PID, calls[i].Name, calls[i].Path, w.pid, w.name, w.path)
		}
	}
	// The injected close is attributed to the file its own pid opened.
	if err := validateInjections(calls, []wantFault{{Group: "close", Errno: "EIO", Path: b}}); err != nil {
		t.Errorf("injected close of pid 20: %v", err)
	}
}

// The hosted Go preview rows: the driver (pid 7042) read the preview file
// before and after the CLI child (pid 7052) opened and closed it for writing.
// A family spread over two tracees must still be refused, never reused.
func TestOrdinalRefusesDriverAndChildSharingAFamily(t *testing.T) {
	const html = "/r/out.html"
	calls, err := parseStrace(strings.Join([]string{
		`7042 openat(AT_FDCWD, "` + html + `", O_RDONLY|O_CLOEXEC) = 4`,
		`7042 close(4) = 0`,
		`7052 openat(AT_FDCWD, "` + html + `", O_WRONLY|O_CREAT|O_TRUNC|O_CLOEXEC, 0644) = 4`,
		`7052 close(4) = 0`,
		`7042 openat(AT_FDCWD, "` + html + `", O_RDONLY|O_CLOEXEC) = 4`,
		`7042 close(4) = 0`,
	}, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	idx, err := findCall(calls, "open", html, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ordinalOf(calls, "open", idx); err == nil || !strings.Contains(err.Error(), "span threads") {
		t.Errorf("a two-tracee open family was accepted: %v", err)
	}
	// With only the child traced (no driver inventory) the position is its own.
	only := []straceCall{calls[2], calls[3]}
	if n, err := ordinalOf(only, "open", 0); err != nil || n != 1 {
		t.Errorf("child-only ordinal = %d (%v), want 1", n, err)
	}
	if n, err := ordinalOf(only, "close", 1); err != nil || n != 1 {
		t.Errorf("child-only close ordinal = %d (%v), want 1", n, err)
	}
}

func TestParseStraceMergesUnfinishedAndRejectsMalformed(t *testing.T) {
	merged := strings.Join([]string{
		`200 openat(AT_FDCWD, "/x", O_RDONLY) = 4`,
		`200 write(4, "abc", 3 <unfinished ...>`,
		`201 getpid() = 201`,
		`200 <... write resumed>) = 3`,
	}, "\n")
	calls, err := parseStrace(merged)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range calls {
		if c.Name == "write" && c.Path == "/x" && c.Ret == "3" {
			found = true
		}
	}
	if !found {
		t.Errorf("unfinished/resumed write not merged: %+v", calls)
	}
	for name, bad := range map[string]string{
		"unterminated": `200 write(4, "abc", 3 <unfinished ...>`,
		"no-result":    `200 rename("/a", "/b")`,
		"orphan":       `200 <... write resumed>) = 3`,
	} {
		if _, err := parseStrace(bad); err == nil {
			t.Errorf("%s: parsed without error", name)
		}
	}
}

func TestClassifyRenames(t *testing.T) {
	line := func(pid, src, dst, tail string) string {
		return pid + ` rename("` + src + `", "` + dst + `") = ` + tail
	}
	ok := func(trace string) ([]string, string, error) {
		calls, err := parseStrace(trace)
		if err != nil {
			t.Fatal(err)
		}
		return classifyRenames(calls, trOut, trStage, trBackup)
	}
	kinds, pid, err := ok(line("9", trOut, trBackup, "0") + "\n" + line("9", trStage, trOut, "0"))
	if err != nil || strings.Join(kinds, ",") != "move-old,publish" || pid != "9" {
		t.Errorf("replace: %v %v %s", kinds, err, pid)
	}
	kinds, _, err = ok(line("9", trOut, trBackup, "0") + "\n" + line("9", trStage, trOut, "-1 EIO (Input/output error) (INJECTED)") + "\n" + line("9", trBackup, trOut, "-1 EIO (Input/output error) (INJECTED)"))
	if err != nil || strings.Join(kinds, ",") != "move-old,publish,restore" {
		t.Errorf("rollback: %v %v", kinds, err)
	}
	if kinds, _, err = ok(line("9", trStage, trOut, "0")); err != nil || strings.Join(kinds, ",") != "publish" {
		t.Errorf("fresh: %v %v", kinds, err)
	}
	// A rename sequence split over two threads is an unexpected layout.
	if _, _, err = ok(line("9", trOut, trBackup, "0") + "\n" + line("10", trStage, trOut, "0")); err == nil || !strings.Contains(err.Error(), "threads") {
		t.Errorf("multi-thread layout accepted: %v", err)
	}
	// A rename that is none of the three, or one from another path pair.
	if _, _, err = ok(line("9", trOut, "/r/elsewhere", "0")); err == nil {
		t.Error("unexpected destination accepted")
	}
	if _, _, err = ok(line("9", "/r/unrelated", "/r/other", "0")); err != nil {
		t.Errorf("unrelated rename must be ignored: %v", err)
	}
}

func TestValidateInjections(t *testing.T) {
	parse := func(trace string) []straceCall {
		calls, err := parseStrace(trace)
		if err != nil {
			t.Fatal(err)
		}
		return calls
	}
	renameFail := parse(`5 rename("` + trStage + `", "` + trOut + `") = -1 EIO (Input/output error) (INJECTED)`)
	want := wantFault{Group: "rename", Errno: "EIO", Path: trStage, Dest: trOut}
	if err := validateInjections(renameFail, []wantFault{want}); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(w *wantFault){
		"wrong group": func(w *wantFault) { w.Group = "unlink" },
		"wrong errno": func(w *wantFault) { w.Errno = "ENOSPC" },
		"wrong path":  func(w *wantFault) { w.Path = trBackup },
		"wrong dest":  func(w *wantFault) { w.Dest = trBackup },
	} {
		w := want
		mutate(&w)
		if err := validateInjections(renameFail, []wantFault{w}); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if err := validateInjections(nil, []wantFault{want}); err == nil {
		t.Error("zero injections accepted")
	}
	if err := validateInjections(renameFail, nil); err == nil {
		t.Error("an unexpected injection was accepted")
	}
	two := parse(renameFail[0].Line + "\n" + `5 rename("` + trBackup + `", "` + trOut + `") = -1 EIO (Input/output error) (INJECTED)`)
	if err := validateInjections(two, []wantFault{want}); err == nil {
		t.Error("an extra injected call was accepted")
	}
	if err := validateInjections(two, []wantFault{want, {Group: "rename", Errno: "EIO", Path: trBackup, Dest: trOut}}); err != nil {
		t.Errorf("publish+restore pair: %v", err)
	}
	fd := parse(`5 openat(AT_FDCWD, "` + trStage + `/ROOT.sha256", O_WRONLY|O_CREAT|O_TRUNC, 0666) = 8` + "\n" + `5 close(8) = -1 EIO (Input/output error) (INJECTED)`)
	if err := validateInjections(fd, []wantFault{{Group: "close", Errno: "EIO", Path: trStage + "/ROOT.sha256"}}); err != nil {
		t.Errorf("fd-resolved close: %v", err)
	}
	if err := validateInjections(fd, []wantFault{{Group: "close", Errno: "EIO", Path: trStage + "/manifest.json"}}); err == nil {
		t.Error("close of a different file accepted")
	}
	// The family members of one group are interchangeable, other groups are not.
	renameat := parse(`5 renameat2(AT_FDCWD, "` + trStage + `", AT_FDCWD, "` + trOut + `", 0) = -1 EIO (Input/output error) (INJECTED)`)
	if err := validateInjections(renameat, []wantFault{want}); err != nil {
		t.Errorf("renameat2 is in the rename group: %v", err)
	}
}

func TestFirstCallAndExpectedText(t *testing.T) {
	calls, err := parseStrace(strings.Join([]string{
		`5 unlinkat(AT_FDCWD, "/r/other/x", 0) = 0`,
		`5 unlinkat(AT_FDCWD, "` + trBackup + `/ROOT.sha256", 0) = 0`,
		`5 rmdir("` + trBackup + `") = 0`,
	}, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := firstCallUnder(calls, "unlink", trBackup)
	if err != nil || c.Name != "unlinkat" || c.Path != trBackup+"/ROOT.sha256" {
		t.Errorf("first removal: %v %+v", err, c)
	}
	if _, err := firstCallUnder(calls, "unlink", "/r/absent"); err == nil {
		t.Error("an unobserved operation was located")
	}
	if _, err := firstCallUnder(calls, "mkdir", trBackup); err == nil {
		t.Error("an unobserved group was located")
	}
	for _, row := range []struct{ group, name, errno, path, dest, want string }{
		{"rename", "rename", "EIO", trStage, trOut, "EIO: i/o error, rename '" + trStage + "' -> '" + trOut + "'"},
		{"mkdir", "mkdir", "EIO", trStage, "", "EIO: i/o error, mkdir '" + trStage + "'"},
		{"open", "openat", "EACCES", trStage + "/manifest.json", "", "EACCES: permission denied, open '" + trStage + "/manifest.json'"},
		{"write", "write", "ENOSPC", trStage + "/manifest.json", "", "ENOSPC: no space left on device, write"},
		{"close", "close", "EIO", trStage + "/manifest.json", "", "EIO: i/o error, close"},
		{"unlink", "unlinkat", "EIO", trBackup + "/ROOT.sha256", "", "EIO: i/o error, unlink '" + trBackup + "/ROOT.sha256'"},
		{"unlink", "rmdir", "EIO", trBackup, "", "EIO: i/o error, rmdir '" + trBackup + "'"},
	} {
		if got := expectedErrorText(row.group, row.name, row.errno, row.path, row.dest); got != row.want {
			t.Errorf("%s/%s: %q, want %q", row.group, row.name, got, row.want)
		}
	}
}

// ---- helpers consumed by demo_artifacts_fault_test.go (linux) ----

// demoGroups are the syscall families of writerfault_live_test.go's faultCalls
// (kept here so the helpers compile and are unit-tested off Linux).
var demoGroups = map[string][]string{
	"open":   {"open", "openat", "openat2"},
	"write":  {"write", "writev", "pwrite64", "pwritev", "pwritev2"},
	"rename": {"rename", "renameat", "renameat2"},
	"unlink": {"unlink", "unlinkat", "rmdir"},
	"close":  {"close"},
	"mkdir":  {"mkdir", "mkdirat"},
}

func inGroup(group, name string) bool {
	for _, n := range demoGroups[group] {
		if n == name {
			return true
		}
	}
	return false
}

// straceCall is one traced syscall with its first path argument (a path-less
// fd call such as write or close takes the path its descriptor was opened
// from), a rename's second path, and its result.
type straceCall struct {
	PID, Name, Path, Dest, Ret, Errno, Line string
	Injected                                bool
}

var (
	straceHead     = regexp.MustCompile(`^\s*(\d+)\s+([a-z0-9_]+)\((.*)$`)
	straceResumed  = regexp.MustCompile(`^\s*(\d+)\s+<\.\.\. ([a-z0-9_]+) resumed>(.*)$`)
	straceResult   = regexp.MustCompile(`\) += (-?\d+|\?)(?: (E[A-Z0-9]+) \([^)]*\))?(?: \(INJECTED\))?\s*$`)
	straceQuoted   = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)
	straceFirstInt = regexp.MustCompile(`^\s*(\d+)`)
)

var fdCalls = map[string]bool{"write": true, "writev": true, "pwrite64": true, "pwritev": true, "pwritev2": true, "close": true, "fsync": true, "fdatasync": true}

// parseStrace parses `strace -f -qq` output. Signal and exit records are
// skipped; a line that is none of a complete call, an unfinished call or its
// resumption is an error, as is an unfinished call that never resumes.
func parseStrace(trace string) ([]straceCall, error) {
	var out []straceCall
	pending := map[string]string{} // pid+name -> text before <unfinished ...>
	// A descriptor belongs to the tracee (pid) that opened it.
	fds := map[string]string{}
	for n, line := range strings.Split(trace, "\n") {
		if line == "" || strings.Contains(line, " --- ") || strings.Contains(line, " +++ ") {
			continue
		}
		if m := straceResumed.FindStringSubmatch(line); m != nil {
			head, ok := pending[m[1]+" "+m[2]]
			if !ok {
				return nil, fmt.Errorf("trace line %d: resumed %s with no unfinished call: %q", n+1, m[2], line)
			}
			delete(pending, m[1]+" "+m[2])
			line = head + m[3]
		} else if i := strings.Index(line, " <unfinished ...>"); i >= 0 {
			m := straceHead.FindStringSubmatch(line)
			if m == nil {
				return nil, fmt.Errorf("trace line %d: unparsable unfinished call: %q", n+1, line)
			}
			pending[m[1]+" "+m[2]] = line[:i]
			continue
		}
		m := straceHead.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("trace line %d: not a syscall record: %q", n+1, line)
		}
		res := straceResult.FindStringSubmatchIndex(line)
		if res == nil {
			return nil, fmt.Errorf("trace line %d: no result: %q", n+1, line)
		}
		call := straceCall{PID: m[1], Name: m[2], Line: line, Ret: line[res[2]:res[3]], Injected: strings.HasSuffix(strings.TrimSpace(line), "(INJECTED)")}
		if res[4] >= 0 {
			call.Errno = line[res[4]:res[5]]
		}
		// m[3] is everything after the call's "(": the arguments are that
		// capture up to the result, with no offset arithmetic on the padding.
		args := strings.TrimLeft(m[3][:res[0]-(len(line)-len(m[3]))], " ")
		if fdCalls[call.Name] {
			if f := straceFirstInt.FindStringSubmatch(args); f != nil {
				key := m[1] + " " + f[1]
				call.Path = fds[key]
				if call.Name == "close" {
					delete(fds, key)
				}
			}
		} else if q := straceQuoted.FindAllStringSubmatch(args, 2); len(q) > 0 {
			call.Path = q[0][1]
			if inGroup("rename", call.Name) && len(q) == 2 {
				call.Dest = q[1][1]
			}
			if inGroup("open", call.Name) && call.Errno == "" && call.Ret != "?" && !strings.HasPrefix(call.Ret, "-") {
				fds[m[1]+" "+call.Ret] = call.Path
			}
		}
		out = append(out, call)
	}
	if len(pending) != 0 {
		return nil, fmt.Errorf("trace ends with %d unfinished call(s)", len(pending))
	}
	return out, nil
}

// classifyRenames names, in trace order, the renames of the replacement
// protocol: move-old (output -> backup), publish (stage -> output) and restore
// (backup -> output). Renames touching none of the three paths are ignored; a
// protocol rename between other paths, or a protocol sequence spread over
// several threads, is rejected rather than mislabeled.
func classifyRenames(calls []straceCall, out, stage, backup string) (kinds []string, pid string, err error) {
	for _, c := range calls {
		if !inGroup("rename", c.Name) {
			continue
		}
		touches := func(p string) bool { return p == out || p == stage || p == backup }
		if !touches(c.Path) && !touches(c.Dest) {
			continue
		}
		var kind string
		switch {
		case c.Path == out && c.Dest == backup:
			kind = "move-old"
		case c.Path == stage && c.Dest == out:
			kind = "publish"
		case c.Path == backup && c.Dest == out:
			kind = "restore"
		default:
			return nil, "", fmt.Errorf("unexpected rename %q -> %q: %s", c.Path, c.Dest, c.Line)
		}
		if pid != "" && c.PID != pid {
			return nil, "", fmt.Errorf("rename sequence spans threads %s and %s (when= counts per thread): %s", pid, c.PID, c.Line)
		}
		pid = c.PID
		kinds = append(kinds, kind)
	}
	return kinds, pid, nil
}

// wantFault is one intended injection: the syscall group, errno and the exact
// path (and, for a rename, destination) that must have been failed.
type wantFault struct{ Group, Errno, Path, Dest string }

// validateInjections requires the (INJECTED) records of calls to be exactly
// the wanted faults: each matched once on group, errno, path and destination,
// with no unexpected or missing record.
func validateInjections(calls []straceCall, want []wantFault) error {
	var injected []straceCall
	for _, c := range calls {
		if c.Injected {
			injected = append(injected, c)
		}
	}
	if len(injected) != len(want) {
		return fmt.Errorf("%d injected call(s), want %d: %v", len(injected), len(want), injectedLines(injected))
	}
	used := make([]bool, len(injected))
	for _, w := range want {
		found := false
		for i, c := range injected {
			if used[i] || !inGroup(w.Group, c.Name) || c.Errno != w.Errno || c.Path != w.Path || (w.Dest != "" && c.Dest != w.Dest) {
				continue
			}
			used[i], found = true, true
			break
		}
		if !found {
			return fmt.Errorf("no injected %s call with %s on %q (-> %q); injected: %v", w.Group, w.Errno, w.Path, w.Dest, injectedLines(injected))
		}
	}
	return nil
}

func injectedLines(calls []straceCall) []string {
	var out []string
	for _, c := range calls {
		out = append(out, c.Line)
	}
	return out
}

// firstCallUnder is the first call of group whose path is dir or below it.
func firstCallUnder(calls []straceCall, group, dir string) (straceCall, error) {
	for _, c := range calls {
		if inGroup(group, c.Name) && (c.Path == dir || strings.HasPrefix(c.Path, dir+"/")) {
			return c, nil
		}
	}
	return straceCall{}, fmt.Errorf("no %s call under %s was observed", group, dir)
}

// findCall is the first call of group on path (and destination, when given).
func findCall(calls []straceCall, group, path, dest string) (int, error) {
	for i, c := range calls {
		if inGroup(group, c.Name) && c.Path == path && (dest == "" || c.Dest == dest) {
			return i, nil
		}
	}
	return -1, fmt.Errorf("no %s call on %q (-> %q) was observed", group, path, dest)
}

// ordinalOf is the 1-based position of calls[idx] among the calls of its own
// group's family (what strace's when= counts, under the same -P filter), and
// fails when that family is spread over more than one thread.
func ordinalOf(calls []straceCall, group string, idx int) (int, error) {
	n := 0
	target := calls[idx]
	pos := 0
	for i, c := range calls {
		if !inGroup(group, c.Name) {
			continue
		}
		if c.PID != target.PID {
			return 0, fmt.Errorf("%s calls span threads %s and %s (when= counts per thread): %s", group, target.PID, c.PID, c.Line)
		}
		n++
		if i == idx {
			pos = n
		}
	}
	return pos, nil
}

// absentStageMkdirText is the message of a recursive mkdir whose target stage
// stayed absent after the injected failure errno. Node's MKDirpSync (and the Go
// port) return EACCES, ENOSPC, ENOTDIR and EPERM directly; any other code,
// EIO included, makes it stat the still-absent path and report that stat's
// ENOENT. The table is hand-written from that observed behavior and applies
// only under the absent-stage precondition; it is not a general model.
func absentStageMkdirText(errno, path string) string {
	reported := "ENOENT"
	switch errno {
	case "EACCES", "ENOSPC", "ENOTDIR", "EPERM":
		reported = errno
	}
	return expectedErrorText("mkdir", "mkdir", reported, path, "")
}

// removalName is the operation a removal error names, read from the traced
// call: rmdir(2), or unlinkat whose flags argument (the last argument, after
// the quoted path) carries AT_REMOVEDIR. A path that merely contains the text
// AT_REMOVEDIR is a file removal.
func removalName(c straceCall) string {
	if c.Name == "rmdir" {
		return "rmdir"
	}
	if c.Name == "unlinkat" {
		open := strings.Index(c.Line, "(")
		res := straceResult.FindStringIndex(c.Line)
		if open >= 0 && res != nil && open < res[0] {
			args := c.Line[open+1 : res[0]]
			// Drop every quoted string (the path), then look at what is left.
			unquoted := straceQuoted.ReplaceAllString(args, `""`)
			if strings.Contains(unquoted, "AT_REMOVEDIR") {
				return "rmdir"
			}
		}
	}
	return "unlink"
}

// expectedErrorText is Node's message for a failed call, as the writer's
// caller sees it (write and close failures carry no path).
func expectedErrorText(group, name, errno, path, dest string) string {
	head := errno + ": " + nodefs.Describe(errno) + ", "
	switch group {
	case "rename":
		return head + "rename '" + path + "' -> '" + dest + "'"
	case "write":
		return head + "write"
	case "close":
		return head + "close"
	case "unlink":
		if name == "rmdir" {
			return head + "rmdir '" + path + "'"
		}
		return head + "unlink '" + path + "'"
	default:
		return head + group + " '" + path + "'"
	}
}
