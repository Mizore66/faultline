package repair

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func chdir(t *testing.T, dir string) {
	t.Helper()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
}

func validBrief(t *testing.T, packetDigest string) string {
	return `{"schemaVersion":"faultline.repair-brief.v1","evidencePacketDigest":"` + packetDigest + `",
		"proposedInvariant":{"statement":"s","evidenceIds":["E1"]},"repairDirections":[{"statement":"d","evidenceIds":["E2"]}],
		"prevention":{"hardEnforcement":[{"statement":"h","evidenceIds":["E3"]}],"softGuidance":[]},"uncertainties":["u"]}`
}

func TestWriteThenVerifyBriefArtifact(t *testing.T) {
	packet, _ := CreateEvidencePacket(unboundInvestigation(t))
	chdir(t, t.TempDir())
	out := filepath.Join(DefaultBriefRoot(), "b1")
	w, err := WriteBriefArtifact(out, packet, mustParse(t, validBrief(t, packet.Get("packetDigest").Str())), Source{Kind: "OFFLINE_INPUT"})
	if err != nil {
		t.Fatal(err)
	}
	d := w.Manifest.Get("manifestDigest").Str()
	v := VerifyBriefArtifact(w.Directory, &d)
	if !v.Valid || v.ExternalDigestStatus != "MATCH" {
		t.Fatalf("%+v", v)
	}
	if _, err := WriteBriefArtifact(out, packet, mustParse(t, validBrief(t, packet.Get("packetDigest").Str())), Source{Kind: "OFFLINE_INPUT"}); err == nil ||
		!strings.HasPrefix(err.Error(), "Repair brief output already exists and will not be replaced: ") {
		t.Fatalf("%v", err)
	}
	if _, err := WriteBriefArtifact(filepath.Join(t.TempDir(), "x"), packet, mustParse(t, validBrief(t, packet.Get("packetDigest").Str())), Source{Kind: "OFFLINE_INPUT"}); err == nil ||
		!strings.HasPrefix(err.Error(), "Repair brief output must be a child directory of ") {
		t.Fatalf("%v", err)
	}
}
