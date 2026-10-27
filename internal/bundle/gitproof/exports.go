package gitproof

import (
	"math"

	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// The src/ledger.ts constants and schemas the internal/ledger writers use;
// the unexported names stay for this package's verifier.
const (
	CodexLifecycleLedgerVersion = codexLifecycleLedgerVersion // "faultline.codex-lifecycle-ledger.v1"
	GitCheckpointVersion        = gitCheckpointVersion        // "faultline.git-checkpoint.v1"
)

var (
	LedgerIdentifierSchema            = ledgerIdentifierSchema            // src/ledger.ts:31
	CodexTransportSchema              = codexTransportSchema              // src/ledger.ts:49
	LifecycleEventInputSchema         = lifecycleEventInputSchema         // src/ledger.ts:143
	UnsignedCodexLifecycleEventSchema = unsignedCodexLifecycleEventSchema // src/ledger.ts:155
	UnsignedGitCheckpointSchema       = unsignedGitCheckpointSchema       // src/ledger.ts:86
	GitCheckpointSchema               = gitCheckpointSchema               // src/ledger.ts:95
)

// CodexTransportOptions is CodexTransportSchema.options, in order.
var CodexTransportOptions = []string{"CODEX_CLI", "CODEX_APP", "SIDE_CAR", "OBSERVED_EXTERNAL_TRANSPORT"}

// LedgerGenesisHash ports ledgerGenesisHash (src/ledger.ts:223).
func LedgerGenesisHash(ledger jsjson.Value) string { return ledgerGenesisHash(ledger) }

// HashLifecycleEvent ports hashLifecycleEvent (src/ledger.ts:234): the
// event is parsed with UnsignedCodexLifecycleEventSchema first, and a
// failure throws its ZodError (jsexc), whose message the catcher reads.
func HashLifecycleEvent(event jsjson.Value) string { return hashLifecycleEvent(event) }

// VerifyGitCheckpoint ports verifyGitCheckpoint (src/ledger.ts:243).
func VerifyGitCheckpoint(value jsjson.Value) []string { return verifyGitCheckpoint(value) }

// TimestampMs is Date.parse for a schema-validated canonical timestamp
// (CanonicalTimestamp holds). It returns NaN for any other string, which
// Date.parse does not: Date.parse("2026-07-16T11:00:00Z") is a number. So
// it is no port of Date.parse on unvalidated input.
func TimestampMs(iso string) float64 {
	if !canonicalTimestamp(iso) {
		return math.NaN()
	}
	return float64(timestampMs(iso))
}

// ErrorSummary is src/ledger.ts:328 errorSummary.
func ErrorSummary(errs []string) string { return errorSummary(errs) }

// VerifyCodexLifecycleLedgerFile ports src/ledger.ts:663: a read or schema
// failure becomes {valid:false, errors:[message], eventCount:0, headHash:null}.
func VerifyCodexLifecycleLedgerFile(filePath string) LedgerVerification {
	var result LedgerVerification
	if err := jsexc.Try(func() {
		ledger, err := ReadCodexLifecycleLedger(filePath)
		if err != nil {
			jsexc.Throw(err)
		}
		result = VerifyCodexLifecycleLedger(ledger)
	}); err != nil {
		return LedgerVerification{Errors: []string{jsexc.Message(err)}}
	}
	return result
}
