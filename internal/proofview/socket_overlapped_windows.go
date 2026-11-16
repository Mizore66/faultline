//go:build windows

package proofview

// Thin Windows ABI adapter for the portable overlapped-write core in
// socket_overlapped.go. It only touches syscall.Overlapped / syscall.WSABuf /
// LazyDLL / WSASend / WaitForSingleObject / CancelIoEx / CloseHandle. It returns
// raw values (names, errors, handles, ok flags); every diagnostic message is
// built by the core.

import (
	"syscall"
	"unsafe"
)

// windowsSocketNativePost is the opaque post object. It strongly owns the
// OVERLAPPED and the WSABUF array; the *Overlapped pointer is stable for the
// life of the post (the core pins it and keeps the post referenced until
// release).
type windowsSocketNativePost struct {
	ov   *syscall.Overlapped
	bufs []syscall.WSABuf
}

type windowsSocketRealAPI struct {
	createEvent *syscall.LazyProc
	getResult   *syscall.LazyProc
}

func newWindowsSocketDefaultAPI() windowsSocketNativeAPI {
	return &windowsSocketRealAPI{
		createEvent: syscall.NewLazyDLL("kernel32.dll").NewProc("CreateEventW"),
		getResult:   syscall.NewLazyDLL("ws2_32.dll").NewProc("WSAGetOverlappedResult"),
	}
}

func (a *windowsSocketRealAPI) Resolve() (string, error) {
	for _, p := range []*syscall.LazyProc{a.createEvent, a.getResult} {
		if err := p.Find(); err != nil {
			return p.Name, err
		}
	}
	return "", nil
}

func (a *windowsSocketRealAPI) CreateEvent() (windowsSocketHandle, bool, error) {
	r1, _, e := a.createEvent.Call(0, 1, 0, 0) // unnamed, manual-reset, non-signaled
	if r1 == 0 {
		return 0, false, e
	}
	return windowsSocketHandle(r1), true, nil
}

func (a *windowsSocketRealAPI) NewPost(taggedEvent uintptr, parts [][]byte) (any, []any) {
	ov := new(syscall.Overlapped)
	ov.HEvent = syscall.Handle(taggedEvent)
	bufs := make([]syscall.WSABuf, len(parts))
	for i, p := range parts {
		bufs[i] = syscall.WSABuf{Len: uint32(len(p)), Buf: &p[0]}
	}
	return &windowsSocketNativePost{ov: ov, bufs: bufs}, []any{ov, &bufs[0]}
}

func (a *windowsSocketRealAPI) Send(h windowsSocketHandle, post any) error {
	np := post.(*windowsSocketNativePost)
	return syscall.WSASend(syscall.Handle(h), &np.bufs[0], uint32(len(np.bufs)), nil, 0, np.ov, nil)
}

func (a *windowsSocketRealAPI) WaitEvent(ev windowsSocketHandle) (uint32, error) {
	return syscall.WaitForSingleObject(syscall.Handle(ev), syscall.INFINITE)
}

func (a *windowsSocketRealAPI) PollEvent(ev windowsSocketHandle) (uint32, error) {
	return syscall.WaitForSingleObject(syscall.Handle(ev), 0)
}

func (a *windowsSocketRealAPI) Result(h windowsSocketHandle, post any) windowsSocketNativeResult {
	np := post.(*windowsSocketNativePost)
	var n, flags uint32
	r1, _, e := a.getResult.Call(uintptr(h), uintptr(unsafe.Pointer(np.ov)), uintptr(unsafe.Pointer(&n)), 0, uintptr(unsafe.Pointer(&flags)))
	if uint32(r1) != 0 { // BOOL is 32 bits
		return windowsSocketNativeResult{OK: true, N: n}
	}
	var en syscall.Errno
	if x, ok := e.(syscall.Errno); ok {
		en = x
	}
	return windowsSocketNativeResult{Errno: en}
}

func (a *windowsSocketRealAPI) CancelIoEx(h windowsSocketHandle, post any) error {
	return syscall.CancelIoEx(syscall.Handle(h), post.(*windowsSocketNativePost).ov)
}

func (a *windowsSocketRealAPI) CloseEvent(ev windowsSocketHandle) error {
	return syscall.CloseHandle(syscall.Handle(ev))
}
