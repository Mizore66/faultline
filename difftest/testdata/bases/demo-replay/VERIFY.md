# Verify this bundle

Run `fl verify <bundle-directory>` for structural self-consistency.

For tamper detection against an editor who could update hash metadata, pass an externally recorded root: `fl verify <bundle-directory> --expect-root <sha256:...>`.

Verification never executes repository code. Use the explicit judge rerun path to execute the reviewed built-in sample.