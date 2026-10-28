package sidecar

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func keys(v jsjson.Value) string { return strings.Join(v.Obj().Keys(), ",") }

func TestRecordingShapeKeyOrders(t *testing.T) {
	head := "h"
	cp := jsjson.MustObject("digest", jsjson.MakeString("d"))
	stopV := jsjson.MustObject("turnId", jsjson.MakeString("t"))
	lock := jsjson.MustObject("state", jsjson.MakeString("ACTIVE"))
	base := Recording{LedgerPath: "/l", SessionID: "s", EventCount: 3, HeadHash: &head, LastEventType: "TURN_COMPLETED", LastEventOccurredAt: "2026-01-01T00:00:00.000Z",
		LatestCheckpoint: cp, LatestStop: stopV, Lock: lock, Errors: []string{"e"}}
	cases := []struct {
		shape RecordingShape
		valid bool
		want  string
	}{
		{ShapeUnverified, false, "ledgerPath,valid,eventCount,headHash,errors"},
		{ShapeContradictory, false, "ledgerPath,sessionId,valid,eventCount,headHash,lastEvent,latestCheckpoint,lock,errors"},
		{ShapeValid, true, "ledgerPath,sessionId,valid,eventCount,headHash,lastEvent,latestCheckpoint,latestStop,lock,errors"},
	}
	for _, c := range cases {
		r := base
		r.Shape, r.Valid = c.shape, c.valid
		if got := keys(r.JSON()); got != c.want {
			t.Errorf("shape %d: %s want %s", c.shape, got, c.want)
		}
	}
	// A null headHash, and optional members left out.
	r := Recording{LedgerPath: "/l", Shape: ShapeUnverified, Errors: []string{}}
	if got := jsjson.Stringify(r.JSON()); got != `{"ledgerPath":"/l","valid":false,"eventCount":0,"headHash":null,"errors":[]}` {
		t.Error(got)
	}
	r = Recording{LedgerPath: "/l", SessionID: "s", Valid: true, HeadHash: &head, Errors: []string{}, Shape: ShapeValid}
	if got := keys(r.JSON()); got != "ledgerPath,sessionId,valid,eventCount,headHash,errors" {
		t.Error(got)
	}
}

func TestLatestJSON(t *testing.T) {
	if got := keys((Latest{LedgerPath: "/l", SessionID: "s", ValidCandidateCount: 2, LastEventOccurredAt: "t"}).JSON()); got != "ledgerPath,sessionId,validCandidateCount,lastEventOccurredAt" {
		t.Error(got)
	}
	if got := keys((Latest{LedgerPath: "/l", ValidCandidateCount: 1}).JSON()); got != "ledgerPath,validCandidateCount" {
		t.Error(got)
	}
}

func TestInspectEmptyStorageAndLatestFailsClosed(t *testing.T) {
	dir := repo(t)
	in, err := Inspect(dir, "")
	if err != nil || len(in.Recordings) != 0 || !strings.HasSuffix(in.StorageDirectory, filepath.Join("faultline", "recordings")) {
		t.Fatalf("%+v %v", in, err)
	}
	if got := jsjson.Stringify(in.JSON()); !strings.HasSuffix(got, `"recordings":[]}`) {
		t.Fatal(got)
	}
	_, err = ResolveLatestLedgerPath(dir)
	want := "No valid Codex sidecar ledger found under Git metadata recordings. Run a Codex session with trusted FaultLine hooks, or pass --ledger <path> explicitly."
	if err == nil || err.Error() != want {
		t.Fatal(err)
	}
}

func TestLatestStopStates(t *testing.T) {
	dir := repo(t)
	s, turn := "s-stop", "t-stop"
	started := rec(t, start(dir, s))
	rec(t, prompt(dir, s, turn, "p"))
	stopped := rec(t, stop(dir, s, turn))
	get := func() Recording {
		in, err := Inspect(dir, s)
		if err != nil || len(in.Recordings) != 1 || in.Recordings[0].Shape != ShapeValid {
			t.Fatalf("%+v %v", in, err)
		}
		return in.Recordings[0]
	}
	r := get()
	if got := jsjson.Stringify(r.LatestStop); got != `{"turnId":"t-stop","status":"CHECKPOINT_RECORDED","receipt":"DURABLE"}` {
		t.Fatal(got)
	}
	if keys(r.LatestCheckpoint) != "digest,capturedAt,headCommit,treeDigest,afterTurnOrdinal" {
		t.Fatal(keys(r.LatestCheckpoint))
	}
	if r.LatestCheckpoint.Get("digest").Str() != stopped.CheckpointDigest {
		t.Fatal("digest")
	}
	files := receiptFiles(t, filepath.Dir(started.LedgerPath))
	os.Remove(files[0])
	if got := jsjson.Stringify(get().LatestStop); got != `{"turnId":"t-stop","status":"CHECKPOINT_RECORDED","receipt":"RECOVERABLE_FROM_LEDGER"}` {
		t.Fatal(got)
	}
	// An unreadable receipt is a contradictory-shape recording.
	os.WriteFile(files[0], []byte("{"), 0o600)
	in, _ := Inspect(dir, s)
	if r := in.Recordings[0]; r.Valid || r.Shape != ShapeContradictory || len(r.Errors) != 1 ||
		!strings.HasPrefix(r.Errors[0], "Invalid sidecar stop receipt codex-stop-") {
		t.Fatalf("%+v", r)
	}
}

func TestLockHealthStates(t *testing.T) {
	dir := repo(t)
	started := rec(t, start(dir, "s-lock"))
	lock := started.LedgerPath + ".sidecar.lock"
	if v := sidecarLockHealth(started.LedgerPath); v.Kind() != jsjson.Undefined {
		t.Fatal("lock present")
	}
	os.WriteFile(lock, nil, 0o600)
	v := sidecarLockHealth(started.LedgerPath)
	if v.Get("state").Str() != "ACTIVE" || keys(v) != "state,ageMilliseconds" {
		t.Fatal(jsjson.Stringify(v))
	}
	old := time.Now().Add(-90 * time.Second)
	os.Chtimes(lock, old, old)
	if v := sidecarLockHealth(started.LedgerPath); v.Get("state").Str() != "STALE" || v.Get("ageMilliseconds").Num() < 89000 {
		t.Fatal(jsjson.Stringify(v))
	}
	os.Remove(lock)
	os.Mkdir(lock, 0o700)
	if got := jsjson.Stringify(sidecarLockHealth(started.LedgerPath)); got != `{"state":"INVALID"}` {
		t.Fatal(got)
	}
}

func TestResolveLatestLedgerPath(t *testing.T) {
	dir := repo(t)
	a := rec(t, start(dir, "s-a"))
	time.Sleep(5 * time.Millisecond)
	b := rec(t, start(dir, "s-b"))
	got, err := ResolveLatestLedgerPath(dir)
	if err != nil || got.LedgerPath != b.LedgerPath || got.SessionID != "s-b" || got.ValidCandidateCount != 2 || got.LastEventOccurredAt == "" {
		t.Fatalf("%+v %v", got, err)
	}
	_ = a
}
