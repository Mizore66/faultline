//go:build windows

package proofview

// Windows-only adapter test: real OVERLAPPED/WSABUF construction behind NewPost.
// Static and unexecuted. Pointer stability, low-bit tag and pin-target identity
// are meaningful only for the real ABI types, so they are not portable.

import (
	"syscall"
	"testing"
	"unsafe"
)

func TestWindowsAdapterNewPostLayout(t *testing.T) {
	a := &windowsSocketRealAPI{}
	p0, p1 := []byte{1, 2, 3}, []byte{4, 5}
	post, pins := a.NewPost(0x1235, [][]byte{p0, p1})
	np, ok := post.(*windowsSocketNativePost)
	if !ok || len(pins) != 2 {
		t.Fatalf("post=%T pins=%d", post, len(pins))
	}
	if uintptr(np.ov.HEvent) != 0x1235 || uintptr(np.ov.HEvent)&1 != 1 {
		t.Fatalf("HEvent %#x not the tagged value", uintptr(np.ov.HEvent))
	}
	if pins[0].(*syscall.Overlapped) != np.ov || pins[1].(*syscall.WSABuf) != &np.bufs[0] {
		t.Fatal("pin targets are not exactly the OVERLAPPED then WSABUF[0]")
	}
	if len(np.bufs) != 2 || np.bufs[0].Len != 3 || np.bufs[1].Len != 2 ||
		np.bufs[0].Buf != &p0[0] || np.bufs[1].Buf != &p1[0] {
		t.Fatalf("WSABUF array wrong: %+v", np.bufs)
	}
	if unsafe.Pointer(np.ov) == nil {
		t.Fatal("nil OVERLAPPED")
	}
}
