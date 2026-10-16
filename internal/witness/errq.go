package witness

import (
	"fmt"
	"strings"
)

// errQueue ports OpenSSL's per-thread error queue (crypto/err/err.c,
// err_local.h, err_mark.c): a ring of ERR_NUM_ERRORS = 16 slots in which
// one slot is always the empty "bottom", so it holds at most 15 errors and
// a new error overwrites the oldest. Marks live on slots, as in OpenSSL.
// Node reports the oldest error left in the queue as the message and every
// error, newest first, as opensslErrorStack.
type errQueue struct {
	buf   [16]uint32
	marks [16]int
	top   int
	bot   int
	// trace, when set (tests), records every call as the traced OpenSSL
	// build prints it: "E<code> " per error, "M ", "P ", "C " per
	// ERR_set_mark, ERR_pop_to_mark, ERR_clear_last_mark.
	trace *strings.Builder
}

// osslReason is one ERR_R_* or <LIB>_R_* reason: lib is 0 for the common
// reasons, which take the library of the caller.
type osslReason struct {
	lib    uint32
	reason uint32
	text   string
}

// errCode packs lib and reason as ERR_PACK does (lib << 23 | reason).
// ERR_raise(lib, reason) always uses the caller's lib, even with another
// library's reason (ERR_raise(ERR_LIB_DH, EVP_R_DECODE_ERROR)).
func errCode(lib uint32, r osslReason) uint32 {
	return lib<<23 | r.reason
}

// Library numbers (ERR_LIB_*).
const (
	libBN      = 3
	libRSA     = 4
	libDH      = 5
	libEVP     = 6
	libOBJ     = 8
	libPEM     = 9
	libDSA     = 10
	libX509    = 11
	libASN1    = 13
	libCRYPTO  = 15
	libEC      = 16
	libPROV    = 57
	libDECODER = 60
)

// raise is ERR_raise(lib, reason): name is a key of osslReasons or
// osslCommon.
func (q *errQueue) raise(lib uint32, reason string) {
	r, ok := osslReasons[reason]
	if !ok {
		if r, ok = osslCommon[reason]; !ok {
			panic("unknown OpenSSL reason " + reason)
		}
	}
	q.push(errCode(lib, r))
}

// push ports ERR_new: err_get_slot then err_clear (which drops the slot's
// marks).
func (q *errQueue) push(code uint32) {
	if q.trace != nil {
		fmt.Fprintf(q.trace, "E%08x ", code)
	}
	q.top = (q.top + 1) % 16
	if q.top == q.bot {
		q.bot = (q.bot + 1) % 16
	}
	q.buf[q.top] = code
	q.marks[q.top] = 0
}

// raiseDataNested ports ERR_raise_data(lib, reason, fmt, arg) whose
// argument raises innerReason first: the macro's ERR_new takes a slot (cleared, code 0)
// before the arguments are evaluated, inner's ERR_raise takes the next
// one, and ERR_set_error then writes lib/name over inner's slot. The
// zero slot stays in the queue, and ERR_get_error stops at it.
func (q *errQueue) raiseDataNested(lib uint32, reason string, innerLib uint32, innerReason string) {
	q.top = (q.top + 1) % 16
	if q.top == q.bot {
		q.bot = (q.bot + 1) % 16
	}
	q.buf[q.top] = 0
	q.marks[q.top] = 0
	q.raise(innerLib, innerReason)
	code := errCode(lib, osslReasons[reason])
	if q.trace != nil {
		fmt.Fprintf(q.trace, "E%08x ", code)
	}
	q.buf[q.top] = code
}

func (q *errQueue) empty() bool { return q.top == q.bot }

// peekFirst is ERR_peek_error: the oldest error, 0 if none.
func (q *errQueue) peekFirst() uint32 {
	if q.empty() {
		return 0
	}
	return q.buf[(q.bot+1)%16]
}

// peekLast is ERR_peek_last_error: the newest error, 0 if none.
func (q *errQueue) peekLast() uint32 {
	if q.empty() {
		return 0
	}
	return q.buf[q.top]
}

// setMark is ERR_set_mark: a no-op on an empty queue.
func (q *errQueue) setMark() {
	q.note("M ")
	if !q.empty() {
		q.marks[q.top]++
	}
}

// popToMark is ERR_pop_to_mark: drop the errors raised since the newest
// mark and remove that mark.
func (q *errQueue) popToMark() {
	q.note("P ")
	for !q.empty() && q.marks[q.top] == 0 {
		q.buf[q.top] = 0
		q.top = (q.top + 15) % 16
	}
	if !q.empty() {
		q.marks[q.top]--
	}
}

// clearLastMark is ERR_clear_last_mark: remove the newest mark, keep the
// errors.
func (q *errQueue) clearLastMark() {
	q.note("C ")
	top := q.top
	for q.bot != top && q.marks[top] == 0 {
		top = (top + 15) % 16
	}
	if q.bot != top {
		q.marks[top]--
	}
}

func (q *errQueue) note(s string) {
	if q.trace != nil {
		q.trace.WriteString(s)
	}
}

// clear is ERR_clear_error.
func (q *errQueue) clear() { *q = errQueue{trace: q.trace} }

// errors lists the queue oldest first as repeated ERR_get_error calls
// see it: up to the first empty (zero) slot.
func (q *errQueue) errors() []uint32 {
	var out []uint32
	for i := (q.bot + 1) % 16; q.bot != q.top; i = (i + 1) % 16 {
		if q.buf[i] == 0 {
			break
		}
		out = append(out, q.buf[i])
		if i == q.top {
			break
		}
	}
	return out
}

// errorString is ERR_error_string_n as Node formats it:
// "error:%08X:<library>:<function>:<reason>", with an empty function.
func errorString(code uint32) string {
	lib := code >> 23
	reason := code & 0x7FFFFF
	text := osslReasonText(lib, reason)
	if text == "" {
		text = fmt.Sprintf("reason(%d)", reason&^(0x1F<<18))
	}
	ls, ok := osslLibs[lib]
	if !ok {
		ls = fmt.Sprintf("lib(%d)", lib)
	}
	return fmt.Sprintf("error:%08X:%s::%s", code, ls, text)
}

// keyError is the error Node throws from createPublicKey/createPrivateKey:
// the oldest queued error is the message; stack is opensslErrorStack.
type keyError struct {
	message string
	stack   []string
}

func (e *keyError) Error() string { return e.message }

// nodeError captures the queue as Node's CryptoErrorStore does: message is
// ERR_error_string_n of err (the error ncrypto peeked), the stack every
// queued error newest first except the one used as the message.
func nodeError(q *errQueue, err uint32) *keyError {
	codes := q.errors()
	var all []string
	for i := len(codes) - 1; i >= 0; i-- {
		all = append(all, errorString(codes[i]))
	}
	return &keyError{message: errorString(err), stack: all}
}

var reasonTextIndex = func() map[[2]uint32]string {
	m := map[[2]uint32]string{}
	for _, r := range osslReasons {
		m[[2]uint32{r.lib, r.reason}] = r.text
	}
	for _, r := range osslCommon {
		m[[2]uint32{0, r.reason}] = r.text
	}
	return m
}()

// osslReasonText is ERR_reason_error_string: the library's own table,
// then the common one.
func osslReasonText(lib, reason uint32) string {
	if t, ok := reasonTextIndex[[2]uint32{lib, reason}]; ok {
		return t
	}
	return reasonTextIndex[[2]uint32{0, reason}]
}
