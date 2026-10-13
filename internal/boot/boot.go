// Package boot gives fl Node's process-wide signal state before anything
// else runs. Its import path sorts first among FaultLine's packages, so Go
// initializes it right after os/signal and before the packages whose init
// takes milliseconds (schema and table setup); the runtime's own start-up
// (under a millisecond) cannot be covered. cmd/fl imports it for effect.
package boot
