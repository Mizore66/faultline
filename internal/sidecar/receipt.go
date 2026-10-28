package sidecar

import (
	"os"
	"strconv"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	s "github.com/Mizore66/faultline/internal/schema"
)

// stopReceiptVersion is StopReceipt's schemaVersion (codex-sidecar.ts:174).
const stopReceiptVersion = "faultline.codex-sidecar-stop-receipt.v1"

// stopReceiptSchema ports StopReceiptSchema (codex-sidecar.ts:184).
var stopReceiptSchema = s.Object(
	s.F("schemaVersion", s.LiteralString(stopReceiptVersion)),
	s.F("sessionId", IdentifierSchema),
	s.F("turnId", IdentifierSchema),
	s.F("status", s.Enum("CHECKPOINT_RECORDED", "CHECKPOINT_SKIPPED_DIRTY", "CHECKPOINT_SKIPPED_UNAVAILABLE", "TURN_SNAPSHOT_RECORDED")),
	s.F("checkpointDigest", s.Optional(s.String(s.Regex(s.Pattern(`^sha256:[a-f0-9]{64}$`), "")))),
	s.F("reason", s.Optional(s.Enum("DIRTY_WORKTREE", "CHECKPOINT_UNAVAILABLE"))),
	s.F("turnSnapshotDigest", s.Optional(s.String(s.Regex(s.Pattern(`^sha256:[a-f0-9]{64}$`), "")))),
).Strict()

// stopReceiptPath ports codex-sidecar.ts:339.
func stopReceiptPath(directory, sessionID, turnID string) string {
	key := canonical.SHA256Hex(jsexc.Concat(sessionID, "\x00", turnID))
	return nodefs.Join(directory, jsexc.Concat("codex-stop-", key, ".json"))
}

// receiptStagePath is writeJsonAtomic's stage (codex-sidecar.ts:529):
// ".<sha256(path)[0:24]>.<pid>.tmp" beside path.
func receiptStagePath(path string) string {
	return nodefs.Join(nodefs.Dirname(path), jsexc.Concat(".", canonical.SHA256Hex(path)[:24], ".", strconv.Itoa(os.Getpid()), ".tmp"))
}

// writeJSONAtomic ports writeJsonAtomic (codex-sidecar.ts:525): an
// exclusive 0600 stage (no fsync) renamed onto path; in `finally` the stage
// is removed (rmSync force) when it still exists, and a failure there
// replaces the result.
func writeJSONAtomic(path string, value jsjson.Value) {
	directory := nodefs.Dirname(path)
	assertSafeDirectory(directory, "FaultLine sidecar recordings")
	safeRegularFileExists(path, "FaultLine sidecar receipt")
	staged := receiptStagePath(path)
	err := nodefs.WriteFileUTF8(staged, jsexc.Concat(jsjson.StringifyIndent(value, "  "), "\n"), 0o600, true)
	if err == nil {
		err = nodefs.Rename(staged, path)
	}
	if nodefs.Exists(staged) {
		if rmErr := gitinv.RemoveForce(staged); rmErr != nil {
			err = rmErr
		}
	}
	if err != nil {
		jsexc.Throw(err)
	}
}

// readStopReceipt ports codex-sidecar.ts:538: Undefined when absent.
func readStopReceipt(path string) jsjson.Value {
	if !safeRegularFileExists(path, "FaultLine sidecar receipt") {
		return jsjson.Value{}
	}
	var receipt jsjson.Value
	if err := jsexc.Try(func() {
		receipt = bundle.Must(bundle.ParseValue(bundle.Must(bundle.ParseJSONFile(path)), stopReceiptSchema))
	}); err != nil {
		fail(jsexc.Concat("Unable to read the sidecar stop receipt: ", jsexc.Message(err)))
	}
	return receipt
}

// resultFromReceipt ports codex-sidecar.ts:593.
func resultFromReceipt(receipt, ledger jsjson.Value, ledgerPath string, in hookInput) Result {
	var turnSnapshot jsjson.Value
	if receipt.Get("turnSnapshotDigest").Kind() != jsjson.Undefined {
		turnSnapshot = turnSnapshotForTurn(ledger, in)
	}
	status := receipt.Get("status").Str()
	sessionID, turnID := receipt.Get("sessionId").Str(), receipt.Get("turnId").Str()
	switch status {
	case "CHECKPOINT_RECORDED":
		digest := receipt.Get("checkpointDigest")
		if digest.Kind() != jsjson.String || digest.Str() == "" {
			fail("The sidecar stop receipt omitted its checkpoint digest.")
		}
		return Result{Status: status, LedgerPath: ledgerPath, SessionID: sessionID, TurnID: turnID, Idempotent: true,
			CheckpointDigest: digest.Str(), TurnSnapshot: turnSnapshot}
	case "TURN_SNAPSHOT_RECORDED":
		if turnSnapshot.Kind() == jsjson.Undefined {
			fail("The sidecar stop receipt claims a turn tree snapshot that is absent from the ledger.")
		}
		return turnSnapshotOnlyResult(ledgerPath, sessionID, turnID, true, turnSnapshot)
	}
	reason := receipt.Get("reason")
	r := Result{Status: status, LedgerPath: ledgerPath, SessionID: sessionID, TurnID: turnID, Idempotent: true, TurnSnapshot: turnSnapshot}
	switch {
	case reason.Kind() == jsjson.String:
		r.Reason = reason.Str()
	case status == "CHECKPOINT_SKIPPED_DIRTY":
		r.Reason = "DIRTY_WORKTREE"
	default:
		r.Reason = "CHECKPOINT_UNAVAILABLE"
	}
	return r
}

// assertReceiptMatchesLedger ports codex-sidecar.ts:623.
func assertReceiptMatchesLedger(receipt, ledger jsjson.Value, sessionID, turnID string) {
	turn := &turnID
	turnOrdinal := turnOrdinalForStartedTurn(ledger, turnID)
	completion := eventByID(ledger, EventID(sessionID, turn, "turn-stop"))
	if completion.Kind() == jsjson.Undefined || !bundle.SameCanonical(completion.Get("event"), turnCompletedInput(turnID, turnOrdinal)) {
		fail("The sidecar stop receipt has no matching completed-turn ledger event.")
	}
	snapshotEvent := eventByID(ledger, EventID(sessionID, turn, "turn-snapshot"))
	if receiptDigest := receipt.Get("turnSnapshotDigest"); receiptDigest.Kind() == jsjson.Undefined {
		if snapshotEvent.Kind() != jsjson.Undefined {
			fail("A sidecar stop receipt without a turn tree snapshot contradicts a recorded one.")
		}
	} else {
		if snapshotEvent.Kind() == jsjson.Undefined || snapshotEvent.Get("event", "type").Str() != "TURN_TREE_SNAPSHOT" ||
			snapshotEvent.Get("event", "payload", "turnOrdinal").Num() != turnOrdinal {
			fail("The sidecar stop receipt has no matching turn tree snapshot ledger event.")
		}
		if receiptDigest.Str() != snapshotEvent.Get("event", "payload", "snapshot", "digest").Str() {
			fail("The sidecar stop receipt turn tree snapshot digest does not match the ledger.")
		}
	}
	checkpoint := eventByID(ledger, EventID(sessionID, turn, "checkpoint"))
	if receipt.Get("status").Str() != "CHECKPOINT_RECORDED" {
		if checkpoint.Kind() != jsjson.Undefined {
			fail("A skipped sidecar stop receipt contradicts a recorded checkpoint.")
		}
		return
	}
	if checkpoint.Kind() == jsjson.Undefined || checkpoint.Get("event", "type").Str() != "WORKTREE_CHECKPOINT" ||
		checkpoint.Get("event", "payload", "afterTurnOrdinal").Num() != turnOrdinal {
		fail("The sidecar stop receipt has no matching checkpoint ledger event.")
	}
	if digest := receipt.Get("checkpointDigest"); digest.Kind() != jsjson.String || digest.Str() != checkpoint.Get("event", "payload", "checkpoint", "digest").Str() {
		fail("The sidecar stop receipt checkpoint digest does not match the ledger.")
	}
}

// writeReceipt ports codex-sidecar.ts:707.
func writeReceipt(directory string, in hookInput, result Result) {
	turnSnapshotDigest := result.TurnSnapshot.Get("digest")
	receipt := jsjson.NewObj()
	receipt.Set("schemaVersion", jsjson.MakeString(stopReceiptVersion))
	receipt.Set("sessionId", jsjson.MakeString(in.sessionID))
	receipt.Set("turnId", jsjson.MakeString(in.turnID))
	receipt.Set("status", jsjson.MakeString(result.Status))
	switch result.Status {
	case "CHECKPOINT_RECORDED":
		if result.CheckpointDigest == "" {
			fail("A checkpoint receipt requires a checkpoint digest.")
		}
		receipt.Set("checkpointDigest", jsjson.MakeString(result.CheckpointDigest))
		if turnSnapshotDigest.Kind() != jsjson.Undefined {
			receipt.Set("turnSnapshotDigest", turnSnapshotDigest)
		}
	case "CHECKPOINT_SKIPPED_DIRTY", "CHECKPOINT_SKIPPED_UNAVAILABLE":
		reason := "CHECKPOINT_UNAVAILABLE"
		if result.Status == "CHECKPOINT_SKIPPED_DIRTY" {
			reason = "DIRTY_WORKTREE"
		}
		receipt.Set("reason", jsjson.MakeString(reason))
		if turnSnapshotDigest.Kind() != jsjson.Undefined {
			receipt.Set("turnSnapshotDigest", turnSnapshotDigest)
		}
	case "TURN_SNAPSHOT_RECORDED":
		if turnSnapshotDigest.Kind() != jsjson.String || turnSnapshotDigest.Str() == "" {
			fail("A turn snapshot receipt requires a turn tree snapshot digest.")
		}
		receipt.Set("turnSnapshotDigest", turnSnapshotDigest)
	default:
		fail("Only Stop checkpoint outcomes can be persisted as a sidecar receipt.")
	}
	parsed := bundle.Must(bundle.ParseValue(jsjson.MakeObject(receipt), stopReceiptSchema))
	writeJSONAtomic(stopReceiptPath(directory, in.sessionID, in.turnID), parsed)
}
