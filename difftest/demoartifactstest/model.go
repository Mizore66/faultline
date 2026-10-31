//go:build faultline_test

// Package demoartifactstest is the test-only request/capture model and
// library adapter for the demo-artifacts oracle (Go Slice 5b.1). It mirrors
// difftest/gen/gen-demo-artifacts.ts and is never reachable from release code.
package demoartifactstest

import "errors"

// Request is one entry of the generator's request table. Optional members
// whose absence differs from the empty value are pointers.
type Request struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Base      string    `json:"base,omitempty"`
	Mutation  string    `json:"mutation,omitempty"`
	Setup     string    `json:"setup,omitempty"`
	Output    *string   `json:"output,omitempty"`
	ProofRoot *string   `json:"proofRoot,omitempty"`
	Argv      *[]string `json:"argv,omitempty"`
}

// Entry is one lstat inventory record. Base64 is present exactly for files
// (an empty file is the empty string), Target exactly for symlinks.
type Entry struct {
	Path   string  `json:"path"`
	Kind   string  `json:"kind"`
	Base64 *string `json:"base64,omitempty"`
	Target *string `json:"target,omitempty"`
}

// Call is a library call's outcome: exactly one member is non-nil.
type Call struct {
	ValueJSON *string `json:"valueJSON"`
	Error     *string `json:"error"`
}

// Process is a CLI child's complete streams and exit status.
type Process struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}

// Capture is faultline.demo-artifacts-case.v1. A library request has Call and
// a null Process; a CLI request has Process and a null Call.
type Capture struct {
	Schema  string   `json:"schema"`
	Request Request  `json:"request"`
	Call    *Call    `json:"call"`
	Process *Process `json:"process"`
	Before  []Entry  `json:"before"`
	After   []Entry  `json:"after"`
}

// CaptureSchema is the Capture.Schema value.
const CaptureSchema = "faultline.demo-artifacts-case.v1"

// ErrSymlinkUnavailable is the cause of an AdapterError whose only problem is
// that the host refused to create a fixture-local symbolic link (Windows
// without the privilege). Callers may skip exactly the named link cases on it.
var ErrSymlinkUnavailable = errors.New("fixture-local symbolic link creation unavailable")

// AdapterError is panicked by Prepare/Invoke helpers for an unsupported or
// malformed request, or a setup/process failure that is not the real call's
// outcome; the driver turns it into a nonzero exit. A real call's error is
// data in Capture.Call and a CLI's exit status is data in Capture.Process; they
// are never AdapterErrors.
type AdapterError struct {
	Message string
	Cause   error
}

func (e AdapterError) Error() string { return e.Message }

// Unwrap exposes the cause (ErrSymlinkUnavailable) to errors.Is.
func (e AdapterError) Unwrap() error { return e.Cause }
