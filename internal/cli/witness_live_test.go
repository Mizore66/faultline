//go:build faultline_test

package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

const validLiveProposalJSON = `{"behavior":"Preserve refund currency.","command":"node witness.mjs","overlayFiles":[],"expectedExitCode":0,"timeoutSeconds":30,"safetyNotes":[]}`

const liveIncidentJSON = `{"symptom":"refund fails","ciLog":"Expected USD; received EUR.","repositoryLanguage":"JavaScript","repositorySummary":"payments"}`

const liveOfflineJSON = `{"proposalId":"offline-1","incidentPacket":{"symptom":"s","ciLog":"c","repositoryLanguage":"Text","repositorySummary":"r"},
"witness":{"behavior":"b","command":"node w.mjs","overlays":[{"path":"w.mjs","bytesBase64":"YQ=="}],
"policy":{"network":"disabled","credentials":"redacted","timeoutSeconds":30}}}`

type liveServer struct {
	mu     sync.Mutex
	bodies []string
	auths  []string
}

func outputEnvelope(proposal string) string {
	return `{"output":[{"type":"message","content":[{"type":"output_text","text":` + jsjson.Stringify(jsjson.MakeString(proposal)) + `}]}]}`
}

// liveWorkspace chdirs into a fresh directory holding incident.json,
// offline.json and an empty overlays directory, with a scripted provider
// answering responseFixture, and returns the workspace.
func liveWorkspace(t *testing.T, responseFixture string) (string, *liveServer) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{"incident.json": liveIncidentJSON, "offline.json": liveOfflineJSON} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "overlays"), 0o700); err != nil {
		t.Fatal(err)
	}
	s := &liveServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.bodies = append(s.bodies, string(b))
		s.auths = append(s.auths, r.Header.Get("Authorization"))
		s.mu.Unlock()
		io.WriteString(w, outputEnvelope(responseFixture))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAULTLINE_TEST_OPENAI_URL", srv.URL+"/v1/responses")
	t.Setenv("OPENAI_API_KEY", "env-key")
	t.Chdir(dir)
	return dir, s
}

func storeRecords(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, ".faultline", "witnesses", "proposals"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func runWitnessLiveWithProvider(t *testing.T, args []string, responseFixture string) (stdout, stderr string, exitCode, requests, stores int) {
	t.Helper()
	dir, s := liveWorkspace(t, responseFixture)
	return runLiveIn(t, dir, s, args)
}

func runLiveIn(t *testing.T, dir string, s *liveServer, args []string) (string, string, int, int, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(append([]string{"witness", "propose"}, args...), &out, &errb)
	s.mu.Lock()
	defer s.mu.Unlock()
	return out.String(), errb.String(), code, len(s.bodies), storeRecords(t, dir)
}

func TestWitnessProposeLive(t *testing.T) {
	t.Run("selection", func(t *testing.T) {
		for _, tc := range []struct {
			name                     string
			args                     []string
			wantRequests, wantStores int
		}{
			{"live incident ignores input", []string{"--live", "--incident", "incident.json", "--input", "offline.json", "--proposal-id", "p1", "--overlay-root", "overlays"}, 1, 1},
			{"live preview has no store", []string{"--live"}, 1, 0},
			{"input stays offline", []string{"--live", "--input", "offline.json"}, 0, 1},
			{"sample without input", []string{"--incident", "incident.json"}, 0, 0},
		} {
			t.Run(tc.name, func(t *testing.T) {
				stdout, stderr, exitCode, requests, stores := runWitnessLiveWithProvider(t, tc.args, validLiveProposalJSON)
				if exitCode != 0 || requests != tc.wantRequests || stores != tc.wantStores {
					t.Fatalf("requests=%d want %d; stores=%d want %d; exit=%d stdout=%q stderr=%q", requests, tc.wantRequests, stores, tc.wantStores, exitCode, stdout, stderr)
				}
			})
		}
	})

	t.Run("persisted output and record", func(t *testing.T) {
		dir, s := liveWorkspace(t, validLiveProposalJSON)
		stdout, stderr, code, requests, stores := runLiveIn(t, dir, s, []string{"--live", "--incident", "incident.json", "--proposal-id", "p1", "--overlay-root", "overlays"})
		if code != 0 || stderr != "" || requests != 1 || stores != 1 {
			t.Fatalf("%d %q %d %d", code, stderr, requests, stores)
		}
		v, err := jsjson.Parse(stdout)
		if err != nil {
			t.Fatal(err)
		}
		keys := v.Obj().Keys()
		if strings.Join(keys, ",") != "status,source,store,proposalId,proposalDigest,incidentPacketDigest,privacy" ||
			v.Get("status").Str() != "PROPOSED" || v.Get("source").Str() != "GPT-6.1" || v.Get("proposalId").Str() != "p1" ||
			!strings.HasSuffix(stdout, "\n}\n") || !strings.HasPrefix(stdout, "{\n  \"status\"") {
			t.Fatalf("stdout=%q", stdout)
		}
		resolvedDir, _ := filepath.EvalSymlinks(dir)
		if got, _ := filepath.EvalSymlinks(v.Get("store").Str()); got != filepath.Join(resolvedDir, ".faultline", "witnesses") {
			t.Fatalf("store=%q", v.Get("store").Str())
		}
		path := filepath.Join(dir, ".faultline", "witnesses", "proposals", "p1.json")
		record, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if info, _ := os.Stat(path); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", info.Mode())
		}
		text := string(record)
		for _, absent := range []string{"expectedExitCode", "safetyNotes"} {
			if strings.Contains(text, absent) || strings.Contains(stdout, absent) {
				t.Fatalf("%s present: %s", absent, text)
			}
		}
		stored, err := jsjson.Parse(text)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Get("proposalOrigin").Str() != "MODEL" || stored.Get("proposalDigest").Str() != v.Get("proposalDigest").Str() ||
			stored.Get("incidentPacketDigest").Str() != v.Get("incidentPacketDigest").Str() ||
			stored.Get("witness", "command").Str() != "node witness.mjs" || stored.Get("witness", "policy", "timeoutSeconds").Num() != 30 ||
			stored.Get("witness", "policy", "network").Str() != "disabled" || !strings.HasSuffix(text, "}\n") {
			t.Fatalf("record=%s", text)
		}
	})

	t.Run("overlays are read and sorted", func(t *testing.T) {
		dir, s := liveWorkspace(t, `{"behavior":"b","command":"c","overlayFiles":["z/w.bin","a.txt"],"expectedExitCode":3,"timeoutSeconds":9,"safetyNotes":["n"]}`)
		os.MkdirAll(filepath.Join(dir, "overlays", "z"), 0o700)
		os.WriteFile(filepath.Join(dir, "overlays", "z", "w.bin"), []byte("\x00\r\n\xff"), 0o600)
		os.WriteFile(filepath.Join(dir, "overlays", "a.txt"), []byte("hi"), 0o600)
		stdout, stderr, code, requests, stores := runLiveIn(t, dir, s, []string{"--live", "--incident", "incident.json", "--proposal-id", "p2", "--overlay-root", "overlays"})
		if code != 0 || requests != 1 || stores != 1 {
			t.Fatalf("%d %q %q", code, stdout, stderr)
		}
		record, _ := os.ReadFile(filepath.Join(dir, ".faultline", "witnesses", "proposals", "p2.json"))
		for _, want := range []string{`"path":"a.txt"`, `"bytesBase64":"aGk="`, `"path":"z/w.bin"`, `"bytesBase64":"AA0K/w=="`, `"timeoutSeconds":9`} {
			if !strings.Contains(string(record), want) {
				t.Fatalf("missing %s in %s", want, record)
			}
		}
		if strings.Contains(string(record), "expectedExitCode") || strings.Contains(string(record), "safetyNotes") {
			t.Fatalf("record=%s", record)
		}
	})

	t.Run("failure order", func(t *testing.T) {
		base := []string{"--live", "--incident", "incident.json"}
		cases := []struct {
			name, stderr, response string
			args                   []string
			requests               int
			unsetKey               bool
		}{
			{name: "missing incident file", args: []string{"--live", "--incident", "nope.json", "--proposal-id", "p", "--overlay-root", "overlays"},
				stderr: "FaultLine error: Unable to read JSON input nope.json: "},
			{name: "missing proposal id", args: append(append([]string{}, base...), "--overlay-root", "overlays"),
				stderr: "FaultLine error: Missing required option: --proposal-id\nNext: pnpm fl help\n"},
			{name: "missing root", args: append(append([]string{}, base...), "--proposal-id", "p"),
				stderr: "FaultLine error: Missing required option: --overlay-root\nNext: pnpm fl help\n"},
			{name: "missing key", args: append(append([]string{}, base...), "--proposal-id", "p", "--overlay-root", "overlays"), unsetKey: true,
				stderr: "FaultLine error: OPENAI_API_KEY is required for a live GPT-5.6 witness proposal. The judge demo does not require one.\nNext: pnpm fl help\n"},
			{name: "bad root after provider", args: append(append([]string{}, base...), "--proposal-id", "p", "--overlay-root", "no-such-root"), requests: 1,
				stderr: "FaultLine error: ENOENT"},
			{name: "overlay failure after provider", args: append(append([]string{}, base...), "--proposal-id", "p", "--overlay-root", "overlays"), requests: 1,
				response: `{"behavior":"b","command":"c","overlayFiles":["../x"],"expectedExitCode":0,"timeoutSeconds":5,"safetyNotes":[]}`,
				stderr:   "FaultLine error: Model proposed an unsafe overlay path: ../x\nNext: pnpm fl help\n"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				response := tc.response
				if response == "" {
					response = validLiveProposalJSON
				}
				dir, s := liveWorkspace(t, response)
				if tc.unsetKey {
					t.Setenv("OPENAI_API_KEY", "")
				}
				stdout, stderr, code, requests, stores := runLiveIn(t, dir, s, tc.args)
				if code != 1 || stdout != "" || requests != tc.requests || stores != 0 || !strings.HasPrefix(stderr, tc.stderr) {
					t.Fatalf("code=%d stdout=%q stderr=%q requests=%d stores=%d", code, stdout, stderr, requests, stores)
				}
				if _, err := os.Stat(filepath.Join(dir, ".faultline")); err == nil {
					t.Fatal("store directory created")
				}
			})
		}
	})

	t.Run("second invocation repeats provider then refuses", func(t *testing.T) {
		dir, s := liveWorkspace(t, validLiveProposalJSON)
		args := []string{"--live", "--incident", "incident.json", "--proposal-id", "p1", "--overlay-root", "overlays"}
		if _, _, code, _, _ := runLiveIn(t, dir, s, args); code != 0 {
			t.Fatal(code)
		}
		path := filepath.Join(dir, ".faultline", "witnesses", "proposals", "p1.json")
		first, _ := os.ReadFile(path)
		stdout, stderr, code, requests, stores := runLiveIn(t, dir, s, args)
		after, _ := os.ReadFile(path)
		if code != 1 || stdout != "" || requests != 2 || stores != 1 || !bytes.Equal(first, after) || !strings.HasPrefix(stderr, "FaultLine error: ") {
			t.Fatalf("code=%d stdout=%q stderr=%q requests=%d", code, stdout, stderr, requests)
		}
	})

	t.Run("models", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			args []string
			want string
		}{
			{"default", nil, `"model":"gpt-6.1"`},
			{"custom", []string{"--model", "gpt-test"}, `"model":"gpt-test"`},
			{"empty", []string{"--model", ""}, `"model":""`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				dir, s := liveWorkspace(t, validLiveProposalJSON)
				args := append([]string{"--live", "--incident", "incident.json", "--proposal-id", "m", "--overlay-root", "overlays"}, tc.args...)
				stdout, stderr, code, requests, _ := runLiveIn(t, dir, s, args)
				if code != 0 || requests != 1 || !strings.HasPrefix(s.bodies[0], "{"+tc.want) || !strings.Contains(stdout, `"source": "GPT-6.1"`) {
					t.Fatalf("code=%d %q %q body=%s", code, stdout, stderr, s.bodies)
				}
			})
		}
	})

	t.Run("preview output", func(t *testing.T) {
		_, s := liveWorkspace(t, validLiveProposalJSON)
		var out, errb bytes.Buffer
		if code := Run([]string{"witness", "propose", "--live", "--model", "gpt-test"}, &out, &errb); code != 0 {
			t.Fatalf("%d %q", code, errb.String())
		}
		v, err := jsjson.Parse(out.String())
		if err != nil || strings.Join(v.Obj().Keys(), ",") != "proposal,incidentPacketDigest,redaction" ||
			!strings.Contains(s.bodies[0], "A completed refund may overwrite settlement currency with display currency.") {
			t.Fatalf("%v %q", err, out.String())
		}
	})
}
