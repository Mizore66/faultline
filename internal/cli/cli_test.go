package cli

import (
	"bytes"
	"testing"
)

func run(args ...string) (string, string, int) {
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

func TestFailures(t *testing.T) {
	cases := []struct {
		args   []string
		stderr string
	}{
		{[]string{"verify"}, "FaultLine error: Usage: fl verify <proof-bundle-directory>\nNext: pnpm fl help\n"},
		{[]string{"verify", ""}, "FaultLine error: Usage: fl verify <proof-bundle-directory>\nNext: pnpm fl help\n"},
		{[]string{"nope"}, "FaultLine error: Unknown command: nope\nNext: pnpm fl help\n"},
		{[]string{"--x"}, "FaultLine error: Unknown command: --x\nNote: Do not place '--' between 'fl' and your subcommand. Use 'pnpm fl <command>'.\nNext: pnpm fl help\n"},
		{[]string{"--"}, "FaultLine error: Unknown command: --\nNote: Do not place '--' between 'fl' and your subcommand. Use 'pnpm fl <command>'.\nNext: pnpm fl help\n"},
		{[]string{"doctor"}, "FaultLine error: not yet ported: doctor\nNext: pnpm fl help\n"},
	}
	for _, c := range cases {
		stdout, stderr, code := run(c.args...)
		if code != 1 || stdout != "" || stderr != c.stderr {
			t.Errorf("%q: code=%d stdout=%q stderr=%q", c.args, code, stdout, stderr)
		}
	}
}

func TestExecutionPolicyHint(t *testing.T) {
	got := formatFailure("running scripts is DISABLED on this system")
	want := "FaultLine error: running scripts is DISABLED on this system\n\n" +
		"Windows tip: If PowerShell blocked pnpm due to ExecutionPolicy restrictions, run:\n" +
		"  Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope Process\n" +
		"Or invoke the command proxy directly:\n" +
		"  pnpm.cmd fl <command> (e.g., pnpm.cmd fl judge-demo)\n" +
		"Next: pnpm fl help"
	if got != want {
		t.Fatalf("got %q", got)
	}
}

func TestOption(t *testing.T) {
	args := []string{"dir", "--expect-root", "sha256:x", "--expect-root", "second"}
	if v, ok := option(args, "--expect-root"); !ok || v != "sha256:x" {
		t.Fatalf("option = %q, %v", v, ok)
	}
	if _, ok := option([]string{"dir", "--expect-root"}, "--expect-root"); ok {
		t.Fatal("a trailing flag has no value")
	}
	if !hasFlag(args, "--expect-root") || hasFlag(args, "--nope") {
		t.Fatal("hasFlag")
	}
}
