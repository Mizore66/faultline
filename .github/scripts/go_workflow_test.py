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
        self.assertIn("TestLiveServeBundleStackCallSites", named_job("serve-bundle-native"))

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


if __name__ == "__main__":
    unittest.main()
