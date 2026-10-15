//go:build faultline_test

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

// unreachableLiterals are error texts in the gated packages (gatedDirs)
// that no golden can produce, each with the reason. Everything else must appear in some golden
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

	// gitproof/write.go (TestLiveProofWriter's requireReached):
	"Git proof root is not a real directory:":                                         "the filesystem root is always a real directory",
	"Proof bundle root cannot traverse a symbolic link or non-directory:":             "race only: the Git proof walk over the same segments refused it first",
	"Proof bundle output must be a child directory of":                                "prepareFreshOutput refuses the same relation first",
	"Proof bundle root cannot be a symbolic link:":                                    "race only: the Git proof walk resolved the root just before",
	"Proof bundle output cannot traverse a symbolic link:":                            "race only: the Git proof walk resolved every parent just before, and the output does not exist",
	"Proof bundle output must be a directory:":                                        "race only: prepareFreshOutput refused an existing output just before",
	"Only a completed Git investigation with a resolved source range can be bundled.": "semantic validation refuses a null repository or range first",
	"Unsupported Git object format:":                                                  "Git reports only sha1 or sha256",
	"Git bundle did not expose exactly the expected descendant reference.":            "git bundle create was given exactly that one ref",
	"Git bundle is empty or exceeds FaultLine's portable artifact limit.":             "a created bundle is never empty, and readBoundedFile refuses one past the limit first",
	"Cannot create a manifest without a resolved Git range.":                          "semantic validation refuses a null range first",
	"Unsafe generated artifact path:":                                                 "generated paths are fixed names and hex run ids",
	"Git proof bundle generated duplicate artifact:":                                  "semantic validation refuses duplicate run ids first",
	"Git proof writer did not produce the complete expected artifact set.":            "the writer puts exactly requiredArtifactPaths",
	"Git proof source artifact changed before package assembly:":                      "race only: the bundle git just wrote is read back unchanged",
	"Git proof bundle output appeared during assembly and will not be replaced:":      "race only: the output was refused when it existed before staging",

	// Slice 2 writers. Git's own invariants: hardened git prints what these
	// checks expect unless it is broken.
	"Git returned an invalid tree object id for commit":                                                     "git rev-parse --verify <commit>^{tree} prints a full object id or fails",
	"Git range enumeration returned an invalid commit object id.":                                           "git rev-list prints full object ids",
	"Git returned an invalid commit object id for revision":                                                 "git rev-parse --verify <revision>^{commit} prints a full object id or fails",
	"Git returned an invalid tree object id for revision":                                                   "git rev-parse --verify <commit>^{tree} prints a full object id or fails",
	"Git did not return an absolute repository root.":                                                       "git rev-parse --show-toplevel prints an absolute path or fails",
	"Git did not return an absolute repository metadata directory.":                                         "git rev-parse --absolute-git-dir prints an absolute path or fails",
	"Git -z output ended without a NUL terminator.":                                                         "git diff --name-status -z ends every field with NUL",
	"Git path unexpectedly contains a NUL byte.":                                                            "the -z output is split at NUL, so no field holds one",
	"Git --name-status -z output was malformed.":                                                            "git diff --name-status -z gives a status then one or two paths",
	"Unable to verify ancestry:":                                                                            "git merge-base --is-ancestor of two commits rev-parse just resolved exits 0 or 1 unless git fails on an I/O error",
	"Could not enumerate changed Git paths:":                                                                "git diff --name-status between two resolved commits fails only on an I/O error",
	"Could not derive an exact binary patch for":                                                            "git diff --binary of a path the same diff just listed fails only on an I/O error",
	"Could not prepare external Git index for":                                                              "assertSafeGitMaterialization resolved the commit with the same hardened git just before; read-tree of it fails only on an I/O error",
	"Could not materialize Git tree":                                                                        "checkout-index of a resolved commit into a fresh empty worktree fails only on an I/O error",
	"Git contains a non-UTF-8 or empty path; FaultLine refuses to pass it through a lossy argv conversion.": "not covered: needs a tree entry whose name is not UTF-8, which APFS and NTFS refuse, so no writer scenario (replayed on every runner) holds one",
	// Internal invariants and earlier refusals.
	"Missing preflight environment fingerprint for":                            "the preflight fingerprints every state this loop materializes",
	"Missing runtime image mapping for":                                        "buildEffectiveRuntimeMapping maps every preflight digest to a non-empty image, and a heterogeneous range with a gap is refused just before",
	"Candidate references a patch unit that does not belong to this Git diff.": "candidates are built from the same diff's patch unit ids",
	"Refusing to write an invalid Git minimization result:":                    "minimizeGitDiff only returns results its own verifier accepts",
	"trusted reviewer public key is invalid:":                                  "parseReviewerKeyring already refused keys that are not Ed25519 or whose fingerprint is not their keyId",
	"Frozen overlay path escaped its worktree:":                                "safeOverlayParts refuses empty, '.' and '..' segments and backslashes first, so the target stays inside the worktree",
	"Minimization output root is not a real directory:":                        "the CLI's safeManagedFileOutput refuses the same output first",
	"Minimization output parent is not a real directory:":                      "race only: the directory was just created by mkdir",
	"Minimization output cannot traverse a symbolic link or non-directory:":    "the CLI's safeManagedFileOutput refuses the same output first",
	"minimization result verification failed safely:":                          "a throw inside the checks of a record that passed the schema: only a V8 limit (string length, stack depth) gets there",
	// Races: the file or directory changes between two steps of fl.
	"Could not remove temporary materialized tree:":                                        "race only: rm of the worktree and index fl just created under its private 0700 temp root",
	"Could not verify materialized Git tree":                                               "race only: update-index --refresh right after checkout-index into the same worktree",
	"Materialized Git tree does not match its external index for":                          "race only: filters are refused and core.autocrlf is off, so a fresh checkout matches its index",
	"FaultLine could not verify exact materialized overlay bytes:":                         "race only: the overlay was just renamed into place",
	"Could not safely create overlay directory:":                                           "race only: the directory was just created by mkdir",
	"Path is a directory: rm returned EISDIR (is a directory)":                             "race only: the staged name carries a fresh UUID",
	"Git minimization result already exists and will not be replaced:":                     "race only: the CLI's safeManagedFileOutput refused an existing output just before",
	"minimization result changed beyond FaultLine's verification read limit while reading": "race only: lstat just reported a size within the limit",
	"Generated Git proof bundle failed verification:":                                      "race only: the bundle fl just wrote verifies unless it changes underneath",
	"Refusing to render a Git proof bundle that changed during verification:":              "race only: the bundle is verified twice in a row",
	"Unable to read verified":                                                              "race only: the file was just verified, so it exists and parses",
}

// errorLiterals collects the string literals passed to errors.New,
// fmt.Errorf, schema.Regex and schema.Refine (as their message), append(errs,
// ...) and local error-adding closures (such as
// `add := func(s string) { errs = append(errs, s) }`) in dir.
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

// gatedDirs are the packages (relative to the module root) whose error
// literals the goldens must reach (TestGoldensReachEveryErrorLiteral) and
// whose error sites the replays must run (coverageGate): the verifier
// (slice 1) and the slice 2 writers.
var gatedDirs = []string{
	"internal/bundle", "internal/clock", "internal/jsre", "internal/safedir", "internal/evidence",
	"internal/witnessresult", "internal/redaction", "internal/witness", "internal/ledgerbind",
	"internal/gitinv", "internal/sandbox", "internal/minimize", "internal/proofview", "internal/cli",
}

// gatedLiterals is errorLiterals over every gatedDirs package.
func gatedLiterals(t *testing.T, repo string) map[string]string {
	out := map[string]string{}
	for _, dir := range gatedDirs {
		for lit, pos := range errorLiterals(t, filepath.Join(repo, filepath.FromSlash(dir))) {
			out[lit] = pos
		}
	}
	return out
}

// liveGatedFiles (relative to the module root) are checked by a live test
// against TS instead of goldens: their functions run on thousands of
// generated inputs there, and the live test fails when one of the file's
// literals was never produced (requireReached).
var liveGatedFiles = map[string]string{
	"internal/bundle/gitproof/ledger.go":  "TestLiveLedger",
	"internal/bundle/gitproof/sandbox.go": "TestLiveSandbox",
	"internal/bundle/gitproof/witness.go": "TestLiveWitness",
	"internal/bundle/gitproof/write.go":   "TestLiveProofWriter",
	"internal/ledgerbind/ledgerbind.go":   "TestLiveLedgerBinding",
	"internal/gitinv/config.go":           "TestLiveConfig",
	"internal/gitinv/fingerprint.go":      "TestLiveRuntimeMapping",
	"internal/gitinv/runtimemap.go":       "TestLiveRuntimeMapping",
	"internal/gitinv/hardened.go":         "TestLiveMaterialize",
	"internal/gitinv/overlay.go":          "TestLiveMaterialize",
	"internal/minimize/verify.go":         "TestLiveMinimizationVerify",
	"internal/sandbox/plan.go":            "TestLiveSandboxPlan",
}

// liveGated reports whether the file at pos (an absolute path, optionally
// followed by :line) is live-gated.
func liveGated(repo, pos string) bool {
	rel, err := filepath.Rel(repo, pos)
	if err != nil {
		return false
	}
	file, _, _ := strings.Cut(filepath.ToSlash(rel), ":")
	return liveGatedFiles[file] != ""
}

// goldenText is every committed golden (verifier and writer replay), as
// one string.
func goldenText(t *testing.T, repo string) string {
	t.Helper()
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
	return goldens.String()
}

func TestGoldensReachEveryErrorLiteral(t *testing.T) {
	repo := oracle.RepoRoot(t)
	all := goldenText(t, repo)
	literals := gatedLiterals(t, repo)
	for lit, pos := range literals {
		if liveGated(repo, pos) {
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
			t.Errorf("unreachableLiterals lists %q, which is no longer an error literal in a gated package", lit)
		}
	}
}
