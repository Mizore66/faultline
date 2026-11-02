# incident-renderer corpus

Frozen-TS evidence for the Go Slice 5b.2 pure interactive renderer (`renderIncidentPage`, frozen source
`ce99a793af0b020bf1b9cf21114f8d7897b37207`, oracle Node `22.22.2`, `en-US` number locale). Produced only by
`difftest/gen/gen-incident-renderer.mjs`; never hand-edited.

Files (created by the generator, exclusive create, never overwritten):

- `golden.jsonl`: 24 rows in the spec's order, exact fields `id`, `analysisJSON`, `htmlJSON`, `afterJSON`.
- `incident.html`: normal UTF-8 bytes of the `replay` row's page.
- `baseline.json`: pins (frozen src blobs/tree hash, compiled dist module hashes, retained input hash,
  generator hash, runtime/locale, ordered case list, corpus hashes/lengths). Frozen manifest 0.1.3 and current
  manifest 0.1.4 are recorded separately.

Commands (each from the repository root; `NODE` is the pinned Node 22.22.2 binary, e.g.
`/Users/anasqumhiyeh/.cache/t12-node/x/node-v22.22.2-darwin-arm64/bin/node`; that path is not stored in metadata):

    NODE difftest/gen/gen-incident-renderer.mjs --check
    NODE difftest/gen/gen-incident-renderer.mjs --scratch /abs/fresh/dir
    NODE difftest/gen/gen-incident-renderer.mjs --recapture --scratch /abs/other/fresh/dir

`--check` imports no TS/dist module and writes nothing. Scratch directories must be new or empty, are preserved,
and are never deleted. RERUN is never executed; `difftest/testdata/bases/demo-rerun/analysis.json` is parsed as
historical input only.
