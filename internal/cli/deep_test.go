package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// copyBase copies a committed difftest base into a temp directory.
func copyBase(t *testing.T, base string) string {
	t.Helper()
	src := filepath.Join("..", "..", "difftest", "testdata", "bases", base)
	dst := filepath.Join(t.TempDir(), base)
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	return dst
}

// A deeply nested value where a zod literal is expected makes TS's
// ZodError.message getter throw RangeError inside JSON.stringify; the verifier
// catch turns that into one "failed safely" line (KNOWN_DIFFERENCES: the
// depth thresholds are calibrated to Node 22's default stack).
func TestDeepLiteralOverflowsLikeV8(t *testing.T) {
	for _, tc := range []struct {
		base, file, old, safe string
	}{
		{"demo-replay", "analysis.json", `"schemaVersion": "faultline.demo.v1"`, "- bundle verification failed safely: Maximum call stack size exceeded\n"},
		{"prevention-verified", "prevention.json", `"verified":true`, "- Prevention proof verification failed safely: Maximum call stack size exceeded\n"},
		{"git-two-states", "source/metadata.json", `"schemaVersion":"faultline.git-proof-source.v1"`, "- Git proof bundle verification failed safely: Maximum call stack size exceeded\n"},
	} {
		for _, depth := range []int{2000, 3000} {
			dir := copyBase(t, tc.base)
			path := filepath.Join(dir, tc.file)
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			key, _, _ := strings.Cut(tc.old, ":")
			deep := key + ":" + strings.Repeat("[", depth) + strings.Repeat("]", depth)
			if !strings.Contains(string(b), tc.old) {
				t.Fatalf("%s: %s lacks %s", tc.base, tc.file, tc.old)
			}
			if err := os.WriteFile(path, []byte(strings.Replace(string(b), tc.old, deep, 1)), 0o644); err != nil {
				t.Fatal(err)
			}
			stdout, _, code := run("verify", dir)
			overflowed := strings.Contains(stdout, tc.safe)
			if code != 1 || overflowed != (depth == 3000) || depth == 3000 && len(stdout) > 1000 {
				t.Errorf("%s depth %d: code=%d overflowed=%v stdout(%d)=%.400q", tc.base, depth, code, overflowed, len(stdout), stdout)
			}
		}
	}
}
