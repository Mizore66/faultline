package repair

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func unboundInvestigation(t *testing.T) jsjson.Value {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "difftest", "testdata", "bases", "git-unbound", "investigation.json"))
	if err != nil {
		t.Fatal(err)
	}
	return mustParse(t, string(b))
}

func TestEvidencePacketFactsAndDigest(t *testing.T) {
	packet, err := CreateEvidencePacket(unboundInvestigation(t))
	if err != nil {
		t.Fatal(err)
	}
	facts := packet.Get("facts").Items()
	if len(facts) != 4 || facts[0].Get("id").Str() != "E1" || facts[3].Get("id").Str() != "E4" ||
		!strings.HasPrefix(facts[1].Get("statement").Str(), "PASS_TO_FAIL transition: ") {
		t.Fatalf("%s", jsjson.Stringify(packet))
	}
	if !PacketDigestValid(packet) {
		t.Fatal("digest")
	}
}

func TestValidateBriefCitesUnknownIDs(t *testing.T) {
	packet, _ := CreateEvidencePacket(unboundInvestigation(t))
	brief := mustParse(t, `{"schemaVersion":"faultline.repair-brief.v1","evidencePacketDigest":"`+packet.Get("packetDigest").Str()+`",
		"proposedInvariant":{"statement":"s","evidenceIds":["E9","E1","E9"]},"repairDirections":[{"statement":"d","evidenceIds":["E2"]}],
		"prevention":{"hardEnforcement":[{"statement":"h","evidenceIds":["E3"]}],"softGuidance":[]},"uncertainties":["u"]}`)
	v := ValidateBrief(packet, brief)
	if v.Valid || len(v.Errors) != 2 || v.Errors[0] != "repair brief cites an unknown evidence ID: E9" || v.Brief.Kind() != jsjson.Undefined {
		t.Fatalf("%+v", v)
	}
}
