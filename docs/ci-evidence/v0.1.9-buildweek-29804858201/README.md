# CI evidence — `v0.1.9-buildweek` tag Verify run

**Tag Verify run:** [29804858201](https://github.com/Mizore66/faultline/actions/runs/29804858201)  
**Event:** `push` of tag `v0.1.9-buildweek`  
**Head SHA (pin commit):** `3ca1bb8adaebf163fe633642a0a4ba20dfb7810a`  
**Conclusion:** `success`  
**Retained:** `run-summary.json` + alpine Docker E2E coverage matrix twin

## Provenance note (honesty)

The `signed GitHub CI provenance` job runs only on **`push` to `main`**, not on
tag pushes. Run `29804858201` therefore has **no** `faultline-ci-provenance-*`
artifact (job skipped).

The sibling main merge that landed the pin,
[29804878259](https://github.com/Mizore66/faultline/actions/runs/29804878259)
(`faa98c0`, merge of `pin/v0.1.9-buildweek`), **did** produce provenance. Those
files are retained under `provenance-from-main-faa98c0/` so judges can open a
receipt + attestation without probing GitHub Actions UI.

| File | Source |
| --- | --- |
| `run-summary.json` | `gh run view 29804858201 --json …` |
| `coverage-matrix-docker-e2e-alpine.*` | artifact from tag run |
| `provenance-from-main-faa98c0/*` | artifact `faultline-ci-provenance-29804878259-1` |

Do not claim the tag push itself attested the receipt — claim the main push that did.
