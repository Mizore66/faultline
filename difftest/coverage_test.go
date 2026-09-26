package difftest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
)

// unreachableLiterals are error texts in internal/bundle that no golden can
// produce, each with the reason. Everything else must appear in some golden
// output, so a dropped or reworded check fails replay.
var unreachableLiterals = map[string]string{
	"declared path escapes bundle:":                                               "only a Windows drive-relative path (C:x) resolves outside the bundle after invalidDeclaredPath",
	"Unsafe artifact path:":                                                       "metadata and manifest paths are schema-checked with the same safe-path rule first",
	"Artifact path escapes its bundle:":                                           "unreachable after the safe-path check (no .. or absolute segments)",
	"Git returned an invalid object identifier while writing a proof bundle.":     "git rev-parse --verify only prints object ids",
	"Git bundle returned an invalid head line:":                                   "git bundle list-heads output is well formed for any bundle git accepts",
	"Git range patch exceeds FaultLine's portable artifact limit.":                "needs a git diff over 128 MiB, larger than the bundle limits allow in the corpus",
	"Git proof bundle artifact exceeds FaultLine's read limit:":                   "needs a 128 MiB file in the corpus",
	"Git proof bundle contains too many files to verify safely":                   "needs more than 2048 files on disk; covered by the catalog-count case instead",
	"Git proof bundle exceeds FaultLine's total verification read limit":          "needs 512 MiB on disk",
	"declared artifacts exceed FaultLine's total verification read limit":         "needs 512 MiB on disk",
	"investigation has no resolved range for source metadata cross-check":         "the investigation schema requires resolvedRange for a bundle with source metadata",
	"prevention package must set verified=true":                                   "verified is z.literal(true); the schema rejects false first",
	"prevention requires NATIVE_DOCKER EXECUTED evidence on every state":          "executionTrust and executionKind are zod literals; the schema rejects other values first",
	"expected frozen digest is not a valid sha256 digest":                         "the manifest schema requires frozenDigest to be a sha256 digest",
	"three-state prevention requires PASS → FAIL → PASS":                          "each state's verdict is a zod literal (PASS, FAIL, PASS); the schema rejects other orders first",
	"prevention requires at least three distinct executions per state":            "distinctExecutionCount has a zod minimum of 3",
	"source metadata artifact paths do not match the manifest":                    "both paths are zod literals, so they always agree after the schemas pass",
	"bound manifest lifecycle artifact path does not match its lifecycle binding": "lifecycle.path is a zod literal in the manifest schema",
	"externally supplied frozen digest does not match":                            "bundle verification passes the record's own frozen digest; TestLiveWitness covers the mismatch",
	// Binding a ledger to the investigation needs a re-signed ledger hash
	// chain plus matching manifest ledgerDigest/headHash, which the generic
	// mutation engines cannot produce.
	"lifecycle ledger cannot bind to the investigation:":                                            "needs a re-signed ledger and manifest binding",
	"Lifecycle checkpoint":                                                                          "needs a re-signed ledger and manifest binding",
	"has a tree that disagrees with investigated commit":                                            "needs a re-signed ledger and manifest binding",
	"Cannot bind a lifecycle ledger without a clean checkpoint matching an investigated Git state.": "needs a re-signed ledger and manifest binding",
	"Lifecycle ledger must include a clean checkpoint for the investigated descendant state.":       "needs a re-signed ledger and manifest binding",
	"Cannot bind an invalid Codex lifecycle ledger:":                                                "binding runs only after the ledger verified",
	"Cannot bind a lifecycle ledger that has no SESSION_STARTED observation.":                       "a verified ledger always starts with SESSION_STARTED",
}

// errorLiterals collects the string literals passed to errors.New,
// fmt.Errorf and append(errs, ...) in internal/bundle.
func errorLiterals(t *testing.T, dir string) map[string]string {
	out := map[string]string{}
	fset := token.NewFileSet()
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			args := call.Args
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				if fn.Name != "append" || len(args) < 2 || !strings.Contains(strings.ToLower(exprString(args[0])), "err") {
					return true
				}
				args = args[1:]
			case *ast.SelectorExpr:
				if fn.Sel.Name != "New" && fn.Sel.Name != "Errorf" {
					return true
				}
			default:
				return true
			}
			for _, a := range args {
				ast.Inspect(a, func(m ast.Node) bool {
					if lit, ok := m.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						s, _ := strconv.Unquote(lit.Value)
						s = strings.TrimSpace(strings.Split(s, "%")[0])
						// Short fragments ("; expected", "sequence") are glue
						// around the longer literal of the same message.
						if len(s) >= 20 {
							out[s] = fset.Position(lit.Pos()).String()
						}
					}
					return true
				})
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func exprString(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return exprString(e.X)
	case *ast.SelectorExpr:
		return exprString(e.X) + "." + e.Sel.Name
	}
	return ""
}

func TestGoldensReachEveryErrorLiteral(t *testing.T) {
	repo := oracle.RepoRoot(t)
	files, err := filepath.Glob(filepath.Join(repo, "difftest", "testdata", "golden", "*.jsonl"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no goldens: %v", err)
	}
	var goldens strings.Builder
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		goldens.Write(b)
	}
	all := goldens.String()
	literals := errorLiterals(t, filepath.Join(repo, "internal", "bundle"))
	for lit, pos := range literals {
		quoted := strconv.Quote(lit)
		if strings.Contains(all, quoted[1:len(quoted)-1]) {
			if reason, ok := unreachableLiterals[lit]; ok {
				t.Errorf("%s: %q is listed as unreachable (%s) but a golden produces it; drop it from the list", pos, lit, reason)
			}
			continue
		}
		if _, ok := unreachableLiterals[lit]; !ok {
			t.Errorf("%s: no golden produces %q; add a mutation template that reaches it, or list it in unreachableLiterals with the reason", pos, lit)
		}
	}
	for lit := range unreachableLiterals {
		if _, ok := literals[lit]; !ok {
			t.Errorf("unreachableLiterals lists %q, which is no longer an error literal in internal/bundle", lit)
		}
	}
}
