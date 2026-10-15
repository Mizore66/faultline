package sigexit

import (
	"sync"
	"syscall"
)

// intercepted are the signals a command currently handles itself (Node
// listeners such as judge-proof's process.once("SIGINT")): boot's handler
// does not die of them while they are intercepted.
var intercepted sync.Map // syscall.Signal -> struct{}

// Intercept makes boot's handler leave sig to the caller, who has already
// registered it with signal.Notify. On Linux boot set sig to SIG_DFL in the
// kernel (as Node starts), which signal.Notify does not undo; Intercept
// gives it back to the runtime's handler.
func Intercept(sig syscall.Signal) {
	intercepted.Store(sig, struct{}{})
	restoreRuntimeHandler(sig)
}

// Intercepted reports whether a command handles sig itself.
func Intercepted(sig syscall.Signal) bool {
	_, ok := intercepted.Load(sig)
	return ok
}
