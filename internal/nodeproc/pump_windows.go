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
// (src/win/pipe.c): each parent end is a duplex overlapped named pipe
// server (Node's pipe slots are UV_CREATE_PIPE | UV_READABLE_PIPE |
// UV_WRITABLE_PIPE) associated with the loop's completion port; a read is a zero-byte
// overlapped ReadFile, whose completion means data (or EOF) is there; the
// loop takes completions from GetQueuedCompletionStatus in the order the
// kernel queued them, reads in 64 KiB overlapped ReadFiles until one comes
// back short, hands each chunk to its 'data' listener, and posts the next
// zero read. So data
// the child wrote to stderr before stdout reaches the listeners in that
// order, as with epoll (round 7 #7).

var (
	procCreateNamedPipeA = kernel32.NewProc("CreateNamedPipeA")
	procCreateIoCP       = kernel32.NewProc("CreateIoCompletionPort")
	procGetQueuedStatus  = kernel32.NewProc("GetQueuedCompletionStatus")
	procReadFile         = kernel32.NewProc("ReadFile")
	procCancelIoEx       = kernel32.NewProc("CancelIoEx")
	procCreateEventW     = kernel32.NewProc("CreateEventW")
	procGetOverlapped    = kernel32.NewProc("GetOverlappedResult")
	procConnectNamedPipe = kernel32.NewProc("ConnectNamedPipe")
	pipeSerial           atomic.Uint64
)

const (
	pipeAccessInbound     = 0x00000001
	pipeAccessOutbound    = 0x00000002
	errorPipeConnected    = syscall.Errno(535)
	genericRead           = 0x80000000
	fileFlagFirstInstance = 0x00080000
	fileFlagOverlapped    = 0x40000000
	writeDAC              = 0x00040000
	pipeTypeByteReadByte  = 0 // PIPE_TYPE_BYTE | PIPE_READMODE_BYTE | PIPE_WAIT
	errorPipeBusy         = syscall.Errno(231)
	errorAccessDenied     = syscall.Errno(5)
	errorBrokenPipe       = syscall.ERROR_BROKEN_PIPE
	errorIOPending        = syscall.ERROR_IO_PENDING
	errorOperationAborted = syscall.ERROR_OPERATION_ABORTED
	genericWrite          = 0x40000000
	invalidHandleValue    = ^uintptr(0)
)

// overlappedPipe is uv__create_stdio_pipe_pair for one of Node's pipe
// slots (UV_CREATE_PIPE | UV_READABLE_PIPE | UV_WRITABLE_PIPE), through
// uv__create_pipe_pair: the server, fl's end, is a named pipe
// \\?\pipe\uv\<n>-<pid> created with PIPE_ACCESS_INBOUND |
// PIPE_ACCESS_OUTBOUND | FILE_FLAG_OVERLAPPED | WRITE_DAC |
// FILE_FLAG_FIRST_PIPE_INSTANCE, byte mode, one instance, 64 KiB buffers
// (a name in use is retried with the next); the client, the child's end, is
// opened with GENERIC_READ | GENERIC_WRITE | WRITE_DAC, no sharing and no
// overlapped flag; then ConnectNamedPipe joins the two, ERROR_PIPE_CONNECTED
// counting as success. os.StartProcess makes the client inheritable in the
// child.
func overlappedPipe() (uintptr, *os.File, error) {
	for {
		name := `\\?\pipe\uv\` + strconv.FormatUint(pipeSerial.Add(1), 10) + "-" + strconv.Itoa(os.Getpid())
		cname := append([]byte(name), 0)
		h, _, e := procCreateNamedPipeA.Call(uintptr(unsafe.Pointer(&cname[0])),
			pipeAccessInbound|pipeAccessOutbound|fileFlagOverlapped|writeDAC|fileFlagFirstInstance,
			pipeTypeByteReadByte, 1, 65536, 65536, 0, 0)
		if h == invalidHandleValue {
			if e == errorPipeBusy || e == errorAccessDenied {
				continue // the name is taken: libuv tries the next one
			}
			return 0, nil, e
		}
		wname, _ := syscall.UTF16PtrFromString(name)
		client, err := syscall.CreateFile(wname, genericRead|genericWrite|writeDAC, 0, nil, syscall.OPEN_EXISTING, 0, 0)
		if err != nil {
			syscall.CloseHandle(syscall.Handle(h))
			return 0, nil, err
		}
		if err := connectServer(h); err != nil {
			syscall.CloseHandle(client)
			syscall.CloseHandle(syscall.Handle(h))
			return 0, nil, err
		}
		return h, os.NewFile(uintptr(client), "|1"), nil
	}
}

// connectServer is uv__create_pipe_pair's ConnectNamedPipe(server, NULL).
// The server is overlapped, so the call is given an OVERLAPPED with a
// private event and waited for (it completes at once: the client is
// already open); ERROR_PIPE_CONNECTED is success.
func connectServer(h uintptr) error {
	ev, _, e := procCreateEventW.Call(0, 1, 0, 0)
	if ev == 0 {
		return e
	}
	defer syscall.CloseHandle(syscall.Handle(ev))
	ov := syscall.Overlapped{HEvent: syscall.Handle(ev | 1)}
	r, _, e := procConnectNamedPipe.Call(h, uintptr(unsafe.Pointer(&ov)))
	switch {
	case r != 0 || e == errorPipeConnected:
		return nil
	case e == errorIOPending:
		var done uint32
		if r, _, e := procGetOverlapped.Call(h, uintptr(unsafe.Pointer(&ov)), uintptr(unsafe.Pointer(&done)), 1); r == 0 && e != errorPipeConnected {
			return e
		}
		return nil
	}
	return e
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
	events := make([]uintptr, len(raw))
	defer func() {
		// Only an aborted loop leaves reads pending: cancel each, wait for
		// the cancelled request to finish (the kernel writes its
		// OVERLAPPED until then), and only then close the handles.
		for i, s := range raw {
			if r := reqs[i]; r != nil {
				procCancelIoEx.Call(s.raw, uintptr(unsafe.Pointer(&r.ov)))
				var done uint32
				procGetOverlapped.Call(s.raw, uintptr(unsafe.Pointer(&r.ov)), uintptr(unsafe.Pointer(&done)), 1)
			}
			closeRaw(s.raw)
			if events[i] != 0 {
				closeRaw(events[i])
			}
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
			// (uv__insert_pending_req), then processed as a failed zero
			// read: EOF or the stream's 'error'.
			reqs[i] = nil
			readErrorOrEOF(raw[i], err)
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
		if r == 0 && e == errorOperationAborted {
			// uv__process_pipe_read_req ignores a cancelled read and
			// queues the next one.
			queue(i)
			continue
		}
		if r == 0 {
			// uv__process_pipe_read_req: a failed zero read is EOF for
			// ERROR_BROKEN_PIPE and an error otherwise; both end the
			// stream (Node emits 'end' or 'error', then 'close').
			readErrorOrEOF(raw[i], e)
			end(i)
			continue
		}
		if events[i] == 0 {
			ev, _, _ := procCreateEventW.Call(0, 1, 0, 0)
			if ev == 0 {
				end(i)
				continue
			}
			events[i] = ev
		}
		if readAvailable(raw[i], events[i]) {
			end(i)
			continue
		}
		queue(i)
	}
	return rest
}

// readAvailable is the read loop of uv__process_pipe_read_req after a
// zero read completed: uv__pipe_read_data (overlapped pipe) until a read
// comes back short. Each read is an overlapped ReadFile of up to 64 KiB
// (Node's alloc_cb buffer), cancelled with CancelIoEx if it pends; there
// is no PeekNamedPipe on this path, so data still buffered after the
// child closed its end is read before ReadFile reports ERROR_BROKEN_PIPE.
// A read that returns data (or is cancelled, ERROR_OPERATION_ABORTED)
// goes to the listener; ERROR_BROKEN_PIPE is EOF and any other error the
// stream's 'error' (uv__pipe_read_error_or_eof); both end the stream. It
// reports whether the stream ended.
func readAvailable(s pipedStream, event uintptr) bool {
	for {
		buf := make([]byte, segment)
		n, err := overlappedRead(s.raw, event, buf)
		switch err {
		case nil, errorOperationAborted:
			if n > 0 && s.listen != nil {
				s.listen(buf[:n]) // Node ignores a read of 0 bytes
			}
		default:
			readErrorOrEOF(s, err)
			return true
		}
		if n != len(buf) {
			return false
		}
	}
}

// readErrorOrEOF is uv__pipe_read_error_or_eof: ERROR_BROKEN_PIPE ends the
// stream quietly ('end'), any other error is the stream's 'error'.
func readErrorOrEOF(s pipedStream, err error) {
	if err != errorBrokenPipe && s.fail != nil {
		s.fail(err)
	}
}

// overlappedRead is uv__pipe_read_data's ReadFile on the overlapped
// handle, with the stream's event in the OVERLAPPED (its low bit set, so
// that the completion does not reach the port): a read that pends is
// cancelled (CancelIoEx) and its result waited for, which gives the bytes
// it read or ERROR_OPERATION_ABORTED with none.
func overlappedRead(h, event uintptr, buf []byte) (int, error) {
	ov := syscall.Overlapped{HEvent: syscall.Handle(event | 1)}
	var done uint32
	err := syscall.ReadFile(syscall.Handle(h), buf, &done, &ov)
	if err == errorIOPending {
		procCancelIoEx.Call(h, uintptr(unsafe.Pointer(&ov)))
		done = 0
		err = nil
		if r, _, e := procGetOverlapped.Call(h, uintptr(unsafe.Pointer(&ov)), uintptr(unsafe.Pointer(&done)), 1); r == 0 {
			err, done = e, 0
		}
	} else if err != nil {
		done = 0
	}
	runtime.KeepAlive(&ov)
	return int(done), err
}
