package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/proofview"
	"github.com/Mizore66/faultline/internal/witness"
)

func ttyProposal(t *testing.T, id string) string {
	t.Helper()
	store := t.TempDir()
	v, err := jsjson.Parse(`{"proposalId":"` + id + `","proposedAt":"2026-01-02T03:04:05.000Z","incidentPacket":{"symptom":"refund currency","ciLog":"Expected USD; received EUR.",
		"repositoryLanguage":"JavaScript","repositorySummary":"A Node service that completes refunds."},
		"witness":{"behavior":"b","command":"node witness.mjs",
		"overlays":[{"path":"witness.mjs","bytesBase64":"Y29uc29sZS5sb2coMSk="}],
		"policy":{"network":"disabled","credentials":"redacted","timeoutSeconds":30}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := witness.Propose(store, v); err != nil {
		t.Fatal(err)
	}
	return store
}

func recordCount(t *testing.T, store string) int {
	t.Helper()
	n := 0
	_ = filepath.Walk(store, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && !strings.Contains(p, "proposals") {
			n++
		}
		return nil
	})
	return n
}

func TestNonTtyReviewRefusalMessage(t *testing.T) {
	if got, want := NonTtyReviewRefusalMessage(""),
		"Refusing non-interactive approval. Start `fl witness review <id> --browser` on an interactive terminal and open the printed URL. FaultLine never auto-approves under yes| piping."; got != want {
		t.Fatalf("got %q", got)
	}
	if got, want := NonTtyReviewRefusalMessage("http://127.0.0.1:9/"),
		"Refusing non-interactive approval. Open the local review URL: http://127.0.0.1:9/ FaultLine never auto-approves under yes| piping."; got != want {
		t.Fatalf("got %q", got)
	}
}

func witnessReviewTTYRefusesNonTTY(t *testing.T) {
	for _, c := range []struct {
		name        string
		stdin, out  bool
		wantMessage string
	}{
		{"stdin", false, true, "TTY-native approval requires an interactive terminal. Open the local review URL instead; FaultLine will not auto-approve under piped stdin."},
		{"stdout", true, false, "TTY-native approval requires an interactive stdout. Open the local review URL instead."},
		{"both", false, false, "TTY-native approval requires an interactive terminal. Open the local review URL instead; FaultLine will not auto-approve under piped stdin."},
	} {
		t.Run(c.name, func(t *testing.T) {
			store := ttyProposal(t, "p1")
			var stdout strings.Builder
			name := "R"
			_, _, _, err := RunWitnessReviewTTY(WitnessReviewTTYOptions{
				StoreDirectory: store, ProposalID: "p1", ApprovedBy: &name,
				Stdin: strings.NewReader("yes\n"), Stdout: &stdout,
				StdinIsTTY: c.stdin, StdoutIsTTY: c.out,
			})
			if err == nil || err.Error() != c.wantMessage {
				t.Fatalf("err = %v", err)
			}
			if stdout.Len() != 0 || recordCount(t, store) != 0 {
				t.Fatal("output or records written")
			}
		})
	}
}

func witnessReviewTTYAnswers(t *testing.T) {
	for _, c := range []struct {
		name, input string
		approve     bool
	}{
		{"y", "y\n", true},
		{"YES padded", " YES \n", true},
		{"no", "n\n", false},
		{"empty", "\n", false},
		{"eof", "", false},
		{"yes without newline", "yes", true},
		{"yep", "yep\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			store := ttyProposal(t, "p1")
			var stdout strings.Builder
			approvedBy := "Test Reviewer"
			approval, frozen, actor, err := RunWitnessReviewTTY(WitnessReviewTTYOptions{
				StoreDirectory: store, ProposalID: "p1", ApprovedBy: &approvedBy,
				Stdin: strings.NewReader(c.input), Stdout: &stdout,
				StdinIsTTY: true, StdoutIsTTY: true,
			})
			out := stdout.String()
			prompt := `Approve & freeze as "Test Reviewer"? [y/N] `
			if !strings.HasPrefix(out, "FaultLine witness review") || !strings.Contains(out, prompt) {
				t.Fatalf("output %q", out)
			}
			if !c.approve {
				if err == nil || err.Error() != "Approval declined; no records were written." {
					t.Fatalf("err = %v", err)
				}
				if recordCount(t, store) != 0 || strings.Contains(out, "Frozen ") {
					t.Fatal("records written on decline")
				}
				return
			}
			if err != nil || actor != approvedBy {
				t.Fatalf("actor=%q err=%v", actor, err)
			}
			if frozen.Get("proposal", "proposalId").Str() != "p1" || approval.Get("approvedBy").Str() != actor {
				t.Fatal("wrong records")
			}
			want := "Frozen p1 · approvedBy Test Reviewer · frozenDigest " + frozen.Get("frozenDigest").Str() + "\n"
			if !strings.HasSuffix(out, prompt+want) && !strings.HasSuffix(out, want) {
				t.Fatalf("output %q", out)
			}
			if recordCount(t, store) == 0 {
				t.Fatal("no records")
			}
		})
	}
}

func witnessReviewTTYRejectsEmptyIdentity(t *testing.T) {
	store := ttyProposal(t, "p1")
	var stdout strings.Builder
	blank := "  "
	_, _, _, err := RunWitnessReviewTTY(WitnessReviewTTYOptions{
		StoreDirectory: store, ProposalID: "p1", ApprovedBy: &blank,
		Stdin: strings.NewReader("y\n"), Stdout: &stdout, StdinIsTTY: true, StdoutIsTTY: true,
	})
	if err == nil || err.Error() != "Reviewer identity is empty." || recordCount(t, store) != 0 {
		t.Fatalf("err = %v", err)
	}
}

func witnessReviewTTYMissingProposal(t *testing.T) {
	var stdout strings.Builder
	name := "R"
	_, _, _, err := RunWitnessReviewTTY(WitnessReviewTTYOptions{
		StoreDirectory: t.TempDir(), ProposalID: "nope", ApprovedBy: &name,
		Stdin: strings.NewReader("y\n"), Stdout: &stdout, StdinIsTTY: true, StdoutIsTTY: true,
	})
	if err == nil || stdout.Len() != 0 {
		t.Fatalf("err = %v out=%q", err, stdout.String())
	}
}

func TestWitnessReviewTTY(t *testing.T) {
	t.Run("RefusesNonTTY", witnessReviewTTYRefusesNonTTY)
	t.Run("Answers", witnessReviewTTYAnswers)
	t.Run("RejectsEmptyIdentity", witnessReviewTTYRejectsEmptyIdentity)
	t.Run("MissingProposal", witnessReviewTTYMissingProposal)
	t.Run("ReviewerFallback", witnessReviewTTYReviewerFallback)
	runWitnessReviewTTYProcessTests(t)
}

func witnessReviewTTYReviewerFallback(t *testing.T) {
	store := ttyProposal(t, "p1")
	var stdout strings.Builder
	_, _, actor, err := RunWitnessReviewTTY(WitnessReviewTTYOptions{
		StoreDirectory: store, ProposalID: "p1",
		Stdin: strings.NewReader("n\n"), Stdout: &stdout, StdinIsTTY: true, StdoutIsTTY: true,
	})
	if err == nil || actor != "" {
		t.Fatalf("err=%v actor=%q", err, actor)
	}
	if !strings.Contains(stdout.String(), `Approve & freeze as "`+proofview.ReadGitUserName()+`"? [y/N] `) {
		t.Fatalf("output %q", stdout.String())
	}
}
