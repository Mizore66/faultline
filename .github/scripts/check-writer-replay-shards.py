#!/usr/bin/env python3
"""Terminal guard for the sharded non-Ubuntu TestWriterReplay jobs.

Usage:
  check-writer-replay-shards.py [--windows] SHARD MANIFEST LOG
  check-writer-replay-shards.py [--windows] total MANIFEST_DIR LOG_DIR
  check-writer-replay-shards.py filter SHARD MANIFEST

SHARD is 0..3 and MANIFEST is difftest/testdata/writer-shards/shard-N.json.
Only terminal lines "--- PASS|FAIL|SKIP: TestWriterReplay/<id>" and
"TestWriterReplay/posix/<id>" are evidence. The parent result and any printed
corpus totals are not: the compared-invocation count comes from the actual
"N selected, M executed writer cases, K golden invocations compared" summary.

macOS/Linux: every manifest id (including the shard 0 recorder) must PASS.
--windows: ordinary ids must PASS and posix ids (recorder included) must SKIP.

total reads MANIFEST_DIR/shard-N.json and exactly one LOG_DIR/*/writer-replay-shard-N.log
(one downloaded artifact directory per shard) for all four shards, runs every per-shard check, then requires 1,075 unique
template cases, the recorder once, and 1,295 (or, --windows, 444) compared
golden invocations.
"""
import glob
import json
import os
import re
import sys

RECORDER = "recorder-fake-docker"
SHARDS = 4
TOTAL_CASES = 1075
TOTAL_COMPARED = {False: 1295, True: 444}
# The design's exact table (spec 3.2); the manifest must agree with it.
TABLE = [
    {"cases": 282, "golden": 332, "windowsPass": 97, "windowsSkip": 185, "windowsCompared": 111},
    {"cases": 281, "golden": 332, "windowsPass": 105, "windowsSkip": 176, "windowsCompared": 113},
    {"cases": 255, "golden": 314, "windowsPass": 99, "windowsSkip": 156, "windowsCompared": 112},
    {"cases": 257, "golden": 317, "windowsPass": 99, "windowsSkip": 158, "windowsCompared": 108},
]
ID_RE = re.compile(r"^[A-Za-z0-9._-]+$")
TERMINAL_RE = re.compile(r"^\s*--- (PASS|FAIL|SKIP): (\S+)(?: \(.*)?$")
SUMMARY_RE = re.compile(
    r"LIVE-SUMMARY TestWriterReplay: (\d+) selected, (\d+) executed writer cases, (\d+) golden invocations compared(?:,|$)"
)
PARENT_RE = re.compile(r"^\s*--- (PASS|FAIL|SKIP): TestWriterReplay(?: \(.*)?$")


class Failure(Exception):
    pass


def quote_meta(s):
    """Go regexp.QuoteMeta."""
    return "".join("\\" + c if c in "\\.+*?()|[]{}^$" else c for c in s)


def load_manifest(path, shard):
    try:
        with open(path, encoding="utf-8") as f:
            m = json.load(f)
    except (OSError, ValueError) as exc:
        raise Failure("cannot read manifest %s: %s" % (path, exc))
    if not isinstance(m, dict) or m.get("shard") != shard:
        raise Failure("manifest %s is not shard %d" % (path, shard))
    for key in ("ordinary", "posix"):
        ids = m.get(key)
        if not isinstance(ids, list) or not all(isinstance(i, str) and ID_RE.match(i) for i in ids):
            raise Failure("manifest %s: malformed %s list" % (path, key))
        if len(ids) != len(set(ids)):
            raise Failure("manifest %s: duplicate id in %s" % (path, key))
    if set(m["ordinary"]) & set(m["posix"]):
        raise Failure("manifest %s: id in both ordinary and posix" % path)
    if RECORDER in m["ordinary"] or (RECORDER in m["posix"]) != (shard == 0):
        raise Failure("manifest %s: %s must be a posix id of shard 0 only" % (path, RECORDER))
    want = TABLE[shard]
    cases = len(m["ordinary"]) + len(m["posix"]) - (1 if shard == 0 else 0)
    got = {
        "cases": cases,
        "golden": m.get("golden"),
        "windowsPass": len(m["ordinary"]),
        "windowsSkip": len(m["posix"]) - (1 if shard == 0 else 0),
        "windowsCompared": m.get("windowsCompared"),
    }
    if m.get("windowsPass") != want["windowsPass"] or m.get("windowsSkip") != want["windowsSkip"]:
        raise Failure("manifest %s: windows pass/skip fields differ from the design table" % path)
    for k, v in want.items():
        if got[k] != v:
            raise Failure("manifest %s: %s is %r, want %d" % (path, k, got[k], v))
    return m


def run_filter(m):
    level2 = list(m["ordinary"]) + (["posix"] if m["posix"] else [])
    f = "^TestWriterReplay$/^(?:" + "|".join(quote_meta(i) for i in level2) + ")$"
    if m["posix"]:
        f += "/^(?:" + "|".join(quote_meta(i) for i in m["posix"]) + ")$"
    return f


def read_log(path):
    try:
        with open(path, encoding="utf-8", errors="replace") as f:
            return f.read().replace("\r", "").split("\n")
    except OSError as exc:
        raise Failure("cannot read log %s: %s" % (path, exc))


def check_shard(m, lines, windows, label):
    """Returns (set of terminal ids, compared invocations). Raises Failure."""
    problems = []
    ordinary, posix = set(m["ordinary"]), set(m["posix"])
    seen = {}
    for line in lines:
        if PARENT_RE.match(line):
            if not line.lstrip().startswith("--- PASS"):
                problems.append("parent TestWriterReplay did not pass")
            continue
        t = TERMINAL_RE.match(line)
        if not t or not t.group(2).startswith("TestWriterReplay/"):
            continue
        status, name = t.group(1), t.group(2)
        rest = name[len("TestWriterReplay/"):]
        if rest.startswith("posix/"):
            kind, ident = "posix", rest[len("posix/"):]
        else:
            kind, ident = "ordinary", rest
        if not ident or "/" in ident:
            problems.append("foreign terminal line %s" % name)
            continue
        if (kind, ident) in seen:
            problems.append("duplicate terminal result %s" % name)
            continue
        seen[(kind, ident)] = status
        if ident not in (ordinary if kind == "ordinary" else posix):
            problems.append("foreign case %s (not in shard %d)" % (name, m["shard"]))
            continue
        want = "SKIP" if (windows and kind == "posix") else "PASS"
        if status != want:
            problems.append("%s is %s, want %s" % (name, status, want))
    for ident in sorted(ordinary):
        if ("ordinary", ident) not in seen:
            problems.append("missing terminal result TestWriterReplay/%s" % ident)
    for ident in sorted(posix):
        if ("posix", ident) not in seen:
            problems.append("missing terminal result TestWriterReplay/posix/%s" % ident)

    summaries = [SUMMARY_RE.search(l) for l in lines]
    summaries = [s for s in summaries if s]
    compared = 0
    if len(summaries) != 1:
        problems.append("want exactly one actual LIVE-SUMMARY (selected/executed/compared), found %d" % len(summaries))
    else:
        selected, executed, compared = (int(x) for x in summaries[0].groups())
        want = TABLE[m["shard"]]
        want_selected = len(ordinary) + len(posix)
        want_executed = want["windowsPass"] if windows else want_selected
        want_compared = want["windowsCompared"] if windows else want["golden"]
        if selected != want_selected:
            problems.append("summary selected %d, want %d" % (selected, want_selected))
        if executed != want_executed:
            problems.append("summary executed %d, want %d" % (executed, want_executed))
        if compared != want_compared:
            problems.append("summary compared %d golden invocations, want %d" % (compared, want_compared))
    if problems:
        shown = problems[:20]
        extra = len(problems) - len(shown)
        raise Failure("%s: %s%s" % (label, "; ".join(shown), "; and %d more" % extra if extra else ""))
    return {(k, i) for (k, i) in seen}, compared


def check_total(manifest_dir, log_dir, windows):
    errors = []
    results = []
    manifests = []
    for n in range(SHARDS):
        try:
            m = load_manifest(os.path.join(manifest_dir, "shard-%d.json" % n), n)
            manifests.append(m)
            logs = sorted(glob.glob(os.path.join(glob.escape(log_dir), "*", "writer-replay-shard-%d.log" % n)))
            if len(logs) != 1:
                raise Failure("shard %d: want exactly one writer-replay-shard-%d.log under %s, found %d" % (n, n, log_dir, len(logs)))
            results.append(check_shard(m, read_log(logs[0]), windows, "shard %d" % n))
        except Failure as exc:
            errors.append(str(exc))
    if errors:
        raise Failure("; ".join(errors))
    union = {}
    for n, (ids, _) in enumerate(results):
        for key in ids:
            if key in union:
                errors.append("%s/%s appears in shards %d and %d" % (key[0], key[1], union[key], n))
            union[key] = n
    template = {k for k in union if k != ("posix", RECORDER)}
    if len(template) != TOTAL_CASES:
        errors.append("%d unique template cases, want %d" % (len(template), TOTAL_CASES))
    recorders = sum(1 for k in union if k == ("posix", RECORDER))
    if recorders != 1:
        errors.append("%s appears %d times, want once" % (RECORDER, recorders))
    compared = sum(c for _, c in results)
    if compared != TOTAL_COMPARED[windows]:
        errors.append("%d compared golden invocations, want %d" % (compared, TOTAL_COMPARED[windows]))
    if errors:
        raise Failure("; ".join(errors))
    return len(template), compared


def main(argv):
    windows = "--windows" in argv
    args = [a for a in argv if a != "--windows"]
    try:
        if len(args) == 3 and args[0] == "filter" and not windows:
            n = int(args[1])
            print(run_filter(load_manifest(args[2], n)))
        elif len(args) == 3 and args[0] == "total":
            cases, compared = check_total(args[1], args[2], windows)
            print("writer-replay total ok: %d template cases, recorder once, %d compared golden invocations" % (cases, compared))
        elif len(args) == 3 and args[0].isdigit() and 0 <= int(args[0]) < SHARDS:
            n = int(args[0])
            m = load_manifest(args[1], n)
            ids, compared = check_shard(m, read_log(args[2]), windows, "shard %d" % n)
            print("writer-replay shard %d ok: %d terminal results, %d compared golden invocations" % (n, len(ids), compared))
        else:
            sys.stderr.write(__doc__)
            return 2
    except ValueError:
        sys.stderr.write(__doc__)
        return 2
    except Failure as exc:
        print("::error title=writer replay shards::%s" % exc)
        sys.stderr.write("check-writer-replay-shards: %s\n" % exc)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
