package difftest

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/cli"
)

func BenchmarkVerifyGitFullyBound(b *testing.B) {
	dir := filepath.Join(oracle.RepoRoot(b), "difftest", "testdata", "bases", "git-fully-bound")
	for b.Loop() {
		var out, errOut bytes.Buffer
		if cli.Run([]string{"verify", dir}, &out, &errOut) != 0 {
			b.Fatal(out.String(), errOut.String())
		}
	}
}

func BenchmarkVerifyDemoRerun(b *testing.B) {
	dir := filepath.Join(oracle.RepoRoot(b), "difftest", "testdata", "bases", "demo-rerun")
	for b.Loop() {
		var out, errOut bytes.Buffer
		if cli.Run([]string{"verify", dir}, &out, &errOut) != 0 {
			b.Fatal(out.String(), errOut.String())
		}
	}
}
