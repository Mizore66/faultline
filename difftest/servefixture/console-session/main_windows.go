//go:build windows

// Command console-session is TEST-ONLY Windows support, not part of fl. It
// drives an interactive program in a console of its own, the way a person at
// a terminal would: it hosts the program (with the console's real input and
// output handles), types key events into the console, and reads the console
// screen buffer.
//
//	console-session host <dir> <exe> [args...]
//	    Runs exe with the console as stdin/stdout and stderr redirected to
//	    <dir>/stderr, writes the exit code to <dir>/exit, then keeps the console
//	    alive until <dir>/release exists, so the final screen can be read.
//	console-session send <pid> <keys>
//	    Attaches to the console of <pid> and writes key events. <keys> is text
//	    plus tokens such as <enter>, <bs>, <ctrl-c>, <ctrl-d>, <ctrl-w>,
//	    <ctrl-u>, <left>, <right>, <home>, <end>, <del>.
//	console-session screen <pid>
//	    Prints the console screen text (trailing blanks and blank rows removed).
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

var (
	kernel     = syscall.NewLazyDLL("kernel32.dll")
	pFree      = kernel.NewProc("FreeConsole")
	pAttach    = kernel.NewProc("AttachConsole")
	pWriteIn   = kernel.NewProc("WriteConsoleInputW")
	pScreen    = kernel.NewProc("GetConsoleScreenBufferInfo")
	pReadChars = kernel.NewProc("ReadConsoleOutputCharacterW")
)

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "console-session: "+format+"\n", a...)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 3 {
		die("usage: host|send|screen ...")
	}
	switch os.Args[1] {
	case "host":
		if len(os.Args) < 4 {
			die("usage: host <dir> <exe> [args...]")
		}
		host(os.Args[2], os.Args[3], os.Args[4:])
	case "send":
		if len(os.Args) != 4 {
			die("usage: send <pid> <keys>")
		}
		attach(os.Args[2])
		send(os.Args[3])
	case "screen":
		if len(os.Args) != 3 {
			die("usage: screen <pid>")
		}
		attach(os.Args[2])
		screen()
	default:
		die("unknown mode %q", os.Args[1])
	}
}

func host(dir, exe string, args []string) {
	stderr, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		die("stderr file: %v", err)
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, stderr
	code := 0
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			die("run: %v", err)
		}
	}
	stderr.Close()
	if err := os.WriteFile(filepath.Join(dir, "exit"), []byte(strconv.Itoa(code)), 0o600); err != nil {
		die("exit file: %v", err)
	}
	for end := time.Now().Add(60 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(dir, "release")); err == nil {
			return
		}
	}
}

func attach(pidText string) {
	pid, err := strconv.ParseUint(pidText, 10, 32)
	if err != nil || pid == 0 {
		die("bad pid %q", pidText)
	}
	if ok, _, err := pFree.Call(); ok == 0 && err != syscall.Errno(6) {
		die("FreeConsole: %v", err)
	}
	if ok, _, err := pAttach.Call(uintptr(pid)); ok == 0 {
		die("AttachConsole: %v", err)
	}
}

const (
	leftCtrl = 0x0008
	enhanced = 0x0100
)

type inputRecord struct {
	EventType uint16
	_         uint16
	Event     [16]byte
}

type keyEvent struct {
	KeyDown     int32
	Repeat      uint16
	VirtualKey  uint16
	ScanCode    uint16
	UnicodeChar uint16
	State       uint32
}

func record(down bool, vk, ch uint16, state uint32) inputRecord {
	r := inputRecord{EventType: 1}
	k := (*keyEvent)(unsafe.Pointer(&r.Event[0]))
	if down {
		k.KeyDown = 1
	}
	k.Repeat, k.VirtualKey, k.UnicodeChar, k.State = 1, vk, ch, state
	return r
}

func keyPair(vk, ch uint16, state uint32) []inputRecord {
	return []inputRecord{record(true, vk, ch, state), record(false, vk, ch, state)}
}

var tokens = map[string][]inputRecord{
	"enter":  keyPair(0x0D, '\r', 0),
	"bs":     keyPair(0x08, 0x08, 0),
	"ctrl-c": keyPair(0x43, 0x03, leftCtrl),
	"ctrl-d": keyPair(0x44, 0x04, leftCtrl),
	"ctrl-w": keyPair(0x57, 0x17, leftCtrl),
	"ctrl-u": keyPair(0x55, 0x15, leftCtrl),
	"ctrl-a": keyPair(0x41, 0x01, leftCtrl),
	"ctrl-e": keyPair(0x45, 0x05, leftCtrl),
	"ctrl-k": keyPair(0x4B, 0x0B, leftCtrl),
	"left":   keyPair(0x25, 0, enhanced),
	"right":  keyPair(0x27, 0, enhanced),
	"up":     keyPair(0x26, 0, enhanced),
	"down":   keyPair(0x28, 0, enhanced),
	"home":   keyPair(0x24, 0, enhanced),
	"end":    keyPair(0x23, 0, enhanced),
	"del":    keyPair(0x2E, 0, enhanced),
}

func send(spec string) {
	in, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		die("CONIN$: %v", err)
	}
	defer in.Close()
	var records []inputRecord
	for len(spec) > 0 {
		if spec[0] == '<' {
			end := strings.IndexByte(spec, '>')
			if end < 0 {
				die("unterminated token in %q", spec)
			}
			recs, ok := tokens[spec[1:end]]
			if !ok {
				die("unknown token %q", spec[:end+1])
			}
			records = append(records, recs...)
			spec = spec[end+1:]
			continue
		}
		r := []rune(spec)[0]
		spec = spec[len(string(r)):]
		for _, u := range utf16.Encode([]rune{r}) {
			records = append(records, keyPair(0, u, 0)...)
		}
	}
	for i := 0; i < len(records); i++ { // one event at a time, in order
		var n uint32
		if ok, _, err := pWriteIn.Call(in.Fd(), uintptr(unsafe.Pointer(&records[i])), 1, uintptr(unsafe.Pointer(&n))); ok == 0 || n != 1 {
			die("WriteConsoleInputW: %v", err)
		}
	}
}

func screen() {
	out, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		die("CONOUT$: %v", err)
	}
	defer out.Close()
	var info struct {
		SizeX, SizeY     int16
		CursorX, CursorY int16
		Attributes       uint16
		Left, Top        int16
		Right, Bottom    int16
		MaxX, MaxY       int16
	}
	if ok, _, err := pScreen.Call(out.Fd(), uintptr(unsafe.Pointer(&info))); ok == 0 {
		die("GetConsoleScreenBufferInfo: %v", err)
	}
	var lines []string
	for y := int16(0); y <= info.CursorY; y++ {
		buf := make([]uint16, info.SizeX)
		var n uint32
		coord := uintptr(uint32(uint16(0)) | uint32(uint16(y))<<16)
		if ok, _, err := pReadChars.Call(out.Fd(), uintptr(unsafe.Pointer(&buf[0])), uintptr(info.SizeX), coord, uintptr(unsafe.Pointer(&n))); ok == 0 {
			die("ReadConsoleOutputCharacterW: %v", err)
		}
		lines = append(lines, strings.TrimRight(string(utf16.Decode(buf[:n])), " "))
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	fmt.Print(strings.Join(lines, "\n"))
}
