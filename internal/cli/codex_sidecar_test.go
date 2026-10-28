package cli

import (
	"strings"
	"testing"
)

func TestSidecarHookQuietSwallowsErrors(t *testing.T) {
	var out, errb strings.Builder
	code := RunWithStdin([]string{"codex", "sidecar", "hook", "--quiet"}, strings.NewReader(""), &out, &errb)
	want := "FaultLine sidecar did not record this hook: Unexpected end of JSON input\nRun fl codex sidecar status in the repository after the turn for a detailed health check.\n"
	if code != 0 || out.String() != "{\"continue\":true}\n" || errb.String() != want {
		t.Fatalf("%d %q %q", code, out.String(), errb.String())
	}
}

func TestSidecarUsage(t *testing.T) {
	_, stderr, code := run("codex", "sidecar")
	if code != 1 || !strings.HasPrefix(stderr, "FaultLine error: Usage: fl codex sidecar config (--cli <built-cli.js> | --command <hook-command> [--command-windows <hook-command>]) | fl codex sidecar install") {
		t.Fatalf("%d %q", code, stderr)
	}
}

func TestSidecarConfigCommand(t *testing.T) {
	stdout, _, code := run("codex", "sidecar", "config", "--command", "x")
	if code != 0 || !strings.Contains(stdout, `"command": "x"`) || strings.Contains(stdout, "commandWindows") {
		t.Fatalf("%d %q", code, stdout)
	}
}

func TestSidecarNotQuietRethrows(t *testing.T) {
	var out, errb strings.Builder
	code := RunWithStdin([]string{"codex", "sidecar", "hook"}, strings.NewReader(""), &out, &errb)
	if code != 1 || out.String() != "" || !strings.HasPrefix(errb.String(), "FaultLine error: Unexpected end of JSON input") {
		t.Fatalf("%d %q %q", code, out.String(), errb.String())
	}
}
