package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const exactlyOneError = "FaultLine error: fl prevention write requires exactly one of --input <json> or --from-bundle <git-proof-bundle>.\nNext: pnpm fl help\n"

// The --input / --from-bundle rule is judged by option(): a flag in last
// position has no value, so it counts as absent (src/cli-app.ts:2588).
func TestPreventionWriteExactlyOne(t *testing.T) {
	t.Chdir(t.TempDir())
	refused := [][]string{
		{"prevention", "write"},
		{"prevention", "write", "--input"},
		{"prevention", "write", "--from-bundle"},
		{"prevention", "write", "--input", "a.json", "--from-bundle", "b"},
		{"prevention", "write", "--from-bundle", "b", "--input", "a.json"},
	}
	for _, args := range refused {
		if stdout, stderr, code := run(args...); code != 1 || stdout != "" || stderr != exactlyOneError {
			t.Errorf("%v: exit %d stdout %q stderr %q", args, code, stdout, stderr)
		}
	}
	// One flag with a value and the other last: exactly one is present,
	// so the command goes on (and fails later, reading the input).
	accepted := [][]string{
		{"prevention", "write", "--input", "missing.json", "--from-bundle"},
		{"prevention", "write", "--from-bundle", "b", "--input"},
		// Each flag's value is the other flag, which is last: option()
		// takes the value as is and the last flag is absent.
		{"prevention", "write", "--from-bundle", "--input"},
		{"prevention", "write", "--input", "--from-bundle"},
	}
	for _, args := range accepted {
		if _, stderr, code := run(args...); code != 1 || stderr == exactlyOneError {
			t.Errorf("%v: exit %d stderr %q", args, code, stderr)
		}
	}
	if _, stderr, _ := run("prevention", "write", "--from-bundle", "b", "--input"); stderr != "FaultLine error: Missing required option: --repaired-runs\nNext: pnpm fl help\n" {
		t.Errorf("--from-bundle with a trailing --input: %q", stderr)
	}
	if _, stderr, _ := run("prevention", "write", "--input", "missing.json", "--from-bundle"); !strings.Contains(stderr, "ENOENT: no such file or directory, open '") {
		t.Errorf("--input with a trailing --from-bundle: %q", stderr)
	}
}

func TestPreventionUsage(t *testing.T) {
	usage := "FaultLine error: Usage: fl prevention write --input <prevention-input.json> | fl prevention write --from-bundle ... | fl prevention verify <directory> [--expect-root <sha256:...>]\nNext: pnpm fl help\n"
	for _, args := range [][]string{{"prevention"}, {"prevention", "nope"}} {
		if _, stderr, code := run(args...); code != 1 || stderr != usage {
			t.Errorf("%v: exit %d stderr %q", args, code, stderr)
		}
	}
	verifyUsage := "FaultLine error: Usage: fl prevention verify <prevention-proof-directory> [--expect-root <sha256:...>]\nNext: pnpm fl help\n"
	for _, args := range [][]string{{"prevention", "verify"}, {"prevention", "verify", ""}} {
		if _, stderr, code := run(args...); code != 1 || stderr != verifyUsage {
			t.Errorf("%v: exit %d stderr %q", args, code, stderr)
		}
	}
}

// A missing package is invalid with a null classification and root.
func TestPreventionVerifyMissing(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := run("prevention", "verify", filepath.Join(dir, "none"), "--expect-root", "x")
	want := "{\n  \"valid\": false,\n  \"schemaVersion\": \"faultline.prevention-proof.v1\",\n  \"classification\": null,\n  \"claim\": \"Invalid\",\n  \"rootDigest\": null,\n  \"externalRootStatus\": \"MISMATCH\",\n  \"errors\": [\n    \"Prevention proof directory does not exist\"\n  ]\n}\n"
	if code != 1 || stderr != "" || stdout != want {
		t.Errorf("exit %d stderr %q stdout %q", code, stderr, stdout)
	}
}

// The --repaired-runs document must hold a runs array; null throws V8's
// TypeError.
func TestPreventionRepairedRunsShape(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for name, text := range map[string]string{"null.json": "null", "obj.json": "{\"runs\":{}}", "arr.json": "[]"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	notArray := "FaultLine error: --repaired-runs must be a JSON object with a runs array of three repaired NATIVE_DOCKER bindings.\nNext: pnpm fl help\n"
	for name, want := range map[string]string{
		"null.json": "FaultLine error: Cannot read properties of null (reading 'runs')\nNext: pnpm fl help\n",
		"obj.json":  notArray,
		"arr.json":  notArray,
	} {
		if _, stderr, code := run("prevention", "write", "--from-bundle", "b", "--repaired-runs", name); code != 1 || stderr != want {
			t.Errorf("%s: exit %d stderr %q", name, code, stderr)
		}
	}
}
