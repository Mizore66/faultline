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

	// Round 8: messages thrown through throw(), fail(), p.fail(), panic and
	// error struct literals, which errorSites did not collect before.
	// CLI paths frozen TS runs for real (Go throws `not yet ported`): no
	// golden can compare them; internal/cli's unit tests pin the texts.
	"not yet ported: help":                           "fl with no command prints TS's help, which slice 2 does not port; TestRunVerifyUsageAndUnported pins the text",
	"not yet ported: %s":                             "every unported top-level command (doctor, init, …) runs for real in TS; TestRunVerifyUsageAndUnported pins the text",
	"not yet ported: investigate --ci-log/--resume":  "guided investigate runs in TS (it calls a model); TestInvestigateUsageAndGuards pins the text",
	"not yet ported: investigate turns":              "investigate turns runs in TS; TestInvestigateUsageAndGuards pins the text",
	"not yet ported: turn proof bundle verification": "a turn proof bundle verifies in TS; slice 2 does not port the turn verifier",
	"not yet ported: witness propose --live":         "witness propose --live calls OpenAI in TS (slice 5); TestWitnessUnported pins the text",
	"not yet ported: witness %s":                     "witness implement/review call a model in TS; TestWitnessUnported pins the text",
	"%s must be a Codex thread identifier (1-160 characters, [A-Za-z0-9._:-], starting with a letter or digit).": "Go-only flags (KNOWN_DIFFERENCES: Codex thread IDs): TS ignores them, so no golden can hold the refusal; TestInvestigateUsageAndGuards pins it",
	"%s output must have a file name.": "the output resolves strictly beneath the managed root (checked just before), so its relative path has at least one segment",
	// Witness store invariants and earlier refusals.
	"Reviewer public key could not be exported as SPKI DER.":               "x509.MarshalPKIXPublicKey of an Ed25519 key never fails; Node's export of a parsed Ed25519 key neither",
	"Reviewer key must use Ed25519.":                                       "publicKeyFingerprint is called only after the keyring parser or witness sign refused non-Ed25519 keys with their own messages",
	"Witness approval path escaped its configured store":                   "proposalId is schema-checked as a safe filename segment first, so <store>/authenticated-approvals/<id>.json stays inside the store",
	"Witness approval root must be a real directory:":                      "the store root passes the witness lock's directory checks (verifyFrozenWitness) before the receipt path is built",
	"Witness approval root is not a real directory:":                       "the store root passes the witness lock's directory checks (verifyFrozenWitness) before the receipt path is built",
	"Cannot sign a frozen witness that changed during verification:":       "race only: the frozen record verified an instant before is read again",
	"Reviewer private key does not match the trusted reviewer public key.": "the keyring entry was found by the private key's own fingerprint, and the keyring parser refused entries whose keyId is not their key's fingerprint",
	"trusted public key is not Ed25519":                                    "the keyring parser refused non-Ed25519 keys before verification looks the key up",
	"trusted public-key fingerprint does not match the signed keyId":       "the entry was found by the receipt's keyId, and the keyring parser refused entries whose keyId is not their key's fingerprint",
	"Ratification refused: executed witness digest is malformed.":          "the digest is compared with the stored witness digest first, so a malformed one is refused as a mismatch",
	"Autonomous session path escaped its store":                            "the id is schema-checked as a safe filename segment first",
	"Incident binding path escaped its configured store":                   "the incident id is schema-checked as a safe identifier first",
	"Witness lock path escaped its configured store":                       "proposalId is schema-checked as a safe filename segment first",
	"Standing approval refused:":                                           "the CLI applies a policy only after matchStandingApprovalPolicy accepted the same proposal (tryAutoFreezeFromStandingPolicy, resolveAutonomousProposal)",
	"unknown OpenSSL reason":                                               "a programming error: every reason the PEM and DER readers raise is in the generated table (TestPEMProbes would panic)",
	// Labels of read helpers whose failure an earlier read of the same file
	// reports first.
	"portable Git bundle":       "race only: sourceMetadataFromArtifacts read the same file as \"source Git bundle\" just before (assertRegularFile, then the read)",
	"portable Git range patch":  "race only: sourceMetadataFromArtifacts read the same file as \"source range patch\" just before",
	"investigation artifact":    "race only: loadVerifiedGitProofView reads investigation.json right after the bundle verified it",
	"prevention proof manifest": "fl verify routes to the prevention verifier only after parsing manifest.json and reading its schemaVersion, so the verifier's own read succeeds (short of a race)",
	// Ledger-bound records: Go has no caller of ReadVerified in slice 2.
	"Ledger-bound investigation verification failed:":                      "readVerifiedLedgerBoundInvestigation has no CLI caller in slice 2 (the ported commands write records; none reads one back)",
	"Unable to read verified ledger-bound investigation:":                  "readVerifiedLedgerBoundInvestigation has no CLI caller in slice 2, and the file was just verified",
	"Cannot bind without a verified ledger head and investigation digest.": "a valid binding report always carries the ledger head hash and the investigation digest",
	"Refusing to write an invalid ledger-bound investigation:":             "the CLI writes only the record createLedgerBoundInvestigation just built from a valid report",
	// jsre: refusals of patterns the port cannot translate. Every pattern
	// fl compiles is a constant (TestPortedPatternsCompile); these fire only
	// for a pattern added to the source, and TestUnsupportedPanics covers them.
	"non-ASCII character in pattern": "jsre refuses a pattern constant the port cannot translate; no CLI input reaches it (TestUnsupportedPanics)",
	"unbalanced )":                   "jsre pattern refusal (TestUnsupportedPanics)",
	"pattern can match the empty string (matchAll's empty-match stepping differs from RE2)":     "jsre pattern refusal (TestUnsupportedPanics)",
	"a class that matches surrogates outside W*, W+ or an escaped-run group":                    "jsre pattern refusal (TestUnsupportedPanics)",
	"quantifier over something that can match the empty string (JS refuses an empty iteration)": "jsre pattern refusal (TestUnsupportedPanics)",
	"capture under a quantifier (JS clears it at each iteration and refuses an empty one)":      "jsre pattern refusal (TestUnsupportedPanics)",
	"a run of a class that matches surrogates inside a repetition":                              "jsre pattern refusal (TestUnsupportedPanics)",
	"a run of a class that matches surrogates may be followed by one":                           "jsre pattern refusal (TestUnsupportedPanics)",
	"a lazy run of a class that matches surrogates at the end of the pattern":                   "jsre pattern refusal (TestUnsupportedPanics)",
	"quantified assertion":                                     "jsre pattern refusal (TestUnsupportedPanics)",
	"{ that is not a quantifier":                               "jsre pattern refusal (TestUnsupportedPanics)",
	"trailing backslash":                                       "jsre pattern refusal (TestUnsupportedPanics)",
	"\\B is not translated (it holds inside a surrogate pair)": "jsre pattern refusal (TestUnsupportedPanics)",
	"unsupported escape \\":                                    "jsre pattern refusal (TestUnsupportedPanics)",
	"lookaround and named groups are not translated":           "jsre pattern refusal (TestUnsupportedPanics)",
	"bad class range":                                          "jsre pattern refusal (TestUnsupportedPanics)",
	"\\b in a class":                                           "jsre pattern refusal (TestUnsupportedPanics)",
	"class treats surrogates, U+FFFD and astral characters differently": "jsre pattern refusal (TestUnsupportedPanics)",
	"jsre: /%s/%s: unsupported flag %q":                                 "jsre refuses a flag the port does not implement; every pattern fl compiles is a constant with g or i",
	"jsre: /%s/: match boundary inside a code point":                    "an internal invariant: RE2 over UTF-8 never ends a match inside a code point",
}

// errorLiterals collects the error-message literals of path (errorSites),
// a package directory or one file of it: a file's literals are found with
// its whole package's error helpers (a helper defined in another file).
func errorLiterals(t *testing.T, path string) map[string]string {
	dir, only := path, ""
	if strings.HasSuffix(path, ".go") {
		dir, only = filepath.Dir(path), path
	}
	sites, err := errorSites(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, s := range sites {
		if only == "" || s.file == only {
			out[s.text] = s.pos
		}
	}
	return out
}

// errorSite is one error literal and where it is: errorLiterals keys them by
// text, the statement-coverage gate (coverageGate) by position.
type errorSite struct {
	text, pos, file string
	line            int
}

// errorSites collects the string literals in dir that become error text:
//   - the arguments of errors.New, fmt.Errorf, jsexc.Throw/bundle.Throw and
//     panic, and the message of schema.Regex and schema.Refine (their last
//     argument: zod's custom issue messages);
//   - append(errs, ...) and local error-adding closures (such as
//     `add := func(s string) { errs = append(errs, s) }`), and error lists
//     built as literals (Result{Errors: []string{"…"}}, `return []string{…}`);
//   - the message field of an error struct literal (&XError{...}, the field
//     its Error method returns, positional or keyed);
//   - the message argument of an error helper: a function or method whose
//     string parameter reaches one of the above (directly, or through
//     jsexc.Concat, fmt.Sprintf or +), found to a fixpoint, so throw(...)
//     in witness, fail(...) in cli and the jsre parser's p.fail(...) count,
//     and so does a helper that calls them (round 7: 26 messages thrown
//     through these were never collected);
//   - the text argument of a message constructor: a function named …Err
//     that returns fmt.Sprintf of a string parameter (pemErr, asn1Err),
//     wherever it is called.
func errorSites(dir string) ([]errorSite, error) {
	type parsed struct {
		file *ast.File
		dir  string
	}
	var files []parsed
	fset := token.NewFileSet()
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		files = append(files, parsed{f, filepath.Dir(path)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Per package directory: error struct types (name → message field
	// index and name), helpers (name → message parameter indexes; methods
	// are keyed ".name") and message constructors.
	type pkgInfo struct {
		structs      map[string]errorStruct
		helpers      map[string]map[int]bool
		constructors map[string]int
	}
	pkgs := map[string]*pkgInfo{}
	for _, p := range files {
		if pkgs[p.dir] == nil {
			pkgs[p.dir] = &pkgInfo{map[string]errorStruct{}, map[string]map[int]bool{}, map[string]int{}}
		}
	}
	for _, p := range files {
		info := pkgs[p.dir]
		for name, s := range errorStructs(p.file) {
			info.structs[name] = s
		}
		for name, i := range messageConstructors(p.file) {
			info.constructors[name] = i
		}
	}
	// sinkArgs gives the arguments of a call that carry error text, or nil.
	sinkArgs := func(info *pkgInfo, adders map[string]bool, call *ast.CallExpr) []ast.Expr {
		args := call.Args
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			switch {
			case fn.Name == "panic" || adders[fn.Name]:
				return args
			case fn.Name == "append" && len(args) >= 2 && appendsToErrors(args[0]):
				return args[1:]
			case info.helpers[fn.Name] != nil:
				return pickArgs(args, info.helpers[fn.Name])
			}
		case *ast.SelectorExpr:
			switch fn.Sel.Name {
			case "New", "Errorf", "Throw":
				return args
			case "Regex", "Refine":
				if len(args) > 0 {
					return args[len(args)-1:]
				}
			}
			if h := info.helpers["."+fn.Sel.Name]; h != nil {
				return pickArgs(args, h)
			}
		}
		return nil
	}
	// structArg is the message field's value of an error struct literal.
	structArg := func(info *pkgInfo, lit *ast.CompositeLit) ast.Expr {
		s, ok := info.structs[exprString(lit.Type)]
		if !ok {
			return nil
		}
		for i, e := range lit.Elts {
			if kv, ok := e.(*ast.KeyValueExpr); ok {
				if key, ok := kv.Key.(*ast.Ident); ok && key.Name == s.field {
					return kv.Value
				}
			} else if i == s.index {
				return e
			}
		}
		return nil
	}
	// Helpers to a fixpoint: a string parameter that reaches a sink.
	for changed := true; changed; {
		changed = false
		for _, p := range files {
			info := pkgs[p.dir]
			adders := errorClosures(p.file)
			forEachFunc(p.file, func(name string, typ *ast.FuncType, body *ast.BlockStmt) {
				params := stringParams(typ)
				if len(params) == 0 {
					return
				}
				ast.Inspect(body, func(n ast.Node) bool {
					var carried []ast.Expr
					switch n := n.(type) {
					case *ast.CallExpr:
						carried = sinkArgs(info, adders, n)
					case *ast.CompositeLit:
						if e := structArg(info, n); e != nil {
							carried = []ast.Expr{e}
						}
					}
					for _, e := range carried {
						for _, i := range flowingParams(e, params) {
							if info.helpers[name] == nil {
								info.helpers[name] = map[int]bool{}
							}
							if !info.helpers[name][i] {
								info.helpers[name][i] = true
								changed = true
							}
						}
					}
					return true
				})
			})
		}
	}
	var out []errorSite
	for _, p := range files {
		info := pkgs[p.dir]
		adders := errorClosures(p.file)
		collect := func(exprs []ast.Expr) { collectLiterals(fset, exprs, &out) }
		ast.Inspect(p.file, func(n ast.Node) bool {
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
			case *ast.CompositeLit:
				if e := structArg(info, n); e != nil {
					collect([]ast.Expr{e})
				}
			case *ast.CallExpr:
				if fn, ok := n.Fun.(*ast.Ident); ok {
					if i, ok := info.constructors[fn.Name]; ok && i < len(n.Args) {
						collect(n.Args[i : i+1])
					}
				}
				collect(sinkArgs(info, adders, n))
			}
			for _, e := range lists {
				if lit, ok := e.(*ast.CompositeLit); ok && isStringSlice(lit.Type) {
					collect(lit.Elts)
				}
			}
			return true
		})
	}
	return out, nil
}

// errorStruct is the field an error type's Error method returns.
type errorStruct struct {
	field string
	index int
}

// errorStructs finds `func (e *T) Error() string { return e.F }` with T a
// struct of f, and the position of F among T's fields.
func errorStructs(f *ast.File) map[string]errorStruct {
	fields := map[string][]string{}
	for _, d := range f.Decls {
		gen, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			var names []string
			for _, fld := range st.Fields.List {
				for _, n := range fld.Names {
					names = append(names, n.Name)
				}
				if len(fld.Names) == 0 {
					names = append(names, exprString(fld.Type))
				}
			}
			fields[ts.Name.Name] = names
		}
	}
	out := map[string]errorStruct{}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || fn.Name.Name != "Error" || fn.Body == nil || len(fn.Body.List) != 1 {
			continue
		}
		ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 {
			continue
		}
		sel, ok := ret.Results[0].(*ast.SelectorExpr)
		if !ok {
			continue
		}
		typ := exprString(fn.Recv.List[0].Type)
		for i, name := range fields[typ] {
			if name == sel.Sel.Name {
				out[typ] = errorStruct{name, i}
			}
		}
	}
	return out
}

// messageConstructors finds the functions named …Err whose body is
// `return fmt.Sprintf(format, …, param, …)` with a string param: the index
// of that param.
func messageConstructors(f *ast.File) map[string]int {
	out := map[string]int{}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !strings.HasSuffix(fn.Name.Name, "Err") || fn.Body == nil || len(fn.Body.List) != 1 {
			continue
		}
		ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 {
			continue
		}
		call, ok := ret.Results[0].(*ast.CallExpr)
		if !ok {
			continue
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "Sprintf" {
			continue
		}
		params := stringParams(fn.Type)
		for _, a := range call.Args[1:] {
			if id, ok := a.(*ast.Ident); ok {
				if i, ok := params[id.Name]; ok {
					out[fn.Name.Name] = i
				}
			}
		}
	}
	return out
}

// forEachFunc calls visit for every function declaration (a method as
// ".name") and every local `name := func(...) {...}` of f.
func forEachFunc(f *ast.File, visit func(name string, typ *ast.FuncType, body *ast.BlockStmt)) {
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			if n.Body == nil {
				return true
			}
			name := n.Name.Name
			if n.Recv != nil {
				name = "." + name
			}
			visit(name, n.Type, n.Body)
		case *ast.AssignStmt:
			if len(n.Lhs) == 1 && len(n.Rhs) == 1 {
				id, ok := n.Lhs[0].(*ast.Ident)
				lit, ok2 := n.Rhs[0].(*ast.FuncLit)
				if ok && ok2 {
					visit(id.Name, lit.Type, lit.Body)
				}
			}
		}
		return true
	})
}

// stringParams maps each string parameter of typ to its index.
func stringParams(typ *ast.FuncType) map[string]int {
	out := map[string]int{}
	i := 0
	for _, fld := range typ.Params.List {
		isString := exprString(fld.Type) == "string"
		if len(fld.Names) == 0 {
			i++
			continue
		}
		for _, n := range fld.Names {
			if isString {
				out[n.Name] = i
			}
			i++
		}
	}
	return out
}

// flowingParams lists the params that e carries as text: the identifier
// itself, or inside jsexc.Concat, fmt.Sprintf or a + concatenation.
func flowingParams(e ast.Expr, params map[string]int) []int {
	var out []int
	var walk func(e ast.Expr)
	walk = func(e ast.Expr) {
		switch e := e.(type) {
		case *ast.Ident:
			if i, ok := params[e.Name]; ok {
				out = append(out, i)
			}
		case *ast.ParenExpr:
			walk(e.X)
		case *ast.BinaryExpr:
			if e.Op == token.ADD {
				walk(e.X)
				walk(e.Y)
			}
		case *ast.CallExpr:
			if sel, ok := e.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "Concat" || sel.Sel.Name == "Sprintf") {
				for _, a := range e.Args {
					walk(a)
				}
			}
		}
	}
	walk(e)
	return out
}

// pickArgs is the arguments at the indexes in which.
func pickArgs(args []ast.Expr, which map[int]bool) []ast.Expr {
	var out []ast.Expr
	for i, a := range args {
		if which[i] {
			out = append(out, a)
		}
	}
	return out
}

// collectLiterals adds the message literals in exprs to out.
func collectLiterals(fset *token.FileSet, exprs []ast.Expr, out *[]errorSite) {
	for _, a := range exprs {
		ast.Inspect(a, func(m ast.Node) bool {
			// Literals inside other calls (v.Get("key")) are not
			// message text; jsexc.Concat and fmt.Sprintf build the
			// message itself.
			if inner, ok := m.(*ast.CallExpr); ok {
				sel, ok := inner.Fun.(*ast.SelectorExpr)
				return ok && (sel.Sel.Name == "Concat" || sel.Sel.Name == "Sprintf")
			}
			// An error struct literal's message field is collected on
			// its own (errorStructs); its other fields (a code) are not
			// message text.
			if _, ok := m.(*ast.CompositeLit); ok {
				return false
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
	// The OpenSSL PEM and DER readers: their reason texts are compared on
	// thousands of generated key texts (round 8).
	"internal/witness/pemread.go": "TestLivePEMKeyParsing",
	"internal/witness/asn1dec.go": "TestLivePEMKeyParsing",
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
