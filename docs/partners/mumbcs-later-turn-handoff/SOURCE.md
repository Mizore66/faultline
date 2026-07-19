# MUMBCS later-turn handoff (mirrored)

Mirrored from private partner branch for FaultLine submission evidence ([#46](https://github.com/Mizore66/faultline/issues/46)):

- Upstream: `monashblockchain/MUMBCS`
- Branch: `faultline/later-turn-demo`
- Path: `mumbcs-later-turn-handoff/`
- Mirrored into FaultLine: 2026-07-19

**What these files are for:** they are the permissioned external-validation packet — consent, structured case-study fields, impact record, publishable summary, and retained digests (frozen witness + turn-package root). Judges/Devpost cite the digests and summary; the full verify-able turn bundle remains in the MUMBCS workspace.

Canonical filled records in this repo:

- [../external-case-study-mumbcs.md](../external-case-study-mumbcs.md)
- [../impact-validation-external-01.md](../impact-validation-external-01.md)

| File | Role |
| --- | --- |
| `README.md` | Session summary + digests + claim boundary |
| `external-case-study.md` | Partner-filled case-study template |
| `impact-validation-external-01.md` | Partner-filled impact record (`Status: completed`) |
| `publishable-summary.md` | Short public / Devpost wording |
| `ROOT.sha256` | Turn package root digest |
| `frozen-digest.txt` | Frozen witness digest |
| `image.txt` | Digest-pinned Docker image used for replay |
