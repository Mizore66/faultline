# CODEX-01 — hooks-native live-fire + thread bindings

## Shipping path

Codex project hooks (`.codex/hooks.json` → `fl codex sidecar hook`) are the
supported live-fire install path. Marketplace `codex plugin install` is not
required for recordings to fire once hooks are trusted.

Live-fire evidence: `docs/impact-validation-codex-self-dogfood.md` and
`docs/samples/faultline-self-sidecar-soak/`.

## Proof-package thread bindings

Prevention / repair packages may carry `codexThreadId` as an identifier only.
Bindings must verify without private transcripts, prompt text, or message dumps.

```powershell
fl prevention write --from-bundle ... --codex-thread-id <id>
fl ledger bind --ledger <ledger.json> --investigation <investigation.json> --output <binding.json>
```

## Honesty

Do not rebadge Cursor / non-Codex sessions as `SIDE_CAR`. Use
`OBSERVED_EXTERNAL_TRANSPORT` for those.
