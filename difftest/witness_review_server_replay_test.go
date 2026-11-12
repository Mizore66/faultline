//go:build faultline_test

package difftest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/servefixture"
	"github.com/Mizore66/faultline/internal/incident"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/proofview"
)

const witnessReviewReplayProposal = `{"proposalId":"p1","incidentPacket":{"symptom":"refund currency","ciLog":"Expected USD; received EUR.",
"repositoryLanguage":"JavaScript","repositorySummary":"A Node service that completes refunds."},
"witness":{"behavior":"b","command":"node witness.mjs","overlays":[{"path":"witness.mjs","bytesBase64":"Y29uc29sZS5sb2coMSk="}],
"policy":{"network":"disabled","credentials":"redacted","timeoutSeconds":30}}}`

func witnessReviewDiagnostics(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "difftest", "testdata", "witness-review", "cli-diagnostics.jsonl"))
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
	return texts
}

// TestWitnessReviewServerReplay runs the covered fl as a real review server
// and compares the refusal pages it serves with the hand-committed
// diagnostics, so the coverage gate sees every browser-reachable refusal
// execute. The server is stopped with a real SIGINT so its counters flush.
func TestWitnessReviewServerReplay(t *testing.T) {
	texts := witnessReviewDiagnostics(t)
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "proposal.json"), []byte(witnessReviewReplayProposal), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := runFl(t, work, nil, "witness", "propose", "--input", "proposal.json"); code != 0 {
		t.Fatalf("propose: %d %q", code, stderr)
	}
	jsonOut, stderr, code := runFl(t, work, nil, "witness", "review", "p1", "--json")
	if code != 0 {
		t.Fatalf("review --json: %d %q", code, stderr)
	}
	review, err := jsjson.Parse(jsonOut)
	if err != nil {
		t.Fatal(err)
	}
	proposal := review.Get("review", "proposal")
	reviewDigest := review.Get("review", "reviewDigest").Str()

	// A repository with two commits for the empty-range page.
	repo := filepath.Join(work, "repo")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.test", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "one")
	first := git("rev-parse", "HEAD")
	git("commit", "-q", "--allow-empty", "-m", "two")
	second := git("rev-parse", "HEAD")

	drafts := filepath.Join(work, "drafts-store")
	writeDraft := func(bound bool, repository, ancestor, descendant string) {
		t.Helper()
		_ = os.Remove(filepath.Join(drafts, "drafts", "p1.json"))
		in := incident.DraftInput{ID: "p1", CreatedAt: "2026-07-16T14:00:00.000Z", Repository: repository, Command: "node witness.mjs",
			Range: jsjson.MustObject("ancestor", jsjson.MakeString(ancestor), "descendant", jsjson.MakeString(descendant))}
		if bound {
			in.Proposal = jsjson.MustObject("proposalId", proposal.Get("proposalId"), "proposalDigest", proposal.Get("proposalDigest"),
				"incidentPacketDigest", proposal.Get("incidentPacketDigest"), "commandDigest", proposal.Get("witness", "commandDigest"))
		}
		draft, err := incident.CreateDraft(in)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := incident.WriteDraft(drafts, draft); err != nil {
			t.Fatal(err)
		}
	}

	pattern := regexp.MustCompile(`^FaultLine local witness review: (http://127\.0\.0\.1:[1-9][0-9]*)\nApprove & freeze is one action\. Press Ctrl\+C to stop\.\n$`)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	proc, err := servefixture.StartWithOptions(ctx, flBinary, nil,
		[]string{"witness", "review", "p1", "--draft-store", drafts}, os.Environ(), work,
		servefixture.StartOptions{StartupLines: 2, StartupDescription: "witness review startup", ValidateStartup: func(out []byte) (string, error) {
			m := pattern.FindStringSubmatch(string(out))
			if m == nil {
				return "", os.ErrInvalid
			}
			return m[1], nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := proc.Dispose(); err != nil {
			t.Errorf("teardown: %v", err)
		}
	})

	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	do := func(method, path, body string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(method, proc.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, string(b)
	}
	requirePage := func(label string, status int, got int, body, id string) {
		t.Helper()
		want := proofview.EscapeHTML(texts[id])
		if texts[id] == "" || got != status || !strings.Contains(body, want) {
			t.Fatalf("%s: status=%d want %d, committed %q not in %.300q", label, got, status, texts[id], body)
		}
	}

	// No draft file: a standalone proposal page.
	status, body := do("GET", "/", "")
	requirePage("standalone", 200, status, body, "witness-review-standalone")
	token := regexp.MustCompile(`name="token" value="([0-9a-f]{64})"`).FindStringSubmatch(body)
	if token == nil {
		t.Fatalf("no token in %.200q", body)
	}
	form := func(extra url.Values) string {
		v := url.Values{"token": {token[1]}, "reviewDigest": {reviewDigest}, "approvedBy": {"Replay Reviewer"}}
		for k, vs := range extra {
			v[k] = vs
		}
		return v.Encode()
	}

	status, body = do("POST", "/approve-freeze", url.Values{"reviewDigest": {reviewDigest}}.Encode())
	requirePage("missing token", 403, status, body, "witness-review-missing-token")
	status, body = do("POST", "/approve-freeze", url.Values{"token": {strings.Repeat("é", 64)}}.Encode())
	requirePage("token byte length", 409, status, body, "witness-review-token-length")
	status, body = do("POST", "/approve-freeze", form(nil))
	requirePage("atomic acknowledgement", 409, status, body, "witness-review-ack-atomic")
	status, body = do("POST", "/approve", form(nil))
	requirePage("approve acknowledgement", 409, status, body, "witness-review-ack-approve")
	status, body = do("POST", "/approve-freeze", form(url.Values{"acknowledged": {"exact-witness-reviewed"}, "reviewDigest": {"sha256:" + strings.Repeat("0", 64)}}))
	requirePage("confirmation mismatch", 409, status, body, "witness-review-confirmation-mismatch")
	status, body = do("POST", "/approve-freeze", "x="+strings.Repeat("a", 40000))
	requirePage("oversized form", 409, status, body, "witness-review-form-too-large")

	// Draft states, replaced between requests.
	writeDraft(false, filepath.Join(work, "absent-repo"), first, second)
	status, body = do("GET", "/", "")
	requirePage("unbound draft", 409, status, body, "witness-review-draft-unbound")
	requirePage("integrity next step", 409, status, body, "witness-review-integrity-next")
	writeDraft(true, filepath.Join(work, "absent-repo"), first, second)
	status, body = do("GET", "/", "")
	requirePage("git unavailable", 200, status, body, "witness-review-git-unavailable")
	requirePage("proof boundary footer", 200, status, body, "witness-review-footer")
	writeDraft(true, repo, second, first)
	status, body = do("GET", "/", "")
	requirePage("empty range", 200, status, body, "witness-review-no-commits")

	// A draft bound to another proposal digest.
	_ = os.Remove(filepath.Join(drafts, "drafts", "p1.json"))
	mismatch, err := incident.CreateDraft(incident.DraftInput{ID: "p1", CreatedAt: "2026-07-16T14:00:00.000Z", Repository: repo, Command: "node witness.mjs",
		Range: jsjson.MustObject("ancestor", jsjson.MakeString(first), "descendant", jsjson.MakeString(second)),
		Proposal: jsjson.MustObject("proposalId", proposal.Get("proposalId"), "proposalDigest", jsjson.MakeString("sha256:"+strings.Repeat("1", 64)),
			"incidentPacketDigest", proposal.Get("incidentPacketDigest"), "commandDigest", proposal.Get("witness", "commandDigest"))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := incident.WriteDraft(drafts, mismatch); err != nil {
		t.Fatal(err)
	}
	status, body = do("GET", "/", "")
	requirePage("draft mismatch", 409, status, body, "witness-review-draft-mismatch")

	if err := proc.Interrupt(ctx, serveBundleConsoleHelper(t)); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	result, err := proc.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode == nil || *result.ExitCode != 0 || result.Signal != nil || result.Stderr != "" {
		t.Fatalf("after SIGINT: %+v", result)
	}
}
