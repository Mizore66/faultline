package turnsnap

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/allowlist"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@example.test"}, {"config", "user.name", "t"}} {
		if out, err := gitCmd(dir, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v", out, err)
		}
	}
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644)
	gitCmd(dir, "add", ".").Run()
	gitCmd(dir, "commit", "-qm", "init").Run()
	return dir
}

// gitCmd is the test's own git, without the developer's global or system
// configuration (signing, hooks, templates).
func gitCmd(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	return cmd
}

func noSleep(time.Duration) {}

func TestCleanCaptureUsesFastPathAndWritesNoQuarantine(t *testing.T) {
	t.Parallel()
	dir := gitRepo(t)
	snap, _, err := Capture(dir, CaptureOptions{Sleep: func(time.Duration) {}})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Get("treeDigest").Str() == "" {
		t.Fatalf("%v", snap)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "faultline", "objects")); !os.IsNotExist(err) {
		t.Fatal("fast path created the quarantine store")
	}
}

func TestDirtyCaptureWritesAlternatesOnce(t *testing.T) {
	t.Parallel()
	dir := gitRepo(t)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b\n"), 0o644)
	for i := 0; i < 2; i++ {
		if _, _, err := Capture(dir, CaptureOptions{Sleep: func(time.Duration) {}}); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".git", "objects", "info", "alternates"))
	if string(b) != "../faultline/objects\n" {
		t.Fatalf("%q", b)
	}
}

// repositoryFixture is tests/turn-snapshot.test.ts:44: tracked.txt holds
// "stable\n" at HEAD.
func repositoryFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "faultline@example.invalid")
	git(t, dir, "config", "user.name", "FaultLine turn snapshot test")
	write(t, dir, "tracked.txt", "stable\n")
	git(t, dir, "add", "tracked.txt")
	git(t, dir, "commit", "-qm", "clean fixture")
	return dir
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitCmd(dir, args...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func lsTree(t *testing.T, dir, tree string) []string {
	return strings.Split(git(t, dir, "ls-tree", "-r", "--name-only", tree), "\n")
}

func snap(t *testing.T, dir string, o CaptureOptions) jsjson.Value {
	t.Helper()
	if o.Sleep == nil {
		o.Sleep = noSleep
	}
	v, _, err := Capture(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func tree(v jsjson.Value) string { return v.Get("treeDigest").Str() }

func mustFail(t *testing.T, dir string, o CaptureOptions, pattern string) error {
	t.Helper()
	if o.Sleep == nil {
		o.Sleep = noSleep
	}
	_, _, err := Capture(dir, o)
	if err == nil {
		t.Fatalf("capture succeeded; want %s", pattern)
	}
	if !regexp.MustCompile(pattern).MatchString(err.Error()) {
		t.Fatalf("error %q does not match %s", err, pattern)
	}
	return err
}

// countingRunner wraps defaultRunner and counts each subcommand.
func countingRunner(counts map[string]int) gitRunner {
	return func(root string, args []string, o runOpts) (string, error) {
		counts[args[0]]++
		return defaultRunner(root, args, o)
	}
}

func verifies(t *testing.T, v jsjson.Value) {
	t.Helper()
	unsigned := jsjson.NewObj()
	for _, k := range v.Obj().Keys() {
		if k != "digest" {
			unsigned.Set(k, v.Obj().Field(k))
		}
	}
	if d, _ := canonical.DigestJSON(jsjson.MakeObject(unsigned)); d != v.Get("digest").Str() {
		t.Fatalf("digest does not verify: %s", jsjson.Stringify(v))
	}
}

func TestCaptureCleanSignsHeadTree(t *testing.T) { // :65
	t.Parallel()
	dir := repositoryFixture(t)
	at := time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)
	v := snap(t, dir, CaptureOptions{now: func() time.Time { return at }})
	if v.Get("schemaVersion").Str() != SnapshotVersion || v.Get("dirty").Bool() || v.Get("capturedAt").Str() != "2026-07-17T00:00:00.000Z" {
		t.Fatal(jsjson.Stringify(v))
	}
	if tree(v) != git(t, dir, "rev-parse", "HEAD^{tree}") {
		t.Fatal("tree is not HEAD^{tree}")
	}
	verifies(t, v)
	if got := v.Obj().Keys(); !slices.Equal(got, []string{"schemaVersion", "repositoryRoot", "headCommit", "treeDigest", "capturedAt", "dirty", "statusDigest", "digest"}) {
		t.Fatal(got)
	}
}

func TestCaptureDirtyTree(t *testing.T) { // :82
	t.Parallel()
	dir := repositoryFixture(t)
	write(t, dir, "tracked.txt", "modified by the turn\n")
	write(t, dir, "untracked.txt", "new from the turn\n")
	v, warnings, err := Capture(dir, CaptureOptions{Sleep: noSleep})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Get("dirty").Bool() || tree(v) == git(t, dir, "rev-parse", "HEAD^{tree}") {
		t.Fatal(jsjson.Stringify(v))
	}
	if !slices.ContainsFunc(warnings, func(w string) bool { return strings.Contains(w, "untracked.txt") }) {
		t.Fatal(warnings)
	}
	if got := lsTree(t, dir, tree(v)); !slices.Equal(got, []string{"tracked.txt", "untracked.txt"}) {
		t.Fatal(got)
	}
	if got := git(t, dir, "show", tree(v)+":tracked.txt"); got != "modified by the turn" {
		t.Fatal(got)
	}
}

func TestCaptureExcludesDefaults(t *testing.T) { // :103
	t.Parallel()
	dir := repositoryFixture(t)
	write(t, dir, ".faultline/recording.json", "{}")
	write(t, dir, "node_modules/some-pkg/index.js", "module.exports = {};\n")
	write(t, dir, "dist/bundle.js", "console.log(1);\n")
	write(t, dir, ".env", "SECRET=do-not-capture\n")
	write(t, dir, "included.txt", "keep me\n")
	if got := lsTree(t, dir, tree(snap(t, dir, CaptureOptions{}))); !slices.Equal(got, []string{"included.txt", "tracked.txt"}) {
		t.Fatal(got)
	}
}

func TestCaptureHonorsFaultlineignore(t *testing.T) { // :129
	t.Parallel()
	dir := repositoryFixture(t)
	write(t, dir, ".faultlineignore", "scratch/\nsecret-notes.txt\n")
	write(t, dir, "scratch/tmp.txt", "ignore me\n")
	write(t, dir, "secret-notes.txt", "ignore me too\n")
	write(t, dir, "kept.txt", "visible\n")
	if got := lsTree(t, dir, tree(snap(t, dir, CaptureOptions{}))); !slices.Equal(got, []string{".faultlineignore", "kept.txt", "tracked.txt"}) {
		t.Fatal(got)
	}
}

func TestCaptureRefusesOversized(t *testing.T) { // :149
	t.Parallel()
	dir := repositoryFixture(t)
	write(t, dir, "huge.bin", string(make([]byte, 2048)))
	// A dirty path is fingerprinted, and refused, before the planner runs.
	mustFail(t, dir, CaptureOptions{caps: caps{fileBytes: 1024}}, `^Refusing turn-tree fingerprint: .*huge\.bin is 2048 bytes \(max 1024 per file\)\.$`)
	// A clean tracked file reaches the planner's cap (the untracked
	// .faultlineignore forces the slow path).
	git(t, dir, "add", "huge.bin")
	git(t, dir, "commit", "-qm", "huge")
	write(t, dir, ".faultlineignore", "# none\n")
	mustFail(t, dir, CaptureOptions{caps: caps{fileBytes: 1024}}, `^Refusing turn-tree snapshot: huge\.bin is 2048 bytes \(max 1024 per file\)\.$`)
	if _, err := os.Stat(ObjectDirectory(dir)); !os.IsNotExist(err) {
		t.Fatal("refusal wrote the quarantine store")
	}
}

func TestCaptureRefusesCountAndTotal(t *testing.T) {
	t.Parallel()
	dir := repositoryFixture(t)
	write(t, dir, "one.txt", "12345\n")
	write(t, dir, "two.txt", "12345\n")
	mustFail(t, dir, CaptureOptions{caps: caps{fileCount: 2}}, `^Refusing turn-tree snapshot: 3 eligible files exceed the maximum of 2\.$`)
	mustFail(t, dir, CaptureOptions{caps: caps{totalBytes: 10}}, `^Refusing turn-tree snapshot: eligible content exceeds the maximum total of 10 bytes\.$`)
}

func TestCaptureRefusesSymlink(t *testing.T) {
	t.Parallel()
	dir := repositoryFixture(t)
	if err := os.Symlink("tracked.txt", filepath.Join(dir, "link")); err != nil {
		t.Skip(err)
	}
	mustFail(t, dir, CaptureOptions{}, `^Refusing turn-tree snapshot: symbolic link is not snapshot-eligible: link$`)
}

func TestCaptureRefusesSecret(t *testing.T) { // :159, :325
	t.Parallel()
	dir := repositoryFixture(t)
	write(t, dir, "leaked.txt", "token = "+fixtureSecret()+"\n")
	mustFail(t, dir, CaptureOptions{}, `secret material detected in leaked\.txt \(rules/kinds: OPENAI_API_KEY\)`)
}

func TestCaptureTrackedOnly(t *testing.T) { // :169
	t.Parallel()
	dir := repositoryFixture(t)
	write(t, dir, "tracked.txt", "modified\n")
	write(t, dir, "untracked-only.txt", "should not appear\n")
	v, warnings, err := Capture(dir, CaptureOptions{TrackedFilesOnly: true, Sleep: noSleep})
	if err != nil {
		t.Fatal(err)
	}
	if got := lsTree(t, dir, tree(v)); !slices.Equal(got, []string{"tracked.txt"}) {
		t.Fatal(got)
	}
	if slices.ContainsFunc(warnings, func(w string) bool { return strings.Contains(w, "untracked-only.txt") }) {
		t.Fatal(warnings)
	}
}

func TestCaptureLeavesIndexAndHead(t *testing.T) { // :183
	t.Parallel()
	dir := repositoryFixture(t)
	write(t, dir, "tracked.txt", "modified but never staged\n")
	write(t, dir, "untracked.txt", "also never staged\n")
	index, _ := os.ReadFile(filepath.Join(dir, ".git", "index"))
	head, status := git(t, dir, "rev-parse", "HEAD"), git(t, dir, "status", "--porcelain=v1", "--untracked-files=all")
	snap(t, dir, CaptureOptions{})
	after, _ := os.ReadFile(filepath.Join(dir, ".git", "index"))
	if !bytes.Equal(index, after) || git(t, dir, "rev-parse", "HEAD") != head || git(t, dir, "status", "--porcelain=v1", "--untracked-files=all") != status {
		t.Fatal("capture changed the index, HEAD or status")
	}
}

const quiescenceRefusal = `^FaultLine could not prove turn-tree quiescence after %d dual-tree attempt\(s\) and refused a torn turn tree snapshot\.$`

func TestCaptureTornStatus(t *testing.T) { // :204
	t.Parallel()
	dir := repositoryFixture(t)
	calls := 0
	runGit := func(root string, args []string, o runOpts) (string, error) {
		if args[0] == "status" {
			calls++
			if calls%2 == 1 {
				return "", nil
			}
			return "?? changed-mid-write.txt\x00", nil
		}
		return defaultRunner(root, args, o)
	}
	err := mustFail(t, dir, CaptureOptions{runGit: runGit, maxAttempts: 2}, strings.Replace(quiescenceRefusal, "%d", "2", 1))
	if _, ok := err.(*Error); !ok || calls != 4 {
		t.Fatalf("%T, %d status calls", err, calls)
	}
}

func TestCaptureTornTrees(t *testing.T) { // :236
	t.Parallel()
	dir := repositoryFixture(t)
	write(t, dir, "tracked.txt", "dirty-for-torn-tree-test\n")
	calls := 0
	runGit := func(root string, args []string, o runOpts) (string, error) {
		if args[0] == "write-tree" {
			calls++
			if calls%2 == 1 {
				return strings.Repeat("a", 40), nil
			}
			return strings.Repeat("b", 40), nil
		}
		return defaultRunner(root, args, o)
	}
	mustFail(t, dir, CaptureOptions{runGit: runGit, maxAttempts: 3}, strings.Replace(quiescenceRefusal, "%d", "3", 1))
	if calls != 6 {
		t.Fatal(calls)
	}
}

func TestCaptureMutationDuringDelay(t *testing.T) { // :259
	t.Parallel()
	dir := repositoryFixture(t)
	n := 0
	mustFail(t, dir, CaptureOptions{maxAttempts: 2, Sleep: func(time.Duration) {
		n++
		write(t, dir, "tracked.txt", "mutated-"+strconv.Itoa(n)+"\n")
	}}, strings.Replace(quiescenceRefusal, "%d", "2", 1))
}

func TestCaptureSleepsOnceWithTheDelay(t *testing.T) { // :271
	t.Parallel()
	dir := repositoryFixture(t)
	var sleeps []time.Duration
	v := snap(t, dir, CaptureOptions{Sleep: func(d time.Duration) { sleeps = append(sleeps, d) }})
	if !slices.Equal(sleeps, []time.Duration{50 * time.Millisecond}) || v.Get("dirty").Bool() || tree(v) != git(t, dir, "rev-parse", "HEAD^{tree}") {
		t.Fatal(sleeps, jsjson.Stringify(v))
	}
}

func TestCaptureAllowlistedSecret(t *testing.T) { // :335
	t.Parallel()
	dir := repositoryFixture(t)
	token := fixtureSecret()
	write(t, dir, "fixture-key.txt", "token = "+token+"\n")
	fileDigest, _ := allowlist.FileContentDigest(filepath.Join(dir, "fixture-key.txt"), SecretScanBytes)
	entry := func(occurrence, file string) string {
		e := `{"path":"fixture-key.txt","kind":"OPENAI_API_KEY","occurrenceDigest":"` + occurrence + `"`
		if file != "" {
			e += `,"fileDigest":"` + file + `"`
		}
		return `{"schemaVersion":"` + allowlist.SchemaVersion + `","entries":[` + e + `}]}` + "\n"
	}
	write(t, dir, allowlist.Filename, entry("sha256:"+canonical.SHA256Hex(token), fileDigest))
	v := snap(t, dir, CaptureOptions{})
	if !v.Get("dirty").Bool() || !slices.Contains(lsTree(t, dir, tree(v)), "fixture-key.txt") {
		t.Fatal(jsjson.Stringify(v))
	}
	write(t, dir, allowlist.Filename, entry("sha256:"+strings.Repeat("0", 64), ""))
	mustFail(t, dir, CaptureOptions{}, `OPENAI_API_KEY`)
}

func cachePath(t *testing.T) string { return filepath.Join(t.TempDir(), "x.turn-snapshot-cache.json") }

func TestCaptureReusesMatchingCache(t *testing.T) { // :370
	t.Parallel()
	dir, cache := repositoryFixture(t), cachePath(t)
	write(t, dir, "tracked.txt", "dirty once\n")
	first := snap(t, dir, CaptureOptions{SessionCachePath: cache})
	raw, err := os.ReadFile(cache)
	if err != nil || !strings.HasPrefix(string(raw), "{\n  \"schemaVersion\": \"faultline.turn-snapshot-cache.v2\",\n  \"headCommit\"") || !strings.HasSuffix(string(raw), "}\n") {
		t.Fatalf("%s %v", raw, err)
	}
	counts := map[string]int{}
	second := snap(t, dir, CaptureOptions{SessionCachePath: cache, runGit: countingRunner(counts)})
	if tree(second) != tree(first) || counts["write-tree"] != 0 || counts["cat-file"] != 2 {
		t.Fatal(counts)
	}
}

func TestCaptureCacheContentChanges(t *testing.T) { // :395, :420
	t.Parallel()
	dir, cache := repositoryFixture(t), cachePath(t)
	write(t, dir, "tracked.txt", "version A\n")
	first := snap(t, dir, CaptureOptions{SessionCachePath: cache})
	write(t, dir, "tracked.txt", "version B — still modified, different bytes\n")
	counts := map[string]int{}
	second := snap(t, dir, CaptureOptions{SessionCachePath: cache, runGit: countingRunner(counts)})
	if tree(second) == tree(first) || counts["write-tree"] == 0 || !strings.Contains(git(t, dir, "show", tree(second)+":tracked.txt"), "version B") {
		t.Fatal(counts)
	}
	write(t, dir, "generated-config.json", "{\"v\":1}\n")
	third := snap(t, dir, CaptureOptions{SessionCachePath: cache})
	write(t, dir, "generated-config.json", "{\"v\":2,\"changed\":true}\n")
	fourth := snap(t, dir, CaptureOptions{SessionCachePath: cache})
	if tree(third) == tree(fourth) || !strings.Contains(git(t, dir, "show", tree(fourth)+":generated-config.json"), `"v":2`) {
		t.Fatal("untracked content change reused the cache")
	}
}

func TestCaptureCachePolicyChanges(t *testing.T) { // :436
	t.Parallel()
	dir, cache := repositoryFixture(t), cachePath(t)
	write(t, dir, "tracked.txt", "dirty\n")
	write(t, dir, "scratch.txt", "keep\n")
	write(t, dir, ".faultlineignore", ".faultline-secret-allowlist.json\n")
	write(t, dir, allowlist.Filename, `{"schemaVersion":"`+allowlist.SchemaVersion+`","entries":[]}`+"\n")
	first := snap(t, dir, CaptureOptions{SessionCachePath: cache})
	policy := func() string {
		raw, _ := os.ReadFile(cache)
		v, _ := jsjson.Parse(string(raw))
		return v.Get("policyDigest").Str()
	}
	policyFirst := policy()
	write(t, dir, ".faultlineignore", ".faultline-secret-allowlist.json\nscratch.txt\n")
	counts := map[string]int{}
	afterIgnore := snap(t, dir, CaptureOptions{SessionCachePath: cache, runGit: countingRunner(counts)})
	if tree(afterIgnore) == tree(first) || counts["write-tree"] == 0 || slices.Contains(lsTree(t, dir, tree(afterIgnore)), "scratch.txt") {
		t.Fatal(counts)
	}
	write(t, dir, allowlist.Filename, `{"schemaVersion":"`+allowlist.SchemaVersion+`","entries":[{"path":"never-matches.txt","kind":"OPENAI_API_KEY","occurrenceDigest":"sha256:`+strings.Repeat("a", 64)+`"}]}`+"\n")
	counts = map[string]int{}
	afterAllowlist := snap(t, dir, CaptureOptions{SessionCachePath: cache, runGit: countingRunner(counts)})
	if policy() == policyFirst || counts["write-tree"] == 0 || tree(afterAllowlist) != tree(afterIgnore) {
		t.Fatal(counts)
	}
}

func TestCaptureCacheModeChange(t *testing.T) { // :484
	t.Parallel()
	dir, cache := repositoryFixture(t), cachePath(t)
	write(t, dir, "tracked.txt", "dirty\n")
	write(t, dir, "untracked-only.txt", "noise\n")
	all := snap(t, dir, CaptureOptions{SessionCachePath: cache})
	trackedOnly := snap(t, dir, CaptureOptions{SessionCachePath: cache, TrackedFilesOnly: true})
	if tree(all) == tree(trackedOnly) || slices.Contains(lsTree(t, dir, tree(trackedOnly)), "untracked-only.txt") {
		t.Fatal("tracked-only reused the cache")
	}
}

func TestCaptureDeleteRecreate(t *testing.T) { // :505
	t.Parallel()
	dir, cache := repositoryFixture(t), cachePath(t)
	write(t, dir, "ephemeral.txt", "first\n")
	withFile := snap(t, dir, CaptureOptions{SessionCachePath: cache})
	os.Remove(filepath.Join(dir, "ephemeral.txt"))
	deleted := snap(t, dir, CaptureOptions{SessionCachePath: cache})
	write(t, dir, "ephemeral.txt", "recreated\n")
	recreated := snap(t, dir, CaptureOptions{SessionCachePath: cache})
	if tree(deleted) == tree(withFile) || tree(recreated) == tree(deleted) || tree(recreated) == tree(withFile) || git(t, dir, "show", tree(recreated)+":ephemeral.txt") != "recreated" {
		t.Fatal("trees not distinct")
	}
}

func TestCaptureKeepsProtectedDescriptors(t *testing.T) { // :525
	t.Parallel()
	dir := repositoryFixture(t)
	write(t, dir, ".faultlineignore", "pnpm-lock.yaml\npackage.json\n")
	write(t, dir, "pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
	write(t, dir, "package.json", `{"name":"demo","version":"1.0.0"}`+"\n")
	var warnings []string
	v := snap(t, dir, CaptureOptions{OnWarning: func(w string) { warnings = append(warnings, w) }})
	paths := lsTree(t, dir, tree(v))
	if !slices.Contains(paths, "pnpm-lock.yaml") || !slices.Contains(paths, "package.json") ||
		!slices.ContainsFunc(warnings, func(w string) bool { return strings.HasPrefix(w, "Protected environment descriptor") }) {
		t.Fatal(paths, warnings)
	}
}

func looseObjects(t *testing.T, dir string) int {
	n := 0
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() && len(e.Name()) == 2 {
			sub, _ := os.ReadDir(filepath.Join(dir, e.Name()))
			n += len(sub)
		}
	}
	return n
}

func TestCaptureQuarantinesObjects(t *testing.T) { // :545
	t.Parallel()
	dir := repositoryFixture(t)
	before := looseObjects(t, PrimaryObjectDirectory(dir))
	write(t, dir, "tracked.txt", "quarantine-me\n")
	write(t, dir, "brand-new.txt", "new blob for quarantine\n")
	snap(t, dir, CaptureOptions{})
	if looseObjects(t, PrimaryObjectDirectory(dir)) != before || looseObjects(t, ObjectDirectory(dir)) == 0 {
		t.Fatal("objects were not quarantined")
	}
}

func TestCaptureStagesIncrementally(t *testing.T) { // :628
	t.Parallel()
	dir := repositoryFixture(t)
	for i := 0; i < 40; i++ {
		write(t, dir, "bulk-"+strconv.Itoa(i)+".txt", "bulk "+strconv.Itoa(i)+"\n")
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "bulk")
	write(t, dir, "tracked.txt", "one dirty edit\n")
	counts := map[string]int{}
	v := snap(t, dir, CaptureOptions{runGit: countingRunner(counts)})
	if counts["read-tree"] == 0 || counts["add"] != 0 || counts["hash-object"] != 2 || counts["update-index"] != 2 {
		t.Fatal(counts)
	}
	if git(t, dir, "show", tree(v)+":tracked.txt") != "one dirty edit" || !slices.Contains(lsTree(t, dir, tree(v)), "bulk-0.txt") {
		t.Fatal("staged tree is wrong")
	}
}

func TestCaptureSkipsCleanFilters(t *testing.T) { // :661
	t.Parallel()
	dir := repositoryFixture(t)
	git(t, dir, "config", "filter.faultline-evil.clean", "sed s/^/CLEANED:/")
	write(t, dir, ".gitattributes", "*.js filter=faultline-evil\n")
	write(t, dir, "app.js", "console.log(2);\n")
	v := snap(t, dir, CaptureOptions{})
	if got := git(t, dir, "show", tree(v)+":app.js"); got != "console.log(2);" {
		t.Fatal(got)
	}
}

func TestCaptureRemovesNewlyIgnoredTracked(t *testing.T) { // :702
	t.Parallel()
	dir, cache := repositoryFixture(t), cachePath(t)
	write(t, dir, "noise.tmp", "noise\n")
	git(t, dir, "add", "noise.tmp")
	git(t, dir, "commit", "-qm", "noise")
	write(t, dir, "tracked.txt", "dirty\n")
	before := snap(t, dir, CaptureOptions{SessionCachePath: cache})
	if !slices.Contains(lsTree(t, dir, tree(before)), "noise.tmp") {
		t.Fatal("noise.tmp missing")
	}
	write(t, dir, ".faultlineignore", "noise.tmp\n")
	after := snap(t, dir, CaptureOptions{SessionCachePath: cache})
	if slices.Contains(lsTree(t, dir, tree(after)), "noise.tmp") || git(t, dir, "show", tree(after)+":tracked.txt") != "dirty" {
		t.Fatal("newly ignored path kept")
	}
}

func TestCaptureRestoredClean(t *testing.T) { // :725-889
	t.Parallel()
	t.Run("mode only", func(t *testing.T) { // :725
		dir, cache := repositoryFixture(t), cachePath(t)
		git(t, dir, "config", "core.filemode", "true")
		write(t, dir, "second.txt", "edited-1\n")
		afterEdit := snap(t, dir, CaptureOptions{SessionCachePath: cache})
		os.Remove(filepath.Join(dir, "tracked.txt"))
		afterDelete := snap(t, dir, CaptureOptions{SessionCachePath: cache})
		os.Chmod(filepath.Join(dir, "second.txt"), 0o755)
		afterMode := snap(t, dir, CaptureOptions{SessionCachePath: cache})
		if tree(afterDelete) == tree(afterEdit) || tree(afterMode) == tree(afterDelete) || git(t, dir, "ls-tree", tree(afterMode), "second.txt")[:6] != "100755" {
			t.Fatal("mode change reused a tree")
		}
	})
	t.Run("restore one of two", func(t *testing.T) { // :751
		dir, cache := repositoryFixture(t), cachePath(t)
		write(t, dir, "tracked.txt", "dirty at turn N\n")
		write(t, dir, "other.txt", "other dirty\n")
		turnN := snap(t, dir, CaptureOptions{SessionCachePath: cache})
		write(t, dir, "tracked.txt", "stable\n")
		write(t, dir, "other.txt", "other still dirty\n")
		turnN1 := snap(t, dir, CaptureOptions{SessionCachePath: cache})
		if git(t, dir, "show", tree(turnN1)+":tracked.txt") != "stable" || git(t, dir, "show", tree(turnN1)+":other.txt") != "other still dirty" || tree(turnN1) == tree(turnN) {
			t.Fatal("restored file kept stale bytes")
		}
	})
	t.Run("restore A while B stays dirty", func(t *testing.T) { // :774
		dir, cache := repositoryFixture(t), cachePath(t)
		write(t, dir, "a.txt", "a-head\n")
		write(t, dir, "b.txt", "b-head\n")
		git(t, dir, "add", "a.txt", "b.txt")
		git(t, dir, "commit", "-qm", "a and b")
		write(t, dir, "a.txt", "a-dirty\n")
		write(t, dir, "b.txt", "b-dirty\n")
		turnN := snap(t, dir, CaptureOptions{SessionCachePath: cache})
		if git(t, dir, "show", tree(turnN)+":a.txt") != "a-dirty" || git(t, dir, "show", tree(turnN)+":b.txt") != "b-dirty" {
			t.Fatal("turn N tree wrong")
		}
		write(t, dir, "a.txt", "a-head\n")
		write(t, dir, "b.txt", "b-still-dirty\n")
		turnN1 := snap(t, dir, CaptureOptions{SessionCachePath: cache})
		if git(t, dir, "show", tree(turnN1)+":a.txt") != "a-head" || git(t, dir, "show", tree(turnN1)+":b.txt") != "b-still-dirty" {
			t.Fatal("restored a.txt kept stale bytes")
		}
	})
	t.Run("restore exec bit", func(t *testing.T) { // :800
		dir, cache := repositoryFixture(t), cachePath(t)
		write(t, dir, "script.sh", "#!/bin/sh\necho ok\n")
		git(t, dir, "add", "script.sh")
		git(t, dir, "commit", "-qm", "script")
		write(t, dir, "script.sh", "#!/bin/sh\necho dirty\n")
		os.Chmod(filepath.Join(dir, "script.sh"), 0o755)
		write(t, dir, "other.txt", "anchor dirty\n")
		turnN := snap(t, dir, CaptureOptions{SessionCachePath: cache})
		write(t, dir, "script.sh", "#!/bin/sh\necho ok\n")
		os.Chmod(filepath.Join(dir, "script.sh"), 0o644)
		write(t, dir, "other.txt", "anchor still dirty\n")
		turnN1 := snap(t, dir, CaptureOptions{SessionCachePath: cache})
		if git(t, dir, "show", tree(turnN1)+":script.sh") != "#!/bin/sh\necho ok" || git(t, dir, "ls-tree", tree(turnN1), "script.sh")[:6] != "100644" || tree(turnN1) == tree(turnN) {
			t.Fatal("restored mode kept")
		}
	})
	t.Run("pass fail pass", func(t *testing.T) { // :831
		dir, cache := repositoryFixture(t), cachePath(t)
		write(t, dir, "noise.tmp", "noise\n")
		git(t, dir, "add", "noise.tmp")
		git(t, dir, "commit", "-qm", "noise")
		write(t, dir, ".faultlineignore", "noise.tmp\n")
		git(t, dir, "add", ".faultlineignore")
		git(t, dir, "commit", "-qm", "ignore noise")
		baseline := snap(t, dir, CaptureOptions{SessionCachePath: cache})
		write(t, dir, "tracked.txt", "breaking edit\n")
		fail := snap(t, dir, CaptureOptions{SessionCachePath: cache})
		write(t, dir, "tracked.txt", "stable\n")
		pass := snap(t, dir, CaptureOptions{SessionCachePath: cache})
		if tree(fail) == tree(baseline) || tree(pass) == tree(fail) || tree(pass) != tree(baseline) || git(t, dir, "show", tree(pass)+":tracked.txt") != "stable" {
			t.Fatal("PASS-FAIL-PASS trees wrong")
		}
		if slices.Contains(lsTree(t, dir, tree(pass)), "noise.tmp") {
			t.Fatal("ignored noise.tmp in the tree")
		}
	})
	t.Run("clean porcelain, ignored tracked path forces the slow path", func(t *testing.T) { // :863
		dir, cache := repositoryFixture(t), cachePath(t)
		write(t, dir, "noise.tmp", "noise\n")
		git(t, dir, "add", "noise.tmp")
		git(t, dir, "commit", "-qm", "noise")
		write(t, dir, ".faultlineignore", "noise.tmp\n")
		git(t, dir, "add", ".faultlineignore")
		git(t, dir, "commit", "-qm", "ignore noise")
		write(t, dir, "tracked.txt", "dirty turn N\n")
		turnN := snap(t, dir, CaptureOptions{SessionCachePath: cache})
		if git(t, dir, "show", tree(turnN)+":tracked.txt") != "dirty turn N" || slices.Contains(lsTree(t, dir, tree(turnN)), "noise.tmp") {
			t.Fatal("turn N tree wrong")
		}
		write(t, dir, "tracked.txt", "stable\n")
		// Porcelain is fully clean; the ignored tracked noise.tmp forces staging.
		if git(t, dir, "status", "--porcelain") != "" {
			t.Fatal("porcelain not clean")
		}
		counts := map[string]int{}
		turnN1 := snap(t, dir, CaptureOptions{SessionCachePath: cache, runGit: countingRunner(counts)})
		if git(t, dir, "show", tree(turnN1)+":tracked.txt") != "stable" || tree(turnN1) == tree(turnN) {
			t.Fatal("restored bytes not captured")
		}
		if counts["write-tree"] == 0 || counts["read-tree"] == 0 {
			t.Fatalf("clean porcelain took the fast path: %v", counts)
		}
	})
}

func TestRepoCachePath(t *testing.T) {
	t.Parallel()
	dir := repositoryFixture(t)
	got, err := RepoCachePath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, ".git", "faultline", "turn-snapshot-cache.json"); got != want {
		t.Fatalf("%s, want %s", got, want)
	}
	if _, err := RepoCachePath(t.TempDir()); err == nil || !strings.HasPrefix(err.Error(), "FaultLine could not safely capture a turn tree snapshot: fatal: not a git repository") {
		t.Fatal(err)
	}
}

func TestCorruptCacheIsIgnored(t *testing.T) {
	t.Parallel()
	dir, cache := repositoryFixture(t), cachePath(t)
	write(t, dir, "tracked.txt", "dirty\n")
	os.WriteFile(cache, []byte("{not json"), 0o644)
	v := snap(t, dir, CaptureOptions{SessionCachePath: cache})
	raw, _ := os.ReadFile(cache)
	if c, err := jsjson.Parse(string(raw)); err != nil || c.Get("treeDigest").Str() != tree(v) {
		t.Fatalf("%s", raw)
	}
}

func TestParsePorcelainStatusZ(t *testing.T) {
	t.Parallel()
	got := parsePorcelainStatusZ(" M a\x00R  new\x00old\x00?? ./b\x00x\x00R  dangling\x00")
	want := []porcelainPath{{" M", "a"}, {"R ", "old"}, {"??", "b"}}
	if !slices.Equal(got, want) {
		t.Fatal(got)
	}
}
