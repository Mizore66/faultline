//go:build faultline_test

package difftest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/difftest/servefixture"
	"github.com/Mizore66/faultline/internal/incident"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// TestLiveWitnessReview* run the frozen TypeScript `node dist/cli.js witness
// review ...` and the Go fl on identical stores and compare, for every case,
// the complete raw HTTP response bytes, command streams, exit status or raw
// signal, and the resulting witness store tree. They need explicit oracle
// enablement (oracle.Skip, first); once enabled there is no further skip: Node
// 22.22.2, manifest 0.1.4 and dist/cli.js 0.1.4 are pinned independently.
//
// Declared normalizations, and only these:
//   - each side's actual ephemeral port, `127.0.0.1:<port>` -> `127.0.0.1:{PORT}`;
//   - each side's independently extracted 64-hex form token -> `{TOKEN}`;
//   - the single `Date:` response header, which is wall-clock by nature; it
//     must be present exactly once and parse as an HTTP-date on each side, and
//     is then replaced by `{HTTP-DATE}`.
//
// Both sides run sequentially in the SAME work directory path, so no path is
// normalized. Record timestamps are made equal by construction: the TypeScript
// side loads difftest/testdata/witness-review/live-clock.mjs and the Go side
// runs under FAULTLINE_TEST_SEED, both stamping the first record
// 2026-07-16T12:00:00.000Z and the next .001Z; the resulting store trees are
// compared byte-for-byte and verified by the independent verifier in
// witness_review_replay_test.go after every step, against an explicit
// expectation written in the case, never one derived from either side.

const (
	liveReviewID      = "p1"
	liveReviewPropose = `{"proposalId":"p1","proposedAt":"2026-07-16T12:00:00.000Z","incidentPacket":{"symptom":"refund currency","ciLog":"Expected USD; received EUR.","repositoryLanguage":"JavaScript","repositorySummary":"A Node service that completes refunds."},"witness":{"behavior":"b","command":"node witness.mjs","overlays":[{"path":"witness.mjs","bytesBase64":"Y29uc29sZS5sb2coMSk="}],"policy":{"network":"disabled","credentials":"redacted","timeoutSeconds":30}}}`
	// A second valid proposal with the same id and a different command.
	liveReviewReplacement = `{"proposalId":"p1","proposedAt":"2026-07-16T12:00:00.000Z","incidentPacket":{"symptom":"refund currency","ciLog":"Expected USD; received EUR.","repositoryLanguage":"JavaScript","repositorySummary":"A Node service that completes refunds."},"witness":{"behavior":"b","command":"node other.mjs","overlays":[{"path":"witness.mjs","bytesBase64":"Y29uc29sZS5sb2coMSk="}],"policy":{"network":"disabled","credentials":"redacted","timeoutSeconds":30}}}`
)

var (
	liveTokenRE   = regexp.MustCompile(`name="token" value="([0-9a-f]{64})"`)
	liveDigestRE  = regexp.MustCompile(`name="reviewDigest" value="(sha256:[0-9a-f]{64})"`)
	liveStartupRE = regexp.MustCompile(`^FaultLine local witness review: (http://127\.0\.0\.1:[1-9][0-9]*)\n`)
	liveDateRE    = regexp.MustCompile(`(?m)^Date: ([^\r\n]*)\r$`)
)

// liveImpl is one implementation's launch recipe.
type liveImpl struct {
	name   string
	exe    string
	prefix []string
	env    []string // extra environment
}

func liveReviewImpls(t *testing.T, node, cli string) []liveImpl {
	t.Helper()
	clock := filepath.Join(oracle.RepoRoot(t), "difftest", "testdata", "witness-review", "live-clock.mjs")
	common := []string{"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "FAULTLINE_NO_BROWSER=1"}
	return []liveImpl{
		{"TS", node, []string{"--import", clock, cli}, common},
		{"Go", flBinary, nil, append(slicesClone(common), "FAULTLINE_TEST_SEED=live")},
	}
}

func slicesClone(s []string) []string { return append([]string(nil), s...) }

func (im liveImpl) environ(root string, extra ...string) []string {
	return serveBundleChildEnv(root, append(slicesClone(im.env), extra...)...)
}

// run runs the side to completion in root and returns its streams and status.
func (im liveImpl) run(t *testing.T, root string, extraEnv []string, args ...string) (stdout, stderr string, code *int, signal *string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), serveBundleStartupTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, im.exe, append(slicesClone(im.prefix), args...)...)
	cmd.Dir, cmd.Env = root, im.environ(root, extraEnv...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("%s %v: %v", im.name, args, err)
	}
	if ctx.Err() != nil {
		t.Fatalf("%s %v did not exit: stdout=%q stderr=%q", im.name, args, out.String(), errOut.String())
	}
	c := cmd.ProcessState.ExitCode()
	return out.String(), errOut.String(), &c, nil
}

// liveWorkdir recreates the fixed work directory (the same path for both
// sides, evaluated through symlinks) and writes the proposal input.
func liveWorkdir(t *testing.T, parent string) string {
	t.Helper()
	root := filepath.Join(parent, "work")
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resolved, "p.json"), []byte(liveReviewPropose), 0o600); err != nil {
		t.Fatal(err)
	}
	return resolved
}

func (im liveImpl) propose(t *testing.T, root, input string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "p.json"), []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code, _ := im.run(t, root, nil, "witness", "propose", "--input", "p.json"); *code != 0 {
		t.Fatalf("%s propose: exit %d %q", im.name, *code, stderr)
	}
}

// ---- raw HTTP ----

func liveExchange(t *testing.T, addr string, request []byte) []byte {
	t.Helper()
	conn, err := net.DialTimeout("tcp4", addr, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	// The server may answer and close before a large body is consumed; a
	// failed write then only means the response is already on its way.
	go func() { _, _ = conn.Write(request) }()
	var got bytes.Buffer
	_, err = io.Copy(&got, conn)
	if err != nil && got.Len() == 0 {
		t.Fatalf("no response to %q: %v", clipText(request), err)
	}
	return got.Bytes()
}

func clipText(b []byte) string {
	if len(b) > 200 {
		return fmt.Sprintf("%q...(%d bytes)", b[:200], len(b))
	}
	return fmt.Sprintf("%q", b)
}

// liveFrame reduces one raw HTTP response to what the shared transport
// documents as comparable (difftest/KNOWN_DIFFERENCES.md, "The judge-proof
// server"): the status line, every header line with its exact name case and
// value, and the de-chunked body. Header ORDER (Go writes sorted, Node in
// insertion order) and chunk BOUNDARIES (Node one chunk, Go 2 KiB chunks) are
// the two already-documented framing qualifications and are the only things
// ignored here; every header name, value and body byte must match exactly. A
// response that does not parse as a status line, headers and a complete body
// (a bare 4xx, a closed connection) is compared as raw bytes.
func liveFrame(raw string) string {
	head, body, ok := strings.Cut(raw, "\r\n\r\n")
	if !ok {
		return raw
	}
	lines := strings.Split(head, "\r\n")
	headers := slicesClone(lines[1:])
	sort.Strings(headers)
	chunked := false
	for _, h := range headers {
		if strings.EqualFold(h, "Transfer-Encoding: chunked") {
			chunked = true
		}
	}
	if chunked {
		var out strings.Builder
		rest := body
		for {
			sizeText, after, ok := strings.Cut(rest, "\r\n")
			if !ok {
				return raw
			}
			size, err := strconv.ParseInt(sizeText, 16, 64)
			if err != nil || int64(len(after)) < size+2 {
				return raw
			}
			if size == 0 {
				body = out.String()
				break
			}
			out.WriteString(after[:size])
			rest = after[size+2:]
		}
	}
	return lines[0] + "\n" + strings.Join(headers, "\n") + "\n\n" + body
}

// liveNormalize applies exactly the declared normalizations and the framing
// reduction above.
func liveNormalize(t *testing.T, side string, raw []byte, port, token string) string {
	t.Helper()
	text := string(raw)
	if matches := liveDateRE.FindAllStringSubmatch(text, -1); len(matches) > 0 {
		if len(matches) != 1 {
			t.Fatalf("%s: Date sent %d times in %s", side, len(matches), clipText(raw))
		}
		if _, err := http.ParseTime(matches[0][1]); err != nil {
			t.Fatalf("%s: Date %q is not an HTTP-date: %v", side, matches[0][1], err)
		}
		text = liveDateRE.ReplaceAllString(text, "Date: {HTTP-DATE}\r")
	}
	text = liveFrame(text)
	if port != "" {
		text = strings.ReplaceAll(text, "127.0.0.1:"+port, "127.0.0.1:{PORT}")
	}
	if token != "" {
		text = strings.ReplaceAll(text, token, "{TOKEN}")
	}
	return text
}

// ---- case model ----

type liveCtx struct {
	t      *testing.T
	root   string
	addr   string // host:port
	port   string
	token  string
	digest string
	// repropose replaces the stored proposal using the side under test.
	repropose func(input string)
}

func (c *liveCtx) form(kv ...string) string {
	var parts []string
	for i := 0; i+1 < len(kv); i += 2 {
		parts = append(parts, kv[i]+"="+urlEncode(kv[i+1]))
	}
	return strings.Join(parts, "&")
}

func urlEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_' || ch == '.' || ch == '~' {
			b.WriteByte(ch)
		} else {
			fmt.Fprintf(&b, "%%%02X", ch)
		}
	}
	return b.String()
}

// good is a complete valid atomic/approve form.
func (c *liveCtx) good(extra ...string) string {
	return c.form(append([]string{"token", c.token, "reviewDigest", c.digest, "approvedBy", "Live Reviewer", "note", "n", "acknowledged", "exact-witness-reviewed"}, extra...)...)
}

func (c *liveCtx) req(method, target, body string, headers ...string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\nHost: %s\r\n", method, target, c.addr)
	for _, h := range headers {
		b.WriteString(h + "\r\n")
	}
	if body != "" {
		fmt.Fprintf(&b, "Content-Type: application/x-www-form-urlencoded\r\nContent-Length: %d\r\n", len(body))
	}
	b.WriteString("Connection: close\r\n\r\n")
	b.WriteString(body)
	return []byte(b.String())
}

func (c *liveCtx) get(target string) []byte { return c.req("GET", target, "") }
func (c *liveCtx) post(target, body string) []byte {
	return c.req("POST", target, body)
}

func (c *liveCtx) chunked(target string, chunks ...string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "POST %s HTTP/1.1\r\nHost: %s\r\nContent-Type: application/x-www-form-urlencoded\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n", target, c.addr)
	for _, ch := range chunks {
		fmt.Fprintf(&b, "%x\r\n%s\r\n", len(ch), ch)
	}
	b.WriteString("0\r\n\r\n")
	return []byte(b.String())
}

// liveStep is one stage of a case. reqs builds the requests (sent sequentially
// unless concurrent); act mutates the store identically for both sides before
// the requests; wantState is the independently expected store state after it.
type liveStep struct {
	label      string
	act        func(c *liveCtx)
	reqs       func(c *liveCtx) [][]byte
	concurrent bool
	wantState  string
}

type liveCase struct {
	name     string
	args     []string // after `witness review p1`
	env      []string
	proposal string
	setup    func(t *testing.T, im liveImpl, root string) // after propose, before start
	steps    []liveStep
}

type liveRecord struct {
	transcript []string
	snapshots  []map[string]string
}

func (c *liveCtx) fetchPage() string {
	raw := liveExchange(c.t, c.addr, c.get("/"))
	return string(raw)
}

func runLiveCase(t *testing.T, tc liveCase, im liveImpl, parent string) liveRecord {
	t.Helper()
	root := liveWorkdir(t, parent)
	proposal := tc.proposal
	if proposal == "" {
		proposal = liveReviewPropose
	}
	im.propose(t, root, proposal)
	if tc.setup != nil {
		tc.setup(t, im, root)
	}
	snapshot := func(label, want string) map[string]string {
		if want == "tampered" {
			// The stored proposal was edited: the independent verifier must
			// REJECT it, and no approval or frozen record may exist.
			if _, err := verifyWitnessStore(root, liveReviewID); err == nil {
				t.Fatalf("%s %s %s: tampered proposal verified clean", tc.name, im.name, label)
			}
			for _, kind := range []string{"approvals", "frozen"} {
				if _, err := os.Lstat(filepath.Join(root, ".faultline", "witnesses", kind, liveReviewID+".json")); !os.IsNotExist(err) {
					t.Fatalf("%s %s %s: %s record exists after a refused action (%v)", tc.name, im.name, label, kind, err)
				}
			}
			snap, err := storeSnapshot(root)
			if err != nil {
				t.Fatal(err)
			}
			return snap
		}
		if want == "-" {
			// The proposal was removed: the independent check is that it is gone.
			if _, err := os.Lstat(filepath.Join(root, ".faultline", "witnesses", "proposals", liveReviewID+".json")); !os.IsNotExist(err) {
				t.Fatalf("%s %s %s: proposal still present (%v)", tc.name, im.name, label, err)
			}
			snap, err := storeSnapshot(root)
			if err != nil {
				t.Fatal(err)
			}
			return snap
		}
		got, err := verifyWitnessStore(root, liveReviewID)
		if err != nil {
			t.Fatalf("%s %s %s: independent store verification: %v", tc.name, im.name, label, err)
		}
		if got != want {
			t.Fatalf("%s %s %s: store state %q, want %q", tc.name, im.name, label, got, want)
		}
		snap, err := storeSnapshot(root)
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
	var rec liveRecord
	rec.snapshots = append(rec.snapshots, snapshot("after propose", "none"))
	args := append([]string{"witness", "review", liveReviewID}, tc.args...)
	ctx, cancel := context.WithTimeout(context.Background(), serveBundleStartupTimeout)
	proc, err := servefixture.StartWithOptions(ctx, im.exe, im.prefix, args, im.environ(root, tc.env...), root, servefixture.StartOptions{
		StartupLines: 2, StartupDescription: "witness review startup lines",
		ValidateStartup: func(out []byte) (string, error) {
			m := liveStartupRE.FindStringSubmatch(string(out))
			if m == nil {
				return "", fmt.Errorf("startup %q", out)
			}
			return m[1], nil
		},
	})
	cancel()
	if err != nil {
		t.Fatalf("%s %s start: %v", tc.name, im.name, err)
	}
	t.Cleanup(func() {
		if err := proc.Dispose(); err != nil {
			t.Errorf("%s %s teardown: %v", tc.name, im.name, err)
		}
	})
	addr := strings.TrimPrefix(proc.URL, "http://")
	c := &liveCtx{t: t, root: root, addr: addr, port: addr[strings.LastIndex(addr, ":")+1:],
		repropose: func(input string) { im.propose(t, root, input) }}
	if _, err := strconv.Atoi(c.port); err != nil {
		t.Fatalf("port %q", c.port)
	}
	// Independent token/digest extraction from this side's own first page.
	first := c.fetchPage()
	if m := liveTokenRE.FindStringSubmatch(first); m != nil {
		c.token = m[1]
	}
	if m := liveDigestRE.FindStringSubmatch(first); m != nil {
		c.digest = m[1]
	}
	rec.transcript = append(rec.transcript, "initial GET /\n"+liveNormalize(t, im.name, []byte(first), c.port, c.token))
	state := "none"
	for _, step := range tc.steps {
		if step.act != nil {
			step.act(c)
		}
		reqs := step.reqs(c)
		responses := make([]string, len(reqs))
		if step.concurrent {
			var wg sync.WaitGroup
			for i, r := range reqs {
				wg.Add(1)
				go func() {
					defer wg.Done()
					responses[i] = liveNormalize(t, im.name, liveExchange(t, c.addr, r), c.port, c.token)
				}()
			}
			wg.Wait()
			sort.Strings(responses) // arrival order is not an observable of a race
		} else {
			for i, r := range reqs {
				responses[i] = liveNormalize(t, im.name, liveExchange(t, c.addr, r), c.port, c.token)
			}
		}
		rec.transcript = append(rec.transcript, "step "+step.label+"\n"+strings.Join(responses, "\n=====\n"))
		if step.wantState != "" {
			state = step.wantState
		}
		rec.snapshots = append(rec.snapshots, snapshot(step.label, state))
	}
	if err := proc.Interrupt(stepContext(t), liveConsoleHelper(t)); err != nil {
		t.Fatalf("%s %s interrupt: %v", tc.name, im.name, err)
	}
	res, err := proc.Wait(stepContext(t))
	if err != nil {
		t.Fatalf("%s %s did not end after SIGINT: %v", tc.name, im.name, err)
	}
	rec.transcript = append(rec.transcript, "exit\n"+liveOutcome(t, im.name, res, c.port))
	return rec
}

func liveOutcome(t *testing.T, side string, res servefixture.ProcessResult, port string) string {
	t.Helper()
	code, sig := "nil", "nil"
	if res.ExitCode != nil {
		code = strconv.Itoa(*res.ExitCode)
	}
	if res.Signal != nil {
		sig = *res.Signal
	}
	return fmt.Sprintf("code=%s signal=%s\nstdout=%q\nstderr=%q", code, sig,
		liveNormalize(t, side, []byte(res.Stdout), port, ""), liveNormalize(t, side, []byte(res.Stderr), port, ""))
}

var (
	liveHelperOnce sync.Once
	liveHelperPath string
)

// liveConsoleHelper is the natively built Windows Ctrl+C emitter (built once);
// empty on POSIX, where real signals are delivered directly.
func liveConsoleHelper(t *testing.T) string {
	if runtime.GOOS != "windows" {
		return ""
	}
	liveHelperOnce.Do(func() { liveHelperPath = serveBundleConsoleHelper(t) })
	return liveHelperPath
}

func compareLiveRecords(t *testing.T, name string, ts, goRec liveRecord) {
	t.Helper()
	if len(ts.transcript) != len(goRec.transcript) {
		t.Fatalf("%s: TS has %d transcript sections, Go %d", name, len(ts.transcript), len(goRec.transcript))
	}
	for i := range ts.transcript {
		if ts.transcript[i] != goRec.transcript[i] {
			t.Errorf("%s: Go differs from TS in %q\n--- TS\n%s\n--- Go\n%s\n%s", name, strings.SplitN(ts.transcript[i], "\n", 2)[0],
				ts.transcript[i], goRec.transcript[i], serveBundleDiff([]byte(goRec.transcript[i]), []byte(ts.transcript[i])))
		}
	}
	for i := range ts.snapshots {
		if d := diffSnapshots(ts.snapshots[i], goRec.snapshots[i]); d != "" {
			t.Errorf("%s: store tree after step %d differs between TS and Go:\n%s", name, i, d)
		}
	}
}

// ---- case table ----

func okStep(label string, wantState string, reqs func(c *liveCtx) [][]byte) liveStep {
	return liveStep{label: label, reqs: reqs, wantState: wantState}
}

func one(f func(c *liveCtx) []byte) func(c *liveCtx) [][]byte {
	return func(c *liveCtx) [][]byte { return [][]byte{f(c)} }
}

func tamperProposal(edit func(string) string) func(c *liveCtx) {
	return func(c *liveCtx) {
		path := filepath.Join(c.root, ".faultline", "witnesses", "proposals", liveReviewID+".json")
		raw, err := os.ReadFile(path)
		if err != nil {
			c.t.Fatal(err)
		}
		edited := edit(string(raw))
		if edited == string(raw) {
			c.t.Fatal("tamper changed nothing")
		}
		if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
			c.t.Fatal(err)
		}
	}
}

// writeLiveDraft writes a draft bound to the stored proposal (read back
// independently of either implementation) into <root>/.faultline/incidents.
func writeLiveDraft(t *testing.T, root string, bind bool, mutate func(*jsjson.Obj), rangeValue jsjson.Value, repo string) {
	writeLiveDraftCommand(t, root, bind, "node witness.mjs", mutate, rangeValue, repo)
}

// writeLiveDraftCommand is writeLiveDraft with the draft command (which the
// binding digest must match for the draft to be valid on its own).
func writeLiveDraftCommand(t *testing.T, root string, bind bool, command string, mutate func(*jsjson.Obj), rangeValue jsjson.Value, repo string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, ".faultline", "witnesses", "proposals", liveReviewID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	stored, err := decodeRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	binding := jsjson.NewObj()
	binding.Set("proposalId", jsjson.MakeString(liveReviewID))
	binding.Set("proposalDigest", jsjson.MakeString(asString(stored["proposalDigest"])))
	binding.Set("incidentPacketDigest", jsjson.MakeString(asString(stored["incidentPacketDigest"])))
	binding.Set("commandDigest", jsjson.MakeString(digestOf([]byte(command))))
	if mutate != nil {
		mutate(binding)
	}
	in := incident.DraftInput{
		ID: liveReviewID, CreatedAt: "2026-07-16T11:00:00.000Z", Repository: repo, Command: command,
	}
	if bind {
		in.Proposal = jsjson.MakeObject(binding)
	}
	if rangeValue.Kind() != jsjson.Undefined {
		in.Range = rangeValue
	} else {
		in.LocalHead = incident.LocalHeadFacts(strings.Repeat("b", 40), []string{strings.Repeat("a", 40)})
	}
	draft, err := incident.CreateDraft(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := incident.WriteDraft(filepath.Join(root, ".faultline", "incidents"), draft); err != nil {
		t.Fatal(err)
	}
}

func liveGitRepo(t *testing.T, root string) (repo, first, second string) {
	t.Helper()
	repo = filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	git := func(date string, args ...string) string {
		cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Fixed Author", "GIT_AUTHOR_EMAIL=a@example.test", "GIT_COMMITTER_NAME=Fixed Author",
			"GIT_COMMITTER_EMAIL=a@example.test", "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("2020-01-02T03:04:05Z", "init", "-q")
	git("2020-01-02T03:04:05Z", "commit", "-q", "--allow-empty", "-m", "first")
	first = git("2020-01-02T03:04:05Z", "rev-parse", "HEAD")
	git("2020-01-03T03:04:05Z", "commit", "-q", "--allow-empty", "-m", "second change")
	second = git("2020-01-03T03:04:05Z", "rev-parse", "HEAD")
	return
}

func witnessReviewLiveCases() []liveCase {
	pageOnly := func(label string) liveStep {
		return okStep(label, "", one(func(c *liveCtx) []byte { return c.get("/") }))
	}
	pad := func(c *liveCtx, total int) string {
		base := c.good()
		need := total - len(base) - len("&pad=")
		return base + "&pad=" + strings.Repeat("x", need)
	}
	return []liveCase{
		{name: "atomic-get", steps: []liveStep{pageOnly("GET /")}},
		{name: "atomic-approve-freeze-then-pages", steps: []liveStep{
			okStep("POST /approve-freeze", "frozen", one(func(c *liveCtx) []byte { return c.post("/approve-freeze", c.good()) })),
			pageOnly("GET after freeze"),
			okStep("POST /approve-freeze again", "frozen", one(func(c *liveCtx) []byte { return c.post("/approve-freeze", c.good()) })),
			okStep("POST /approve after freeze", "frozen", one(func(c *liveCtx) []byte { return c.post("/approve", c.good()) })),
			okStep("POST /freeze after freeze", "frozen", one(func(c *liveCtx) []byte { return c.post("/freeze", c.form("token", c.token, "reviewDigest", c.digest)) })),
		}},
		{name: "two-step-get-and-flow", args: []string{"--two-step"}, steps: []liveStep{
			pageOnly("GET two-step"),
			okStep("POST /approve-freeze is 404", "none", one(func(c *liveCtx) []byte { return c.post("/approve-freeze", c.good()) })),
			okStep("POST /freeze before approve", "none", one(func(c *liveCtx) []byte { return c.post("/freeze", c.form("token", c.token, "reviewDigest", c.digest)) })),
			okStep("POST /approve", "approved", one(func(c *liveCtx) []byte { return c.post("/approve", c.good()) })),
			pageOnly("GET approved"),
			okStep("POST /approve twice", "approved", one(func(c *liveCtx) []byte { return c.post("/approve", c.good()) })),
			okStep("POST /freeze", "frozen", one(func(c *liveCtx) []byte { return c.post("/freeze", c.form("token", c.token, "reviewDigest", c.digest)) })),
			pageOnly("GET frozen"),
		}},
		{name: "two-step-environment", env: []string{"FAULTLINE_WITNESS_TWO_STEP=1"}, steps: []liveStep{
			pageOnly("GET"),
			okStep("POST /approve-freeze is 404", "none", one(func(c *liveCtx) []byte { return c.post("/approve-freeze", c.good()) })),
		}},
		{name: "bad-token-and-acknowledgement", steps: []liveStep{
			okStep("wrong token", "none", one(func(c *liveCtx) []byte { return c.post("/approve-freeze", c.good("token", "00")) })),
			okStep("missing token", "none", one(func(c *liveCtx) []byte {
				return c.post("/approve-freeze", c.form("reviewDigest", c.digest, "approvedBy", "R", "acknowledged", "exact-witness-reviewed"))
			})),
			okStep("empty body", "none", one(func(c *liveCtx) []byte { return c.post("/approve-freeze", "") })),
			okStep("wrong acknowledgement", "none", one(func(c *liveCtx) []byte {
				return c.post("/approve-freeze", c.form("token", c.token, "reviewDigest", c.digest, "approvedBy", "R", "acknowledged", "yes"))
			})),
			okStep("missing acknowledgement /approve", "none", one(func(c *liveCtx) []byte {
				return c.post("/approve", c.form("token", c.token, "reviewDigest", c.digest, "approvedBy", "R"))
			})),
			okStep("wrong digest", "none", one(func(c *liveCtx) []byte {
				return c.post("/approve-freeze", c.good("reviewDigest", "sha256:"+strings.Repeat("0", 64)))
			})),
			okStep("blank reviewer", "none", one(func(c *liveCtx) []byte { return c.post("/approve-freeze", c.good("approvedBy", "   ")) })),
			okStep("reviewer 513 chars", "none", one(func(c *liveCtx) []byte { return c.post("/approve-freeze", c.good("approvedBy", strings.Repeat("r", 513))) })),
			okStep("note 8001 chars", "none", one(func(c *liveCtx) []byte { return c.post("/approve-freeze", c.good("note", strings.Repeat("n", 8001))) })),
			okStep("freeze before approve", "none", one(func(c *liveCtx) []byte {
				return c.post("/freeze", c.form("token", c.token, "reviewDigest", c.digest))
			})),
		}},
		{name: "legacy-direct-actions", steps: []liveStep{
			okStep("POST /freeze unapproved", "none", one(func(c *liveCtx) []byte { return c.post("/freeze", c.form("token", c.token, "reviewDigest", c.digest)) })),
			okStep("POST /approve", "approved", one(func(c *liveCtx) []byte { return c.post("/approve", c.good()) })),
			pageOnly("GET approved (atomic-mode)"),
			okStep("POST /freeze", "frozen", one(func(c *liveCtx) []byte { return c.post("/freeze", c.form("token", c.token, "reviewDigest", c.digest)) })),
		}},
		{name: "duplicate-form-keys", steps: []liveStep{
			okStep("bad token last", "none", one(func(c *liveCtx) []byte {
				return c.post("/approve-freeze", c.good()+"&token=bad")
			})),
			okStep("bad ack first, good last", "none", one(func(c *liveCtx) []byte {
				return c.post("/approve-freeze", "acknowledged=no&"+c.good()+"&acknowledged=no")
			})),
			okStep("bad token first, good token last wins", "frozen", one(func(c *liveCtx) []byte {
				return c.post("/approve-freeze", "token=bad&"+c.good("approvedBy", "Second Name"))
			})),
		}},
		{name: "form-size-chunked", steps: []liveStep{
			okStep("32769-byte form", "none", one(func(c *liveCtx) []byte { return c.post("/approve-freeze", pad(c, 32769)) })),
			okStep("32768-byte chunked oversize+1 (two chunks)", "none", one(func(c *liveCtx) []byte {
				body := pad(c, 32769)
				return c.chunked("/approve-freeze", body[:20000], body[20000:])
			})),
			okStep("32768-byte form", "frozen", one(func(c *liveCtx) []byte { return c.post("/approve-freeze", pad(c, 32768)) })),
		}},
		{name: "form-chunked-valid", steps: []liveStep{
			okStep("chunked valid", "frozen", one(func(c *liveCtx) []byte {
				body := c.good()
				return c.chunked("/approve-freeze", body[:15], body[15:40], body[40:])
			})),
		}},
		{name: "chunked-oversize-exact", steps: []liveStep{
			okStep("chunked 32769", "none", one(func(c *liveCtx) []byte {
				body := pad(c, 32769)
				return c.chunked("/approve-freeze", body)
			})),
			okStep("chunked 32768", "frozen", one(func(c *liveCtx) []byte {
				body := pad(c, 32768)
				return c.chunked("/approve-freeze", body)
			})),
		}},
		{name: "targets-query-slashes-absolute", steps: []liveStep{
			okStep("GET query", "", one(func(c *liveCtx) []byte { return c.get("/?a=b&c") })),
			okStep("GET repeated slashes", "", one(func(c *liveCtx) []byte { return c.get("///") })),
			okStep("GET //?x", "", one(func(c *liveCtx) []byte { return c.get("//?x=1") })),
			okStep("GET absolute http", "", one(func(c *liveCtx) []byte { return c.get("http://" + c.addr + "/") })),
			okStep("GET absolute HTTPS caps", "", one(func(c *liveCtx) []byte { return c.get("HTTPS://example.invalid/") })),
			okStep("GET absolute no path", "", one(func(c *liveCtx) []byte { return c.get("http://" + c.addr) })),
			okStep("GET /index", "", one(func(c *liveCtx) []byte { return c.get("/index") })),
			okStep("GET /approve-freeze", "", one(func(c *liveCtx) []byte { return c.get("/approve-freeze") })),
			okStep("HEAD /", "", one(func(c *liveCtx) []byte { return c.req("HEAD", "/", "") })),
			okStep("PUT /", "", one(func(c *liveCtx) []byte { return c.req("PUT", "/", "") })),
			okStep("POST /", "", one(func(c *liveCtx) []byte { return c.post("/", c.good()) })),
			okStep("backslash and dot segments", "", one(func(c *liveCtx) []byte { return c.get("/%2e/\\approve") })),
			okStep("POST /approve-freeze?q", "none", one(func(c *liveCtx) []byte { return c.post("/approve-freeze?x=1", c.good("token", "bad")) })),
			okStep("POST //approve-freeze", "none", one(func(c *liveCtx) []byte { return c.post("//approve-freeze", c.good("token", "bad")) })),
			okStep("POST absolute /approve-freeze", "frozen", one(func(c *liveCtx) []byte {
				return c.post("http://"+c.addr+"/approve-freeze", c.good())
			})),
		}},
		{name: "concurrent-atomic-posts", steps: []liveStep{{
			label: "8 concurrent POST /approve-freeze", wantState: "frozen", concurrent: true,
			reqs: func(c *liveCtx) [][]byte {
				var out [][]byte
				for range 8 {
					out = append(out, c.post("/approve-freeze", c.good()))
				}
				return out
			},
		}, pageOnly("GET after race")}},
		{name: "concurrent-approve-posts", steps: []liveStep{{
			label: "8 concurrent POST /approve", wantState: "approved", concurrent: true,
			reqs: func(c *liveCtx) [][]byte {
				var out [][]byte
				for range 8 {
					out = append(out, c.post("/approve", c.good()))
				}
				return out
			},
		}}},
		{name: "stale-review-replaced-proposal", steps: []liveStep{
			{label: "replace stored proposal then POST old digest", wantState: "none",
				act: func(c *liveCtx) {
					path := filepath.Join(c.root, ".faultline", "witnesses", "proposals", liveReviewID+".json")
					if err := os.Remove(path); err != nil {
						c.t.Fatal(err)
					}
					// Re-propose with a different command; both sides use their own propose.
					c.repropose(liveReviewReplacement)
				},
				reqs: one(func(c *liveCtx) []byte { return c.post("/approve-freeze", c.good()) })},
			pageOnly("GET after replacement"),
		}},
		{name: "tampered-stored-proposal", steps: []liveStep{
			{label: "tamper command text", wantState: "tampered", act: tamperProposal(func(s string) string { return strings.Replace(s, "node witness.mjs", "node evil.mjs", 1) }),
				reqs: func(c *liveCtx) [][]byte {
					return [][]byte{c.get("/"), c.post("/approve-freeze", c.good()), c.post("/approve", c.good()), c.post("/freeze", c.form("token", c.token, "reviewDigest", c.digest))}
				}},
		}},
		{name: "tampered-unblinded-proposal", steps: []liveStep{
			{label: "add an unblinded member", wantState: "tampered", act: tamperProposal(func(s string) string { return strings.Replace(s, `"proposalId":"p1"`, `"extra":1,"proposalId":"p1"`, 1) }),
				reqs: func(c *liveCtx) [][]byte { return [][]byte{c.get("/"), c.post("/approve-freeze", c.good())} }},
		}},
		{name: "draft-missing-file-and-matching", setup: func(t *testing.T, im liveImpl, root string) {
			if err := os.MkdirAll(filepath.Join(root, ".faultline", "incidents", "drafts"), 0o700); err != nil {
				t.Fatal(err)
			}
		}, steps: []liveStep{pageOnly("GET with empty draft store"),
			{label: "write matching draft then GET", act: func(c *liveCtx) { writeLiveDraft(c.t, c.root, true, nil, jsjson.Value{}, filepath.Join(c.root, "absent-repo")) },
				reqs: func(c *liveCtx) [][]byte {
					return [][]byte{c.get("/"), c.post("/approve-freeze", c.good())}
				}, wantState: "frozen"},
			pageOnly("GET frozen with draft")}},
		{name: "draft-git-range", setup: func(t *testing.T, im liveImpl, root string) {
			repo, first, second := liveGitRepo(t, root)
			rng := jsjson.MustObject("ancestor", jsjson.MakeString(first), "descendant", jsjson.MakeString(second))
			writeLiveDraft(t, root, true, nil, rng, repo)
		}, steps: []liveStep{pageOnly("GET with real git range")}},
		{name: "draft-explicit-store-flag", args: []string{"--draft-store", "alt-drafts"}, setup: func(t *testing.T, im liveImpl, root string) {
			// Only the default store holds a (mismatching) draft: --draft-store must ignore it.
			writeLiveDraftCommand(t, root, true, "node other.mjs", nil, jsjson.Value{}, filepath.Join(root, "absent-repo"))
		}, steps: []liveStep{pageOnly("GET (draft in default store ignored)")}},
		{name: "draft-mismatch-digest", setup: func(t *testing.T, im liveImpl, root string) {
			writeLiveDraft(t, root, true, nil, jsjson.Value{}, filepath.Join(root, "absent-repo"))
		}, steps: []liveStep{
			{label: "mismatching command digest", act: func(c *liveCtx) {
				c.t.Helper()
				removeLiveDraft(c)
				writeLiveDraftCommand(c.t, c.root, true, "node other.mjs", nil, jsjson.Value{}, filepath.Join(c.root, "absent-repo"))
			}, reqs: func(c *liveCtx) [][]byte { return [][]byte{c.get("/"), c.post("/approve-freeze", c.good())} }, wantState: "none"},
			{label: "mismatching proposal digest", act: func(c *liveCtx) {
				removeLiveDraft(c)
				writeLiveDraft(c.t, c.root, true, func(o *jsjson.Obj) { o.Set("proposalDigest", jsjson.MakeString("sha256:"+strings.Repeat("2", 64))) }, jsjson.Value{}, filepath.Join(c.root, "absent-repo"))
			}, reqs: func(c *liveCtx) [][]byte { return [][]byte{c.get("/"), c.post("/approve", c.good())} }, wantState: "none"},
			{label: "draft not bound to a proposal", act: func(c *liveCtx) {
				removeLiveDraft(c)
				writeLiveDraft(c.t, c.root, false, nil, jsjson.Value{}, filepath.Join(c.root, "absent-repo"))
			}, reqs: func(c *liveCtx) [][]byte { return [][]byte{c.get("/")} }, wantState: "none"},
			{label: "malformed draft file", act: func(c *liveCtx) {
				dir := filepath.Join(c.root, ".faultline", "incidents", "drafts")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					c.t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, liveReviewID+".json"), []byte("{not json"), 0o600); err != nil {
					c.t.Fatal(err)
				}
			}, reqs: func(c *liveCtx) [][]byte { return [][]byte{c.get("/"), c.post("/approve-freeze", c.good())} }, wantState: "none"},
		}},
		{name: "unknown-proposal", steps: []liveStep{
			{label: "remove the proposal", act: func(c *liveCtx) {
				_ = os.Remove(filepath.Join(c.root, ".faultline", "witnesses", "proposals", liveReviewID+".json"))
			}, reqs: func(c *liveCtx) [][]byte { return [][]byte{c.get("/"), c.post("/approve-freeze", c.good())} }, wantState: "-"},
		}},
	}
}

func removeLiveDraft(c *liveCtx) {
	_ = os.Remove(filepath.Join(c.root, ".faultline", "incidents", "drafts", liveReviewID+".json"))
}

func TestLiveWitnessReview(t *testing.T) {
	oracle.Skip(t)
	node, cli := pinServeBundleOracle(t)
	impls := liveReviewImpls(t, node, cli)
	for _, tc := range witnessReviewLiveCases() {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			var records [2]liveRecord
			for i, im := range impls {
				records[i] = runLiveCase(t, tc, im, parent)
			}
			compareLiveRecords(t, tc.name, records[0], records[1])
		})
	}
}

// ---- command output: --json, refusals, exit statuses ----

func TestLiveWitnessReviewCLI(t *testing.T) {
	oracle.Skip(t)
	node, cli := pinServeBundleOracle(t)
	impls := liveReviewImpls(t, node, cli)
	type result struct {
		stdout, stderr string
		code           *int
	}
	cases := []struct {
		name string
		args []string
		env  []string
		pre  func(t *testing.T, im liveImpl, root string)
	}{
		{name: "json", args: []string{"--json"}},
		{name: "json invalid port irrelevant", args: []string{"--json", "--port", "bad"}},
		{name: "json before port and draft", args: []string{"--port", "99999", "--json", "--draft-store", "/nonexistent/x"}},
		{name: "json after approve", args: []string{"--json"}, pre: func(t *testing.T, im liveImpl, root string) {
			if _, stderr, code, _ := im.run(t, root, nil, "witness", "approve", liveReviewID, "--approved-by", "Direct Reviewer"); *code != 0 {
				t.Fatalf("approve: %q", stderr)
			}
		}},
		{name: "missing id", args: nil, pre: nil},
		{name: "port bad", args: []string{"--port", "bad"}},
		{name: "port 65536", args: []string{"--port", "65536"}},
		{name: "port negative", args: []string{"--port", "-1"}},
		{name: "port fractional", args: []string{"--port", "1.5"}},
		// --port "" is Number("") = 0, a valid port: both sides start a server, so
		// it is covered by the lifecycle test, not this run-to-exit table.
		{name: "port Infinity", args: []string{"--port", "Infinity"}},
		{name: "port whitespace", args: []string{"--port", " 8 "}},
		{name: "port hex invalid-port-first", args: []string{"--port", "0x10000"}},
		{name: "json unknown proposal", args: []string{"--json"}, pre: func(t *testing.T, im liveImpl, root string) {
			_ = os.Remove(filepath.Join(root, ".faultline", "witnesses", "proposals", liveReviewID+".json"))
		}},
		{name: "json tampered proposal", args: []string{"--json"}, pre: func(t *testing.T, im liveImpl, root string) {
			path := filepath.Join(root, ".faultline", "witnesses", "proposals", liveReviewID+".json")
			raw, _ := os.ReadFile(path)
			_ = os.WriteFile(path, []byte(strings.Replace(string(raw), "node witness.mjs", "node evil.mjs", 1)), 0o600)
		}},
		{name: "json unblinded proposal", args: []string{"--json"}, pre: func(t *testing.T, im liveImpl, root string) {
			path := filepath.Join(root, ".faultline", "witnesses", "proposals", liveReviewID+".json")
			raw, _ := os.ReadFile(path)
			_ = os.WriteFile(path, []byte(strings.Replace(string(raw), `"proposalId":"p1"`, `"extra":1,"proposalId":"p1"`, 1)), 0o600)
		}},
		{name: "json explicit store", args: []string{"--json", "--store", "elsewhere"}},
		{name: "json bad id characters", args: []string{"--json"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			parent := t.TempDir()
			var results [2]result
			var snaps [2]map[string]string
			for i, im := range impls {
				root := liveWorkdir(t, parent)
				im.propose(t, root, liveReviewPropose)
				if c.pre != nil {
					c.pre(t, im, root)
				}
				args := []string{"witness", "review"}
				switch c.name {
				case "missing id":
				case "json bad id characters":
					args = append(args, "../escape")
					args = append(args, c.args...)
				default:
					args = append(args, liveReviewID)
					args = append(args, c.args...)
				}
				stdout, stderr, code, _ := im.run(t, root, c.env, args...)
				results[i] = result{stdout, stderr, code}
				snap, err := storeSnapshot(root)
				if err != nil {
					t.Fatal(err)
				}
				snaps[i] = snap
			}
			if results[0].stdout != results[1].stdout || results[0].stderr != results[1].stderr || *results[0].code != *results[1].code {
				t.Errorf("Go differs from TS\nTS exit=%d\nstdout %q\nstderr %q\nGo exit=%d\nstdout %q\nstderr %q", *results[0].code, results[0].stdout, results[0].stderr, *results[1].code, results[1].stdout, results[1].stderr)
			}
			if d := diffSnapshots(snaps[0], snaps[1]); d != "" {
				t.Errorf("store trees differ after the command:\n%s", d)
			}
			// Independent structural check of --json output, shared by both.
			if len(c.args) > 0 && c.args[0] == "--json" && *results[0].code == 0 {
				var v struct {
					Status string `json:"status"`
				}
				if err := json.Unmarshal([]byte(results[0].stdout), &v); err != nil || v.Status != "LOCAL_READ_ONLY_REVIEW" || !strings.HasSuffix(results[0].stdout, "}\n") {
					t.Errorf("--json output is not the expected document: %v %q", err, results[0].stdout)
				}
			}
		})
	}
}

// ---- lifecycle: SIGINT, forced fallback, second SIGINT, SIGTERM ----

// holdReview opens a connection that is accepted and answered (a complete 404
// response) but whose request body stays incomplete, so the server is draining.
func holdReview(ctx context.Context, p *servefixture.Started) error {
	conn, err := p.Dial(ctx)
	if err != nil {
		return err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if _, err := io.WriteString(conn, "POST / HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Length: 10\r\n\r\nabc"); err != nil {
		return err
	}
	res, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "POST"})
	if err != nil {
		return fmt.Errorf("no complete response on the held connection: %w", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 404 || string(body) != "Not found\n" {
		return fmt.Errorf("held connection answered %d %q", res.StatusCode, body)
	}
	_ = conn.SetDeadline(time.Time{})
	return nil
}

func TestLiveWitnessReviewLifecycle(t *testing.T) {
	oracle.Skip(t)
	node, cli := pinServeBundleOracle(t)
	impls := liveReviewImpls(t, node, cli)
	helper := liveConsoleHelper(t)
	start := func(t *testing.T, im liveImpl, parent string, args ...string) (*servefixture.Started, string) {
		root := liveWorkdir(t, parent)
		im.propose(t, root, liveReviewPropose)
		ctx, cancel := context.WithTimeout(context.Background(), serveBundleStartupTimeout)
		defer cancel()
		proc, err := servefixture.StartWithOptions(ctx, im.exe, im.prefix, append([]string{"witness", "review", liveReviewID}, args...), im.environ(root), root,
			servefixture.StartOptions{StartupLines: 2, StartupDescription: "witness review startup lines",
				ValidateStartup: func(out []byte) (string, error) {
					m := liveStartupRE.FindStringSubmatch(string(out))
					if m == nil {
						return "", fmt.Errorf("startup %q", out)
					}
					return m[1], nil
				}})
		if err != nil {
			t.Fatalf("%s start: %v", im.name, err)
		}
		t.Cleanup(func() {
			if err := proc.Dispose(); err != nil {
				t.Errorf("%s teardown: %v", im.name, err)
			}
		})
		// Readiness barrier: the startup line is written one statement BEFORE
		// the frozen CLI registers process.once("SIGINT"), so a signal sent at
		// once can find the default action in place (observed: the first run
		// of this test had TS die of SIGINT). A served page proves the event
		// loop has passed that registration.
		if _, body, err := fetchServeBundlePage(stepContext(t), proc); err != nil || !bytes.Contains(body, []byte("local witness review")) {
			t.Fatalf("%s not ready: %v", im.name, err)
		}
		return proc, root
	}
	finish := func(t *testing.T, im liveImpl, proc *servefixture.Started) (string, servefixture.ProcessResult) {
		res, err := proc.Wait(stepContext(t))
		if err != nil {
			t.Fatalf("%s did not end: %v", im.name, err)
		}
		port := proc.URL[strings.LastIndex(proc.URL, ":")+1:]
		return liveOutcome(t, im.name, res, port), res
	}
	compare := func(t *testing.T, outs [2]string) {
		if outs[0] != outs[1] {
			t.Errorf("%s: Go differs from TS\n--- TS\n%s\n--- Go\n%s", runtime.GOOS, outs[0], outs[1])
		}
	}
	each := func(t *testing.T, run func(t *testing.T, im liveImpl, parent string) string) {
		parent := t.TempDir()
		var outs [2]string
		for i, im := range impls {
			outs[i] = run(t, im, parent)
		}
		compare(t, outs)
	}
	interrupt := func(t *testing.T, proc *servefixture.Started) {
		if err := proc.Interrupt(stepContext(t), helper); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("sigint atomic exits 0", func(t *testing.T) {
		each(t, func(t *testing.T, im liveImpl, parent string) string {
			proc, _ := start(t, im, parent)
			interrupt(t, proc)
			text, res := finish(t, im, proc)
			if res.ExitCode == nil || *res.ExitCode != 0 || res.Signal != nil {
				t.Errorf("%s: code=%v signal=%v, want exit 0", im.name, res.ExitCode, res.Signal)
			}
			return text
		})
	})
	t.Run("sigint two-step exits 0", func(t *testing.T) {
		each(t, func(t *testing.T, im liveImpl, parent string) string {
			proc, _ := start(t, im, parent, "--two-step")
			interrupt(t, proc)
			text, res := finish(t, im, proc)
			if res.ExitCode == nil || *res.ExitCode != 0 || res.Signal != nil {
				t.Errorf("%s: code=%v signal=%v, want exit 0", im.name, res.ExitCode, res.Signal)
			}
			return text
		})
	})
	t.Run("sigint after approval keeps records", func(t *testing.T) {
		each(t, func(t *testing.T, im liveImpl, parent string) string {
			proc, _ := start(t, im, parent)
			root := proc.Cmd.Dir
			addr := strings.TrimPrefix(proc.URL, "http://")
			c := &liveCtx{t: t, root: root, addr: addr}
			page := string(liveExchange(t, addr, c.get("/")))
			c.token, c.digest = liveTokenRE.FindStringSubmatch(page)[1], liveDigestRE.FindStringSubmatch(page)[1]
			if r := string(liveExchange(t, addr, c.post("/approve-freeze", c.good()))); !strings.HasPrefix(r, "HTTP/1.1 200 ") {
				t.Fatalf("%s approve: %s", im.name, clipText([]byte(r)))
			}
			interrupt(t, proc)
			text, res := finish(t, im, proc)
			if res.ExitCode == nil || *res.ExitCode != 0 {
				t.Errorf("%s: code=%v signal=%v", im.name, res.ExitCode, res.Signal)
			}
			if state, err := verifyWitnessStore(root, liveReviewID); err != nil || state != "frozen" {
				t.Errorf("%s: store state %q err %v, want frozen", im.name, state, err)
			}
			return text
		})
	})
	t.Run("forced tty fallback exits 1 after sigint", func(t *testing.T) {
		each(t, func(t *testing.T, im liveImpl, parent string) string {
			proc, _ := start(t, im, parent, "--tty")
			interrupt(t, proc)
			text, res := finish(t, im, proc)
			if res.ExitCode == nil || *res.ExitCode != 1 || res.Signal != nil {
				t.Errorf("%s: code=%v signal=%v, want exit 1", im.name, res.ExitCode, res.Signal)
			}
			if !strings.Contains(res.Stderr, "Refusing non-interactive approval. Open the local review URL: "+proc.URL+" FaultLine never auto-approves under yes| piping.\n") {
				t.Errorf("%s stderr %q", im.name, res.Stderr)
			}
			if _, err := os.Stat(filepath.Join(proc.Cmd.Dir, ".faultline", "witnesses", "approvals", liveReviewID+".json")); err == nil {
				t.Errorf("%s: forced fallback wrote an approval", im.name)
			}
			return text
		})
	})
	t.Run("held second sigint takes the default action", func(t *testing.T) {
		each(t, func(t *testing.T, im liveImpl, parent string) string {
			proc, _ := start(t, im, parent)
			if err := holdReview(stepContext(t), proc); err != nil {
				t.Fatalf("%s hold: %v", im.name, err)
			}
			interrupt(t, proc)
			if err := waitListenerClosed(stepContext(t), proc); err != nil {
				t.Fatalf("%s listener: %v", im.name, err)
			}
			if proc.Exited() {
				t.Fatalf("%s exited after the first interrupt while a client was held", im.name)
			}
			interrupt(t, proc)
			text, res := finish(t, im, proc)
			requireSecondInterruptDeath(t, res)
			return text
		})
	})
	t.Run("default terminate", func(t *testing.T) {
		each(t, func(t *testing.T, im liveImpl, parent string) string {
			proc, _ := start(t, im, parent)
			if err := proc.Terminate(stepContext(t)); err != nil {
				t.Fatal(err)
			}
			text, res := finish(t, im, proc)
			// On Windows this is TerminateProcess, a forced stop whose raw
			// status is only compared between implementations; it is never
			// evidence of graceful SIGTERM handling.
			requireTerminateStatus(t, res)
			return text
		})
	})
	t.Run("held then terminate", func(t *testing.T) {
		each(t, func(t *testing.T, im liveImpl, parent string) string {
			proc, _ := start(t, im, parent)
			if err := holdReview(stepContext(t), proc); err != nil {
				t.Fatalf("%s hold: %v", im.name, err)
			}
			interrupt(t, proc)
			if err := waitListenerClosed(stepContext(t), proc); err != nil {
				t.Fatalf("%s listener: %v", im.name, err)
			}
			if err := proc.Terminate(stepContext(t)); err != nil {
				t.Fatal(err)
			}
			text, res := finish(t, im, proc)
			requireTerminateStatus(t, res)
			return text
		})
	})
}

// TestLiveWitnessReviewPipedYes starts both sides with a never-ending `yes`
// stream on stdin and forced --tty. It must refuse (stdin is not a terminal),
// start the loopback fallback, and write no record. The processes are then
// stopped with a forced kill, which is not graceful-shutdown evidence; their
// graceful exit is covered above.
func TestLiveWitnessReviewPipedYes(t *testing.T) {
	oracle.Skip(t)
	node, cli := pinServeBundleOracle(t)
	impls := liveReviewImpls(t, node, cli)
	for _, args := range [][]string{{"--tty"}, {}} {
		t.Run(strings.Join(append([]string{"yes"}, args...), " "), func(t *testing.T) {
			parent := t.TempDir()
			var texts [2]string
			for i, im := range impls {
				root := liveWorkdir(t, parent)
				im.propose(t, root, liveReviewPropose)
				ctx, cancel := context.WithTimeout(context.Background(), serveBundleStartupTimeout)
				cmd := exec.CommandContext(ctx, im.exe, append(append(slicesClone(im.prefix), "witness", "review", liveReviewID), args...)...)
				cmd.Dir, cmd.Env = root, im.environ(root)
				cmd.Stdin = &yesReader{}
				var out, errOut reviewSyncBuffer
				cmd.Stdout, cmd.Stderr = &out, &errOut
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				deadline := time.Now().Add(20 * time.Second)
				for !strings.HasSuffix(out.String(), "\n") || strings.Count(out.String(), "\n") < 2 {
					if time.Now().After(deadline) {
						_ = cmd.Process.Kill()
						t.Fatalf("%s: no startup: stdout=%q stderr=%q", im.name, out.String(), errOut.String())
					}
					time.Sleep(20 * time.Millisecond)
				}
				time.Sleep(300 * time.Millisecond) // let any (wrongly) accepted answer be written
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				cancel()
				port := liveStartupRE.FindStringSubmatch(out.String())[1]
				port = port[strings.LastIndex(port, ":")+1:]
				state, err := verifyWitnessStore(root, liveReviewID)
				if err != nil || state != "none" {
					t.Fatalf("%s: store state %q err %v, piped yes must not approve", im.name, state, err)
				}
				texts[i] = fmt.Sprintf("stdout=%q\nstderr=%q",
					liveNormalize(t, im.name, []byte(out.String()), port, ""), liveNormalize(t, im.name, []byte(errOut.String()), port, ""))
			}
			if texts[0] != texts[1] {
				t.Errorf("Go differs from TS\n--- TS\n%s\n--- Go\n%s", texts[0], texts[1])
			}
		})
	}
}

// reviewSyncBuffer is a bytes.Buffer a test may read while a child writes it.
// lockedBuffer lives in a unix-only file, so the cross-platform live file
// needs its own (found by the windows cross-compilation of the test binary).
type reviewSyncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *reviewSyncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *reviewSyncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type yesReader struct{}

func (*yesReader) Read(p []byte) (int, error) {
	n := 0
	for n+4 <= len(p) {
		n += copy(p[n:], "yes\n")
	}
	if n == 0 {
		return 0, io.ErrShortBuffer
	}
	return n, nil
}
