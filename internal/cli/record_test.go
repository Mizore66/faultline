package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func jsonString(s string) string { return jsjson.Stringify(jsjson.MakeString(s)) }

func TestRecordInitThenVerify(t *testing.T) {
	dir := t.TempDir()
	ledger := filepath.Join(dir, "l.json")
	stdout, stderr, code := run("record", "init", "--session", "s-1", "--ledger", ledger, "--repo", dir, "--model", "")
	if code != 0 || stderr != "" || !strings.HasPrefix(stdout, "{\n  \"status\": \"RECORDING_STARTED\",\n  \"ledger\": "+jsonString(ledger)+",\n  \"sessionId\": \"s-1\",\n  \"headHash\": \"sha256:") {
		t.Fatalf("%d %q %q", code, stdout, stderr)
	}
	if b, _ := os.ReadFile(ledger); strings.Contains(string(b), "\"model\"") {
		t.Fatal("empty --model must be left out")
	}
	stdout, _, code = run("record", "verify", "--ledger", ledger)
	if code != 0 || !strings.Contains(stdout, "\"valid\": true") {
		t.Fatalf("%d %s", code, stdout)
	}
}

func TestRecordAfterTurnParsing(t *testing.T) {
	for _, v := range []string{"-1", "1.5", "abc", "Infinity"} {
		_, stderr, code := run("record", "checkpoint", "--repo", ".", "--after-turn", v)
		if code != 1 || !strings.Contains(stderr, "--after-turn must be a non-negative integer.") {
			t.Errorf("%s: %d %q", v, code, stderr)
		}
	}
}

func TestRecordStdinRequiresEvents(t *testing.T) {
	var out, errb strings.Builder
	code := RunWithStdin([]string{"record", "stdin", "--ledger", filepath.Join(t.TempDir(), "l.json")}, strings.NewReader("\r\n  \n"), &out, &errb)
	if code != 1 || errb.String() != "FaultLine error: Lifecycle stdin contained no NDJSON events.\nNext: pnpm fl help\n" {
		t.Fatalf("%d %q", code, errb.String())
	}
}

// TestRecordRouter pins the usage text of both entry points and the codex
// router's record branch (src/cli-app.ts:2994: `codex record` before the
// flags).
func TestRecordRouter(t *testing.T) {
	const usage = "FaultLine error: Usage: fl record init|stdin|checkpoint|attach|verify ...\nNext: pnpm fl help\n"
	for _, args := range [][]string{{"record"}, {"record", "nope"}, {"codex", "record"}, {"codex", "record", "--dry-run"}} {
		if _, stderr, code := run(args...); code != 1 || stderr != usage {
			t.Errorf("%q: %d %q", args, code, stderr)
		}
	}
}

// TestRecordStdinEmptyIDsFailTheSchema: an explicit "" eventId or
// occurredAt is passed through (TS `??`), so the append's schema refuses it
// rather than generating one; the line before stays appended.
func TestRecordStdinEmptyIDsFailTheSchema(t *testing.T) {
	dir := t.TempDir()
	ledger := filepath.Join(dir, "l.json")
	if _, stderr, code := run("record", "init", "--session", "s-1", "--ledger", ledger, "--repo", dir); code != 0 {
		t.Fatal(stderr)
	}
	turn := `{"type":"TURN_STARTED","payload":{"turnId":"t-1","turnOrdinal":1,"promptDigest":"sha256:` + strings.Repeat("c", 64) + `"}}`
	for _, c := range []struct{ envelope, want string }{
		{`{"eventId":"","event":` + turn + `}`, "eventId"},
		{`{"occurredAt":"","event":` + turn + `}`, "Expected a canonical ISO-8601 UTC timestamp"},
	} {
		var out, errb strings.Builder
		code := RunWithStdin([]string{"record", "stdin", "--ledger", ledger}, strings.NewReader(c.envelope+"\n"), &out, &errb)
		if code != 1 || !strings.Contains(errb.String(), c.want) {
			t.Errorf("%s: %d %q", c.envelope, code, errb.String())
		}
	}
	if stdout, _, _ := run("record", "verify", "--ledger", ledger); !strings.Contains(stdout, "\"eventCount\": 1,") {
		t.Fatalf("a refused line was appended: %s", stdout)
	}
}

func TestLedgerPathOption(t *testing.T) {
	cwd, _ := os.Getwd()
	for _, c := range []struct {
		args       []string
		session    string
		hasSession bool
		want       string
	}{
		{nil, "", false, filepath.Join(cwd, ".faultline/recordings/session.json")},
		{nil, "s-1", true, filepath.Join(cwd, ".faultline/recordings/s-1.json")},
		{[]string{"--ledger", "x.json"}, "s-1", true, filepath.Join(cwd, "x.json")},
		{[]string{"--ledger"}, "", false, filepath.Join(cwd, ".faultline/recordings/session.json")},
		{[]string{"--ledger", ""}, "s-1", true, cwd},
	} {
		if got := ledgerPathOption(c.args, c.session, c.hasSession); got != c.want {
			t.Errorf("%q: %s, want %s", c.args, got, c.want)
		}
	}
}
