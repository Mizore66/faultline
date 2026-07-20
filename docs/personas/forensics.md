# Persona: Forensics / incident responder

Start here if you are reconstructing an agent-introduced regression.

1. Ensure Codex sidecar hooks are installed and trusted (`fl codex sidecar status`)
2. Freeze a human-reviewed witness (`fl witness review` → approve → freeze)
3. `fl investigate git` or `fl investigate turns` with digest-pinned `--image`
4. Heterogeneous lockfiles: `fl runtime mapping write ...` then `--runtime-mapping`
5. Minimize → repair → `fl prevention write --from-bundle` when Docker facts exist
6. Offline verify every retained package with `fl verify`

Related: [../proof-bundles.md](../proof-bundles.md), [../codex-sidecar.md](../codex-sidecar.md),
[../security-model.md](../security-model.md).
