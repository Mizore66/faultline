package guided

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/proofview"
	"github.com/Mizore66/faultline/internal/witness"
)

const taskFreezeID = "refund-1"
const taskFreezeCancelMsg = "Guided investigate cancelled before freeze."

func taskFreezeProposal(t *testing.T) jsjson.Value {
	t.Helper()
	v, err := jsjson.Parse(`{"proposalId":"` + taskFreezeID + `","proposedAt":"2026-01-02T03:04:05.000Z","incidentPacket":{"symptom":"refund currency","ciLog":"Expected USD; received EUR.",
		"repositoryLanguage":"JavaScript","repositorySummary":"A Node service that completes refunds."},
		"witness":{"behavior":"b","command":"node witness.mjs",
		"overlays":[{"path":"witness.mjs","bytesBase64":"Y29uc29sZS5sb2coMSk="}],
		"policy":{"network":"disabled","credentials":"redacted","timeoutSeconds":30}}}`)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func taskFreezeProposed(t *testing.T) string {
	t.Helper()
	store := t.TempDir()
	if _, err := witness.Propose(store, taskFreezeProposal(t)); err != nil {
		t.Fatal(err)
	}
	return store
}

func taskFreezeInput(review jsjson.Value) jsjson.Value {
	in := jsjson.NewObj()
	in.Set("reviewDigest", jsjson.MakeString(review.Get("reviewDigest").Str()))
	in.Set("approvedBy", jsjson.MakeString("Reviewer One"))
	return jsjson.MakeObject(in)
}

func taskFreezeFrozen(t *testing.T) (store, digest string) {
	t.Helper()
	store = taskFreezeProposed(t)
	review, err := witness.OpenWitnessReview(store, taskFreezeID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := witness.ApproveAndFreezeWitnessReview(store, review, taskFreezeInput(review)); err != nil {
		t.Fatal(err)
	}
	v := witness.VerifyFrozen(store, taskFreezeID, "", false)
	if !v.Valid || v.FrozenDigest == nil {
		t.Fatalf("fixture not frozen: %+v", v)
	}
	return store, *v.FrozenDigest
}

// taskFreezeLog records ordered events from concurrent seams.
type taskFreezeLog struct {
	mu     sync.Mutex
	events []string
}

func (l *taskFreezeLog) add(e string) {
	l.mu.Lock()
	l.events = append(l.events, e)
	l.mu.Unlock()
}

func (l *taskFreezeLog) get() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.events, ",")
}

type taskFakeServer struct {
	log      *taskFreezeLog
	closeErr error
	closes   int
}

func (s *taskFakeServer) URL() string { return "http://127.0.0.1:1" }
func (s *taskFakeServer) Close() error {
	s.closes++
	s.log.add("close")
	return s.closeErr
}

func taskInvalid() gitproof.WitnessVerification {
	return gitproof.WitnessVerification{Errors: []string{"approved proposal has not been frozen"}}
}

func taskValid(d string) gitproof.WitnessVerification {
	return gitproof.WitnessVerification{Valid: true, FrozenDigest: &d}
}

// taskFakeDeps builds deterministic seams: verify results come from the
// sequence (the last repeats), sleep is a no-op unless sleepFn is given.
func taskFakeDeps(log *taskFreezeLog, srv *taskFakeServer, seq []gitproof.WitnessVerification, sleepFn func(context.Context, time.Duration) error) freezeDeps {
	i := 0
	return freezeDeps{
		verify: func(string, string) gitproof.WitnessVerification {
			log.add("verify")
			v := seq[min(i, len(seq)-1)]
			i++
			return v
		},
		start: func(proofview.WitnessReviewServerOptions, float64) (freezeServer, error) {
			log.add("start")
			return srv, nil
		},
		sleep: func(ctx context.Context, d time.Duration) error {
			log.add("sleep")
			if sleepFn != nil {
				return sleepFn(ctx, d)
			}
			return nil
		},
	}
}

func TestGuidedFreezeExistingFrozenIgnoresCancelledSignalAndServer(t *testing.T) {
	store, digest := taskFreezeFrozen(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	res, err := WaitForFreeze(FreezeOptions{
		ProposalID: taskFreezeID, WitnessStore: store, Signal: ctx,
		Port:          func() *float64 { p := -5.0; return &p }(), // would fail if the server were started
		OnReviewReady: func(string) { called = true },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.FrozenDigest != digest || res.ReviewURL != nil || !res.AlreadyFrozen || called {
		t.Fatalf("res=%+v called=%v", res, called)
	}
}

func TestGuidedFreezeCancelledBeforeFreezeExactError(t *testing.T) {
	store := taskFreezeProposed(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var ready string
	_, err := WaitForFreeze(FreezeOptions{
		ProposalID: taskFreezeID, WitnessStore: store, Signal: ctx,
		OnReviewReady: func(u string) { ready = u },
	})
	if err == nil || err.Error() != taskFreezeCancelMsg {
		t.Fatalf("err=%v", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatal("must not be context.Canceled")
	}
	if ready == "" {
		t.Fatal("server must be started and ready before the signal is checked")
	}
	taskRequireRefused(t, ready)
}

func taskRequireRefused(t *testing.T, rawURL string) {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", u.Host, 2*time.Second)
	if err == nil {
		conn.Close()
		t.Fatalf("listener %s still accepting", u.Host)
	}
}

func TestGuidedFreezeOrderServerReadyImmediatePollThenSleep(t *testing.T) {
	log := &taskFreezeLog{}
	srv := &taskFakeServer{log: log}
	deps := taskFakeDeps(log, srv, []gitproof.WitnessVerification{
		taskValid("unused-first-is-existing-check"), // overridden below
	}, nil)
	// 1: existing check invalid, 2: immediate poll invalid, 3: after sleep valid.
	seq := []gitproof.WitnessVerification{taskInvalid(), taskInvalid(), taskValid("sha256:abc")}
	deps = taskFakeDeps(log, srv, seq, nil)
	res, err := waitForFreeze(FreezeOptions{
		ProposalID: "p", WitnessStore: "s",
		OnReviewReady: func(string) { log.add("ready") },
	}, deps)
	if err != nil {
		t.Fatal(err)
	}
	want := "verify,start,ready,verify,sleep,verify,close"
	if got := log.get(); got != want {
		t.Fatalf("order %s want %s", got, want)
	}
	if res.FrozenDigest != "sha256:abc" || res.AlreadyFrozen || res.ReviewURL == nil || *res.ReviewURL != srv.URL() {
		t.Fatalf("res=%+v", res)
	}
}

func TestGuidedFreezeDefaultsPortZeroPollAndAtomic(t *testing.T) {
	t.Setenv("FAULTLINE_WITNESS_TWO_STEP", "")
	var gotPort float64 = -1
	var gotOpts proofview.WitnessReviewServerOptions
	var gotSleep time.Duration
	log := &taskFreezeLog{}
	srv := &taskFakeServer{log: log}
	deps := taskFakeDeps(log, srv, []gitproof.WitnessVerification{taskInvalid(), taskInvalid(), taskValid("d")}, func(_ context.Context, d time.Duration) error {
		gotSleep = d
		return nil
	})
	deps.start = func(o proofview.WitnessReviewServerOptions, p float64) (freezeServer, error) {
		gotOpts, gotPort = o, p
		return srv, nil
	}
	if _, err := waitForFreeze(FreezeOptions{ProposalID: "p", WitnessStore: "w", DraftStore: "d"}, deps); err != nil {
		t.Fatal(err)
	}
	if gotPort != 0 || gotSleep != 500*time.Millisecond || gotOpts.TwoStep ||
		gotOpts.StoreDirectory != "w" || gotOpts.ProposalID != "p" || gotOpts.DraftStore != "d" {
		t.Fatalf("port=%v sleep=%v opts=%+v", gotPort, gotSleep, gotOpts)
	}
}

func TestGuidedFreezeTwoStepOnlyFromEnvironmentOne(t *testing.T) {
	for value, want := range map[string]bool{"1": true, "true": false, "0": false, "": false, " 1": false} {
		t.Setenv("FAULTLINE_WITNESS_TWO_STEP", value)
		var got bool
		log := &taskFreezeLog{}
		srv := &taskFakeServer{log: log}
		deps := taskFakeDeps(log, srv, []gitproof.WitnessVerification{taskInvalid(), taskValid("d")}, nil)
		deps.start = func(o proofview.WitnessReviewServerOptions, _ float64) (freezeServer, error) {
			got = o.TwoStep
			return srv, nil
		}
		if _, err := waitForFreeze(FreezeOptions{ProposalID: "p", WitnessStore: "w"}, deps); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("env %q: TwoStep=%v want %v", value, got, want)
		}
	}
}

func TestGuidedFreezeExplicitPortAndPollIntervalForwarded(t *testing.T) {
	var gotPort float64
	var gotSleep time.Duration
	log := &taskFreezeLog{}
	srv := &taskFakeServer{log: log}
	deps := taskFakeDeps(log, srv, []gitproof.WitnessVerification{taskInvalid(), taskInvalid(), taskValid("d")}, func(_ context.Context, d time.Duration) error {
		gotSleep = d
		return nil
	})
	deps.start = func(_ proofview.WitnessReviewServerOptions, p float64) (freezeServer, error) {
		gotPort = p
		return srv, nil
	}
	port := 4321.0
	poll := 7 * time.Millisecond
	if _, err := waitForFreeze(FreezeOptions{ProposalID: "p", WitnessStore: "w", Port: &port, PollInterval: &poll}, deps); err != nil {
		t.Fatal(err)
	}
	if gotPort != 4321 || gotSleep != poll {
		t.Fatalf("port=%v sleep=%v", gotPort, gotSleep)
	}
}

func TestGuidedFreezeSignalCheckedBeforeEachPoll(t *testing.T) {
	log := &taskFreezeLog{}
	srv := &taskFakeServer{log: log}
	ctx, cancel := context.WithCancel(context.Background())
	deps := taskFakeDeps(log, srv, []gitproof.WitnessVerification{taskInvalid()}, nil)
	_, err := waitForFreeze(FreezeOptions{
		ProposalID: "p", WitnessStore: "w", Signal: ctx,
		OnReviewReady: func(string) { log.add("ready"); cancel() },
	}, deps)
	if err == nil || err.Error() != taskFreezeCancelMsg {
		t.Fatalf("err=%v", err)
	}
	// No poll and no sleep after cancellation; the server is closed.
	if got, want := log.get(), "verify,start,ready,close"; got != want {
		t.Fatalf("order %s want %s", got, want)
	}
}

func TestGuidedFreezeCancelDuringSleepWakesAndCloses(t *testing.T) {
	log := &taskFreezeLog{}
	srv := &taskFakeServer{log: log}
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	deps := taskFakeDeps(log, srv, []gitproof.WitnessVerification{taskInvalid()}, func(c context.Context, _ time.Duration) error {
		close(entered)
		return freezeSleep(c, time.Hour)
	})
	type out struct {
		res FreezeResult
		err error
	}
	done := make(chan out, 1)
	go func() {
		r, e := waitForFreeze(FreezeOptions{ProposalID: "p", WitnessStore: "w", Signal: ctx}, deps)
		done <- out{r, e}
	}()
	<-entered
	cancel()
	o := <-done
	if o.err == nil || o.err.Error() != taskFreezeCancelMsg {
		t.Fatalf("err=%v", o.err)
	}
	if got, want := log.get(), "verify,start,verify,sleep,close"; got != want {
		t.Fatalf("order %s want %s", got, want)
	}
}

func TestGuidedFreezeSleepRejectsAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := freezeSleep(ctx, time.Hour); err == nil || err.Error() != taskFreezeCancelMsg {
		t.Fatalf("err=%v", err)
	}
	if err := freezeSleep(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
}

func TestGuidedFreezeInvalidObservationsRetryWithoutOverallTimeout(t *testing.T) {
	log := &taskFreezeLog{}
	srv := &taskFakeServer{log: log}
	seq := []gitproof.WitnessVerification{taskInvalid()}
	for i := 0; i < 50; i++ {
		seq = append(seq, gitproof.WitnessVerification{Errors: []string{"corrupt record"}})
	}
	seq = append(seq, taskValid("late"))
	res, err := waitForFreeze(FreezeOptions{ProposalID: "p", WitnessStore: "w"}, taskFakeDeps(log, srv, seq, nil))
	if err != nil || res.FrozenDigest != "late" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if srv.closes != 1 {
		t.Fatalf("closes=%d", srv.closes)
	}
}

func TestGuidedFreezeValidWithoutDigestIsNotFrozen(t *testing.T) {
	log := &taskFreezeLog{}
	srv := &taskFakeServer{log: log}
	seq := []gitproof.WitnessVerification{taskInvalid(), {Valid: true}, taskValid("d")}
	res, err := waitForFreeze(FreezeOptions{ProposalID: "p", WitnessStore: "w"}, taskFakeDeps(log, srv, seq, nil))
	if err != nil || res.FrozenDigest != "d" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestGuidedFreezeCloseErrorReplacesReturnAndError(t *testing.T) {
	closeErr := errors.New("close failed")
	log := &taskFreezeLog{}
	srv := &taskFakeServer{log: log, closeErr: closeErr}
	res, err := waitForFreeze(FreezeOptions{ProposalID: "p", WitnessStore: "w"},
		taskFakeDeps(log, srv, []gitproof.WitnessVerification{taskInvalid(), taskValid("d")}, nil))
	if err != closeErr || res != (FreezeResult{}) {
		t.Fatalf("res=%+v err=%v", res, err)
	}

	log = &taskFreezeLog{}
	srv = &taskFakeServer{log: log, closeErr: closeErr}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = waitForFreeze(FreezeOptions{ProposalID: "p", WitnessStore: "w", Signal: ctx},
		taskFakeDeps(log, srv, []gitproof.WitnessVerification{taskInvalid()}, nil))
	if err != closeErr {
		t.Fatalf("err=%v", err)
	}
}

func TestGuidedFreezeStartupErrorReturnedWithoutCallbackOrClose(t *testing.T) {
	log := &taskFreezeLog{}
	startErr := errors.New("listen EADDRINUSE")
	called := false
	deps := taskFakeDeps(log, &taskFakeServer{log: log}, []gitproof.WitnessVerification{taskInvalid()}, nil)
	deps.start = func(proofview.WitnessReviewServerOptions, float64) (freezeServer, error) { return nil, startErr }
	_, err := waitForFreeze(FreezeOptions{ProposalID: "p", WitnessStore: "w", OnReviewReady: func(string) { called = true }}, deps)
	if err != startErr || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}

// The readiness callback runs before the cleanup region: a throwing callback
// rejects with its own error and the started server is NOT closed by the
// waiter. The test owns cleanup of that server explicitly.
func TestGuidedFreezeReadyCallbackThrowDoesNotCloseServer(t *testing.T) {
	log := &taskFreezeLog{}
	srv := &taskFakeServer{log: log}
	t.Cleanup(func() { srv.Close() }) // explicit test-owned cleanup
	boom := errors.New("callback boom")
	deps := taskFakeDeps(log, srv, []gitproof.WitnessVerification{taskInvalid()}, nil)
	_, err := waitForFreeze(FreezeOptions{
		ProposalID: "p", WitnessStore: "w",
		OnReviewReady: func(string) { jsexc.Throw(boom) },
	}, deps)
	if err != boom {
		t.Fatalf("err=%v", err)
	}
	if srv.closes != 0 {
		t.Fatalf("waiter closed the server %d times", srv.closes)
	}
	if got := log.get(); got != "verify,start" {
		t.Fatalf("order %s", got)
	}
}

// Real server variant of the same seam: the listener stays live after the
// callback throws, and the test closes it through a captured handle.
func TestGuidedFreezeReadyCallbackThrowLeavesRealListenerOpen(t *testing.T) {
	store := taskFreezeProposed(t)
	var real freezeServer
	deps := defaultFreezeDeps()
	inner := deps.start
	deps.start = func(o proofview.WitnessReviewServerOptions, p float64) (freezeServer, error) {
		s, err := inner(o, p)
		real = s
		return s, err
	}
	boom := errors.New("callback boom")
	_, err := waitForFreeze(FreezeOptions{
		ProposalID: taskFreezeID, WitnessStore: store,
		OnReviewReady: func(string) { jsexc.Throw(boom) },
	}, deps)
	if real == nil {
		t.Fatal("server not started")
	}
	t.Cleanup(func() { real.Close() }) // explicit test-owned cleanup
	if err != boom {
		t.Fatalf("err=%v", err)
	}
	u, _ := url.Parse(real.URL())
	conn, derr := net.DialTimeout("tcp", u.Host, 2*time.Second)
	if derr != nil {
		t.Fatalf("listener closed by waiter: %v", derr)
	}
	conn.Close()
}

// ---- real HTTP ----

var (
	taskTokenRe  = regexp.MustCompile(`name="token" value="([^"]*)"`)
	taskDigestRe = regexp.MustCompile(`name="reviewDigest" value="([^"]*)"`)
)

type taskHTTPReply struct {
	status int
	body   string
}

func taskHTTP(t *testing.T, method, target, body string) taskHTTPReply {
	t.Helper()
	req, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return taskHTTPReply{res.StatusCode, string(b)}
}

type taskRun struct {
	ready  chan string
	done   chan taskRunOut
	cancel context.CancelFunc
}

type taskRunOut struct {
	res FreezeResult
	err error
}

func taskStart(t *testing.T, store string) *taskRun {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &taskRun{ready: make(chan string, 1), done: make(chan taskRunOut, 1), cancel: cancel}
	poll := time.Millisecond
	go func() {
		res, err := WaitForFreeze(FreezeOptions{
			ProposalID: taskFreezeID, WitnessStore: store, PollInterval: &poll, Signal: ctx,
			OnReviewReady: func(u string) { r.ready <- u },
		})
		r.done <- taskRunOut{res, err}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.done:
		case <-time.After(30 * time.Second):
			t.Error("waiter goroutine did not join")
		}
	})
	return r
}

func (r *taskRun) url(t *testing.T) string {
	t.Helper()
	select {
	case u := <-r.ready:
		return u
	case <-time.After(30 * time.Second):
		t.Fatal("review never ready")
		return ""
	}
}

func taskForm(t *testing.T, page string) url.Values {
	t.Helper()
	tok := taskTokenRe.FindStringSubmatch(page)
	dig := taskDigestRe.FindStringSubmatch(page)
	if tok == nil || dig == nil {
		t.Fatalf("form credential not found in page")
	}
	v := url.Values{}
	v.Set("token", tok[1])
	v.Set("reviewDigest", dig[1])
	v.Set("approvedBy", "Reviewer One")
	v.Set("acknowledged", "exact-witness-reviewed")
	return v
}

func taskRequireJoined(t *testing.T, r *taskRun, ready string) taskRunOut {
	t.Helper()
	select {
	case o := <-r.done:
		taskRequireRefused(t, ready)
		r.done <- o // keep for cleanup join
		return o
	case <-time.After(30 * time.Second):
		t.Fatal("waiter did not return")
		return taskRunOut{}
	}
}

func TestGuidedFreezeRealServerAtomicApproveFreeze(t *testing.T) {
	t.Setenv("FAULTLINE_WITNESS_TWO_STEP", "")
	store := taskFreezeProposed(t)
	r := taskStart(t, store)
	u := r.url(t)

	page := taskHTTP(t, "GET", u+"/", "")
	if page.status != 200 || !strings.Contains(page.body, `action="/approve-freeze"`) || strings.Contains(page.body, `action="/freeze"`) {
		t.Fatalf("expected atomic page: %d", page.status)
	}
	// An invalid credential neither approves nor releases the waiter.
	bad := taskForm(t, page.body)
	bad.Set("token", strings.Repeat("0", 64))
	if rep := taskHTTP(t, "POST", u+"/approve-freeze", bad.Encode()); rep.status != 403 {
		t.Fatalf("bad credential status=%d", rep.status)
	}
	select {
	case o := <-r.done:
		t.Fatalf("waiter returned without a human freeze: %+v", o)
	default:
	}
	if v := witness.VerifyFrozen(store, taskFreezeID, "", false); v.Valid {
		t.Fatal("record frozen by an invalid credential")
	}

	if rep := taskHTTP(t, "POST", u+"/approve-freeze", taskForm(t, page.body).Encode()); rep.status != 200 {
		t.Fatalf("approve-freeze status=%d", rep.status)
	}
	o := taskRequireJoined(t, r, u)
	if o.err != nil {
		t.Fatal(o.err)
	}
	v := witness.VerifyFrozen(store, taskFreezeID, "", false)
	if !v.Valid || v.FrozenDigest == nil || *v.FrozenDigest != o.res.FrozenDigest {
		t.Fatalf("chain=%+v res=%+v", v, o.res)
	}
	if o.res.AlreadyFrozen || o.res.ReviewURL == nil || *o.res.ReviewURL != u {
		t.Fatalf("res=%+v", o.res)
	}
	// Digest also verifies as the expected external digest.
	if ev := witness.VerifyFrozen(store, taskFreezeID, o.res.FrozenDigest, true); !ev.Valid || ev.ExternalDigestStatus != "MATCH" {
		t.Fatalf("external=%+v", ev)
	}
}

func TestGuidedFreezeRealServerTwoStepFromEnvironment(t *testing.T) {
	t.Setenv("FAULTLINE_WITNESS_TWO_STEP", "1")
	store := taskFreezeProposed(t)
	r := taskStart(t, store)
	u := r.url(t)

	page := taskHTTP(t, "GET", u+"/", "")
	if !strings.Contains(page.body, `action="/approve"`) || strings.Contains(page.body, `action="/approve-freeze"`) {
		t.Fatal("expected two-step page")
	}
	if rep := taskHTTP(t, "POST", u+"/approve-freeze", taskForm(t, page.body).Encode()); rep.status != 404 {
		t.Fatalf("atomic action status=%d", rep.status)
	}
	if rep := taskHTTP(t, "POST", u+"/approve", taskForm(t, page.body).Encode()); rep.status != 200 {
		t.Fatalf("approve status=%d", rep.status)
	}
	// Approved is not frozen: the waiter keeps waiting. Poll the store through
	// the page (a later request is served only if the waiter is still running).
	select {
	case o := <-r.done:
		t.Fatalf("waiter returned after approval only: %+v", o)
	default:
	}
	if rep := taskHTTP(t, "GET", u+"/", ""); rep.status != 200 {
		t.Fatalf("server not serving after approve: %d", rep.status)
	}
	page2 := taskHTTP(t, "GET", u+"/", "")
	if !strings.Contains(page2.body, `action="/freeze"`) {
		t.Fatal("freeze form missing after approval")
	}
	if rep := taskHTTP(t, "POST", u+"/freeze", taskForm(t, page2.body).Encode()); rep.status != 200 {
		t.Fatalf("freeze status=%d body=%.200s", rep.status, rep.body)
	}
	o := taskRequireJoined(t, r, u)
	if o.err != nil {
		t.Fatal(o.err)
	}
	v := witness.VerifyFrozen(store, taskFreezeID, o.res.FrozenDigest, true)
	if !v.Valid || v.ExternalDigestStatus != "MATCH" || o.res.ReviewURL == nil || *o.res.ReviewURL != u {
		t.Fatalf("chain=%+v res=%+v", v, o.res)
	}
}

func TestGuidedFreezeRealServerCancelAfterReadyDrainsAndCloses(t *testing.T) {
	store := taskFreezeProposed(t)
	r := taskStart(t, store)
	u := r.url(t)
	if rep := taskHTTP(t, "GET", u+"/", ""); rep.status != 200 {
		t.Fatalf("status=%d", rep.status)
	}
	r.cancel()
	o := taskRequireJoined(t, r, u)
	if o.err == nil || o.err.Error() != taskFreezeCancelMsg || o.res != (FreezeResult{}) {
		t.Fatalf("out=%+v", o)
	}
	if v := witness.VerifyFrozen(store, taskFreezeID, "", false); v.Valid {
		t.Fatal("cancellation must not freeze")
	}
}

func TestGuidedFreezeRealServerFailedVerifyKeepsWaitingUntilCancel(t *testing.T) {
	store := taskFreezeProposed(t)
	r := taskStart(t, store)
	u := r.url(t)
	// Approval without a freeze record stays "has not been frozen"; many polls
	// pass while the server remains live.
	for i := 0; i < 5; i++ {
		if rep := taskHTTP(t, "GET", u+"/", ""); rep.status != 200 {
			t.Fatalf("status=%d", rep.status)
		}
	}
	select {
	case o := <-r.done:
		t.Fatalf("returned early: %+v", o)
	default:
	}
	r.cancel()
	o := taskRequireJoined(t, r, u)
	if o.err == nil || o.err.Error() != taskFreezeCancelMsg {
		t.Fatalf("err=%v", o.err)
	}
}

func TestGuidedFreezeCancelledErrorsAreFreshWithExactText(t *testing.T) {
	a, b := newFreezeCancelledError(), newFreezeCancelledError()
	if a == b || a.Error() != taskFreezeCancelMsg || b.Error() != taskFreezeCancelMsg {
		t.Fatalf("a=%v b=%v same=%v", a, b, a == b)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e1, e2 := freezeSleep(ctx, time.Hour), freezeSleep(ctx, time.Hour)
	if e1 == e2 || e1.Error() != taskFreezeCancelMsg {
		t.Fatalf("sleep errors not fresh: %v %v", e1, e2)
	}
}

func TestGuidedFreezeBindFailureReturnsServerError(t *testing.T) {
	store := taskFreezeProposed(t)
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := float64(l.Addr().(*net.TCPAddr).Port)
	called := false
	_, err = WaitForFreeze(FreezeOptions{
		ProposalID: taskFreezeID, WitnessStore: store, Port: &port,
		OnReviewReady: func(string) { called = true },
	})
	if err == nil || !strings.HasPrefix(err.Error(), "listen EADDRINUSE") || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}
