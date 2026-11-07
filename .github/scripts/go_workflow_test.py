#!/usr/bin/env python3
"""Structural guard for the `go` job in .github/workflows/go.yml.

Standard library only (no YAML parser), so it reads the job as text. It checks
structure; it does not replace hosted execution.
"""
import re
import unittest
from pathlib import Path

WORKFLOW = Path(__file__).resolve().parents[1] / "workflows" / "go.yml"
OSES = ["ubuntu-24.04", "ubuntu-24.04-arm", "macos-latest", "macos-15-intel", "windows-latest", "windows-11-arm"]


def go_job():
    text = WORKFLOW.read_text()
    m = re.search(r"^  go:\n(.*?)(?=^  \S|\Z)", text, re.S | re.M)
    assert m, "go job not found"
    return m.group(1)


def matrix_list(job, key):
    m = re.search(r"^        %s: \[([^\]]*)\]" % key, job, re.M)
    return [v.strip() for v in m.group(1).split(",")] if m else []


def steps(job):
    body = job.split("    steps:\n", 1)[1]
    parts = re.split(r"^      - ", body, flags=re.M)[1:]
    return parts


class GoWorkflow(unittest.TestCase):
    def setUp(self):
        self.job = go_job()

    def test_twelve_legs(self):
        self.assertEqual(matrix_list(self.job, "os"), OSES)
        variants = matrix_list(self.job, "variant")
        self.assertEqual(variants, ["untagged", "tagged"])
        legs = {(o, v) for o in OSES for v in variants}
        self.assertEqual(len(legs), 12)

    def test_commands(self):
        for cmd in [
            "go mod tidy -diff",
            "go vet ./...",
            "go test -count=1 -timeout 30m -v ./...",
            "go vet -tags faultline_test ./...",
            "go test -tags faultline_test -count=1 -timeout 120m -v ./...",
            "go test -count=1 -timeout 15m -v ./difftest/testdata/globalopen/",
        ]:
            self.assertEqual(self.job.count(cmd), 1, cmd)

    def test_variant_gating(self):
        by_cmd = {}
        for s in steps(self.job):
            by_cmd[s] = s
        def step_with(cmd):
            found = [s for s in by_cmd if cmd in s]
            self.assertEqual(len(found), 1, cmd)
            return found[0]
        for cmd in ["go mod tidy -diff", "go vet ./...\n"]:
            self.assertIn("matrix.variant == 'untagged'", step_with(cmd), cmd)
        self.assertIn("matrix.variant == 'tagged'", step_with("go vet -tags faultline_test ./...\n"))

    def test_logs_separate(self):
        self.assertIn("go-${{ matrix.variant }}.log", self.job)
        self.assertNotIn("/go.log", self.job)

    def test_skip_guards_kept(self):
        for s in [
            ".github/scripts/check-skips.sh",
            ".github/scripts/check-serve-bundle-skip-reasons.sh",
            ".github/scripts/windows-writer-skips.txt",
        ]:
            self.assertIn(s, self.job)
        self.assertNotIn("t.Parallel", self.job)

    def test_coverage_only_ubuntu_tagged(self):
        envs = re.findall(r"FAULTLINE_COVERAGE_GATE: (.*)", self.job)
        self.assertEqual(len(envs), 1)
        self.assertIn("matrix.os == 'ubuntu-24.04'", envs[0])
        self.assertIn("matrix.variant == 'tagged'", envs[0])

    def test_one_release_override_check_per_os(self):
        found = [s for s in steps(self.job) if "FAULTLINE_TEST_OPENAI_URL" in s and "go build" in s]
        self.assertEqual(len(found), 1)
        self.assertIn("matrix.variant == 'untagged'", found[0])


SHARDS = {
    "platform-process": "TestLiveStackThresholdsMatchNode TestLiveSpreadLimitsMatchNode TestLiveRimrafDepthMatchesNode TestLiveStdoutMatchesNode TestLiveSignalsMatchNode TestLiveNofileMatchesNode TestLiveGroupSignalMatchesNode TestLiveStderrMatchesNode TestLiveJudgeServer TestLiveJudgeSignalStop TestLiveBrowserOpen TestLiveSeedPreload TestLiveSpawn TestLiveHardenedGit TestLiveMaterialize TestLiveHostCmd TestLiveResolveCodexRunner".split(),
    "turn-incident": "TestLiveInvestigateTurns TestLiveTurnSnapshotCapture TestLiveIncidentSuggest TestLiveIncidentDraft".split(),
    "context-sidecar": "TestLiveProjectContext TestLiveSidecarHooks TestLiveCpSyncTree".split(),
}
NARROWING = [
    "FAULTLINE_TURNSNAP_CASES", "FAULTLINE_TURNINV_CASES", "FAULTLINE_TURNPACK_CASES",
    "FAULTLINE_TURNWRITE_CASES", "FAULTLINE_CP_CASES", "FAULTLINE_SIDECAR_SEQS",
    "FAULTLINE_SIDECAR_STORES", "FAULTLINE_SIDECAR_STORES_FROM", "FAULTLINE_REPAIR_WT_CASES",
    "FAULTLINE_REPAIR_ONLY", "FAULTLINE_ABORT_CASE", "FAULTLINE_TURN_SNAPSHOT_TRACKED_ONLY",
    "FAULTLINE_COVER_ONLY",
]


def named_job(name):
    text = WORKFLOW.read_text()
    m = re.search(r"^  %s:\n(.*?)(?=^  \S|\Z)" % re.escape(name), text, re.S | re.M)
    assert m, name + " job not found"
    return m.group(1)


class StackLiveShards(unittest.TestCase):
    def setUp(self):
        self.job = named_job("stack-live")

    def test_matrix(self):
        self.assertEqual(matrix_list(self.job, "os"), ["ubuntu-24.04-arm", "macos-latest"])
        self.assertEqual(matrix_list(self.job, "suite"), list(SHARDS))

    def test_exact_groups(self):
        for suite, tests in SHARDS.items():
            m = re.search(r"- suite: %s\n\s+tests: (.*)" % suite, self.job)
            self.assertIsNotNone(m, suite)
            self.assertEqual(m.group(1).strip().strip("'\"").split(), tests, suite)
        every = [t for ts in SHARDS.values() for t in ts]
        self.assertEqual(len(every), len(set(every)))
        self.assertEqual(len(every), 24)

    def test_duplicate_removed(self):
        self.assertNotIn("TestLiveServeBundleStackCallSites", self.job)
        self.assertNotIn("TestLiveServeBundleStackCallSites", named_job("serve-bundle-native"))
        self.assertIn("TestLiveServeBundleStackCallSites", named_job("serve-bundle-stack-native"))

    def test_full_sets_and_unset(self):
        run = self.job.split("name: Live platform tests", 1)[1].split("\n      - name:", 1)[0]
        for v in NARROWING:
            self.assertRegex(run, r"unset [^\n]*\b%s\b" % v)
            self.assertNotRegex(self.job, r"%s=" % v)
        self.assertNotIn("t.Parallel", self.job)

    def test_per_shard_log_and_skips(self):
        self.assertIn("live-${{ matrix.suite }}.log", self.job)
        self.assertIn("live-${{ matrix.suite }}.list", self.job)
        self.assertNotIn("/live.log", self.job)
        self.assertIn(
            "allowed='^(TestLiveRimrafDepthMatchesNode|TestLiveSignalsMatchNode|TestLiveStdoutMatchesNode|TestLiveProjectContext/sparse256MiB|TestLiveCpSyncTree)$'",
            self.job)
        self.assertIn("check-skips.sh", self.job)

    def test_platform_only_steps(self):
        for marker in ["Measured stack depths", "TimSort against V8"]:
            self.assertEqual(self.job.count(marker), 1, marker)
            step = self.job.split("name: " + marker, 1)[1].split("\n      - ", 1)[0]
            self.assertIn("matrix.suite == 'platform-process'", step, marker)
        self.assertEqual(self.job.count("TestSortMatchesV8 -v"), 1)
        self.assertEqual(self.job.count("stack_live_test"), 3)


SERVE_TARGETS = [
    ("ubuntu-24.04", "linux", "amd64"), ("ubuntu-24.04-arm", "linux", "arm64"),
    ("macos-latest", "darwin", "arm64"), ("macos-15-intel", "darwin", "amd64"),
    ("windows-latest", "windows", "amd64"), ("windows-11-arm", "windows", "arm64"),
]


def targets(job):
    return re.findall(r"- \{ os: (\S+), goos: (\S+), goarch: (\S+) \}", job)


class ServeStackProbes(unittest.TestCase):
    def setUp(self):
        self.native = named_job("serve-bundle-native")
        self.stack = named_job("serve-bundle-stack-native")

    def test_same_six_targets(self):
        self.assertEqual(targets(self.native), SERVE_TARGETS)
        self.assertEqual(targets(self.stack), SERVE_TARGETS)
        self.assertIn("fail-fast: false", self.stack)
        self.assertIn("FAULTLINE_EVIDENCE_EXPECT_GOOS: ${{ matrix.goos }}", self.stack)
        self.assertIn("FAULTLINE_EVIDENCE_EXPECT_GOARCH: ${{ matrix.goarch }}", self.stack)

    def test_whole_stack_test_only_in_stack_job(self):
        self.assertNotIn("TestLiveServeBundleStackCallSites", self.native)
        self.assertEqual(self.stack.count("TestLiveServeBundleStackCallSites"), 1)
        self.assertIn("tests='TestLiveServeBundleStackCallSites'", self.stack)
        self.assertNotIn("TestLiveServeBundleStackCallSites/", self.stack)
        for other in ["TestLiveServeBundleEvidenceIdentity", "TestLiveServeBundleBinding", "TestLiveServeBundleSignals",
                      "TestServeBundleEvidenceAudit", "TestServeBundleStandalone"]:
            self.assertNotIn(other, self.stack, other)

    def test_own_log_list_and_no_skip(self):
        self.assertIn("serve-stack.list", self.stack)
        self.assertIn("serve-stack.log", self.stack)
        self.assertNotIn("serve-live.", self.stack)
        self.assertIn('check-skips.sh "$RUNNER_TEMP/serve-stack.log" \'^$\' "$RUNNER_TEMP/serve-stack.list"', self.stack)
        self.assertIn("-timeout 100m", self.stack)
        self.assertNotIn("t.Parallel", self.stack)
        self.assertNotIn("EVIDENCE_DIR", self.stack)

    def test_unique_artifact(self):
        ups = [s for s in steps(self.stack) if "upload-artifact" in s]
        self.assertEqual(len(ups), 1)
        self.assertIn("name: serve-bundle-stack-native-${{ matrix.os }}", ups[0])
        self.assertIn("serve-stack.log", ups[0])
        self.assertIn("if: always()", ups[0])
        nat = [s for s in steps(self.native) if "upload-artifact" in s]
        self.assertEqual(len(nat), 1)
        self.assertIn("name: serve-bundle-native-${{ matrix.os }}\n", nat[0])

    def test_native_evidence_preserved(self):
        for frag in ["TestLiveServeBundleEvidenceIdentity TestLiveServeBundle TestLiveServeBundleBinding TestLiveServeBundleSignals",
                     "check-serve-bundle-counts.sh", "check-serve-bundle-skip-reasons.sh",
                     "TestServeBundleEvidenceAudit", "SERVE-BUNDLE-EVIDENCE-AUDIT ok",
                     "TestServeBundleStandalone",
                     "${{ runner.temp }}/serve-bundle-evidence"]:
            self.assertIn(frag, self.native, frag)
        self.assertEqual(self.native.count("serve-bundle-evidence"), 2)
        for bad in ["partition", "merge"]:
            self.assertNotIn(bad, self.native.lower().replace("merge ref", ""))
            self.assertNotIn(bad, self.stack.lower())

    def test_gate_needs_stack_job(self):
        self.assertIn("serve-bundle-stack-native", needs_of(job_text(WORKFLOW, "go-ci-gate")))


WRITER_SHARDS = {
    "core": (105, "propose-input policy-freeze ratify freeze ledger-bind minimize-git investigate-two-states commit-proof-preview"),
    "runtime-init": (114, "runtime-resolve-node runtime-mapping-nested-output init-yes-dirty init-standing init-cli doctor-security"),
    "incident-prevention": (147, "incident-start-command incident-continue-unapproved incident-continue-inherit-digest prevention-write-bundle-ok prevention-write-input-summary prevention-write-input-agents-append"),
    "turns": (75, "turns-proof turns-minimize"),
    "attest-repair": (111, "attest-create-git provenance-create-ok repair-brief-input-ok witness-implement-template repair-bundle-codex-ok"),
    "record-sidecar": (102, "record-init record-checkpoint codex-snapshot-clean codex-gc-force codex-gc-alternates-other-entries sidecar-hook-session-quiet sidecar-hook-full-turn"),
}


def step_named(job, name):
    found = [s for s in steps(job) if s.startswith("name: " + name)]
    assert len(found) == 1, name
    return found[0]


class WriterFaultShards(unittest.TestCase):
    def setUp(self):
        self.job = named_job("difftest-live-faults")

    def test_matrix_membership(self):
        entries = re.findall(r"- shard: (\S+)\n\s+count: (\d+)\n\s+cases: (.*)", self.job)
        self.assertEqual([e[0] for e in entries], list(WRITER_SHARDS))
        seen = []
        for shard, count, cases in entries:
            self.assertEqual(int(count), WRITER_SHARDS[shard][0], shard)
            self.assertEqual(cases.strip().strip("'\"").split(), WRITER_SHARDS[shard][1].split(), shard)
            seen += cases.strip().strip("'\"").split()
        self.assertEqual(len(seen), len(set(seen)))
        self.assertEqual(sum(int(e[1]) for e in entries), 654)

    def test_matrix_runner(self):
        self.assertIn("runs-on: ubuntu-24.04\n", self.job)
        self.assertIn("fail-fast: false", self.job)

    def test_exact_first_segment_and_guards(self):
        run = step_named(self.job, "Writer faults (sharded)")
        self.assertIn("^TestLiveWriterFaults\\$/^(", run)
        self.assertIn("matrix.cases", run + self.job)
        self.assertIn("tests='TestLiveWriterFaults'", run)
        self.assertIn("printf '%s\\n' $tests", run)
        self.assertIn("check-skips.sh", run)
        self.assertIn("'^$'", run)
        self.assertIn('check-writer-fault-shards.sh "${{ matrix.shard }}" "${{ matrix.count }}"', run)
        self.assertIn("writer-${{ matrix.shard }}.log", run)
        self.assertNotIn("TestLiveRealDocker", run)
        self.assertNotIn("t.Parallel", self.job)

    def test_every_leg_installs_strace(self):
        s = step_named(self.job, "Install strace")
        self.assertNotIn("if:", s)
        self.assertIn("install -y -qq strace", s)

    def test_core_owns_docker_and_globalopen(self):
        for name in [
            "Docker's classic image store",
            "Pull the pinned image",
            "GLOBALOPEN helper native controls",
            "Real Docker and demo artifact faults",
        ]:
            self.assertRegex(step_named(self.job, name), r"if: (always\(\) && )?matrix\.shard == 'core'", name)
        for marker in ["TestLiveRealDocker ", "TestLiveRealDockerProjectBuild", "TestLiveDemoArtifactFaults", "docker pull", "TestGlobalOpenDefaultHelperScenarios"]:
            self.assertEqual(self.job.count(marker) >= 1, True, marker)
        real = step_named(self.job, "Real Docker and demo artifact faults")
        self.assertIn("tests='TestLiveRealDocker TestLiveRealDockerProjectBuild TestLiveDemoArtifactFaults'", real)
        self.assertEqual(self.job.count("docker pull"), 1)
        self.assertEqual(self.job.count("FAULTLINE_REAL_DOCKER=1"), 1)
        self.assertNotIn("TestLiveWriterFaults", real.replace("TestLiveWriterFaults fails", ""))

    def test_unique_artifacts_with_raw_log(self):
        uploads = [s for s in steps(self.job) if "upload-artifact" in s]
        self.assertEqual(len(uploads), 1)
        up = uploads[0]
        self.assertIn("name: writer-faults-${{ matrix.shard }}", up)
        self.assertIn("writer-${{ matrix.shard }}.log", up)
        self.assertNotIn("name: globalopen-proof\n", up)
        self.assertIn("if: always()", up)

    def test_no_seventh_job_and_difftest_live_exclusion(self):
        text = WORKFLOW.read_text()
        self.assertEqual(len(re.findall(r"^  difftest-live-faults:", text, re.M)), 1)
        self.assertNotIn("difftest-live-faults-", text)
        live = named_job("difftest-live")
        self.assertIn("TestLiveWriterFaults", live)


VERIFY = WORKFLOW.parent / "verify.yml"
GO_JOBS = ["go", "difftest-goldens", "difftest-live", "difftest-live-investigate", "difftest-live-writers",
           "difftest-live-replay", "difftest-live-faults", "stack-live", "serve-bundle-native",
           "serve-bundle-stack-native", "windows-console-diagnostic"]
VERIFY_JOBS = ["submission-freeze", "test", "docker-integration", "docker-integration-windows",
               "docker-integration-macos-colima", "tag-checkout", "provenance"]


def job_ids(path):
    text = path.read_text()
    body = text.split("\njobs:\n", 1)[1]
    return re.findall(r"^  ([A-Za-z_][\w-]*):\n", body, re.M)


def job_text(path, name):
    body = path.read_text().split("\njobs:\n", 1)[1]
    m = re.search(r"^  %s:\n(.*?)(?=^  [A-Za-z_][\w-]*:\n|\Z)" % re.escape(name), body, re.S | re.M)
    assert m, name
    return m.group(1)


def needs_of(text):
    m = re.search(r"^    needs: \[([^\]]*)\]", text, re.M)
    return [v.strip() for v in m.group(1).split(",")] if m else []


class Gating:
    path = None
    heavy = []
    gate = None

    def test_triggers(self):
        text = self.path.read_text()
        on = text.split("\njobs:\n", 1)[0]
        self.assertRegex(on, r"(?m)^  pull_request:\n")
        self.assertRegex(on, r"(?m)^  push:\n    branches: \[main\]\n")
        self.assertEqual(len(re.findall(r"(?m)^  push:", on)), 1)

    def test_job_inventory(self):
        self.assertEqual(sorted(job_ids(self.path)), sorted(["scope", self.gate] + self.heavy))

    def test_heavy_jobs_scoped(self):
        for name in self.heavy:
            t = job_text(self.path, name)
            self.assertIn("scope", needs_of(t), name)
            self.assertIn("needs.scope.outputs.scope == 'code'", t.split("    steps:", 1)[0], name)

    def test_scope_job(self):
        t = job_text(self.path, "scope")
        self.assertIn("fetch-depth: 0", t)
        self.assertIn(".github/scripts/ci_scope.sh", t)
        self.assertIn("GITHUB_OUTPUT", t)
        self.assertIn("github.event.pull_request.base.sha", t)
        self.assertIn("github.event.pull_request.head.sha", t)
        self.assertIn("github.event.before", t)
        self.assertIn("github.sha", t)
        self.assertIn("scope: ${{ steps.scope.outputs.scope }}", t)
        self.assertIn("sort", t)
        self.assertIn("git diff --check", t)
        self.assertRegex(t, r"docs-only")

    def test_gate_needs_complete(self):
        t = job_text(self.path, self.gate)
        needs = needs_of(t)
        self.assertEqual(len(needs), len(set(needs)), "duplicate needs")
        self.assertEqual(sorted(needs), sorted(["scope"] + self.heavy))
        self.assertIn("if: always()\n", t)

    def test_gate_invocation(self):
        t = job_text(self.path, self.gate)
        self.assertEqual(t.count("ci_gate.py"), 1)
        for frag in ["--scope", "needs.scope.outputs.scope", "github.event_name", "github.ref", "toJSON(needs)"]:
            self.assertIn(frag, t)
        self.assertIn("EVENT_NAME: ${{ github.event_name }}", t)
        self.assertIn("REF: ${{ github.ref }}", t)
        self.assertIn('--event "$EVENT_NAME" --ref "$REF"', t)
        run = t.rsplit("run: |", 1)[1]
        self.assertNotIn("${{", run)

    def test_no_third_party_paths_action(self):
        text = self.path.read_text()
        for bad in ["dorny/paths-filter", "tj-actions/changed-files", "paths-ignore"]:
            self.assertNotIn(bad, text)
        self.assertNotRegex(text, r"(?m)^\s+paths:")


class GoGating(Gating, unittest.TestCase):
    path = WORKFLOW
    heavy = GO_JOBS
    gate = "go-ci-gate"

    def test_writer_artifacts_and_total(self):
        t = job_text(self.path, "go-ci-gate")
        downloads = [s for s in steps(t) if "download-artifact" in s]
        self.assertEqual(len(downloads), 6)
        dirs = []
        for shard in WRITER_SHARDS:
            d = [s for s in downloads if "name: writer-faults-%s\n" % shard in s]
            self.assertEqual(len(d), 1, shard)
            self.assertIn("needs.scope.outputs.scope == 'code'", d[0])
            self.assertIn("download-artifact@d3f86a106a0bac45b974a628896c90dbdf5c8093", d[0])
            m = re.search(r"path: (.+)", d[0])
            dirs.append(m.group(1))
            self.assertIn(shard, m.group(1))
        self.assertEqual(len(set(dirs)), 6)
        total = [s for s in steps(t) if "check-writer-fault-shards.sh total 654" in s]
        self.assertEqual(len(total), 1)
        self.assertIn("needs.scope.outputs.scope == 'code'", total[0])
        for shard in WRITER_SHARDS:
            self.assertEqual(total[0].count("writer-%s.log" % shard), 1, shard)

    def test_provenance_not_in_go(self):
        self.assertNotIn("provenance", job_text(self.path, "go-ci-gate"))


class VerifyGating(Gating, unittest.TestCase):
    path = VERIFY
    heavy = VERIFY_JOBS
    gate = "verify-ci-gate"

    def test_dispatch_preserved(self):
        on = self.path.read_text().split("\njobs:\n", 1)[0]
        self.assertIn("workflow_dispatch:", on)
        self.assertIn("unfreeze_override:", on)

    def test_provenance_graph_and_skip_name(self):
        p = job_text(self.path, "provenance")
        self.assertEqual(sorted(needs_of(p)), ["docker-integration", "scope", "test"])
        self.assertIn("github.event_name == 'push' && github.ref == 'refs/heads/main'", p)
        self.assertIn("name: signed GitHub CI provenance", p)
        g = job_text(self.path, "verify-ci-gate")
        self.assertIn("signed GitHub CI provenance", g)


if __name__ == "__main__":
    unittest.main()
