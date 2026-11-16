//go:build linux || darwin

package proofview

// Default native factory on non-Windows hosts: every operation reports a
// structured ENOSYS. There is no physical Windows overlapped I/O here; the
// portable core is exercised only through injected per-instance fake APIs.

import "syscall"

type windowsSocketUnsupportedAPI struct{}

func newWindowsSocketDefaultAPI() windowsSocketNativeAPI { return windowsSocketUnsupportedAPI{} }

func (windowsSocketUnsupportedAPI) Resolve() (string, error) { return "", syscall.ENOSYS }

func (windowsSocketUnsupportedAPI) CreateEvent() (windowsSocketHandle, bool, error) {
	return 0, false, syscall.ENOSYS
}

func (windowsSocketUnsupportedAPI) NewPost(uintptr, [][]byte) (any, []any) { return nil, nil }

func (windowsSocketUnsupportedAPI) Send(windowsSocketHandle, any) error { return syscall.ENOSYS }

func (windowsSocketUnsupportedAPI) WaitEvent(windowsSocketHandle) (uint32, error) {
	return 0, syscall.ENOSYS
}

func (windowsSocketUnsupportedAPI) PollEvent(windowsSocketHandle) (uint32, error) {
	return 0, syscall.ENOSYS
}

func (windowsSocketUnsupportedAPI) Result(windowsSocketHandle, any) windowsSocketNativeResult {
	return windowsSocketNativeResult{Errno: syscall.ENOSYS}
}

func (windowsSocketUnsupportedAPI) CancelIoEx(windowsSocketHandle, any) error { return syscall.ENOSYS }

func (windowsSocketUnsupportedAPI) CloseEvent(windowsSocketHandle) error { return syscall.ENOSYS }
