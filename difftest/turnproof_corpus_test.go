//go:build faultline_test

package difftest

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
)

// The hostile-verifier corpus (docs/post-submission/hostile-verifier-turn-parity-e40b.md)
// has 15 rows. Each is covered by these templates in mutations.json, replayed
// by TestGoldenReplay/turn-*; the goldens pin the exact text TS prints.
//
//	 1 missing manifest/hashes/ROOT       delete, rename-hashes, rename-manifest (generic)
//	 2 extra undeclared file              add-root-stray, add-nested-stray, turn-stray-declared
//	 3 traversal                          hashes-traversal, hashes-dotdot-escape, turn-hashes-nested-traversal
//	 4 absolute / Windows paths           hashes-absolute, hashes-backslash, hashes-nul, turn-hashes-drive-letter
//	 5 symlink / special file             symlink-to-hashes, symlink-outside, root-symlink, make-dir, turn-fifo
//	 6 duplicate JSON keys                turn-dup-key-last-valid, turn-dup-key-last-invalid, turn-dup-schema-version-git
//	 7 oversized hashes.txt               turn-hashes-at-read-limit, turn-hashes-one-over
//	 8 catalog entries over the cap       turn-catalog-at-2048, turn-catalog-over-2048, turn-catalog-2100,
//	                                      turn-files-at-limit, turn-files-one-over, turn-declared-total-one-over
//	 9 root mismatch                      root-mismatch (and the base's root-bad, root-malformed, root-empty)
//	10 run-id reuse / metadata mismatch   turn-run-id-reuse, turn-metadata-tree-swap, turn-run-tree-swap,
//	                                      turn-run-duplicate, turn-run-unsigned-edit
//	11 contradictory outcomes             turn-flip-verdict, turn-transition-kind, turn-proof-bit, turn-non-monotonic-flag
//	12 pack tampering                     flip-first/flip-last on the pack, turn-pack-wrong-trees, turn-pack-garbage,
//	                                      turn-git-fail-init, turn-git-fail-unpack, turn-git-fail-unpack-index,
//	                                      turn-git-fail-index-pack
//	13 downgrade / grade confusion        turn-grade-turn-proof, turn-grade-none, turn-label-legacy, turn-label-commit,
//	                                      turn-strip-grade, turn-schema-as-git
//	14 archive / compression bombs        N/A: the verifier takes a directory, never an archive, and git unpacks the
//	                                      pack with --strict under a 64 MiB output cap. turn-deep-literal-shallow
//	                                      (depth 500, the zod text at SiteTurnProof) and turn-deep-literal-overflow
//	                                      (depth 10000, "Maximum call stack size exceeded") stand in as the
//	                                      parser-bomb analogue; plan depth 3000 straddles the platform-dependent
//	                                      stack limit, so it was split in two.
//	15 malformed JSON                     truncate-json, bom-json, invalid-utf8-json, retype-first, drop-first,
//	                                      add-unknown-key, add-proto-key (generic)
//
// Also turn-specific: turn-ledger-hash, turn-ledger-snapshot-edit,
// turn-lifecycle-transport, turn-manifest-codex-thread.
//
// Reach of verifier branches the rows above did not run (coverage gate,
// position-keyed): turn-hash-line-dotdot-name ("Artifact path escapes its
// bundle", files.go safeArtifactPath), turn-manifest-frozen-digest (manifest
// frozen or witness digest vs the frozen witness artifact),
// turn-transition-artifact (transition artifact vs the investigation
// catalog, transitions/0000.json edited and rehashed) and
// turn-witness-command (frozen witness verification failure).
//
// Platform note (plan correction 11): on POSIX the drive-letter path of row 4
// is a safe relative path and reads as ENOENT; on Windows it is unsafe. The
// template is posixOnly.
var turnCorpusTemplates = []string{
	"turn-stray-declared", "turn-hashes-nested-traversal", "turn-hashes-drive-letter", "turn-fifo",
	"turn-dup-key-last-valid", "turn-dup-key-last-invalid", "turn-dup-schema-version-git",
	"turn-hashes-at-read-limit", "turn-hashes-one-over",
	"turn-catalog-at-2048", "turn-catalog-over-2048", "turn-catalog-2100",
	"turn-files-at-limit", "turn-files-one-over", "turn-declared-total-one-over",
	"turn-run-id-reuse", "turn-metadata-tree-swap", "turn-run-tree-swap", "turn-run-duplicate", "turn-run-unsigned-edit",
	"turn-flip-verdict", "turn-transition-kind", "turn-proof-bit", "turn-non-monotonic-flag",
	"turn-pack-wrong-trees", "turn-pack-garbage",
	"turn-git-fail-init", "turn-git-fail-unpack", "turn-git-fail-unpack-index", "turn-git-fail-index-pack",
	"turn-grade-turn-proof", "turn-grade-none", "turn-label-legacy", "turn-label-commit", "turn-strip-grade", "turn-schema-as-git",
	"turn-deep-literal-shallow", "turn-deep-literal-overflow",
	"turn-ledger-hash", "turn-ledger-snapshot-edit", "turn-lifecycle-transport", "turn-manifest-codex-thread",
	"turn-hash-line-dotdot-name", "turn-manifest-frozen-digest", "turn-transition-artifact", "turn-witness-command",
}

// TestTurnCorpusCoversEveryRow requires every turn template named above to
// exist in mutations.json with a turn- base, and every turn- template there
// to be named above (so the header stays the map of the corpus).
func TestTurnCorpusCoversEveryRow(t *testing.T) {
	repo := oracle.RepoRoot(t)
	have := map[string]bool{}
	for _, tpl := range loadTemplates(t, filepath.Join(repo, "difftest", "testdata", "mutations.json")) {
		id := tpl.str("id")
		bases := tpl.fields.Field("bases")
		turnBase := false
		for _, b := range bases.Items() {
			turnBase = turnBase || strings.HasPrefix(b.Str(), "turn-")
		}
		if strings.HasPrefix(id, "turn-") {
			have[id] = true
			if !turnBase {
				t.Errorf("template %s has no turn- base", id)
			}
		}
	}
	named := map[string]bool{}
	for _, id := range turnCorpusTemplates {
		named[id] = true
		if !have[id] {
			t.Errorf("corpus template %s is missing from mutations.json", id)
		}
	}
	for id := range have {
		if !named[id] {
			t.Errorf("mutations.json template %s is not in the corpus map", id)
		}
	}
}
