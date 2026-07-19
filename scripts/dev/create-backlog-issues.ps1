#Requires -Version 5.1
<#
.SYNOPSIS
  Create FaultLine backlog GitHub issues (SEC/COH/CDX) against the origin remote.
#>
$ErrorActionPreference = "Continue"
$env:Path = "C:\Program Files\GitHub CLI;" + $env:Path

Set-Location (Resolve-Path (Join-Path $PSScriptRoot "..\.."))

$origin = (git remote get-url origin).Trim()
if (-not $origin) { throw "No git remote named origin." }

if ($origin -match 'github\.com[:/](?<owner>[^/]+)/(?<repo>[^/.]+)') {
  $repo = "$($Matches.owner)/$($Matches.repo)"
} else {
  throw "Origin is not a GitHub remote: $origin"
}

Write-Host "Target repository: $repo"
Write-Host "Origin URL:        $origin"
Write-Host ""

gh auth status
if ($LASTEXITCODE -ne 0) { throw "gh is not authenticated. Run: gh auth login" }

# Ensure labels exist (idempotent).
$labels = @(
  @{ name = "security"; color = "B60205"; description = "Security posture hardening" },
  @{ name = "ux"; color = "1D76DB"; description = "Product coherence and UX" },
  @{ name = "codex-native"; color = "0E8A16"; description = "Codex-native depth and validation" }
)
foreach ($label in $labels) {
  # --force updates color/description when the label already exists.
  & gh label create $label.name --repo $repo --color $label.color --description $label.description --force | Out-Null
}

$issues = @(
  @{
    title = "SEC-01: Establish Explicit Contact Vector and Vulnerability Reporting Channel"
    label = "security"
    body = @"
## Goal
Replace ambiguous security-contact language with a definitive reporting channel so researchers know exactly how to reach maintainers.

## Target
- ``SECURITY.md``

## Acceptance criteria
- [ ] Remove ambiguous phrasing such as "Prefer advisories (if enabled)".
- [ ] Declare one explicit, real contact path for vulnerability reports (private advisory URL and/or a monitored maintainer email).
- [ ] Keep "do not open public issues for exploitable reports" guidance.
- [ ] Document what to include in a report (version/SHA, OS, Docker involvement, minimal reproduction, impact).

## Notes
This is documentation-only hardening; no runtime behavior changes.
"@
  },
  @{
    title = "SEC-02: Cryptographic Isolation and Quarantine of Turn Snapshot Blobs"
    label = "security"
    body = @"
## Goal
Prevent turn-snapshot ``git add`` / ``write-tree`` from polluting the user's primary object database with loose snapshot blobs.

## Targets
- ``src/turn-snapshot.ts``
- CLI surface for ``fl codex snapshot gc`` (new command module as needed)

## Acceptance criteria
- [ ] Dynamically set ``GIT_OBJECT_DIRECTORY`` to ``.git/faultline/objects`` during snapshot blob generation.
- [ ] Update cache-reuse ``cat-file -e`` checks to inspect that alternate object directory.
- [ ] Expose ``fl codex snapshot gc`` to purge ``.git/faultline/objects``.
- [ ] Regression test: snapshot + gc leaves ``.git/objects`` with zero newly created loose objects attributable to the snapshot path.

## Local verification
``````bash
pnpm test -- tests/turn-snapshot.test.ts
``````
"@
  },
  @{
    title = "SEC-03: Structural Quarantine of Unsafe Evaluators (UNSAFE_LOCAL)"
    label = "security"
    body = @"
## Goal
Make ``UNSAFE_LOCAL`` results structurally unexportable from portable Git proof packages.

## Target
- ``src/git-proof-bundle.ts``

## Acceptance criteria
- [ ] In the bundle serialization / write path, intercept payloads containing any ``UNSAFE_LOCAL`` run fact.
- [ ] Throw a validation error and refuse to compile/export the package.
- [ ] Regression test asserts strict validation failure when ``UNSAFE_LOCAL`` is present.

## Local verification
``````bash
pnpm test -- tests/git-proof-bundle.test.ts
``````
"@
  },
  @{
    title = "SEC-04: Deterministic Supply Chain Hardening"
    label = "security"
    body = @"
## Goal
Eliminate mutable Action tag drift and add a production dependency audit signal in CI.

## Targets
- ``.github/workflows/*.yml``
- ``package.json`` (scripts if needed)

## Acceptance criteria
- [ ] Pin all floating tags (e.g. ``@v5``, ``@v6``, ``@v4``) to full-length immutable commit SHAs, with version comments.
- [ ] Add ``pnpm audit --prod`` as a non-blocking CI step (must not fail the job on findings).
- [ ] Confirm workflow YAML remains valid across verify + action-smoke jobs.

## Local verification
``````bash
# After pin updates, CI should stay green on push/PR.
pnpm audit --prod || true
``````
"@
  },
  @{
    title = "SEC-05: Real-time Sandboxing Hardening Verification Engine"
    label = "security"
    body = @"
## Goal
Provide a live self-test that FaultLine's hardened Git policy is actually enforced in the environment.

## Targets
- ``src/doctor.ts`` / CLI doctor command
- Mock repository fixture with a dummy malicious hook

## Acceptance criteria
- [ ] Implement ``fl doctor --security``.
- [ ] Assert live enforcement of ``protocol.allow=never``, hooks neutralization, and path filters.
- [ ] Programmatically spawn a test Git operation against a mock repo fixture containing a malicious hook and prove the hook does not execute.
- [ ] Regression coverage for pass/fail outcomes of the security self-test.

## Local verification
``````bash
pnpm fl doctor --security
pnpm test -- tests/doctor.test.ts
``````
"@
  },
  @{
    title = "SEC-06: Compute Sandbox Parameter Tightening"
    label = "security"
    body = @"
## Goal
Lock down Docker sandbox containers against tmpfs/IPC resource abuse.

## Target
- ``src/sandbox.ts`` (``createDockerSandboxPlan``)

## Acceptance criteria
- [ ] Inject ``--ipc=none`` into the Docker argv plan.
- [ ] Inject an ``--ulimit fsize`` cap into the Docker argv plan.
- [ ] Update sandbox unit tests to assert both flags are present.

## Local verification
``````bash
pnpm test -- tests/sandbox.test.ts
``````
"@
  },
  @{
    title = "COH-01: Automated Workspace Digest Inheritance Flow"
    label = "ux"
    body = @"
## Goal
Stop forcing humans to copy-paste ``--expect-digest`` / ``--ledger`` between related lifecycle commands when a local incident session already knows those values.

## Targets
- Investigate / prove / continue CLI paths
- Local incident store

## Acceptance criteria
- [ ] Downstream commands read ``--expect-digest`` and ``--ledger`` from the local incident store by default.
- [ ] Manual flags remain available as overrides for cross-machine validation.
- [ ] Integration test confirms an end-to-end path that needs zero manual digest flags.

## Local verification
``````bash
pnpm test -- tests/cli.test.ts tests/incident-store.test.ts
``````
"@
  },
  @{
    title = "COH-02: Structured Project Level Initialization State"
    label = "ux"
    body = @"
## Goal
Persist project choices from ``fl init`` so later commands can reuse digests, paths, and snapshot preferences without mandatory re-entry.

## Targets
- ``src/project-init.ts`` / CLI ``fl init``
- New ``src/config.ts`` (or equivalent)
- ``.faultline/config.json``

## Acceptance criteria
- [ ] ``fl init`` writes ``.faultline/config.json``.
- [ ] Persist resolved runtime Docker image digests, tracked snapshot preferences, and default store paths.
- [ ] Downstream commands treat ``--image`` as an optional override rather than a mandatory flag when config supplies a digest-pinned image.

## Local verification
``````bash
pnpm fl init --repo .
pnpm test -- tests/cli.test.ts
``````
"@
  },
  @{
    title = "COH-03: Actionable Exit Context Delivery"
    label = "ux"
    body = @"
## Goal
Every CLI termination or refusal should leave the operator with exactly one clear next command.

## Targets
- CLI output helpers / refusal paths under ``src/cli.ts`` (and any UI helpers)

## Acceptance criteria
- [ ] Audit all command exits and operational refusal blocks.
- [ ] Each termination state prints exactly one clean, copy-pasteable next-step terminal command.
- [ ] Avoid dumping multiple competing "try this or that" command lists at exit.

## Local verification
``````bash
pnpm test -- tests/cli.test.ts
``````
"@
  },
  @{
    title = "COH-04: Multi-Environment Documentation Alignment"
    label = "ux"
    body = @"
## Goal
Make quickstarts usable for both PowerShell and Bash/Zsh users without translation friction.

## Targets
- ``README.md``
- ``docs/first-incident.md``

## Acceptance criteria
- [ ] Every PowerShell code block has a dual-shell Bash/Zsh alternative (adjacent blocks or clean tabs).
- [ ] Commands remain accurate for Windows and POSIX shells.
- [ ] No orphaned PowerShell-only examples in the quickstart path.

## Local verification
Manual docs review + spot-run of quickstart commands on both shells where available.
"@
  },
  @{
    title = "COH-05: Non-Ambiguous Evidence Grading Taxonomy"
    label = "ux"
    body = @"
## Goal
Keep internal evidence-grade identifiers intact while making human-facing copy unambiguous.

## Targets
- ``src/proof-roots.ts``
- ``src/incident-page.ts``
- Related terminal/page label helpers (e.g. ``src/evidence-grade.ts``)

## Acceptance criteria
- [ ] Keep identifiers ``COMMIT_PROOF`` and ``EXPERIMENTAL_TURN``.
- [ ] Human-facing layouts prefix with:
  - ``COMMIT_PROOF`` → "Proven at commit granularity — portable and offline-verifiable"
  - ``EXPERIMENTAL_TURN`` → "Experimental turn-level evidence — not yet a portable proof"
- [ ] Update any UI/terminal assertions that snapshot the old labels.

## Local verification
``````bash
pnpm test -- tests/evidence-grade.test.ts tests/ui.test.ts
# If snapshots exist and need refresh:
pnpm test -- -u
``````
"@
  },
  @{
    title = "COH-06: Unification of Local Proof Serving Architecture"
    label = "ux"
    body = @"
## Goal
Provide a single local gateway that discovers and serves FaultLine artifacts from ``.faultline``.

## Targets
- CLI ``fl ui`` entrypoint
- Existing proof/incident page servers as needed

## Acceptance criteria
- [ ] Implement ``fl ui``.
- [ ] Dynamically scan ``.faultline`` for bundles, incidents, and ledgers.
- [ ] Serve a unified hub where the user can open any discovered artifact.
- [ ] Basic regression/smoke coverage for discovery + serve wiring.

## Local verification
``````bash
pnpm fl ui
pnpm test -- tests/ui.test.ts tests/cli.test.ts
``````
"@
  },
  @{
    title = "CDX-01: Multi-Turn Execution Record Realization & Self-Dogfood Tracking"
    label = "codex-native"
    body = @"
## Goal
Bring the multi-turn session ledger protocol fully online and replace synthetic fixtures with an authentic dogfood ledger where possible.

## Targets
- ``src/codex-sidecar.ts``
- ``docs/impact-validation-external-01.md``
- Exported ledger dataset used by ``fl investigate turns``

## Acceptance criteria
- [ ] Sidecar loop can record multi-turn development history into a durable ledger.
- [ ] Export that ledger and route ``fl investigate turns`` against the authentic session dataset.
- [ ] Document how the dataset was produced / where it lives.

## Local verification
``````bash
pnpm fl codex sidecar status --repo .
pnpm test -- tests/codex-sidecar.test.ts tests/ledger.test.ts
``````
"@
  },
  @{
    title = "CDX-02: Structural Minimization Lifecycle Drill Integration"
    label = "codex-native"
    body = @"
## Goal
Prove the turn minimization → transition prove arc on a real session tree, not only synthetic fixtures.

## Target
- ``tests/turn-minimization.test.ts`` (or equivalent bridge tests)

## Acceptance criteria
- [ ] Live/integration pass drives data through ``fl investigate turns --minimize`` into ``fl prove transition``.
- [ ] Uses the authentic session tree from CDX-01 where available.
- [ ] Failures are deterministic and CI-safe (skip only with explicit env gates if Docker/runtime is required).

## Local verification
``````bash
pnpm test -- tests/turn-minimization-bridge.test.ts
# If a dedicated turn-minimization test file is added:
pnpm test -- tests/turn-minimization.test.ts
``````
"@
  },
  @{
    title = "CDX-03: Prevention Level Cryptographic Traceability Grounding"
    label = "codex-native"
    body = @"
## Goal
Ensure turn-proof execution IDs ground into explicit ``PREVENTION_VERIFIED`` states before any summary fallback.

## Targets
- ``src/prevention-from-artifacts.ts``
- Evidence summary / prevention summary helpers (add ``src/evidence-summary.ts`` if needed)

## Acceptance criteria
- [ ] Wire turn-proof execution IDs into the cryptographic verification path.
- [ ] Turn paths can reach explicit ``PREVENTION_VERIFIED`` states analogous to git-tracking logic.
- [ ] Regression test covers verified grounding vs summary-only fallback.

## Local verification
``````bash
pnpm test -- tests/prevention-proof.test.ts
``````
"@
  },
  @{
    title = "CDX-04: Out-of-the-Box Local Codex Execution Runner Setup"
    label = "codex-native"
    body = @"
## Goal
If Codex CLI is already on ``PATH``, FaultLine should use it without requiring a bespoke runner config.

## Targets
- New ``src/runners/codex-runner.ts`` (or equivalent)
- ``fl witness implement --with-codex``
- ``fl repair --with-codex``

## Acceptance criteria
- [ ] Scan system ``PATH`` for the Codex CLI.
- [ ] When discovered, initialize a built-in zero-configuration command runner loop.
- [ ] Preserve existing explicit ``--with-codex`` / safety gates (no silent full-auto escalation).

## Local verification
``````bash
pnpm test -- tests/codex-loop.test.ts tests/cli.test.ts
``````
"@
  },
  @{
    title = "CDX-05: Fine-Grained Sub-Turn Attribution Ledger Tracking"
    label = "codex-native"
    body = @"
## Goal
Record tool-level attribution without capturing private transcripts or raw command text.

## Targets
- ``src/codex-sidecar.ts``
- ``src/ledger.ts``

## Acceptance criteria
- [ ] Recorder catches Codex ``PreToolUse`` / ``PostToolUse`` events.
- [ ] Log tool names and command hashes into the execution ledger.
- [ ] Strict privacy guard: never log raw source text strings or transcripts — identifiers and digests only.
- [ ] Schema + verification updates for the new event types.

## Local verification
``````bash
pnpm test -- tests/codex-sidecar.test.ts tests/ledger.test.ts
``````
"@
  },
  @{
    title = "CDX-06: Invariant Constraints Materialization Layer (AGENTS.md)"
    label = "codex-native"
    body = @"
## Goal
After prevention verification, automatically materialize a structured invariant snippet into project docs.

## Targets
- Prevention write path (``src/prevention-proof.ts`` / new ``src/prevention-writer.ts`` as needed)
- Root ``AGENTS.md`` append behavior

## Acceptance criteria
- [ ] ``fl prevention write`` maps verified execution parameters into a clean structured documentation snippet.
- [ ] Append that snippet to the project root ``AGENTS.md``.
- [ ] Idempotent / bounded write behavior (no unbounded duplicate spam; no secret leakage).
- [ ] Regression coverage for generated snippet content.

## Local verification
``````bash
pnpm test -- tests/prevention-proof.test.ts
``````
"@
  }
)

$created = @()
$tempRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("faultline-issues-" + [guid]::NewGuid().ToString("n"))
New-Item -ItemType Directory -Path $tempRoot | Out-Null
try {
  $index = 0
  foreach ($issue in $issues) {
    $index += 1
    Write-Host "Creating: $($issue.title)"
    $bodyFile = Join-Path $tempRoot ("issue-{0:d2}.md" -f $index)
    # Normalize to LF so GitHub renders markdown cleanly.
    $normalized = ($issue.body -replace "`r`n", "`n").Trim() + "`n"
    [System.IO.File]::WriteAllText($bodyFile, $normalized, [System.Text.UTF8Encoding]::new($false))

    $url = & gh issue create `
      --repo $repo `
      --title $issue.title `
      --body-file $bodyFile `
      --label $issue.label
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($url)) {
      throw "Failed to create issue: $($issue.title)"
    }
    $created += $url.Trim()
    Write-Host "  -> $($url.Trim())"
  }
} finally {
  Remove-Item -Recurse -Force $tempRoot -ErrorAction SilentlyContinue
}

Write-Host ""
Write-Host "Created $($created.Count) issues:"
$created | ForEach-Object { Write-Host $_ }
