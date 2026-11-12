package proofview

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"sync"

	"github.com/Mizore66/faultline/internal/incident"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/witness"
)

// WitnessReviewServerOptions configures the local witness review server.
type WitnessReviewServerOptions struct {
	StoreDirectory, ProposalID, DraftStore string
	// TwoStep selects separate Approve and Freeze forms.
	TwoStep bool
}

// WitnessReviewServer is the local browser review server.
type WitnessReviewServer struct {
	server  *Server
	options WitnessReviewServerOptions
	token   string

	// mu serializes the handler section after the body is received, as
	// Node's synchronous read/verify/write sequence is.
	mu sync.Mutex
	// betweenOpenAndAction runs after a mutation opened the review and
	// before it acts (test hook; nil in production).
	betweenOpenAndAction func()
}

// URL is the server's http://127.0.0.1:<port> address.
func (s *WitnessReviewServer) URL() string { return s.server.URL() }

// Close stops the server and waits for its connections to drain.
func (s *WitnessReviewServer) Close() error { return s.server.Close() }

func (s *WitnessReviewServer) setBetweenOpenAndAction(f func()) {
	s.mu.Lock()
	s.betweenOpenAndAction = f
	s.mu.Unlock()
}

// StartWitnessReviewServer serves the review page for one stored proposal
// on 127.0.0.1 with a fresh random form credential.
func StartWitnessReviewServer(options WitnessReviewServerOptions, port float64) (*WitnessReviewServer, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, err
	}
	return startWitnessReviewServer(options, hex.EncodeToString(raw[:]), port)
}

func startWitnessReviewServer(options WitnessReviewServerOptions, credential string, port float64) (*WitnessReviewServer, error) {
	s := &WitnessReviewServer{options: options, token: credential}
	server, err := startNodeServerWithOptions(port, nodeServerOptions{captureFormBody: true, reviewPaths: true},
		func(string) http.HandlerFunc { return s.handle })
	if err != nil {
		return nil, err
	}
	s.server = server
	return s, nil
}

const (
	reviewHTMLCSP = "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"
	ackField      = "exact-witness-reviewed"
)

// setWireHeader writes a header under the exact lowercase name the frozen
// server passes to writeHead, which Node puts on the wire as given. Header.Set
// would canonicalize it. A nil canonical Content-Type entry stops net/http
// sniffing and adding a second, canonical one.
func setWireHeader(h http.Header, name, value string) {
	h[name] = []string{value}
	if name == "content-type" {
		h["Content-Type"] = nil
	}
}

func writeReviewHTML(w http.ResponseWriter, r *http.Request, status int, html string) {
	h := w.Header()
	setWireHeader(h, "content-type", "text/html; charset=utf-8")
	setWireHeader(h, "cache-control", "no-store")
	setWireHeader(h, "content-security-policy", reviewHTMLCSP)
	setWireHeader(h, "referrer-policy", "no-referrer")
	setWireHeader(h, "x-content-type-options", "nosniff")
	nodeHeaders(h, r)
	writeNodeBody(w, r, status, jsstr.ToUTF8(html))
}

func writeReviewNotFound(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	setWireHeader(h, "content-type", "text/plain; charset=utf-8")
	setWireHeader(h, "cache-control", "no-store")
	nodeHeaders(h, r)
	writeNodeBody(w, r, http.StatusNotFound, "Not found\n")
}

// reviewState is tryReadState.
type reviewState struct {
	review jsjson.Value
	draft  *jsjson.Value
	page   witnessReviewPageState
}

func isMissingRecord(err error) bool {
	var ne *nodefs.Error
	return errors.As(err, &ne) && ne.Code == "ENOENT"
}

func throwUnlessMissing(err error) {
	if err != nil && !isMissingRecord(err) {
		jsexc.Throw(err)
	}
}

// readBoundDraft is readBoundDraft: a missing draft is no linked draft, any
// other failure or mismatch is fatal.
func readBoundDraft(draftStore string, review jsjson.Value) *jsjson.Value {
	if draftStore == "" {
		return nil
	}
	proposal := review.Get("proposal")
	id := proposal.Get("proposalId").Str()
	if !nodefs.Exists(nodefs.Join(draftStore, "drafts", id+".json")) {
		return nil
	}
	draft, err := incident.ReadDraft(draftStore, id)
	if err != nil {
		if isMissingRecord(err) {
			return nil
		}
		jsexc.Throw(err)
	}
	if draft.Get("review", "witnessState").Str() != "PROPOSED" {
		reviewThrow("Incident draft is not bound to a proposed witness; do not review or freeze it through this workbench.")
	}
	binding := draft.Get("review", "proposal")
	if binding.Get("proposalId").Str() != id ||
		binding.Get("proposalDigest").Str() != proposal.Get("proposalDigest").Str() ||
		binding.Get("incidentPacketDigest").Str() != proposal.Get("incidentPacketDigest").Str() ||
		binding.Get("commandDigest").Str() != proposal.Get("witness", "commandDigest").Str() {
		reviewThrow("Incident draft does not match the stored witness proposal; do not approve or freeze either record.")
	}
	return &draft
}

type reviewMessage string

func (m reviewMessage) Error() string { return string(m) }

func reviewThrow(message string) { jsexc.Throw(reviewMessage(message)) }

func (s *WitnessReviewServer) readState() reviewState {
	review, err := witness.OpenWitnessReview(s.options.StoreDirectory, s.options.ProposalID)
	if err != nil {
		jsexc.Throw(err)
	}
	state := reviewState{review: review, draft: readBoundDraft(s.options.DraftStore, review)}
	if approved, err := witness.ReadApproval(s.options.StoreDirectory, s.options.ProposalID); err == nil {
		state.page.Approved = &approved
	} else {
		throwUnlessMissing(err)
	}
	if frozen, err := witness.ReadFrozen(s.options.StoreDirectory, s.options.ProposalID); err == nil {
		state.page.Frozen = &frozen
	} else {
		throwUnlessMissing(err)
	}
	return state
}

func (s *WitnessReviewServer) render(state reviewState, notice string) string {
	page := state.page
	if notice != "" {
		page.Notice = &notice
	}
	return renderWitnessReviewPage(state.review, state.draft, s.token, s.options.TwoStep, page)
}

// try runs a handler section whose throws become the 409 refusal page.
func (s *WitnessReviewServer) try(w http.ResponseWriter, r *http.Request, fn func()) {
	if err := jsexc.Try(fn); err != nil {
		writeReviewHTML(w, r, http.StatusConflict, renderWitnessReviewFailurePage(jsexc.Message(err)))
	}
}

func (s *WitnessReviewServer) handle(w http.ResponseWriter, r *http.Request) {
	path := nodeRequestPath(r)
	if r.Method == "GET" && path == "/" {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.try(w, r, func() {
			state := s.readState()
			writeReviewHTML(w, r, http.StatusOK, s.render(state, ""))
		})
		return
	}
	allowed := path == "/approve" || path == "/freeze"
	if !s.options.TwoStep {
		allowed = allowed || path == "/approve-freeze"
	}
	if r.Method != "POST" || !allowed {
		writeReviewNotFound(w, r)
		return
	}
	body, err := nodeFormBody(r)
	if err != nil {
		writeReviewHTML(w, r, http.StatusConflict, renderWitnessReviewFailurePage(err.Error()))
		return
	}
	form := parseForm(body)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.try(w, r, func() { s.act(w, r, path, form) })
}

// parseForm is URLSearchParams over the UTF-8 decoded body, last key wins.
func parseForm(body []byte) map[string]string {
	form := map[string]string{}
	text := jsstr.DecodeUTF8(body)
	for text != "" {
		var pair string
		pair, text = cutByte(text, '&')
		if pair == "" {
			continue
		}
		key, value := cutByte(pair, '=')
		form[formUnescape(key)] = formUnescape(value)
	}
	return form
}

func cutByte(s string, b byte) (string, string) {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}

// formUnescape is the application/x-www-form-urlencoded decoder: "+" is a
// space and an invalid escape stays literal.
func formUnescape(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '+':
			out = append(out, ' ')
		case c == '%' && i+2 < len(s) && isHexByte(s[i+1]) && isHexByte(s[i+2]):
			out = append(out, unhex(s[i+1])<<4|unhex(s[i+2]))
			i += 2
		default:
			out = append(out, c)
		}
	}
	return jsstr.DecodeUTF8(out)
}

func isHexByte(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case c >= 'a':
		return c - 'a' + 10
	case c >= 'A':
		return c - 'A' + 10
	}
	return c - '0'
}

func (s *WitnessReviewServer) sameToken(actual string, present bool) bool {
	if !present {
		return false
	}
	if len(actual) != len(s.token) {
		// Node compares UTF-16 lengths first, then throws when the UTF-8
		// byte lengths differ.
		if jsstr.UTF16Len(actual) == len(s.token) {
			reviewThrow("Input buffers must have the same byte length")
		}
		return false
	}
	return subtle.ConstantTimeCompare([]byte(s.token), []byte(actual)) == 1
}

func (s *WitnessReviewServer) act(w http.ResponseWriter, r *http.Request, path string, form map[string]string) {
	token, hasToken := form["token"]
	if !s.sameToken(token, hasToken) {
		writeReviewHTML(w, r, http.StatusForbidden, renderWitnessReviewFailurePage("This local review action did not include the current review token."))
		return
	}
	review := s.readState().review
	if s.betweenOpenAndAction != nil {
		s.betweenOpenAndAction()
	}
	input := jsjson.NewObj()
	input.Set("reviewDigest", jsjson.MakeString(form["reviewDigest"]))
	switch path {
	case "/approve-freeze", "/approve":
		if form["acknowledged"] != ackField {
			message := "Human approval requires acknowledgement that the exact command and overlay were reviewed."
			if path == "/approve" {
				message = "Human approval requires acknowledgement that the exact command and overlay bytes were reviewed."
			}
			writeReviewHTML(w, r, http.StatusConflict, renderWitnessReviewFailurePage(message))
			return
		}
		input.Set("approvedBy", jsjson.MakeString(form["approvedBy"]))
		if note := form["note"]; note != "" {
			input.Set("note", jsjson.MakeString(note))
		}
		if path == "/approve-freeze" {
			_, _, err := witness.ApproveAndFreezeWitnessReview(s.options.StoreDirectory, review, jsjson.MakeObject(input))
			if err != nil {
				jsexc.Throw(err)
			}
			writeReviewHTML(w, r, http.StatusOK, s.render(s.readState(), "Approved and frozen as a write-once record."))
			return
		}
		if _, err := witness.ApproveWitnessReview(s.options.StoreDirectory, review, jsjson.MakeObject(input)); err != nil {
			jsexc.Throw(err)
		}
		writeReviewHTML(w, r, http.StatusOK, s.render(s.readState(), "Human approval was recorded. Freezing still requires its own explicit action."))
	default:
		if _, err := witness.FreezeWitnessReview(s.options.StoreDirectory, review, jsjson.MakeObject(input)); err != nil {
			jsexc.Throw(err)
		}
		writeReviewHTML(w, r, http.StatusOK, s.render(s.readState(), "The approved witness is now frozen as a write-once record."))
	}
}
