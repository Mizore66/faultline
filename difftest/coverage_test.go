//go:build faultline_test

package difftest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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

	// prevention/write.go (TestLivePreventionWrite's requireReached):
	"Prevention proof root is not a real directory:":                             "the filesystem root is always a real directory",
	"Prevention proof output appeared during assembly and will not be replaced:": "race only: the output was refused when it existed before staging",
	"Refusing to publish an invalid prevention proof:":                           "a stage the writer just wrote verifies unless the filesystem fails between the writes and the verify; an unreadable stage (umask 0477) fails its removal too, and that error is thrown instead",

	// prevention/artifacts.go (TestLivePreventionFromArtifacts): the Git proof
	// verifier runs first, and a bundle it accepts reconstructs its stable
	// states and transitions from runs that all match the state's commit and
	// tree, are DOCKER_ISOLATED NATIVE_DOCKER with one decisive verdict and a
	// PREDICATE_PASS / PREDICATE_FAIL reason, and carry the frozen witness
	// digests; a PASS_TO_FAIL transition goes from a PASS to a FAIL state.
	// TS's "repairBaseTree must equal the first-bad state's tree" check is
	// not ported (artifacts.go): the CLI passes no repairBaseTree, which then
	// defaults to that tree. The writer case prevention-write-bundle-base-
	// tree-mismatch is named for it but pins the refusal the CLI can reach
	// (--repaired-tree set to the first-bad tree: "Repaired run … commit/tree
	// does not match the repaired state."), not that check.
	"Prevention export requires a Docker-isolated proof-grade Git investigation.": "the Git proof semantics refuse a non-proof or non-NATIVE_DOCKER investigation first",
	"stable state at":                                 "a reconstructed PASS_TO_FAIL transition has a PASS before and a FAIL after state",
	"is not present in the proof bundle":              "stable states are reconstructed from the bundle's own runs",
	"commit/tree does not match stable state":         "a reconstructed stable state's runs all match its commit and tree",
	"witness digests do not match the frozen witness": "the Git proof semantics refuse a run whose digests differ from the frozen witness",
	"is not NATIVE_DOCKER":                            "a reconstructed stable state's runs are all NATIVE_DOCKER",
	"is not DOCKER_ISOLATED":                          "a reconstructed stable state's runs are all DOCKER_ISOLATED",
	"PASS reason must be PREDICATE_PASS":              "the Git proof semantics refuse a PASS run without PREDICATE_PASS",
	"FAIL reason must be PREDICATE_FAIL":              "the Git proof semantics refuse a FAIL run without PREDICATE_FAIL",

	// Slice 2 writers. Git's own invariants: hardened git prints what these
	// checks expect unless it is broken.
	"Git range enumeration returned an invalid commit object id.":   "git rev-list prints full object ids",
	"Git did not return an absolute repository root.":               "git rev-parse --show-toplevel prints an absolute path or fails",
	"Git did not return an absolute repository metadata directory.": "git rev-parse --absolute-git-dir prints an absolute path or fails",
	"Git -z output ended without a NUL terminator.":                 "git diff --name-status -z ends every field with NUL",
	"Git path unexpectedly contains a NUL byte.":                    "the -z output is split at NUL, so no field holds one",
	"Git --name-status -z output was malformed.":                    "git diff --name-status -z gives a status then one or two paths",
	"Could not enumerate changed Git paths:":                        "git diff --name-status reports a missing subtree on stderr but exits 0, and the attribute scan (git grep) over both commits refuses an unreadable tree first",
	// Internal invariants and earlier refusals.
	"Missing preflight environment fingerprint for":                            "the preflight fingerprints every state this loop materializes",
	"Missing runtime image mapping for":                                        "buildEffectiveRuntimeMapping maps every preflight digest to a non-empty image, and a heterogeneous range with a gap is refused just before",
	"Candidate references a patch unit that does not belong to this Git diff.": "candidates are built from the same diff's patch unit ids",
	"trusted reviewer public key is invalid:":                                  "parseReviewerKeyring already refused keys that are not Ed25519 or whose fingerprint is not their keyId",
	"Frozen overlay path escaped its worktree:":                                "safeOverlayParts refuses empty, '.' and '..' segments and backslashes first, so the target stays inside the worktree",
	"Minimization output root is not a real directory:":                        "the CLI's safeManagedFileOutput refuses the same output first",
	"Minimization output parent is not a real directory:":                      "race only: the directory was just created by mkdir",
	"FaultLine config path escaped .faultline":                                 "the config path is <dir>/config.json and resolveTrustedSystemPath rewrites the same /var or /tmp alias prefix of both, so the relative path is always config.json",
	"Minimization output cannot traverse a symbolic link or non-directory:":    "the CLI's safeManagedFileOutput refuses the same output first",
	"minimization result verification failed safely:":                          "a throw inside the checks of a record that passed the schema: only a V8 limit (string length, stack depth) gets there",
	// Races: the file or directory changes between two steps of fl.
	"Could not remove temporary materialized tree:":                                        "race only: rm of the worktree and index fl just created under its private 0700 temp root",
	"Could not verify materialized Git tree":                                               "only a filesystem that folds case reaches it (a tree with A.txt and a.txt: update-index --refresh fails); TestLiveMaterialize requires it on macOS (stack-live), and on Linux both files check out",
	"Materialized Git tree does not match its external index for":                          "race only: filters are refused and core.autocrlf is off, so a fresh checkout matches its index",
	"FaultLine could not verify exact materialized overlay bytes:":                         "race only: the overlay was just renamed into place",
	"Could not safely create overlay directory:":                                           "race only: the directory was just created by mkdir",
	"Path is a directory: rm returned EISDIR (is a directory)":                             "race only: the staged name carries a fresh UUID",
	"Git minimization result already exists and will not be replaced:":                     "race only: the CLI's safeManagedFileOutput refused an existing output just before",
	"minimization result changed beyond FaultLine's verification read limit while reading": "race only: lstat just reported a size within the limit",
	"Generated Git proof bundle failed verification:":                                      "race only: the bundle fl just wrote verifies unless it changes underneath",
	"Generated incident proof bundle failed verification:":                                 "race only: the bundle incident continue just wrote verifies unless it changes underneath (the Git bundle text above is the same check)",
	"Refusing to render a Git proof bundle that changed during verification:":              "race only: the bundle is verified twice in a row",
	"Unable to read verified":                                                              "race only: the file was just verified, so it exists and parses",

	// Round 8: messages thrown through throw(), fail(), p.fail(), panic and
	// error struct literals, which errorSites did not collect before.
	// CLI paths frozen TS runs for real (Go throws `not yet ported`): no
	// golden can compare them; internal/cli's unit tests pin the texts.
	"not yet ported: help":                           "fl with no command prints TS's help, which slice 2 does not port; internal/cli TestFailures pins the text",
	"not yet ported: %s":                             "every unported top-level command (codex, record, prove, attest, provenance and repair in slice 4; serve, ui, demo, tutorial and the rest in slice 5) runs for real in TS; internal/cli TestFailures pins the text",
	"not yet ported: investigate --ci-log/--resume":  "guided investigate runs in TS (it calls a model); TestInvestigateUsageAndGuards pins the text",
	"not yet ported: investigate turns":              "investigate turns runs in TS; TestInvestigateUsageAndGuards pins the text",
	"not yet ported: turn proof bundle verification": "a turn proof bundle verifies in TS; slice 2 does not port the turn verifier",
	"not yet ported: witness propose --live":         "witness propose --live calls OpenAI in TS (slice 5); TestWitnessProposeSampleAndUnported pins the text",
	"not yet ported: witness %s":                     "witness implement/review call a model in TS; TestWitnessProposeSampleAndUnported pins the text",
	"%s must be a Codex thread identifier (1-160 characters, [A-Za-z0-9._:-], starting with a letter or digit).": "Go-only flags (KNOWN_DIFFERENCES: Codex thread IDs): TS ignores them, so no golden can hold the refusal; TestInvestigateUsageAndGuards pins it",
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
	"Incident draft path escaped its configured store":                     "the draft id is schema-checked as a safe identifier first",
	"Witness lock path escaped its configured store":                       "proposalId is schema-checked as a safe filename segment first",
	"Standing approval refused:":                                           "the CLI applies a policy only after matchStandingApprovalPolicy accepted the same proposal (tryAutoFreezeFromStandingPolicy, resolveAutonomousProposal)",
	"unknown OpenSSL reason":                                               "a programming error: every reason the PEM and DER readers raise is in the generated table (TestPEMProbes would panic)",

	// Round 8 review: messages that flow through return values, []string
	// helpers, cross-package helpers and message variables.
	// Defaults overwritten before use or reached only through an earlier refusal:
	"Investigation did not produce proof.":                                                                    "baseResult's default reason; every caller passes at least one error",
	"Frozen witness did not match the externally supplied digest.":                                            "VerifyFrozenWitnessRecord reports an error whenever it is not a MATCH with an expected digest supplied, so the fallback list is never used",
	"A separately supplied frozen witness digest did not match.":                                              "VerifyFrozenWitnessRecord reports an error whenever it is not a MATCH with an expected digest supplied, so the fallback list is never used",
	"Git range resolved to no states.":                                                                        "resolveCommitRange always returns the ancestor at least",
	"Sandbox configuration failed.":                                                                           "a configuration error is always recorded in errs first, whose first entry becomes the reason",
	"An injected runner produced these observations; FaultLine refuses to certify it as native Docker proof.": "the CLI always runs the native Docker runner; only an injected runner (library callers) gets here",
	"Unsafe local execution is explicitly inapplicable and cannot be proof.":                                  "the sandbox schemas accept only DOCKER_ISOLATED for new runs, so no CLI run is UNSAFE_LOCAL",
	"Unsafe local execution is explicitly inapplicable and cannot support proof.":                             "the sandbox schemas accept only DOCKER_ISOLATED for new runs, so no CLI run is UNSAFE_LOCAL",
	"Environment descriptors (lockfiles/toolchains) changed across the investigated range; FaultLine refuses a single-image proof grade. Provide per-fingerprint runtimes or narrow the range.": "the preflight refuses a heterogeneous range without a complete mapping (CONFIGURATION_ERROR) before any run, and a mapped range is complete",
	"the bundle changed while its artifacts were being read":                        "race only: a failed re-verification always reports its errors, which replace this default",
	"the bundle presentation artifacts changed during verification":                 "race only: the bundle is verified three times in a row",
	"no valid root digest was produced":                                             "a failed verification always reports at least one error, which replaces this default",
	"FaultLine refuses to name a first bad state without a stable bracket.":         "page text: commit-proof-preview-no-regression writes it into the preview HTML, which the writer golden compares by digest (goldens hold file digests, not contents)",
	"human owned the question after execution; digests identical":                   "the default ratification note is written into the ratification record, which the CLI does not print and the writer goldens hash (the file digest covers it)",
	"--require-signature needs a reviewer keyring":                                  "the CLI refuses --require-signature without --keyring itself (\"--require-signature needs --keyring …\") before verification runs",
	"authenticated reviewer signature cannot be trusted without a reviewer keyring": "the CLI refuses --require-signature without --keyring itself before verification runs",
	"frozen witness changed during authenticated approval verification":             "race only: the frozen record verified an instant before is read again",
	"policy network must be disabled":                                               "the policy schema requires network \"disabled\" (a zod literal), parsed before matching",
	"proposal network is not disabled":                                              "the proposal schema requires network \"disabled\" (a zod literal)",
	"proposal credentials are not redacted":                                         "the proposal schema requires credentials \"redacted\" (a zod literal)",
	"Invalid lifecycle ledger at":                                                   "both callers bind a ledger that readVerifiedCodexLifecycleLedger, or the bound-record schema, already parsed with the same schema",
	"Invalid Git investigation at":                                                  "both callers bind an investigation already parsed with the same schema (the CLI, the investigation result, the bound-record schema)",
	"Invalid Git checkpoint at":                                                     "the ledger schema parses every WORKTREE_CHECKPOINT payload's checkpoint with the same schema first",
	"Invalid turn tree snapshot at":                                                 "the ledger schema parses every snapshot payload with the same schema first",
	"content mismatch":                                                              "git diff-files --quiet reports a difference by exit 1 with no stderr only when the worktree changes under the fresh checkout (race only); the bounded-run reasons are checked by TestLiveHardenedGit",
	"Git command exceeded bounded execution time":                                   "materialization runs a real git over a fresh checkout; TestLiveHardenedGit compares this reason with a slow fake git",
	"Git command exceeded bounded output limit":                                     "materialization runs a real git over a fresh checkout; TestLiveHardenedGit compares this reason with a flooding fake git",
	// minimize.go (live-gated by TestLiveMinimize): branches its generated
	// repositories cannot take.
	"Minimization did not produce proof.":                                 "baseResult's default reason; every caller passes at least one error",
	"Git returned no patch.":                                              "git diff of a path the same name-status listing just reported always prints a patch or fails with stderr",
	"FaultLine did not produce a patch application result.":               "every path through the probe sets the application fact before this fallback",
	"No sandbox execution was produced.":                                  "every path through the probe sets a note before this fallback",
	"Git apply failed.":                                                   "git apply always explains a refusal on stderr (beyond a symbolic link, not a directory, invalid path), so the fallback detail is never used",
	"Could not verify applied patch scope:":                               "git diff --cached --name-only -z after an apply fl just ran fails only on an I/O error",
	"Certification executions were not distinct.":                         "each execution draws a fresh UUID nonce, so three executions are always distinct",
	"Every single-unit removal was tested.":                               "overwritten: a completed removal pass sets \"Every single-unit removal produced a Docker PASS.\" or the unresolved reason",
	"A smaller failing candidate was found; retesting its removals.":      "overwritten when the restarted removal pass completes",
	"Every single-hunk removal was tested.":                               "overwritten: a completed hunk pass sets the after-file-level-refinement reason",
	"A smaller failing hunk candidate was found; retesting its removals.": "overwritten when the restarted hunk pass completes",
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

	// internal/projinit (slice 3a Task 10): SidecarCliCommand and
	// CodexSidecarLedgerPath are shared with `fl codex sidecar`, not ported
	// yet; `fl init` passes only `--cli <resolved path>` and the fixed
	// session id.
	"must not be empty.": "assertSidecarCommandText runs on --command/--command-windows only, which `fl init` never passes (fl codex sidecar config/install)",
	"Provide exactly one of --cli <built-cli.js> or --command <stable-hook-command>.": "fl init calls sidecarCliCommand with exactly [--cli, path] (fl codex sidecar config/install)",
	"--command-windows": "the --command-windows label of assertSidecarCommandText, reached only through fl codex sidecar config/install",
	"--command-windows is only valid with an explicit --command; --cli generates both platform commands safely.": "fl init never passes --command-windows to sidecarCliCommand (fl codex sidecar config/install)",
	"faultline-install-check":                                       "the fixed session id HookTarget passes to codexSidecarLedgerPath: an argument, not output",
	"FaultLine observes this Codex session":                         "a statusMessage written into .codex/hooks.json, compared by the file digest, not printed",
	"FaultLine records an observed Codex turn":                      "a statusMessage written into .codex/hooks.json, compared by the file digest, not printed",
	"FaultLine records tool-use attribution (digests only)":         "a statusMessage written into .codex/hooks.json, compared by the file digest, not printed",
	"FaultLine records tool-use completion (digests only)":          "a statusMessage written into .codex/hooks.json, compared by the file digest, not printed",
	"FaultLine captures a clean observed checkpoint when available": "a statusMessage written into .codex/hooks.json, compared by the file digest, not printed",
	"is unavailable:":                  "race only: assertSafeDirectory lstats a directory realpathSync or safeDirectoryExists has just accepted",
	"FaultLine Git metadata directory": "race only: the label of assertSafeDirectory on the realpath of git's common directory, which exists when git reports it",
	"Git did not report a common metadata directory for this worktree.": "git rev-parse --git-common-dir prints a path or fails",
	"FaultLine could not resolve Git metadata storage:":                 "race only: realpathSync of the common directory git just reported",

	// internal/runtimeimage/context.go (live-gated by TestLiveProjectContext):
	"FaultLine could not read the reviewed Dockerfile": "race only: the Dockerfile resolves inside the context, so the walk has just read it (an unreadable one fails there as `could not read build-context file`)",

	// internal/turnsnap (live-gated by TestLiveTurnSnapshotScan and
	// TestLiveTurnSnapshotCapture): Git's invariants and races with the
	// worktree.
	"Could not read":                                                                   "race only: the scan's file (`Could not read <path> for snapshot secret scanning`) was just read whole by computePathDigests, whose failure propagates first",
	"for snapshot secret scanning:":                                                    "race only: the scan's file was just read whole by computePathDigests, whose failure propagates first",
	"FaultLine could not stat":                                                         "race only: computeDirtyFingerprint lstats the path just before fullFileContentDigest does",
	"for content fingerprinting.":                                                      "race only: computeDirtyFingerprint lstats the path just before fullFileContentDigest does",
	"Refusing to fingerprint non-regular path:":                                        "race only: computeDirtyFingerprint skips symlinks and non-regular files just before",
	"bytes while hashing.":                                                             "race only: the file grew between its lstat and the read",
	"Refusing turn-tree snapshot: path is not a regular file:":                         "race only: computeIndexDelta keeps only paths it has just lstat'ed as regular files",
	"FaultLine could not prove turn-tree quiescence after":                             "race only: four dual-tree attempts must each see the worktree change between two status samples (the unit tests drive it with a scripted runner)",
	"dual-tree attempt(s) and refused a torn turn tree snapshot.":                      "race only: four dual-tree attempts must each see the worktree change between two status samples",
	"Git did not return a valid HEAD tree object id for the clean worktree fast path.": "git rev-parse HEAD^{tree} prints a full object id or fails",
	"Git did not return a valid HEAD tree object id for incremental turn staging.":     "git rev-parse HEAD^{tree} prints a full object id or fails",
	"git hash-object --stdin did not return a blob id for":                             "git hash-object -w --stdin prints a full object id or fails",
	"Git did not return a valid tree object id for this turn tree snapshot.":           "git write-tree prints a full object id or fails",
	"Git did not return a resolvable HEAD commit for this turn tree snapshot.":         "git rev-parse HEAD prints a full object id or fails (an unborn HEAD fails)",
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

// errorSites collects the string literals in dir that become error or
// refusal text, by following message flow through every internal package
// (round 7 #4, round 8 review):
//   - sinks: the arguments of errors.New, fmt.Errorf, jsexc.Throw /
//     bundle.Throw and panic; the message of schema.Regex and schema.Refine
//     (their last argument, zod's custom issue message); the message field
//     of an error struct literal (the field its Error method returns); a
//     composite-literal field or a JSON key ("errors", "reason", "note", …,
//     followed by its value in obj(…), o.Set(…), MustObject(…)) named like a
//     message (messageName);
//   - message variables: a local variable, parameter or field named like a
//     message, or one that some message expression reads; whatever is
//     assigned or appended to it is a message expression too;
//   - helpers: a function, method or local closure whose parameter is a
//     message variable (witness throw, cli fail, jsre p.fail,
//     failedVerification, a failure(errs []string, …) closure,
//     gitproof.IssueLines, nodeproc.InvalidArgValue): its arguments there
//     are message expressions, across packages;
//   - message functions: a function whose result is used as a message
//     (assigned to a message variable or read by a message expression:
//     outcomeNote, forceRemoveContainer, errorSummary): its returned
//     expressions at that position are message expressions;
//   - error lists built as literals (`return []string{"…"}`);
//   - the text argument of a message constructor (a function named …Err
//     that returns fmt.Sprintf of a string parameter: pemErr, asn1Err).
//
// A message expression is followed through jsexc.Concat, fmt.Sprintf,
// jsexc.Join, the jsjson/jsstr string wrappers, []string literals, append
// and +; its literals of at least 12 characters (between format verbs) are
// the sites. All of internal/ is analysed, so helpers in ungated packages
// count, but only sites in dir are returned.
func errorSites(dir string) ([]errorSite, error) {
	a, err := loadFlow(dir)
	if err != nil {
		return nil, err
	}
	return a.sites(dir), nil
}

// flowCache keeps one analysis per module root: errorSites is called once
// per gated package.
var flowCache = map[string]*flowAnalysis{}

func loadFlow(dir string) (*flowAnalysis, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	root := abs
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			return nil, os.ErrNotExist
		}
		root = parent
	}
	if a := flowCache[root]; a != nil {
		return a, nil
	}
	a := &flowAnalysis{fset: token.NewFileSet(), root: root,
		helpers: map[string]map[int]bool{}, results: map[string]map[int]bool{}, vars: map[string]bool{},
		structs: map[string]errorStruct{}, constructors: map[string]int{}, found: map[string]errorSite{}}
	err = filepath.Walk(filepath.Join(root, "internal"), func(path string, info os.FileInfo, err error) error {
		// testing.go files are test support compiled into their package.
		if err != nil || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || filepath.Base(path) == "testing.go" {
			return err
		}
		f, err := parser.ParseFile(a.fset, path, nil, 0)
		if err != nil {
			return err
		}
		a.files = append(a.files, flowFile{f, filepath.Dir(path), path, fileImports(f)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, f := range a.files {
		for name, s := range errorStructs(f.file) {
			a.structs[f.dir+"."+name] = s
		}
		for name, i := range messageConstructors(f.file) {
			a.constructors[f.dir+"."+name] = i
		}
	}
	a.run()
	flowCache[root] = a
	return a, nil
}

type flowFile struct {
	file      *ast.File
	dir, path string
	imports   map[string]string // local name → package directory
}

// fileImports maps the local names of f's module imports to their directories.
func fileImports(f *ast.File) map[string]string {
	const module = "github.com/Mizore66/faultline/"
	out := map[string]string{}
	for _, imp := range f.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		if !strings.HasPrefix(p, module) {
			continue
		}
		name := p[strings.LastIndex(p, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		out[name] = filepath.FromSlash(strings.TrimPrefix(p, module))
	}
	return out
}

type flowAnalysis struct {
	fset  *token.FileSet
	root  string
	files []flowFile
	// helpers and results are keyed by function: "<dir>.<name>" (a method
	// or closure: "<dir>..<name>"); vars by "<function>|<name>".
	helpers, results map[string]map[int]bool
	vars             map[string]bool
	structs          map[string]errorStruct
	constructors     map[string]int
	found            map[string]errorSite // by position
	changed          bool
}

// messageWords are the identifier words (camelCase split, lower case) and
// JSON keys that name a message.
var messageWords = map[string]bool{
	"err": true, "errs": true, "error": true, "errors": true, "reason": true, "reasons": true,
	"note": true, "notes": true, "detail": true, "details": true, "message": true, "messages": true,
	"msg": true, "stderr": true,
}

var identWord = regexp.MustCompile(`[A-Z]?[a-z0-9]+|[A-Z]+(?:[A-Z][a-z]|$)`)

// messageName reports whether an identifier or key names a message.
func messageName(name string) bool {
	for _, w := range identWord.FindAllString(name, -1) {
		if messageWords[strings.ToLower(w)] {
			return true
		}
	}
	return false
}

// dataName are parameter names that carry data a message quotes (a path,
// a pattern, an id), never message text: a helper is not inferred from
// them (jsre.MustCompile's source). Nor is one inferred in the system-call
// wrappers (systemPackages), whose errors quote their arguments
// (nodefs.Mkdtemp's prefix).
var dataName = map[string]bool{
	"path": true, "paths": true, "dir": true, "directory": true, "root": true, "file": true,
	"source": true, "flags": true, "pattern": true, "target": true, "src": true, "dest": true,
	"destination": true, "repository": true, "cwd": true, "id": true, "commit": true, "revision": true,
	"name": true, "value": true, "argv": true, "args": true,
}

var systemPackages = map[string]bool{"nodefs": true, "nodeproc": true, "safedir": true}

// flowFunc is one function body being analysed.
type flowFunc struct {
	key    string
	params map[string]int
	file   *flowFile
}

func (a *flowAnalysis) run() {
	for pass := 0; ; pass++ {
		a.changed = false
		for i := range a.files {
			f := &a.files[i]
			a.visitFile(f)
		}
		if !a.changed || pass > 50 {
			return
		}
	}
}

func (a *flowAnalysis) mark(m map[string]map[int]bool, key string, i int) {
	if m[key] == nil {
		m[key] = map[int]bool{}
	}
	if !m[key][i] {
		m[key][i] = true
		a.changed = true
	}
}

func (a *flowAnalysis) markVar(fn *flowFunc, name string) {
	k := fn.key + "|" + name
	if !a.vars[k] {
		a.vars[k] = true
		a.changed = true
	}
	if i, ok := fn.params[name]; ok {
		a.mark(a.helpers, fn.key, i)
	}
}

func (a *flowAnalysis) isVar(fn *flowFunc, name string) bool {
	return messageName(name) || a.vars[fn.key+"|"+name]
}

// funcKey resolves the function a call names: a package function or a
// cross-package one, or a method or closure by name in the package.
func (a *flowAnalysis) funcKeys(f *flowFile, call *ast.CallExpr) []string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return []string{f.dir + "." + fn.Name, f.dir + ".." + fn.Name}
	case *ast.SelectorExpr:
		if x, ok := fn.X.(*ast.Ident); ok {
			if d, ok := f.imports[x.Name]; ok {
				return []string{filepath.Join(a.root, d) + "." + fn.Sel.Name}
			}
		}
		return []string{f.dir + ".." + fn.Sel.Name, "*.." + fn.Sel.Name}
	}
	return nil
}

func (a *flowAnalysis) lookup(m map[string]map[int]bool, keys []string) map[int]bool {
	out := map[int]bool{}
	for _, k := range keys {
		if strings.HasPrefix(k, "*..") {
			name := k[1:]
			for key, v := range m {
				if strings.HasSuffix(key, name) {
					for i := range v {
						out[i] = true
					}
				}
			}
			continue
		}
		for i := range m[k] {
			out[i] = true
		}
	}
	return out
}

func (a *flowAnalysis) visitFile(f *flowFile) {
	forEachFunc(f.file, func(name string, typ *ast.FuncType, body *ast.BlockStmt, method bool) {
		key := f.dir + "." + name
		if method {
			key = f.dir + ".." + name
		}
		fn := &flowFunc{key: key, params: map[string]int{}, file: f}
		i := 0
		for _, fld := range typ.Params.List {
			if len(fld.Names) == 0 {
				i++
			}
			for _, n := range fld.Names {
				if !dataName[n.Name] && !systemPackages[filepath.Base(f.dir)] {
					fn.params[n.Name] = i
				}
				if messageName(n.Name) {
					a.mark(a.helpers, key, i)
				}
				i++
			}
		}
		var named []string
		if typ.Results != nil {
			for _, fld := range typ.Results.List {
				if len(fld.Names) == 0 {
					named = append(named, "")
				}
				for _, n := range fld.Names {
					named = append(named, n.Name)
				}
			}
		}
		ast.Inspect(body, func(n ast.Node) bool {
			// A closure's returns are its own; the rest of its body shares
			// the enclosing function's variables (a named closure is also
			// analysed on its own, as a helper).
			if lit, ok := n.(*ast.FuncLit); ok && lit.Body != body {
				ast.Inspect(lit.Body, func(m ast.Node) bool {
					if _, ok := m.(*ast.ReturnStmt); !ok {
						a.visitNode(fn, m, nil)
					}
					return true
				})
				return false
			}
			a.visitNode(fn, n, named)
			return true
		})
	})
	// Package-level declarations (schemas with custom messages).
	top := &flowFunc{key: f.dir + ".<package>", params: map[string]int{}, file: f}
	for _, d := range f.file.Decls {
		if gen, ok := d.(*ast.GenDecl); ok {
			ast.Inspect(gen, func(n ast.Node) bool {
				a.visitNode(top, n, nil)
				return true
			})
		}
	}
}

// visitNode applies the sink rules of one node of fn.
func (a *flowAnalysis) visitNode(fn *flowFunc, n ast.Node, named []string) {
	f := fn.file
	switch n := n.(type) {
	case *ast.CallExpr:
		for _, e := range a.sinkArgs(fn, n) {
			a.message(fn, e)
		}
		if fid, ok := n.Fun.(*ast.Ident); ok {
			if i, ok := a.constructors[f.dir+"."+fid.Name]; ok && i < len(n.Args) {
				a.message(fn, n.Args[i])
			}
		}
		// JSON keys: a literal key named like a message, then its value.
		for i := 0; i+1 < len(n.Args); i++ {
			if lit, ok := n.Args[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if k, _ := strconv.Unquote(lit.Value); messageWords[k] {
					a.message(fn, n.Args[i+1])
				}
			}
		}
	case *ast.CompositeLit:
		if s, ok := a.structs[f.dir+"."+exprString(n.Type)]; ok {
			for i, e := range n.Elts {
				if kv, ok := e.(*ast.KeyValueExpr); ok {
					if key, ok := kv.Key.(*ast.Ident); ok && key.Name == s.field {
						a.message(fn, kv.Value)
					}
				} else if i == s.index {
					a.message(fn, e)
				}
			}
		}
		for _, e := range n.Elts {
			if kv, ok := e.(*ast.KeyValueExpr); ok {
				if key, ok := kv.Key.(*ast.Ident); ok && messageName(key.Name) {
					a.message(fn, kv.Value)
				}
			}
		}
	case *ast.AssignStmt:
		a.assign(fn, n.Lhs, n.Rhs)
	case *ast.ValueSpec:
		lhs := make([]ast.Expr, len(n.Names))
		for i, id := range n.Names {
			lhs[i] = id
		}
		a.assign(fn, lhs, n.Values)
	case *ast.ReturnStmt:
		res := a.results[fn.key]
		for i, e := range n.Results {
			if res[i] {
				a.message(fn, e)
			}
			if lit, ok := e.(*ast.CompositeLit); ok && isStringSlice(lit.Type) {
				a.collect(lit.Elts)
			}
		}
		if len(n.Results) == 0 {
			for i, name := range named {
				if res[i] && name != "" {
					a.markVar(fn, name)
				}
			}
		}
	}
}

// assign: a value assigned to a message variable is a message; a call's
// result assigned to one makes that result a message.
func (a *flowAnalysis) assign(fn *flowFunc, lhs, rhs []ast.Expr) {
	target := func(e ast.Expr) bool {
		for {
			switch x := e.(type) {
			case *ast.Ident:
				return x.Name != "_" && a.isVar(fn, x.Name)
			case *ast.SelectorExpr:
				return messageName(x.Sel.Name)
			case *ast.IndexExpr:
				e = x.X
				continue
			case *ast.StarExpr:
				e = x.X
				continue
			}
			return false
		}
	}
	if len(rhs) == 1 && len(lhs) > 1 {
		if call, ok := rhs[0].(*ast.CallExpr); ok {
			for i, l := range lhs {
				if target(l) {
					for _, k := range a.funcKeys(fn.file, call) {
						a.mark(a.results, k, i)
					}
				}
			}
		}
		return
	}
	for i, l := range lhs {
		if i < len(rhs) && target(l) {
			a.message(fn, rhs[i])
		}
	}
}

// sinkArgs are the arguments of call that carry message text.
func (a *flowAnalysis) sinkArgs(fn *flowFunc, call *ast.CallExpr) []ast.Expr {
	args := call.Args
	switch x := call.Fun.(type) {
	case *ast.Ident:
		switch x.Name {
		case "panic":
			return args
		case "append":
			if len(args) >= 2 {
				t := args[0]
				for {
					if s, ok := t.(*ast.SelectorExpr); ok {
						if messageName(s.Sel.Name) {
							return args[1:]
						}
						break
					}
					if st, ok := t.(*ast.StarExpr); ok {
						t = st.X
						continue
					}
					if id, ok := t.(*ast.Ident); ok && a.isVar(fn, id.Name) {
						return args[1:]
					}
					break
				}
			}
			return nil
		}
	case *ast.SelectorExpr:
		switch x.Sel.Name {
		case "New", "Errorf", "Throw":
			if id, ok := x.X.(*ast.Ident); ok && (id.Name == "errors" || id.Name == "fmt" || id.Name == "jsexc" || id.Name == "bundle") {
				return args
			}
		case "Regex", "Refine":
			if len(args) > 0 {
				return args[len(args)-1:]
			}
		}
	}
	return pickArgs(args, a.lookup(a.helpers, a.funcKeys(fn.file, call)))
}

// transparent are the calls a message flows through unchanged.
var transparent = map[string]bool{
	"Concat": true, "Sprintf": true, "Join": true, "MakeString": true, "ToUTF8": true, "Trim": true, "TrimSpace": true,
	"jsStrings": true, "stringList": true, "stringArray": true, "str": true, "FromUTF16": true, "append": true,
}

// message follows a message expression: identifiers become message
// variables, calls make their result a message, literals are collected.
func (a *flowAnalysis) message(fn *flowFunc, e ast.Expr) {
	switch x := e.(type) {
	case *ast.BasicLit:
		a.collect([]ast.Expr{x})
	case *ast.Ident:
		if x.Name != "nil" && x.Name != "true" && x.Name != "false" {
			a.markVar(fn, x.Name)
		}
	case *ast.ParenExpr:
		a.message(fn, x.X)
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			a.message(fn, x.X)
			a.message(fn, x.Y)
		}
	case *ast.CompositeLit:
		if isStringSlice(x.Type) {
			for _, el := range x.Elts {
				a.message(fn, el)
			}
		}
	case *ast.CallExpr:
		name := ""
		switch f := x.Fun.(type) {
		case *ast.Ident:
			name = f.Name
		case *ast.SelectorExpr:
			name = f.Sel.Name
		}
		if transparent[name] {
			for _, arg := range x.Args {
				a.message(fn, arg)
			}
			return
		}
		for _, k := range a.funcKeys(fn.file, x) {
			a.mark(a.results, k, 0)
		}
	}
}

func (a *flowAnalysis) collect(exprs []ast.Expr) {
	var sites []errorSite
	collectLiterals(a.fset, exprs, &sites)
	for _, s := range sites {
		if _, ok := a.found[s.pos]; !ok {
			a.found[s.pos] = s
			a.changed = true
		}
	}
}

// sites are the collected sites under dir, in position order.
func (a *flowAnalysis) sites(dir string) []errorSite {
	abs, _ := filepath.Abs(dir)
	var out []errorSite
	for _, s := range a.found {
		if s.file == abs || strings.HasPrefix(s.file, abs+string(filepath.Separator)) {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(x, y errorSite) int {
		if c := strings.Compare(x.file, y.file); c != 0 {
			return c
		}
		return x.line - y.line
	})
	return out
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

// forEachFunc calls visit for every function declaration, method and local
// `name := func(...) {...}` of f (method is true for the last two).
func forEachFunc(f *ast.File, visit func(name string, typ *ast.FuncType, body *ast.BlockStmt, method bool)) {
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			if n.Body != nil {
				visit(n.Name.Name, n.Type, n.Body, n.Recv != nil)
			}
		case *ast.AssignStmt:
			if len(n.Lhs) == 1 && len(n.Rhs) == 1 {
				id, ok := n.Lhs[0].(*ast.Ident)
				lit, ok2 := n.Rhs[0].(*ast.FuncLit)
				if ok && ok2 {
					visit(id.Name, lit.Type, lit.Body, true)
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
				// An all-caps identifier (DUPLICATE_CHECKPOINT) is a
				// reason code beside the message, not its text.
				if len(messageFragments(s)) > 0 && !reasonCode.MatchString(s) {
					p := fset.Position(lit.Pos())
					*out = append(*out, errorSite{s, p.String(), p.Filename, p.Line})
				}
			}
			return true
		})
	}
}

var reasonCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

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
// (slice 1), the slice 2 writers and the slice 3 commands.
var gatedDirs = []string{
	"internal/bundle", "internal/clock", "internal/jsre", "internal/safedir", "internal/evidence",
	"internal/witnessresult", "internal/redaction", "internal/witness", "internal/ledgerbind",
	"internal/gitinv", "internal/sandbox", "internal/minimize", "internal/proofview", "internal/cli", "internal/projinit",
	"internal/safegit", "internal/hostcmd", "internal/runtimeimage", "internal/doctor", "internal/allowlist", "internal/turnsnap",
	"internal/incident",
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
	"internal/minimize/minimize.go":       "TestLiveMinimize",
	"internal/gitinv/investigate.go":      "TestLiveGitRefusals",
	"internal/sandbox/run.go":             "TestLiveNativeRunner",
	// The OpenSSL PEM and DER readers: their reason texts are compared on
	// thousands of generated key texts (round 8).
	"internal/witness/pemread.go":  "TestLivePEMKeyParsing",
	"internal/witness/asn1dec.go":  "TestLivePEMKeyParsing",
	"internal/witness/osslpem.go":  "TestLivePEMKeyParsing",
	"internal/witness/decoder.go":  "TestLivePEMKeyParsing",
	"internal/witness/keyalgs.go":  "TestLivePEMKeyParsing",
	"internal/witness/ecgroup.go":  "TestLivePEMKeyParsing",
	"internal/witness/nodekeys.go": "TestLivePEMKeyParsing",
	"internal/witness/pqkeys.go":   "TestLivePEMKeyParsing",
	"internal/witness/mlkem.go":    "TestLivePEMKeyParsing",
	"internal/witness/ecmul.go":    "TestLivePEMKeyParsing",
	"internal/witness/gf2m.go":     "TestLivePEMKeyParsing",
	// Slice 3a: the Docker image runner's scripted outcomes, the build-context
	// walk and the turn-snapshot planner and capture, each on generated inputs.
	"internal/runtimeimage/curated.go": "TestLiveCuratedRuntime",
	"internal/runtimeimage/context.go": "TestLiveProjectContext",
	"internal/runtimeimage/project.go": "TestLiveProjectPrepare",
	"internal/turnsnap/ignore.go":      "TestLiveTurnSnapshotIgnore",
	"internal/turnsnap/scan.go":        "TestLiveTurnSnapshotScan",
	"internal/turnsnap/plan.go":        "TestLiveTurnSnapshotCapture",
	"internal/turnsnap/capture.go":     "TestLiveTurnSnapshotCapture",
	// Slice 3b: the incident draft schemas, creation and store, on
	// generated inputs and store layouts.
	"internal/incident/schemas.go": "TestLiveIncidentDraft",
	"internal/incident/draft.go":   "TestLiveIncidentDraft",
	"internal/incident/store.go":   "TestLiveIncidentDraft",
	// Slice 3b: the incident intake runner, on scripted Git transcripts and
	// real repositories.
	"internal/incident/intake.go": "TestLiveIncidentSuggest",
	// Slice 3b: the prevention writer, the --from-bundle build and the
	// AGENTS.md upsert, on generated inputs.
	"internal/bundle/prevention/write.go":     "TestLivePreventionWrite",
	"internal/bundle/prevention/agentsmd.go":  "TestLiveAgentsMd",
	"internal/bundle/prevention/artifacts.go": "TestLivePreventionFromArtifacts",
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
	// The writer replay's per-task goldens (writer_test.go writerGoldenDir).
	perTask, err := filepath.Glob(filepath.Join(repo, "difftest", "testdata", "golden", writerGoldenDir, "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, perTask...)
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
