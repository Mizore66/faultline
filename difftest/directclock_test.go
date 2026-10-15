//go:build faultline_test

package difftest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
)

// Packages that may read the clock, randomness or temp names directly.
var directClockExempt = map[string]string{
	"internal/clock":     "the seam itself",
	"internal/nodeproc":  "process plumbing, not a writer",
	"internal/boot":      "process plumbing, not a writer",
	"internal/sigexit":   "monotonic hold timer, not recorded data",
	"internal/collation": "no clock use; excluded by the spec",
}

// TestNoDirectClockReads fails on any non-test code in internal/ that reads
// the wall clock, randomness or temp names without going through
// internal/clock, which would escape the seeded sequence.
func TestNoDirectClockReads(t *testing.T) {
	root := oracle.RepoRoot(t)
	internal := filepath.Join(root, "internal")
	err := filepath.WalkDir(internal, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if _, skip := directClockExempt[rel]; skip {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		// Local names of the imports of interest.
		names := map[string]string{}
		for _, imp := range file.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			switch p {
			case "time", "crypto/rand", "math/rand", "math/rand/v2", "os":
				name := p[strings.LastIndex(p, "/")+1:]
				if p == "math/rand/v2" {
					name = "rand"
				}
				if imp.Name != nil {
					name = imp.Name.Name
				}
				names[name] = p
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			pkg := names[id.Name]
			bad := false
			switch pkg {
			case "time":
				bad = sel.Sel.Name == "Now" || sel.Sel.Name == "Since"
			case "crypto/rand", "math/rand", "math/rand/v2":
				bad = true
			case "os":
				bad = sel.Sel.Name == "MkdirTemp" || sel.Sel.Name == "CreateTemp"
			}
			if bad {
				t.Errorf("%s: direct %s.%s; use internal/clock", rel, id.Name, sel.Sel.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
