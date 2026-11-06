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


if __name__ == "__main__":
    unittest.main()
