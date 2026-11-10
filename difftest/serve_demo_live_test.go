//go:build faultline_test && unix

package difftest

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/bundle/demo"
)

const plainServeBanner = "FaultLine incident page: "

// startPlainServe starts one side's bare `serve` in a fresh workspace and
// reads its stdout up to the page URL line.
func startPlainServe(t *testing.T, repo string, side judgeSide, args ...string) (cmd *exec.Cmd, url string, out *strings.Builder, rest *bufio.Reader, stderr *lockedBuffer) {
	return startJudgeCommand(t, repo, side, judgeEnv("FAULTLINE_NO_BROWSER=1"), "serve", plainServeBanner, append([]string{"--port", "0"}, args...)...)
}

// plainServeStop sends SIGINT, drains stdout and returns the process result.
func plainServeStop(t *testing.T, cmd *exec.Cmd, out *strings.Builder, rest *bufio.Reader) string {
	t.Helper()
	time.Sleep(300 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	tail, _ := io.ReadAll(rest)
	out.Write(tail)
	cmd.Wait()
	return cmd.ProcessState.String()
}

func plainServeBundleDir(cmd *exec.Cmd) string {
	return filepath.Join(cmd.Dir, ".faultline", "bundles", "judge-demo")
}

// plainServeVerifiedRoot independently verifies the side's own bundle and
// returns its root, which must equal the ROOT.sha256 it wrote.
func plainServeVerifiedRoot(t *testing.T, side string, cmd *exec.Cmd) string {
	t.Helper()
	dir := plainServeBundleDir(cmd)
	raw, err := os.ReadFile(filepath.Join(dir, "ROOT.sha256"))
	if err != nil {
		t.Fatalf("%s: %v", side, err)
	}
	root := strings.TrimSpace(string(raw))
	if !judgeDemoRoot.MatchString(root) {
		t.Fatalf("%s: malformed bundle root %q", side, root)
	}
	if v := demo.Verify(dir, "", false); !v.Valid || v.RootDigest == nil || *v.RootDigest != root {
		t.Fatalf("%s: independent verification disagrees with ROOT.sha256 %s: %+v", side, root, v)
	}
	if v := demo.Verify(dir, root, true); !v.Valid {
		t.Fatalf("%s: verification against the root failed: %+v", side, v)
	}
	return root
}

func decodeAnalysis(t *testing.T, side, text string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("%s: analysis is not JSON: %v\n%.300s", side, err, text)
	}
	return v
}

var (
	plainISOTime   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)
	plainSHA256    = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	plainTimestamp = regexp.MustCompile(`"generatedAt":"[^"]*"`)
)

// shape reduces a decoded JSON value to its structure: scalar kinds, object
// keys and array element shapes. It never compares dynamic values.
func shape(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := map[string]any{}
		for k, e := range x {
			m[k] = shape(e)
		}
		return m
	case []any:
		a := make([]any, len(x))
		for i, e := range x {
			a[i] = shape(e)
		}
		return a
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "bool"
	}
	return "null"
}

// plainRerunRoles are the stable protocol roles of a RERUN analysis that
// must match exactly: everything but execution identity, digests, times,
// durations and the built-in Go sample's output text.
func plainRerunRoles(a map[string]any) map[string]any {
	roles := map[string]any{}
	for _, k := range []string{"schemaVersion", "mode", "fixture", "metrics"} {
		roles[k] = a[k]
	}
	var runs []any
	for _, r := range a["contributionRuns"].([]any) {
		m := r.(map[string]any)
		runs = append(runs, map[string]any{"stateId": m["stateId"], "verdict": m["verdict"], "executionKind": m["executionKind"]})
	}
	roles["contributionRuns"] = runs
	return roles
}

// plainCheckAnalysis checks one side's analysis (as served) against its own
// verified artifacts: the digests are well formed and the bundle's
// analysis.json is the same analysis.
func plainCheckAnalysis(t *testing.T, side string, served map[string]any, cmd *exec.Cmd) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(plainServeBundleDir(cmd), "analysis.json"))
	if err != nil {
		t.Fatalf("%s: %v", side, err)
	}
	if bundled := decodeAnalysis(t, side, string(raw)); !reflect.DeepEqual(bundled, served) {
		t.Errorf("%s: served analysis differs from its verified bundle's analysis.json", side)
	}
	if !plainISOTime.MatchString(served["generatedAt"].(string)) {
		t.Errorf("%s: generatedAt %v", side, served["generatedAt"])
	}
	for _, r := range served["contributionRuns"].([]any) {
		run := r.(map[string]any)
		for _, k := range []string{"id", "witnessDigest", "environmentDigest"} {
			if s, _ := run[k].(string); !plainSHA256.MatchString(s) {
				t.Errorf("%s: run %v %s %q", side, run["stateId"], k, run[k])
			}
		}
		if !plainISOTime.MatchString(run["executedAt"].(string)) || run["durationMs"].(float64) < 0 {
			t.Errorf("%s: run %v time/duration %v %v", side, run["stateId"], run["executedAt"], run["durationMs"])
		}
	}
}

// TestLivePlainServeLifecycle compares startup output and the first-SIGINT
// stop, with and without a RERUN, against the frozen TypeScript.
func TestLivePlainServeLifecycle(t *testing.T) {
	oracle.Skip(t)
	repo := repoRoot(t)
	for _, rerun := range []bool{false, true} {
		name := "idle"
		if rerun {
			name = "after-rerun"
		}
		t.Run(name, func(t *testing.T) {
			got := map[string]string{}
			roots := map[string]string{}
			for _, side := range judgeSides(repo) {
				cmd, url, out, rest, stderr := startPlainServe(t, repo, side)
				line, _ := rest.ReadString('\n')
				out.WriteString(line)
				if _, err := os.Stat(plainServeBundleDir(cmd)); err == nil {
					t.Errorf("%s: a bundle exists before any RERUN", side.name)
				}
				if rerun {
					r := fetch(t, strings.TrimPrefix(url, "http://"), judgeRequest{"POST", "/api/rerun", "HTTP/1.1", "Content-Length: 0"})
					if r.status != 200 {
						t.Fatalf("%s: RERUN %d %s", side.name, r.status, r.body)
					}
				}
				state := plainServeStop(t, cmd, out, rest)
				if rerun {
					roots[side.name] = plainServeVerifiedRoot(t, side.name, cmd)
				}
				got[side.name] = loopbackPort.ReplaceAllString(out.String(), "127.0.0.1:<PORT>") + "--- stderr\n" + stderr.String() + "--- " + state
			}
			if got["Go"] != got["TS"] {
				t.Errorf("Go\n%s\nTS\n%s", got["Go"], got["TS"])
			}
			want := "FaultLine incident page: http://127.0.0.1:<PORT>\nPress Ctrl+C to stop.\n--- stderr\n--- exit status 0"
			if got["TS"] != want {
				t.Errorf("frozen TS output is not the expected plain lifecycle:\n%s", got["TS"])
			}
			if rerun && roots["Go"] == "" {
				t.Errorf("no verified Go root")
			}
		})
	}
}

// TestLivePlainServeHTTP compares routes, headers and bodies, and compares
// the RERUN protocol and artifacts structurally while verifying each
// side's own bundle.
func TestLivePlainServeHTTP(t *testing.T) {
	oracle.Skip(t)
	repo := repoRoot(t)
	t.Run("rerun-failure", func(t *testing.T) { testPlainServeRerunFailure(t, repo) })
	static := []judgeRequest{
		{"GET", "/", "HTTP/1.1", ""}, {"GET", "/api/analysis", "HTTP/1.1", ""}, {"GET", "/x", "HTTP/1.1", ""},
		{"HEAD", "/", "HTTP/1.1", ""}, {"POST", "/", "HTTP/1.1", "Content-Length: 0"}, {"GET", "/api/rerun", "HTTP/1.1", ""},
		{"GET", "/?a=1", "HTTP/1.1", ""}, {"GET", "/api/analysis?x=1", "HTTP/1.1", ""}, {"GET", "/x/..", "HTTP/1.1", ""},
	}
	replies := map[string][]httpReply{}
	rerun := map[string]httpReply{}
	after := map[string]httpReply{}
	pages := map[string]httpReply{}
	var cmds = map[string]*exec.Cmd{}
	for _, side := range judgeSides(repo) {
		cmd, url, out, rest, stderr := startPlainServe(t, repo, side)
		cmds[side.name] = cmd
		addr := strings.TrimPrefix(url, "http://")
		for _, r := range static {
			replies[side.name] = append(replies[side.name], fetch(t, addr, r))
		}
		rerun[side.name] = fetch(t, addr, judgeRequest{"POST", "/api/rerun", "HTTP/1.1", "Content-Length: 0"})
		after[side.name] = fetch(t, addr, judgeRequest{"GET", "/api/analysis", "HTTP/1.1", ""})
		pages[side.name] = fetch(t, addr, judgeRequest{"GET", "/", "HTTP/1.1", ""})
		plainServeStop(t, cmd, out, rest)
		if stderr.String() != "" {
			t.Errorf("%s: stderr %q", side.name, stderr.String())
		}
	}
	for i, r := range static {
		g, ts := plainMaskReplayEnv(t, "Go", "v22.22.2", replies["Go"][i]), plainMaskReplayEnv(t, "TS", nodeVersion(t), replies["TS"][i])
		if !reflect.DeepEqual(g, ts) {
			i := 0
			for i < len(g.body) && i < len(ts.body) && g.body[i] == ts.body[i] {
				i++
			}
			lo := max(0, i-60)
			t.Errorf("%v: status %d/%d headers equal %v bodies %d/%d first diff at %d\nGo ...%q\nTS ...%q", r, g.status, ts.status, reflect.DeepEqual(g.headers, ts.headers), len(g.body), len(ts.body), i, g.body[lo:min(len(g.body), i+100)], ts.body[lo:min(len(ts.body), i+100)])
		}
	}
	if s := replies["TS"][1]; s.status != 200 || decodeAnalysis(t, "TS", s.body)["mode"] != "REPLAY" {
		t.Errorf("frozen TS REPLAY analysis not served: %+v", s)
	}

	// RERUN success: same status and headers; the body is the exact JSON
	// shape with each side's own generatedAt, equal to that side's served
	// and bundled analysis.
	g, ts := rerun["Go"], rerun["TS"]
	if g.status != ts.status || !reflect.DeepEqual(g.headers, ts.headers) || ts.status != 200 {
		t.Errorf("RERUN reply: Go %d %v\nTS %d %v", g.status, g.headers, ts.status, ts.headers)
	}
	for _, side := range []string{"TS", "Go"} {
		body := rerun[side].body
		served := decodeAnalysis(t, side, after[side].body)
		generated, _ := served["generatedAt"].(string)
		if want := `{"ok":true,"mode":"RERUN","generatedAt":"` + generated + `"}`; body != want {
			t.Errorf("%s: RERUN body %q, want %q", side, body, want)
		}
		if !reflect.DeepEqual(after[side].headers, after["TS"].headers) || after[side].status != 200 {
			t.Errorf("%s: post-RERUN analysis reply %+v", side, after[side])
		}
		root := plainServeVerifiedRoot(t, side, cmds[side])
		plainCheckAnalysis(t, side, served, cmds[side])
		if pages[side].status != 200 || !strings.Contains(pages[side].body, "<html") {
			t.Errorf("%s: page %d", side, pages[side].status)
		}
		if raw, _ := os.ReadFile(filepath.Join(plainServeBundleDir(cmds[side]), "ROOT.sha256")); !strings.Contains(string(raw), root) {
			t.Errorf("%s: root", side)
		}
	}
	if !reflect.DeepEqual(pages["Go"].headers, pages["TS"].headers) {
		t.Errorf("page headers: Go %v TS %v", pages["Go"].headers, pages["TS"].headers)
	}
	goA, tsA := decodeAnalysis(t, "Go", after["Go"].body), decodeAnalysis(t, "TS", after["TS"].body)
	if !reflect.DeepEqual(shape(goA), shape(tsA)) {
		gs, _ := json.MarshalIndent(shape(goA), "", " ")
		ts, _ := json.MarshalIndent(shape(tsA), "", " ")
		t.Errorf("RERUN analysis structure differs:\nGo %s\nTS %s", gs, ts)
	}
	if gr, tr := plainRerunRoles(goA), plainRerunRoles(tsA); !reflect.DeepEqual(gr, tr) {
		t.Errorf("RERUN roles differ:\nGo %v\nTS %v", gr, tr)
	}
	if mode := tsA["mode"]; mode != "RERUN" {
		t.Errorf("frozen TS analysis mode %v", mode)
	}
	// The two sides' roots and identities legitimately differ, but each
	// bundle must at least list the same files.
	if gl, tl := plainBundleFiles(t, cmds["Go"]), plainBundleFiles(t, cmds["TS"]); !reflect.DeepEqual(gl, tl) {
		t.Errorf("bundle files differ:\nGo %v\nTS %v", gl, tl)
	}
}

// nodeVersion is the frozen TypeScript's own process.version.
func nodeVersion(t *testing.T) string {
	out, err := exec.Command("node", "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// plainMaskReplayEnv checks that every environmentDigest in a served REPLAY
// analysis is the SHA-256 of that side's own canonical environment record
// (TS records its Node process.version, Go the fixed historical runtime),
// then replaces only those verified digests with <ENV>.
func plainMaskReplayEnv(t *testing.T, side, runtime string, r httpReply) httpReply {
	if !strings.Contains(r.body, `"environmentDigest"`) {
		return r
	}
	canonical := `{"credentials":"redacted","network":"disabled","packageManager":"none","runner":"built-in-sample-v1","runtime":"` + runtime + `"}`
	sum := sha256.Sum256([]byte(canonical))
	want := "sha256:" + hex.EncodeToString(sum[:])
	if n := strings.Count(r.body, `"environmentDigest":"`+want+`"`); n != strings.Count(r.body, `"environmentDigest"`) || n == 0 {
		t.Errorf("%s: REPLAY environmentDigest values are not the digest %s of the side's own environment", side, want)
	}
	r.body = strings.ReplaceAll(r.body, want, "<ENV>")
	return r
}

// plainBundleFiles lists the bundle's files with run directory names (which
// are content-derived) reduced to a placeholder.
func plainBundleFiles(t *testing.T, cmd *exec.Cmd) []string {
	var files []string
	dir := plainServeBundleDir(cmd)
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			files = append(files, regexp.MustCompile(`sha256_[0-9a-f]{64}`).ReplaceAllString(rel, "sha256_<ID>"))
		}
		return nil
	})
	sort.Strings(files)
	return files
}

// TestLivePlainServeRerunFailure makes the bundle output unwritable and
// compares the failure reply and the later behavior with the frozen TS.
func testPlainServeRerunFailure(t *testing.T, repo string) {
	got := map[string]httpReply{}
	later := map[string]httpReply{}
	for _, side := range judgeSides(repo) {
		cmd, url, out, rest, _ := startPlainServe(t, repo, side)
		// `.faultline` as a file makes creating the bundle directory fail.
		os.RemoveAll(filepath.Join(cmd.Dir, ".faultline"))
		if err := os.WriteFile(filepath.Join(cmd.Dir, ".faultline"), []byte("not a directory"), 0o644); err != nil {
			t.Fatal(err)
		}
		addr := strings.TrimPrefix(url, "http://")
		r := fetch(t, addr, judgeRequest{"POST", "/api/rerun", "HTTP/1.1", "Content-Length: 0"})
		r.body = strings.ReplaceAll(r.body, cmd.Dir, "<WORKSPACE>")
		got[side.name] = r
		later[side.name] = fetch(t, addr, judgeRequest{"GET", "/api/analysis", "HTTP/1.1", ""})
		plainServeStop(t, cmd, out, rest)
	}
	if g, ts := got["Go"], got["TS"]; !reflect.DeepEqual(g, ts) || ts.status != http.StatusInternalServerError {
		t.Errorf("failure reply: Go %+v\nTS %+v", g, ts)
	}
	// Declared difference (spec section 6): frozen TS assigns the RERUN
	// analysis before writing, so after a failed write it keeps serving the
	// unwritten RERUN analysis; Go leaves the prior REPLAY analysis
	// unchanged. Status and headers must still match; each side's mode is
	// asserted against its own documented behavior.
	g, ts := later["Go"], later["TS"]
	if g.status != ts.status || !reflect.DeepEqual(g.headers, ts.headers) {
		t.Errorf("analysis reply after a failed RERUN: Go %d %v\nTS %d %v", g.status, g.headers, ts.status, ts.headers)
	}
	if m := decodeAnalysis(t, "TS", ts.body)["mode"]; m != "RERUN" {
		t.Errorf("frozen TS served mode %v after a failed RERUN", m)
	}
	if m := decodeAnalysis(t, "Go", g.body)["mode"]; m != "REPLAY" {
		t.Errorf("Go served mode %v after a failed RERUN, want the retained REPLAY", m)
	}
}
