# Fake Codex

Each directory here holds one `codex` (a POSIX `sh` script) for the writer
replay (`difftest/writer_test.go`, `difftest/gen/gen-writers.ts`), so no real
Codex CLI ever answers. A writer template selects one with
`"fakeCodex": "<name>"`: the case gets a copy at `<HOME>/fakecodex/codex`
(mode 0755) with the case's argv log path written into its `log=` line, and
`FAULTLINE_CODEX_BIN` names it. `"fakeCodexOnPath": true` installs the
`edits` copy the same way (or the `fakeCodex` one, when both are set) and
puts `<HOME>/fakecodex` on `PATH` right after the recorders, without
`FAULTLINE_CODEX_BIN`. fl runs codex with `PATH` as its only environment
variable, which is why the log path is written into the copy
(`installFakeCodex`, `scenarios.ts installFakeCodex`).

Record: every call appends one record to the log: `codex ` + the arguments
joined by `\037`, then `STDIN=<stdin bytes>`, `PATH=<$PATH>` and
`ENV=<the names of the other environment variables, sorted, joined by ",">`
(without `PWD`, `OLDPWD`, `SHLVL` and `_`, which `sh` sets on its own),
terminated by `\036`.

Protocol:

- `codex --version`: stdout `codex-cli 0.0.0-fake`, exit 0, except for the
  `probe-*` fakes.
- `codex exec --full-auto <prompt>`: reads the prompt on stdin, records it,
  and acts as the table says, in its cwd.
- anything else: exit 125 with `fake codex (<name>): unsupported command:
  <args>` on stderr, so a new Codex call shows up in a golden instead of
  passing.

| name | `--version` | `exec --full-auto <prompt>` |
| --- | --- | --- |
| `edits` | plain | in a `witness implement` overlay directory (`README.faultline.txt` in the cwd) rewrites `witness.mjs` with a fixed passing witness; otherwise writes `good\n` to `state.txt`. Stdout `fake codex: edited`, exit 0. |
| `noop` | plain | stdout `fake codex: nothing to do`, exit 0, changes nothing. |
| `probe-fail` | stderr `codex: unsupported`, exit 2 | as `noop` (not reached). |
| `probe-empty` | stdout `\n`, exit 0 | as `noop` (not reached). |
| `probe-crlf` | stdout `  codex-cli 1.2.3\r\nbuild fake\r\n`, exit 0 | as `noop`. |
| `exit-nonzero` | plain | writes `bad\n` to `state.txt`, stderr `codex failed`, exit 3. |
| `killed` | plain | `kill -TERM $$`: TS sees `status: null`, which is what a spawnSync timeout gives (slice 4c correction 9). |
| `flood` | plain | stdout 6,000 `é` (12,000 bytes), stderr 5,000 `x`, exit 0: the 4,096-unit preview slices. |
| `outside-edits` | plain | as `edits`, and also writes `outside\n` to `../outside.txt`, which lands in the managed repair root. |
| `symlink-edit` | plain | writes `good\n` to a new `good.txt` and replaces `state.txt` with a symlink to it; stdout `fake codex: edited`, exit 0. |

The helpers they run (`cat`, `awk`, `rm`, `ln`) are found on `PATH`, else
in `/usr/bin` or `/bin`, so a case with a narrow `PATH` (`pathTools`, the end
to end without Node) still records. Keep the outputs fixed text: goldens
compare them byte for byte. `difftest/fakes_test.go` checks the record.
