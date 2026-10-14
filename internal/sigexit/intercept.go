package sigexit

import (
	"sync"
	"syscall"
)

// intercepted are the signals a command currently handles itself (Node
// listeners such as judge-proof's process.once("SIGINT")): boot's handler
// does not die of them while they are intercepted.
var intercepted sync.Map // syscall.Signal -> struct{}

// Intercept makes boot's handler leave sig to the caller.
func Intercept(sig syscall.Signal) { intercepted.Store(sig, struct{}{}) }

// Release gives sig back to boot's handler (the Node listener was removed,
// so the default action applies again).
func Release(sig syscall.Signal) { intercepted.Delete(sig) }

// Intercepted reports whether a command handles sig itself.
func Intercepted(sig syscall.Signal) bool {
	_, ok := intercepted.Load(sig)
	return ok
}
