package servefixture

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// CheckHTTP is the one portable HTTP verifier for the serve-bundle server (the
// direct proofview server, the actual CLI, and the release and live tests). It
// speaks raw HTTP over owned TCP connections so every status line, header
// (names, values, count) and framing byte the server emits is compared, not
// what net/http would normalise. The expectations restate src/server.ts
// startGitProofServer plus Node 22's http framing as documented in
// difftest/KNOWN_DIFFERENCES.md, never what the Go server happens to emit:
// Date is validated (present exactly once, parses, and is in the canonical
// HTTP-date format) and only its value is then ignored; an HTTP/1.0 status line
// is HTTP/1.0 (the documented difference from Node); chunk boundaries are not
// compared. Every client is closed before CheckHTTP returns and no case waits
// on a timer: a connection that must stay open is proven by a follow-up
// request on it, and one that must close by reading EOF.
//
// The server being checked must have no other client that it needs to close.
func CheckHTTP(ctx context.Context, rawURL string, page []byte) error {
	return checkHTTP(ctx, rawURL, page, false)
}

// CheckNodeHTTP is CheckHTTP for the frozen Node server. It runs the SAME case
// matrix and the same checker; the only expectation that differs is the status
// line version of the response to an HTTP/1.0 request, which Node always
// writes as HTTP/1.1 where Go answers HTTP/1.0 (difftest/KNOWN_DIFFERENCES.md,
// "The judge-proof server"). Each entry point strictly rejects the other's
// version; neither accepts either.
func CheckNodeHTTP(ctx context.Context, rawURL string, page []byte) error {
	return checkHTTP(ctx, rawURL, page, true)
}

func checkHTTP(ctx context.Context, rawURL string, page []byte, node bool) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != "" {
		return fmt.Errorf("%q is not an http://127.0.0.1:<port> address", rawURL)
	}
	var failures []error
	for _, c := range httpCases(u.Host, page, node) {
		if err := runHTTPCase(ctx, u.Host, c); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", c.name, err))
		}
		if ctx.Err() != nil {
			failures = append(failures, ctx.Err())
			break
		}
	}
	return errors.Join(failures...)
}

// ---- expected responses ----

type wantResponse struct {
	version string // "" is HTTP/1.1
	status  string // "404 Not Found"
	headers map[string]string
	noDate  bool // a bare response written before a destroyed connection
	body    string
	head    bool // answers a HEAD request: framing headers but no body
}

type httpCase struct {
	name string
	send string
	want []wantResponse
}

var (
	htmlHeaders = map[string]string{
		"Content-Type":            "text/html; charset=utf-8",
		"Cache-Control":           "no-store",
		"Content-Security-Policy": "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
		"Referrer-Policy":         "no-referrer",
		"X-Content-Type-Options":  "nosniff",
	}
	textHeaders = map[string]string{
		"Content-Type":           "text/plain; charset=utf-8",
		"Cache-Control":          "no-store",
		"X-Content-Type-Options": "nosniff",
	}
)

// framing is the Connection, Keep-Alive and Transfer-Encoding Node adds.
func framing(kind string) map[string]string {
	switch kind {
	case "keep":
		return map[string]string{"Connection": "keep-alive", "Keep-Alive": "timeout=5", "Transfer-Encoding": "chunked"}
	case "keep-head":
		return map[string]string{"Connection": "keep-alive", "Keep-Alive": "timeout=5"}
	case "close":
		return map[string]string{"Connection": "close", "Transfer-Encoding": "chunked"}
	}
	return map[string]string{"Connection": "close"} // close-head, close10, close10-head
}

func expectedHTTPResponse(status string, base map[string]string, kind, body string) wantResponse {
	headers := map[string]string{}
	for k, v := range base {
		headers[k] = v
	}
	for k, v := range framing(kind) {
		headers[k] = v
	}
	w := wantResponse{status: status, headers: headers, body: body}
	if strings.HasSuffix(kind, "head") {
		w.head, w.body = true, ""
	}
	if strings.HasPrefix(kind, "close10") {
		w.version = "HTTP/1.0"
	}
	return w
}

// bare is a response Node writes before destroying a connection it cannot
// parse: no Date, no body.
func bare(status string) wantResponse {
	return wantResponse{status: status, headers: map[string]string{"Connection": "close"}, noDate: true}
}

var continueResponse = wantResponse{status: "100 Continue", noDate: true}

func request(host, method, target, proto string, extra ...string) string {
	s := method + " " + target + " " + proto + "\r\nHost: " + host + "\r\n"
	for _, e := range extra {
		s += e + "\r\n"
	}
	return s + "\r\n"
}

func httpCases(host string, page []byte, node bool) []httpCase {
	cases := goHTTPCases(host, page)
	if node {
		for _, c := range cases {
			for i := range c.want {
				if c.want[i].version == "HTTP/1.0" {
					c.want[i].version = "HTTP/1.1" // the single documented Node difference
				}
			}
		}
	}
	return cases
}

func goHTTPCases(host string, page []byte) []httpCase {
	body := string(page)
	html := func(kind string) wantResponse { return expectedHTTPResponse("200 OK", htmlHeaders, kind, body) }
	notFound := func(kind string) wantResponse {
		return expectedHTTPResponse("404 Not Found", textHeaders, kind, "Not found")
	}
	failed := func(kind string) wantResponse { return expectedHTTPResponse("417 Expectation Failed", nil, kind, "") }
	follow := request(host, "GET", "/x", "HTTP/1.1", "Connection: close") // proves a connection stayed open
	var cases []httpCase
	add := func(name, send string, want ...wantResponse) { cases = append(cases, httpCase{name, send, want}) }

	add("get-close", request(host, "GET", "/", "HTTP/1.1", "Connection: close"), html("close"))
	add("get-keep-alive", request(host, "GET", "/", "HTTP/1.1")+follow, html("keep"), notFound("close"))
	// src/server.ts:69 serves the page only for GET, so HEAD / is the 404.
	add("head-root", request(host, "HEAD", "/", "HTTP/1.1")+follow, notFound("keep-head"), notFound("close"))
	add("head-unknown", request(host, "HEAD", "/x", "HTTP/1.1")+follow, notFound("keep-head"), notFound("close"))
	add("pipelined", request(host, "GET", "/", "HTTP/1.1")+follow, html("keep"), notFound("close"))

	// The same page for every target whose URL pathname is "/".
	for _, target := range []string{"/?a=1", "/?", "/x/..", "/%2e", "/%2E", "/x/.%2e", "/x/%2e%2E", "/x/%2E.", "//host/", "//host/?q"} {
		add("page "+target, request(host, "GET", target, "HTTP/1.1")+follow, html("keep"), notFound("close"))
	}
	// Everything else, including the routes a mutating server would have.
	for _, target := range []string{"/x", "/index.html", "/api/analysis", "/api/rerun", "/manifest.json", "/download", "/x/../y", "//host/x", "/%2e/x"} {
		add("not-found "+target, request(host, "GET", target, "HTTP/1.1")+follow, notFound("keep"), notFound("close"))
	}
	add("post-root", request(host, "POST", "/", "HTTP/1.1", "Content-Length: 0")+follow, notFound("keep"), notFound("close"))
	add("post-rerun", request(host, "POST", "/api/rerun", "HTTP/1.1", "Content-Length: 2")+"{}"+follow, notFound("keep"), notFound("close"))
	add("put-download", request(host, "PUT", "/download", "HTTP/1.1", "Content-Length: 0")+follow, notFound("keep"), notFound("close"))
	add("options-root", request(host, "OPTIONS", "/", "HTTP/1.1")+follow, notFound("keep"), notFound("close"))
	add("options-star", request(host, "OPTIONS", "*", "HTTP/1.1")+follow, notFound("keep"), notFound("close"))

	// HTTP/1.0 cannot keep a Node connection alive: Connection: close, no
	// chunking, body ended by the close; the status line says HTTP/1.0.
	add("http10-root", request(host, "GET", "/", "HTTP/1.0"), expectedHTTPResponse("200 OK", htmlHeaders, "close10", body))
	add("http10-keep-alive", request(host, "GET", "/x", "HTTP/1.0", "Connection: keep-alive"), expectedHTTPResponse("404 Not Found", textHeaders, "close10", "Not found"))
	add("http10-head", request(host, "HEAD", "/", "HTTP/1.0"), expectedHTTPResponse("404 Not Found", textHeaders, "close10-head", ""))

	// Expect: 100-continue gets the interim response first; any other value
	// the empty 417 through the normal keep-alive response.
	add("expect-other", request(host, "GET", "/x", "HTTP/1.1", "Expect: foo")+follow, failed("keep"), notFound("close"))
	add("expect-other-close", request(host, "GET", "/x", "HTTP/1.1", "Connection: close", "Expect: foo"), failed("close"))
	add("expect-other-head", request(host, "HEAD", "/", "HTTP/1.1", "Expect: foo")+follow, failed("keep-head"), notFound("close"))
	add("expect-empty", request(host, "GET", "/x", "HTTP/1.1", "Expect: ")+follow, failed("keep"), notFound("close"))
	add("expect-word", request(host, "GET", "/x", "HTTP/1.1", "Expect: x100-continue")+follow, failed("keep"), notFound("close"))
	add("expect-continue-get", request(host, "GET", "/", "HTTP/1.1", "Expect: 100-Continue")+follow, continueResponse, html("keep"), notFound("close"))
	add("expect-continue-post", request(host, "POST", "/x", "HTTP/1.1", "Content-Length: 5", "Expect: 100-continue")+"abcde"+follow, continueResponse, notFound("keep"), notFound("close"))
	add("expect-continue-close", request(host, "GET", "/x", "HTTP/1.1", "Connection: close", "Expect: 100-continue"), continueResponse, notFound("close"))

	// Unread bodies are consumed and the connection kept.
	chunked := request(host, "POST", "/x", "HTTP/1.1", "Transfer-Encoding: chunked")
	big := strings.Repeat("x", 300000)
	add("body-content-length", request(host, "POST", "/x", "HTTP/1.1", "Content-Length: 5")+"abcde"+follow, notFound("keep"), notFound("close"))
	add("body-content-length-trailing-space", request(host, "POST", "/x", "HTTP/1.1", "Content-Length: 5 ")+"abcde"+follow, notFound("keep"), notFound("close"))
	add("body-unread-300k", request(host, "POST", "/x", "HTTP/1.1", "Content-Length: 300000")+big+follow, notFound("keep"), notFound("close"))
	add("body-chunked", chunked+"2\r\nab\r\n0\r\n\r\n"+follow, notFound("keep"), notFound("close"))
	add("body-chunked-trailer", chunked+"2\r\nab\r\n0\r\nT: b\r\n\r\n"+follow, notFound("keep"), notFound("close"))
	add("body-chunked-300k", chunked+strconv.FormatInt(int64(len(big)), 16)+"\r\n"+big+"\r\n0\r\n\r\n"+follow, notFound("keep"), notFound("close"))
	add("leading-crlf-between", request(host, "GET", "/x", "HTTP/1.1")+"\r\n\r\n"+follow, notFound("keep"), notFound("close"))

	// Malformed framing Node rejects with a bare 400 and a destroyed socket.
	post := request(host, "POST", "/x", "HTTP/1.1", "Content-Length: 1", "Content-Length: 1")
	add("content-length-twice", post+"a", bare("400 Bad Request"))
	add("content-length-and-chunked", request(host, "POST", "/x", "HTTP/1.1", "Content-Length: 3", "Transfer-Encoding: chunked")+"0\r\n\r\n", bare("400 Bad Request"))
	add("transfer-encoding-chunked-twice", request(host, "POST", "/x", "HTTP/1.1", "Transfer-Encoding: chunked, chunked")+"0\r\n\r\n", bare("400 Bad Request"))
	add("content-length-not-digits", request(host, "POST", "/x", "HTTP/1.1", "Content-Length: 5x")+"abcde", bare("400 Bad Request"))

	// Head limit: URL, field names and values (a value's leading whitespace
	// excluded) reach 16384 bytes -> bare 431. Host: x keeps the count exact:
	// "/x"(2) + "Host"(4) + "x"(1) + "X"(1) + value.
	limit := func(target string, extra string) string {
		return "GET " + target + " HTTP/1.1\r\nHost: x\r\n" + extra + "Connection: close\r\n\r\n"
	}
	// "Connection: close" (10+5) is counted too: reserve it in each value.
	const counted = 2 + 4 + 1 + 1 + len("Connection") + len("close")
	value := func(n int) string { return strings.Repeat("a", n) }
	add("header-16383", limit("/x", "X: "+value(16383-counted)+"\r\n"), notFound("close"))
	add("header-16384", limit("/x", "X: "+value(16384-counted)+"\r\n"), bare("431 Request Header Fields Too Large"))
	add("header-leading-whitespace", limit("/x", "X:"+strings.Repeat(" ", 100)+value(16383-counted)+"\r\n"), notFound("close"))
	add("header-trailing-whitespace", limit("/x", "X: "+value(16384-counted-6)+strings.Repeat(" ", 6)+"\r\n"), bare("431 Request Header Fields Too Large"))
	// Without the X header the counted bytes are the URL, "Host"+"x" and
	// "Connection"+"close": urlOverhead = 4+1+10+5.
	const urlOverhead = 4 + 1 + 10 + 5
	add("url-16383", limit("/"+value(16383-urlOverhead-1), ""), notFound("close"))
	add("url-16384", limit("/"+value(16384-urlOverhead-1), ""), bare("431 Request Header Fields Too Large"))
	return cases
}

// ---- raw client ----

func runHTTPCase(ctx context.Context, host string, c httpCase) error {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp4", host)
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	written := make(chan struct{})
	go func() {
		defer close(written)
		// One write, so the server has read the whole request before it can
		// destroy the connection. A write error is expected when it rejects the
		// request early; the responses read below decide the case.
		conn.Write([]byte(c.send))
	}()
	defer func() {
		stop()
		conn.Close()
		<-written
	}()
	reader := bufio.NewReader(conn)
	var problems []string
	for i, want := range c.want {
		got, err := readResponse(reader, want.head)
		if err != nil {
			problems = append(problems, fmt.Sprintf("response %d: %v", i, err))
			break
		}
		problems = append(problems, compareResponse(i, got, want)...)
	}
	if len(problems) == 0 {
		// Nothing more may arrive and the server must have ended the connection.
		extra, err := readResponse(reader, false)
		switch {
		case err == nil:
			problems = append(problems, fmt.Sprintf("unexpected extra response %q", extra.status))
		case !errors.Is(err, io.EOF):
			problems = append(problems, fmt.Sprintf("connection did not end cleanly: %v", err))
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

type rawResponse struct {
	version, status string
	headers         [][2]string
	body            []byte
	interim         bool
}

func (r *rawResponse) values(name string) []string {
	var out []string
	for _, h := range r.headers {
		if h[0] == name {
			out = append(out, h[1])
		}
	}
	return out
}

func readLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) && line == "" {
			return "", io.EOF
		}
		return "", fmt.Errorf("incomplete line %q: %w", line, err)
	}
	text, ok := strings.CutSuffix(line, "\r\n")
	if !ok {
		return "", fmt.Errorf("line %q does not end in CRLF", line)
	}
	return text, nil
}

// readResponse reads one response by its own framing: chunked, Content-Length
// or, absent both, until the server closes. head says the request was HEAD.
func readResponse(reader *bufio.Reader, head bool) (*rawResponse, error) {
	statusLine, err := readLine(reader)
	if err != nil {
		return nil, err
	}
	version, status, ok := strings.Cut(statusLine, " ")
	if !ok || len(status) < 3 {
		return nil, fmt.Errorf("malformed status line %q", statusLine)
	}
	r := &rawResponse{version: version, status: status, interim: status[0] == '1'}
	for {
		line, err := readLine(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ": ")
		if !ok {
			return nil, fmt.Errorf("malformed header line %q", line)
		}
		r.headers = append(r.headers, [2]string{name, value})
	}
	encoding := r.values("Transfer-Encoding")
	lengths := r.values("Content-Length")
	switch {
	case r.interim || head:
	case len(encoding) == 1 && encoding[0] == "chunked":
		r.body, err = readChunked(reader)
	case len(encoding) > 0:
		err = fmt.Errorf("unsupported Transfer-Encoding %q", encoding)
	case len(lengths) == 1:
		var n int
		if n, err = strconv.Atoi(lengths[0]); err == nil {
			r.body = make([]byte, n)
			_, err = io.ReadFull(reader, r.body)
		}
	default:
		r.body, err = io.ReadAll(reader)
	}
	if err != nil {
		return nil, fmt.Errorf("body of %q: %w", statusLine, err)
	}
	return r, nil
}

func readChunked(reader *bufio.Reader) ([]byte, error) {
	var body []byte
	for {
		sizeLine, err := readLine(reader)
		if err != nil {
			return nil, err
		}
		size, err := strconv.ParseUint(sizeLine, 16, 31)
		if err != nil {
			return nil, fmt.Errorf("chunk size %q: %w", sizeLine, err)
		}
		if size == 0 {
			for { // trailer section ends at an empty line
				line, err := readLine(reader)
				if err != nil {
					return nil, err
				}
				if line == "" {
					return body, nil
				}
			}
		}
		chunk := make([]byte, size)
		if _, err := io.ReadFull(reader, chunk); err != nil {
			return nil, err
		}
		body = append(body, chunk...)
		if end, err := readLine(reader); err != nil || end != "" {
			return nil, fmt.Errorf("chunk is not followed by CRLF (%q, %v)", end, err)
		}
	}
}

func compareResponse(i int, got *rawResponse, want wantResponse) []string {
	var problems []string
	note := func(format string, a ...any) {
		problems = append(problems, fmt.Sprintf("response %d: ", i)+fmt.Sprintf(format, a...))
	}
	wantVersion := want.version
	if wantVersion == "" {
		wantVersion = "HTTP/1.1"
	}
	if got.version != wantVersion || got.status != want.status {
		note("status line %q %q, want %q %q", got.version, got.status, wantVersion, want.status)
	}
	seen := map[string]string{}
	var dates []string
	for _, h := range got.headers {
		if h[0] == "Date" {
			dates = append(dates, h[1])
			continue
		}
		if _, dup := seen[h[0]]; dup {
			note("header %q is sent twice", h[0])
		}
		seen[h[0]] = h[1]
	}
	for name, value := range want.headers {
		if have, ok := seen[name]; !ok {
			note("header %q is missing (want %q)", name, value)
		} else if have != value {
			note("header %q = %q, want %q", name, have, value)
		}
	}
	for name, value := range seen {
		if _, ok := want.headers[name]; !ok {
			note("unexpected header %q: %q", name, value)
		}
	}
	switch {
	case want.noDate && len(dates) != 0:
		note("unexpected Date %q", dates)
	case !want.noDate && len(dates) != 1:
		note("Date is sent %d times, want exactly once", len(dates))
	case !want.noDate:
		parsed, err := http.ParseTime(dates[0])
		if err != nil || parsed.UTC().Format(http.TimeFormat) != dates[0] {
			note("Date %q is not a canonical HTTP-date (%v)", dates[0], err)
		}
	}
	if string(got.body) != want.body {
		note("body differs: got %d bytes, want %d%s", len(got.body), len(want.body), firstDifference(got.body, []byte(want.body)))
	}
	return problems
}

func firstDifference(got, want []byte) string {
	n := min(len(got), len(want))
	i := 0
	for i < n && got[i] == want[i] {
		i++
	}
	window := func(b []byte) string { return fmt.Sprintf("%q", b[max(i-30, 0):min(i+60, len(b))]) }
	return fmt.Sprintf("; first difference at byte %d: got ...%s want ...%s", i, window(got), window(want))
}
