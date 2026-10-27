// Package ledger ports the lifecycle ledger writers of src/ledger.ts:
// creating a ledger, appending signed events, the atomic file write, the
// locked read-verify-append-write transaction and the clean Git
// checkpoint. The verifier itself is internal/bundle/gitproof's.
package ledger // Port of src/ledger.ts writers; the verifier is gitproof.

import (
	"os"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// IntegrityError ports LedgerIntegrityError (src/ledger.ts:215). Its
// Message is the TS message; callers test for it with errors.As.
type IntegrityError struct{ Message string }

func (e *IntegrityError) Error() string { return e.Message }

// CreateOptions ports CreateLedgerOptions (src/ledger.ts:190). "" is
// undefined for LedgerID and CreatedAt.
type CreateOptions struct{ SessionID, LedgerID, CreatedAt string }

// Create ports createCodexLifecycleLedger (src/ledger.ts:318): reads
// clock.NewUUID() only when LedgerID is "", then clock.Now() only when
// CreatedAt is "", in that order, and parses with
// gitproof.CodexLifecycleLedgerSchema (a ZodError on failure).
func Create(o CreateOptions) (ledger jsjson.Value, err error) {
	defer jsexc.Catch(func(e error) { ledger, err = jsjson.Value{}, e })
	ledgerID := o.LedgerID
	if ledgerID == "" {
		ledgerID = "ledger-" + clock.NewUUID()
	}
	createdAt := o.CreatedAt
	if createdAt == "" {
		createdAt = clock.ISO(clock.Now())
	}
	return bundle.ParseValue(jsjson.MustObject(
		"schemaVersion", jsjson.MakeString(gitproof.CodexLifecycleLedgerVersion),
		"ledgerId", jsjson.MakeString(ledgerID),
		"sessionId", jsjson.MakeString(o.SessionID),
		"createdAt", jsjson.MakeString(createdAt),
		"events", jsjson.MakeArray([]jsjson.Value{}),
	), gitproof.CodexLifecycleLedgerSchema)
}

// ensureAppendableLedger ports src/ledger.ts:332: the schema parse (a
// ZodError propagates), then the integrity check.
func ensureAppendableLedger(ledger jsjson.Value) jsjson.Value {
	parsed := bundle.Must(bundle.ParseValue(ledger, gitproof.CodexLifecycleLedgerSchema))
	if verification := gitproof.VerifyCodexLifecycleLedger(parsed); !verification.Valid {
		jsexc.Throw(&IntegrityError{Message: jsexc.Concat("Refusing to append to an invalid ledger: ", gitproof.ErrorSummary(verification.Errors))})
	}
	return parsed
}

// AppendOptions ports AppendLifecycleEventOptions (src/ledger.ts:196).
// "" is undefined, unless the matching Has flag is set: TS uses `??`, so an
// explicit "" (fl record stdin passes any string eventId or occurredAt from
// the envelope) is kept and then fails its schema.
type AppendOptions struct {
	EventID, OccurredAt       string
	HasEventID, HasOccurredAt bool
}

// Append ports appendLifecycleEvent (src/ledger.ts:345): it returns the new
// ledger value and never mutates its input.
func Append(ledger, input jsjson.Value, o AppendOptions) (next jsjson.Value, err error) {
	defer jsexc.Catch(func(e error) { next, err = jsjson.Value{}, e })
	current := ensureAppendableLedger(ledger)
	event := bundle.Must(bundle.ParseValue(input, gitproof.LifecycleEventInputSchema))
	occurredAt := o.OccurredAt
	if occurredAt == "" && !o.HasOccurredAt {
		occurredAt = clock.ISO(clock.Now())
	}
	bundle.Must(bundle.ParseValue(jsjson.MakeString(occurredAt), gitproof.CanonicalTimestampSchema))
	events := current.Get("events").Items()
	previousHash := gitproof.LedgerGenesisHash(current)
	if len(events) > 0 {
		previousHash = events[len(events)-1].Get("hash").Str()
	}
	eventID := o.EventID
	if eventID == "" && !o.HasEventID {
		eventID = "event-" + clock.NewUUID()
	}
	unsigned := jsjson.NewObj()
	unsigned.Set("schemaVersion", jsjson.MakeString(gitproof.CodexLifecycleLedgerVersion))
	unsigned.Set("ledgerId", current.Get("ledgerId"))
	unsigned.Set("sessionId", current.Get("sessionId"))
	unsigned.Set("sequence", jsjson.MakeNumber(float64(len(events)+1)))
	unsigned.Set("eventId", jsjson.MakeString(eventID))
	unsigned.Set("occurredAt", jsjson.MakeString(occurredAt))
	unsigned.Set("event", event)
	unsigned.Set("previousHash", jsjson.MakeString(previousHash))
	hash := gitproof.HashLifecycleEvent(jsjson.MakeObject(unsigned))
	// {...unsigned, hash}
	signed := jsjson.NewObj()
	for _, k := range unsigned.Keys() {
		signed.Set(k, unsigned.Field(k))
	}
	signed.Set("hash", jsjson.MakeString(hash))
	// {...current, events: [...current.events, signed]}
	spread := jsjson.NewObj()
	for _, k := range current.Obj().Keys() {
		spread.Set(k, current.Obj().Field(k))
	}
	nextEvents := make([]jsjson.Value, 0, len(events)+1)
	nextEvents = append(append(nextEvents, events...), jsjson.MakeObject(signed))
	spread.Set("events", jsjson.MakeArray(nextEvents))
	parsed := bundle.Must(bundle.ParseValue(jsjson.MakeObject(spread), gitproof.CodexLifecycleLedgerSchema))
	if verification := gitproof.VerifyCodexLifecycleLedger(parsed); !verification.Valid {
		return jsjson.Value{}, &IntegrityError{Message: jsexc.Concat("Refusing an invalid lifecycle transition: ", gitproof.ErrorSummary(verification.Errors))}
	}
	return parsed, nil
}

// WriteAtomic ports writeCodexLifecycleLedgerAtomic (src/ledger.ts:632):
// ensureAppendableLedger, then nodefs.AtomicWriteFile with
// JSON.stringify(parsed, null, 2) + "\n". It returns resolve(filePath).
func WriteAtomic(filePath string, ledger jsjson.Value) (written string, err error) {
	defer jsexc.Catch(func(e error) { written, err = "", e })
	parsed := ensureAppendableLedger(ledger)
	return nodefs.AtomicWriteFile(filePath, jsexc.Concat(jsjson.StringifyIndent(parsed, "  "), "\n"))
}

// acquireLedgerLock ports src/ledger.ts:676: openSync(lockPath, "wx", 0o600).
func acquireLedgerLock(lockPath string) (*os.File, error) {
	lock, err := nodefs.OpenExclusive(lockPath, 0o600)
	if err != nil {
		return nil, &IntegrityError{Message: jsexc.Concat("Could not acquire lifecycle ledger append lock ", lockPath, ": ", jsexc.Message(err))}
	}
	return lock, nil
}

// AppendAtomic ports appendLifecycleEventAtomic (src/ledger.ts:689): under
// an exclusive "<ledger>.lock" (no wait, no retry), read and verify the
// ledger, append, and write it back atomically. In `finally` a close error
// replaces the result (and skips the unlink); an unlink error is ignored.
func AppendAtomic(filePath string, input jsjson.Value, o AppendOptions) (next jsjson.Value, err error) {
	defer jsexc.Catch(func(e error) { next, err = jsjson.Value{}, e })
	absolutePath := nodefs.Resolve(filePath)
	lockPath := jsexc.Concat(absolutePath, ".lock")
	if err := nodefs.MkdirAll(nodefs.Dirname(absolutePath), 0o777); err != nil {
		return jsjson.Value{}, err
	}
	lock, err := acquireLedgerLock(lockPath)
	if err != nil {
		return jsjson.Value{}, err
	}
	err = jsexc.Try(func() {
		ledger := bundle.Must(ReadVerified(absolutePath))
		next = bundle.Must(Append(ledger, input, o))
		bundle.Must(WriteAtomic(absolutePath, next))
	})
	if closeErr := nodefs.Close(lock); closeErr != nil {
		return jsjson.Value{}, closeErr
	}
	_ = nodefs.Unlink(lockPath) // A successful append is still durable if cleanup races with a caller.
	if err != nil {
		return jsjson.Value{}, err
	}
	return next, nil
}

// Verify is gitproof.VerifyCodexLifecycleLedger (src/ledger.ts:538).
func Verify(value jsjson.Value) gitproof.LedgerVerification {
	return gitproof.VerifyCodexLifecycleLedger(value)
}

// VerifyFile is gitproof.VerifyCodexLifecycleLedgerFile (src/ledger.ts:663).
func VerifyFile(filePath string) gitproof.LedgerVerification {
	return gitproof.VerifyCodexLifecycleLedgerFile(filePath)
}

// ReadVerified is gitproof.ReadVerifiedCodexLifecycleLedger
// (src/ledger.ts:653). Each of its failures is a LedgerIntegrityError in TS
// (src/ledger.ts:643, 647 and 656), so it is an *IntegrityError here.
func ReadVerified(filePath string) (ledger jsjson.Value, err error) {
	defer jsexc.Catch(func(e error) { ledger, err = jsjson.Value{}, e })
	ledger, err = gitproof.ReadVerifiedCodexLifecycleLedger(filePath)
	if err != nil {
		return jsjson.Value{}, &IntegrityError{Message: err.Error()}
	}
	return ledger, nil
}
