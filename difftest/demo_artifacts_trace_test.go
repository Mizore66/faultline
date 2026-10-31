//go:build faultline_test

package difftest

import (
	"fmt"
	"regexp"
	"strconv"
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

// ---- positively terminated unavailable syscall entry (detach-lifecycle repair) ----
//
// With `strace -f -q`, a tracee whose syscall-entry registers cannot be read is
// printed as `PID  ???( <unfinished ...>` (the next record from any tid closes
// the open line) and, when that tid ends normally, `PID +++ exited with N +++`.
// The unavailable entry is a non-call: it never took the path filter or an
// injection. It is supported only with that positive normal exit; everything
// else stays an error. These tests pin the contract through parseStrace and the
// unchanged validators; they define no new helper.

const lcHTML = "/r/out.html"

func lcTrace(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

const (
	lcOpen  = `8408  openat(AT_FDCWD, "` + lcHTML + `", O_WRONLY|O_CREAT|O_TRUNC|O_CLOEXEC, 0644) = 4`
	lcClose = `8408  close(4)                          = -1 EIO (Input/output error) (INJECTED)`
	lcUnk   = `8411  ???( <unfinished ...>`
	lcChld  = `8409  --- SIGCHLD {si_signo=SIGCHLD, si_code=CLD_EXITED, si_pid=8408, si_uid=1001, si_status=1, si_utime=1, si_stime=1} ---`
)

func TestParseStraceAcceptsUnavailableEntryWithPositiveNormalExit(t *testing.T) {
	trace := lcTrace(
		lcOpen,
		lcUnk,
		lcClose,
		lcChld,
		`8408 +++ exited with 1 +++`,
		`8411 +++ exited with 1 +++`,
		`8409 +++ exited with 0 +++`,
	)
	calls, err := parseStrace(trace)
	if err != nil {
		t.Fatalf("a positively terminated unavailable entry was rejected: %v", err)
	}
	// Only the two actual calls are visible to every helper.
	if len(calls) != 2 || calls[0].Name != "openat" || calls[1].Name != "close" {
		t.Fatalf("calls = %+v, want exactly openat then close", calls)
	}
	for _, c := range calls {
		if c.PID != "8408" || c.Path != lcHTML {
			t.Errorf("call %s: pid %q path %q, want the 8408 target", c.Name, c.PID, c.Path)
		}
	}
	want := []wantFault{{Group: "close", Errno: "EIO", Path: lcHTML}}
	if err := validateInjections(calls, want); err != nil {
		t.Errorf("the exact injection no longer validates: %v", err)
	}
	if idx, err := findCall(calls, "open", lcHTML, ""); err != nil || idx != 0 {
		t.Errorf("open located at %d (%v), want 0", idx, err)
	} else if n, err := ordinalOf(calls, "open", idx); err != nil || n != 1 {
		t.Errorf("open ordinal %d (%v), want 1", n, err)
	}
	if n, err := ordinalOf(calls, "close", 1); err != nil || n != 1 {
		t.Errorf("close ordinal %d (%v), want 1", n, err)
	}
}

// Normal exit notices alone (what -q adds for every tid) are lifecycle records,
// not syscalls, and a trace may consist of calls plus exits with no unknown.
func TestParseStraceNormalExitNoticesAreNotCalls(t *testing.T) {
	calls, err := parseStrace(lcTrace(lcOpen, lcClose, `8408 +++ exited with 1 +++`, `8409 +++ exited with 0 +++`))
	if err != nil {
		t.Fatalf("normal exit notices rejected: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("%d calls, want 2", len(calls))
	}
	for _, code := range []string{"0", "1", "127", "255"} {
		if _, err := parseStrace(lcTrace(lcOpen, `8408 +++ exited with `+code+` +++`)); err != nil {
			t.Errorf("exit code %s rejected: %v", code, err)
		}
	}
}

// Accepting the lifecycle never relaxes the injection proof.
func TestUnavailableEntryDoesNotMaskMissingOrExtraInjections(t *testing.T) {
	terminated := func(extra ...string) string {
		lines := append([]string{lcOpen, lcUnk}, extra...)
		return lcTrace(append(lines, `8408 +++ exited with 1 +++`, `8411 +++ exited with 1 +++`)...)
	}
	parse := func(trace string) []straceCall {
		t.Helper()
		calls, err := parseStrace(trace)
		if err != nil {
			t.Fatalf("a positively terminated unavailable entry was rejected: %v", err)
		}
		return calls
	}
	want := []wantFault{{Group: "close", Errno: "EIO", Path: lcHTML}}
	// Missing: the target close was never injected.
	if err := validateInjections(parse(terminated(`8408  close(4) = 0`)), want); err == nil {
		t.Error("a missing injection was accepted next to an unavailable entry")
	}
	// Extra: a second injected record is not hidden by the lifecycle record.
	extra := parse(terminated(lcClose, `8408  close(4) = -1 EIO (Input/output error) (INJECTED)`))
	if err := validateInjections(extra, want); err == nil {
		t.Error("an extra injection was accepted next to an unavailable entry")
	}
	// The unavailable entry itself is never an injection.
	if err := validateInjections(parse(terminated()), want); err == nil {
		t.Error("an unavailable entry satisfied a wanted injection")
	}
	if err := validateInjections(parse(terminated(lcClose)), nil); err == nil {
		t.Error("an unexpected injection was accepted")
	}
}

// Lifecycle records are anchored whole-line records. A syscall whose quoted
// path merely contains the text of one stays a syscall (the old substring skip
// dropped such lines silently).
func TestParseStraceQuotedLifecycleTextInPathStaysASyscall(t *testing.T) {
	calls, err := parseStrace(lcTrace(
		`5  openat(AT_FDCWD, "/r/odd +++ name", O_RDONLY|O_CLOEXEC) = 4`,
		`5  openat(AT_FDCWD, "/r/odd --- name", O_RDONLY|O_CLOEXEC) = 5`,
		`5  openat(AT_FDCWD, "/r/ +++ exited with 0 +++", O_RDONLY|O_CLOEXEC) = 6`,
		`5  close(5)                          = 0`,
	))
	if err != nil {
		t.Fatal(err)
	}
	wantPath := []string{"/r/odd +++ name", "/r/odd --- name", "/r/ +++ exited with 0 +++", "/r/odd --- name"}
	if len(calls) != 4 {
		t.Fatalf("%d calls, want 4: %+v", len(calls), calls)
	}
	for i, p := range wantPath {
		if calls[i].Path != p {
			t.Errorf("call %d path %q, want %q", i, calls[i].Path, p)
		}
	}
}

// Everything outside the one supported shape fails closed. The first row is the
// exact trace the hosted job produced; it must stay rejected.
func TestParseStraceRejectsUnsupportedLifecycleRecords(t *testing.T) {
	exit1 := `8411 +++ exited with 1 +++`
	for name, trace := range map[string]string{
		"original hosted detached record":            lcTrace(lcOpen, lcClose, `8411  ???( <detached ...>`, lcChld),
		"detached record with a synthetic exit":      lcTrace(lcOpen, `8411  ???( <detached ...>`, lcClose, exit1),
		"unavailable entry without any terminal":     lcTrace(lcOpen, lcUnk, lcClose, lcChld),
		"terminal only for another tid":              lcTrace(lcOpen, lcUnk, lcClose, `8408 +++ exited with 1 +++`),
		"terminal before the unknown entry":          lcTrace(lcOpen, exit1, lcUnk, lcClose),
		"duplicate terminal":                         lcTrace(lcOpen, lcUnk, lcClose, exit1, exit1),
		"two unknown entries for one tid":            lcTrace(lcOpen, lcUnk, lcUnk, lcClose, exit1),
		"call by the same tid after the unknown":     lcTrace(lcOpen, lcUnk, `8411  close(4) = 0`, exit1),
		"signal by the same tid after the unknown":   lcTrace(lcOpen, lcUnk, `8411  --- SIGCHLD {si_signo=SIGCHLD, si_code=CLD_EXITED, si_pid=8408, si_uid=1001, si_status=1} ---`, exit1),
		"resume by the same tid after the unknown":   lcTrace(lcOpen, lcUnk, `8411  <... close resumed>) = 0`, exit1),
		"record after the terminal":                  lcTrace(lcOpen, lcUnk, exit1, `8411  close(4) = 0`),
		"killed placeholder instead of exit":         lcTrace(lcOpen, lcUnk, lcClose, `8411 +++ killed by SIGKILL +++`),
		"killed notice without any unknown":          lcTrace(lcOpen, lcClose, `8408 +++ killed by SIGKILL +++`),
		"killed with core dump":                      lcTrace(lcOpen, `8408 +++ killed by SIGSEGV (core dumped) +++`),
		"named unfinished call even when terminated": lcTrace(lcOpen, `8411  write(4, "x", 1 <unfinished ...>`, lcClose, exit1),
		"named detached call even when terminated":   lcTrace(lcOpen, `8411  write(4, "x", 1 <detached ...>`, lcClose, exit1),
		"unknown after a pending named call":         lcTrace(lcOpen, `8411  write(4, "x", 1 <unfinished ...>`, lcUnk, lcClose, exit1),
		"unavailable entry with extra text":          lcTrace(lcOpen, `8411  ???( <unfinished ...> junk`, lcClose, exit1),
		"unavailable entry with arguments":           lcTrace(lcOpen, `8411  ???(4, 5 <unfinished ...>`, lcClose, exit1),
		"unavailable entry with a result":            lcTrace(lcOpen, `8411  ???() = -1 ENOSYS (Function not implemented)`, lcClose, exit1),
		"unavailable entry without a tid":            lcTrace(lcOpen, `  ???( <unfinished ...>`, lcClose),
		"exit code out of range":                     lcTrace(lcOpen, lcUnk, lcClose, `8411 +++ exited with 256 +++`),
		"negative exit code":                         lcTrace(lcOpen, lcUnk, lcClose, `8411 +++ exited with -1 +++`),
		"non-decimal exit code":                      lcTrace(lcOpen, lcUnk, lcClose, `8411 +++ exited with 0x1 +++`),
		"exit notice with trailing text":             lcTrace(lcOpen, lcUnk, lcClose, `8411 +++ exited with 1 +++ extra`),
		"exit notice without a tid":                  lcTrace(lcOpen, lcClose, `+++ exited with 1 +++`),
		"standalone malformed exit notice":           lcTrace(lcOpen, lcClose, `8408 +++ exited with +++`),
	} {
		t.Run(name, func(t *testing.T) {
			if calls, err := parseStrace(trace); err == nil {
				t.Fatalf("accepted unsupported lifecycle trace (%d calls):\n%s", len(calls), trace)
			}
		})
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

// Lifecycle records of `strace -f -q`, each an anchored whole-line record (a
// syscall whose quoted path merely contains such text is still a syscall).
var (
	straceSignal  = regexp.MustCompile(`^\s*(\d+)\s+--- SIG[A-Z0-9+-]+( \{.*\})? ---\s*$`)
	straceExit    = regexp.MustCompile(`^\s*(\d+)\s+\+\+\+ exited with (\d{1,3}) \+\+\+\s*$`)
	straceKilled  = regexp.MustCompile(`^\s*(\d+)\s+\+\+\+ killed by `)
	straceUnavail = regexp.MustCompile(`^\s*(\d+)\s+\?\?\?\( <unfinished \.\.\.>\s*$`)
)

// parseStrace parses `strace -f -q` output into the actual syscalls. See
// parseStraceLifecycle for the accepted lifecycle records.
func parseStrace(trace string) ([]straceCall, error) {
	calls, _, err := parseStraceLifecycle(trace)
	return calls, err
}

// parseStraceLifecycle returns the calls and one note per accepted unavailable
// syscall entry. Signal records (SIG... {...}) and normal exit notices
// (`PID +++ exited with N +++`, N 0..255) are lifecycle records, not calls. A
// tracee whose syscall-entry registers strace could not read is printed
// `PID ???( <unfinished ...>`: that stop never reached the path filter or the
// injection, so it is a non-call. It is accepted only for a tid that has no
// pending named call, appears once, makes no other record, and later has
// exactly one normal exit notice; otherwise the trace is an error. Killed
// notices, detached records, named detached or never-resumed calls, and every
// other line are errors.
func parseStraceLifecycle(trace string) ([]straceCall, []string, error) {
	var out []straceCall
	var notes []string
	pending := map[string]string{} // pid+name -> text before <unfinished ...>
	unavailable := map[string]bool{}
	terminated := map[string]bool{}
	pendingFor := func(pid string) bool {
		for k := range pending {
			if strings.HasPrefix(k, pid+" ") {
				return true
			}
		}
		return false
	}
	// A tid that is unavailable or already terminated makes no further record.
	quiet := func(n int, pid, line string) error {
		if unavailable[pid] || terminated[pid] {
			return fmt.Errorf("trace line %d: record by tid %s after its unavailable entry or termination: %q", n+1, pid, line)
		}
		return nil
	}
	// A descriptor belongs to the tracee (pid) that opened it.
	fds := map[string]string{}
	for n, line := range strings.Split(trace, "\n") {
		if line == "" {
			continue
		}
		if m := straceSignal.FindStringSubmatch(line); m != nil {
			if err := quiet(n, m[1], line); err != nil {
				return nil, nil, err
			}
			continue
		}
		if m := straceExit.FindStringSubmatch(line); m != nil {
			code, _ := strconv.Atoi(m[2])
			if code > 255 {
				return nil, nil, fmt.Errorf("trace line %d: exit code out of range: %q", n+1, line)
			}
			if terminated[m[1]] {
				return nil, nil, fmt.Errorf("trace line %d: second terminal record for tid %s: %q", n+1, m[1], line)
			}
			if pendingFor(m[1]) {
				return nil, nil, fmt.Errorf("trace line %d: tid %s ended with a named call still unfinished: %q", n+1, m[1], line)
			}
			terminated[m[1]] = true
			if unavailable[m[1]] {
				notes = append(notes, fmt.Sprintf("tid %s: unavailable syscall entry (???( <unfinished ...>), then normal exit %s", m[1], m[2]))
			}
			continue
		}
		if straceKilled.MatchString(line) {
			return nil, nil, fmt.Errorf("trace line %d: killed notices are not supported: %q", n+1, line)
		}
		if m := straceUnavail.FindStringSubmatch(line); m != nil {
			if unavailable[m[1]] || terminated[m[1]] || pendingFor(m[1]) {
				return nil, nil, fmt.Errorf("trace line %d: unavailable entry for tid %s after a prior unavailable entry, termination or pending named call: %q", n+1, m[1], line)
			}
			unavailable[m[1]] = true
			continue
		}
		if m := straceResumed.FindStringSubmatch(line); m != nil {
			if err := quiet(n, m[1], line); err != nil {
				return nil, nil, err
			}
		} else if m := straceHead.FindStringSubmatch(line); m != nil {
			if err := quiet(n, m[1], line); err != nil {
				return nil, nil, err
			}
		}
		if m := straceResumed.FindStringSubmatch(line); m != nil {
			head, ok := pending[m[1]+" "+m[2]]
			if !ok {
				return nil, nil, fmt.Errorf("trace line %d: resumed %s with no unfinished call: %q", n+1, m[2], line)
			}
			delete(pending, m[1]+" "+m[2])
			line = head + m[3]
		} else if i := strings.Index(line, " <unfinished ...>"); i >= 0 {
			m := straceHead.FindStringSubmatch(line)
			if m == nil {
				return nil, nil, fmt.Errorf("trace line %d: unparsable unfinished call: %q", n+1, line)
			}
			pending[m[1]+" "+m[2]] = line[:i]
			continue
		}
		m := straceHead.FindStringSubmatch(line)
		if m == nil {
			return nil, nil, fmt.Errorf("trace line %d: not a syscall record: %q", n+1, line)
		}
		res := straceResult.FindStringSubmatchIndex(line)
		if res == nil {
			return nil, nil, fmt.Errorf("trace line %d: no result: %q", n+1, line)
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
		return nil, nil, fmt.Errorf("trace ends with %d unfinished call(s)", len(pending))
	}
	for pid := range unavailable {
		if !terminated[pid] {
			return nil, nil, fmt.Errorf("tid %s has an unavailable syscall entry and no normal exit record", pid)
		}
	}
	return out, notes, nil
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
