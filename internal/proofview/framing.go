package proofview

import (
	"bytes"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/Mizore66/faultline/internal/clock"
)

// Request framing in front of net/http. net/http decides some things about a
// request before the handler runs, and Node does them differently:
//   - it answers an `Expect` other than 100-continue with its own 417 and
//     closes, never running the handler (Node passes the 417 through its
//     normal keep-alive response), and never writes `100 Continue` for a
//     handler that does not read the body (Node writes it first);
//   - it limits the head to MaxHeaderBytes of raw bytes (Node: 16 KiB of
//     URL, field names and values, its own 431 response, then destroy);
//   - it gives up on an unread body past 256 KiB and resets the connection
//     (Node dumps any unread body and keeps the connection).
//
// So nodeConn.Read hands net/http each request head without its framing
// and expectation lines (Content-Length, Transfer-Encoding, Expect), plus
// an Expect marker the handler reads, and consumes the bodies itself. It
// only finds where each message ends, the way llhttp frames a request:
// leading CR/LF skipped, the head up to its empty line, a Content-Length or
// chunked body, trailers. The request line and the other header lines are
// left to net/http, whose handling of malformed ones differs
// (KNOWN_DIFFERENCES.md). It also drives the connection state Node's parser
// keeps (message begin, headers complete, message complete).

// Node's limits (node_http_parser.cc: --max-http-header-size and
// kMaxChunkExtensionsSize), and the raw head size held before giving up.
const (
	maxHeaderSize          = 16384
	maxChunkExtensionsSize = 16384
	maxHeadBytes           = 1 << 20
)

// expectMarker carries Node's expectation decision to the handler:
// "continue" (write `100 Continue`, then respond) or "fail" (417).
const expectMarker = "Faultline-Node-Expect"

// What Node writes before destroying a socket whose request it cannot parse
// (socketOnError).
const (
	badRequestResponse              = "HTTP/1.1 400 Bad Request\r\nConnection: close\r\n\r\n"
	headerFieldsTooLargeResponse    = "HTTP/1.1 431 Request Header Fields Too Large\r\nConnection: close\r\n\r\n"
	chunkExtensionsTooLargeResponse = "HTTP/1.1 413 Payload Too Large\r\nConnection: close\r\n\r\n"
)

type framePhase uint8

const (
	framingStart       framePhase = iota // between messages
	framingHead                          // in a request head
	framingBody                          // in a Content-Length body
	framingChunkSize                     // in a chunk-size
	framingChunkExt                      // in its extensions
	framingChunkSizeLF                   // after its CR
	framingChunkData                     // in chunk data
	framingChunkDataCR                   // after chunk data
	framingChunkDataLF                   // after its CR
	framingTrailers                      // in the trailer section
	framingFailed                        // the connection is to be destroyed
)

// lineState is where the head (or trailer) scan is within a line, for
// Node's header byte count (TrackHeader: the URL, each field name and each
// value without its leading whitespace).
type lineState uint8

const (
	lineMethod lineState = iota
	lineBeforeURL
	lineURL
	lineVersion
	lineStart
	lineBlankCR
	lineName
	lineBeforeValue
	lineValue
)

// framer is nodeConn's framing state. It is guarded by nodeConn.mu.
type framer struct {
	buf       []byte // what one read of the socket returns (Node reads 64 KiB)
	out       []byte // rewritten heads net/http has not read yet
	phase     framePhase
	line      lineState
	head      []byte // the head read so far
	counted   int    // header_nread_
	remaining uint64 // body or chunk bytes left
	digits    int    // chunk-size digits read
	extBytes  int    // chunk extension bytes read
	trailerCR bool   // the last trailer byte was CR
	// failure is the response to write once net/http has written the
	// responses to every head before it; suppressed is Node finding a
	// response still attached to the socket, when it writes nothing. That
	// is the case when a head completed earlier in the same read and no
	// body byte followed it: the handler has ended its response, but the
	// response finishes (and leaves the socket) only when the tick queue
	// runs, which the parser's on_body callback does and its header and
	// message callbacks do not (headsInChunk).
	failure      string
	suppressed   bool
	headsInChunk bool
	heads, idles int // heads handed to net/http, requests it has finished
	readDeadline time.Time
}

// Read hands net/http the rewritten heads, consuming bodies on the way.
func (c *nodeConn) Read(b []byte) (int, error) {
	c.mu.Lock()
	for {
		if len(c.out) > 0 {
			n := copy(b, c.out)
			c.out = c.out[n:]
			c.mu.Unlock()
			return n, nil
		}
		if c.phase == framingFailed {
			return c.failConnection()
		}
		c.mu.Unlock()
		n, err := c.Conn.Read(c.buf)
		c.mu.Lock()
		if n > 0 {
			c.lastActivity = clock.Monotonic()
			c.feed(c.buf[:n])
			continue
		}
		if err == io.EOF && c.phase != framingStart && c.phase != framingFailed {
			// socketOnEnd: parser.finish() fails inside a message. No
			// response is attached by then.
			c.fail(badRequestResponse)
			c.suppressed = false
			continue
		}
		c.mu.Unlock()
		return 0, err
	}
}

// SetReadDeadline also wakes a Read waiting in failConnection: net/http
// aborts its background read with a deadline in the past, and must get it
// back before it finishes the request that Read is waiting for.
func (c *nodeConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.readDeadline = t
	c.cond.Broadcast()
	c.mu.Unlock()
	return c.Conn.SetReadDeadline(t)
}

// idled is net/http having written a response in full and gone back to
// waiting for the next request (StateIdle).
func (c *nodeConn) idled() {
	c.mu.Lock()
	c.idles++
	c.cond.Broadcast()
	c.mu.Unlock()
}

// failConnection is socketOnError, called with c.mu held: once every
// response before the failure is written, it writes the failure response
// (unless suppressed) and destroys the socket.
func (c *nodeConn) failConnection() (int, error) {
	for c.idles < c.heads && !c.destroyed {
		if d := c.readDeadline; !d.IsZero() && !clock.Monotonic().Before(d) {
			c.mu.Unlock()
			return 0, os.ErrDeadlineExceeded
		}
		c.cond.Wait()
	}
	write := !c.destroyed && !c.suppressed
	c.mu.Unlock()
	if write {
		c.Conn.SetWriteDeadline(clock.Monotonic().Add(time.Second))
		io.WriteString(c.Conn, c.failure)
	}
	c.destroy()
	return 0, io.EOF
}

// fail stops the parse: response is written once the earlier responses are.
func (c *nodeConn) fail(response string) {
	c.phase, c.failure, c.suppressed = framingFailed, response, c.headsInChunk
}

// feed runs one read's bytes through the framing (llhttp's execute).
func (c *nodeConn) feed(data []byte) {
	c.headsInChunk = false
	for i := 0; i < len(data) && c.phase != framingFailed; {
		switch c.phase {
		case framingStart:
			if data[i] == '\r' || data[i] == '\n' {
				i++
				continue
			}
			// on_message_begin
			c.inMessage, c.headersComplete, c.messageStart = true, false, c.lastActivity
			c.phase, c.line, c.counted, c.head = framingHead, lineMethod, 0, c.head[:0]
		case framingHead:
			b := data[i]
			i++
			// A value's leading whitespace is neither counted nor kept
			// (net/http trims it too), so a head Node accepts fits in
			// maxHeadBytes.
			if c.line != lineBeforeValue || b != ' ' && b != '\t' {
				c.head = append(c.head, b)
			}
			if c.scanHeaderByte(b) {
				c.endHead()
			} else if len(c.head) > maxHeadBytes && c.phase != framingFailed {
				c.fail(headerFieldsTooLargeResponse)
			}
		case framingBody, framingChunkData:
			n := min(uint64(len(data)-i), c.remaining)
			i += int(n)
			c.headsInChunk = false
			c.remaining -= n
			if c.remaining == 0 {
				if c.phase == framingBody {
					c.endMessage()
				} else {
					c.phase = framingChunkDataCR
				}
			}
		case framingChunkSize:
			b := data[i]
			i++
			if v, ok := hexDigit(b); ok {
				if c.remaining > (math.MaxUint64-v)/16 {
					c.fail(badRequestResponse) // Chunk size overflow
				}
				c.remaining = c.remaining*16 + v
				c.digits++
				continue
			}
			switch {
			case c.digits == 0:
				c.fail(badRequestResponse)
			case b == '\r':
				c.phase = framingChunkSizeLF
			case b == ';':
				c.phase, c.extBytes = framingChunkExt, 0
			default:
				c.fail(badRequestResponse)
			}
		case framingChunkExt:
			b := data[i]
			i++
			switch b {
			case '\r':
				c.phase = framingChunkSizeLF
			case '\n':
				c.fail(badRequestResponse)
			case ';', '=':
			default:
				if c.extBytes++; c.extBytes > maxChunkExtensionsSize {
					c.fail(chunkExtensionsTooLargeResponse)
				}
			}
		case framingChunkSizeLF:
			b := data[i]
			i++
			switch {
			case b != '\n':
				c.fail(badRequestResponse)
			case c.remaining == 0:
				// The last chunk: trailers follow, counted afresh
				// (on_chunk_header resets header_nread_).
				c.phase, c.line, c.counted, c.trailerCR = framingTrailers, lineStart, 0, false
			default:
				c.phase = framingChunkData
			}
		case framingChunkDataCR, framingChunkDataLF:
			b := data[i]
			i++
			switch {
			case c.phase == framingChunkDataCR && b == '\r':
				c.phase = framingChunkDataLF
			case c.phase == framingChunkDataLF && b == '\n':
				c.phase, c.remaining, c.digits = framingChunkSize, 0, 0
			default:
				c.fail(badRequestResponse)
			}
		case framingTrailers:
			b := data[i]
			i++
			if !c.validTrailerByte(b) {
				c.fail(badRequestResponse)
			} else if c.scanHeaderByte(b) {
				c.endMessage()
			}
		}
	}
}

// scanHeaderByte follows one head or trailer byte, counting the bytes Node
// counts and failing with 431 when they reach maxHeaderSize; it reports the
// empty line that ends the section.
func (c *nodeConn) scanHeaderByte(b byte) (end bool) {
	count := false
	switch c.line {
	case lineMethod:
		switch b {
		case ' ':
			c.line = lineBeforeURL
		case '\n':
			c.line = lineStart
		}
	case lineBeforeURL, lineURL:
		switch b {
		case ' ':
			if c.line == lineURL {
				c.line = lineVersion
			}
		case '\n':
			c.line = lineStart
		case '\r':
		default:
			count, c.line = true, lineURL
		}
	case lineVersion:
		if b == '\n' {
			c.line = lineStart
		}
	case lineStart, lineBlankCR:
		switch {
		case b == '\n':
			return true
		case b == '\r' && c.line == lineStart:
			c.line = lineBlankCR
		default:
			count, c.line = true, lineName
		}
	case lineName:
		switch b {
		case ':':
			c.line = lineBeforeValue
		case '\n':
			c.line = lineStart
		case '\r':
		default:
			count = true
		}
	case lineBeforeValue, lineValue:
		switch b {
		case ' ', '\t':
			count = c.line == lineValue
		case '\n':
			c.line = lineStart
		case '\r':
		default:
			count, c.line = true, lineValue
		}
	}
	if count {
		c.counted++
	}
	return false
}

// validTrailerByte checks a trailer byte as llhttp does (the trailers do
// not reach net/http): a field name of token bytes and a colon, a value of
// tab and 0x20 to 0xff but 0x7f, each line ended by CR LF.
func (c *nodeConn) validTrailerByte(b byte) bool {
	prevCR := c.trailerCR
	c.trailerCR = b == '\r'
	switch {
	case prevCR:
		return b == '\n'
	case b == '\r':
		return true
	case b == '\n':
		return false
	}
	switch c.line {
	case lineStart:
		return isTokenByte(b)
	case lineName:
		return b == ':' || isTokenByte(b)
	}
	return b == '\t' || b >= 0x20 && b != 0x7f
}

func isTokenByte(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || strings.IndexByte("!#$%&'*+-.^_`|~", b) >= 0
}

// endHead is on_headers_complete: the head goes to net/http without its
// framing and Expect lines, with the expectation marker, and the body is
// framed as llhttp frames it. A Content-Length or Transfer-Encoding llhttp
// rejects (not digits and spaces, repeated, together, a coding after
// chunked) gets Node's 400; other malformed lines are left to net/http.
// Node reads only the first 1000 header lines (maxHeaderPairs 2000), so an
// Expect line after them is ignored, as is the expectation of an HTTP/1.0
// request.
func (c *nodeConn) endHead() {
	out := c.out
	rest := c.head
	end := bytes.IndexByte(rest, '\n') + 1
	requestLine := bytes.TrimSuffix(rest[:end-1], []byte("\r"))
	out = append(out, rest[:end]...)
	rest = rest[end:]
	http11 := bytes.HasSuffix(requestLine, []byte(" HTTP/1.1"))
	var expect []byte
	var length uint64
	hasExpect, hasLength, hasTE, chunked := false, false, false, false
	for lines := 0; ; lines++ {
		end = bytes.IndexByte(rest, '\n') + 1
		raw := rest[:end]
		rest = rest[end:]
		line := bytes.TrimSuffix(raw[:end-1], []byte("\r"))
		if len(line) == 0 {
			if http11 && hasExpect {
				decision := "fail"
				if continueExpression(expect) {
					decision = "continue"
				}
				out = append(out, expectMarker+": "+decision+"\r\n"...)
			}
			out = append(out, raw...)
			break
		}
		name, value, _ := bytes.Cut(line, []byte(":"))
		value = bytes.TrimLeft(value, " \t")
		switch {
		case bytes.EqualFold(name, []byte("Content-Length")):
			n, ok := contentLength(value)
			if hasLength || hasTE || !ok {
				c.fail(badRequestResponse)
				return
			}
			length, hasLength = n, true
		case bytes.EqualFold(name, []byte("Transfer-Encoding")):
			if hasLength {
				c.fail(badRequestResponse)
				return
			}
			if len(value) == 0 {
				break // an empty value sets no flag
			}
			last, ok := transferEncodingChunked(value)
			if chunked || !ok {
				c.fail(badRequestResponse)
				return
			}
			hasTE, chunked = true, last
		case bytes.EqualFold(name, []byte("Expect")):
			// Node joins repeated Expect values with ", " and trims the
			// value's trailing whitespace.
			if lines >= 1000 {
				break
			}
			if hasExpect {
				expect = append(expect, ", "...)
			}
			expect = append(expect, bytes.TrimRight(value, " \t")...)
			hasExpect = true
		case bytes.EqualFold(name, []byte(expectMarker)):
		default:
			out = append(out, raw...)
		}
	}
	c.out = out
	c.heads++
	c.headsInChunk = true
	// parserOnIncoming: resetSocketTimeout clears the keep-alive timeout.
	c.headersComplete, c.keepAlive = true, false
	switch {
	case hasTE && !chunked:
		// llhttp__after_headers_complete returns 5 for a request whose
		// last coding is not chunked: the error comes after the handler.
		c.fail(badRequestResponse)
	case chunked:
		c.phase, c.remaining, c.digits = framingChunkSize, 0, 0
	case length > 0:
		c.phase, c.remaining = framingBody, length
	default:
		c.endMessage()
	}
}

// endMessage is on_message_complete.
func (c *nodeConn) endMessage() {
	c.inMessage, c.phase = false, framingStart
}

// continueExpression is Node's /(?:^|\W)100-continue(?:$|\W)/i.
func continueExpression(v []byte) bool {
	const token = "100-continue"
	for i := 0; i+len(token) <= len(v); i++ {
		if bytes.EqualFold(v[i:i+len(token)], []byte(token)) &&
			(i == 0 || !isWordByte(v[i-1])) && (i+len(token) == len(v) || !isWordByte(v[i+len(token)])) {
			return true
		}
	}
	return false
}

func isWordByte(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b == '_'
}

// contentLength is llhttp's Content-Length value: decimal digits (an
// unsigned 64-bit value) followed by spaces.
func contentLength(v []byte) (uint64, bool) {
	var n uint64
	i := 0
	for ; i < len(v) && v[i] >= '0' && v[i] <= '9'; i++ {
		d := uint64(v[i] - '0')
		if n > (math.MaxUint64-d)/10 {
			return 0, false
		}
		n = n*10 + d
	}
	if i == 0 {
		return 0, false
	}
	for ; i < len(v); i++ {
		if v[i] != ' ' {
			return 0, false
		}
	}
	return n, true
}

// transferEncodingChunked reports whether a Transfer-Encoding value's last
// coding is chunked (followed only by spaces), and fails one where chunked
// is followed by another coding (forbidden in a request).
func transferEncodingChunked(v []byte) (last, ok bool) {
	codings := bytes.Split(v, []byte(","))
	for i, coding := range codings {
		coding = bytes.TrimLeft(coding, " \t")
		if i < len(codings)-1 {
			if bytes.EqualFold(bytes.TrimRight(coding, " "), []byte("chunked")) {
				return false, false
			}
			continue
		}
		last = bytes.EqualFold(bytes.TrimRight(coding, " "), []byte("chunked"))
	}
	return last, true
}

func hexDigit(b byte) (uint64, bool) {
	switch {
	case b >= '0' && b <= '9':
		return uint64(b - '0'), true
	case b >= 'a' && b <= 'f':
		return uint64(b-'a') + 10, true
	case b >= 'A' && b <= 'F':
		return uint64(b-'A') + 10, true
	}
	return 0, false
}
