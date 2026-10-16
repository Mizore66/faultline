// createPublicKey / createPrivateKey over the PEM strings in the JSON file
// argv[2] ([{text, pub}]), as one JSON object on stdout: the OpenSSL
// version and, per case, the key type and Ed25519 SPKI or the error message
// with opensslErrorStack (newest first). Used by difftest/pem_live_test.go,
// which starts it without a controlling terminal and with stdin on
// /dev/null: OpenSSL's PEM_def_callback prompts on /dev/tty (then stdin)
// for an encrypted block in a public pass, and must find neither.
import { createPrivateKey, createPublicKey } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";

const cases = JSON.parse(readFileSync(process.argv[2], "utf8"));
const results = cases.map(({ text, pub }) => {
  try {
    const key = pub ? createPublicKey(text) : createPrivateKey(text);
    const type = key.asymmetricKeyType ?? "";
    const spki = type === "ed25519" ? (pub ? key : createPublicKey(key)).export({ type: "spki", format: "der" }).toString("hex") : "";
    return { ok: true, type, spki };
  } catch (error) {
    return { ok: false, message: error.message, stack: error.opensslErrorStack ?? [] };
  }
});
const out = `${JSON.stringify({ openssl: process.versions.openssl, results })}\n`;
if (process.argv[3]) writeFileSync(process.argv[3], out);
else process.stdout.write(out);
