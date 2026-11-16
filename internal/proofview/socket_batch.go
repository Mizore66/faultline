package proofview

import (
	"strconv"

	"github.com/Mizore66/faultline/internal/jsstr"
)

// socketBodyFraming selects how a text body is framed on the wire. The caller
// decides the policy; this file only encodes bytes.
type socketBodyFraming uint8

const (
	socketBodyChunked socketBodyFraming = iota
	socketBodyCloseDelimited
	socketBodySuppressed
)

// socketWriteBatch is one logical write operation. parts are the physical
// bytes in wire order; logicalLen is what Node retires on whole-operation
// completion (header bytes, framing bytes, and body UTF-16 units) and is never
// reduced by partially accepted physical bytes.
type socketWriteBatch struct {
	parts      [][]byte
	logicalLen int
}

const socketChunkTerminator = "0\r\n\r\n"

// makeSocketTextBatch copies the opaque, already-serialized header (including
// its terminating CRLFCRLF) and encodes body as UTF-8 for the wire. The
// logical length counts the body in JS UTF-16 units.
func makeSocketTextBatch(header []byte, body string, framing socketBodyFraming) socketWriteBatch {
	if framing == socketBodySuppressed {
		return socketWriteBatch{parts: [][]byte{append([]byte(nil), header...)}, logicalLen: len(header)}
	}
	units := jsstr.UTF16Len(body)
	encoded := []byte(jsstr.ToUTF8(body))
	if framing == socketBodyCloseDelimited {
		parts := [][]byte{append([]byte(nil), header...)}
		if len(encoded) > 0 {
			parts = append(parts, encoded)
		}
		return socketWriteBatch{parts: parts, logicalLen: len(header) + units}
	}
	if len(encoded) == 0 {
		first := append(append([]byte(nil), header...), socketChunkTerminator...)
		return socketWriteBatch{parts: [][]byte{first}, logicalLen: len(first)}
	}
	size := strconv.FormatInt(int64(len(encoded)), 16)
	first := append(append(make([]byte, 0, len(header)+len(size)), header...), size...)
	return socketWriteBatch{
		parts: [][]byte{
			first,
			[]byte("\r\n"),
			encoded,
			[]byte("\r\n"),
			[]byte(socketChunkTerminator),
		},
		logicalLen: len(header) + len(size) + 2 + units + 2 + len(socketChunkTerminator),
	}
}

// wireLen is the total physical byte count.
func (b socketWriteBatch) wireLen() int {
	n := 0
	for _, p := range b.parts {
		n += len(p)
	}
	return n
}

// tail returns the physical bytes remaining after accepted bytes, for
// 0 <= accepted <= wireLen (validated by the caller). The returned outer slice
// is fresh; byte storage is shared and immutable. logicalLen is unchanged.
func (b socketWriteBatch) tail(accepted int) [][]byte {
	out := make([][]byte, 0, len(b.parts))
	for _, p := range b.parts {
		if accepted >= len(p) {
			accepted -= len(p)
			continue
		}
		out = append(out, p[accepted:])
		accepted = 0
	}
	return out
}
