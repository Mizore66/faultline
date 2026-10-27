package gitproof

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	s "github.com/Mizore66/faultline/internal/schema"
)

func TestVerifyLedgerFileUnreadable(t *testing.T) {
	v := VerifyCodexLifecycleLedgerFile(filepath.Join(t.TempDir(), "missing.json"))
	if v.Valid || v.HeadHash != nil || len(v.Errors) != 1 || !strings.HasPrefix(v.Errors[0], "Unable to read lifecycle ledger ") {
		t.Fatalf("%+v", v)
	}
}

func TestVerifyLedgerFileSchemaAndValid(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte(`{"schemaVersion":"x"}`), 0o644)
	v := VerifyCodexLifecycleLedgerFile(bad)
	if v.Valid || v.HeadHash != nil || v.EventCount != 0 || len(v.Errors) != 1 || !strings.HasPrefix(v.Errors[0], "Lifecycle ledger "+bad+" has invalid schema: ") {
		t.Fatalf("%+v", v)
	}
	good := filepath.Join("..", "..", "..", "difftest", "testdata", "bases", "git-fully-bound", "lifecycle", "ledger.json")
	v = VerifyCodexLifecycleLedgerFile(good)
	if !v.Valid || v.HeadHash == nil || v.EventCount == 0 {
		t.Fatalf("%+v", v)
	}
}

func TestErrorSummary(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{{nil, ""}, {[]string{"a"}, "a"}, {[]string{"a", "b"}, "a; b"}} {
		if got := ErrorSummary(tc.in); got != tc.want {
			t.Errorf("ErrorSummary(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTimestampMs(t *testing.T) {
	if got := TimestampMs("2026-07-16T11:00:00.000Z"); got != 1784199600000 {
		t.Fatal(got)
	}
	if got := TimestampMs("+275760-09-13T00:00:00.000Z"); got != 8.64e15 {
		t.Fatal(got)
	}
	if !math.IsNaN(TimestampMs("2026-07-16T11:00:00Z")) {
		t.Fatal("non-canonical")
	}
}

func TestExportedLedgerSchemas(t *testing.T) {
	if CodexLifecycleLedgerVersion != "faultline.codex-lifecycle-ledger.v1" || GitCheckpointVersion != "faultline.git-checkpoint.v1" {
		t.Fatal("versions")
	}
	// The enum's own options (zod's invalid_enum_value issue lists them in
	// order) must be exactly CodexTransportOptions.
	_, issues, ok := s.Parse(CodexTransportSchema, jsjson.MakeString("NOT_A_TRANSPORT"))
	if ok || len(issues) != 1 {
		t.Fatalf("%v %v", ok, issues)
	}
	var options []string
	for _, o := range issues[0].Fields.Field("options").Items() {
		options = append(options, o.Str())
	}
	if !slices.Equal(options, CodexTransportOptions) {
		t.Fatalf("CodexTransportOptions %q, schema options %q", CodexTransportOptions, options)
	}
	ledger := baseFile(t, "git-fully-bound", "lifecycle/ledger.json")
	events := ledger.Get("events").Items()
	first := events[0]
	if first.Get("previousHash").Str() != LedgerGenesisHash(ledger) {
		t.Fatal("genesis")
	}
	unsigned := jsjson.NewObj()
	for _, k := range []string{"schemaVersion", "ledgerId", "sessionId", "sequence", "eventId", "occurredAt", "event", "previousHash"} {
		unsigned.Set(k, first.Get(k))
	}
	if HashLifecycleEvent(jsjson.MakeObject(unsigned)) != first.Get("hash").Str() {
		t.Fatal("hash")
	}
	unsigned.Set("eventId", jsjson.MakeString("bad id!"))
	err := jsexc.Try(func() { HashLifecycleEvent(jsjson.MakeObject(unsigned)) })
	if err == nil || !strings.Contains(jsexc.Message(err), "Identifier contains unsupported characters") {
		t.Fatalf("%v", err)
	}
}
