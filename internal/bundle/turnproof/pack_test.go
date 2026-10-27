package turnproof

import (
	"os/exec"
	"strings"
	"testing"
)

func TestPackRoundTrip(t *testing.T) {
	dir := t.TempDir()
	git := func(stdin string, args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	git("", "init", "-q")
	blob := git("good\n", "hash-object", "-w", "--stdin")
	tree := git("100644 blob "+blob+"\tstate.txt\n", "mktree")
	pack, err := PackTurnTrees(dir, []string{tree, tree})
	if err != nil || len(pack) < 32 || string(pack[:4]) != "PACK" {
		t.Fatalf("%v %d", err, len(pack))
	}
	var errs []string
	if err := VerifyTreesPack(pack, []string{tree}, &errs); err != nil || len(errs) != 0 {
		t.Fatalf("%v %q", err, errs)
	}
	errs = nil
	VerifyTreesPack(pack[:len(pack)-5], []string{tree}, &errs)
	if len(errs) != 1 || !strings.HasPrefix(errs[0], "Turn tree pack is not a valid Git pack: ") {
		t.Fatalf("%q", errs)
	}
}
