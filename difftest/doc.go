// Package difftest compares the Go fl with the frozen TypeScript CLI: golden
// replay, writer replay and the live tests against the Node oracle. Every
// other file builds only with -tags faultline_test (the seeded clock), so a
// plain `go test ./...` sees an empty package here.
package difftest
