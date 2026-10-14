//go:build linux

package difftest

import (
	"bufio"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// writerFault fails the first call of one syscall group on one writer path
// with errno (spec 3.2). glob is a path.Match pattern over the path relative
// to the case root (ws/..., tmp/...); the first path a fault-free TS run of
// the case names that matches it is the one faulted, on both sides.
type writerFault struct {
	caseID, label, glob, call, errno string
}

func (f writerFault) name() string { return f.caseID + "/" + f.label + "/" + f.call + "-" + f.errno }

// faultCalls are the syscalls of each group: libuv and Go reach a file
// through different members of a family (write or pwrite64, rmdir for an
// empty directory, renameat2 on newer libcs).
var faultCalls = map[string]string{
	"open":   "open,openat,openat2",
	"write":  "write,writev,pwrite64,pwritev,pwritev2",
	"rename": "rename,renameat,renameat2",
	"link":   "link,linkat",
	"unlink": "unlink,unlinkat,rmdir",
	"close":  "close",
}

// discoveryCalls are traced in the fault-free run that names the paths.
const discoveryCalls = "open,openat,openat2,rename,renameat,renameat2,link,linkat,unlink,unlinkat,rmdir,mkdir,mkdirat"

// writerFaultPaths are the writers of spec 3.2, each with the syscall
// groups that reach its path in a fault-free run (a group that never does
// injects nothing, which the test rejects).
var writerFaultPaths = []struct {
	caseID, label, glob string
	calls               []string
}{
	{"propose-input", "witness-record", "ws/.faultline/witnesses/proposals/*.json", []string{"open", "write", "close"}},
	{"policy-freeze", "policy", "ws/.faultline/witnesses/standing-policies/*.json", []string{"open", "write", "close"}},
	{"ratify", "ratification", "ws/.faultline/witnesses/ratifications/*.json", []string{"open", "write", "close"}},
	{"freeze", "binding", "ws/.faultline/incidents/bindings/*.json", []string{"open", "write", "close"}},
	{"ledger-bind", "ledger-staged", "ws/.bound-new.json.<PID>.*.tmp", []string{"open", "write", "close", "rename"}},
	{"minimize-git", "minimization-temp", "ws/.faultline/minimizations/.*.faultline-git-minimization.tmp", []string{"open", "write", "close", "link", "unlink"}},
	{"investigate-two-states", "stage-manifest", "ws/.faultline/git-proof-bundles/.*.staging-*/manifest.json", []string{"open", "write", "close"}},
	{"investigate-two-states", "stage-root", "ws/.faultline/git-proof-bundles/.*.staging-*/ROOT.sha256", []string{"open", "write", "close"}},
	{"investigate-two-states", "stage-rename", "ws/.faultline/git-proof-bundles/.*.staging-*", []string{"rename"}},
	{"investigate-two-states", "overlay-staged", "tmp/*/worktrees/*/.faultline-overlay-*", []string{"open", "write", "close", "rename"}},
	{"commit-proof-preview", "preview-html", "ws/docs/*.html", []string{"open", "write", "close"}},
}

func writerFaultMatrix() []writerFault {
	var out []writerFault
	for _, p := range writerFaultPaths {
		for _, call := range p.calls {
			for _, errno := range []string{"ENOSPC", "EIO", "EACCES"} {
				out = append(out, writerFault{p.caseID, p.label, p.glob, call, errno})
			}
		}
	}
	return out
}

// requireStrace skips without strace on PATH, except in CI.
func requireStrace(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("strace"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("strace is required in CI")
		}
		t.Skip("no strace")
	}
}

// TestLiveWriterFaults fails one syscall on one writer path with strace and
// compares TS and Go like the writer replay: stdout, stderr, exit status,
// recorded argv and the workspace tree (what each leaves behind). Go runs
// with GOMAXPROCS=1: strace counts when=1 per thread (round 6 finding 15),
// and each faulted path is one the writer touches once per group.
func TestLiveWriterFaults(t *testing.T) {
	oracle.Skip(t)
	requireStrace(t)
	setWriterUmask(t)
	repo := repoRoot(t)
	templates := map[string]writerTemplate{}
	for _, tpl := range loadWriterTemplates(t, repo) {
		templates[tpl.ID] = tpl
	}
	discovered := map[string]string{}
	for _, f := range writerFaultMatrix() {
		t.Run(f.name(), func(t *testing.T) {
			tpl, ok := templates[f.caseID]
			if !ok || len(tpl.Argv) != 1 {
				t.Fatalf("writer template %s missing or not a single invocation", f.caseID)
			}
			tpl.Env = append(tpl.Env, [2]string{"GOMAXPROCS", "1"})
			rel, ok := discovered[f.caseID+"|"+f.glob]
			if !ok {
				rel = discoverFaultPath(t, repo, tpl, f.glob)
				discovered[f.caseID+"|"+f.glob] = rel
			}
			ts := runTSWithFault(t, repo, tpl, f, rel)
			goRes := runGoWithFault(t, repo, tpl, f, rel)
			if ts.injected == 0 {
				t.Fatalf("TS made no %s call on %s to fail", f.call, rel)
			}
			if goRes.injected != ts.injected {
				t.Errorf("%s on %s injected %d times in TS, %d in Go", f.call, rel, ts.injected, goRes.injected)
			}
			compareWriter(t, f.name(), goRes.writerResult, asGolden(ts.writerResult))
			first, _, _ := strings.Cut(strings.TrimSpace(ts.Stderr+ts.Stdout), "\n")
			t.Logf("both exit %d, %d file(s) changed: %.160s", ts.Exit, len(ts.Files), first)
		})
	}
}

// faultResult is an invocation run under strace and the number of calls
// strace failed.
type faultResult struct {
	writerResult
	injected int
}

func runTSWithFault(t *testing.T, repo string, tpl writerTemplate, f writerFault, rel string) faultResult {
	return runWithFault(t, repo, tpl, f, rel, tsCommand(repo))
}

func runGoWithFault(t *testing.T, repo string, tpl writerTemplate, f writerFault, rel string) faultResult {
	return runWithFault(t, repo, tpl, f, rel, []string{flBinary})
}

// stagedPid is the process id in a ledger binding's staged name
// (src/ledger-binding.ts:540 `.${basename}.${pid}.${uuid}.tmp`).
var stagedPid = regexp.MustCompile(`(\.[^./]+\.json)\.[0-9]+(\.[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.tmp)`)

// maskStagedPid masks the pid in staged names in text and renames any such
// file the invocation left in the workspace, so TS and Go trees compare.
func maskStagedPid(t *testing.T, r writerRoots, _ int, stdout, stderr string) (string, string) {
	t.Helper()
	filepath.WalkDir(r.ws, func(p string, d os.DirEntry, err error) error {
		if err == nil && stagedPid.MatchString(d.Name()) {
			if err := os.Rename(p, filepath.Join(filepath.Dir(p), stagedPid.ReplaceAllString(d.Name(), "$1.<PID>$2"))); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	})
	return stagedPid.ReplaceAllString(stdout, "$1.<PID>$2"), stagedPid.ReplaceAllString(stderr, "$1.<PID>$2")
}

// runWithFault runs the case's invocation with command under strace -D (the
// tracee keeps the shell's pid, so a staged name holding the pid is known
// before it runs), failing the first call of f's group on rel.
func runWithFault(t *testing.T, repo string, tpl writerTemplate, f writerFault, rel string, command []string) faultResult {
	t.Helper()
	trace := filepath.Join(t.TempDir(), "trace")
	calls := faultCalls[f.call]
	run := writerRun{
		noVerify: true,
		mask:     maskStagedPid,
		command: func(r writerRoots, _ int, args []string) []string {
			quote := func(p string) string {
				return "'" + strings.ReplaceAll(p, "<PID>", `'"$$"'`) + "'"
			}
			abs := filepath.Join(r.root, filepath.FromSlash(rel))
			script := "exec strace -D -f -qq -s 4096 -o " + quote(trace) + " -P " + quote(abs)
			// A writer may name a workspace path relative to its cwd.
			if after, ok := strings.CutPrefix(rel, "ws/"); ok {
				script += " -P " + quote(after)
			}
			script += " -e trace=" + calls + " -e inject=" + calls + ":error=" + f.errno + ":when=1 -- \"$@\""
			return append([]string{"sh", "-c", script, "sh"}, append(command, args...)...)
		},
	}
	res := runWriterCaseWith(t, repo, tpl, run)[0]
	b, _ := os.ReadFile(trace)
	return faultResult{res, strings.Count(string(b), "(INJECTED)")}
}

// tracedPath is a quoted path argument in an strace line.
var tracedPath = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)

// discoverFaultPath runs the case under TS without a fault, tracing file
// syscalls, and returns the first path (relative to the case root) matching
// glob, with the process id in it written <PID>.
func discoverFaultPath(t *testing.T, repo string, tpl writerTemplate, glob string) string {
	t.Helper()
	trace := filepath.Join(t.TempDir(), "trace")
	var found string
	var seen []string
	run := writerRun{
		noVerify: true,
		command: func(_ writerRoots, _ int, args []string) []string {
			return append([]string{"strace", "-f", "-qq", "-s", "4096", "-o", trace, "-e", "trace=" + discoveryCalls}, append(tsCommand(repo), args...)...)
		},
		mask: func(t *testing.T, r writerRoots, _ int, stdout, stderr string) (string, string) {
			file, err := os.Open(trace)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			scanner := bufio.NewScanner(file)
			scanner.Buffer(nil, 1<<20)
			pid := ""
			for scanner.Scan() && found == "" {
				line := scanner.Text()
				first, _, _ := strings.Cut(line, " ")
				if pid == "" {
					pid = first
				}
				for _, m := range tracedPath.FindAllStringSubmatch(line, -1) {
					p := m[1]
					if !filepath.IsAbs(p) {
						// Only fl's own cwd is the workspace (git children
						// chdir); a Node thread-pool call logs its thread id.
						if first != pid {
							continue
						}
						p = filepath.Join(r.ws, p)
					}
					rel, ok := strings.CutPrefix(p, r.root+"/")
					if !ok {
						continue
					}
					rel = strings.ReplaceAll(rel, "."+pid+".", ".<PID>.")
					if !strings.Contains(rel, ".git/") && !strings.Contains(rel, "/hooks/") && !strings.Contains(rel, "/objects/") {
						seen = append(seen, rel)
					}
					if ok, _ := path.Match(glob, rel); ok {
						found = rel
						break
					}
				}
			}
			return stdout, stderr
		},
	}
	res := runWriterCaseWith(t, repo, tpl, run)[0]
	if found == "" {
		t.Fatalf("%s: no traced path matches %s (exit %d)\n%s%s\ntraced: %s", tpl.ID, glob, res.Exit, res.Stdout, res.Stderr, strings.Join(slices.Compact(slices.Sorted(slices.Values(seen))), "\n"))
	}
	t.Logf("%s: faulting %s", tpl.ID, found)
	return found
}

// asGolden is res as a writer golden line, for compareWriter.
func asGolden(res writerResult) *jsjson.Obj {
	o := jsjson.NewObj()
	o.Set("stdout", jsjson.MakeString(res.Stdout))
	o.Set("stderr", jsjson.MakeString(res.Stderr))
	o.Set("exit", jsjson.MakeNumber(float64(res.Exit)))
	o.Set("tree", jsjson.MakeString(res.Tree))
	o.Set("modes", jsjson.MakeString(res.Modes))
	files := jsjson.NewObj()
	for k, v := range res.Files {
		files.Set(k, jsjson.MakeString(v))
	}
	o.Set("files", jsjson.MakeObject(files))
	argv := make([]jsjson.Value, len(res.Argv))
	for i, a := range res.Argv {
		argv[i] = jsjson.MakeString(a)
	}
	o.Set("argv", jsjson.MakeArray(argv))
	o.Set("inv", jsjson.MakeNumber(float64(res.Inv)))
	return o
}
