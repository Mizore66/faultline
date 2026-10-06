# Verify this Git investigation package

## Handling warning

This package retains a frozen witness, Git object references, and bounded recorded evidence so it can be independently checked. Treat it as sensitive incident material and share it only with authorized reviewers.

Run FaultLine's Git proof verifier with an externally retained root digest.

The verifier never executes the frozen witness. It validates every declared byte, reconstructs the recorded stable states and transitions from raw run facts, and reconstructs the Git source range from the bundled descendant history before checking the binary range patch.

An external root is necessary to detect an editor who rewrites the mutable hash catalog too.