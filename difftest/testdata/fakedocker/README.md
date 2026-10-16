# Fake Docker

Each directory here holds one `docker` (a POSIX `sh` script) that the writer
replay (`difftest/writer_test.go`, `difftest/gen/gen-writers.ts`) puts on
`PATH` after `difftest/testdata/recorder`, so no daemon is needed and both
sides see the same results. A writer template selects one with
`"fakeDocker": "<name>"`; the recorder logs every argv before execing it.

Protocol:

- `docker run ...`: the source directory is the `src=` of
  `--mount type=bind,src=<dir>,...`; the script reads `<dir>/state.txt`
  (trailing newlines dropped) and answers from its table.
- `docker rm ...`: exit 0, no output.
- anything else: exit 125 with `fake docker (<name>): unsupported command: <cmd>`
  on stderr, so a new Docker call shows up in a golden instead of passing.

| name | `run` behaviour |
| --- | --- |
| `pass-fail` | `state.txt` = `bad`: stdout `state=bad` and a `PREDICATE_FAIL` result line, stderr `witness failed`, exit 1. Otherwise stdout `state=<state>` and a `PREDICATE_PASS` result line, exit 0 (`difftest/gen/fixtures.ts` `deterministicDockerRunner`). |
| `odd-bad` | fails the predicate when an odd number of the source's `state*.txt` files hold a line `bad` (a witness over several files, or over one hunk), otherwise passes; no `state=` line. |
| `timeout` | stdout `started`, then `exec sleep 30` (the runner's timeout kills it). |
| `flood` | `exec yes 'flood é'`: stdout without end (the output limit kills it). |
| `daemon-down` | stderr `docker: Cannot connect to the Docker daemon ...`, exit 1. |
| `missing-image` | stderr `docker: Error response from daemon: No such image: ...`, exit 125. |
| `crash` | stdout `partial`, stderr `about to crash`, then dies of SIGTERM. Its `rm` fails: stderr `Error response from daemon: No such container: <name>`, exit 1. |
| `setup-127` | stderr `/bin/sh: 1: node: not found`, exit 127. |
| `forged-pass` | stdout a `PREDICATE_PASS` result line, exit 1. |
| `compile-error` | stderr `witness.mjs:3` and `SyntaxError: Unexpected token '}'`, exit 1. |
| `tmpdir-swap` | moves `$TMPDIR` to `$TMPDIR.moved` and leaves an empty file in its place (once), then stdout a `PREDICATE_PASS` result line, exit 0: fl's cleanup of its temporary root then fails `lstat` with ENOTDIR. |

None of them reads `state.txt` except `pass-fail` and `odd-bad`. Long-running children are
started with `exec`, so a SIGKILL to the pid the runner spawned (the
recorder, which also execs) reaches the process holding the pipes. Keep the
outputs fixed text: goldens compare them byte for byte.
`difftest/sandbox_live_test.go` runs each one through the native runner on
both sides.
