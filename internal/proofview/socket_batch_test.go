package proofview

import (
	"bytes"
	"crypto/sha256"
	"strconv"
	"testing"

	"github.com/Mizore66/faultline/internal/jsstr"
)

const btHeader = "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n"

func btFlatten(parts [][]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func btMake(header, body string, f socketBodyFraming) socketWriteBatch {
	return makeSocketTextBatch([]byte(header), body, f)
}

// btChunked builds the expected chunked wire from independent raw bytes.
func btChunked(header, utf8Body string) string {
	if utf8Body == "" {
		return header + "0\r\n\r\n"
	}
	return header + strconv.FormatInt(int64(len(utf8Body)), 16) + "\r\n" + utf8Body + "\r\n0\r\n\r\n"
}

func btCheckWire(t *testing.T, b socketWriteBatch, wantWire string, wantLogical int) {
	t.Helper()
	got := btFlatten(b.parts)
	if !bytes.Equal(got, []byte(wantWire)) {
		t.Fatalf("wire mismatch:\n got %q\nwant %q", got, wantWire)
	}
	if sha256.Sum256(got) != sha256.Sum256([]byte(wantWire)) {
		t.Fatalf("wire hash mismatch")
	}
	if b.wireLen() != len(wantWire) {
		t.Fatalf("wireLen=%d want %d", b.wireLen(), len(wantWire))
	}
	if b.logicalLen != wantLogical {
		t.Fatalf("logicalLen=%d want %d", b.logicalLen, wantLogical)
	}
}

func TestSocketTextBatchChunkedASCIIExactVectors(t *testing.T) {
	b := btMake(btHeader, "hello", socketBodyChunked)
	want := []string{btHeader + "5", "\r\n", "hello", "\r\n", "0\r\n\r\n"}
	if len(b.parts) != len(want) {
		t.Fatalf("got %d vectors want %d: %q", len(b.parts), len(want), b.parts)
	}
	for i, w := range want {
		if string(b.parts[i]) != w {
			t.Fatalf("part %d = %q want %q", i, b.parts[i], w)
		}
	}
	wire := btHeader + "5\r\nhello\r\n0\r\n\r\n"
	btCheckWire(t, b, wire, len(wire))
	if b.logicalLen != b.wireLen() {
		t.Fatalf("ASCII logicalLen %d != wireLen %d", b.logicalLen, b.wireLen())
	}
}

func TestSocketTextBatchChunkedUnicodeUsesJSUnitsForLogicalLen(t *testing.T) {
	loneHigh := jsstr.FromUTF16([]uint16{0xD83D})
	cases := []struct {
		name        string
		body        string
		utf8        string
		units       int
		chunkHex    string
		wantUTF8Len int
	}{
		{"eacute-emoji", jsstr.FromUTF16([]uint16{0x00E9, 0xD83D, 0xDE00}), "é😀", 3, "6", 6},
		{"lone-high-surrogate", loneHigh, "�", 1, "3", 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := jsstr.UTF16Len(c.body); got != c.units {
				t.Fatalf("precondition UTF16Len=%d want %d", got, c.units)
			}
			if got := jsstr.ToUTF8(c.body); got != c.utf8 || len(got) != c.wantUTF8Len {
				t.Fatalf("precondition ToUTF8=%q (%d bytes)", got, len(got))
			}
			b := btMake(btHeader, c.body, socketBodyChunked)
			wire := btHeader + c.chunkHex + "\r\n" + c.utf8 + "\r\n0\r\n\r\n"
			if wire != btChunked(btHeader, c.utf8) {
				t.Fatalf("test oracle inconsistent")
			}
			// header + chunk-size digits + CRLF + body UTF-16 units + CRLF + 0 CRLF CRLF
			logical := len(btHeader) + len(c.chunkHex) + 2 + c.units + 2 + 5
			btCheckWire(t, b, wire, logical)
			if b.wireLen()-b.logicalLen != c.wantUTF8Len-c.units {
				t.Fatalf("physical-logical delta=%d want %d", b.wireLen()-b.logicalLen, c.wantUTF8Len-c.units)
			}
		})
	}
	// Explicit: eacute-emoji physical wire is 3 larger than logical.
	b := btMake(btHeader, jsstr.FromUTF16([]uint16{0x00E9, 0xD83D, 0xDE00}), socketBodyChunked)
	if b.wireLen() != b.logicalLen+3 {
		t.Fatalf("wireLen=%d logicalLen=%d, want wire = logical+3", b.wireLen(), b.logicalLen)
	}
	// Lone surrogate must not be counted as bytes or runes.
	lb := btMake(btHeader, loneHigh, socketBodyChunked)
	if lb.wireLen() != lb.logicalLen+2 {
		t.Fatalf("lone surrogate wireLen=%d logicalLen=%d, want +2", lb.wireLen(), lb.logicalLen)
	}
}

func TestSocketTextBatchEmptyChunkedBodyHasSingleTerminator(t *testing.T) {
	b := btMake(btHeader, "", socketBodyChunked)
	wire := btHeader + "0\r\n\r\n"
	btCheckWire(t, b, wire, len(btHeader)+5)
	if b.logicalLen != b.wireLen() {
		t.Fatalf("logicalLen %d != wireLen %d", b.logicalLen, b.wireLen())
	}
	if bytes.Count(btFlatten(b.parts), []byte("0\r\n\r\n")) != 1 {
		t.Fatalf("terminator not exactly once: %q", btFlatten(b.parts))
	}
	nonEmpty := 0
	for _, p := range b.parts {
		if len(p) > 0 {
			nonEmpty++
		}
	}
	if nonEmpty > 2 {
		t.Fatalf("empty body produced %d nonempty vectors: %q", nonEmpty, b.parts)
	}
}

func TestSocketTextBatchCloseDelimitedAndSuppressed(t *testing.T) {
	const hdr = "HTTP/1.1 200 OK\r\nConnection: close\r\n\r\n"
	uni := jsstr.FromUTF16([]uint16{0x00E9, 0xD83D, 0xDE00})
	cases := []struct {
		name    string
		body    string
		framing socketBodyFraming
		wire    string
		logical int
	}{
		{"close-unicode", uni, socketBodyCloseDelimited, hdr + "é😀", len(hdr) + 3},
		{"close-empty", "", socketBodyCloseDelimited, hdr, len(hdr)},
		{"suppressed-unicode", uni, socketBodySuppressed, hdr, len(hdr)},
		{"suppressed-empty", "", socketBodySuppressed, hdr, len(hdr)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := btMake(hdr, c.body, c.framing)
			btCheckWire(t, b, c.wire, c.logical)
			w := btFlatten(b.parts)
			if c.name == "close-unicode" && (bytes.Contains(w, []byte("\r\n0\r\n")) || bytes.HasSuffix(w, []byte("\r\n"))) {
				t.Fatalf("close-delimited has chunk framing: %q", w)
			}
		})
	}
	// Close-delimited unicode: physical is 3 larger than logical.
	b := btMake(hdr, uni, socketBodyCloseDelimited)
	if b.wireLen() != len(hdr)+6 || b.logicalLen != len(hdr)+3 {
		t.Fatalf("wireLen=%d logicalLen=%d", b.wireLen(), b.logicalLen)
	}
}

func TestSocketTextBatchOwnsHeader(t *testing.T) {
	for _, f := range []socketBodyFraming{socketBodyChunked, socketBodyCloseDelimited, socketBodySuppressed} {
		hdr := []byte(btHeader)
		b := makeSocketTextBatch(hdr, "hello", f)
		before := append([]byte(nil), btFlatten(b.parts)...)
		wl, ll := b.wireLen(), b.logicalLen
		for i := range hdr {
			hdr[i] = 'X'
		}
		after := btFlatten(b.parts)
		if !bytes.Equal(before, after) {
			t.Fatalf("framing %d: batch aliased caller header:\n before %q\n after  %q", f, before, after)
		}
		if b.wireLen() != wl || b.logicalLen != ll {
			t.Fatalf("framing %d: metadata changed after header mutation", f)
		}
	}
}

func TestSocketTextBatchTailPrefixAccounting(t *testing.T) {
	uni := jsstr.FromUTF16([]uint16{0x00E9, 0xD83D, 0xDE00})
	batches := map[string]socketWriteBatch{
		"ascii":   btMake(btHeader, "hello", socketBodyChunked),
		"unicode": btMake(btHeader, uni, socketBodyChunked),
	}
	for name, b := range batches {
		t.Run(name, func(t *testing.T) {
			original := append([]byte(nil), btFlatten(b.parts)...)
			logical := b.logicalLen
			wire := b.wireLen()
			// Snapshot outer slice headers and bytes of original parts.
			type snap struct {
				p  []byte
				cp []byte
			}
			var snaps []snap
			offsets := map[int]bool{0: true, wire: true}
			pos := 0
			for _, p := range b.parts {
				snaps = append(snaps, snap{p, append([]byte(nil), p...)})
				for _, d := range []int{-1, 0, 1, len(p) / 2} {
					if o := pos + d; o >= 0 && o <= wire {
						offsets[o] = true
					}
				}
				pos += len(p)
				offsets[pos] = true
			}
			offsets[len(btHeader)-1] = true
			offsets[len(btHeader)] = true
			offsets[len(btHeader)+1] = true
			for n := range offsets {
				tl := b.tail(n)
				got := append(append([]byte(nil), original[:n]...), btFlatten(tl)...)
				if !bytes.Equal(got, original) {
					t.Fatalf("n=%d: prefix+tail mismatch:\n got %q\nwant %q", n, got, original)
				}
				// Pump-style mutation of the returned outer slice.
				if len(tl) > 0 {
					tl[0] = []byte("ZZ")
					tl = tl[1:]
				}
				for i := range tl {
					tl[i] = nil
				}
				if b.logicalLen != logical || b.wireLen() != wire {
					t.Fatalf("n=%d: batch metadata changed", n)
				}
				if len(b.parts) != len(snaps) {
					t.Fatalf("n=%d: parts length changed", n)
				}
				for i, s := range snaps {
					if len(b.parts[i]) != len(s.p) || (len(s.p) > 0 && &b.parts[i][0] != &s.p[0]) {
						t.Fatalf("n=%d: part %d slice header changed", n, i)
					}
					if !bytes.Equal(b.parts[i], s.cp) {
						t.Fatalf("n=%d: part %d bytes changed", n, i)
					}
				}
			}
			if full := btFlatten(b.tail(wire)); len(full) != 0 {
				t.Fatalf("tail(wireLen) has %d bytes", len(full))
			}
			if !bytes.Equal(btFlatten(b.tail(0)), original) {
				t.Fatalf("tail(0) != original")
			}
			if b.logicalLen == b.wireLen() && name == "unicode" {
				t.Fatalf("unicode logical and physical lengths must differ")
			}
		})
	}
}
