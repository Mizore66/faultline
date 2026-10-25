package doctor

import (
	"os/exec"
	"testing"
)

func TestSecuritySelfTestIsSecure(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	r, err := RunSecurity()
	if err != nil {
		t.Fatal(err)
	}
	if r.Get("status").Str() != "SECURE" || len(r.Get("checks").Items()) != 5 || SecurityExitCode(r) != 0 {
		t.Fatalf("%v", r)
	}
}
