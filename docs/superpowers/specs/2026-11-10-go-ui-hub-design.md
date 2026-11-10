# UI hub, artifact discovery, and turn dashboard — design

- **Date:** 2026-11-10
- **Status:** Design approved by the user in architecture, behavior, verification, and scope sections. Written-spec review and implementation-plan approval remain separate gates. No implementation is authorized by this document.
- **Parent:** [Migration overview](2026-09-25-go-migration-overview.md), [Slice 5 dependency map](2026-10-30-go-slice5a-cli-surfaces-design.md#5-remaining-slice-5-dependency-map), and [Slice 5c verified rendering design](2026-11-01-go-slice5c-serve-bundle-design.md).
- **Baseline:** plain `serve` merged through [PR #227](https://github.com/Mizore66/faultline/pull/227) at `b1e23b4f5c5cb9b02fc092c79f1922a0827537bc`.

## 1. Deliverable and boundary

Port the frozen `ui` hub and local artifact discovery, and add a Go-only dedicated turn dashboard while retaining the raw JSON inspection path.

Included:

- `ui [--repo <directory>] [--port <number>]` with exact repository, port, discovery-count, browser, and SIGINT behavior.
- Discovery of Git proof bundles, turn proof bundles, incident drafts, and recording ledgers under `.faultline`.
- Hub index links, verified Git-proof rendering, turn verification, dedicated turn dashboard, and escaped raw JSON.
- Independent expectations for discovery, verification, dashboard content, escaping, and process lifecycle.

Not included:

- Changes to `serve --bundle`, plain `serve`, native execution, `judge-demo`, witness review, tutorial/live demos, release, oracle retirement, or TS deletion.
- A second UI framework, browser automation dependency, new TypeScript UI, mutation route, or unverified artifact rendering.
- Protected TypeScript, workflow, package manifest, or dependency changes.

The dedicated dashboard is intentionally Go-only. Frozen TypeScript continues to provide the raw turn JSON behavior used for independent comparison; it is not edited to add the new page.

## 2. Selected architecture

Use the existing Go proofview transport and renderer ownership. `startNodeServer` is a Go `net/http` helper, not a Node process.

| Component | Responsibility |
| --- | --- |
| New `internal/proofview/ui_hub.go` | Filesystem discovery, artifact identity/order, hub index, artifact/raw routes, and close delegation. |
| New `internal/proofview/turn_view.go` | Verified turn JSON projection and escaped dedicated dashboard rendering. |
| `internal/proofview/judge.go` | Existing verified Git-proof incident renderer, unchanged. |
| `internal/proofview/server.go` | Existing listener/framing/connection accounting only. |
| New `internal/cli/ui.go` | Repository/port options, startup output, browser opener, and `awaitBundleInterrupt`. |
| `internal/cli/browser.go` | Existing environment suppression and opener behavior; no second browser implementation. |

Use Go 1.27 and the standard library only. No production Node execution, new public Go framework, or new dependency is introduced.

## 3. Discovery contract

Resolve the repository to an absolute path and inspect only its `.faultline` tree. A missing or non-directory `.faultline` discovers zero artifacts.

Discover these fixed roots:

- `git-proof-bundles`: directories with `ROOT.sha256`, `manifest.json`, and `investigation.json`, excluding turn-proof manifests.
- `turn-proof-bundles`: directories whose manifest parses and whose `schemaVersion` contains `turn-proof`.
- `bundles`, `proofs`, `git-proofs`, and `turn-proofs`: either bundle kind, preserving the fixed root order.
- `incidents/drafts`: regular, non-symlink `.json` files; ID/label is the basename without `.json`.
- `recordings`: regular, non-symlink `.json` files except names containing `turn-snapshot-cache`; ID is the basename without `.json`, label is the full filename.

Skip hidden directories, symlinks, non-directories, and non-files. Do not follow links or recurse into arbitrary trees. De-duplicate by resolved absolute path while preserving discovery order.

A turn manifest that is missing, malformed, or has no string `schemaVersion` containing `turn-proof` is treated as a Git-proof bundle when the three required files exist; it is not silently discarded as an invalid turn package. Verification behavior applies only after selection.

## 4. Hub and route contract

### 4.1 Index

`GET /` returns the frozen hub index with repository and escaped artifact labels/paths. An empty hub includes `empty-state` and `fl quickstart`.

Each artifact links to `/artifact/<index>` for Git/turn bundles and `/raw/<index>` for drafts/ledgers. Indices refer to the immutable discovery order captured at server startup; later filesystem changes do not rewrite the index or route mapping.

Unknown routes and invalid artifact indices return 404 with the exact `Not found` or `Artifact not found` body as applicable. Valid routes include `Cache-Control: no-store`; do not add CORS permission or a new security policy.

### 4.2 Git and turn artifacts

Git-proof bundles continue through `loadVerifiedGitProofView` and the existing verified incident renderer.

Turn bundles are verified with the existing turn-proof verifier before dashboard rendering. A failed verification produces the frozen `Turn package failed verification` page with the joined error list, not a dashboard from unverified content.

### 4.3 Raw JSON

`/raw/<index>` supports incident drafts, ledgers, and turn bundles. Drafts/ledgers read their JSON file; turn bundles read `investigation.json`. The raw page escapes all bytes and displays the basename. Git-proof raw fallback is not added.

`/artifact/<index>` for a valid turn bundle serves the dedicated dashboard. The raw route remains available for exact inspection.

## 5. Dedicated turn dashboard

Read only the verified `investigation.json` and render a small, information-focused dashboard:

- Status, proof result/reason, evidence grade/label, environment homogeneity, introduction status/reason, and errors.
- Stable states with turn ID/ordinal, verdict, tree/snapshot digests, and execution/run IDs.
- Transitions with kind and before/after state ordinals.
- Repository, ledger digest, executions-per-state, recorder, native interception, and runtime mapping metadata.

Use existing escaping/digest formatting helpers where their output matches the intended display. Every dynamic value is HTML-escaped, including IDs, digests, repository text, reasons, and errors. No private model reasoning, semantic-cause claim, or native Codex interception claim is inferred from the data.

The page is read-only and adds no scripts, mutation controls, external assets, or new animation system. Use a small inline style block consistent with the existing hub rather than a new design system.

## 6. CLI and lifecycle

`ui` resolves `--repo` with existing option behavior and defaults to the current directory. Port defaults to 4173 and accepts 0 for an ephemeral listener; invalid ports use the frozen `--port must be an integer from 0 through 65535.` refusal.

After startup print exactly:

```text
FaultLine UI hub: <actual-loopback-URL>
Discovered <N> local artifact(s) under .faultline
Press Ctrl+C to stop.
```

Print the existing human next command: `fl quickstart` when empty, otherwise `fl ui --repo <resolved-repository>`. Invoke `openLocalDemoURL` after startup output. Wait with `awaitBundleInterrupt`, preserving first/second SIGINT behavior and no stop summary. Startup failure produces no browser launch or wait.

## 7. Files and compatibility documentation

Expected scope:

- Add `internal/proofview/ui_hub.go`, `turn_view.go`, and focused tests.
- Add `internal/cli/ui.go`, `ui_test.go`, and actual-process tests.
- Modify only the CLI dispatch seam to select `ui`.
- Add `difftest/ui_hub_live_test.go` and independent dashboard goldens under `difftest/testdata/ui-hub/`.
- Modify `difftest/KNOWN_DIFFERENCES.md`, the migration overview, and Slice 5 dependency map after corresponding acceptance exists.

`serve_bundle.go`, `demo_server.go`, native execution/verifier code, `src/`, workflows, `package.json`, and `pnpm-lock.yaml` remain unchanged. New production diagnostics and statement positions must satisfy applicable literal/coverage gates without exemptions.

## 8. Verification

### 8.1 Discovery and routes

Cover missing roots, hidden entries, symlinks, malformed manifests, duplicate paths, fixed root order, drafts, ledgers, cache exclusion, and invalid indices. Test hub index escaping and immutable startup mappings. Exercise Git-proof and turn artifact routes, raw JSON, and all 404 paths.

### 8.2 Dashboard expectations

Use valid and invalid turn-proof fixtures to prove verification-before-render. Test every dashboard field, transition, execution/run ID, digest, error, and escaping case. Independently authored goldens must not be generated from the Go renderer under test.

Test browser suppression and opener failure with fake openers, not real desktop sessions. Compare the frozen-TypeScript hub behavior for index, raw JSON, startup, and SIGINT; the dedicated dashboard is separately golden-tested as a Go-only addition.

### 8.3 Process and regression gates

Build `fl` and run outside the repository with Node and Go absent from controlled PATH. Verify exact startup/discovery counts, real artifact/raw requests, occupied/invalid ports, and clean SIGINT with no leaked process.

Use meaningful RED before production changes, then GREEN. Run affected ordinary/tagged/race/vet/literal/native-coverage gates, pure-renderer/read-only-server regressions, pinned live-oracle comparisons, actual-process tests, and six-target cross-compilation. Retain failures, skips, timeouts, and incomplete attempts; no platform skip or waiver is added. Every shell command stays below five minutes.

Require independent task/spec-quality reviews and a final whole-branch review. Hosted execution on all six targets and all required PR checks must pass before separate ready/merge authorization. Cross-compilation is compilation evidence only.

## 9. Completion and authorization

This project is complete only when discovery, hub/CLI, verification, dashboard, raw JSON, lifecycle, independent expectation, coverage, review, and hosted acceptance contracts pass. It does not complete `serve --bundle`, witness review, the rest of Slice 5, release readiness, or TS retirement.

Writing or committing this spec authorizes only written-spec review and then preparation of an implementation plan. Implementation requires approval of that plan. Publication, ready/merge transitions, protected-source changes, release publication, branch/worktree/evidence deletion, and further subprojects retain separate authorization boundaries.
