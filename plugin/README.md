# FaultLine Codex plugin (experimental packaging scaffold)

**Packaging scaffold. Hook firing from plugin context is NOT yet live-fire verified; the sidecar installer remains the supported path.**

This directory bundles the three public Codex lifecycle hooks FaultLine observes:

- `SessionStart`
- `UserPromptSubmit`
- `Stop`

Manifest: [`faultline.codex-plugin.json`](faultline.codex-plugin.json).

## Supported path (do use)

```bash
fl codex sidecar install
# or: fl init / fl codex sidecar config
```

See [`docs/codex-sidecar.md`](../docs/codex-sidecar.md) and [`docs/codex-native-livefire.md`](../docs/codex-native-livefire.md).

## Not claimed

- Marketplace `codex plugin install` is **not** the supported live-fire path.
- This scaffold does **not** prove hooks fire under plugin context.
- Do not cite this directory as evidence of plugin-native install (CDX-08 remains open / human-gated).
