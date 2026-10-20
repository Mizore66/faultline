# Fake toolchains

Each directory here is one fake toolchain for `fl doctor`: POSIX `sh`
scripts named `git`, `node` and (except `no-docker`) `docker`. A writer
template selects one with `"fakeTools": "<name>"`; the writer replay
(`difftest/writer_test.go`, `difftest/gen/gen-writers.ts`) then puts the
directory on `PATH` after the recorders (`difftest/testdata/recorder`) and
before the template's fake Docker and the inherited `PATH`. The recorder
logs every git and docker argv before execing the fake.

What each script answers (anything else is passed on, by `exec`, to the
next executable of the same name on `PATH` after the script's own
directory, the way the recorders do it):

- `git`: a call whose last argument is `--version` (doctor puts the `-c`
  pairs first) prints the toolchain's version text. Every other call
  (`rev-parse`, `status`) runs the real git.
- `node`: exactly `node --version` prints the toolchain's version text. The
  harness starts the TS oracle as `process.execPath`, an absolute path, so
  this script never runs TS itself (`gen-writers.ts` asserts that).
- `docker`: `--version` and `info` (doctor's `info --format
  {{.ServerVersion}}`) print the toolchain's texts.

| name | git `--version` | node `--version` | docker `--version` | docker `info` |
| --- | --- | --- | --- | --- |
| `ready` | `git version 2.99.0` | `v22.22.2` | `Docker version 27.0.0, build fake` | `27.0.0` |
| `old-node` | as `ready` | `v20.11.0` | as `ready` | as `ready` |
| `odd-node-v22` | as `ready` | `v22` | as `ready` | as `ready` |
| `odd-node-nightly` | as `ready` | `v22.1.2-nightly` | as `ready` | as `ready` |
| `odd-node-bare` | as `ready` | `22.0.0` (no `v`) | as `ready` | as `ready` |
| `no-docker` | as `ready` | as `ready` | none: the case also sets `FAULTLINE_HIDE_DOCKER=1`, which makes `recorder/docker` exit 127 as a missing command would | not reached |
| `daemon-down` | as `ready` | as `ready` | as `ready` | stderr `Cannot connect to the Docker daemon ...`, exit 1 |
| `slow-git` | `exec sleep 6` (past doctor's 5 s timeout) | as `ready` | as `ready` | as `ready` |
| `flood-node` | as `ready` | 70,000 bytes of `v` (past the 65,536-byte cap) | as `ready` | as `ready` |
| `crlf-git` | `git version 2.99.0\r\n\r\n  (fake build)  \r\n` | as `ready` | as `ready` | as `ready` |
| `long-docker` | as `ready` | as `ready` | 303 bytes: `Docker version `, 144 `1`s, U+1F600 at UTF-16 units 159-160, 139 `y`s (doctor's 160-unit slice cuts the pair) | as `ready` |

Keep the outputs fixed text: goldens compare them byte for byte.
