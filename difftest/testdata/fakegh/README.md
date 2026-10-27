# Fake gh

Each directory here holds one `gh` (a POSIX `sh` script) that the writer
replay (`difftest/writer_test.go`, `difftest/gen/gen-writers.ts`) puts on
`PATH` after the recorders, so no real GitHub CLI ever answers. A writer
template selects one with `"fakeGh": "<name>"`. fl runs `gh` with its whole
environment, so the fake appends its own record to `$FAULTLINE_ARGV_LOG`:
`gh ` + the arguments joined by `\037`, terminated by `\036`.

Protocol: `gh attestation verify <receipt> ...` answers from the table;
anything else exits 125 with `fake gh (<name>): unsupported command: <args>`
on stderr.

| name | `attestation verify <receipt> ...` |
| --- | --- |
| `pass` | stdout `Loaded digest sha256:<hex> for file://<receipt>` (`<hex>` is the receipt's SHA-256), exit 0. |
| `fail` | stderr `Error: verifying with issuer "sigstore.dev"\r\n`, exit 1. |
| `long-fail` | 9,000 bytes on stdout (4,500 `é`) and 9,000 on stderr (3,498 `x`, one U+1F600, 5,498 `y`), exit 1: fl keeps 8,000 UTF-16 units of `stdout + "\n" + stderr`, cutting U+1F600 in half. |
| `flood` | 1,200,000 bytes on stdout, exit 0: over spawnSync's 1 MiB maxBuffer, so fl reports `Could not run gh: spawnSync gh ENOBUFS`. |

The provenance cross-check (`fl provenance verify` on a receipt a case
wrote) runs with `pass`. Keep the outputs fixed text: goldens compare them
byte for byte.
