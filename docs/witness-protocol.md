# Witness protocol

Human review, freeze, optional authenticated approval, and verification. Guided intake narrative: [first-incident.md](first-incident.md).

## Propose → review → approve → freeze → verify

First create and freeze a reviewed witness. The proposal input is a blinded incident packet plus the exact overlay bytes to execute.

```powershell
pnpm fl witness propose --input .\proposal.json
pnpm fl witness review <proposal-id>
# Review the local page, then make separate Approve and Freeze clicks.
pnpm fl witness verify <proposal-id> --expect-digest <frozen-digest>
```

FaultLine never auto-approves or auto-freezes. `fl witness approve` records a reviewed approval but is intentionally not an identity assertion.

Optional: `fl witness implement` (Codex drafts overlay; human still freezes).

`fl witness propose --live` uses the Responses API with strict structured output after `OPENAI_API_KEY` is set. The model sees a blinded, redacted incident packet: candidate commits, turns, diffs, timeline data, and localization results are excluded by schema. GPT-5.6 never assigns PASS/FAIL. Boundaries: [concepts.md](concepts.md#gpt-56-boundaries).

## Optional authenticated reviewer approval (sign + keyring)

When a reviewer needs to authenticate the approval, sign the already-frozen witness with an Ed25519 private key and verify it against a separately retained reviewer keyring:

```powershell
pnpm fl witness sign <proposal-id> `
  --private-key .\reviewer-ed25519.pem `
  --keyring .\reviewers.json
pnpm fl witness verify <proposal-id> `
  --expect-digest sha256:<frozen-digest> `
  --keyring .\reviewers.json `
  --require-signature
```

The keyring, not the approval record, decides which reviewer keys are trusted. It contains the reviewer's stable identity, the SHA-256 fingerprint of its SPKI public key, and that public key:

```json
{
  "schemaVersion": "faultline.reviewer-keyring.v1",
  "reviewers": [
    {
      "approvedBy": "reviewer@example.com",
      "keyId": "sha256:<SPKI-public-key-fingerprint>",
      "algorithm": "ED25519",
      "publicKeyPem": "-----BEGIN PUBLIC KEY-----\\n...\\n-----END PUBLIC KEY-----\\n"
    }
  ]
}
```

The signed record binds the proposal, ordinary approval, exact frozen-witness digest, and witness digest. A normal verification may accept a frozen witness without a signature for the local/offline MVP; `--require-signature` fails closed when the authenticated record is missing, untrusted, or altered. Keep private keys outside the repository, rotate keys by changing the retained keyring, and do not treat a displayed `approvedBy` string as authenticated unless this signature check passes.

Security caveats: [security-model.md](security-model.md#signature-caveats-reviewer-keyring).
