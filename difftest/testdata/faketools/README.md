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

| `empty-version` | a blank line | as `ready` | two spaces and a CRLF | not reached |
| `daemon-empty` | as `ready` | as `ready` | as `ready` | a blank line, exit 0 |
| `no-repository-root` | as `ready`; `rev-parse --show-toplevel` exits 0 with no output | as `ready` | as `ready` | as `ready` |
| `status-fail` | as `ready`; `status` prints `fatal: fake status failure` and exits 128 | as `ready` | as `ready` | as `ready` |
| `security-hook-runs` | as `ready`; a hardened `commit` (one with doctor's `core.hooksPath` override) first writes `HOOK_EXECUTED.marker` in its `-C` directory | as `ready` | as `ready` | as `ready` |
| `security-commit-fail` | as `ready`; a hardened `commit` prints `fake commit refused` and exits 1 | as `ready` | as `ready` | as `ready` |
| `security-ls-remote-ok` | as `ready`; `ls-remote` prints one ref and exits 0 | as `ready` | as `ready` | as `ready` |
| `security-lfs-filter` | as `ready`; a hardened `status` without `--untracked-files=all` and without the turn-snapshot `core.attributesFile` override (only `doctor --security`'s own) first prints `malicious-lfs-filter: invoked` on stderr | as `ready` | as `ready` | as `ready` |
| `security-capture-fail` | as `ready`; a `rev-parse` carrying the turn-snapshot `core.attributesFile` override prints `fatal: fake snapshot failure` and exits 128 | as `ready` | as `ready` | as `ready` |

The `security-*` toolchains each defeat one `doctor --security` check, so it
reaches FAIL (and the report INSECURE) on both sides. The fifth check,
`overlay-path-traversal`, calls no external command, so no toolchain can make
it fail.

A docker that cannot be started at all (spawn `ENOENT`) needs a `PATH` with
no `docker` on it, which the recorders and the inherited `PATH` always
provide. A writer template's `"pathTools": ["git", "dirname"]` asks for that:
the case's `PATH` is then the recorders, the fake toolchain, the fake Docker
and `<case>/bin`, which holds a symlink to the first executable of each
listed name on the inherited `PATH`, with no inherited `PATH` after it; the
recorder `docker` is left out unless `docker` is listed
(`difftest/gen/scenarios.ts` `linkPathTools`, `difftest/writer_test.go`
`linkPathTools`). Use it with the `no-docker` toolchain.

Keep the outputs fixed text: goldens compare them byte for byte.
