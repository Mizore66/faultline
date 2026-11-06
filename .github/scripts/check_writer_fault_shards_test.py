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


CASES = {
    "core": ["propose-input", "policy-freeze", "ratify", "freeze", "ledger-bind",
             "minimize-git", "investigate-two-states", "commit-proof-preview"],
    "runtime-init": ["runtime-resolve-node", "runtime-mapping-nested-output",
                     "init-yes-dirty", "init-standing", "init-cli", "doctor-security"],
    "incident-prevention": ["incident-start-command", "incident-continue-unapproved",
                            "incident-continue-inherit-digest", "prevention-write-bundle-ok",
                            "prevention-write-input-summary",
                            "prevention-write-input-agents-append"],
    "turns": ["turns-proof", "turns-minimize"],
    "attest-repair": ["attest-create-git", "provenance-create-ok", "repair-brief-input-ok",
                      "witness-implement-template", "repair-bundle-codex-ok"],
    "record-sidecar": ["record-init", "record-checkpoint", "codex-snapshot-clean",
                       "codex-gc-force", "codex-gc-alternates-other-entries",
                       "sidecar-hook-session-quiet", "sidecar-hook-full-turn"],
}


def leaf(case, i, status="PASS"):
    return "    --- %s: TestLiveWriterFaults/%s/label%d/close-EIO (0.01s)" % (status, case, i)


def leaves(shard, n, start=0):
    cs = CASES[shard]
    return [leaf(cs[i % len(cs)], i + start) for i in range(n)]


def log_lines(shard, n):
    cs = CASES[shard]
    return (
        ["=== RUN   TestLiveWriterFaults/%s" % cs[0],
         "    --- PASS: TestLiveWriterFaults/%s/label0 (0.01s)" % cs[0]]
        + leaves(shard, n)
        + ["--- PASS: TestLiveWriterFaults/%s (1.00s)" % cs[0], "PASS"]
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
        lines.insert(4, lines[3])
        p = self.write("t", lines)
        self.fail(self.run_guard("turns", "75", p), "duplicate leaf")

    def test_foreign_case_other_shard(self):
        lines = log_lines("turns", 74) + [leaf("propose-input", 900)]
        p = self.write("t", lines)
        self.fail(self.run_guard("turns", "75", p), "foreign caseID")

    def test_turns_proof_in_core_is_foreign(self):
        lines = log_lines("core", 104) + [leaf("turns-proof", 900)]
        p = self.write("c", lines)
        self.fail(self.run_guard("core", "105", p), "foreign caseID")

    def test_shard_name_is_not_a_case_id(self):
        lines = log_lines("turns", 74) + [leaf("turns", 900)]
        p = self.write("t", lines)
        self.fail(self.run_guard("turns", "75", p), "foreign caseID")

    def test_unknown_case(self):
        lines = log_lines("turns", 75) + [leaf("bogus", 1)]
        p = self.write("t", lines)
        self.fail(self.run_guard("turns", "75", p), "foreign caseID")

    def test_failed_leaf(self):
        lines = log_lines("turns", 74) + [leaf("turns-proof", 900, "FAIL")]
        p = self.write("t", lines)
        self.fail(self.run_guard("turns", "75", p), "non-PASS leaf")

    def test_skipped_leaf(self):
        lines = log_lines("turns", 74) + [leaf("turns-minimize", 900, "SKIP")]
        p = self.write("t", lines)
        self.fail(self.run_guard("turns", "75", p), "non-PASS leaf")

    def test_label_with_slash_counts(self):
        lines = log_lines("turns", 74) + [
            "    --- PASS: TestLiveWriterFaults/turns-proof/a/b/close-EIO (0.01s)"
        ]
        p = self.write("t", lines)
        proc = self.run_guard("turns", "75", p)
        self.assertEqual(proc.returncode, 0, proc.stderr)

    def test_malformed_leaf_fails_loudly(self):
        lines = log_lines("turns", 75) + [
            "    --- PASS: TestLiveWriterFaults/turns-proof/label/closeEIO (0.01s)"
        ]
        p = self.write("t", lines)
        self.fail(self.run_guard("turns", "75", p), "malformed leaf")

    def test_wrong_expected(self):
        p = self.write("t", log_lines("turns", 74))
        self.fail(self.run_guard("turns", "74", p), "expected count")

    def test_duplicate_across_shards(self):
        logs = self.six()
        # same leaf as the first one in the core log, placed in turns log
        # instead of a turns leaf, so per-log counts stay exact.
        with open(logs[0]) as f:
            core_leaf = [l for l in f.read().splitlines() if "close-EIO" in l][0]
        turns = log_lines("turns", 74) + [core_leaf]
        logs[3] = self.write("turns2", turns)
        self.fail(self.run_guard("total", "654", *logs), "duplicate leaf")

    def test_total_missing_shard(self):
        self.fail(self.run_guard("total", "654", *self.six()[:5]), "expected 654")

    def test_total_checks_per_shard_sums(self):
        logs = self.six()
        # move one core leaf's worth: core 104 + turns 76 keeps 654 total.
        logs[0] = self.write("c", log_lines("core", 104))
        logs[3] = self.write("t", log_lines("turns", 76))
        self.fail(self.run_guard("total", "654", *logs), "shard core has 104")

    def test_parent_only_summary(self):
        lines = [
            "--- PASS: TestLiveWriterFaults (9.00s)",
            "--- PASS: TestLiveWriterFaults/ratify (1.00s)",
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
