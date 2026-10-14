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

// Intercepted reports whether a command handles sig itself.
func Intercepted(sig syscall.Signal) bool {
	_, ok := intercepted.Load(sig)
	return ok
}
