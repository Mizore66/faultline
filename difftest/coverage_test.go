package difftest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
)

// unreachableLiterals are error texts in internal/bundle that no golden can
// produce, each with the reason. Everything else must appear in some golden
// output, so a dropped or reworded check fails replay.
var unreachableLiterals = map[string]string{
	"Git proof bundle directory does not exist":                               "a Git bundle is only recognised by reading its manifest.json, so its directory exists (short of a race)",
	"Prevention proof directory does not exist":                               "a prevention bundle is only recognised by reading its manifest.json, so its directory exists (short of a race)",
	"Unsafe artifact path:":                                                   "metadata and manifest paths are schema-checked with the same safe-path rule first",
	"Git range patch exceeds FaultLine's portable artifact limit.":            "git diff output past the 4 MiB maxBuffer fails with ENOBUFS before the 128 MiB artifact limit is checked",
	"AGENT_DRAFT evidence cannot be exported as a Git proof bundle":           "proof.evidenceGrade is a zod enum without AGENT_DRAFT",
	"witness digest is invalid":                                               "SandboxAuditSchema requires sha256 digests, and verify parses it first",
	"command digest is invalid":                                               "SandboxAuditSchema requires sha256 digests, and verify parses it first",
	"environment policy digest is invalid":                                    "SandboxAuditSchema requires sha256 digests, and verify parses it first",
	"sandbox policy digest is invalid":                                        "SandboxAuditSchema requires sha256 digests, and verify parses it first",
	"run sandbox limits are invalid:":                                         "SandboxAuditSchema requires positive integer limits",
	"cached replay minimization must be marked NOT_EXECUTED":                  "minimization.termination is a zod enum of BIDIRECTIONALLY_VALIDATED and NOT_EXECUTED, both handled before this branch",
	"prevention package must set verified=true":                               "verified is z.literal(true); the schema rejects false first",
	"prevention requires NATIVE_DOCKER EXECUTED evidence on every state":      "executionTrust and executionKind are zod literals; the schema rejects other values first",
	"expected frozen digest is not a valid sha256 digest":                     "the manifest schema requires frozenDigest to be a sha256 digest",
	"three-state prevention requires PASS → FAIL → PASS":                      "each state's verdict is a zod literal (PASS, FAIL, PASS); the schema rejects other orders first",
	"prevention requires at least three distinct executions per state":        "distinctExecutionCount has a zod minimum of 3",
	"source metadata artifact paths do not match the manifest":                "both paths are zod literals, so they always agree after the schemas pass",
	"Cannot bind an invalid Codex lifecycle ledger:":                          "binding runs only after the ledger verified",
	"Cannot bind a lifecycle ledger that has no SESSION_STARTED observation.": "a verified ledger always starts with SESSION_STARTED",
}

// errorLiterals collects the string literals passed to errors.New,
// fmt.Errorf, schema.Regex and schema.Refine (as their message), append(errs,
// ...) and local error-adding closures (such as
// `add := func(s string) { errs = append(errs, s) }`) in internal/bundle.
func errorLiterals(t *testing.T, dir string) map[string]string {
	sites, err := errorSites(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, s := range sites {
		out[s.text] = s.pos
	}
	return out
}

// errorSite is one error literal and where it is: errorLiterals keys them by
// text, the statement-coverage gate (coverageGate) by position.
type errorSite struct {
	text, pos, file string
	line            int
}

func errorSites(dir string) ([]errorSite, error) {
	var out []errorSite
	fset := token.NewFileSet()
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		adders := errorClosures(f)
		collect := func(exprs []ast.Expr) { collectLiterals(fset, exprs, &out) }
		ast.Inspect(f, func(n ast.Node) bool {
			// Error lists built as literals: Result{Errors: []string{"…"}}
			// and `return []string{"…"}` (round 6: seven such messages
			// were never collected).
			var lists []ast.Expr
			switch n := n.(type) {
			case *ast.KeyValueExpr:
				if key, ok := n.Key.(*ast.Ident); ok && key.Name == "Errors" {
					lists = append(lists, n.Value)
				}
			case *ast.ReturnStmt:
				lists = n.Results
			}
			for _, e := range lists {
				if lit, ok := e.(*ast.CompositeLit); ok && isStringSlice(lit.Type) {
					collect(lit.Elts)
				}
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			args := call.Args
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				switch {
				case adders[fn.Name]:
				case fn.Name == "append" && len(args) >= 2 && appendsToErrors(args[0]):
					args = args[1:]
				default:
					return true
				}
			case *ast.SelectorExpr:
				switch fn.Sel.Name {
				case "New", "Errorf":
				case "Regex", "Refine":
					// schema.Regex(m, message) and schema.Refine(inner, pred,
					// message): zod's custom issue messages.
					args = args[len(args)-1:]
				default:
					return true
				}
			default:
				return true
			}
			collect(args)
			return true
		})
		return nil
	})
	return out, err
}

// collectLiterals adds the message literals in exprs to out.
func collectLiterals(fset *token.FileSet, exprs []ast.Expr, out *[]errorSite) {
	for _, a := range exprs {
		ast.Inspect(a, func(m ast.Node) bool {
			// Literals inside other calls (v.Get("key")) are not
			// message text; jsexc.Concat builds the message itself.
			if inner, ok := m.(*ast.CallExpr); ok {
				sel, ok := inner.Fun.(*ast.SelectorExpr)
				return ok && sel.Sel.Name == "Concat"
			}
			if lit, ok := m.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				s, _ := strconv.Unquote(lit.Value)
				s = strings.TrimSpace(s)
				// Short literals ("; expected", "sequence") are glue
				// around the longer literal of the same message.
				if len(messageFragments(s)) > 0 {
					p := fset.Position(lit.Pos())
					*out = append(*out, errorSite{s, p.String(), p.Filename, p.Line})
				}
			}
			return true
		})
	}
}

func appendsToErrors(e ast.Expr) bool { return strings.Contains(strings.ToLower(exprString(e)), "err") }

// errorClosures names the local function variables whose body appends to an
// error slice.
func errorClosures(f *ast.File) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return true
		}
		name, ok := assign.Lhs[0].(*ast.Ident)
		lit, ok2 := assign.Rhs[0].(*ast.FuncLit)
		if !ok || !ok2 {
			return true
		}
		ast.Inspect(lit.Body, func(m ast.Node) bool {
			if call, ok := m.(*ast.CallExpr); ok {
				if fn, ok := call.Fun.(*ast.Ident); ok && fn.Name == "append" && len(call.Args) >= 2 && appendsToErrors(call.Args[0]) {
					out[name.Name] = true
				}
			}
			return true
		})
		return true
	})
	return out
}

var formatVerb = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z%]`)

// messageFragments splits a literal at its format verbs and keeps the parts
// long enough to identify the message; each must appear in some golden.
func messageFragments(s string) []string {
	var out []string
	for _, part := range formatVerb.Split(s, -1) {
		if part = strings.TrimSpace(part); len(part) >= 12 {
			out = append(out, part)
		}
	}
	return out
}

func isStringSlice(e ast.Expr) bool {
	arr, ok := e.(*ast.ArrayType)
	return ok && arr.Len == nil && exprString(arr.Elt) == "string"
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

// liveGatedFiles are checked by a live test against TS instead of goldens:
// their functions run on thousands of generated inputs there, and the live
// test fails when one of the file's literals was never produced
// (requireReached).
var liveGatedFiles = map[string]string{
	"gitproof/ledger.go":  "TestLiveLedger",
	"gitproof/sandbox.go": "TestLiveSandbox",
	"gitproof/witness.go": "TestLiveWitness",
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
	bundleDir := filepath.Join(repo, "internal", "bundle") + string(filepath.Separator)
	for lit, pos := range literals {
		if file, _, _ := strings.Cut(strings.TrimPrefix(pos, bundleDir), ":"); liveGatedFiles[filepath.ToSlash(file)] != "" {
			continue
		}
		reached := true
		for _, frag := range messageFragments(lit) {
			quoted := strconv.Quote(frag)
			reached = reached && strings.Contains(all, quoted[1:len(quoted)-1])
		}
		if reached {
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
