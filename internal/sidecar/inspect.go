package sidecar

import (
	"strconv"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsre"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/ledger"
	"github.com/Mizore66/faultline/internal/nodefs"
)

var (
	ledgerNamePattern  = jsre.MustCompile(`^codex-[a-f0-9]{64}\.json$`, "")
	receiptNamePattern = jsre.MustCompile(`^codex-stop-[a-f0-9]{64}\.json$`, "")
)

// RecordingShape names the three recording literals of codex-sidecar.ts
// :1036, :1068 and :1097, whose key orders differ.
type RecordingShape int

const (
	ShapeUnverified    RecordingShape = iota // {ledgerPath, valid, eventCount, headHash, errors}
	ShapeContradictory                       // receipt errors; sessionId, lastEvent?, latestCheckpoint?, lock?
	ShapeValid                               // sessionId, lastEvent?, latestCheckpoint?, latestStop?, lock?
)

// Recording is one entry of CodexSidecarInspection.recordings
// (codex-sidecar.ts:143).
type Recording struct {
	LedgerPath          string
	SessionID           string // "" is absent
	Valid               bool
	EventCount          int
	HeadHash            *string
	LastEventType       string // "" is absent (with LastEventOccurredAt)
	LastEventOccurredAt string
	LatestCheckpoint    jsjson.Value // Undefined is absent
	LatestStop          jsjson.Value // Undefined is absent
	Lock                jsjson.Value // Undefined is absent
	Errors              []string
	Shape               RecordingShape // which TS literal built it
}

// Inspection ports CodexSidecarInspection (codex-sidecar.ts:141).
type Inspection struct {
	StorageDirectory string
	Recordings       []Recording
}

func stringArray(items []string) jsjson.Value {
	values := make([]jsjson.Value, len(items))
	for i, item := range items {
		values[i] = jsjson.MakeString(item)
	}
	return jsjson.MakeArray(values)
}

// JSON is the recording in the key order of the TS literal named by Shape.
func (r Recording) JSON() jsjson.Value {
	o := jsjson.NewObj()
	o.Set("ledgerPath", jsjson.MakeString(r.LedgerPath))
	if r.Shape != ShapeUnverified {
		o.Set("sessionId", jsjson.MakeString(r.SessionID))
	}
	o.Set("valid", jsjson.MakeBool(r.Valid))
	o.Set("eventCount", jsjson.MakeNumber(float64(r.EventCount)))
	if r.HeadHash == nil {
		o.Set("headHash", jsjson.MakeNull())
	} else {
		o.Set("headHash", jsjson.MakeString(*r.HeadHash))
	}
	if r.Shape != ShapeUnverified {
		if r.LastEventType != "" {
			o.Set("lastEvent", jsjson.MustObject("type", jsjson.MakeString(r.LastEventType), "occurredAt", jsjson.MakeString(r.LastEventOccurredAt)))
		}
		if r.LatestCheckpoint.Kind() != jsjson.Undefined {
			o.Set("latestCheckpoint", r.LatestCheckpoint)
		}
		if r.Shape == ShapeValid && r.LatestStop.Kind() != jsjson.Undefined {
			o.Set("latestStop", r.LatestStop)
		}
		if r.Lock.Kind() != jsjson.Undefined {
			o.Set("lock", r.Lock)
		}
	}
	o.Set("errors", stringArray(r.Errors))
	return jsjson.MakeObject(o)
}

// JSON is {storageDirectory, recordings}.
func (i Inspection) JSON() jsjson.Value {
	recordings := make([]jsjson.Value, len(i.Recordings))
	for n, r := range i.Recordings {
		recordings[n] = r.JSON()
	}
	return jsjson.MustObject("storageDirectory", jsjson.MakeString(i.StorageDirectory), "recordings", jsjson.MakeArray(recordings))
}

// stopReceipts is stopReceiptsForSession's result: a Map by turnId (the
// last receipt wins, in the order of the first insertion) and the errors.
type stopReceipts struct {
	order    []string
	receipts map[string]jsjson.Value
	errors   []string
}

// stopReceiptsForSession ports codex-sidecar.ts:992.
func stopReceiptsForSession(directory, sessionID string) stopReceipts {
	scan := stopReceipts{receipts: map[string]jsjson.Value{}, errors: []string{}}
	if !safeDirectoryExists(directory, "FaultLine sidecar recordings") {
		return scan // coverage gate: race only (Inspect accepted the directory just before)
	}
	for _, entry := range jsexc.Must(nodefs.ReadDirents(directory)) {
		if !receiptNamePattern.Test(entry.Name) {
			continue
		}
		var receipt jsjson.Value
		if err := jsexc.Try(func() { receipt = readStopReceipt(nodefs.Join(directory, entry.Name)) }); err != nil {
			scan.errors = append(scan.errors, jsexc.Concat("Invalid sidecar stop receipt ", entry.Name, ": ", jsexc.Message(err)))
			continue
		}
		if receipt.Kind() != jsjson.Undefined && receipt.Get("sessionId").Str() == sessionID {
			turnID := receipt.Get("turnId").Str()
			if _, ok := scan.receipts[turnID]; !ok {
				scan.order = append(scan.order, turnID)
			}
			scan.receipts[turnID] = receipt
		}
	}
	return scan
}

// latestCheckpointOf is the last WORKTREE_CHECKPOINT's summary, Undefined
// when there is none.
func latestCheckpointOf(l jsjson.Value) jsjson.Value {
	all := events(l)
	for i := len(all) - 1; i >= 0; i-- {
		e := all[i]
		if e.Get("event", "type").Str() != "WORKTREE_CHECKPOINT" {
			continue
		}
		checkpoint := e.Get("event", "payload", "checkpoint")
		return jsjson.MustObject(
			"digest", checkpoint.Get("digest"),
			"capturedAt", checkpoint.Get("capturedAt"),
			"headCommit", checkpoint.Get("headCommit"),
			"treeDigest", checkpoint.Get("treeDigest"),
			"afterTurnOrdinal", e.Get("event", "payload", "afterTurnOrdinal"),
		)
	}
	return jsjson.Value{}
}

// lastEventOfType is [...events].reverse().find(type and pred).
func lastEventOfType(l jsjson.Value, eventType string, pred func(jsjson.Value) bool) jsjson.Value {
	all := events(l)
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Get("event", "type").Str() == eventType && (pred == nil || pred(all[i])) {
			return all[i]
		}
	}
	return jsjson.Value{}
}

// inspectRecording is the recordings.map callback of codex-sidecar.ts:1032.
func inspectRecording(directory, ledgerPath string) Recording {
	verification := ledger.VerifyFile(ledgerPath)
	if !verification.Valid {
		return Recording{LedgerPath: ledgerPath, Valid: false, EventCount: verification.EventCount,
			HeadHash: verification.HeadHash, Errors: verification.Errors, Shape: ShapeUnverified}
	}
	l := readSidecarLedger(ledgerPath)
	r := Recording{LedgerPath: ledgerPath, SessionID: l.Get("sessionId").Str(), EventCount: verification.EventCount, HeadHash: verification.HeadHash}
	if all := events(l); len(all) > 0 {
		last := all[len(all)-1]
		r.LastEventType, r.LastEventOccurredAt = last.Get("event", "type").Str(), last.Get("occurredAt").Str()
	}
	r.Lock = sidecarLockHealth(ledgerPath)
	r.LatestCheckpoint = latestCheckpointOf(l)
	lastCompletion := lastEventOfType(l, "TURN_COMPLETED", nil)
	scan := stopReceiptsForSession(directory, r.SessionID)
	receiptErrors := append([]string{}, scan.errors...)
	for _, turnID := range scan.order {
		receipt := scan.receipts[turnID]
		if err := jsexc.Try(func() {
			assertReceiptMatchesLedger(receipt, l, receipt.Get("sessionId").Str(), receipt.Get("turnId").Str())
		}); err != nil {
			receiptErrors = append(receiptErrors, jsexc.Concat("Contradictory sidecar stop receipt for ", receipt.Get("turnId").Str(), ": ", jsexc.Message(err)))
		}
	}
	if len(receiptErrors) > 0 {
		r.Valid, r.Errors, r.Shape = false, receiptErrors, ShapeContradictory
		return r
	}
	if lastCompletion.Kind() != jsjson.Undefined {
		turnID := lastCompletion.Get("event", "payload", "turnId").Str()
		turnOrdinal := lastCompletion.Get("event", "payload", "turnOrdinal").Num()
		receipt, ok := scan.receipts[turnID]
		switch {
		case !ok && lastEventOfType(l, "WORKTREE_CHECKPOINT", func(e jsjson.Value) bool {
			return e.Get("event", "payload", "afterTurnOrdinal").Num() == turnOrdinal
		}).Kind() == jsjson.Undefined:
			r.LatestStop = jsjson.MustObject("turnId", jsjson.MakeString(turnID), "status", jsjson.MakeString("CHECKPOINT_PENDING_RECOVERY"), "receipt", jsjson.MakeString("MISSING"))
		case !ok:
			r.LatestStop = jsjson.MustObject("turnId", jsjson.MakeString(turnID), "status", jsjson.MakeString("CHECKPOINT_RECORDED"), "receipt", jsjson.MakeString("RECOVERABLE_FROM_LEDGER"))
		default:
			o := jsjson.NewObj()
			o.Set("turnId", jsjson.MakeString(turnID))
			o.Set("status", receipt.Get("status"))
			if reason := receipt.Get("reason"); reason.Kind() != jsjson.Undefined {
				o.Set("reason", reason)
			}
			o.Set("receipt", jsjson.MakeString("DURABLE"))
			r.LatestStop = jsjson.MakeObject(o)
		}
	}
	r.Valid, r.Errors, r.Shape = true, []string{}, ShapeValid
	return r
}

// Inspect ports inspectObservedCodexSidecar (codex-sidecar.ts:1016);
// sessionID "" is undefined. It reads local recorder health without
// exposing prompts, assistant messages or transcript paths.
func Inspect(cwd, sessionID string) (inspection Inspection, err error) {
	defer jsexc.Catch(func(e error) { inspection, err = Inspection{}, e })
	if sessionID != "" {
		bundle.Must(bundle.ParseValue(jsjson.MakeString(sessionID), IdentifierSchema))
	}
	directory := recordingDirectory(nodefs.Resolve(cwd), false)
	if !safeDirectoryExists(directory, "FaultLine sidecar recordings") {
		return Inspection{StorageDirectory: directory, Recordings: []Recording{}}, nil
	}
	var ledgerPaths []string
	if sessionID == "" {
		var names []string
		for _, entry := range jsexc.Must(nodefs.ReadDirents(directory)) {
			if ledgerNamePattern.Test(entry.Name) {
				names = append(names, entry.Name)
			}
		}
		var paths []string
		for _, name := range names {
			if path := nodefs.Join(directory, name); safeRegularFileExists(path, "FaultLine sidecar ledger") {
				paths = append(paths, path)
			}
		}
		ledgerPaths = canonical.SortLocale(paths)
	} else if requested := nodefs.Join(directory, ledgerFileName(sessionID)); safeRegularFileExists(requested, "FaultLine sidecar ledger") {
		ledgerPaths = []string{requested}
	}
	recordings := make([]Recording, 0, len(ledgerPaths))
	for _, ledgerPath := range ledgerPaths {
		recordings = append(recordings, inspectRecording(directory, ledgerPath))
	}
	return Inspection{StorageDirectory: directory, Recordings: recordings}, nil
}

// Latest ports ResolvedLatestSidecarLedger (codex-sidecar.ts:1113).
type Latest struct {
	LedgerPath          string
	SessionID           string // "" is undefined
	ValidCandidateCount int
	LastEventOccurredAt string // "" is undefined
}

// JSON is {ledgerPath, sessionId?, validCandidateCount, lastEventOccurredAt?}.
func (l Latest) JSON() jsjson.Value {
	o := jsjson.NewObj()
	o.Set("ledgerPath", jsjson.MakeString(l.LedgerPath))
	if l.SessionID != "" {
		o.Set("sessionId", jsjson.MakeString(l.SessionID))
	}
	o.Set("validCandidateCount", jsjson.MakeNumber(float64(l.ValidCandidateCount)))
	if l.LastEventOccurredAt != "" {
		o.Set("lastEventOccurredAt", jsjson.MakeString(l.LastEventOccurredAt))
	}
	return jsjson.MakeObject(o)
}

func ledgerPathsOf(recordings []Recording) []string {
	paths := make([]string, len(recordings))
	for i, r := range recordings {
		paths[i] = r.LedgerPath
	}
	return paths
}

// ResolveLatestLedgerPath ports resolveLatestSidecarLedgerPath
// (codex-sidecar.ts:1124): the single newest valid sidecar ledger, failing
// closed when there is none or the newest is ambiguous.
func ResolveLatestLedgerPath(cwd string) (latest Latest, err error) {
	defer jsexc.Catch(func(e error) { latest, err = Latest{}, e })
	inspection := jsexc.Must(Inspect(nodefs.Resolve(cwd), ""))
	var valid []Recording
	for _, r := range inspection.Recordings {
		if r.Valid {
			valid = append(valid, r)
		}
	}
	if len(valid) == 0 {
		fail("No valid Codex sidecar ledger found under Git metadata recordings. " +
			"Run a Codex session with trusted FaultLine hooks, or pass --ledger <path> explicitly.")
	}
	if len(valid) == 1 {
		only := valid[0]
		return Latest{LedgerPath: only.LedgerPath, SessionID: only.SessionID, ValidCandidateCount: 1, LastEventOccurredAt: only.LastEventOccurredAt}, nil
	}
	var withTime []Recording
	for _, r := range valid {
		if r.LastEventType != "" {
			withTime = append(withTime, r)
		}
	}
	if len(withTime) == 0 {
		fail(jsexc.Concat("Ambiguous latest sidecar ledger: ", strconv.Itoa(len(valid)), " valid recordings without comparable last-event timestamps. ",
			"Pass --ledger explicitly. Candidates: ", jsexc.Join(ledgerPathsOf(valid), "; ")))
	}
	latestTime := withTime[0].LastEventOccurredAt
	for _, r := range withTime {
		if jsstr.CompareUTF16(r.LastEventOccurredAt, latestTime) > 0 {
			latestTime = r.LastEventOccurredAt
		}
	}
	var tops []Recording
	for _, r := range withTime {
		if r.LastEventOccurredAt == latestTime {
			tops = append(tops, r)
		}
	}
	if len(tops) != 1 {
		fail(jsexc.Concat("Ambiguous latest sidecar ledger: ", strconv.Itoa(len(tops)), " valid recordings share last-event time ", latestTime, ". ",
			"Pass --ledger explicitly. Candidates: ", jsexc.Join(ledgerPathsOf(tops), "; ")))
	}
	chosen := tops[0]
	return Latest{LedgerPath: chosen.LedgerPath, SessionID: chosen.SessionID, ValidCandidateCount: len(valid), LastEventOccurredAt: chosen.LastEventOccurredAt}, nil
}
