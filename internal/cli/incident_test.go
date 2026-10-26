package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestIncidentTimeoutParsing(t *testing.T) {
	for _, c := range []struct {
		value string
		want  float64
		ok    bool
	}{
		{"", 0, false}, // Number("") is 0
		{" 5 ", 5, true},
		{"0x10", 16, true},
		{"1e3", 1000, true},
		{"3600", 3600, true},
		{"1", 1, true},
		{"1.0", 1, true},
		{"1.5", 0, false},
		{"0", 0, false},
		{"3601", 0, false},
		{"-1", 0, false},
		{"abc", 0, false},
		{"Infinity", 0, false},
	} {
		got, err := parseTimeoutSeconds([]string{"--timeout-seconds", c.value})
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("%q: got %v, %v", c.value, got, err)
		}
		if err != nil && err.Error() != "--timeout-seconds must be an integer from 1 through 3600." {
			t.Errorf("%q: error %q", c.value, err)
		}
	}
	if got, err := parseTimeoutSeconds(nil); err != nil || got != 300 {
		t.Fatalf("default: %v %v", got, err)
	}
	// A trailing --timeout-seconds has no value: option is undefined, so 300.
	if got, err := parseTimeoutSeconds([]string{"--timeout-seconds"}); err != nil || got != 300 {
		t.Fatalf("trailing flag: %v %v", got, err)
	}
}

func TestIncidentCommandInputPrecedence(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "cmd.txt")
	if err := os.WriteFile(file, []byte("pnpm test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exactlyOne := "Incident intake requires exactly one of --command <failing-command> or --command-file <utf8-file>."
	for _, c := range []struct {
		args    []string
		command string
		file    string
		err     string
	}{
		{nil, "", "", exactlyOne},
		// The exactly-one check comes before the empty-value check.
		{[]string{"--command", "", "--command-file", ""}, "", "", exactlyOne},
		{[]string{"--command", "x", "--command-file", file}, "", "", exactlyOne},
		// A trailing flag is absent (option() is undefined).
		{[]string{"--command"}, "", "", exactlyOne},
		{[]string{"--command", ""}, "", "", "Missing required option: --command"},
		{[]string{"--command", "--x"}, "", "", "Missing required option: --command"},
		{[]string{"--command-file", ""}, "", "", "Missing required option: --command-file"},
		{[]string{"--command", "go test ./..."}, "go test ./...", "", ""},
		{[]string{"--command-file", file}, "pnpm test\n", file, ""},
	} {
		got, err := incidentCommandInput(c.args)
		if c.err != "" {
			if err == nil || err.Error() != c.err {
				t.Errorf("%q: error %v, want %q", c.args, err, c.err)
			}
			continue
		}
		if err != nil || got.command != c.command || got.file != c.file {
			t.Errorf("%q: got %+v, %v", c.args, got, err)
		}
	}
}

func TestIncidentCommandFileRefusals(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	regular := write("ok.txt", []byte("ok"))
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(regular, link); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ file, err string }{
		{link, "Unable to read --command-file " + link + ": must be a real regular file, not a symbolic link or directory"},
		{dir, "Unable to read --command-file " + dir + ": must be a real regular file, not a symbolic link or directory"},
		{filepath.Join(dir, "missing"), "Unable to read --command-file " + filepath.Join(dir, "missing") + ": ENOENT: no such file or directory, lstat '" + filepath.Join(dir, "missing") + "'"},
		{write("big.txt", bytes.Repeat([]byte("a"), 32_001)), "--command-file exceeds the 32 KB witness-command limit."},
		{write("bad.txt", []byte{'a', 0xff}), "--command-file must contain valid UTF-8 without an ambiguous byte encoding."},
		// An encoded lone surrogate decodes to U+FFFD three times.
		{write("surrogate.txt", []byte{0xed, 0xa0, 0x80}), "--command-file must contain valid UTF-8 without an ambiguous byte encoding."},
	} {
		_, err := incidentCommandInput([]string{"--command-file", c.file})
		if err == nil || err.Error() != c.err {
			t.Errorf("%s: error %v, want %q", c.file, err, c.err)
		}
	}
	// The limit is checked on bytes: 32,000 bytes pass, and a leading BOM is
	// kept (Buffer.toString keeps U+FEFF), so the round trip holds.
	if got, err := incidentCommandInput([]string{"--command-file", write("limit.txt", bytes.Repeat([]byte("a"), 32_000))}); err != nil || len(got.command) != 32_000 {
		t.Fatalf("limit: %v", err)
	}
	if got, err := incidentCommandInput([]string{"--command-file", write("bom.txt", []byte("\xef\xbb\xbfx"))}); err != nil || got.command != "\ufeffx" {
		t.Fatalf("bom: %q %v", got.command, err)
	}
}

func TestSplitJSWhitespace(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"", []string{}},
		{"a", []string{"a"}},
		{"a b", []string{"a", "b"}},
		{"  a\t b\u3000 ", []string{"a", "b"}},
		{"a\u00a0b\u2028c", []string{"a", "b", "c"}},
	} {
		if got := splitJSWhitespace(c.in); !slices.Equal(got, c.want) {
			t.Errorf("%q: got %q", c.in, got)
		}
	}
}

func TestIncidentUsage(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"incident"}, "Usage: fl incident suggest|start|status|continue ..."},
		{[]string{"incident", "nope"}, "Usage: fl incident suggest|start|status|continue ..."},
		{[]string{"incident", "suggest"}, "Missing required option: --repo"},
		{[]string{"incident", "start"}, "Missing required option: --repo"},
		{[]string{"incident", "start", "--repo", "."}, "Incident intake requires exactly one of --command <failing-command> or --command-file <utf8-file>."},
		{[]string{"incident", "start", "--repo", ".", "--command", "x", "--from", "a"}, "Incident intake accepts --from and --to together, or neither for the conservative local HEAD-parent fallback."},
		{[]string{"incident", "start", "--repo", ".", "--command", "x", "--id", "i", "--runtime", "node", "--image", "y"}, "Incident intake accepts either --runtime <node|python|go> or --image <digest-pinned-image>, not both."},
		{[]string{"incident", "start", "--repo", ".", "--command", "x", "--id", "i", "--timeout-seconds", "0"}, "--timeout-seconds must be an integer from 1 through 3600."},
		{[]string{"incident", "status", "x"}, "not yet ported: incident status"},
		{[]string{"incident", "continue", "x"}, "not yet ported: incident continue"},
	} {
		var stdout, stderr bytes.Buffer
		if code := Run(c.args, &stdout, &stderr); code != 1 || stdout.Len() != 0 ||
			stderr.String() != "FaultLine error: "+c.want+"\nNext: pnpm fl help\n" {
			t.Errorf("%q: exit %d, stdout %q, stderr %q", c.args, code, stdout.String(), stderr.String())
		}
	}
	if !strings.Contains(incidentUsage, "suggest|start|status|continue") {
		t.Fatal(incidentUsage)
	}
}
