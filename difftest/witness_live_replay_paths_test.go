//go:build faultline_test

package difftest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
)

func TestWitnessLiveReplayExpectedPaths(t *testing.T) {
	const root = "Overlay root must be a real directory: "
	const nul = "The argument 'path' must be a string, Uint8Array, or URL without null bytes. Received "
	const next = "\nNext: pnpm fl help\n"
	for _, tc := range []struct {
		name, ws, diagnostic, want string
		windows                    bool
	}{
		{
			name: "windows-native-root", windows: true,
			ws:         `C:\Users\runneradmin\AppData\Local\Temp\w123`,
			diagnostic: root + "<WS>/overlays",
			want:       "FaultLine error: " + root + `C:\Users\runneradmin\AppData\Local\Temp\w123\overlays` + next,
		},
		{
			name: "windows-short-alias-and-case-root", windows: true,
			ws:         `C:\Users\RUNNER~1\AppData\Local\Temp\MiXeD`,
			diagnostic: root + "<WS>/overlays",
			want:       "FaultLine error: " + root + `C:\Users\RUNNER~1\AppData\Local\Temp\MiXeD\overlays` + next,
		},
		{
			name: "windows-inspected-nul", windows: true,
			ws:         `C:\Users\runneradmin\AppData\Local\Temp\w123`,
			diagnostic: nul + `'<WS>/overlays/a\x00b'`,
			want:       "FaultLine error: " + nul + `'C:\\Users\\runneradmin\\AppData\\Local\\Temp\\w123\\overlays\\a\x00b'` + next,
		},
		{
			name: "windows-inspected-short-alias-and-case", windows: true,
			ws:         `C:\Users\RUNNER~1\Temp\MiXeD`,
			diagnostic: nul + `'<WS>/overlays/a\x00b'`,
			want:       "FaultLine error: " + nul + `'C:\\Users\\RUNNER~1\\Temp\\MiXeD\\overlays\\a\x00b'` + next,
		},
		{
			name: "windows-inspected-apostrophe", windows: true,
			ws:         `C:\Temp\it's`,
			diagnostic: nul + `'<WS>/overlays/a\x00b'`,
			want:       "FaultLine error: " + nul + `"C:\\Temp\\it's\\overlays\\a\x00b"` + next,
		},
		{
			name: "posix-physical-root", ws: "/private/var/tmp/w123",
			diagnostic: root + "<WS>/overlays",
			want:       "FaultLine error: " + root + "/private/var/tmp/w123/overlays" + next,
		},
		{
			name: "posix-inspected-nul", ws: "/private/var/tmp/w123",
			diagnostic: nul + `'<WS>/overlays/a\x00b'`,
			want:       "FaultLine error: " + nul + `'/private/var/tmp/w123/overlays/a\x00b'` + next,
		},
		{
			name: "posix-inspected-literal-backslash", ws: `/tmp/a\b`,
			diagnostic: nul + `'<WS>/overlays/a\x00b'`,
			want:       "FaultLine error: " + nul + `'/tmp/a\\b/overlays/a\x00b'` + next,
		},
		{
			name: "posix-inspected-both-quotes", ws: `/tmp/it's "x"`,
			diagnostic: nul + `'<WS>/overlays/a\x00b'`,
			want:       "FaultLine error: " + nul + "`/tmp/it's \"x\"/overlays/a\\x00b`" + next,
		},
		{
			name: "posix-inspected-all-quotes", ws: "/tmp/it's \"x\" `y`",
			diagnostic: nul + `'<WS>/overlays/a\x00b'`,
			want:       "FaultLine error: " + nul + "'/tmp/it\\'s \"x\" `y`/overlays/a\\x00b'" + next,
		},
		{
			name: "posix-inspected-template-interpolation", ws: `/tmp/it's "${x}"`,
			diagnostic: nul + `'<WS>/overlays/a\x00b'`,
			want:       "FaultLine error: " + nul + `'/tmp/it\'s "${x}"/overlays/a\x00b'` + next,
		},
		{
			name: "only-dynamic-token-changes", windows: true, ws: `C:\Temp\MiXeD`,
			diagnostic: `literal \x00 \tail before <WS>/overlays after a/b`,
			want:       "FaultLine error: " + `literal \x00 \tail before C:\Temp\MiXeD\overlays after a/b` + next,
		},
		{
			name: "path-free-diagnostic-unchanged", windows: true, ws: `C:\Temp\w123`,
			diagnostic: "Model proposed an unsafe overlay path: ../x",
			want:       "FaultLine error: Model proposed an unsafe overlay path: ../x" + next,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := witnessLiveReplayExpectedStderr(tc.diagnostic, tc.ws, tc.windows); got != tc.want {
				t.Fatalf("stderr = %q, want %q", got, tc.want)
			}
		})
	}
}

// A raw alias used for cmd.Dir can differ from the owned physical path used
// in the expectation. Exercise both the returned launch directory and a real
// CLI refusal, rather than treating equal filesystem identity as equal text.
func TestWitnessLiveReplayWorkspace(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	physical := filepath.Join(base, "Real'Workspace")
	if err := os.Mkdir(physical, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "launch-alias")
	if runtime.GOOS == "windows" {
		// Windows EvalSymlinks restores the on-disk case without requiring
		// symlink privileges on the hosted runner.
		alias = filepath.Join(base, "rEAL'wORKSPACE")
	} else if err := os.Symlink(physical, alias); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"incident.json": `{"symptom":"refund fails","ciLog":"Expected USD; received EUR.","repositoryLanguage":"JavaScript","repositorySummary":"payments"}`,
		"overlays":      "not a directory",
	} {
		if err := os.WriteFile(filepath.Join(physical, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ name, ws string }{{"physical", physical}, {"alias", alias}} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				requests.Add(1)
				io.WriteString(w, `{"output":[{"type":"message","content":[{"type":"output_text","text":"{\"behavior\":\"b\",\"command\":\"node witness.mjs\",\"overlayFiles\":[],\"expectedExitCode\":0,\"timeoutSeconds\":30,\"safetyNotes\":[]}"}]}]}`)
			}))
			defer srv.Close()
			const diagnostic = "Overlay root must be a real directory: <WS>/overlays"
			dir, want := witnessLiveReplayWorkspace(t, tc.ws, diagnostic)
			if dir != physical {
				t.Errorf("command directory = %q, want owned physical path %q", dir, physical)
			}
			independentWant := "FaultLine error: Overlay root must be a real directory: " + filepath.Join(physical, "overlays") + "\nNext: pnpm fl help\n"
			if want != independentWant {
				t.Errorf("expected stderr = %q, want %q", want, independentWant)
			}
			stdout, stderr, code := runFl(t, dir, map[string]string{
				"FAULTLINE_TEST_OPENAI_URL": srv.URL + "/v1/responses",
				"OPENAI_API_KEY":            "fl-test-key",
			}, "witness", "propose", "--live", "--incident", "incident.json", "--proposal-id", "live-1", "--overlay-root", "overlays")
			if code != 1 || stdout != "" || stderr != independentWant || requests.Load() != 1 {
				t.Fatalf("code=%d stdout=%q stderr=%q requests=%d, want stderr %q requests 1", code, stdout, stderr, requests.Load(), independentWant)
			}
			if _, err := os.Stat(filepath.Join(physical, ".faultline")); !os.IsNotExist(err) {
				t.Fatalf("refused invocation created a store or stat failed: %v", err)
			}
		})
	}
}
