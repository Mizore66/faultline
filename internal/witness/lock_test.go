//go:build faultline_test

package witness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsjson"
)

func proposalInput(t *testing.T) jsjson.Value {
	v, err := jsjson.Parse(`{"proposalId":"demo-1","incidentPacket":{"symptom":"refund currency","ciLog":"Expected USD; received EUR.",
		"repositoryLanguage":"JavaScript","repositorySummary":"A Node service that completes refunds."},
		"witness":{"behavior":"b","command":"node witness.mjs",
		"overlays":[{"path":"witness.mjs","bytesBase64":"Y29uc29sZS5sb2coMSk="}],
		"policy":{"network":"disabled","credentials":"redacted","timeoutSeconds":30}}}`)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestProposeApproveFreezeVerify(t *testing.T) {
	clock.ResetForTest("witness")
	store := t.TempDir()
	p, err := Propose(store, proposalInput(t))
	if err != nil {
		t.Fatal(err)
	}
	if p.Get("proposedAt").Str() != "2026-07-16T12:00:00.000Z" {
		t.Fatalf("proposedAt %s", p.Get("proposedAt").Str())
	}
	approval, _ := jsjson.Parse(`{"approvedBy":"reviewer@example.test"}`)
	if _, err := Approve(store, "demo-1", approval); err != nil {
		t.Fatal(err)
	}
	frozen, err := Freeze(store, "demo-1", FreezeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	v := VerifyFrozen(store, "demo-1", frozen.Get("frozenDigest").Str(), true)
	if !v.Valid || v.ExternalDigestStatus != "MATCH" {
		t.Fatalf("%+v", v)
	}
	info, _ := os.Stat(filepath.Join(store, "frozen", "demo-1.json"))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
	if _, err := Propose(store, proposalInput(t)); err == nil || !strings.HasPrefix(err.Error(), "Immutable witness lock record already exists: ") {
		t.Fatalf("second propose: %v", err)
	}
}
