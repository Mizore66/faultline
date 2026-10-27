package repair

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/adler32"
	"hash/crc32"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodeproc"
	"github.com/Mizore66/faultline/internal/redaction"
)

// openAIResponsesURL is the endpoint src/repair-brief.ts:208 posts to.
const openAIResponsesURL = "https://api.openai.com/v1/responses"

// Port of src/repair-brief.ts:213-220 (joined by " ").
var briefInstructions = strings.Join([]string{
	"You are preparing an evidence-bounded repair brief after deterministic localization.",
	"Every recommendation must cite only supplied evidence IDs.",
	"Treat every recommendation as inferred guidance, not an executed verdict or proof of model intent, a unique cause, or a culprit.",
	"Prioritize hard enforcement (test, type/schema rule, static check, runtime assertion, CI guard) before soft repository guidance.",
	"Do not propose a new witness, invent run results, name an agent, or state a confidence percentage.",
	"Return only the requested structured object.",
}, " ")

// BriefTextFormat is the request body's `text` member,
// src/repair-brief.ts:222 `{ format: { type: "json_schema", name, strict,
// schema } }`. It is the only place the json_schema wrapper is built.
func BriefTextFormat() jsjson.Value {
	return jsjson.MustObject("format", jsjson.MustObject(
		"type", str("json_schema"),
		"name", str("faultline_repair_brief"),
		"strict", jsjson.MakeBool(true),
		"schema", BriefJSONSchema(),
	))
}

// BriefRequestBody is the POST body of src/repair-brief.ts:211-223:
// JSON.stringify({ model, instructions, input: JSON.stringify(safePacket), text }).
func BriefRequestBody(model string, safePacket jsjson.Value) string {
	return jsjson.Stringify(jsjson.MustObject(
		"model", str(model),
		"instructions", str(briefInstructions),
		"input", str(jsjson.Stringify(safePacket)),
		"text", BriefTextFormat(),
	))
}

// The undici defaults fetch runs with: a 10 s connect timeout and 300 s
// headers and body (idle) timeouts (undici Agent/Client defaults).
const (
	connectTimeout = 10 * time.Second
	headersTimeout = 300 * time.Second
	bodyTimeout    = 300 * time.Second
)

// responsesClient is built once. Like Node's fetch it ignores proxy
// variables, speaks HTTP/1.1 only, never decompresses on its own (the
// Accept-Encoding header is explicit) and follows redirects in fetch's
// way (followRedirects), not net/http's.
var responsesClient = func() *http.Client {
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           (&net.Dialer{Timeout: connectTimeout}).DialContext,
			TLSHandshakeTimeout:   connectTimeout,
			ResponseHeaderTimeout: headersTimeout,
			DisableCompression:    true,
			Protocols:             &protocols,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}()

// errFetchFailed and errTerminated are the TypeError messages Node's fetch gives
// for a network error before the response (fetch rejects) and for a body
// that fails while it is read (response.text()/json() reject).
var (
	errFetchFailed = errors.New("fetch failed")
	errTerminated  = errors.New("terminated")
)

// header is one request header in the order fetch sends it.
type header struct{ name, value string }

// fetchRequest is the part of undici's request that redirects change.
type fetchRequest struct {
	method  string
	url     *url.URL
	headers []header
	body    []byte // nil once a redirect turned the request into a GET
}

// isHTTPWhitespace is undici's isHTTPWhiteSpaceCharCode.
func isHTTPWhitespace(u uint16) bool { return u == '\t' || u == '\n' || u == '\r' || u == ' ' }

// headerValue converts one fetch `headers` record value the way undici's
// Headers does: webidl ByteString (a unit above 255 throws), then
// headerValueNormalize and isValidHeaderValue. The result is the latin1
// bytes sent on the wire.
func headerValue(value string) (string, error) {
	units := jsstr.ToUTF16(value)
	for i, u := range units {
		if u > 255 {
			return "", errors.New("Cannot convert argument to a ByteString because the character at index " + strconv.Itoa(i) +
				" has a value of " + strconv.Itoa(int(u)) + " which is greater than 255.")
		}
	}
	i, j := 0, len(units)
	for j > i && isHTTPWhitespace(units[j-1]) {
		j--
	}
	for j > i && isHTTPWhitespace(units[i]) {
		i++
	}
	units = units[i:j]
	for _, u := range units {
		if u == '\n' || u == '\r' || u == 0 {
			return "", errors.New(jsexc.Concat(`Headers.append: "`, jsstr.FromUTF16(units), `" is an invalid header value.`))
		}
	}
	b := make([]byte, len(units))
	for k, u := range units {
		b[k] = byte(u)
	}
	return string(b), nil
}

// dispatchableHeader is undici's processHeader check (headerCharRegex
// /[^\t\x20-\x7e\x80-\xff]/); a failure rejects fetch with "fetch failed".
func dispatchableHeader(value string) bool {
	for i := 0; i < len(value); i++ {
		if c := value[i]; c != '\t' && (c < 0x20 || c == 0x7f) {
			return false
		}
	}
	return true
}

// The headers undici adds to every fetch request (fetching() and
// httpNetworkOrCacheFetch), in its order. Node sends accept-encoding
// "br, gzip, deflate" to an https URL; Go has no Brotli decoder, so it
// offers "gzip, deflate" (KNOWN_DIFFERENCES).
var undiciHeaders = []header{
	{"accept", "*/*"},
	{"accept-language", "*"},
	{"sec-fetch-mode", "cors"},
	{"user-agent", "node"},
	{"accept-encoding", "gzip, deflate"},
}

// fetchResponse is a response fetch has resolved with.
type fetchResponse struct {
	status int
	body   []byte // decoded, before UTF-8 decoding
	err    error  // what reading the body rejects with (errTerminated)
}

// postJSON is `await fetch(url, { method: "POST", headers, body })` with
// redirect: "follow". Header conversion errors are the TypeErrors fetch
// throws; network errors are "fetch failed".
func postJSON(rawURL, authorization string, body []byte) (*fetchResponse, error) {
	auth, err := headerValue(authorization)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errFetchFailed
	}
	req := &fetchRequest{method: "POST", url: u, body: body,
		headers: []header{{"Authorization", auth}, {"Content-Type", "application/json"}}}
	for _, h := range req.headers {
		if !dispatchableHeader(h.value) {
			return nil, errFetchFailed
		}
	}
	for redirects := 0; ; redirects++ {
		resp, err := send(req)
		if err != nil {
			return nil, errFetchFailed
		}
		next, follow, err := redirectTarget(req, resp, redirects)
		if err != nil {
			resp.Body.Close()
			return nil, errFetchFailed
		}
		if !follow {
			return readResponse(req, resp)
		}
		resp.Body.Close()
		req = next
	}
}

// send makes one request hop. The context outlives the call: the body
// timer cancels it when the body stalls for bodyTimeout.
func send(req *fetchRequest) (*http.Response, error) {
	ctx, cancel := context.WithCancel(context.Background())
	var body io.Reader
	if req.body != nil {
		body = bytes.NewReader(req.body)
	}
	hreq, err := http.NewRequestWithContext(ctx, req.method, req.url.String(), body)
	if err != nil {
		cancel()
		return nil, err
	}
	for _, h := range append(append([]header{}, req.headers...), undiciHeaders...) {
		hreq.Header[http.CanonicalHeaderKey(h.name)] = []string{h.value}
	}
	resp, err := responsesClient.Do(hreq)
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &idleBody{ReadCloser: resp.Body, timer: time.AfterFunc(bodyTimeout, cancel), cancel: cancel}
	return resp, nil
}

// idleBody is undici's bodyTimeout: the body fails once no data arrives for
// bodyTimeout.
type idleBody struct {
	io.ReadCloser
	timer  *time.Timer
	cancel context.CancelFunc
}

func (b *idleBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.timer.Reset(bodyTimeout)
	return n, err
}

func (b *idleBody) Close() error {
	b.timer.Stop()
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// Port of undici's redirectStatusSet and nullBodyStatus.
func isRedirectStatus(status int) bool {
	return status == 301 || status == 302 || status == 303 || status == 307 || status == 308
}

func isNullBodyStatus(status int) bool {
	return status == 101 || status == 204 || status == 205 || status == 304
}

// headerJoin is undici's headersList.get(name): every value joined by ", ",
// present or not.
func headerJoin(h http.Header, name string) (string, bool) {
	values, ok := h[http.CanonicalHeaderKey(name)]
	return strings.Join(values, ", "), ok
}

// origin is the WHATWG (scheme, host, port) tuple: a lowercase host and no
// default port.
func origin(u *url.URL) string {
	port := u.Port()
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	return u.Scheme + "://" + strings.ToLower(u.Hostname()) + ":" + port
}

// redirectTarget ports undici's httpRedirectFetch (fetch/index.js) for
// redirect: "follow". follow is false when the response is returned as is.
func redirectTarget(req *fetchRequest, resp *http.Response, redirects int) (*fetchRequest, bool, error) {
	if !isRedirectStatus(resp.StatusCode) {
		return nil, false, nil
	}
	location, ok := headerJoin(resp.Header, "location")
	if !ok {
		return nil, false, nil
	}
	target, err := req.url.Parse(location)
	if err != nil {
		return nil, false, err
	}
	if target.Fragment == "" {
		target.Fragment, target.RawFragment = req.url.Fragment, req.url.RawFragment
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, false, errors.New("URL scheme must be a HTTP(S) scheme")
	}
	if redirects == 20 {
		return nil, false, errors.New("redirect count exceeded")
	}
	if target.User != nil {
		// Node's request has no origin, so a Location with credentials is
		// never same-origin with it.
		return nil, false, errors.New(`cross origin not allowed for request mode "cors"`)
	}
	next := &fetchRequest{method: req.method, url: target, body: req.body, headers: req.headers}
	if ((resp.StatusCode == 301 || resp.StatusCode == 302) && req.method == "POST") ||
		(resp.StatusCode == 303 && req.method != "GET" && req.method != "HEAD") {
		next.method, next.body = "GET", nil
		next.headers = withoutHeaders(next.headers, "content-encoding", "content-language", "content-location", "content-type", "content-length")
	}
	if origin(req.url) != origin(target) {
		next.headers = withoutHeaders(next.headers, "authorization", "proxy-authorization", "cookie", "host")
	}
	return next, true, nil
}

func withoutHeaders(headers []header, names ...string) []header {
	var out []header
	for _, h := range headers {
		drop := false
		for _, n := range names {
			drop = drop || strings.EqualFold(h.name, n)
		}
		if !drop {
			out = append(out, h)
		}
	}
	return out
}

// readResponse reads and decodes the final response's body, as undici's
// onHeaders/onData do before response.text() sees it.
func readResponse(req *fetchRequest, resp *http.Response) (*fetchResponse, error) {
	defer resp.Body.Close()
	out := &fetchResponse{status: resp.StatusCode}
	if req.method == "HEAD" || isNullBodyStatus(resp.StatusCode) {
		return out, nil
	}
	var codings []string
	if encoding, ok := headerJoin(resp.Header, "content-encoding"); ok && encoding != "" {
		// toLowerCase on a latin1 string; only ASCII codings are known.
		codings = strings.Split(asciiLower(encoding), ",")
	}
	if len(codings) > 5 {
		// "too many content-encodings in response" aborts before fetch resolves.
		return nil, errFetchFailed
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		out.err = errTerminated
		return out, nil
	}
	var decoders []func([]byte) ([]byte, error)
	for i := len(codings) - 1; i >= 0; i-- {
		switch jsTrimLatin1(codings[i]) {
		case "gzip", "x-gzip":
			decoders = append(decoders, gunzip)
		case "deflate":
			decoders = append(decoders, inflate)
		case "br":
			decoders = append(decoders, func([]byte) ([]byte, error) { return nil, errors.New("brotli is not supported") })
		default:
			decoders = nil
			i = -1
		}
	}
	for _, decode := range decoders {
		if raw, err = decode(raw); err != nil {
			out.err = errTerminated
			return out, nil
		}
	}
	out.body = raw
	return out, nil
}

// asciiLower lowercases A-Z and leaves every other byte alone.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// jsTrimLatin1 is String.prototype.trim on a latin1-decoded header value.
func jsTrimLatin1(s string) string {
	isSpace := func(c byte) bool { return (c >= 0x09 && c <= 0x0d) || c == ' ' || c == 0xa0 }
	i, j := 0, len(s)
	for i < j && isSpace(s[i]) {
		i++
	}
	for j > i && isSpace(s[j-1]) {
		j--
	}
	return s[i:j]
}

var errZlib = errors.New("zlib error")

// gunzip is Node's zlib.createGunzip with finishFlush Z_SYNC_FLUSH
// (node_zlib.cc): a member, then another while input remains whose next
// byte is not 0x00. Input that ends early yields what was decoded.
func gunzip(in []byte) ([]byte, error) {
	var out []byte
	for {
		member, rest, end, err := gunzipMember(in)
		out = append(out, member...)
		if err != nil {
			return nil, err
		}
		if !end || len(rest) == 0 || rest[0] == 0 {
			return out, nil
		}
		in = rest
	}
}

// gunzipMember decodes one gzip member in zlib's order (inflate.c HEAD
// through LENGTH): each header check once its bytes have arrived, the
// deflate data, then the CRC-32 and the length. end is false when the
// input ran out first.
func gunzipMember(in []byte) (out, rest []byte, end bool, err error) {
	pos := 0
	take := func(n int) ([]byte, bool) {
		if len(in)-pos < n {
			return nil, false
		}
		pos += n
		return in[pos-n : pos], true
	}
	head, ok := take(2)
	if !ok {
		return nil, nil, false, nil
	}
	if head[0] != 0x1f || head[1] != 0x8b {
		return nil, nil, false, errZlib // incorrect header check
	}
	method, ok := take(2)
	if !ok {
		return nil, nil, false, nil
	}
	if method[0] != 8 {
		return nil, nil, false, errZlib // unknown compression method
	}
	flags := method[1]
	if flags&0xe0 != 0 {
		return nil, nil, false, errZlib // unknown header flags set
	}
	if _, ok := take(6); !ok { // MTIME, XFL, OS
		return nil, nil, false, nil
	}
	if flags&0x04 != 0 { // FEXTRA
		n, ok := take(2)
		if !ok {
			return nil, nil, false, nil
		}
		if _, ok := take(int(binary.LittleEndian.Uint16(n))); !ok {
			return nil, nil, false, nil
		}
	}
	for _, flag := range []byte{0x08, 0x10} { // FNAME, FCOMMENT
		if flags&flag == 0 {
			continue
		}
		for {
			b, ok := take(1)
			if !ok {
				return nil, nil, false, nil
			}
			if b[0] == 0 {
				break
			}
		}
	}
	if flags&0x02 != 0 { // FHCRC
		want := uint16(crc32.ChecksumIEEE(in[:pos]))
		b, ok := take(2)
		if !ok {
			return nil, nil, false, nil
		}
		if binary.LittleEndian.Uint16(b) != want {
			return nil, nil, false, errZlib // header crc mismatch
		}
	}
	s, done, err := inflateRaw(in[pos:])
	if err != nil {
		return nil, nil, false, errZlib
	}
	if !done {
		return s.out, nil, false, nil
	}
	trailer := s.rest()
	if len(trailer) < 4 {
		return s.out, nil, false, nil
	}
	if binary.LittleEndian.Uint32(trailer[:4]) != crc32.ChecksumIEEE(s.out) {
		return nil, nil, false, errZlib // incorrect data check
	}
	if len(trailer) < 8 {
		return s.out, nil, false, nil
	}
	if binary.LittleEndian.Uint32(trailer[4:8]) != uint32(len(s.out)) {
		return nil, nil, false, errZlib // incorrect length check
	}
	return s.out, trailer[8:], true, nil
}

// inflate is undici's InflateStream: a zlib stream (zlib.createInflate)
// when the first byte's low nibble is 8, otherwise raw deflate
// (createInflateRaw). Bytes after the stream are ignored.
func inflate(in []byte) ([]byte, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if in[0]&15 != 8 {
		s, _, err := inflateRaw(in)
		if err != nil {
			return nil, errZlib
		}
		return s.out, nil
	}
	// inflate.c HEAD: the check, the method and the window size, then
	// FDICT's DICTID (Node has no dictionary: an error once it is read).
	if len(in) < 2 {
		return nil, nil
	}
	if (uint(in[0])<<8|uint(in[1]))%31 != 0 || in[0]&15 != 8 || in[0]>>4+8 > 15 {
		return nil, errZlib
	}
	if in[1]&0x20 != 0 {
		if len(in) < 6 {
			return nil, nil
		}
		return nil, errZlib // Missing dictionary
	}
	s, done, err := inflateRaw(in[2:])
	if err != nil {
		return nil, errZlib
	}
	if !done || len(s.rest()) < 4 {
		return s.out, nil
	}
	if binary.BigEndian.Uint32(s.rest()[:4]) != adler32.Checksum(s.out) {
		return nil, errZlib // incorrect data check
	}
	return s.out, nil
}

// decodeBody is undici's utf8DecodeBytes: strip a UTF-8 BOM, then
// TextDecoder.decode, which strips one more and replaces bad bytes.
func decodeBody(b []byte) string {
	for range 2 {
		b = bytes.TrimPrefix(b, []byte{0xef, 0xbb, 0xbf})
	}
	return jsstr.DecodeUTF8(b)
}

// text is `await response.text()`.
func (r *fetchResponse) text() (string, error) {
	if r.err != nil {
		return "", r.err
	}
	return decodeBody(r.body), nil
}

// ProposeBrief ports src/repair-brief.ts:195 proposeRepairBriefWithGpt with
// the CLI's options ({ model }): the key comes from OPENAI_API_KEY. It
// returns the validated brief; an error is the message TS throws.
func ProposeBrief(packetInput jsjson.Value, model string) (jsjson.Value, error) {
	packet, err := bundle.ParseValue(packetInput, EvidencePacketSchema)
	if err != nil {
		return jsjson.Value{}, err
	}
	if !PacketDigestValid(packet) {
		return jsjson.Value{}, errors.New("Refusing to send a repair packet with an invalid digest.")
	}
	apiKey, _ := nodeproc.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return jsjson.Value{}, errors.New("OPENAI_API_KEY is required for a live GPT-5.6 repair brief.")
	}
	redacted, _ := redaction.RedactValue(packet, "$")
	safePacket, err := bundle.ParseValue(redacted, EvidencePacketSchema)
	if err != nil {
		return jsjson.Value{}, err
	}
	body := jsstr.ToUTF8(BriefRequestBody(model, safePacket))
	response, err := postJSON(responsesURL(), "Bearer "+apiKey, []byte(body))
	if err != nil {
		return jsjson.Value{}, err
	}
	if response.status < 200 || response.status > 299 {
		text, err := response.text()
		if err != nil {
			return jsjson.Value{}, err
		}
		return jsjson.Value{}, errors.New(jsexc.Concat("OpenAI Responses request failed: ", strconv.Itoa(response.status), " ", text))
	}
	text, err := response.text()
	if err != nil {
		return jsjson.Value{}, err
	}
	parsed, err := jsjson.Parse(text)
	if err != nil {
		return jsjson.Value{}, err
	}
	outputText, err := ExtractResponseText(parsed)
	if err != nil {
		return jsjson.Value{}, err
	}
	output, err := jsjson.Parse(outputText)
	if err != nil {
		return jsjson.Value{}, err
	}
	validation := ValidateBrief(packet, output)
	if !validation.Valid {
		return jsjson.Value{}, errors.New(jsexc.Concat("GPT-5.6 repair brief was rejected: ", jsexc.Join(validation.Errors, "; ")))
	}
	return validation.Brief, nil
}
