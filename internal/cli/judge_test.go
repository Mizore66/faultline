package cli

import (
	"bytes"
	"testing"
	"time"
)

func TestShouldAutoOpenBrowser(t *testing.T) {
	for _, c := range []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{}, true},
		{map[string]string{"FAULTLINE_NO_BROWSER": "1"}, false},
		{map[string]string{"FAULTLINE_NO_BROWSER": "true"}, true},
		{map[string]string{"CI": "true"}, false},
		{map[string]string{"CI": "1"}, false},
		{map[string]string{"CI": "yes"}, true},
		{map[string]string{"GITHUB_ACTIONS": "true"}, false},
		{map[string]string{"GITHUB_ACTIONS": "1"}, true},
	} {
		if got := shouldAutoOpenBrowser(func(k string) string { return c.env[k] }); got != c.want {
			t.Errorf("%v: %v", c.env, got)
		}
	}
}

// The opener command lines are TS's (src/cli-app.ts:891-895) byte for byte:
// the page URL never holds a shell metacharacter, so a dropped quote
// changes nothing a fake opener can see (round 7 #14, mutant P5).
func TestOpenerCommand(t *testing.T) {
	const url = "http://127.0.0.1:4174"
	for goos, want := range map[string]string{
		"windows": `cmd /c start "" "http://127.0.0.1:4174"`,
		"darwin":  `open "http://127.0.0.1:4174"`,
		"linux":   `xdg-open "http://127.0.0.1:4174"`,
		"freebsd": `xdg-open "http://127.0.0.1:4174"`,
	} {
		if got := openerCommand(goos, url); got != want {
			t.Errorf("%s: %s, want %s", goos, got, want)
		}
	}
}

// No bundle anyone holds has the recorded self-incident root (a SHA-256), so
// only this test reaches the match line; the other branch is in the
// judge-proof writer goldens (round 7 #14, mutant P3).
func TestSelfIncidentRootLine(t *testing.T) {
	if got := selfIncidentRootLine("sha256:f6a391b3407731d766bd19510c4e4172ad44f28771f1d034030fc56513625b75"); got != "Matched historical self-incident root.\n" {
		t.Errorf("recorded root: %q", got)
	}
	if got, want := selfIncidentRootLine("sha256:f85c446dfd5ab92222b10a314e79209a8a7dc10ee69af9d2deaa04aceafeb7d9"),
		"Note: historical self-incident root is sha256:f6a391b3407731d766bd19510c4e4172ad44f28771f1d034030fc56513625b75 (see docs/faultline-self-incident.md).\n"; got != want {
		t.Errorf("sample root: %q", got)
	}
}

func TestTimeoutDuration(t *testing.T) {
	for ms, want := range map[float64]time.Duration{0: time.Millisecond, 0.5: time.Millisecond, 200: 200 * time.Millisecond, 3e9: time.Millisecond} {
		if got := timeoutDuration(ms); got != want {
			t.Errorf("%v: %v", ms, got)
		}
	}
}

func TestJudgeProofUsageErrors(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"commit-proof-preview", "--bundle", "--output"}, "Missing required option: --bundle"},
		{[]string{"commit-proof-preview", "--output", "--x"}, "Missing required option: --output"},
		{[]string{"judge-proof", "--bundle", "--x"}, "Missing required option: --bundle"},
	} {
		var out, errOut bytes.Buffer
		if code := Run(c.args, &out, &errOut); code != 1 || errOut.String() != "FaultLine error: "+c.want+"\nNext: pnpm fl help\n" {
			t.Errorf("%v: %d %q", c.args, code, errOut.String())
		}
	}
}
