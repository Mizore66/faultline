# Codex sidecar

Opt-in observation of public Codex lifecycle hooks and clean Git checkpoints. This is not private model interception. Storage warning for turn-tree snapshots: [security-model.md](security-model.md#turn-tree-snapshot-storage-warning). Guided narrative: [first-incident.md](first-incident.md#1a-optional-observe-a-codex-session-without-claiming-private-access).

## Sidecar install / status

For an opt-in Codex App/CLI hook path, build FaultLine once and point the installer at that exact built CLI. It generates safe Unix and Windows command variants even when the path contains spaces, shows the complete root `hooks` document for review, and changes nothing until `--yes`. It only creates a new target project's `.codex/hooks.json`; it never overwrites or guesses how to merge an existing hook document.

```powershell
# Run from this FaultLine checkout.
pnpm build
$FaultLineCli = (Resolve-Path .\dist\cli.js).Path
$TargetRepo = (Resolve-Path C:\path\to\the\application).Path

# Preview the exact hook document and its one-file effect first.
node $FaultLineCli codex sidecar install --repo $TargetRepo --cli $FaultLineCli

# After reviewing that output, explicitly create $TargetRepo\.codex\hooks.json.
node $FaultLineCli codex sidecar install --repo $TargetRepo --cli $FaultLineCli --yes
```

Open or restart Codex in `$TargetRepo`, then use `/hooks` to inspect, trust, enable, or disable the FaultLine commands before they run. Project hook configuration is only appropriate for a project you trust. If `.codex/hooks.json` already exists, FaultLine refuses to replace it: emit the same root document with `node $FaultLineCli codex sidecar config --cli $FaultLineCli`, review it, and merge the `hooks` object yourself. Advanced installations may instead pass their own explicit `--command` and (when needed) `--command-windows` values to `sidecar config`.

The generated hooks observe `SessionStart`, `UserPromptSubmit`, and `Stop`; they never read or store `last_assistant_message`, a transcript path, or the raw prompt. Operational records live beneath the repository's local Git metadata directory rather than the worktree, so the recorder cannot hide arbitrary `.faultline` worktree files from a clean-checkpoint check.

After a session, inspect the recorder and copy its returned `ledgerPath` only when it applies to the incident's selected Git states:

```powershell
# Run the rest of the incident workflow from the application repository so
# its managed .faultline stores remain with the incident source.
Push-Location $TargetRepo
node $FaultLineCli codex sidecar status --repo .
node $FaultLineCli incident continue <id> `
  --expect-digest <retained-frozen-digest> `
  --ledger <ledgerPath-from-status>
```

## Dirty checkpoint

The sidecar captures a checkpoint only when `Stop` sees a clean Git worktree. It reports a durable `CHECKPOINT_SKIPPED_DIRTY` result when agent edits remain uncommitted; it does not fake a clean turn or infer an ephemeral diff checkpoint. Commit/stash/model the state before a later observed checkpoint, or use the manual recording route below for another observed transport.

## Record attach

For manually supplied observed events, record lifecycle facts and clean checkpoints directly:

```powershell
pnpm fl record init --session <session-id> --repo . --transport SIDE_CAR --actor you@example.com
pnpm fl record attach `
  --ledger .faultline\recordings\<session-id>.json `
  --repo . `
  --turn turn-1 `
  --ordinal 1 `
  --prompt-digest sha256:<64-lowercase-hex> `
  --output-digest sha256:<64-lowercase-hex> `
  --contribution "short observed change label" `
  --checkpoint
```

`record attach` is a convenience path for sidecar session attribution: it appends a started/completed turn pair and, when requested, a clean Git checkpoint for the completed turn. The attribution fields are reviewer-supplied context, not identity proof, private Codex interception, model intent, or turn-level blame. The ledger is observed evidence, not a claim that FaultLine reads private model reasoning.

At runtime, `fl record` accepts a strictly ordered, hash-chained Codex-compatible NDJSON lifecycle stream and records clean Git checkpoints; it labels the supplied transport as `CODEX_CLI`, `CODEX_APP`, or `SIDE_CAR` rather than claiming private event interception. Start a recording with `pnpm fl codex record init --session <id> --repo . --transport CODEX_CLI`, pipe observed events through `fl codex record stdin`, and capture checkpoints with `fl codex record checkpoint`.

## Ledger bind

Pass the chosen `--ledger` path to `fl investigate git` or `fl incident continue` to embed and validate matching lifecycle checkpoints in the Git package. A rendered package labels its real coverage as `FULLY_BOUND`, `PARTIALLY_BOUND`, or conservative **LEGACY BOUND** for old packages; it never treats a descendant-only checkpoint as coverage of every replayed state. If every state should be bound to an ordered checkpoint, create a strict sidecar record:

```powershell
pnpm fl ledger bind `
  --ledger .faultline\recordings\<session-id>.json `
  --investigation <proof-bundle>\investigation.json `
  --output .faultline\bindings\<investigation>.json
pnpm fl ledger verify .faultline\bindings\<investigation>.json
```

## Turn investigate note

Turn localization is an **experimental** evidence grade (`EXPERIMENTAL_TURN`). It is not interchangeable with commit-path portable proof (`COMMIT_PROOF`).

```powershell
pnpm fl investigate turns --repo . --ledger .faultline\ledgers\session.json --proposal <id> --expect-digest sha256:... --image <digest-pinned-image>
```

Prefer `fl investigate turns` when a turn ledger exists; otherwise `fl investigate git` / `fl incident continue`. Never headline turn localization in demos — lead with Git `COMMIT_PROOF`. Grades: [concepts.md](concepts.md#evidence-grades-commit-path-vs-turn-path).
