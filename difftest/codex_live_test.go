//go:build faultline_test && unix

package difftest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/bundle/prevention"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/codexloop"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/sandbox"
	"github.com/Mizore66/faultline/internal/witnessresult"
)

// codexResolveCase is one environment for TestLiveResolveCodexRunner: the
// cwd, and PATH and FAULTLINE_CODEX_BIN (nil: unset) on top of the test's
// environment.
type codexResolveCase struct {
	Cwd  string  `json:"cwd"`
	Path *string `json:"path"`
	Bin  *string `json:"bin"`
}

// TestLiveResolveCodexRunner runs resolveCodexRunner (frozen TS, compiled
// dist/runners/codex-runner.js) and codexloop.ResolveRunner each in a child
// process with the same cwd, PATH and FAULTLINE_CODEX_BIN, and compares the
// resolution ({bin, version, source}) or the error message. The cases cover
// the override (absent, blank, padded, relative, absolute-missing, a
// directory, symlinks), probe scripts that fail, print nothing, print CRLF,
// die of a signal, overrun maxBuffer or are not executable, and PATH shapes
// (trailing slashes, empty and relative entries, a directory named codex,
// symlinked and dangling codex, and no codex: the bare fallback).
func TestLiveResolveCodexRunner(t *testing.T) {
	o := oracle.Start(t)
	defer o.Close()
	helper := filepath.Join(t.TempDir(), "codexresolve")
	build := exec.Command("go", "build", "-o", helper, "./testdata/codexresolve")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building codexresolve: %v\n%s", err, out)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mkdir := func(rel string) string {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	script := func(rel string, mode os.FileMode, body string) string {
		p := filepath.Join(root, rel)
		mkdir(filepath.Dir(rel))
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	sh := func(rel, body string) string { return script(rel, 0o755, "#!/bin/sh\n"+body) }
	symlink := func(target, rel string) string {
		p := filepath.Join(root, rel)
		mkdir(filepath.Dir(rel))
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
		return p
	}

	ok := sh("ok/codex", "echo codex-cli 1.0.0\n")
	sh("ok2/codex", "echo codex-cli 2.0.0\n")
	failBoth := sh("fail/both", "printf 'bad flag\\n' >&2; printf ' usage \\n'; exit 3\n")
	failSilent := sh("fail/silent", "exit 1\n")
	failStdout := sh("fail/stdout", "echo only-stdout; exit 9\n")
	failSpace := sh("fail/space", "printf ' \\r\\n\\t' >&2; exit 4\n")
	empty := sh("probe/empty", "exit 0\n")
	blank := sh("probe/blank", "printf ' \\r\\n\\n\\t \\n'\n")
	crlf := sh("probe/crlf", "printf '  codex-cli 1.2.3\\r\\nbuild 7\\r\\n'\n")
	lfMulti := sh("probe/lf", "printf 'codex-cli 3\\nmore\\n'\n")
	crOnly := sh("probe/cr", "printf 'codex-cli 4\\rtail\\n'\n")
	stderrOnly := sh("probe/stderr", "echo to-stderr >&2\n")
	unicode := sh("probe/unicode", "printf '\\342\\200\\250codex-cli \\303\\251 5\\302\\240\\n'\n")
	badUTF8 := sh("probe/badutf8", "printf 'codex-cli \\377\\376 6\\n'\n")
	signal := sh("probe/signal", "echo partial; kill -TERM $$\n")
	big := sh("probe/big", "head -c 2000000 /dev/zero | tr '\\000' a\n")
	noShebang := script("probe/noshebang", 0o755, "echo codex-cli no-shebang\n")
	noExec := script("probe/noexec", 0o644, "#!/bin/sh\necho never\n")
	badInterp := script("probe/badinterp", 0o755, "#!/nonexistent/interpreter\necho never\n")
	dirBin := mkdir("probe/dir.bin")
	link := symlink(ok, "links/codex-link")
	dangling := symlink(filepath.Join(root, "nowhere"), "links/dangling")
	sh("cwd/codex", "echo codex-cli cwd\n")
	sh("cwd/sub/codex", "echo codex-cli sub\n")
	sh("cwd/rel/codex", "echo codex-cli rel-entry\n")
	sh("cwd/sub/version-fail", "echo sub-fail >&2; exit 2\n")
	cwd := filepath.Join(root, "cwd")

	emptyDir := mkdir("pathdirs/empty")
	mkdir("pathdirs/hasdir/codex")
	symlink(ok, "pathdirs/symlinked/codex")
	symlink(filepath.Join(root, "nowhere"), "pathdirs/dangling/codex")
	script("pathdirs/noexec/codex", 0o644, "#!/bin/sh\necho never\n")
	sh("pathdirs/failing/codex", "echo path-fail >&2; exit 5\n")
	sh("pathdirs/crlf/codex", "printf 'codex-cli path\\r\\n'\n")
	sh("pathdirs/é dir/codex", "echo codex-cli unicode-dir\n")
	// A PATH entry "back\" is trimmed to "back" and joined with "\", so
	// the candidate is the file "back\codex" next to it.
	sh(`pathdirs/back\codex`, "echo codex-cli backslash\n")
	mkdir(`pathdirs/back\`)
	symlink(filepath.Join(root, "pathdirs/symlinked"), "pathdirs/dirlink")
	pd := func(name string) string { return filepath.Join(root, "pathdirs", name) }
	okDir := filepath.Dir(ok)
	join := func(entries ...string) string { return strings.Join(entries, ":") }
	sysPath := "/usr/bin:/bin"

	str := func(s string) *string { return &s }
	type tcase struct {
		name string
		cwd  string // "" is root; cwd holds codex, sub/ and rel/
		path *string
		bin  *string
	}
	cases := []tcase{
		// The override.
		{name: "override absent, ok on PATH", path: str(join(okDir, sysPath))},
		{name: "override empty", path: str(join(okDir, sysPath)), bin: str("")},
		{name: "override blank", path: str(join(okDir, sysPath)), bin: str("   ")},
		{name: "override JS whitespace only", path: str(join(okDir, sysPath)), bin: str("\t\n\u00a0\ufeff\u2028\u3000")},
		{name: "override ok", path: str(sysPath), bin: str(ok)},
		{name: "override padded", path: str(sysPath), bin: str("  \t" + ok + " \n")},
		{name: "override padded with NBSP and BOM", path: str(sysPath), bin: str("\u00a0\ufeff" + ok + "\u2003")},
		{name: "override wins over PATH", path: str(join(pd("crlf"), sysPath)), bin: str(ok)},
		{name: "override absolute missing", path: str(sysPath), bin: str(filepath.Join(root, "missing", "codex"))},
		{name: "override absolute missing padded", path: str(sysPath), bin: str(" " + filepath.Join(root, "missing") + " ")},
		{name: "override file with trailing slash", path: str(sysPath), bin: str(ok + "/")},
		{name: "override directory", path: str(sysPath), bin: str(dirBin)},
		{name: "override symlink", path: str(sysPath), bin: str(link)},
		{name: "override dangling symlink", path: str(sysPath), bin: str(dangling)},
		{name: "override relative bare, on PATH", cwd: cwd, path: str(join(okDir, sysPath)), bin: str("codex")},
		{name: "override relative bare, only in cwd", cwd: cwd, path: str(sysPath), bin: str("codex")},
		{name: "override relative bare, empty PATH entry", cwd: cwd, path: str(join("", sysPath)), bin: str("codex")},
		{name: "override relative missing", cwd: cwd, path: str(sysPath), bin: str("faultline-no-such-codex")},
		{name: "override relative dot slash", cwd: cwd, path: str(sysPath), bin: str("./sub/codex")},
		{name: "override relative with slash", cwd: cwd, path: str(sysPath), bin: str("sub/codex")},
		{name: "override relative failing", cwd: cwd, path: str(sysPath), bin: str("sub/version-fail")},
		{name: "override relative missing with slash", cwd: cwd, path: str(sysPath), bin: str("sub/nope")},
		{name: "override relative padded", cwd: cwd, path: str(join(okDir, sysPath)), bin: str(" codex ")},
		{name: "override with PATH unset", bin: str(ok)},
		// Probe results.
		{name: "probe fails with stderr and stdout", path: str(sysPath), bin: str(failBoth)},
		{name: "probe fails silently", path: str(sysPath), bin: str(failSilent)},
		{name: "probe fails with stdout only", path: str(sysPath), bin: str(failStdout)},
		{name: "probe fails with whitespace", path: str(sysPath), bin: str(failSpace)},
		{name: "probe empty output", path: str(sysPath), bin: str(empty)},
		{name: "probe blank output", path: str(sysPath), bin: str(blank)},
		{name: "probe CRLF", path: str(sysPath), bin: str(crlf)},
		{name: "probe LF lines", path: str(sysPath), bin: str(lfMulti)},
		{name: "probe lone CR", path: str(sysPath), bin: str(crOnly)},
		{name: "probe stderr only", path: str(sysPath), bin: str(stderrOnly)},
		{name: "probe unicode whitespace", path: str(sysPath), bin: str(unicode)},
		{name: "probe invalid UTF-8", path: str(sysPath), bin: str(badUTF8)},
		{name: "probe killed by a signal", path: str(sysPath), bin: str(signal)},
		{name: "probe overruns maxBuffer", path: str(sysPath), bin: str(big)},
		{name: "probe script without shebang", path: str(sysPath), bin: str(noShebang)},
		{name: "probe not executable", path: str(sysPath), bin: str(noExec)},
		{name: "probe missing interpreter", path: str(sysPath), bin: str(badInterp)},
		// PATH.
		{name: "PATH trailing slashes", path: str(join(okDir+"///", sysPath))},
		{name: "PATH empty entries", path: str(join("", "", emptyDir, "", okDir, ""))},
		{name: "PATH directory named codex", path: str(join(pd("hasdir"), okDir))},
		{name: "PATH symlinked codex", path: str(join(emptyDir, pd("symlinked")))},
		{name: "PATH symlinked directory", path: str(join(pd("dirlink"), okDir))},
		{name: "PATH dangling codex skipped", path: str(join(pd("dangling"), filepath.Dir(filepath.Join(root, "ok2/codex"))))},
		{name: "PATH non-executable codex first", path: str(join(pd("noexec"), okDir))},
		{name: "PATH failing codex first", path: str(join(pd("failing"), okDir))},
		{name: "PATH CRLF codex", path: str(pd("crlf"))},
		{name: "PATH unicode directory", path: str(join(pd("é dir"), okDir))},
		{name: "PATH backslash entry", path: str(join(pd(`back\`), okDir))},
		{name: "PATH relative entry", cwd: cwd, path: str(join("rel", okDir))},
		{name: "PATH dot entry", cwd: cwd, path: str(join(".", okDir))},
		{name: "PATH no codex: bare fallback", path: str(join(emptyDir, sysPath))},
		{name: "PATH no codex, codex in cwd", cwd: cwd, path: str(join(emptyDir, sysPath))},
		{name: "PATH no codex, empty entry finds cwd", cwd: cwd, path: str(join(emptyDir, "", sysPath))},
		{name: "PATH empty string", path: str("")},
		{name: "PATH empty string, codex in cwd", cwd: cwd, path: str("")},
		{name: "PATH unset"},
	}
	if len(cases) != 60 {
		t.Fatalf("%d cases, want 60", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := codexResolveCase{Cwd: tc.cwd, Path: tc.path, Bin: tc.bin}
			if c.Cwd == "" {
				c.Cwd = root
			}
			req, _ := json.Marshal(c)
			want, err := o.Call("resolveCodexRunner", string(req))
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(helper)
			cmd.Dir = c.Cwd
			for _, kv := range os.Environ() {
				if name, _, _ := strings.Cut(kv, "="); name != "PATH" && name != "FAULTLINE_CODEX_BIN" {
					cmd.Env = append(cmd.Env, kv)
				}
			}
			if c.Path != nil {
				cmd.Env = append(cmd.Env, "PATH="+*c.Path)
			}
			if c.Bin != nil {
				cmd.Env = append(cmd.Env, "FAULTLINE_CODEX_BIN="+*c.Bin)
			}
			got, err := cmd.Output()
			if err != nil {
				t.Fatalf("go child: %v", err)
			}
			if string(got) != want {
				t.Errorf("node %s\ngo   %s", want, got)
			}
		})
	}
}

// TestLiveImplementCodexRunner runs implementWitnessWithCodex with the
// runner witness implement --with-codex builds (frozen TS) and
// codexloop.Implement with codexloop.CLIRunner, against a fake codex, and
// compares the results with the overlay directory masked. The cases pin the
// refusal without --allow-codex-full-auto, the exit-code notes, and the
// ERR_INVALID_ARG_VALUE text for a NUL in the real multi-line prompt (from
// the symptom or the behavior), which util.inspect splits per line and cuts
// at 128 units.
func TestLiveImplementCodexRunner(t *testing.T) {
	o := oracle.Start(t)
	defer o.Close()
	dir := t.TempDir()
	fake := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then echo codex-cli 0.0.1; exit 0; fi\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	ok := fake("codex-ok", "cat >/dev/null; exit 0\n")
	exit3 := fake("codex-exit3", "cat >/dev/null; echo nope >&2; exit 3\n")
	killed := fake("codex-killed", "kill -KILL $$\n")
	type tcase struct {
		name, bin, symptom, behavior string
		allow                        bool
	}
	cases := []tcase{
		{name: "refused without allow", bin: ok, symptom: "CI fails", behavior: "b", allow: false},
		{name: "drafted", bin: ok, symptom: "CI fails", behavior: "b", allow: true},
		{name: "exit 3", bin: exit3, symptom: "CI fails", behavior: "b", allow: true},
		{name: "killed", bin: killed, symptom: "CI fails", behavior: "b", allow: true},
		{name: "missing bin", bin: filepath.Join(dir, "nope"), symptom: "CI fails", behavior: "b", allow: true},
		{name: "NUL in symptom", bin: ok, symptom: "x\x00y", behavior: "The human-reviewed predicate must hold.", allow: true},
		{name: "NUL in behavior", bin: ok, symptom: "CI fails", behavior: "a\x00b", allow: true},
		{name: "NUL, quotes and unicode", bin: ok, symptom: "it's \"bad\" é\x00 ", behavior: "b`${x}`", allow: true},
		{name: "NUL refused without allow", bin: ok, symptom: "x\x00y", behavior: "b", allow: false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			incident := jsjson.MustObject(
				"symptom", jsjson.MakeString(tc.symptom),
				"ciLog", jsjson.MakeString("log"),
				"repositoryLanguage", jsjson.MakeString("go"),
				"repositorySummary", jsjson.MakeString("summary"),
			)
			nodeOut := filepath.Join(dir, "node-"+strconv.Itoa(i))
			goOut := filepath.Join(dir, "go-"+strconv.Itoa(i))
			req := jsjson.MustObject(
				"bin", jsjson.MakeString(tc.bin),
				"allow", jsjson.MakeBool(tc.allow),
				"proposalId", jsjson.MakeString("p1"),
				"incident", incident,
				"behavior", jsjson.MakeString(tc.behavior),
				"overlayOut", jsjson.MakeString(nodeOut),
				"repository", jsjson.MakeString(dir),
			)
			res, err := o.Call("implementWitnessCodexCli", jsjson.Stringify(req))
			if err != nil {
				t.Fatal(err)
			}
			var want string
			if err := json.Unmarshal([]byte(res), &want); err != nil {
				t.Fatal(err)
			}
			t.Setenv("FAULTLINE_CODEX_BIN", tc.bin)
			refusal := "Codex drafting requires explicit --allow-codex-full-auto in addition to --with-codex."
			got, err := codexloop.Implement(codexloop.ImplementOptions{
				ProposalID: "p1", Incident: incident, Behavior: tc.behavior, OverlayOut: goOut, Repository: dir,
				Runner: codexloop.CLIRunner(tc.allow, refusal, 120*time.Second),
			})
			if err != nil {
				t.Fatal(err)
			}
			want = strings.ReplaceAll(want, nodeOut, "<OUT>")
			gotText := strings.ReplaceAll(jsjson.Stringify(got), goOut, "<OUT>")
			recordOutput(t, want)
			if gotText != want {
				t.Errorf("node %s\ngo   %s", want, gotText)
			}
		})
	}
	requireReachedGroup(t, "internal/codexloop/runner.go")
}

// --- Slice 4c Task 16: repairWithCodex and the frozen-witness verifier ---

// repairEdit is one change the scripted Codex makes in the worktree.
type repairEdit struct {
	Path    string  `json:"path"`
	Content *string `json:"content,omitempty"`
	Mode    *int    `json:"mode,omitempty"`
	Remove  bool    `json:"remove,omitempty"`
}

// repairCodex is the scripted Codex step: a result (exit code, output,
// edits) or a throw.
type repairCodex struct {
	Kind     string       `json:"kind"`
	ExitCode *int         `json:"exitCode"`
	Stdout   string       `json:"stdout"`
	Stderr   string       `json:"stderr"`
	Edits    []repairEdit `json:"edits,omitempty"`
	Message  string       `json:"message,omitempty"`
}

// repairVerdict is one scripted verifier answer (verifier "scripted"):
// RemoveFrozen deletes the bundle's witness/frozen.json and RemoveAdminDir
// the worktree's administrative directory under the repository, before the
// verdict is returned. A Throw is a rejected verifier.
type repairVerdict struct {
	OK             bool    `json:"ok"`
	Detail         string  `json:"detail"`
	ExecutionID    *string `json:"executionId,omitempty"`
	RemoveAdminDir bool    `json:"removeAdminDir,omitempty"`
	RemoveFrozen   bool    `json:"removeFrozen,omitempty"`
	Throw          string  `json:"-"`
}

// repairScenario is one TestLiveRepairWithCodex case.
type repairScenario struct {
	name       string
	sameRoot   bool
	investigat []string
	mutate     string // "", "no-image", "repo-missing", "frozen-gone"
	verify     string // "real", "bad-root", "valid"
	// options
	instructionsOnly, keep, withCodex bool
	invariant                         *string
	codex                             *repairCodex
	verifier                          string   // "none", "frozen", "scripted"
	sandbox                           []string // nil: the native Docker runner (the fake Docker on PATH)
	verdicts                          []repairVerdict
	preOutput                         bool
	wantStatus                        string // a status, or two joined by _OR_ (not checked)
	wantExport                        string // "", "ok", "fail"
}

func ptr[T any](v T) *T { return &v }

// native says whether the scenario runs the verifier through the fake Docker
// (NATIVE_DOCKER), the only one whose executionId the harness names.
func (sc repairScenario) native() bool { return sc.verifier == "frozen" && sc.sandbox == nil }

func repairFix() []repairEdit {
	return []repairEdit{{Path: "state.txt", Content: ptr("good\n")}}
}

func repairScenarios() []repairScenario {
	// 4,095 units of "a" and then an astral character: slice(0, 4096) cuts
	// the surrogate pair in two.
	flood := strings.Repeat("a", 4095) + "\U0001F600" + strings.Repeat("b", 50)
	okCodex := func() *repairCodex {
		return &repairCodex{Kind: "result", ExitCode: ptr(0), Stdout: "done é\n", Stderr: "warn\n", Edits: repairFix()}
	}
	firstID := ptr("sha256:" + strings.Repeat("b", 64))
	return []repairScenario{
		{name: "bundle-invalid", verify: "bad-root", wantStatus: "BUNDLE_INVALID", sameRoot: true},
		{name: "instructions-only", verify: "real", instructionsOnly: true, invariant: ptr("Keep state good."), wantStatus: "REPAIR_INSTRUCTIONS_PREPARED", sameRoot: true},
		{name: "worktree-ready", verify: "real", wantStatus: "REPAIR_WORKTREE_READY_OR_CLEANUP", sameRoot: true},
		{name: "keep-worktree", verify: "real", keep: true, wantStatus: "REPAIR_WORKTREE_READY", sameRoot: true},
		{name: "output-exists", verify: "real", preOutput: true, wantStatus: "REPAIR_SETUP_FAILED", sameRoot: true},
		{name: "repository-missing", verify: "valid", mutate: "repo-missing", wantStatus: "REPAIR_SETUP_FAILED", sameRoot: true},
		{name: "codex-no-runner", verify: "real", withCodex: true, wantStatus: "REPAIR_WORKTREE_READY_OR_CLEANUP", sameRoot: true},
		{name: "codex-exit-3", verify: "real", withCodex: true, keep: true, codex: &repairCodex{Kind: "result", ExitCode: ptr(3), Stdout: flood, Stderr: flood}, verifier: "frozen", sandbox: []string{"PASS"}, wantStatus: "CODEX_UNAVAILABLE", sameRoot: true},
		{name: "codex-null-exit", verify: "real", withCodex: true, codex: &repairCodex{Kind: "result", Stdout: "x"}, verifier: "frozen", sandbox: []string{"PASS"}, wantStatus: "CODEX_UNAVAILABLE", sameRoot: true},
		{name: "codex-throws", verify: "real", withCodex: true, codex: &repairCodex{Kind: "throw", Message: "spawn codex ENOENT"}, verifier: "frozen", sandbox: []string{"PASS"}, wantStatus: "CODEX_UNAVAILABLE", sameRoot: true},
		{name: "codex-draft-no-verifier", verify: "real", withCodex: true, keep: true, codex: okCodex(), wantStatus: "CODEX_DRAFT_RECORDED", sameRoot: true},
		{name: "codex-draft-empty-patch", verify: "real", withCodex: true, codex: &repairCodex{Kind: "result", ExitCode: ptr(0)}, wantStatus: "CODEX_DRAFT_OR_CLEANUP", sameRoot: true},
		{name: "codex-binary-edit", verify: "real", withCodex: true, keep: true, codex: &repairCodex{Kind: "result", ExitCode: ptr(0), Edits: []repairEdit{
			{Path: "state.txt", Content: ptr("good\n")}, {Path: "bin/run.sh", Content: ptr("#!/bin/sh\n\x00\x01"), Mode: ptr(0o755)}, {Path: "naïve 名.txt", Content: ptr("x")}}},
			wantStatus: "CODEX_DRAFT_RECORDED", sameRoot: true},
		{name: "verifier-read-fails", verify: "valid", mutate: "frozen-gone", withCodex: true, codex: okCodex(), verifier: "frozen", sandbox: []string{"PASS"}, wantStatus: "CODEX_UNAVAILABLE", sameRoot: true},
		{name: "no-pinned-image", verify: "valid", mutate: "no-image", withCodex: true, codex: okCodex(), verifier: "frozen", sandbox: []string{"PASS"}, wantStatus: "REPAIR_VERIFICATION_FAILED", sameRoot: true},
		{name: "injected-fail-first", verify: "real", withCodex: true, codex: okCodex(), verifier: "frozen", sandbox: []string{"FAIL"}, wantStatus: "REPAIR_VERIFICATION_FAILED", sameRoot: true},
		{name: "injected-error-first", verify: "real", withCodex: true, codex: okCodex(), verifier: "frozen", sandbox: []string{"ERROR"}, wantStatus: "REPAIR_VERIFICATION_FAILED", sameRoot: true},
		{name: "injected-pass-x4", verify: "real", withCodex: true, codex: okCodex(), verifier: "frozen", sandbox: []string{"PASS"}, wantStatus: "REPAIR_CANDIDATE_VERIFIED", wantExport: "ok", sameRoot: true},
		{name: "injected-pass-x4-keep", verify: "real", keep: true, withCodex: true, codex: okCodex(), verifier: "frozen", sandbox: []string{"PASS"}, wantStatus: "REPAIR_CANDIDATE_VERIFIED", wantExport: "ok", sameRoot: true},
		{name: "injected-pass-then-fail", verify: "real", withCodex: true, codex: okCodex(), verifier: "frozen", sandbox: []string{"PASS", "FAIL"}, wantStatus: "REPAIR_CANDIDATE_VERIFIED", wantExport: "fail", sameRoot: true},
		{name: "injected-pass-then-error", verify: "real", withCodex: true, codex: okCodex(), verifier: "frozen", sandbox: []string{"PASS", "ERROR"}, wantStatus: "REPAIR_CANDIDATE_VERIFIED", wantExport: "fail", sameRoot: true},
		{name: "scripted-missing-execution-id", verify: "real", withCodex: true, codex: okCodex(), verifier: "scripted", wantStatus: "REPAIR_CANDIDATE_VERIFIED", wantExport: "fail", sameRoot: true,
			verdicts: []repairVerdict{{OK: true, Detail: "first", ExecutionID: firstID}, {OK: true, Detail: "no id"}}},
		{name: "scripted-verifier-throws-in-export", verify: "real", withCodex: true, codex: okCodex(), verifier: "scripted", wantStatus: "REPAIR_CANDIDATE_VERIFIED", wantExport: "fail", sameRoot: true,
			verdicts: []repairVerdict{{OK: true, Detail: "first", ExecutionID: firstID}, {Throw: "THROW:verifier exploded"}}},
		{name: "scripted-frozen-removed-in-export", verify: "real", withCodex: true, codex: okCodex(), verifier: "scripted", wantStatus: "REPAIR_CANDIDATE_VERIFIED", wantExport: "fail", sameRoot: true,
			verdicts: []repairVerdict{{OK: true, Detail: "first", ExecutionID: firstID, RemoveFrozen: true}}},
		{name: "scripted-cleanup-fails", verify: "real", withCodex: true, codex: okCodex(), verifier: "scripted", wantStatus: "WORKTREE_CLEANUP_FAILED", sameRoot: true,
			verdicts: []repairVerdict{{OK: true, Detail: "first", ExecutionID: firstID, RemoveAdminDir: true}}},
		{name: "scripted-fail", verify: "real", withCodex: true, codex: okCodex(), verifier: "scripted", wantStatus: "REPAIR_VERIFICATION_FAILED", sameRoot: true,
			verdicts: []repairVerdict{{OK: false, Detail: "scripted no"}}},
		// The first real repaired objects: the native verifier through the
		// fake Docker (pass-fail), Task 2's masks, two workspaces.
		{name: "native-success", verify: "real", withCodex: true, codex: okCodex(), verifier: "frozen", wantStatus: "REPAIR_CANDIDATE_VERIFIED", wantExport: "ok"},
		{name: "native-success-keep", verify: "real", keep: true, withCodex: true, codex: okCodex(), verifier: "frozen", wantStatus: "REPAIR_CANDIDATE_VERIFIED", wantExport: "ok"},
		{name: "native-noop-fails", verify: "real", withCodex: true, codex: &repairCodex{Kind: "result", ExitCode: ptr(0)}, verifier: "frozen", wantStatus: "REPAIR_VERIFICATION_FAILED"},
		{name: "native-heterogeneous", verify: "real", investigat: []string{"--runtime-mapping", "runtime-mapping.json"}, withCodex: true, codex: okCodex(), verifier: "frozen", wantStatus: "REPAIR_CANDIDATE_VERIFIED", wantExport: "fail", sameRoot: true},
	}
}

// investigatedWorkspace is a copy of a workspace right after `fl
// investigate git` (before a scenario changes it), with the bundle and its
// root.
type investigatedWorkspace struct{ dir, bundle, root string }

// fanOutDir is a repository's .git/objects/xx directory.
var fanOutDir = regexp.MustCompile(`^(?:(.*)/)?\.git/objects/([0-9a-f]{2})$`)

// baseHasFanOut says whether an object that existed before the case lives in
// the repository's objects/<xx> directory.
func baseHasFanOut(base *gitBase, repo, xx string) bool {
	if repo == "" {
		repo = "."
	}
	for id := range base.objects[repo] {
		if strings.HasPrefix(id, xx) {
			return true
		}
	}
	return false
}

// repairObservation is what one side of a scenario left behind, masked.
type repairObservation struct {
	status, export, result string
	entries                map[string]string
	deltaTicks, deltaUUIDs int
	afterTicks, afterUUIDs int
	calls                  string
	argv                   []string
	tmpLeft                []string
	seeds                  []string // the exec:/run: digests Task 2 named
}

func repairStringArray(items []string) jsjson.Value {
	out := make([]jsjson.Value, len(items))
	for i, s := range items {
		out[i] = jsjson.MakeString(s)
	}
	return jsjson.MakeArray(out)
}

// repairDateFixer writes a git that fixes the identity and the commit dates
// (commitRepairedWorktreeState's hardened environment passes none) and runs
// the real git; it sits on PATH after the recorder, so the recorder still
// logs the call.
func repairDateFixer(t *testing.T) string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nexport GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@example.test GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@example.test\n" +
		"export GIT_AUTHOR_DATE=2026-07-16T12:00:00Z GIT_COMMITTER_DATE=2026-07-16T12:00:00Z\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// repairCaseRoots makes a case's directories under root (writerRoots as
// makeWriterRoots makes them, at a path the caller chooses).
func repairCaseRoots(t *testing.T, repo, root string) writerRoots {
	t.Helper()
	r := writerRoots{root: root, ws: filepath.Join(root, "ws"), home: filepath.Join(root, "home"), tmp: filepath.Join(root, "tmp"),
		argvLog: filepath.Join(root, "argv.log"), recorder: filepath.Join(root, "recorder")}
	for _, dir := range []string{r.root, r.ws, r.home, r.tmp, r.recorder} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(r.home, "empty.gitconfig"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	writeRecorders(t, repo, r)
	return r
}

// repairEnv is the writer environment of the case plus the date fixer after
// the recorder on PATH, as a map.
func repairEnv(repo string, r writerRoots, datefix string) map[string]string {
	env := map[string]string{}
	for _, kv := range writerEnv(repo, r, "repair-bundle", writerTemplate{ID: "repair-bundle", FakeDocker: "pass-fail"}) {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	env["PATH"] = strings.Replace(env["PATH"], r.recorder, r.recorder+string(os.PathListSeparator)+datefix, 1)
	delete(env, "FAULTLINE_TEST_SEED")
	return env
}

// repairApplyEdits makes the scripted Codex's changes under cwd.
func repairApplyEdits(cwd string, edits []repairEdit) error {
	for _, e := range edits {
		target := e.Path
		if !filepath.IsAbs(target) {
			target = filepath.Join(cwd, e.Path)
		}
		if e.Remove {
			if err := os.RemoveAll(target); err != nil {
				return err
			}
			continue
		}
		// mkdirSync(recursive) asks for 0777; the umask is 0 here.
		if err := os.MkdirAll(filepath.Dir(target), 0o777); err != nil {
			return err
		}
		content := ""
		if e.Content != nil {
			content = *e.Content
		}
		if err := os.WriteFile(target, []byte(content), 0o666); err != nil {
			return err
		}
		if e.Mode != nil {
			if err := os.Chmod(target, os.FileMode(*e.Mode)); err != nil {
				return err
			}
		}
	}
	return nil
}

// repairAlign brings the Go seeded clock to the oracle's counters at the
// start of the call.
func repairAlign(seed string, ticks, uuids int) {
	clock.ResetForTest(seed)
	for range ticks {
		clock.NowMillis()
	}
	for range uuids {
		clock.NewUUID()
	}
}

// TestLiveRepairWithCodex runs repairWithCodex (frozen TS, the op in
// node-oracle.ts) and codexloop.RepairWithCodex on the same scenarios with
// scripted Codex results (exit 0 with edits, exit 3, a throw, null, no
// runner) and scripted sandbox runners (PASS x4, FAIL first, PASS then FAIL
// or ERROR, a missing execution id, a rejected verifier), over the committed
// git-unbound and git-heterogeneous scenarios with the proof bundle fl wrote
// into the workspace. It compares the result JSON, the whole workspace tree
// (relocation: Task 2's content-addressed Git object names and the
// exec:/run: seeds), the recorded git and docker argv, the scripted calls
// and the seeded clock's counters (ticks and UUIDs, before and after).
//
// A scenario with sameRoot runs the two sides one after the other at the
// same paths (the workspace is rebuilt in between), with a git that fixes
// the commit dates: an INJECTED_RUNNER executionId is a digest the harness
// does not name, and with equal paths and times every digest is equal. The
// native scenarios (the fake Docker on PATH, NATIVE_DOCKER) use two
// workspaces, so those digests are compared through the exec:/run: seeds.
func TestLiveRepairWithCodex(t *testing.T) {
	oracle.Skip(t)
	const seed = "live-repair-with-codex"
	setWriterUmask(t)
	repo := repoRoot(t)
	c := oracle.StartSeeded(t, seed)
	defer c.Close()
	datefix := repairDateFixer(t)
	pinned := "registry.example/faultline-node@sha256:" + strings.Repeat("b", 64)
	statuses := map[string]int{}
	exports := map[string]int{}

	sharedRoot := filepath.Join(t.TempDir(), "case")
	cacheDir := t.TempDir()
	investigated := map[string]investigatedWorkspace{}
	// FAULTLINE_REPAIR_ONLY="<name> <name> ..." runs only those scenarios,
	// and FAULTLINE_REPAIR_SKIP the others (a local way to split the run
	// into windows that each fit a five-minute command).
	only := strings.Fields(os.Getenv("FAULTLINE_REPAIR_ONLY"))
	skip := strings.Fields(os.Getenv("FAULTLINE_REPAIR_SKIP"))
	for _, sc := range repairScenarios() {
		if (len(only) > 0 && !slices.Contains(only, sc.name)) || slices.Contains(skip, sc.name) {
			continue
		}
		t.Run(sc.name, func(t *testing.T) {
			scenario := "git-unbound"
			investigate := []string{"investigate", "git", "--repo", "repo", "--from", "HEAD~2", "--to", "HEAD", "--proposal", "demo-1", "--image", pinned}
			if len(sc.investigat) > 0 {
				scenario = "git-heterogeneous"
				investigate = []string{"investigate", "git", "--repo", "repo", "--from", "HEAD~1", "--to", "HEAD", "--proposal", "demo-1", "--image", pinned}
				investigate = append(investigate, sc.investigat...)
			}
			rootOf := func() string {
				if sc.sameRoot {
					return sharedRoot
				}
				return filepath.Join(t.TempDir(), "case")
			}

			// setup builds a side's workspace: the scenario, the bundle fl
			// investigate wrote, and the scenario's change to the bundle. A
			// sameRoot scenario restores a copy (cp -a) of the investigated
			// workspace, kept per investigate command, at the shared path.
			setup := func() (writerRoots, string, string, map[string]string) {
				root := rootOf()
				if sc.sameRoot {
					restoreWritable(root)
					os.RemoveAll(root)
				}
				var r writerRoots
				var bundle, expectRoot string
				var env map[string]string
				cacheKey := strings.Join(investigate, " ")
				if cached, ok := investigated[cacheKey]; ok && sc.sameRoot {
					if out, err := exec.Command("cp", "-a", cached.dir, root).CombinedOutput(); err != nil {
						t.Fatalf("cp -a: %v\n%s", err, out)
					}
					r = repairCaseRoots(t, repo, root)
					os.RemoveAll(r.tmp)
					os.Mkdir(r.tmp, 0o755)
					os.Remove(r.argvLog)
					bundle, expectRoot, env = cached.bundle, cached.root, repairEnv(repo, r, datefix)
				} else {
					r = repairCaseRoots(t, repo, root)
					copyScenario(t, repo, scenario, r.ws)
					resetModes(t, r.ws)
					env = repairEnv(repo, r, datefix)
					cmd := exec.Command(flBinary, investigate...)
					cmd.Dir = r.ws
					cmd.Env = os.Environ()
					for k, v := range env {
						cmd.Env = append(cmd.Env, k+"="+v)
					}
					cmd.Env = append(cmd.Env, "FAULTLINE_TEST_SEED=repair-bundle")
					var stdout, stderr strings.Builder
					cmd.Stdout, cmd.Stderr = &stdout, &stderr
					if err := cmd.Run(); err != nil {
						t.Fatalf("fl investigate: %v\n%s%s", err, stdout.String(), stderr.String())
					}
					var ok bool
					if bundle, ok = writtenBundle(stdout.String()); !ok {
						t.Fatalf("no bundle in %s", stdout.String())
					}
					rootText, err := os.ReadFile(filepath.Join(bundle, "ROOT.sha256"))
					if err != nil {
						t.Fatal(err)
					}
					expectRoot = strings.TrimSpace(string(rootText))
					if sc.sameRoot {
						copyDir := filepath.Join(cacheDir, strconv.Itoa(len(investigated)))
						if out, err := exec.Command("cp", "-a", root, copyDir).CombinedOutput(); err != nil {
							t.Fatalf("cp -a: %v\n%s", err, out)
						}
						investigated[cacheKey] = investigatedWorkspace{dir: copyDir, bundle: bundle, root: expectRoot}
					}
				}
				switch sc.mutate {
				case "no-image", "repo-missing":
					path := filepath.Join(bundle, "investigation.json")
					raw, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					investigation, err := jsjson.Parse(string(raw))
					if err != nil {
						t.Fatal(err)
					}
					if sc.mutate == "no-image" {
						for _, run := range investigation.Get("runs").Items() {
							run.Get("sandbox", "runtime").Obj().Set("image", jsjson.MakeString("node:22"))
						}
					} else {
						investigation.Obj().Set("repository", jsjson.MakeString(filepath.Join(r.ws, "no-such-repository")))
					}
					if err := os.WriteFile(path, []byte(jsjson.Stringify(investigation)), 0o644); err != nil {
						t.Fatal(err)
					}
				case "frozen-gone":
					if err := os.Remove(filepath.Join(bundle, "witness", "frozen.json")); err != nil {
						t.Fatal(err)
					}
				}
				if sc.preOutput {
					if err := os.MkdirAll(filepath.Join(r.ws, ".faultline", "repairs", "repair-1"), 0o755); err != nil {
						t.Fatal(err)
					}
				}
				return r, bundle, expectRoot, env
			}

			expectFor := func(root string) string {
				if sc.verify == "bad-root" {
					return "sha256:" + strings.Repeat("0", 64)
				}
				return root
			}
			output := func(r writerRoots) string { return filepath.Join(r.ws, ".faultline", "repairs", "repair-1") }

			observe := func(r writerRoots, base *gitBase, resultText string, codexCalls, sandboxCalls jsjson.Value, before, after [2]int) repairObservation {
				records := readArgvLog(r.argvLog)
				rel := relocation(t, r, records, base)
				// Task 2 names a created object's content, not its path: a loose
				// object's path holds its id, and a linked worktree's gitdir
				// file holds the case path (relocation does not walk .git).
				// Key the entries by the masked path and digest that file's
				// masked text; every other entry is compared as relocation
				// digests it.
				fileDigest := func(path string, b []byte) string {
					if strings.HasSuffix(path, ".git/worktrees/worktree/gitdir") {
						return canonical.SHA256Hex(rel.text(jsstr.DecodeUTF8(b)))
					}
					return rel.fileDigest(path, b)
				}
				entries := map[string]string{}
				for k, v := range treeEntries(t, r.ws, fileDigest) {
					if m := looseObject.FindStringSubmatch(k); m != nil {
						repo := m[1]
						if repo == "" {
							repo = "."
						}
						if name, created := rel.ids[m[2]+m[3]]; created && rel.created[repo][m[2]+m[3]] != "" {
							k = strings.TrimSuffix(k, m[2]+"/"+m[3]) + name
						}
					} else if m := fanOutDir.FindStringSubmatch(k); m != nil && !baseHasFanOut(base, m[1], m[2]) {
						continue // a fan-out directory only created objects live in
					}
					entries[rel.text(k)] = v
				}
				o := repairObservation{result: rel.text(resultText), entries: entries,
					deltaTicks: after[0] - before[0], deltaUUIDs: after[1] - before[1], afterTicks: after[0], afterUUIDs: after[1],
					calls: rel.text(jsjson.Stringify(jsjson.MustObject("codex", codexCalls, "sandbox", sandboxCalls))), tmpLeft: tmpLeftovers(r)}
				for _, record := range records {
					o.argv = append(o.argv, normalizeRecord(record, r, rel))
				}
				o.argv = sortVersionRecordRuns(o.argv)
				for hex, name := range rel.names {
					if strings.HasPrefix(name, "exec:") || strings.HasPrefix(name, "run:") {
						o.seeds = append(o.seeds, name)
						_ = hex
					}
				}
				sort.Strings(o.seeds)
				if v, err := jsjson.Parse(resultText); err == nil {
					o.status = v.Get("status").Str()
					if e := v.Get("preventionExport"); e.Kind() == jsjson.Object {
						o.export = "fail"
						if e.Get("ok").Bool() {
							o.export = "ok"
						}
					}
				} else {
					o.status = resultText
				}
				return o
			}

			// ---- frozen TS ----
			r, bundle, root, env := setup()
			base := snapshotGit(t, r.ws)
			os.Remove(r.argvLog)
			codexArg := jsjson.MakeString("none")
			if sc.codex != nil {
				raw, _ := json.Marshal(sc.codex)
				codexArg = jsexc.Must(jsjson.Parse(string(raw)))
			}
			sandboxArg := jsjson.MakeString("native")
			if sc.sandbox != nil {
				sandboxArg = repairStringArray(sc.sandbox)
			}
			verdicts := make([]jsjson.Value, len(sc.verdicts))
			for i, v := range sc.verdicts {
				if v.Throw != "" {
					verdicts[i] = jsjson.MakeString(v.Throw)
					continue
				}
				raw, _ := json.Marshal(v)
				verdicts[i] = jsexc.Must(jsjson.Parse(string(raw)))
			}
			verify := jsjson.MustObject("kind", jsjson.MakeString("real"))
			if sc.verify == "valid" {
				verify = jsjson.MustObject("kind", jsjson.MakeString("valid"), "rootDigest", jsjson.MakeNull())
			}
			verifier := sc.verifier
			if verifier == "" {
				verifier = "none"
			}
			envObj := jsjson.NewObj()
			for _, k := range slices.Sorted(func(yield func(string) bool) {
				for k := range env {
					if !yield(k) {
						return
					}
				}
			}) {
				envObj.Set(k, jsjson.MakeString(env[k]))
			}
			args := jsjson.MustObject(
				"bundle", jsjson.MakeString(bundle), "expectRoot", jsjson.MakeString(expectFor(root)), "repository", jsjson.MakeString(filepath.Join(r.ws, "repo")),
				"output", jsjson.MakeString(output(r)), "cwd", jsjson.MakeString(r.ws), "env", jsjson.MakeObject(envObj),
				"withCodex", jsjson.MakeBool(sc.withCodex), "instructionsOnly", jsjson.MakeBool(sc.instructionsOnly), "keepWorktree", jsjson.MakeBool(sc.keep),
				"verify", verify, "codex", codexArg, "verifier", jsjson.MakeString(verifier), "sandbox", sandboxArg, "verdicts", jsjson.MakeArray(verdicts))
			if sc.invariant != nil {
				args.Obj().Set("invariant", jsjson.MakeString(*sc.invariant))
			}
			raw := oracleCall(t, c, "repairWithCodex", jsjson.Stringify(args))
			tsRes, err := jsjson.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			tsBefore := [2]int{int(tsRes.Get("before", "ticks").Num()), int(tsRes.Get("before", "uuids").Num())}
			tsAfter := [2]int{int(tsRes.Get("after", "ticks").Num()), int(tsRes.Get("after", "uuids").Num())}
			normalize := func(v jsjson.Value) jsjson.Value { return jsexc.Must(jsjson.Parse(jsjson.Stringify(v))) }
			ts := observe(r, base, tsRes.Get("result").Str(), normalize(tsRes.Get("codexCalls")), normalize(tsRes.Get("sandboxCalls")), tsBefore, tsAfter)
			restoreWritable(r.root)

			// ---- Go ----
			r, bundle, root, env = setup()
			base = snapshotGit(t, r.ws)
			os.Remove(r.argvLog)
			for k, v := range env {
				t.Setenv(k, v)
			}
			t.Chdir(r.ws)
			var codexCalls, sandboxCalls []jsjson.Value
			var runner codexloop.Runner
			if sc.codex != nil {
				runner = func(args []string, cwd, input string) (codexloop.RunResult, error) {
					codexCalls = append(codexCalls, jsjson.MustObject("args", repairStringArray(args), "cwd", jsjson.MakeString(cwd), "input", jsjson.MakeString(input)))
					if sc.codex.Kind == "throw" {
						return codexloop.RunResult{}, errors.New(sc.codex.Message)
					}
					if err := repairApplyEdits(cwd, sc.codex.Edits); err != nil {
						return codexloop.RunResult{}, err
					}
					return codexloop.RunResult{ExitCode: sc.codex.ExitCode, Stdout: sc.codex.Stdout, Stderr: sc.codex.Stderr}, nil
				}
			}
			var sandboxRunner sandbox.Runner
			if sc.sandbox != nil {
				next := 0
				sandboxRunner = func(p sandbox.Plan) (sandbox.RunResult, error) {
					kind := sc.sandbox[min(next, len(sc.sandbox)-1)]
					next++
					sandboxCalls = append(sandboxCalls, jsjson.MustObject("kind", jsjson.MakeString(kind), "arguments", p.Value.Get("arguments"),
						"cwd", p.Value.Get("sourceDirectory"), "containerName", p.Value.Get("containerName")))
					zero, one := 0, 1
					switch kind {
					case "ERROR":
						return sandbox.RunResult{}, errors.New("scripted sandbox failure")
					case "FAIL":
						return sandbox.RunResult{ExitCode: &one, Stdout: "state=bad\n" + witnessresult.Format("PREDICATE_FAIL", "", false) + "\n", Stderr: "witness failed"}, nil
					}
					return sandbox.RunResult{ExitCode: &zero, Stdout: "state=good\n" + witnessresult.Format("PREDICATE_PASS", "", false) + "\n"}, nil
				}
			}
			var verifyCandidate codexloop.VerifyCandidate
			switch verifier {
			case "frozen":
				verifyCandidate = codexloop.FrozenWitnessVerifier(sandboxRunner, sandboxRunner != nil)
			case "scripted":
				next := 0
				verifyCandidate = func(worktree, baseCommit, patchPath, bundleDirectory string) (prevention.Verdict, error) {
					v := sc.verdicts[min(next, len(sc.verdicts)-1)]
					next++
					if v.Throw != "" {
						return prevention.Verdict{}, errors.New(strings.TrimPrefix(v.Throw, "THROW:"))
					}
					if v.RemoveFrozen {
						os.Remove(filepath.Join(bundle, "witness", "frozen.json"))
					}
					if v.RemoveAdminDir {
						pointer, err := os.ReadFile(filepath.Join(worktree, ".git"))
						if err != nil {
							t.Fatal(err)
						}
						os.RemoveAll(strings.TrimPrefix(strings.TrimSpace(string(pointer)), "gitdir: "))
					}
					verdict := prevention.Verdict{OK: v.OK, Detail: v.Detail}
					if v.ExecutionID != nil {
						verdict.ExecutionID, verdict.HasExecutionID = *v.ExecutionID, true
					}
					return verdict, nil
				}
			}
			repairAlign(seed, tsBefore[0], tsBefore[1])
			goBefore0, goBefore1 := clock.ConsumedForTest()
			options := codexloop.RepairOptions{
				BundleDirectory: bundle, ExpectRoot: expectFor(root), Repository: filepath.Join(r.ws, "repo"), OutputDirectory: output(r),
				WithCodex: sc.withCodex, InstructionsOnly: sc.instructionsOnly, KeepWorktree: sc.keep, Runner: runner, VerifyCandidate: verifyCandidate,
				VerifyBundle: func(dir, expectRoot string) (bool, []string, *string) {
					if sc.verify == "valid" {
						return true, nil, nil
					}
					v := gitproof.Verify(dir, expectRoot, true)
					return v.Valid, v.Errors, v.RootDigest
				},
			}
			if sc.invariant != nil {
				options.Invariant, options.HasInvariant = *sc.invariant, true
			}
			var goResult string
			if res, err := codexloop.RepairWithCodex(options); err != nil {
				goResult = "THREW:" + jsexc.Message(err)
			} else {
				goResult = jsjson.Stringify(res)
			}
			goAfter0, goAfter1 := clock.ConsumedForTest()
			gone := observe(r, base, goResult, jsjson.MakeArray(codexCalls), jsjson.MakeArray(sandboxCalls), [2]int{goBefore0, goBefore1}, [2]int{goAfter0, goAfter1})
			restoreWritable(r.root)

			if ts.result != gone.result {
				t.Errorf("result\nnode %s\ngo   %s", ts.result, gone.result)
			}
			if ts.calls != gone.calls {
				t.Errorf("scripted calls\nnode %s\ngo   %s", ts.calls, gone.calls)
			}
			if ts.deltaTicks != gone.deltaTicks || ts.deltaUUIDs != gone.deltaUUIDs || ts.afterTicks != gone.afterTicks || ts.afterUUIDs != gone.afterUUIDs {
				t.Errorf("clock: node consumed %d ticks %d uuids (now %d, %d), go %d ticks %d uuids (now %d, %d)",
					ts.deltaTicks, ts.deltaUUIDs, ts.afterTicks, ts.afterUUIDs, gone.deltaTicks, gone.deltaUUIDs, gone.afterTicks, gone.afterUUIDs)
			}
			if !slices.Equal(ts.argv, gone.argv) {
				t.Errorf("git/docker argv\nnode %q\ngo   %q", ts.argv, gone.argv)
			}
			if !slices.Equal(ts.seeds, gone.seeds) {
				t.Errorf("named exec:/run: digests\nnode %q\ngo   %q", ts.seeds, gone.seeds)
			}
			// The frozen verifier's PASS writes an execution id (the exec:
			// seed) whether or not the export that follows succeeds: a
			// heterogeneous bundle fails the export after a PASS (Task 20).
			if sc.native() && (len(gone.seeds) != 0) != (gone.status == "REPAIR_CANDIDATE_VERIFIED") {
				t.Errorf("exec:/run: digests named: %q, status %s, preventionExport %q", gone.seeds, gone.status, gone.export)
			}
			if !slices.Equal(ts.tmpLeft, gone.tmpLeft) {
				t.Errorf("TMPDIR left: node %v, go %v", ts.tmpLeft, gone.tmpLeft)
			}
			var diffs []string
			for _, k := range slices.Sorted(func(yield func(string) bool) {
				for k := range ts.entries {
					if !yield(k) {
						return
					}
				}
				for k := range gone.entries {
					if _, ok := ts.entries[k]; !ok && !yield(k) {
						return
					}
				}
			}) {
				if ts.entries[k] != gone.entries[k] {
					diffs = append(diffs, fmt.Sprintf("%s: node %q go %q", k, ts.entries[k], gone.entries[k]))
				}
			}
			sort.Strings(diffs)
			if len(diffs) > 0 {
				t.Errorf("workspace tree differs in %d entries:\n%s", len(diffs), strings.Join(diffs, "\n"))
			}
			recordOutput(t, ts.result)
			statuses[gone.status]++
			if gone.export != "" {
				exports[gone.export]++
			}
			if sc.wantExport != "" && gone.export != sc.wantExport {
				t.Errorf("preventionExport %q, want %q (status %s)", gone.export, sc.wantExport, gone.status)
			}
			if !strings.Contains(sc.wantStatus, "_OR_") && sc.wantStatus != "" && gone.status != sc.wantStatus {
				t.Errorf("status %s, want %s", gone.status, sc.wantStatus)
			}
			t.Logf("status %s export %q, %d ticks %d uuids, %d tree entries", gone.status, gone.export, gone.deltaTicks, gone.deltaUUIDs, len(gone.entries))
			if os.Getenv("FAULTLINE_REPAIR_LOG_RESULT") == "1" {
				t.Logf("masked result: %s", gone.result)
				for _, k := range slices.Sorted(func(yield func(string) bool) {
					for k := range gone.entries {
						if !yield(k) {
							return
						}
					}
				}) {
					if strings.Contains(k, "<COMMIT:") || strings.Contains(k, "<TREE:") || strings.Contains(k, "<BLOB:") || strings.Contains(k, "artifacts") || strings.Contains(k, "prevention-proofs") {
						t.Logf("entry %s = %s", k, gone.entries[k])
					}
				}
				t.Logf("named seeds: %q", gone.seeds)
			}
		})
	}
	t.Logf("LIVE-SUMMARY statuses %v exports %v", statuses, exports)
	for _, file := range []string{"internal/codexloop/repair.go", "internal/codexloop/verifier.go"} {
		requireReachedGroup(t, file)
	}
}
