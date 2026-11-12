//go:build windows

package term

import (
	"errors"
	"io"
	"os"
	"sync"
	"syscall"
	"unsafe"
)

// This is the Windows console half of the raw input path. libuv's
// uv_tty_set_mode(UV_TTY_MODE_RAW) REPLACES the input console mode with
// ENABLE_WINDOW_INPUT (no echo, no line input, no processed input, so Ctrl+C
// arrives as byte 0x03 and is not a signal), and the TTY stream reads
// KEY_EVENT_RECORDs with ReadConsoleInputW, translating them as KeyTranslator
// does. Output is written with VT processing enabled
// (ENABLE_VIRTUAL_TERMINAL_PROCESSING), which is how Node's console writes
// the readline escape sequences.

const (
	enableProcessedInput = 0x0001
	enableLineInput      = 0x0002
	enableEchoInput      = 0x0004
	enableWindowInput    = 0x0008

	enableProcessedOutput           = 0x0001
	enableVirtualTerminalProcessing = 0x0004

	keyEventType          = 0x0001
	windowBufferSizeEvent = 0x0004
)

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode        = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode        = kernel32.NewProc("SetConsoleMode")
	procReadConsoleInputW     = kernel32.NewProc("ReadConsoleInputW")
	procGetConsoleScreenBuf   = kernel32.NewProc("GetConsoleScreenBufferInfo")
	procWaitForSingleObject   = kernel32.NewProc("WaitForSingleObject")
	procGetNumberOfConsoleEvt = kernel32.NewProc("GetNumberOfConsoleInputEvents")
)

func getMode(h syscall.Handle) (uint32, bool) {
	var m uint32
	r, _, _ := procGetConsoleMode.Call(uintptr(h), uintptr(unsafe.Pointer(&m)))
	return m, r != 0
}

func setMode(h syscall.Handle, m uint32) bool {
	r, _, _ := procSetConsoleMode.Call(uintptr(h), uintptr(m))
	return r != 0
}

// inputRecord is INPUT_RECORD: EventType, padding, then a 16-byte union that
// holds a KEY_EVENT_RECORD or a WINDOW_BUFFER_SIZE_RECORD.
type inputRecord struct {
	EventType uint16
	_         uint16
	Event     [16]byte
}

// keyEventRecord is the KEY_EVENT_RECORD layout inside the union.
type keyEventRecord struct {
	KeyDown         int32
	RepeatCount     uint16
	VirtualKeyCode  uint16
	VirtualScanCode uint16
	UnicodeChar     uint16
	ControlKeyState uint32
}

// MakeRaw switches the console input handle to libuv's raw mode and turns on
// VT processing for the output handle. restore returns both modes.
func MakeRaw(file *os.File) (restore func(), ok bool) {
	h := syscall.Handle(file.Fd())
	orig, got := getMode(h)
	if !got {
		return nil, false
	}
	if !setMode(h, enableWindowInput) {
		return nil, false
	}
	return func() { setMode(h, orig) }, true
}

// MakeRawAgain is a no-op here: Windows has no job control to resume from.
func MakeRawAgain(file *os.File) {}

// EnableVT turns on ENABLE_VIRTUAL_TERMINAL_PROCESSING for a console output
// handle (as libuv does when it first writes) and returns the restore.
func EnableVT(file *os.File) (restore func()) {
	h := syscall.Handle(file.Fd())
	orig, ok := getMode(h)
	if !ok {
		return func() {}
	}
	setMode(h, orig|enableProcessedOutput|enableVirtualTerminalProcessing)
	return func() { setMode(h, orig) }
}

// Columns is the console window's width in columns (the screen buffer's
// visible window), or 0 when unknown.
func Columns(file *os.File) int {
	if file == nil {
		return 0
	}
	var info struct {
		SizeX, SizeY     int16
		CursorX, CursorY int16
		Attributes       uint16
		Left, Top        int16
		Right, Bottom    int16
		MaxX, MaxY       int16
	}
	r, _, _ := procGetConsoleScreenBuf.Call(file.Fd(), uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return 0
	}
	return int(info.Right-info.Left) + 1
}

// ConsoleReader reads a raw-mode console as a byte stream of libuv's key
// translation. A resize event is reported on Resize.
type ConsoleReader struct {
	h      syscall.Handle
	tr     KeyTranslator
	mu     sync.Mutex
	buf    []byte
	closed chan struct{}
	once   sync.Once
	Resize chan struct{}
}

func NewConsoleReader(file *os.File) *ConsoleReader {
	return &ConsoleReader{h: syscall.Handle(file.Fd()), closed: make(chan struct{}), Resize: make(chan struct{}, 1)}
}

// Close makes a pending Read return io.EOF.
func (c *ConsoleReader) Close() { c.once.Do(func() { close(c.closed) }) }

func (c *ConsoleReader) Read(p []byte) (int, error) {
	for len(c.buf) == 0 {
		select {
		case <-c.closed:
			return 0, io.EOF
		default:
		}
		// Wait for console input in slices so Close is noticed.
		r, _, _ := procWaitForSingleObject.Call(uintptr(c.h), 100)
		if r != 0 { // WAIT_TIMEOUT (258) or failure
			if r == 0xFFFFFFFF {
				return 0, errors.New("console wait failed")
			}
			continue
		}
		var left uint32
		if ok, _, _ := procGetNumberOfConsoleEvt.Call(uintptr(c.h), uintptr(unsafe.Pointer(&left))); ok == 0 {
			return 0, errors.New("console event count failed")
		}
		for ; left > 0; left-- {
			var rec inputRecord
			var n uint32
			if ok, _, _ := procReadConsoleInputW.Call(uintptr(c.h), uintptr(unsafe.Pointer(&rec)), 1, uintptr(unsafe.Pointer(&n))); ok == 0 || n == 0 {
				return 0, errors.New("ReadConsoleInputW failed")
			}
			switch rec.EventType {
			case windowBufferSizeEvent:
				select {
				case c.Resize <- struct{}{}:
				default:
				}
			case keyEventType:
				k := (*keyEventRecord)(unsafe.Pointer(&rec.Event[0]))
				c.buf = append(c.buf, c.tr.Translate(KeyEvent{
					KeyDown:        k.KeyDown != 0,
					RepeatCount:    k.RepeatCount,
					VirtualKeyCode: k.VirtualKeyCode,
					UnicodeChar:    k.UnicodeChar,
					ControlState:   k.ControlKeyState,
				})...)
			}
		}
	}
	n := copy(p, c.buf)
	c.buf = c.buf[n:]
	return n, nil
}
