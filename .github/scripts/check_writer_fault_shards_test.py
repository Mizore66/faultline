import os
import subprocess
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
GUARD = os.path.join(HERE, "check-writer-fault-shards.sh")
SHARDS = [
    ("core", 105),
    ("runtime-init", 114),
    ("incident-prevention", 147),
    ("turns", 75),
    ("attest-repair", 111),
    ("record-sidecar", 102),
]


def leaves(case, n, status="PASS"):
    return [
        "    --- %s: TestLiveWriterFaults/%s/label%d/close-EIO (0.01s)" % (status, case, i)
        for i in range(n)
    ]


def log_lines(case, n):
    return (
        ["=== RUN   TestLiveWriterFaults/%s" % case]
        + leaves(case, n)
        + ["--- PASS: TestLiveWriterFaults/%s (1.00s)" % case, "PASS"]
    )


class GuardTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)

    def write(self, name, lines):
        p = os.path.join(self.tmp.name, name + ".log")
        with open(p, "w") as f:
            f.write("\n".join(lines) + "\n")
        return p

    def run_guard(self, *args):
        return subprocess.run([GUARD, *args], text=True, capture_output=True)

    def six(self):
        return [self.write(c, log_lines(c, n)) for c, n in SHARDS]

    def fail(self, proc, text):
        self.assertNotEqual(proc.returncode, 0, proc.stdout)
        self.assertIn(text, proc.stderr)

    def test_each_shard_passes(self):
        for (c, n), p in zip(SHARDS, self.six()):
            proc = self.run_guard(c, str(n), p)
            self.assertEqual(proc.returncode, 0, proc.stderr)

    def test_total_passes(self):
        proc = self.run_guard("total", "654", *self.six())
        self.assertEqual(proc.returncode, 0, proc.stderr)

    def test_missing_leaf(self):
        p = self.write("t", log_lines("turns", 74))
        self.fail(self.run_guard("turns", "75", p), "expected 75")

    def test_duplicate_leaf(self):
        lines = log_lines("turns", 75)
        lines.insert(3, lines[2])
        p = self.write("t", lines)
        self.fail(self.run_guard("turns", "75", p), "duplicate leaf")

    def test_foreign_case(self):
        lines = log_lines("turns", 74) + leaves("core", 1)
        p = self.write("t", lines)
        self.fail(self.run_guard("turns", "75", p), "foreign caseID")

    def test_unknown_case(self):
        lines = log_lines("turns", 75) + leaves("bogus", 1)
        p = self.write("t", lines)
        self.fail(self.run_guard("turns", "75", p), "foreign caseID")

    def test_failed_leaf(self):
        lines = log_lines("turns", 74) + leaves("turns", 1, "FAIL")[:0]
        lines.append("    --- FAIL: TestLiveWriterFaults/turns/zz/close-EIO (0.01s)")
        p = self.write("t", lines)
        self.fail(self.run_guard("turns", "75", p), "non-PASS leaf")

    def test_skipped_leaf(self):
        lines = log_lines("turns", 74)
        lines.append("    --- SKIP: TestLiveWriterFaults/turns/zz/close-EIO (0.00s)")
        p = self.write("t", lines)
        self.fail(self.run_guard("turns", "75", p), "non-PASS leaf")

    def test_wrong_expected(self):
        p = self.write("t", log_lines("turns", 74))
        self.fail(self.run_guard("turns", "74", p), "expected count")

    def test_duplicate_across_shards(self):
        logs = self.six()
        extra = self.write("dup", leaves("core", 1))
        # replace one turns leaf-count neutral: core log already has label0
        proc = self.run_guard("total", "654", *logs, extra)
        self.fail(proc, "duplicate leaf")

    def test_total_missing_shard(self):
        self.fail(self.run_guard("total", "654", *self.six()[:5]), "expected 654")

    def test_parent_only_summary(self):
        lines = [
            "--- PASS: TestLiveWriterFaults (9.00s)",
            "--- PASS: TestLiveWriterFaults/core (1.00s)",
            "len(matrix)=654",
            "654",
            "PASS",
        ]
        p = self.write("p", lines)
        self.fail(self.run_guard("total", "654", p), "expected 654")

    def test_parent_pass_hides_dropped_case(self):
        logs = self.six()
        logs[3] = self.write("t2", log_lines("turns", 74) + ["len(matrix)=654"])
        self.fail(self.run_guard("total", "654", *logs), "turns")

    def test_bad_usage(self):
        self.assertNotEqual(self.run_guard("core").returncode, 0)
        p = self.write("x", [])
        self.assertNotEqual(self.run_guard("nope", "1", p).returncode, 0)
        self.assertNotEqual(self.run_guard("core", "105", p + ".missing").returncode, 0)


if __name__ == "__main__":
    unittest.main()
