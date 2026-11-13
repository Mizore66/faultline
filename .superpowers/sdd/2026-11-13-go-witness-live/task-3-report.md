# Task 3 report: model overlay input and path safety

Base d25aa6b-equivalent (worktree HEAD 30559d1).

## Files
- internal/witness/overlay_input.go (ModelOverlay, ReadModelOverlayInput, injectable overlayOps for call recording)
- internal/witness/overlay_input_test.go (TestReadModelOverlayInput, table subtests)
- internal/witness/overlay_input_windows_test.go (TestReadModelOverlayInputJunction, mklink /J parent/leaf/root)

## RED
`go test -count=1 -run '^TestReadModelOverlayInput$' ./internal/witness` -> `undefined: ReadModelOverlayInput` (build failure).

## GREEN
First run failed on my own test fixture (1,050,000 bytes encodes to exactly 1,400,000 chars); fixed fixture to 1,050,001 bytes. Then:
- go test (target) ok; go test -race ok; go vet ./internal/witness ok
- GOOS=windows GOARCH=amd64 and arm64 `go test -c` compile ok. Native Windows execution not run (hosted).

## Behavior covered
a/./b -> a/b, a/../b -> b, a//b; binary/CRLF bytes with independent base64 literal; empty/"."/"a/.."/absolute-outside/escaping/backslash paths refused with the TS message; missing/file/symlink root; regular-file and symlink parent; missing/dir/symlink leaf; missing parent/leaf yield raw nodefs ENOENT lstat errors; duplicate reads both returned; case aliases returned on case-insensitive FS (skipped on linux, where distinct-case files are covered instead); >1.4M base64 and >512 path read without refusal; symlinked root ancestors accepted; call recorder proves sequential lstat/read order and stop at first failure.

## Concerns
- Parity follows src/overlay-input.ts exactly, including its quirk that an absolute path inside the root is accepted (relative() normalizes it); tests assert this.
- The TS "parts contain . or .." check is unreachable after normalization but ported anyway.
- Windows junction test is compile-checked only.
- Not run under -tags faultline_test; the new files carry no tag.
