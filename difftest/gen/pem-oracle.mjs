// createPublicKey / createPrivateKey over the PEM strings in the JSON file
// argv[2] ([{text, pub}]), one JSON result per case on stdout. Used by
// difftest/pem_live_test.go, which starts it without a controlling terminal
// and with stdin on /dev/null: OpenSSL's PEM_def_callback prompts on
// /dev/tty (then stdin) for an encrypted block in a public pass, and must
// find neither.
import { createPrivateKey, createPublicKey } from "node:crypto";
import { readFileSync } from "node:fs";

const cases = JSON.parse(readFileSync(process.argv[2], "utf8"));
const out = cases.map(({ text, pub }) => {
  try {
    const key = pub ? createPublicKey(text) : createPrivateKey(text);
    const type = key.asymmetricKeyType;
    const spki = type === "ed25519" ? (pub ? key : createPublicKey(key)).export({ type: "spki", format: "der" }).toString("hex") : "";
    return { ok: true, type, spki };
  } catch (error) {
    return { ok: false, message: error.message };
  }
});
process.stdout.write(`${JSON.stringify({ openssl: process.versions.openssl, results: out })}\n`);
