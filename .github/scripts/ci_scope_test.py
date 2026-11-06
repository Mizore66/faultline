import os
import subprocess
import tempfile
import unittest

SCRIPT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "ci_scope.sh")
ZERO = "0" * 40


class ScopeTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.repo = self.tmp.name
        self.git("init", "-q")
        self.write("README.md", "base\n")
        self.commit("base")
        self.base = self.rev()

    def tearDown(self):
        self.tmp.cleanup()

    def git(self, *args):
        return subprocess.run(
            ["git", "-c", "user.name=CI Test", "-c", "user.email=ci@example.invalid", *args],
            cwd=self.repo, text=True, capture_output=True, check=True,
        ).stdout.strip()

    def write(self, rel, content):
        path = os.path.join(self.repo, rel)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w") as f:
            f.write(content)

    def commit(self, msg):
        self.git("add", "-A")
        self.git("commit", "-q", "--allow-empty", "-m", msg)

    def rev(self):
        return self.git("rev-parse", "HEAD")

    def run_scope(self, event, base, head, workdir=None, cwd=None):
        args = [SCRIPT, event, base, head] + ([workdir] if workdir else [])
        return subprocess.run(args, cwd=cwd or self.repo, text=True, capture_output=True)

    def change(self, files):
        for rel, content in files.items():
            self.write(rel, content)
        self.commit("change")
        return self.rev()

    def assertScope(self, result, scope):
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.splitlines()[0], "scope=" + scope)

    def test_docs_only(self):
        head = self.change({"docs/superpowers/specs/x.md": "x\n"})
        r = self.run_scope("pull_request", self.base, head)
        self.assertScope(r, "docs-only")
        self.assertIn("path=docs/superpowers/specs/x.md\n", r.stdout)

    def test_code(self):
        head = self.change({"internal/x.go": "package x\n"})
        r = self.run_scope("pull_request", self.base, head)
        self.assertScope(r, "code")
        self.assertIn("path=internal/x.go\n", r.stdout)

    def test_mixed(self):
        head = self.change({"docs/a.md": "a\n", "internal/x.go": "package x\n"})
        r = self.run_scope("pull_request", self.base, head)
        self.assertScope(r, "code")
        self.assertEqual(r.stdout.splitlines()[1:], ["path=docs/a.md", "path=internal/x.go"])

    def test_known_differences_is_code(self):
        head = self.change({"difftest/KNOWN_DIFFERENCES.md": "k\n"})
        self.assertScope(self.run_scope("pull_request", self.base, head), "code")

    def test_superpowers_docs_only(self):
        head = self.change({"docs/superpowers/plans/x.md": "x\n", "docs/superpowers/specs/y.md": "y\n"})
        r = self.run_scope("pull_request", self.base, head)
        self.assertScope(r, "docs-only")
        self.assertEqual(r.stdout.splitlines()[1:],
                         ["path=docs/superpowers/plans/x.md", "path=docs/superpowers/specs/y.md"])

    def test_other_docs_and_root_files_are_code(self):
        for name in ("docs/samples/a.md", "docs/a.md", "README.md", "LICENSE", "NOTICE", "CHANGELOG.md",
                     "docs/superpowers"):
            with self.subTest(name=name):
                base = self.rev()
                head = self.change({name: "z\n"})
                self.assertScope(self.run_scope("pull_request", base, head), "code")

    def test_root_lookalikes_are_code(self):
        for name in ("README.md.sh", "LICENSE2", "docs.go", "Docs/a.md", "NOTICE.txt", "sub/README.md", "docs/superpowers2/a.md", "xdocs/superpowers/a.md"):
            with self.subTest(name=name):
                base = self.rev()
                head = self.change({name: "z\n"})
                self.assertScope(self.run_scope("pull_request", base, head), "code")

    def test_empty_diff_is_code(self):
        self.commit("empty")
        self.assertScope(self.run_scope("pull_request", self.base, self.rev()), "code")

    def test_zero_before_is_code(self):
        head = self.change({"docs/superpowers/a.md": "a\n"})
        self.assertScope(self.run_scope("push", ZERO, head), "code")
        self.assertScope(self.run_scope("push", "", head), "code")

    def test_invalid_revision_is_code(self):
        r = self.run_scope("pull_request", "deadbeef" * 5, "cafebabe" * 5)
        self.assertScope(r, "code")

    def test_not_a_repo_is_code(self):
        with tempfile.TemporaryDirectory() as d:
            r = self.run_scope("pull_request", "a", "b", cwd=d)
            self.assertScope(r, "code")

    def test_diff_check_failure_is_code(self):
        head = self.change({"docs/superpowers/a.md": "trailing   \n"})
        self.assertScope(self.run_scope("pull_request", self.base, head), "code")

    def test_other_events_are_code(self):
        head = self.change({"docs/superpowers/a.md": "a\n"})
        for ev in ("workflow_dispatch", "schedule", "merge_group", "bogus"):
            with self.subTest(ev=ev):
                self.assertScope(self.run_scope(ev, self.base, head), "code")

    def test_push_two_dot(self):
        head = self.change({"docs/superpowers/a.md": "a\n"})
        self.assertScope(self.run_scope("push", self.base, head), "docs-only")

    def test_pr_three_dot_ignores_base_advance(self):
        self.git("checkout", "-q", "-b", "feature")
        fhead = self.change({"docs/superpowers/a.md": "a\n"})
        self.git("checkout", "-q", "-")
        mbase = self.change({"internal/x.go": "package x\n"})
        r = self.run_scope("pull_request", mbase, fhead)
        self.assertScope(r, "docs-only")
        self.assertEqual(r.stdout.splitlines()[1:], ["path=docs/superpowers/a.md"])

    def test_workdir_argument(self):
        head = self.change({"docs/superpowers/a.md": "a\n"})
        with tempfile.TemporaryDirectory() as d:
            r = self.run_scope("pull_request", self.base, head, workdir=self.repo, cwd=d)
            self.assertScope(r, "docs-only")

    def test_bad_workdir_is_code(self):
        r = self.run_scope("pull_request", "a", "b", workdir="/nonexistent/zzz")
        self.assertScope(r, "code")

    def test_option_like_revision_is_code(self):
        r = self.run_scope("pull_request", "--output=/tmp/ci_scope_pwn", "HEAD")
        self.assertScope(r, "code")
        self.assertFalse(os.path.exists("/tmp/ci_scope_pwn"))

    def test_path_with_spaces_and_newline_is_code(self):
        head = self.change({"docs/superpowers/a b.md": "a\n"})
        r = self.run_scope("pull_request", self.base, head)
        self.assertScope(r, "docs-only")
        self.assertIn("path=docs/superpowers/a b.md\n", r.stdout)

    def test_docs_deletion_and_rename(self):
        self.change({"docs/superpowers/a.md": "a\n"})
        b = self.rev()
        os.rename(os.path.join(self.repo, "docs/superpowers/a.md"), os.path.join(self.repo, "internal.go"))
        self.commit("rename out")
        r = self.run_scope("pull_request", b, self.rev())
        self.assertScope(r, "code")
        self.assertIn("path=docs/superpowers/a.md\n", r.stdout)
        self.assertIn("path=internal.go\n", r.stdout)


if __name__ == "__main__":
    unittest.main()
