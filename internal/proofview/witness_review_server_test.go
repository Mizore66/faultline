package proofview

import (
	"bufio"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/incident"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/witness"
)

const (
	reviewTestToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	reviewTestID    = "refund-1"
	ackValue        = "exact-witness-reviewed"
	reviewCSP       = "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"
)

type reviewFixture struct {
	t      *testing.T
	store  string
	server *WitnessReviewServer
	review jsjson.Value
	twoStp bool
}

func reviewProposal(t *testing.T, id string) jsjson.Value {
	t.Helper()
	v, err := jsjson.Parse(`{"proposalId":"` + id + `","proposedAt":"2026-01-02T03:04:05.000Z","incidentPacket":{"symptom":"refund currency","ciLog":"Expected USD; received EUR.",
		"repositoryLanguage":"JavaScript","repositorySummary":"A Node service that completes refunds."},
		"witness":{"behavior":"b","command":"node witness.mjs",
		"overlays":[{"path":"witness.mjs","bytesBase64":"Y29uc29sZS5sb2coMSk="}],
		"policy":{"network":"disabled","credentials":"redacted","timeoutSeconds":30}}}`)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func newReviewFixture(t *testing.T, twoStep bool, draftStore string) *reviewFixture {
	t.Helper()
	store := t.TempDir()
	if _, err := witness.Propose(store, reviewProposal(t, reviewTestID)); err != nil {
		t.Fatal(err)
	}
	review, err := witness.OpenWitnessReview(store, reviewTestID)
	if err != nil {
		t.Fatal(err)
	}
	server, err := startWitnessReviewServer(WitnessReviewServerOptions{
		StoreDirectory: store, ProposalID: reviewTestID, DraftStore: draftStore, TwoStep: twoStep,
	}, reviewTestToken, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	return &reviewFixture{t: t, store: store, server: server, review: review, twoStp: twoStep}
}

type reviewReply struct {
	status int
	header http.Header
	body   string
}

func reviewDo(t *testing.T, method, target, body string) reviewReply {
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
	return reviewReply{res.StatusCode, res.Header, string(b)}
}

// decodeReply parses a raw HTTP response (undoing chunked framing).
func decodeReply(t *testing.T, raw string) reviewReply {
	t.Helper()
	res, err := http.ReadResponse(bufio.NewReader(strings.NewReader(raw)), nil)
	if err != nil {
		t.Fatalf("reply %.200q: %v", raw, err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return reviewReply{res.StatusCode, res.Header, string(b)}
}

func (f *reviewFixture) digest() string { return f.review.Get("reviewDigest").Str() }

func (f *reviewFixture) form(extra ...string) string {
	v := url.Values{}
	v.Set("token", reviewTestToken)
	v.Set("reviewDigest", f.digest())
	v.Set("approvedBy", "Reviewer One")
	v.Set("acknowledged", ackValue)
	for i := 0; i+1 < len(extra); i += 2 {
		if extra[i+1] == "\x00delete" {
			v.Del(extra[i])
		} else {
			v.Set(extra[i], extra[i+1])
		}
	}
	return v.Encode()
}

func (f *reviewFixture) post(path, body string) reviewReply {
	f.t.Helper()
	return reviewDo(f.t, "POST", f.server.URL()+path, body)
}

func (f *reviewFixture) records() (approvals, frozen int) {
	count := func(dir string) int {
		entries, err := os.ReadDir(filepath.Join(f.store, dir))
		if err != nil {
			return 0
		}
		return len(entries)
	}
	return count("approvals"), count("frozen")
}

func (f *reviewFixture) requireRecords(approvals, frozen int) {
	f.t.Helper()
	a, z := f.records()
	if a != approvals || z != frozen {
		f.t.Fatalf("records approvals=%d frozen=%d, want %d/%d", a, z, approvals, frozen)
	}
}

func requireHTMLHeaders(t *testing.T, r reviewReply) {
	t.Helper()
	for k, want := range map[string]string{
		"Content-Type":            "text/html; charset=utf-8",
		"Cache-Control":           "no-store",
		"Content-Security-Policy": reviewCSP,
		"Referrer-Policy":         "no-referrer",
		"X-Content-Type-Options":  "nosniff",
	} {
		if got := r.header.Values(k); len(got) != 1 || got[0] != want {
			t.Fatalf("%s = %q, want %q", k, got, want)
		}
	}
}

func requireNotFound(t *testing.T, r reviewReply) {
	t.Helper()
	if r.status != 404 || r.body != "Not found\n" || r.header.Get("Content-Type") != "text/plain; charset=utf-8" || r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("not found: %d %q %v", r.status, r.body, r.header)
	}
	if r.header.Get("Content-Security-Policy") != "" {
		t.Fatal("404 carries a CSP")
	}
}

func requireRefusal(t *testing.T, r reviewReply, status int, message string) {
	t.Helper()
	if r.status != status {
		t.Fatalf("status %d, want %d\n%s", r.status, status, r.body)
	}
	requireHTMLHeaders(t, r)
	if want := renderWitnessReviewFailurePage(message); r.body != want {
		t.Fatalf("refusal page differs for %q:\n%s", message, r.body)
	}
}

// The frozen server calls writeHead with lowercase header names for every HTML
// response and for the 404, and Node writes them to the wire exactly as given
// (only Connection, Date, Transfer-Encoding and Keep-Alive are its own,
// capitalized). A Go canonicalized name is a wire difference found by the
// frozen-TypeScript comparison; this pins the exact header-name bytes.
func TestWitnessReviewServerHeaderNames(t *testing.T) {
	f := newReviewFixture(t, false, "")
	host := strings.TrimPrefix(f.server.URL(), "http://")
	body := f.form("token", "bad")
	htmlHeaders := []string{
		"cache-control: no-store", "content-security-policy: " + reviewCSP, "content-type: text/html; charset=utf-8",
		"referrer-policy: no-referrer", "x-content-type-options: nosniff",
	}
	for _, c := range []struct {
		name, request string
		want          []string
	}{
		{"html page", "GET / HTTP/1.1\r\nHost: " + host + "\r\nConnection: close\r\n\r\n", htmlHeaders},
		{"refusal page", "POST /approve-freeze HTTP/1.1\r\nHost: " + host + "\r\nConnection: close\r\nContent-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body, htmlHeaders},
		{"not found", "GET /nope HTTP/1.1\r\nHost: " + host + "\r\nConnection: close\r\n\r\n", []string{
			"cache-control: no-store", "content-type: text/plain; charset=utf-8",
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			head, _, _ := strings.Cut(rawExchange(t, f.server.URL(), c.request), "\r\n\r\n")
			var got []string
			for _, line := range strings.Split(head, "\r\n")[1:] {
				name, _, _ := strings.Cut(line, ":")
				switch name {
				case "Connection", "Date", "Transfer-Encoding", "Keep-Alive":
					continue // Node's own capitalized framing headers
				}
				got = append(got, line)
			}
			sort.Strings(got)
			if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
				t.Fatalf("header lines\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(c.want, "\n"))
			}
		})
	}
}

func TestWitnessReviewServerHTTP(t *testing.T) {
	t.Run("get serves the current page", func(t *testing.T) {
		f := newReviewFixture(t, false, "")
		for _, path := range []string{"/", "/?x=1", "//", "///?a=b"} {
			r := reviewDo(t, "GET", f.server.URL()+path, "")
			if r.status != 200 {
				t.Fatalf("%s: %d", path, r.status)
			}
			requireHTMLHeaders(t, r)
			if want := RenderWitnessReviewHTML(f.review, nil, reviewTestToken, false); r.body != want {
				t.Fatalf("%s: page differs", path)
			}
		}
		f.requireRecords(0, 0)
	})
	t.Run("two-step page", func(t *testing.T) {
		f := newReviewFixture(t, true, "")
		r := reviewDo(t, "GET", f.server.URL()+"/", "")
		if want := RenderWitnessReviewHTML(f.review, nil, reviewTestToken, true); r.status != 200 || r.body != want {
			t.Fatal("two-step page differs")
		}
	})
	t.Run("token failures are 403", func(t *testing.T) {
		f := newReviewFixture(t, false, "")
		msg := "This local review action did not include the current review token."
		requireRefusal(t, f.post("/approve-freeze", f.form("token", "\x00delete")), 403, msg)
		requireRefusal(t, f.post("/approve-freeze", f.form("token", strings.Repeat("0", 64))), 403, msg)
		requireRefusal(t, f.post("/approve-freeze", f.form("token", "short")), 403, msg)
		requireRefusal(t, f.post("/approve-freeze", ""), 403, msg)
		// Equal UTF-16 length, different UTF-8 length: timingSafeEqual throws.
		requireRefusal(t, f.post("/approve-freeze", f.form("token", strings.Repeat("é", 64))), 409, "Input buffers must have the same byte length")
		f.requireRecords(0, 0)
	})
	t.Run("acknowledgement", func(t *testing.T) {
		f := newReviewFixture(t, false, "")
		requireRefusal(t, f.post("/approve-freeze", f.form("acknowledged", "yes")), 409,
			"Human approval requires acknowledgement that the exact command and overlay were reviewed.")
		requireRefusal(t, f.post("/approve-freeze", f.form("acknowledged", "\x00delete")), 409,
			"Human approval requires acknowledgement that the exact command and overlay were reviewed.")
		requireRefusal(t, f.post("/approve", f.form("acknowledged", "no")), 409,
			"Human approval requires acknowledgement that the exact command and overlay bytes were reviewed.")
		f.requireRecords(0, 0)
	})
	t.Run("wrong review digest", func(t *testing.T) {
		f := newReviewFixture(t, false, "")
		requireRefusal(t, f.post("/approve-freeze", f.form("reviewDigest", "sha256:"+strings.Repeat("0", 64))), 409,
			"Explicit review confirmation does not match the review snapshot")
		requireRefusal(t, f.post("/approve-freeze", f.form("approvedBy", "  ")), 409, f.approvalIssue("  "))
		f.requireRecords(0, 0)
	})
	t.Run("atomic approve and freeze", func(t *testing.T) {
		f := newReviewFixture(t, false, "")
		r := f.post("/approve-freeze", f.form("approvedBy", "First", "note", "looks right")+"&approvedBy=Re%C3%A9+Two&note=")
		if r.status != 200 {
			t.Fatalf("%d %s", r.status, r.body)
		}
		requireHTMLHeaders(t, r)
		if !strings.Contains(r.body, `<p class="review-notice" role="status">Approved and frozen as a write-once record.</p>`) || !strings.Contains(r.body, "<b>FROZEN</b>") {
			t.Fatal("success page")
		}
		f.requireRecords(1, 1)
		approval, err := witness.ReadApproval(f.store, reviewTestID)
		if err != nil {
			t.Fatal(err)
		}
		if approval.Get("approvedBy").Str() != "Re\u00e9 Two" || approval.Get("note").Kind() != jsjson.Undefined {
			t.Fatalf("last key must win: %s", jsjson.Stringify(approval))
		}
		frozen, err := witness.ReadFrozen(f.store, reviewTestID)
		if err != nil || frozen.Get("approval", "approvalDigest").Str() != approval.Get("approvalDigest").Str() {
			t.Fatalf("frozen record: %v", err)
		}
		again := f.post("/approve-freeze", f.form())
		if again.status != 409 || !strings.Contains(again.body, "Immutable witness lock record already exists") {
			t.Fatalf("second approval: %d", again.status)
		}
		f.requireRecords(1, 1)
	})
	t.Run("atomic mode accepts legacy direct actions", func(t *testing.T) {
		f := newReviewFixture(t, false, "")
		r := f.post("/approve", f.form("note", "n"))
		if r.status != 200 || !strings.Contains(r.body, "Human approval was recorded. Freezing still requires its own explicit action.") {
			t.Fatalf("approve: %d", r.status)
		}
		f.requireRecords(1, 0)
		r = f.post("/freeze", url.Values{"token": {reviewTestToken}, "reviewDigest": {f.digest()}}.Encode())
		if r.status != 200 || !strings.Contains(r.body, "The approved witness is now frozen as a write-once record.") {
			t.Fatalf("freeze: %d %s", r.status, r.body)
		}
		f.requireRecords(1, 1)
	})
	t.Run("freeze without approval", func(t *testing.T) {
		f := newReviewFixture(t, false, "")
		r := f.post("/freeze", f.form())
		requireRefusal(t, r, 409, "Cannot freeze an unapproved proposal: "+reviewTestID)
		f.requireRecords(0, 0)
	})
	t.Run("two-step", func(t *testing.T) {
		f := newReviewFixture(t, true, "")
		requireNotFound(t, f.post("/approve-freeze", f.form()))
		f.requireRecords(0, 0)
		if r := f.post("/approve", f.form()); r.status != 200 {
			t.Fatalf("approve %d", r.status)
		}
		f.requireRecords(1, 0)
		page := reviewDo(t, "GET", f.server.URL()+"/", "")
		if !strings.Contains(page.body, `action="/freeze"`) || strings.Contains(page.body, `action="/approve"`) {
			t.Fatal("approved two-step page must offer only freeze")
		}
		if r := f.post("/freeze", f.form()); r.status != 200 {
			t.Fatalf("freeze %d", r.status)
		}
		f.requireRecords(1, 1)
	})
	t.Run("unknown routes", func(t *testing.T) {
		f := newReviewFixture(t, false, "")
		requireNotFound(t, reviewDo(t, "GET", f.server.URL()+"/approve", ""))
		requireNotFound(t, reviewDo(t, "GET", f.server.URL()+"/other", ""))
		requireNotFound(t, f.post("/", f.form()))
		requireNotFound(t, f.post("/other", f.form()))
		requireNotFound(t, reviewDo(t, "PUT", f.server.URL()+"/approve-freeze", f.form()))
		if r := reviewDo(t, "HEAD", f.server.URL()+"/", ""); r.status != 404 || r.body != "" {
			t.Fatalf("HEAD: %d %q", r.status, r.body)
		}
		f.requireRecords(0, 0)
	})
	t.Run("review path handling", func(t *testing.T) {
		f := newReviewFixture(t, false, "")
		// Backslashes and dot segments are not normalized for review.
		requireNotFound(t, reviewDo(t, "POST", f.server.URL()+`/x/../approve-freeze`, f.form()))
		requireNotFound(t, reviewDo(t, "POST", f.server.URL()+`/%5Capprove-freeze`, f.form()))
		f.requireRecords(0, 0)
		if r := reviewDo(t, "POST", f.server.URL()+"//approve-freeze?x=1", f.form()); r.status != 200 {
			t.Fatalf("repeated slashes: %d %s", r.status, r.body)
		}
		f.requireRecords(1, 1)
	})
	t.Run("absolute target", func(t *testing.T) {
		f := newReviewFixture(t, false, "")
		body := f.form()
		reply := rawExchange(t, f.server.URL(), "POST http://example.test//approve-freeze?q=1 HTTP/1.1\r\nHost: x\r\nConnection: close\r\nContent-Length: "+strconv.Itoa(len(body))+"\r\n\r\n"+body)
		if !strings.HasPrefix(reply, "HTTP/1.1 200 ") {
			t.Fatalf("absolute target: %s", reply)
		}
		f.requireRecords(1, 1)
	})
	t.Run("chunked form", func(t *testing.T) {
		f := newReviewFixture(t, false, "")
		body := f.form()
		var b strings.Builder
		for i := 0; i < len(body); i += 10 {
			part := body[i:min(i+10, len(body))]
			b.WriteString(strconv.FormatInt(int64(len(part)), 16) + "\r\n" + part + "\r\n")
		}
		b.WriteString("0\r\n\r\n")
		reply := rawExchange(t, f.server.URL(), "POST /approve-freeze HTTP/1.1\r\nHost: x\r\nConnection: close\r\nTransfer-Encoding: chunked\r\n\r\n"+b.String())
		if !strings.HasPrefix(reply, "HTTP/1.1 200 ") {
			t.Fatalf("chunked: %s", reply)
		}
		f.requireRecords(1, 1)
	})
	t.Run("form size limit", func(t *testing.T) {
		f := newReviewFixture(t, false, "")
		base := f.form()
		over := base + "&pad=" + strings.Repeat("a", 32769-len(base)-5)
		if len(over) != 32769 {
			t.Fatalf("test body is %d bytes", len(over))
		}
		requireRefusal(t, f.post("/approve-freeze", over), 409, "review form is too large")
		// A chunked body of 32769 bytes is refused the same way.
		var b strings.Builder
		b.WriteString(strconv.FormatInt(int64(len(over)), 16) + "\r\n" + over + "\r\n0\r\n\r\n")
		reply := rawExchange(t, f.server.URL(), "POST /approve-freeze HTTP/1.1\r\nHost: x\r\nConnection: close\r\nTransfer-Encoding: chunked\r\n\r\n"+b.String())
		requireRefusal(t, decodeReply(t, reply), 409, "review form is too large")
		f.requireRecords(0, 0)
		exact := over[:32768]
		if r := f.post("/approve-freeze", exact); r.status != 200 {
			t.Fatalf("32768 bytes: %d", r.status)
		}
		f.requireRecords(1, 1)
	})
	t.Run("stale review between open and post", func(t *testing.T) {
		f := newReviewFixture(t, false, "")
		f.server.setBetweenOpenAndAction(func() {
			path := filepath.Join(f.store, "proposals", reviewTestID+".json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Error(err)
				return
			}
			p, err := jsjson.Parse(string(raw))
			if err != nil {
				t.Error(err)
				return
			}
			o := jsjson.NewObj()
			for _, k := range p.Obj().Keys() {
				if k != "proposalDigest" {
					o.Set(k, p.Obj().Field(k))
				}
			}
			o.Set("proposedAt", jsjson.MakeString("2026-02-02T03:04:05.000Z"))
			o.Set("proposalDigest", jsjson.MakeString(jsexc.Must(canonical.DigestJSON(jsjson.MakeObject(o)))))
			text := jsexc.Must(canonical.CanonicalJSON(jsjson.MakeObject(o)))
			if err := os.WriteFile(path, []byte(text+"\n"), 0o600); err != nil {
				t.Error(err)
			}
		})
		requireRefusal(t, f.post("/approve-freeze", f.form()), 409,
			"Witness review is stale because the stored proposal no longer exactly matches the reviewed snapshot")
		f.requireRecords(0, 0)
	})
	t.Run("proposal integrity failure on get", func(t *testing.T) {
		f := newReviewFixture(t, false, "")
		path := filepath.Join(f.store, "proposals", reviewTestID+".json")
		raw, _ := os.ReadFile(path)
		if err := os.WriteFile(path, []byte(strings.Replace(string(raw), "node witness.mjs", "node evil.mjs", 1)), 0o600); err != nil {
			t.Fatal(err)
		}
		r := reviewDo(t, "GET", f.server.URL()+"/", "")
		if r.status != 409 || !strings.Contains(r.body, "Witness review refused: proposal integrity verification failed: ") {
			t.Fatalf("%d %s", r.status, r.body)
		}
		requireHTMLHeaders(t, r)
	})
	t.Run("production credential", func(t *testing.T) {
		store := t.TempDir()
		if _, err := witness.Propose(store, reviewProposal(t, reviewTestID)); err != nil {
			t.Fatal(err)
		}
		tokens := map[string]bool{}
		for i := 0; i < 2; i++ {
			s, err := StartWitnessReviewServer(WitnessReviewServerOptions{StoreDirectory: store, ProposalID: reviewTestID}, 0)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(s.URL(), "http://127.0.0.1:") {
				t.Fatalf("url %s", s.URL())
			}
			r := reviewDo(t, "GET", s.URL()+"/", "")
			m := regexp.MustCompile(`name="token" value="([0-9a-f]{64})"`).FindStringSubmatch(r.body)
			if m == nil || tokens[m[1]] {
				t.Fatalf("token %v", m)
			}
			tokens[m[1]] = true
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
	t.Run("invalid port", func(t *testing.T) {
		if _, err := StartWitnessReviewServer(WitnessReviewServerOptions{StoreDirectory: t.TempDir(), ProposalID: reviewTestID}, 70000); err == nil ||
			!strings.HasPrefix(err.Error(), "options.port should be >= 0 and < 65536.") {
			t.Fatalf("port error %v", err)
		}
	})
}

// approvalIssue is the message the strict approval schema gives for a
// blank reviewer, taken from the store rather than hard-coded.
func (f *reviewFixture) approvalIssue(by string) string {
	_, _, err := witness.ApproveAndFreezeWitnessReview(f.store, f.review, jsjson.MustObject(
		"reviewDigest", f.review.Get("reviewDigest"), "approvedBy", jsjson.MakeString(by)))
	if err == nil {
		f.t.Fatal("blank reviewer accepted")
	}
	return jsexc.Message(err)
}

func TestWitnessReviewServerDraftBinding(t *testing.T) {
	proposal := reviewProposal(t, reviewTestID)
	stored, err := witness.Propose(t.TempDir(), proposal)
	if err != nil {
		t.Fatal(err)
	}
	bind := func(command string, mutate func(*jsjson.Obj)) jsjson.Value {
		binding := jsjson.NewObj()
		binding.Set("proposalId", stored.Get("proposalId"))
		binding.Set("proposalDigest", stored.Get("proposalDigest"))
		binding.Set("incidentPacketDigest", stored.Get("incidentPacketDigest"))
		binding.Set("commandDigest", jsjson.MakeString(gitproof.CommandDigest(command)))
		if mutate != nil {
			mutate(binding)
		}
		return jsjson.MakeObject(binding)
	}
	writeDraft := func(store string, in incident.DraftInput) jsjson.Value {
		draft, err := incident.CreateDraft(in)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := incident.WriteDraft(store, draft); err != nil {
			t.Fatal(err)
		}
		return draft
	}
	input := func(command string, proposal jsjson.Value) incident.DraftInput {
		return incident.DraftInput{
			ID: reviewTestID, CreatedAt: "2026-07-16T14:00:00.000Z", Repository: filepath.Join(t.TempDir(), "absent-repo"),
			Command: command, LocalHead: incident.LocalHeadFacts(strings.Repeat("b", 40), []string{strings.Repeat("a", 40)}),
			Proposal: proposal,
		}
	}
	const dsMismatch = "Incident draft does not match the stored witness proposal; do not approve or freeze either record."

	t.Run("missing draft is unlinked", func(t *testing.T) {
		for _, ds := range []string{t.TempDir(), filepath.Join(t.TempDir(), "absent")} {
			f := newReviewFixture(t, false, ds)
			r := reviewDo(t, "GET", f.server.URL()+"/", "")
			if r.status != 200 || r.body != RenderWitnessReviewHTML(f.review, nil, reviewTestToken, false) || strings.Contains(r.body, "Draft digest") {
				t.Fatalf("unlinked: %d", r.status)
			}
			if r := f.post("/approve-freeze", f.form()); r.status != 200 {
				t.Fatalf("post: %d", r.status)
			}
		}
	})
	t.Run("matching draft is linked", func(t *testing.T) {
		ds := t.TempDir()
		draft := writeDraft(ds, input("node witness.mjs", bind("node witness.mjs", nil)))
		f := newReviewFixture(t, false, ds)
		r := reviewDo(t, "GET", f.server.URL()+"/", "")
		if r.status != 200 || r.body == RenderWitnessReviewHTML(f.review, nil, reviewTestToken, false) || !strings.Contains(r.body, draft.Get("draftDigest").Str()) {
			t.Fatalf("linked page: %d", r.status)
		}
		if p := f.post("/approve-freeze", f.form()); p.status != 200 || !strings.Contains(p.body, draft.Get("draftDigest").Str()) {
			t.Fatalf("post: %d", p.status)
		}
		f.requireRecords(1, 1)
	})
	refused := func(t *testing.T, ds, message string) {
		t.Helper()
		f := newReviewFixture(t, false, ds)
		get := reviewDo(t, "GET", f.server.URL()+"/", "")
		if message == "" {
			if get.status != 409 || !strings.Contains(get.body, "Do not freeze") {
				t.Fatalf("get: %d", get.status)
			}
		} else {
			requireRefusal(t, get, 409, message)
		}
		post := f.post("/approve-freeze", f.form())
		if post.status != 409 || post.body != get.body {
			t.Fatalf("post: %d", post.status)
		}
		// The draft is read before the acknowledgement is checked.
		if p := f.post("/approve", f.form("acknowledged", "no")); p.body != get.body {
			t.Fatal("draft check must precede acknowledgement")
		}
		f.requireRecords(0, 0)
	}
	t.Run("mismatched command digest", func(t *testing.T) {
		ds := t.TempDir()
		writeDraft(ds, input("node other.mjs", bind("node other.mjs", nil)))
		refused(t, ds, dsMismatch)
	})
	t.Run("mismatched proposal digest", func(t *testing.T) {
		ds := t.TempDir()
		writeDraft(ds, input("node witness.mjs", bind("node witness.mjs", func(o *jsjson.Obj) {
			o.Set("proposalDigest", jsjson.MakeString("sha256:"+strings.Repeat("0", 64)))
		})))
		refused(t, ds, dsMismatch)
	})
	t.Run("mismatched packet digest", func(t *testing.T) {
		ds := t.TempDir()
		writeDraft(ds, input("node witness.mjs", bind("node witness.mjs", func(o *jsjson.Obj) {
			o.Set("incidentPacketDigest", jsjson.MakeString("sha256:"+strings.Repeat("1", 64)))
		})))
		refused(t, ds, dsMismatch)
	})
	t.Run("draft not bound to a proposal", func(t *testing.T) {
		ds := t.TempDir()
		writeDraft(ds, input("node witness.mjs", jsjson.Value{}))
		refused(t, ds, "Incident draft is not bound to a proposed witness; do not review or freeze it through this workbench.")
	})
	t.Run("malformed draft", func(t *testing.T) {
		ds := t.TempDir()
		if err := os.MkdirAll(filepath.Join(ds, "drafts"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ds, "drafts", reviewTestID+".json"), []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		refused(t, ds, "")
	})
	t.Run("draft that fails verification", func(t *testing.T) {
		ds := t.TempDir()
		path := writeDraftPath(t, ds, writeDraft(ds, input("node witness.mjs", bind("node witness.mjs", nil))))
		raw, _ := os.ReadFile(path)
		if err := os.WriteFile(path, []byte(strings.Replace(string(raw), "2026-07-16T14:00:00.000Z", "2026-07-16T14:00:01.000Z", 1)), 0o600); err != nil {
			t.Fatal(err)
		}
		refused(t, ds, "")
	})
}

func writeDraftPath(t *testing.T, store string, draft jsjson.Value) string {
	t.Helper()
	return filepath.Join(store, "drafts", draft.Get("draftId").Str()+".json")
}

func TestWitnessReviewServerConcurrentMutation(t *testing.T) {
	for _, path := range []string{"/approve-freeze", "/approve"} {
		t.Run(path, func(t *testing.T) {
			f := newReviewFixture(t, false, "")
			const posts = 8
			statuses := make([]int, posts)
			bodies := make([]string, posts)
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i := 0; i < posts; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					body := f.form()
					reply, err := rawExchangeErr(f.server.URL(), "POST "+path+" HTTP/1.1\r\nHost: x\r\nConnection: close\r\nContent-Length: "+strconv.Itoa(len(body))+"\r\n\r\n"+body)
					if err != nil {
						t.Error(err)
						return
					}
					res, err := http.ReadResponse(bufio.NewReader(strings.NewReader(reply)), nil)
					if err != nil {
						t.Errorf("reply %.200q: %v", reply, err)
						return
					}
					b, err := io.ReadAll(res.Body)
					res.Body.Close()
					if err != nil {
						t.Errorf("body: %v", err)
						return
					}
					statuses[i], bodies[i] = res.StatusCode, string(b)
				}()
			}
			close(start)
			wg.Wait()
			ok := 0
			for i, s := range statuses {
				switch s {
				case 200:
					ok++
				case 409:
					if !strings.Contains(bodies[i], "Immutable witness lock record already exists") {
						t.Errorf("loser %d: %s", i, bodies[i])
					}
				default:
					t.Errorf("status %d", s)
				}
			}
			if ok != 1 {
				t.Fatalf("%d successful mutations in %v", ok, statuses)
			}
			wantFrozen := 1
			if path == "/approve" {
				wantFrozen = 0
			}
			f.requireRecords(1, wantFrozen)
			if _, err := witness.ReadApproval(f.store, reviewTestID); err != nil {
				t.Fatal(err)
			}
			if wantFrozen == 1 {
				if _, err := witness.ReadFrozen(f.store, reviewTestID); err != nil {
					t.Fatal(err)
				}
			}
			entries, _ := os.ReadDir(filepath.Join(f.store, "approvals"))
			for _, e := range entries {
				if !strings.HasSuffix(e.Name(), ".json") {
					t.Fatalf("stray file %s", e.Name())
				}
			}
		})
	}
}
