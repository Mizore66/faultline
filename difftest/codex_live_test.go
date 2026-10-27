//go:build faultline_test && unix

package difftest

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/codexloop"
	"github.com/Mizore66/faultline/internal/jsjson"
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
			if gotText != want {
				t.Errorf("node %s\ngo   %s", want, gotText)
			}
		})
	}
}
