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

Later tasks add `timeout`, `flood`, `daemon-down`, `missing-image` and
`crash`. Keep the outputs fixed text: goldens compare them byte for byte.
