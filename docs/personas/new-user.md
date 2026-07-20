# Persona: New user

Start here if you are installing FaultLine for the first time.

1. Install from a packed tarball (see [../distribution.md](../distribution.md)) or clone + `pnpm install`
2. `fl doctor`
3. `fl tutorial --yes` — generates a disposable toy repo + frozen witness
4. When Docker is ready: `fl demo full --export-only`
5. For your own CI log: `fl investigate --ci-log <file> --repo .`

Related: [../first-incident.md](../first-incident.md), [../runtime-preparation.md](../runtime-preparation.md).
