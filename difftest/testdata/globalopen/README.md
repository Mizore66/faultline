# globalopen (test-only helper)

A ptrace injector used only by `TestLiveWriterFaults` for the ordinary
first-open fault rows (`f.call == "open"`, not every-call). It is not a
production command and ships in no release. Its log lines are labeled
`GLOBALOPEN`; they are never stock strace output.

## Why

Stock strace counts "the first call" per tracee and per syscall, so it cannot
express "fail exactly one open in the whole process tree, whichever thread or
child makes it". The helper fails the first matching `open`, `openat` or
`openat2` entry in one global order, with the configured errno
(`ENOSPC`, `EIO`, `EACCES`; `ENOENT` is accepted for fixtures).

```
globalopen -errno E -path /abs/template -log FILE -- cmd args...
```

The literal `<PID>` in `-path` is replaced by the pid of the root tracee, the
traced command itself (no strace `-D` claim). HEALTH is separate from the traced
command's own exit status; a missing, truncated or unhealthy trace is a failure
whatever the writer returned.

## Prerequisites and semantics

- Linux, `ptrace` permitted, and the pidfd facility: Linux 5.3 (`pidfd_open`),
  5.1 (`pidfd_send_signal`), 5.2 with Go 1.23 for `CLONE_PIDFD` through
  `SysProcAttr.PidFD`. Checked before any tracee is launched; there is no
  wait-only fallback. Architectures: linux/arm64 and linux/amd64.
- Cleanup uses stable per-group pidfds only. No numeric signal is ever sent and
  ESRCH is never treated as death.
- The tracer locks its goroutine to one OS thread (`init`). That is required
  ptrace ownership, not alignment of any tracee's threads, timing or CPU.

## Unsupported or disclosed

- A non-leader thread's `execve` is reconciled without a phantom (from the
  actual `PTRACE_GETEVENTMSG`) but HEALTH is always false after one.
- An exec event met while draining is reconciled the same way; cleanup is not
  claimed universal. A refused reconciliation and a failed `execve` after the
  point of no return can still end in the loud ECHILD failure.
- No PID-namespace support is claimed; group-stop and the 32-bit ABI are hard
  errors; aliases other than symlinked directory components are not detected.

## Tests

Default helper plus the adversarial fixture (`../globalopen-fixture`) and its
real verifier, run from `difftest`:

```
go test -tags faultline_test -count=1 -run 'TestGlobalOpen|TestValidateGlobalOpenTrace|TestValidateGlobalOpenTraceAcceptsReconciledNonPendingLoss' ./difftest/
```

Hook-tagged lifecycle controls (the default build excludes every hook):

```
go test -tags 'globalopen_hooks,globalopen_residual,nonleader_exec_staged,log_controls,ledger_staged' -count=1 ./difftest/testdata/globalopen/
```

Override interface (a Go-less Linux container runs cross-built DEFAULT tools):
`FAULTLINE_GLOBALOPEN_BINARY` and `FAULTLINE_GLOBALOPEN_FIXTURE_BINARY` must be
absolute paths of existing executable regular files; anything else fails
loudly, and an override binary is never deleted. Unset, each tool is built once
per test process into a temp root removed by `TestMain`.
`FAULTLINE_GLOBALOPEN_EVIDENCE_DIR` retains every raw ordinary-open trace and a
paired `.meta` file exclusively (never overwritten) for CI artifacts.
