package turnproof

import (
	"fmt"
	"strings"
	"testing"
)

func TestCatalogHasNoCapAndOwnMessages(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 2100; i++ {
		fmt.Fprintf(&b, "%s  extra/%05d.json\n", strings.Repeat("0", 64), i)
	}
	var errs []string
	c := ParseHashCatalog([]byte(b.String()), &errs)
	if len(c.Keys) != 2100 || len(errs) != 0 {
		t.Fatalf("keys %d errs %q", len(c.Keys), errs)
	}
}

func TestCatalogLineRules(t *testing.T) {
	h := strings.Repeat("a", 64)
	var errs []string
	c := ParseHashCatalog([]byte(h+"  m.json\n\n"+h+"  ../x\n"+h+"  m.json\n"+h+" one-space\n"+h+"  last   "), &errs)
	want := []string{
		"hashes.txt must end with a trailing newline",
		"hashes.txt contains a malformed line: ",
		"hashes.txt contains an unsafe artifact path: " + h + "  ../x",
		"hashes.txt declares duplicate artifact: m.json",
		"hashes.txt contains a malformed line: " + h + " one-space",
	}
	if strings.Join(errs, "|") != strings.Join(want, "|") || len(c.Keys) != 2 || c.Keys[1] != "last" {
		t.Fatalf("%q %q", errs, c.Keys)
	}
}
