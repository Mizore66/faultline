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
