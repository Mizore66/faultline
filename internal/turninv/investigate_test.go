//go:build faultline_test && unix

package turninv

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/ledger"
	"github.com/Mizore66/faultline/internal/sandbox"
	"github.com/Mizore66/faultline/internal/turnsnap"
	"github.com/Mizore66/faultline/internal/witnessresult"
)

// testLedger builds the tests' lifecycle ledgers with the ledger package
// (src/ledger.ts createCodexLifecycleLedger, appendLifecycleEvent and
// writeCodexLifecycleLedgerAtomic), as the TS test does.
type testLedger struct {
	t      *testing.T
	ledger jsjson.Value
}

func newTestLedger(t *testing.T, sessionID string) *testLedger {
	t.Helper()
	l, err := ledger.Create(ledger.CreateOptions{SessionID: sessionID})
	if err != nil {
		t.Fatal(err)
	}
	return &testLedger{t, l}
}

func (l *testLedger) append(eventType string, payload jsjson.Value) {
	l.t.Helper()
	next, err := ledger.Append(l.ledger, jsjson.MustObject("type", jsjson.MakeString(eventType), "payload", payload), ledger.AppendOptions{})
	if err != nil {
		l.t.Fatal(err)
	}
	l.ledger = next
}

func (l *testLedger) write(path string) {
	l.t.Helper()
	if _, err := ledger.WriteAtomic(path, l.ledger); err != nil {
		l.t.Fatal(err)
	}
}

// The tests' session helpers (turn-investigation.test.ts:105-135).
func (l *testLedger) start(repository string) {
	l.append("SESSION_STARTED", jsjson.MustObject("transport", jsjson.MakeString("SIDE_CAR"), "workingDirectory", jsjson.MakeString(repository)))
}

func (l *testLedger) baseline(snapshot jsjson.Value) {
	l.append("SESSION_BASELINE_SNAPSHOT", jsjson.MustObject("snapshot", snapshot))
}

func (l *testLedger) turn(ordinal int, snapshot jsjson.Value) {
	turnID, n := jsjson.MakeString("turn-"+strconv.Itoa(ordinal)), jsjson.MakeNumber(float64(ordinal))
	l.append("TURN_STARTED", jsjson.MustObject("turnId", turnID, "turnOrdinal", n, "promptDigest", jsjson.MakeString("sha256:"+strings.Repeat("a", 64))))
	l.append("TURN_COMPLETED", jsjson.MustObject("turnId", turnID, "turnOrdinal", n, "outcome", jsjson.MakeString("COMPLETED")))
	l.snapshotAgain(ordinal, snapshot)
}

// snapshotAgain records another TURN_TREE_SNAPSHOT for the completed turn
// ordinal: a verified ledger with a duplicate ordinal.
func (l *testLedger) snapshotAgain(ordinal int, snapshot jsjson.Value) {
	l.append("TURN_TREE_SNAPSHOT", jsjson.MustObject("turnId", jsjson.MakeString("turn-"+strconv.Itoa(ordinal)),
		"turnOrdinal", jsjson.MakeNumber(float64(ordinal)), "snapshot", snapshot))
}

// session is a repository whose state.txt starts "good", with a ledger and
// the frozen witness.
type session struct {
	t                    *testing.T
	repository, ledgerAt string
	ledger               *testLedger
	frozen               jsjson.Value
}

func newSession(t *testing.T, files map[string]string) *session {
	t.Helper()
	if files == nil {
		files = map[string]string{}
	}
	files["state.txt"] = "good\n"
	repo, _ := gitinv.BuildRepoForTest(t, "sha1", "repo", []gitinv.RepoCommitForTest{{Files: files}})
	l := newTestLedger(t, "turn-investigation")
	l.start(repo)
	return &session{t, repo, filepath.Join(filepath.Dir(repo), "ledger.json"), l, gitinv.FrozenWitnessForTest(t)}
}

func (s *session) write(path, content string) {
	s.t.Helper()
	if err := os.WriteFile(filepath.Join(s.repository, path), []byte(content), 0o644); err != nil {
		s.t.Fatal(err)
	}
}

func (s *session) capture() jsjson.Value {
	s.t.Helper()
	snapshot, _, err := turnsnap.Capture(s.repository, turnsnap.CaptureOptions{Sleep: func(time.Duration) {}})
	if err != nil {
		s.t.Fatal(err)
	}
	return snapshot
}

func (s *session) investigate(r Request) (jsjson.Value, error) {
	s.t.Helper()
	s.ledger.write(s.ledgerAt)
	r.Repository, r.LedgerPath, r.FrozenWitness = s.repository, s.ledgerAt, s.frozen
	r.ExpectedFrozenDigest = s.frozen.Get("frozenDigest").Str()
	if r.Image == "" {
		r.Image = gitinv.PinnedImageForTest
	}
	return InvestigateTurnTrees(r)
}

// stateReadingRunner is the TS test's stateReadingRunner (:72): it reads
// state.txt in the plan's source directory.
func stateReadingRunner(p sandbox.Plan) (sandbox.RunResult, error) {
	b, err := os.ReadFile(filepath.Join(p.Value.Get("sourceDirectory").Str(), "state.txt"))
	if err != nil {
		return sandbox.RunResult{}, err
	}
	state := strings.TrimSpace(string(b))
	code := 0
	outcome := "PREDICATE_PASS"
	if state == "bad" {
		code, outcome = 1, "PREDICATE_FAIL"
	}
	return sandbox.RunResult{ExitCode: &code, Stdout: "state=" + state + "\n" + witnessresult.Format(outcome, "", false) + "\n"}, nil
}

func fakeDockerOnPath(t *testing.T, name string) {
	t.Setenv("PATH", gitinv.FakeDockerDirForTest(t, name)+string(os.PathListSeparator)+os.Getenv("PATH"))
}

var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

func TestInvalidWitnessShortCircuits(t *testing.T) {
	t.Parallel()
	frozen := gitinv.FrozenWitnessForTest(t)
	result, err := InvestigateTurnTrees(Request{
		Repository: t.TempDir(), LedgerPath: "/nonexistent/ledger.json", FrozenWitness: frozen,
		ExpectedFrozenDigest: "sha256:" + strings.Repeat("0", 64),
		Image:                "registry.example/faultline-node@sha256:" + strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatal(err) // the ledger is never read for an invalid witness
	}
	if result.Get("status").Str() != "INVALID_WITNESS" || result.Get("proof", "reason").Str() != "Frozen witness digest mismatch." ||
		result.Get("repository").Kind() != jsjson.Null || result.Get("proof", "evidenceGrade").Str() != "NONE" {
		t.Fatal(result)
	}
	if result.Get("introduction", "reason").Str() != "Frozen witness digest mismatch." || result.Get("witness", "externalDigestStatus").Str() != "MISMATCH" ||
		result.Get("witness", "witnessDigest").Kind() != jsjson.Null || len(result.Get("errors").Items()) == 0 {
		t.Fatal(jsjson.Stringify(result))
	}
}

// "attributes introduction to Turn 1 when the session baseline passes and
// Turn 1 fails" (:317), through the native runner and the pass-fail fake
// Docker: the only proof-eligible path.
func TestBaselinePassToFailIsAttributedProof(t *testing.T) {
	fakeDockerOnPath(t, "pass-fail")
	s := newSession(t, nil)
	s.ledger.baseline(s.capture())
	s.write("state.txt", "bad\n")
	s.ledger.turn(1, s.capture())
	result, err := s.investigate(Request{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Get("status").Str() != "COMPLETED" || len(result.Get("transitions").Items()) != 1 ||
		result.Get("transitions").Items()[0].Get("kind").Str() != "PASS_TO_FAIL" {
		t.Fatal(jsjson.Stringify(result))
	}
	if st := result.Get("states").Items()[0]; st.Get("role").Str() != "SESSION_BASELINE" || st.Get("turnId").Str() != SessionBaselineTurnID {
		t.Fatal(jsjson.Stringify(st))
	}
	intro := result.Get("introduction")
	if intro.Get("status").Str() != "ATTRIBUTED" || intro.Get("turnOrdinal").Num() != 1 || intro.Get("turnId").Str() != "turn-1" ||
		!strings.Contains(intro.Get("reason").Str(), "Earliest recorded stable PASS→FAIL occurred at turn 1") {
		t.Fatal(jsjson.Stringify(intro))
	}
	proof := result.Get("proof")
	if !proof.Get("isProof").Bool() || proof.Get("executionTrust").Str() != "NATIVE_DOCKER" || proof.Get("evidenceGrade").Str() != "EXPERIMENTAL_TURN" ||
		proof.Get("reason").Str() != "Each listed transition has three distinct Docker-isolated executions on both adjacent turn-tree states." {
		t.Fatal(jsjson.Stringify(proof))
	}
	runs := result.Get("runs").Items()
	if len(runs) != 6 || len(result.Get("stableStates").Items()) != 2 || result.Get("environmentHomogeneity").Str() != "HOMOGENEOUS" {
		t.Fatal(jsjson.Stringify(result))
	}
	for _, run := range runs {
		if run.Get("result", "executor").Str() != "NATIVE_DOCKER" || run.Get("sandbox", "runtime", "image").Str() != gitinv.PinnedImageForTest ||
			!strings.HasPrefix(run.Get("result", "stdoutDigest").Str(), "sha256:") {
			t.Fatal(jsjson.Stringify(run))
		}
	}
	if !digestPattern.MatchString(Digest(result)) {
		t.Fatal(Digest(result))
	}
}

// "localizes PASS→FAIL when an earlier turn snapshot passes and a later one
// fails" (:262): an injected runner is never proof, and without a session
// baseline the failure is not attributed.
func TestInjectedRunnerIsNeverProof(t *testing.T) {
	t.Parallel()
	s := newSession(t, nil)
	s.ledger.turn(1, s.capture())
	s.write("state.txt", "bad\n")
	s.ledger.turn(2, s.capture())
	result, err := s.investigate(Request{Runner: stateReadingRunner, Injected: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Get("status").Str() != "COMPLETED" || len(result.Get("transitions").Items()) != 1 || len(result.Get("runs").Items()) != 6 {
		t.Fatal(jsjson.Stringify(result))
	}
	if intro := result.Get("introduction"); intro.Get("status").Str() != "UNATTRIBUTED" || intro.Get("turnOrdinal").Kind() != jsjson.Null {
		t.Fatal(jsjson.Stringify(intro))
	}
	proof := result.Get("proof")
	if proof.Get("isProof").Bool() || proof.Get("dockerIsolated").Bool() || proof.Get("executionTrust").Str() != "INJECTED_RUNNER" ||
		proof.Get("evidenceGrade").Str() != "EXPERIMENTAL_TURN" ||
		proof.Get("reason").Str() != "An injected runner produced these observations; FaultLine refuses to certify it as native Docker proof." {
		t.Fatal(jsjson.Stringify(proof))
	}
	for _, run := range result.Get("runs").Items() {
		if run.Get("result", "executor").Str() != "INJECTED_RUNNER" {
			t.Fatal(jsjson.Stringify(run))
		}
	}
}

func TestDuplicateOrdinalRefusesLocalization(t *testing.T) {
	t.Parallel()
	s := newSession(t, nil)
	s.ledger.turn(1, s.capture())
	s.write("state.txt", "bad\n")
	s.ledger.snapshotAgain(1, s.capture())
	result, err := s.investigate(Request{Runner: stateReadingRunner, Injected: true})
	if err != nil {
		t.Fatal(err)
	}
	errs := result.Get("errors").Items()
	if result.Get("status").Str() != "DUPLICATE_TURN_SNAPSHOTS" || len(errs) != 1 || errs[0].Str() != DuplicateOrdinalError ||
		result.Get("proof", "reason").Str() != "Multiple snapshot events share a turnOrdinal; refuse ambiguous localization." ||
		len(result.Get("states").Items()) != 2 || len(result.Get("runs").Items()) != 0 || !digestPattern.MatchString(result.Get("ledgerDigest").Str()) ||
		result.Get("repository").Str() != s.repository || result.Get("proof", "executionTrust").Str() != "INJECTED_RUNNER" {
		t.Fatal(jsjson.Stringify(result))
	}
}

func TestFewerThanTwoStates(t *testing.T) {
	t.Parallel()
	s := newSession(t, nil)
	s.ledger.baseline(s.capture())
	result, err := s.investigate(Request{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Get("status").Str() != "NO_TURN_SNAPSHOTS" || len(result.Get("errors").Items()) != 0 || len(result.Get("states").Items()) != 1 ||
		result.Get("introduction", "reason").Str() != "Need at least two snapshot states (session baseline and/or TURN_TREE_SNAPSHOT events) for turn localization." ||
		result.Get("environmentHomogeneity").Str() != "EMPTY" || result.Get("proof", "dockerIsolated").Bool() != true {
		t.Fatal(jsjson.Stringify(result))
	}
}

// "fails closed when package.json changes" (:832) and "selects
// per-fingerprint images from runtimeMapping when environments diverge"
// (:876).
func TestHeterogeneousEnvironment(t *testing.T) {
	t.Parallel()
	s := newSession(t, map[string]string{"package.json": `{"name":"fixture","scripts":{"test":"node a"}}`})
	s.ledger.turn(1, s.capture())
	s.write("state.txt", "bad\n")
	s.write("package.json", `{"name":"fixture","scripts":{"test":"node b"}}`)
	s.ledger.turn(2, s.capture())
	blocked, err := s.investigate(Request{Runner: stateReadingRunner, Injected: true})
	if err != nil {
		t.Fatal(err)
	}
	errs := blocked.Get("errors").Items()
	if blocked.Get("status").Str() != "ENVIRONMENT_CHANGED" || blocked.Get("environmentHomogeneity").Str() != "HETEROGENEOUS" ||
		len(errs) != 1 || errs[0].Str() != gitinv.EnvironmentChangedProofMessage || blocked.Get("proof", "reason").Str() != gitinv.EnvironmentChangedProofMessage ||
		len(blocked.Get("runs").Items()) != 0 || blocked.Get("proof", "evidenceGrade").Str() != "NONE" || len(blocked.Get("environmentFingerprints").Items()) != 2 {
		t.Fatal(jsjson.Stringify(blocked))
	}
	digestA := blocked.Get("environmentFingerprints").Items()[0].Get("digest").Str()
	digestB := blocked.Get("environmentFingerprints").Items()[1].Get("digest").Str()
	if digestA == digestB {
		t.Fatal(digestA)
	}

	// A mapped image that is not digest-pinned is a ZodError message.
	invalid, err := s.investigate(Request{Runner: stateReadingRunner, Injected: true,
		RuntimeMapping: []gitinv.MappingEntry{{Fingerprint: digestA, Image: gitinv.PinnedImageForTest}, {Fingerprint: digestB, Image: "node:22"}}})
	if err != nil {
		t.Fatal(err)
	}
	if invalid.Get("status").Str() != "EXECUTION_ERROR" || !strings.Contains(invalid.Get("proof", "reason").Str(), "expected a digest-pinned image reference") {
		t.Fatal(jsjson.Stringify(invalid))
	}

	imageA := "registry.example/faultline-a@sha256:" + strings.Repeat("a", 64)
	imageB := "registry.example/faultline-b@sha256:" + strings.Repeat("b", 64)
	result, err := s.investigate(Request{Runner: stateReadingRunner, Injected: true,
		RuntimeMapping: []gitinv.MappingEntry{{Fingerprint: digestA, Image: imageA}, {Fingerprint: digestB, Image: imageB}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Get("status").Str() != "COMPLETED" || len(result.Get("runs").Items()) != 6 || len(result.Get("transitions").Items()) != 1 ||
		result.Get("proof", "isProof").Bool() || result.Get("proof", "evidenceGrade").Str() != "EXPERIMENTAL_TURN" {
		t.Fatal(jsjson.Stringify(result))
	}
	if mapping := result.Get("runtimeMapping"); mapping.Get(digestA).Str() != imageA || mapping.Get(digestB).Str() != imageB || len(mapping.Obj().Keys()) != 2 {
		t.Fatal(jsjson.Stringify(mapping))
	}
	for _, run := range result.Get("runs").Items() {
		digest, image := digestA, imageA
		if run.Get("stateIndex").Num() == 1 {
			digest, image = digestB, imageB
		}
		if run.Get("environmentFingerprintDigest").Str() != digest || run.Get("sandbox", "runtime", "image").Str() != image {
			t.Fatal(jsjson.Stringify(run))
		}
	}
}

// resign replaces a snapshot's treeDigest and signs it again
// (signTurnTreeSnapshot, src/turn-snapshot.ts:290).
func resign(t *testing.T, snapshot jsjson.Value, tree string) jsjson.Value {
	t.Helper()
	unsigned := bundle.Without(snapshot, "digest")
	unsigned.Obj().Set("treeDigest", jsjson.MakeString(tree))
	parsed, err := bundle.ParseValue(unsigned, gitproof.UnsignedTurnTreeSnapshotSchema)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Obj().Set("digest", jsjson.MakeString(bundle.Must(canonical.DigestJSON(parsed))))
	return parsed
}

// A snapshot whose tree is not in the repository fails to materialize: no
// fingerprint, so the fingerprint count check refuses the investigation.
func TestMaterializationError(t *testing.T) {
	t.Parallel()
	s := newSession(t, nil)
	s.ledger.baseline(s.capture())
	s.ledger.turn(1, resign(t, s.capture(), strings.Repeat("e", 40)))
	result, err := s.investigate(Request{Runner: stateReadingRunner, Injected: true})
	if err != nil {
		t.Fatal(err)
	}
	errs := result.Get("errors").Items()
	if result.Get("status").Str() != "EXECUTION_ERROR" || len(errs) != 1 || !strings.Contains(errs[0].Str(), strings.Repeat("e", 40)) ||
		result.Get("proof", "reason").Str() != "Could not compute an environment fingerprint for every turn state." ||
		len(result.Get("environmentFingerprints").Items()) != 1 || result.Get("environmentHomogeneity").Str() != "HOMOGENEOUS" ||
		len(result.Get("runs").Items()) != 0 {
		t.Fatal(jsjson.Stringify(result))
	}
}

// The ledger read's error is what the promise rejects with.
func TestLedgerReadErrorPropagates(t *testing.T) {
	t.Parallel()
	s := newSession(t, nil)
	s.ledger.write(s.ledgerAt)
	_, err := InvestigateTurnTrees(Request{Repository: s.repository, LedgerPath: s.ledgerAt + ".missing", FrozenWitness: s.frozen,
		ExpectedFrozenDigest: s.frozen.Get("frozenDigest").Str(), Image: gitinv.PinnedImageForTest})
	if err == nil || !strings.HasPrefix(err.Error(), "Unable to read lifecycle ledger "+s.ledgerAt+".missing: ") {
		t.Fatal(err)
	}
}
