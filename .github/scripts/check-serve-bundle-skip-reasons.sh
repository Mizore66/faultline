#!/bin/sh
# check-serve-bundle-skip-reasons.sh LOG RUNNER_OS: the Slice 5c serve-bundle
# skip identities AND reasons. check-skips.sh validates only skipped test names;
# this checks, for the few new tests that may skip on a Windows host, that each
# reported skip is an eligible name carrying exactly its documented reason, and
# that nothing of these exact families skips anywhere else. It does not widen
# check-skips.sh and does not look at any other test.
#
# Eligible (Windows only), with the reason each must report:
#   TestInventoryRejectsLinksInRetainedTree
#     symlink creation needs privileges on Windows; Windows retained trees contain no links by construction
#   TestPermissionBitsAreNotInventoried
#     POSIX mode bits are meaningless on Windows
#   TestAttachmentHostileRootSymlink/(minimization|repair|prevention)
#   TestAttachmentHostileChildSymlink/(repair|prevention)
#   TestServeBundleBindingLoaderRejectsLinks/(minimization/foreign-frozen.json|cases.jsonl|golden.jsonl|inventory.json|baseline.json|.gitattributes)
#     symlink creation needs a privilege this Windows host lacks (ERROR_PRIVILEGE_NOT_HELD): <the real error>
# A skip of any other test of those families (a parent included), or an eligible
# skip on a non-Windows runner, fails. The reason line is read from the t.Log
# line go test -v prints for the skipped test. Three layouts are read:
#   - streamed (real go test -v): the reason follows its "=== RUN|NAME|CONT name"
#     header and the named test's "--- SKIP: name" summary comes later (a nested
#     subtest's summary is printed under its parent, after other tests'). The
#     reason is therefore bound to the current === header name and resolved by
#     name, never by adjacency;
#   - the reason line directly before an unheaded "--- SKIP:" line;
#   - the reason line directly after a "--- SKIP:" line.
# A fresh "=== RUN name" discards any reason held for that name, and a package
# boundary (PASS/FAIL/ok/? line) clears everything, so no reason is carried to
# an unrelated test or run, and no test borrows the reason of another test.
log=$1
runner=$2
[ -n "$log" ] && [ -n "$runner" ] || { echo "usage: $0 LOG RUNNER_OS" >&2; exit 2; }
tr -d '\r' < "$log" | awk -v runner="$runner" '
function fail(msg) { print "::error title=serve-bundle skip::" msg; bad = 1 }
function reason_of(name) {
  if (name == "TestInventoryRejectsLinksInRetainedTree") return "E:symlink creation needs privileges on Windows; Windows retained trees contain no links by construction"
  if (name == "TestPermissionBitsAreNotInventoried") return "E:POSIX mode bits are meaningless on Windows"
  if (name ~ /^TestAttachmentHostileRootSymlink\/(minimization|repair|prevention)$/) return "P:symlink creation needs a privilege this Windows host lacks (ERROR_PRIVILEGE_NOT_HELD): "
  if (name ~ /^TestAttachmentHostileChildSymlink\/(repair|prevention)$/) return "P:symlink creation needs a privilege this Windows host lacks (ERROR_PRIVILEGE_NOT_HELD): "
  if (name ~ /^TestServeBundleBindingLoaderRejectsLinks\/(minimization\/foreign-frozen\.json|cases\.jsonl|golden\.jsonl|inventory\.json|baseline\.json|\.gitattributes)$/) return "P:symlink creation needs a privilege this Windows host lacks (ERROR_PRIVILEGE_NOT_HELD): "
  return ""
}
function inscope(name) {
  return name ~ /^(TestAttachmentHostile|TestInventoryRejectsLinksInRetainedTree|TestPermissionBitsAreNotInventoried|TestServeBundleBindingLoaderRejectsLinks)/
}
function check(name, text,   want, kind, expected) {
  want = reason_of(name)
  if (want == "") { fail("not an eligible skip: " name); return }
  if (runner != "Windows") { fail("eligible name skipped on a non-Windows runner (" runner "): " name); return }
  kind = substr(want, 1, 1); expected = substr(want, 3)
  if (kind == "E" && text != expected) { fail(name " reported the reason [" text "], want exactly [" expected "]"); return }
  if (kind == "P" && (index(text, expected) != 1 || length(text) == length(expected))) { fail(name " reported the reason [" text "], want the prefix [" expected "] followed by the real error"); return }
  seen++
}
function logtext(line) {
  if (match(line, /^ +[A-Za-z0-9_.\/-]+\.go:[0-9]+: /)) return substr(line, RLENGTH + 1)
  return ""
}
function reset_package() {
  # A package/test-binary boundary: nothing carries across it.
  if (pending != "") fail(pending " has no reason line")
  pending = ""; prev = ""; cur = ""
  split("", reason)
}
{
  if (pending != "") {
    t = logtext($0)
    p = pending
    pending = ""
    prev = ""
    if (t != "") { check(p, t); next }  # the line after the SKIP header is its own reason only
    fail(p " has no reason line")
  }
  if ($0 ~ /^(PASS|FAIL)$/ || $0 ~ /^(ok|FAIL|\?)[ \t]/) { reset_package(); next }
  # A "=== RUN|NAME|CONT name" header makes name the test the following log
  # lines belong to. A fresh RUN of a name discards any reason left for it.
  if ($0 ~ /^=== (RUN|NAME|CONT) +[^ ]/) {
    cur = $3
    if ($2 == "RUN") delete reason[cur]
    prev = ""
    next
  }
  if ($0 ~ /^ *--- / && $0 !~ /--- SKIP: /) { prev = ""; next }
  t = logtext($0)
  if (t != "" && $0 !~ /--- SKIP: /) {
    # Streamed output: the reason belongs to the current test, whatever other
    # test summaries are printed later.
    if (cur != "") reason[cur] = t
    prev = t
  }
  if (match($0, /--- SKIP: [^ ]+/)) {
    name = substr($0, RSTART + 10, RLENGTH - 10)
    if (inscope(name)) {
      if (name in reason) { check(name, reason[name]); delete reason[name] }
      else if (prev != "" && (cur == "" || cur == name)) check(name, prev)
      else pending = name
    }
    prev = ""
  }
}
END {
  if (pending != "") fail(pending " has no reason line")
  if (bad) exit 1
  print "serve-bundle skips: " seen + 0 " eligible, all with their exact reasons"
}'
