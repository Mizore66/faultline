package cli

import (
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/gitinv"
)

const pairError = "Each --fingerprint must be followed by --image <digest-pinned-image>."

func TestMappingPairScanReusesValues(t *testing.T) {
	pairs, err := runtimeMappingPairs([]string{"mapping", "write", "--fingerprint", "--fingerprint", "--image", "x"})
	if err == nil || len(pairs) != 1 || pairs[0].Fingerprint != "--fingerprint" || pairs[0].Image != "x" {
		t.Fatalf("%v %v", pairs, err)
	}
	if err.Error() != pairError {
		t.Fatalf("error %q", err)
	}
}

func TestMappingPairScan(t *testing.T) {
	cases := []struct {
		args  string
		pairs []gitinv.MappingEntry
		err   bool
	}{
		// Stray --image flags are ignored.
		{"mapping write --image a --output o", nil, false},
		{"mapping write --fingerprint f --image i --output o", []gitinv.MappingEntry{{Fingerprint: "f", Image: "i"}}, false},
		{"mapping write --fingerprint f --image i --fingerprint g --image j", []gitinv.MappingEntry{{Fingerprint: "f", Image: "i"}, {Fingerprint: "g", Image: "j"}}, false},
		{"mapping write --fingerprint f --image", nil, true},
		{"mapping write --fingerprint f", nil, true},
		{"mapping write --fingerprint", nil, true},
		{"mapping write --fingerprint f --output o", nil, true},
	}
	for _, c := range cases {
		pairs, err := runtimeMappingPairs(strings.Fields(c.args))
		if (err != nil) != c.err {
			t.Errorf("%s: err %v", c.args, err)
			continue
		}
		if !c.err && (len(pairs) != len(c.pairs) || (len(pairs) > 0 && pairs[len(pairs)-1] != c.pairs[len(c.pairs)-1])) {
			t.Errorf("%s: pairs %v", c.args, pairs)
		}
	}
	// An empty value is falsy, like undefined.
	if _, err := runtimeMappingPairs([]string{"--fingerprint", "", "--image", "i"}); err == nil {
		t.Error("an empty fingerprint is accepted")
	}
	if _, err := runtimeMappingPairs([]string{"--fingerprint", "f", "--image", ""}); err == nil {
		t.Error("an empty image is accepted")
	}
}

func TestRuntimeUsageErrors(t *testing.T) {
	usage := "Usage: fl runtime resolve <node|python|go> | fl runtime prepare <node|python|go> --yes | fl runtime mapping write ... | fl runtime project plan|build|resolve ..."
	mapping := "Usage: fl runtime mapping write --fingerprint <sha256:...> --image <digest-pinned-image> [--fingerprint ... --image ...] --output <mapping.json>"
	project := "Usage: fl runtime project plan [--context <directory>] [--dockerfile <file>] --tag <repository:tag> [--network <none|default>] | fl runtime project build [same options] --expect-plan <sha256:digest> --yes | fl runtime project resolve --tag <repository:tag>"
	for _, c := range []struct {
		args    []string
		message string
	}{
		{[]string{"runtime"}, usage},
		{[]string{"runtime", "resolve"}, usage},
		{[]string{"runtime", "resolve", ""}, usage},
		{[]string{"runtime", "nope", "node"}, usage},
		{[]string{"runtime", "mapping"}, mapping},
		{[]string{"runtime", "mapping", "read"}, mapping},
		{[]string{"runtime", "mapping", "write", "--fingerprint", "f", "--image", "i"}, "Missing required option: --output"},
		{[]string{"runtime", "mapping", "write", "--output", "o.json"}, "runtime mapping write requires at least one --fingerprint / --image pair"},
		{[]string{"runtime", "project"}, project},
		{[]string{"runtime", "project", "nope"}, project},
		{[]string{"runtime", "project", "resolve"}, "Missing required option: --tag"},
		{[]string{"runtime", "project", "plan", "--network", "host"}, "--network must be either none or default for a project runtime image build."},
		{[]string{"runtime", "resolve", "node", "--pull"}, "FaultLine never pulls a runtime image implicitly. Use fl runtime prepare <node|python|go> --yes to explicitly pull one reviewed catalog image, or pull it yourself and then run fl runtime resolve again."},
		{[]string{"runtime", "prepare", "node", "--yes", "--pull"}, "fl runtime prepare already performs one explicit catalog pull after --yes; do not add --pull."},
		{[]string{"runtime", "prepare", "ruby"}, `Unknown curated runtime "ruby". Choose node, python, or go.`},
	} {
		stdout, stderr, code := run(c.args...)
		if want := "FaultLine error: " + c.message + "\nNext: pnpm fl help\n"; code != 1 || stdout != "" || stderr != want {
			t.Errorf("%q: code=%d stdout=%q stderr=%q", c.args, code, stdout, stderr)
		}
	}
}

func TestRuntimePrepareWithoutYes(t *testing.T) {
	stdout, stderr, code := run("runtime", "prepare", "go")
	want := "{\n  \"status\": \"CONFIRMATION_REQUIRED\",\n  \"runtime\": {\n    \"alias\": \"go\",\n    \"displayName\": \"Go 1.24 (Alpine)\",\n    \"tag\": \"golang:1.24-alpine\"\n  },\n" +
		"  \"effect\": \"FaultLine will run docker pull golang:1.24-alpine, which may access the network and changes the local Docker image store. It will then inspect and print Docker's immutable RepoDigest. This setup action is not a proof execution.\",\n" +
		"  \"next\": \"fl runtime prepare go --yes\"\n}\n"
	if code != 1 || stderr != "" || stdout != want {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
