import json
import os
import subprocess
import sys
import unittest

GATE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "ci_gate.py")
PROV = "signed GitHub CI provenance"
MAIN = "refs/heads/main"
BRANCH = "refs/pull/1/merge"


def gate(scope, ref, needs, raw=None, event="pull_request"):
    nj = raw if raw is not None else json.dumps(needs)
    args = [sys.executable, "-I", GATE, "--scope", scope, "--event", event,
            "--ref", ref, "--needs-json", nj]
    return subprocess.run(args, text=True, capture_output=True)


def jobs(**results):
    return {k.replace("_", " "): {"result": v} for k, v in results.items()}


class GateTest(unittest.TestCase):
    def ok(self, r, text):
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn(text, r.stdout)

    def bad(self, r):
        self.assertNotEqual(r.returncode, 0, r.stdout)

    def test_code_all_success(self):
        self.ok(gate("code", MAIN, jobs(a="success", b="success")), "CI-SCOPE code")

    def test_code_failure(self):
        self.bad(gate("code", MAIN, jobs(a="success", b="failure")))

    def test_code_cancelled(self):
        self.bad(gate("code", BRANCH, jobs(a="cancelled")))

    def test_code_unexpected_skip(self):
        self.bad(gate("code", BRANCH, jobs(a="skipped")))

    def test_provenance_skip_non_main(self):
        needs = {"a": {"result": "success"}, PROV: {"result": "skipped"}}
        self.ok(gate("code", BRANCH, needs), "CI-SCOPE code")

    def test_provenance_skip_main_rejected(self):
        self.bad(gate("code", MAIN, {"a": {"result": "success"}, PROV: {"result": "skipped"}}, event="push"))

    def test_provenance_skip_dispatch_main_allowed(self):
        needs = {"a": {"result": "success"}, PROV: {"result": "skipped"}}
        self.ok(gate("code", MAIN, needs, event="workflow_dispatch"), "CI-SCOPE code")

    def test_provenance_skip_push_main_rejected(self):
        needs = {"a": {"result": "success"}, PROV: {"result": "skipped"}}
        self.bad(gate("code", MAIN, needs, event="push"))

    def test_provenance_skip_pull_request_allowed(self):
        needs = {"a": {"result": "success"}, PROV: {"result": "skipped"}}
        self.ok(gate("code", BRANCH, needs, event="pull_request"), "CI-SCOPE code")

    def test_other_skip_dispatch_main_rejected(self):
        needs = {"a": {"result": "skipped"}, PROV: {"result": "skipped"}}
        self.bad(gate("code", MAIN, needs, event="workflow_dispatch"))

    def test_provenance_failure_non_main_rejected(self):
        self.bad(gate("code", BRANCH, {PROV: {"result": "failure"}}))

    def test_docs_only_ignores_heavy(self):
        needs = {"scope": {"result": "success"}, "a": {"result": "skipped"}, "b": {"result": "skipped"}}
        self.ok(gate("docs-only", BRANCH, needs), "CI-SCOPE docs-only")

    def test_docs_only_scope_failure_rejected(self):
        for res in ("failure", "cancelled", "skipped", "Success"):
            with self.subTest(res=res):
                needs = {"scope": {"result": res}, "a": {"result": "skipped"}, "b": {"result": "skipped"}}
                self.bad(gate("docs-only", BRANCH, needs))

    def test_docs_only_missing_scope_rejected(self):
        self.bad(gate("docs-only", BRANCH, jobs(a="skipped", b="skipped")))
        self.bad(gate("docs-only", BRANCH, {}))

    def test_unknown_scope(self):
        self.bad(gate("weird", BRANCH, jobs(a="success")))
        self.bad(gate("", BRANCH, jobs(a="success")))

    def test_malformed_json(self):
        for raw in ("", "{", "null", "[]", '"x"', "1", '{"a": 1}', '{"a": {}}',
                    '{"a": {"result": null}}', '{"a": {"result": ["success"]}}'):
            with self.subTest(raw=raw):
                self.bad(gate("code", MAIN, None, raw=raw))

    def test_empty_needs_rejected_for_code(self):
        self.bad(gate("code", MAIN, {}))

    def test_malformed_json_docs_only_rejected(self):
        self.bad(gate("docs-only", BRANCH, None, raw="{"))

    def test_missing_args(self):
        r = subprocess.run([sys.executable, "-I", GATE, "--scope", "code"], text=True, capture_output=True)
        self.bad(r)

    def test_case_sensitive_result(self):
        self.bad(gate("code", MAIN, jobs(a="Success")))


if __name__ == "__main__":
    unittest.main()
