//go:build faultline_test

package difftest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

// TestWitnessLiveReplay runs the covered fl binary on refused
// `witness propose --live` invocations against a scripted loopback provider
// and compares exact stderr with the hand-committed diagnostics, so the
// coverage gate sees the internal/cli/witness_live.go and overlay-input error
// sites execute. A refusal before the provider call must make no request, and
// one after it exactly one.
func TestWitnessLiveReplay(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "difftest", "testdata", "witness-live", "diagnostics.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	texts := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		var row struct{ ID, Text string }
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		texts[row.ID] = row.Text
	}
	const incident = `{"symptom":"refund fails","ciLog":"Expected USD; received EUR.","repositoryLanguage":"JavaScript","repositorySummary":"payments"}`
	proposal := func(overlays string) string {
		text := `{"behavior":"b","command":"node witness.mjs","overlayFiles":` + overlays + `,"expectedExitCode":0,"timeoutSeconds":30,"safetyNotes":[]}`
		return `{"output":[{"type":"message","content":[{"type":"output_text","text":` + jsjson.Stringify(jsjson.MakeString(text)) + `}]}]}`
	}
	live := []string{"witness", "propose", "--live", "--incident", "incident.json"}
	withIDRoot := append(append([]string{}, live...), "--proposal-id", "live-1", "--overlay-root", "overlays")
	for _, tc := range []struct {
		id       string
		args     []string
		overlays string
		files    map[string]string
		noKey    bool
		requests int
	}{
		{id: "witness-live-no-proposal-id", args: append(append([]string{}, live...), "--overlay-root", "overlays")},
		{id: "witness-live-no-overlay-root", args: append(append([]string{}, live...), "--proposal-id", "live-1")},
		{id: "witness-live-no-key", args: withIDRoot, noKey: true},
		{id: "witness-live-unsafe-overlay", args: withIDRoot, overlays: `["../x"]`, files: map[string]string{"overlays/keep.txt": "k"}, requests: 1},
		{id: "witness-live-nul-overlay", args: withIDRoot, overlays: `["a\u0000b"]`, files: map[string]string{"overlays/keep.txt": "k"}, requests: 1},
		{id: "witness-live-root-not-directory", args: withIDRoot, files: map[string]string{"overlays": "file"}, requests: 1},
		{id: "witness-live-parent-not-directory", args: withIDRoot, overlays: `["a/b.mjs"]`, files: map[string]string{"overlays/a": "file"}, requests: 1},
		{id: "witness-live-leaf-not-file", args: withIDRoot, overlays: `["d"]`, files: map[string]string{"overlays/d/x.txt": "x"}, requests: 1},
	} {
		t.Run(tc.id, func(t *testing.T) {
			if texts[tc.id] == "" {
				t.Fatalf("no committed %s diagnostic", tc.id)
			}
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				requests++
				io.WriteString(w, proposal(map[bool]string{true: tc.overlays, false: "[]"}[tc.overlays != ""]))
			}))
			defer srv.Close()
			ws := t.TempDir()
			if tc.id == "witness-live-nul-overlay" {
				// The message cuts the inspected path at 128 units, so this
				// case needs a short workspace path on every platform.
				short, err := os.MkdirTemp("", "w")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.RemoveAll(short) })
				ws = short
			}
			files := map[string]string{"incident.json": incident}
			for k, v := range tc.files {
				files[k] = v
			}
			for name, content := range files {
				path := filepath.Join(ws, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := os.Stat(filepath.Join(ws, "overlays")); err != nil {
				if err := os.Mkdir(filepath.Join(ws, "overlays"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			key := "fl-test-key"
			if tc.noKey {
				key = ""
			}
			dir, want := witnessLiveReplayWorkspace(t, ws, texts[tc.id])
			stdout, stderr, code := runFl(t, dir, map[string]string{"FAULTLINE_TEST_OPENAI_URL": srv.URL + "/v1/responses", "OPENAI_API_KEY": key}, tc.args...)
			if code != 1 || stdout != "" || stderr != want || requests != tc.requests {
				t.Fatalf("code=%d stdout=%q stderr=%q requests=%d, want stderr %q requests %d", code, stdout, stderr, requests, want, tc.requests)
			}
			if _, err := os.Stat(filepath.Join(ws, ".faultline")); err == nil {
				t.Fatal("store directory created by a refused invocation")
			}
		})
	}
}

func witnessLiveReplayWorkspace(t *testing.T, ws, diagnostic string) (dir, want string) {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(ws)
	if err != nil {
		t.Fatal(err)
	}
	// Windows cwd preserves the launch spelling (including 8.3 aliases),
	// while POSIX cwd is physical. Launch from the same resolved path that
	// supplies the dynamic diagnostic tokens on both platforms.
	return resolved, witnessLiveReplayExpectedStderr(diagnostic, resolved, runtime.GOOS == "windows")
}

func witnessLiveReplayExpectedStderr(diagnostic, ws string, windows bool) string {
	separator := "/"
	if windows {
		separator = `\`
	}
	root := ws + separator + "overlays"
	// Only these owned path tokens vary; the committed diagnostic wording,
	// relative paths and all actual stderr bytes remain untouched.
	const inspectedToken = `'<WS>/overlays/a\x00b'`
	if strings.Contains(diagnostic, inspectedToken) {
		path := root + separator + "a\x00b"
		// The short NUL fixture stays below util.inspect's 128-unit cap.
		// Match only its path literal's quoting/escaping, independently of
		// the production formatter (see nodeproc/async_test.go's frozen cases).
		quote := "'"
		if strings.Contains(path, "'") {
			if !strings.Contains(path, `"`) {
				quote = `"`
			} else if !strings.Contains(path, "`") && !strings.Contains(path, "${") {
				quote = "`"
			}
		}
		escaped := strings.NewReplacer(`\`, `\\`, "\x00", `\x00`).Replace(path)
		if quote == "'" {
			escaped = strings.ReplaceAll(escaped, "'", `\'`)
		}
		diagnostic = strings.ReplaceAll(diagnostic, inspectedToken, quote+escaped+quote)
	}
	diagnostic = strings.ReplaceAll(diagnostic, "<WS>/overlays", root)
	return "FaultLine error: " + diagnostic + "\nNext: pnpm fl help\n"
}
