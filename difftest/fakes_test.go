//go:build faultline_test && unix

package difftest

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFakeCodexRecordsArgvStdinAndEnv(t *testing.T) {
	repo := repoRoot(t)
	r := makeWriterRoots(t)
	bin := installFakeCodex(t, repo, r, "edits") // <HOME>/fakecodex/codex with the log path written in
	work := t.TempDir()
	cmd := exec.Command(bin, "exec", "--full-auto", "fix it")
	cmd.Dir = work
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	cmd.Stdin = strings.NewReader("fix it")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	records := readArgvLog(r.argvLog)
	if len(records) != 1 || !strings.HasPrefix(records[0], "codex exec\x1f--full-auto\x1ffix it\x1fSTDIN=fix it\x1fPATH=") ||
		!strings.HasSuffix(records[0], "\x1fENV=") {
		t.Fatalf("record %q", records)
	}
	if b, _ := os.ReadFile(filepath.Join(work, "state.txt")); string(b) != "good\n" {
		t.Fatalf("state.txt %q", b)
	}
}

// TestFakeCodexEnvIgnoresOnlyHelperAddedAwkNames: some awks (gawk on the
// hosted Ubuntu images) add AWKPATH and AWKLIBPATH to their own ENVIRON. The
// record must not count them when the fake's shell never had them, and must
// still count them, even empty, when fl really passed them.
func TestFakeCodexEnvIgnoresOnlyHelperAddedAwkNames(t *testing.T) {
	realAwk, err := exec.LookPath("awk")
	if err != nil {
		t.Skip("no awk")
	}
	repo := repoRoot(t)
	for _, tc := range []struct {
		name string
		env  []string
		want string
	}{
		{"helper-added-only", nil, ""},
		{"unrelated-name", []string{"FL_UNRELATED=1"}, "FL_UNRELATED"},
		{"passed-empty", []string{"AWKPATH=", "AWKLIBPATH="}, "AWKLIBPATH,AWKPATH"},
		{"passed-nonempty", []string{"AWKPATH=/p", "AWKLIBPATH=/l"}, "AWKLIBPATH,AWKPATH"},
		{"passed-awkpath-only", []string{"AWKPATH="}, "AWKPATH"},
		{"passed-awklibpath-only", []string{"AWKLIBPATH=/l", "FL_UNRELATED=1"}, "AWKLIBPATH,FL_UNRELATED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := makeWriterRoots(t)
			bin := installFakeCodex(t, repo, r, "edits")
			wrap := t.TempDir()
			script := "#!/bin/sh\nAWKPATH=/helper AWKLIBPATH=/helper\nexport AWKPATH AWKLIBPATH\nexec '" + realAwk + "' \"$@\"\n"
			if err := os.WriteFile(filepath.Join(wrap, "awk"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bin, "exec", "--full-auto", "fix it")
			cmd.Dir = t.TempDir()
			cmd.Env = append([]string{"PATH=" + wrap + string(os.PathListSeparator) + os.Getenv("PATH")}, tc.env...)
			cmd.Stdin = strings.NewReader("fix it")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			records := readArgvLog(r.argvLog)
			if len(records) != 1 || !strings.HasSuffix(records[0], "\x1fENV="+tc.want) {
				t.Fatalf("want ENV=%s: %q", tc.want, records)
			}
		})
	}
}

func TestFakeOpenAIRecordsRequestAndReplies(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "reply.json"), []byte(`{"status":500,"contentType":"text/plain","body":"upstream down"}`), 0o644)
	log := filepath.Join(t.TempDir(), "argv.log")
	url := startFakeOpenAI(t, ws, "reply.json", log)
	req, _ := http.NewRequest("POST", url, strings.NewReader(`{"model":"gpt-5.6"}`))
	req.Header.Set("Authorization", "Bearer fl-test-key")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 500 || string(body) != "upstream down" {
		t.Fatalf("%d %q", resp.StatusCode, body)
	}
	want := "openai\x1fPOST\x1f/v1/responses\x1fBearer fl-test-key\x1fapplication/json\x1f{\"model\":\"gpt-5.6\"}"
	if got := readArgvLog(log); len(got) != 1 || got[0] != want {
		t.Fatalf("%q", got)
	}
}

func TestInheritedOutDropsGitHub(t *testing.T) {
	for _, name := range []string{"GITHUB_SHA", "GITHUB_ACTIONS", "GITHUB_REPOSITORY", "OPENAI_API_KEY", "GIT_DIR", "FAULTLINE_CODEX_BIN"} {
		if !inheritedOut(name) {
			t.Errorf("%s is inherited", name)
		}
	}
}

func TestWrittenArtifactDetectors(t *testing.T) {
	if p, d, ok := writtenAttestation("{\n  \"status\": \"ATTESTED\",\n  \"path\": \"/s/r1.json\",\n  \"receiptDigest\": \"sha256:aa\"\n}\n"); !ok || p != "/s/r1.json" || d != "sha256:aa" {
		t.Fatal(p, d, ok)
	}
	if _, _, ok := writtenRepairBrief("{\"status\":\"PERSISTED\"}\n"); ok {
		t.Fatal("no directory: not a brief")
	}
	if d, r, ok := writtenRepairPrevention(`{"status":"REPAIR_CANDIDATE_VERIFIED","preventionExport":{"ok":true,"directory":"/p","rootDigest":"sha256:bb"}}`); !ok || d != "/p" || r != "sha256:bb" {
		t.Fatal(d, r, ok)
	}
	if _, _, ok := writtenRepairPrevention(`{"preventionExport":{"ok":false,"reasons":["x"]}}`); ok {
		t.Fatal("a failed export is not a package")
	}
}
