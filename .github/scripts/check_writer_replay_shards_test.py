#!/usr/bin/env python3
"""Tests for check-writer-replay-shards.py using manifest-shaped synthetic logs."""
import json
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
GUARD = os.path.join(HERE, "check-writer-replay-shards.py")
MANIFESTS = os.path.join(HERE, "..", "..", "difftest", "testdata", "writer-shards")
RECORDER = "recorder-fake-docker"
GOLDEN = [336, 332, 316, 318]
WIN_COMPARED = [115, 113, 114, 109]
CASES = [286, 281, 257, 258]


def manifest(n):
    with open(os.path.join(MANIFESTS, "shard-%d.json" % n), encoding="utf-8") as f:
        return json.load(f)


def load_guard():
    import importlib.util
    spec = importlib.util.spec_from_file_location("check_writer_replay_shards", GUARD)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


class FilterGolden(unittest.TestCase):
    """Must equal Go's TestWriterShardFilterPythonGolden literal (writerShardFilterFor)."""

    def test_matches_go_filter(self):
        guard = load_guard()
        m = {"ordinary": ["a.b", "c+d", "e\\f"], "posix": ["p-1", "q+r"]}
        self.assertEqual(guard.run_filter(m), r"^TestWriterReplay$/^(?:a\.b|c\+d|e\\f|posix)$/^(?:p-1|q\+r)$")
        self.assertEqual(guard.run_filter({"ordinary": ["a.b"], "posix": []}), r"^TestWriterReplay$/^(?:a\.b)$")


def run(*args):
    p = subprocess.run([sys.executable, "-I", GUARD, *args], capture_output=True, text=True)
    return p.returncode, p.stdout + p.stderr


def log_lines(n, windows=False, compared=None):
    m = manifest(n)
    out = ["=== RUN   TestWriterReplay"]
    for i in m["ordinary"]:
        out += ["=== RUN   TestWriterReplay/%s" % i, "    --- PASS: TestWriterReplay/%s (0.01s)" % i]
    for i in m["posix"]:
        out.append("=== RUN   TestWriterReplay/posix/%s" % i)
        if windows:
            out += ["        writer_test.go:1862: POSIX fake git/docker recorders",
                    "    --- SKIP: TestWriterReplay/posix/%s (0.00s)" % i]
        else:
            out.append("    --- PASS: TestWriterReplay/posix/%s (0.01s)" % i)
    sel = len(m["ordinary"]) + len(m["posix"])
    ex = len(m["ordinary"]) if windows else sel
    if compared is None:
        compared = WIN_COMPARED[n] if windows else GOLDEN[n]
    out += ["    writer_test.go:1521: LIVE-SUMMARY TestWriterReplay: %d selected, %d executed writer cases, %d golden invocations compared, 0 Go bundles verified by TS" % (sel, ex, compared),
            "--- PASS: TestWriterReplay (12.00s)", "PASS"]
    return out


class Env:
    def __init__(self, test):
        self.dir = tempfile.TemporaryDirectory()
        test.addCleanup(self.dir.cleanup)

    def write(self, name, lines):
        p = os.path.join(self.dir.name, name)
        os.makedirs(os.path.dirname(p), exist_ok=True)
        with open(p, "w", encoding="utf-8") as f:
            f.write("\n".join(lines) + "\n")
        return p

    def total_dir(self, windows=False, mutate=None):
        logs = os.path.join(self.dir.name, "logs")
        for n in range(4):
            lines = log_lines(n, windows)
            if mutate:
                lines = mutate(n, lines)
            self.write("logs/writer-replay-%d/writer-replay-shard-%d.log" % (n, n), lines)
        return MANIFESTS, logs


class ManifestShape(unittest.TestCase):
    def test_totals(self):
        for n in range(4):
            m = manifest(n)
            self.assertEqual(len(m["ordinary"]) + len(m["posix"]) - (n == 0), CASES[n])
        self.assertEqual(sum(CASES), 1082)
        self.assertEqual(sum(GOLDEN), 1302)
        self.assertEqual(sum(WIN_COMPARED), 451)


class ShardGuard(unittest.TestCase):
    def check(self, n, lines, windows=False):
        e = Env(self)
        args = ["--windows"] if windows else []
        return run(*args, str(n), os.path.join(MANIFESTS, "shard-%d.json" % n), e.write("x.log", lines))

    def test_valid_unix_all_shards(self):
        for n in range(4):
            rc, out = self.check(n, log_lines(n))
            self.assertEqual(rc, 0, out)

    def test_valid_windows_all_shards(self):
        for n in range(4):
            rc, out = self.check(n, log_lines(n, windows=True), windows=True)
            self.assertEqual(rc, 0, out)

    def test_recorder_only_in_shard_zero(self):
        self.assertIn(RECORDER, manifest(0)["posix"])
        for n in (1, 2, 3):
            self.assertNotIn(RECORDER, manifest(n)["posix"])
        lines = log_lines(1) + ["    --- PASS: TestWriterReplay/posix/%s (0.01s)" % RECORDER]
        rc, out = self.check(1, lines)
        self.assertEqual(rc, 1)
        self.assertIn("foreign", out)

    def test_missing_ordinary(self):
        m = manifest(2)["ordinary"][0]
        lines = [l for l in log_lines(2) if "TestWriterReplay/%s " % m not in l]
        rc, out = self.check(2, lines)
        self.assertEqual(rc, 1)
        self.assertIn("missing terminal result TestWriterReplay/%s" % m, out)

    def test_missing_posix_and_recorder(self):
        lines = [l for l in log_lines(0) if "posix/%s" % RECORDER not in l]
        rc, out = self.check(0, lines)
        self.assertEqual(rc, 1)
        self.assertIn("missing terminal result TestWriterReplay/posix/%s" % RECORDER, out)

    def test_duplicate(self):
        i = manifest(3)["ordinary"][0]
        rc, out = self.check(3, log_lines(3) + ["    --- PASS: TestWriterReplay/%s (0.01s)" % i])
        self.assertEqual(rc, 1)
        self.assertIn("duplicate", out)

    def test_foreign(self):
        rc, out = self.check(1, log_lines(1) + ["    --- PASS: TestWriterReplay/not-a-case (0.01s)"])
        self.assertEqual(rc, 1)
        self.assertIn("foreign", out)
        other = manifest(0)["ordinary"][0]
        rc, out = self.check(1, log_lines(1) + ["    --- PASS: TestWriterReplay/%s (0.01s)" % other])
        self.assertEqual(rc, 1)

    def test_posix_ordinary_cross_branch_is_foreign(self):
        i = manifest(2)["ordinary"][0]
        lines = [l.replace("TestWriterReplay/%s " % i, "TestWriterReplay/posix/%s " % i) for l in log_lines(2)]
        rc, _ = self.check(2, lines)
        self.assertEqual(rc, 1)

    def test_failed(self):
        i = manifest(0)["ordinary"][0]
        lines = [l.replace("--- PASS: TestWriterReplay/%s " % i, "--- FAIL: TestWriterReplay/%s " % i) for l in log_lines(0)]
        rc, out = self.check(0, lines)
        self.assertEqual(rc, 1)
        self.assertIn("is FAIL, want PASS", out)

    def test_unexpected_skip(self):
        i = manifest(0)["ordinary"][0]
        lines = [l.replace("--- PASS: TestWriterReplay/%s " % i, "--- SKIP: TestWriterReplay/%s " % i) for l in log_lines(0)]
        rc, out = self.check(0, lines)
        self.assertEqual(rc, 1)
        self.assertIn("is SKIP, want PASS", out)

    def test_unix_posix_skip_rejected(self):
        i = manifest(1)["posix"][0]
        lines = [l.replace("--- PASS: TestWriterReplay/posix/%s " % i, "--- SKIP: TestWriterReplay/posix/%s " % i) for l in log_lines(1)]
        rc, _ = self.check(1, lines)
        self.assertEqual(rc, 1)

    def test_windows_posix_pass_rejected(self):
        i = manifest(1)["posix"][0]
        lines = [l.replace("--- SKIP: TestWriterReplay/posix/%s " % i, "--- PASS: TestWriterReplay/posix/%s " % i) for l in log_lines(1, True)]
        rc, out = self.check(1, lines, windows=True)
        self.assertEqual(rc, 1)
        self.assertIn("want SKIP", out)

    def test_windows_ordinary_skip_rejected(self):
        i = manifest(1)["ordinary"][0]
        lines = [l.replace("--- PASS: TestWriterReplay/%s " % i, "--- SKIP: TestWriterReplay/%s " % i) for l in log_lines(1, True)]
        rc, _ = self.check(1, lines, windows=True)
        self.assertEqual(rc, 1)

    def test_wrong_golden_count(self):
        for n in range(4):
            rc, out = self.check(n, log_lines(n, compared=GOLDEN[n] - 1))
            self.assertEqual(rc, 1, n)
            self.assertIn("compared", out)
        rc, _ = self.check(0, log_lines(0, windows=True, compared=GOLDEN[0]), windows=True)
        self.assertEqual(rc, 1, "windows must compare the windows count, not the golden total")

    def test_parent_only_summary(self):
        rc, _ = self.check(0, ["=== RUN   TestWriterReplay", "--- PASS: TestWriterReplay (12.00s)", "PASS"])
        self.assertEqual(rc, 1)

    def test_printed_corpus_totals_are_not_evidence(self):
        lines = [l for l in log_lines(0) if "LIVE-SUMMARY" not in l]
        lines.append("    writer_test.go:1521: LIVE-SUMMARY TestWriterReplay: 1082 of 1082 writer cases run (1302 golden invocations), 120 Go bundles verified by TS")
        rc, _ = self.check(0, lines)
        self.assertEqual(rc, 1)

    def test_summary_selected_executed_mismatch(self):
        lines = [l.replace("%d selected" % (len(manifest(0)["ordinary"]) + len(manifest(0)["posix"])), "1 selected") for l in log_lines(0)]
        rc, out = self.check(0, lines)
        self.assertEqual(rc, 1)
        self.assertIn("selected", out)

    def test_duplicate_summary_rejected(self):
        lines = log_lines(0)
        lines += [l for l in lines if "LIVE-SUMMARY" in l]
        rc, _ = self.check(0, lines)
        self.assertEqual(rc, 1)

    def test_failed_parent_rejected(self):
        lines = [l.replace("--- PASS: TestWriterReplay (", "--- FAIL: TestWriterReplay (") for l in log_lines(0)]
        rc, _ = self.check(0, lines)
        self.assertEqual(rc, 1)

    def test_subtest_of_subtest_is_foreign(self):
        i = manifest(0)["ordinary"][0]
        rc, _ = self.check(0, log_lines(0) + ["        --- PASS: TestWriterReplay/%s/extra (0.00s)" % i])
        self.assertEqual(rc, 1)

    def test_unreadable_log_and_usage(self):
        rc, _ = run("0", os.path.join(MANIFESTS, "shard-0.json"), "/nonexistent/log")
        self.assertEqual(rc, 1)
        rc, _ = run("9", os.path.join(MANIFESTS, "shard-0.json"), "/dev/null")
        self.assertEqual(rc, 2)
        rc, _ = run()
        self.assertEqual(rc, 2)

    def test_wrong_manifest_for_shard(self):
        e = Env(self)
        rc, out = run("1", os.path.join(MANIFESTS, "shard-0.json"), e.write("x.log", log_lines(0)))
        self.assertEqual(rc, 1)
        self.assertIn("not shard 1", out)

    def test_tampered_manifest_rejected(self):
        e = Env(self)
        m = manifest(2)
        m["ordinary"] = m["ordinary"][1:]
        p = e.write("m.json", [json.dumps(m)])
        rc, out = run("2", p, e.write("x.log", log_lines(2)))
        self.assertEqual(rc, 1)
        m = manifest(1)
        m["posix"].append(RECORDER)
        p = e.write("m1.json", [json.dumps(m)])
        rc, _ = run("1", p, e.write("y.log", log_lines(1)))
        self.assertEqual(rc, 1)
        m = manifest(1)
        m["ordinary"].append(m["ordinary"][0])
        rc, _ = run("1", e.write("m2.json", [json.dumps(m)]), e.write("z.log", log_lines(1)))
        self.assertEqual(rc, 1)


class FilterOutput(unittest.TestCase):
    def test_filter_shape(self):
        for n in range(4):
            rc, out = run("filter", str(n), os.path.join(MANIFESTS, "shard-%d.json" % n))
            self.assertEqual(rc, 0, out)
            f = out.strip()
            self.assertTrue(f.startswith("^TestWriterReplay$/^(?:"), f)
            self.assertIn("|posix)$/^(?:", f)
            self.assertEqual(f.count(RECORDER) == 1, n == 0)

    def test_filter_branches_exact(self):
        for n in range(4):
            m = manifest(n)
            rc, out = run("filter", str(n), os.path.join(MANIFESTS, "shard-%d.json" % n))
            level2, level3 = out.strip().split("/", 1)[1].split("/")[0], out.strip().split("/")[-1]
            self.assertEqual(level2.count("|") + 1, len(m["ordinary"]) + 1)
            self.assertEqual(level3.count("|") + 1, len(m["posix"]))


    def test_filter_ids_equal_manifest_all_four_and_recorder_once(self):
        # The filter is the only place the workflow gets its -run pattern from,
        # so it must name exactly the manifest ids: no more, no fewer.
        seen_recorder = 0
        for n in range(4):
            m = manifest(n)
            rc, out = run("filter", str(n), os.path.join(MANIFESTS, "shard-%d.json" % n))
            self.assertEqual(rc, 0, out)
            head, l2, l3 = out.strip().split("/")
            self.assertEqual(head, "^TestWriterReplay$")
            unq = lambda b: [x.replace("\\", "") for x in b[len("^(?:"):-len(")$")].split("|")]
            self.assertEqual(unq(l2), m["ordinary"] + ["posix"])
            self.assertEqual(unq(l3), m["posix"])
            seen_recorder += unq(l3).count(RECORDER)
        self.assertEqual(seen_recorder, 1)

    def test_filter_rejects_wrong_manifest(self):
        rc, _ = run("filter", "1", os.path.join(MANIFESTS, "shard-0.json"))
        self.assertEqual(rc, 1)


class TotalGuard(unittest.TestCase):
    def total(self, windows=False, mutate=None, drop=None):
        e = Env(self)
        mdir, ldir = e.total_dir(windows, mutate)
        if drop is not None:
            os.remove(os.path.join(ldir, "writer-replay-%d" % drop, "writer-replay-shard-%d.log" % drop))
        return run(*(["--windows"] if windows else []), "total", mdir, ldir)

    def test_valid_unix(self):
        rc, out = self.total()
        self.assertEqual(rc, 0, out)
        self.assertIn("1082 template cases", out)
        self.assertIn("1302", out)

    def test_valid_windows(self):
        rc, out = self.total(windows=True)
        self.assertEqual(rc, 0, out)
        self.assertIn("451", out)

    def test_missing_log(self):
        rc, out = self.total(drop=2)
        self.assertEqual(rc, 1)
        self.assertIn("shard 2", out)

    def test_windows_totals_not_unix_totals(self):
        e = Env(self)
        mdir, ldir = e.total_dir(False)
        rc, _ = run("--windows", "total", mdir, ldir)
        self.assertEqual(rc, 1)
        e2 = Env(self)
        mdir, ldir = e2.total_dir(True)
        rc, _ = run("total", mdir, ldir)
        self.assertEqual(rc, 1)

    def test_one_bad_shard_fails_total(self):
        def mut(n, lines):
            return [l for l in lines if "TestWriterReplay/%s " % manifest(3)["ordinary"][0] not in l] if n == 3 else lines
        rc, _ = self.total(mutate=mut)
        self.assertEqual(rc, 1)

    def test_duplicate_log_in_two_directories(self):
        e = Env(self)
        mdir, ldir = e.total_dir()
        e.write("logs/writer-replay-copy/writer-replay-shard-1.log", log_lines(1))
        rc, out = run("total", mdir, ldir)
        self.assertEqual(rc, 1)
        self.assertIn("found 2", out)

    def test_shard_log_replaced_by_another_shards_log_is_rejected(self):
        e = Env(self)
        mdir, ldir = e.total_dir()
        e.write("logs/writer-replay-1/writer-replay-shard-1.log", log_lines(0))
        rc, _ = run("total", mdir, ldir)
        self.assertEqual(rc, 1)

    def test_cross_shard_duplicate_and_recorder_twice(self):
        # shard manifests that overlap are rejected through the cross-shard check
        e = Env(self)
        mdir = os.path.join(e.dir.name, "m")
        os.makedirs(mdir)
        for n in range(4):
            m = manifest(n)
            with open(os.path.join(mdir, "shard-%d.json" % n), "w") as f:
                json.dump(m, f)
        # copy shard 0's recorder into shard 1 with consistent log: table check still fails
        m1 = manifest(1)
        m1["posix"].append(RECORDER)
        with open(os.path.join(mdir, "shard-1.json"), "w") as f:
            json.dump(m1, f)
        _, ldir = e.total_dir()
        rc, _ = run("total", mdir, ldir)
        self.assertEqual(rc, 1)

    def test_parent_pass_alone_is_not_total_evidence(self):
        def mut(n, lines):
            return ["=== RUN   TestWriterReplay", "--- PASS: TestWriterReplay (1.00s)", "PASS"]
        rc, _ = self.total(mutate=mut)
        self.assertEqual(rc, 1)

    def test_inflated_summary_cannot_hide_missing_cases(self):
        def mut(n, lines):
            return [l for l in lines if "TestWriterReplay/posix/" not in l] if n == 2 else lines
        rc, _ = self.total(mutate=mut)
        self.assertEqual(rc, 1)


if __name__ == "__main__":
    unittest.main()
