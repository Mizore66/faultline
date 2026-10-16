package nodeproc

import (
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// This is libuv 1.51's reading of a child's stdout and stderr on Windows
// (src/win/pipe.c): each parent end is an overlapped named pipe server
// associated with the loop's completion port; a read is a zero-byte
// overlapped ReadFile, whose completion means data (or EOF) is there; the
// loop takes completions from GetQueuedCompletionStatus in the order the
// kernel queued them, reads what is available in 64 KiB ReadFiles, hands
// each chunk to its 'data' listener, and posts the next zero read. So data
// the child wrote to stderr before stdout reaches the listeners in that
// order, as with epoll (round 7 #7).

var (
	procCreateNamedPipeA = kernel32.NewProc("CreateNamedPipeA")
	procPeekNamedPipe    = kernel32.NewProc("PeekNamedPipe")
	procCreateIoCP       = kernel32.NewProc("CreateIoCompletionPort")
	procGetQueuedStatus  = kernel32.NewProc("GetQueuedCompletionStatus")
	procReadFile         = kernel32.NewProc("ReadFile")
	procCancelIoEx       = kernel32.NewProc("CancelIoEx")
	procCreateEventW     = kernel32.NewProc("CreateEventW")
	procGetOverlapped    = kernel32.NewProc("GetOverlappedResult")
	pipeSerial           atomic.Uint64
)

const (
	pipeAccessInbound     = 0x00000001
	fileFlagFirstInstance = 0x00080000
	fileFlagOverlapped    = 0x40000000
	writeDAC              = 0x00040000
	pipeTypeByteReadByte  = 0 // PIPE_TYPE_BYTE | PIPE_READMODE_BYTE | PIPE_WAIT
	errorPipeBusy         = syscall.Errno(231)
	errorAccessDenied     = syscall.Errno(5)
	errorBrokenPipe       = syscall.ERROR_BROKEN_PIPE
	errorIOPending        = syscall.ERROR_IO_PENDING
	errorOperationAborted = syscall.ERROR_OPERATION_ABORTED
	errorMoreData         = syscall.ERROR_MORE_DATA
	genericWrite          = 0x40000000
	fileReadAttributes    = 0x0080
	invalidHandleValue    = ^uintptr(0)
)

// overlappedPipe is uv__create_pipe_pair for a readable slot: a named pipe
// \\.\pipe\uv\<ptr>-<pid> created with PIPE_ACCESS_INBOUND |
// FILE_FLAG_OVERLAPPED | WRITE_DAC | FILE_FLAG_FIRST_PIPE_INSTANCE, byte
// mode, one instance, 64 KiB buffers; the client end, opened with
// GENERIC_WRITE | FILE_READ_ATTRIBUTES and no overlapped flag, is the
// child's (inheritable once os.StartProcess duplicates it into the child).
func overlappedPipe() (uintptr, *os.File, error) {
	for {
		name := `\\.\pipe\uv\` + strconv.FormatUint(pipeSerial.Add(1), 16) + "-" + strconv.Itoa(os.Getpid())
		cname := append([]byte(name), 0)
		h, _, e := procCreateNamedPipeA.Call(uintptr(unsafe.Pointer(&cname[0])),
			pipeAccessInbound|fileFlagOverlapped|writeDAC|fileFlagFirstInstance,
			pipeTypeByteReadByte, 1, 65536, 65536, 0, 0)
		if h == invalidHandleValue {
			if e == errorPipeBusy || e == errorAccessDenied {
				continue // the name is taken: libuv tries the next one
			}
			return 0, nil, e
		}
		wname, _ := syscall.UTF16PtrFromString(name)
		client, err := syscall.CreateFile(wname, genericWrite|fileReadAttributes, 0, nil, syscall.OPEN_EXISTING, 0, 0)
		if err != nil {
			syscall.CloseHandle(syscall.Handle(h))
			return 0, nil, err
		}
		return h, os.NewFile(uintptr(client), "|1"), nil
	}
}

// newCompletionPort is the loop's port, with the non-zero handles
// associated in order under keys 1, 2, ...: Spawn lists the raw streams in
// the same order (stdout, then stderr), so key k is stream k-1.
func newCompletionPort(handles ...uintptr) (uintptr, error) {
	port, _, e := procCreateIoCP.Call(invalidHandleValue, 0, 0, 1)
	if port == 0 {
		return 0, e
	}
	key := uintptr(1)
	for _, h := range handles {
		if h == 0 {
			continue
		}
		if r, _, e := procCreateIoCP.Call(h, port, key, 0); r == 0 {
			syscall.CloseHandle(syscall.Handle(port))
			return 0, e
		}
		key++
	}
	return port, nil
}

func closeRaw(h uintptr) { syscall.CloseHandle(syscall.Handle(h)) }

// zeroRead is one stream's pending uv__pipe_queue_read.
type zeroRead struct {
	ov   syscall.Overlapped
	zero [1]byte
}

// pumpPolled runs libuv's loop over the raw pipe handles; streams with an
// *os.File (none today) go to pumpEach.
func pumpPolled(streams []pipedStream, port uintptr) []pipedStream {
	var rest, raw []pipedStream
	for _, s := range streams {
		if s.raw != 0 {
			raw = append(raw, s)
		} else {
			rest = append(rest, s)
		}
	}
	if len(raw) == 0 {
		return rest
	}
	reqs := make([]*zeroRead, len(raw))
	defer func() {
		// Only an aborted loop leaves reads pending: cancel them and keep
		// their OVERLAPPED alive until the handles are closed.
		for i, s := range raw {
			if reqs[i] != nil {
				procCancelIoEx.Call(s.raw, uintptr(unsafe.Pointer(&reqs[i].ov)))
			}
			closeRaw(s.raw)
		}
		closeRaw(port)
		runtime.KeepAlive(reqs)
	}()
	open := len(raw)
	ended := make([]bool, len(raw))
	end := func(i int) {
		if !ended[i] {
			ended[i] = true
			open--
		}
	}
	queue := func(i int) {
		r := &zeroRead{}
		reqs[i] = r
		// ReadFile(handle, &uv_zero_, 0, NULL, &overlapped).
		ok, _, err := procReadFile.Call(raw[i].raw, uintptr(unsafe.Pointer(&r.zero[0])), 0, 0, uintptr(unsafe.Pointer(&r.ov)))
		if ok == 0 && err != errorIOPending {
			// A failed queue is reported through the port in libuv
			// (uv__insert_pending_req); here it ends the stream.
			reqs[i] = nil
			end(i)
		}
	}
	for i := range raw {
		queue(i)
	}
	for open > 0 {
		var qty uint32
		var key uintptr
		var ov *syscall.Overlapped
		r, _, e := procGetQueuedStatus.Call(port, uintptr(unsafe.Pointer(&qty)), uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&ov)), syscall.INFINITE)
		if ov == nil {
			if r == 0 {
				// The port itself failed (libuv aborts here): stop
				// reading, which ends the remaining streams.
				return rest
			}
			continue
		}
		i := int(key) - 1
		if i < 0 || i >= len(raw) || ended[i] || reqs[i] == nil || ov != &reqs[i].ov {
			continue
		}
		reqs[i] = nil
		if r == 0 && e != errorMoreData {
			// uv__process_pipe_read_req: a failed zero read is EOF for
			// ERROR_BROKEN_PIPE and an error otherwise; both end the
			// stream (Node emits 'end' or 'error', then 'close').
			end(i)
			continue
		}
		if readAvailable(raw[i]) {
			end(i)
			continue
		}
		queue(i)
	}
	return rest
}

// readAvailable is the read loop of uv__process_pipe_read_req: while data
// is available, read up to 64 KiB (uv__pipe_read_data) and hand it to the
// listener; a read that fills the buffer means there may be more. It
// reports whether the stream ended (EOF or an error).
func readAvailable(s pipedStream) bool {
	var buf []byte
	for {
		var avail uint32
		if r, _, _ := procPeekNamedPipe.Call(s.raw, 0, 0, 0, uintptr(unsafe.Pointer(&avail)), 0); r == 0 {
			return true // the write end is closed: EOF
		}
		if avail == 0 {
			return false
		}
		if buf == nil {
			buf = make([]byte, segment)
		}
		want := min(uint32(len(buf)), avail)
		n, err := overlappedRead(s.raw, buf[:want])
		if err != nil {
			return true
		}
		if n > 0 {
			data := buf[:n]
			buf = nil // the listener owns data
			if s.listen != nil {
				s.listen(data)
			}
		}
		if n < segment {
			return false
		}
	}
}

// overlappedRead is a ReadFile of available data on the overlapped handle,
// waited for with an event so that the completion does not reach the port
// (an event handle with its low bit set suppresses the port notification).
func overlappedRead(h uintptr, buf []byte) (int, error) {
	ev, _, e := procCreateEventW.Call(0, 1, 0, 0)
	if ev == 0 {
		return 0, e
	}
	defer syscall.CloseHandle(syscall.Handle(ev))
	ov := syscall.Overlapped{HEvent: syscall.Handle(ev | 1)}
	var done uint32
	err := syscall.ReadFile(syscall.Handle(h), buf, &done, &ov)
	if err == errorIOPending {
		err = nil
		if r, _, e := procGetOverlapped.Call(h, uintptr(unsafe.Pointer(&ov)), uintptr(unsafe.Pointer(&done)), 1); r == 0 {
			err = e
		}
	}
	if err != nil && err != errorMoreData {
		return int(done), err
	}
	return int(done), nil
}
