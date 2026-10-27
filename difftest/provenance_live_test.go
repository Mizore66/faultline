//go:build faultline_test && unix

package difftest

import (
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/proofview"
	"github.com/Mizore66/faultline/internal/provenance"
)

// Slice 4c, Task 14: internal/provenance (src/github-provenance.ts) against
// frozen TS. The proof bundle loads are memoized on both sides (the
// oracle's provenanceModule, provenanceViews here): each takes seconds and
// the bases do not change.

const (
	provRepository = "Mizore66/faultline"
	provWorkflow   = "Mizore66/faultline/.github/workflows/go.yml"
	provRef        = "refs/heads/main"
	provCreatedAt  = "2026-07-16T12:00:00.000Z"
)

func vstr(s string) jsjson.Value { return jsjson.MakeString(s) }

// provenanceViews memoizes the Go side's loadVerifiedGitProofView per
// directory (the view, or the error), as the oracle does for TS.
type provenanceViews struct {
	mu    sync.Mutex
	views map[string]provView
}

type provView struct {
	view proofview.View
	err  error
}

func memoizeProvenanceViews(t *testing.T) *provenanceViews {
	m := &provenanceViews{views: map[string]provView{}}
	restore := provenance.SetViewLoaderForTest(func(dir string) (proofview.View, error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		v, ok := m.views[dir]
		if !ok {
			v.view, v.err = proofview.LoadVerifiedGitProofView(dir, nil)
			m.views[dir] = v
		}
		return v.view, v.err
	})
	t.Cleanup(restore)
	return m
}

// provBaseDirs are the Git bases (all of them native Docker), by name.
func provBaseDirs(t *testing.T) (names []string, dirs map[string]string) {
	t.Helper()
	root := filepath.Join(oracle.RepoRoot(t), "difftest", "testdata", "bases")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	dirs = map[string]string{}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "git-") {
			names = append(names, e.Name())
			dirs[e.Name()] = filepath.Join(root, e.Name())
		}
	}
	return names, dirs
}

// provBaseEnv is base-env.json's environment for a base (git-sha256's
// bundle extraction needs GIT_DEFAULT_HASH=sha256).
var provBaseEnv = map[string]map[string]string{"git-sha256": {"GIT_DEFAULT_HASH": "sha256"}}

func provCreateArgs(bundle string, ci jsjson.Value, createdAt *string, env map[string]string) string {
	o := jsjson.NewObj()
	o.Set("bundle", vstr(bundle))
	o.Set("ci", ci)
	if createdAt != nil {
		o.Set("createdAt", vstr(*createdAt))
	}
	if len(env) > 0 {
		e := jsjson.NewObj()
		for k, v := range env {
			e.Set(k, vstr(v))
		}
		o.Set("env", jsjson.MakeObject(e))
	}
	return jsjson.Stringify(jsjson.MakeObject(o))
}

// provCreate is createGithubProvenanceReceipt on the Go side, with
// createdAt nil reading the clock.
func provCreate(bundle string, ci jsjson.Value, createdAt *string) string {
	var receipt jsjson.Value
	var err error
	if createdAt == nil {
		receipt, err = provenance.CreateReceipt(bundle, ci)
	} else {
		receipt, err = provenance.CreateReceiptAt(bundle, ci, *createdAt)
	}
	return threwOr(receipt, err)
}

// warmProvenanceBases loads each base once on both sides (the Go loads run
// next to the oracle's) and compares the receipts made from it with ci.
func warmProvenanceBases(t *testing.T, c *oracle.Client, names []string, dirs map[string]string, ci jsjson.Value) map[string]string {
	t.Helper()
	at := provCreatedAt
	// The Go loads run next to the oracle's: a temporary directory of their
	// own keeps the two seeded mkdtemp sequences from meeting.
	t.Setenv("TMPDIR", t.TempDir())
	goResults := map[string]string{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, name := range names {
			var restore []func()
			for k, v := range provBaseEnv[name] {
				saved, had := os.LookupEnv(k)
				os.Setenv(k, v)
				restore = append(restore, func() {
					if had {
						os.Setenv(k, saved)
					} else {
						os.Unsetenv(k)
					}
				})
			}
			goResults[name] = provCreate(dirs[name], ci, &at)
			for _, f := range restore {
				f()
			}
		}
	}()
	want := map[string]string{}
	for _, name := range names {
		want[name] = oracleText(t, c, "createGithubProvenanceReceiptFromView", provCreateArgs(dirs[name], ci, &at, provBaseEnv[name]))
	}
	<-done
	for _, name := range names {
		if goResults[name] != want[name] {
			t.Fatalf("base %s:\ngot  %s\nwant %s", name, goResults[name], want[name])
		}
	}
	return want
}

// syncProvenanceClock is syncAttestClock for the provenance ops: the
// module is imported by a call refused before the clock read.
func syncProvenanceClock(t *testing.T, c *oracle.Client) func(where string) {
	t.Helper()
	oracleCall(t, c, "createGithubProvenanceReceiptFromView", `{"bundle":"/nonexistent-faultline-bundle","ci":null}`)
	time.Sleep(time.Second)
	tick, err := strconv.ParseInt(oracleCall(t, c, "seededTick", "null"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	for range tick + 1 {
		clock.NowMillis()
	}
	return func(where string) {
		t.Helper()
		if ts, goTick := oracleCall(t, c, "seededTick", "null"), clock.NowMillis()-seededEpochMillis; ts != strconv.FormatInt(goTick, 10) {
			t.Fatalf("%s: oracle clock at %s, Go clock at %d", where, ts, goTick)
		}
	}
}

// provGit runs the test's own git with fixed identity and dates and no
// user configuration.
func provGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=FaultLine Test", "-c", "user.email=test@faultline.invalid",
		"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_DATE=2026-07-16T12:00:00Z", "GIT_COMMITTER_DATE=2026-07-16T12:00:00Z")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %q: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// provRepo is a one-commit repository: its HEAD, tree and file blob.
type provRepo struct{ dir, head, tree, blob string }

func makeProvRepo(t *testing.T, dir, format string) provRepo {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	provGit(t, dir, "init", "-q", "--object-format="+format)
	if err := os.WriteFile(filepath.Join(dir, "state.txt"), []byte("state\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	provGit(t, dir, "add", "state.txt")
	provGit(t, dir, "commit", "-q", "-m", "state")
	return provRepo{dir, provGit(t, dir, "rev-parse", "HEAD"), provGit(t, dir, "rev-parse", "HEAD^{tree}"), provGit(t, dir, "rev-parse", "HEAD:state.txt")}
}

// provEnvVar is one GITHUB_* variable; a nil value is unset.
type provEnvVar struct {
	name  string
	value *string
}

type provIdentityCase struct {
	workspace string
	env       []provEnvVar
}

var provEnvNames = []string{"GITHUB_ACTIONS", "GITHUB_SHA", "GITHUB_REPOSITORY", "GITHUB_WORKFLOW_REF", "GITHUB_REF",
	"GITHUB_RUN_ID", "GITHUB_RUN_ATTEMPT", "GITHUB_EVENT_NAME"}

// provIdentifier is a workflow ref or ref at NonEmptyIdentifierSchema's
// boundaries: trim, min 1, max 1,024 UTF-16 units, control characters.
func provIdentifier(r *rand.Rand, good string) string {
	return []string{
		good, " " + good + " ", "\u00a0" + good + "\ufeff", good + "\u0001", "a\nb", "a\tb", good + "\u007f",
		strings.Repeat("w", 1024), strings.Repeat("w", 1025), " " + strings.Repeat("w", 1024) + " ", strings.Repeat("w", 1023) + astralChar,
		good + astralChar, good + loneSurrogate, "\u2028" + good, "é", "\u0080x",
	}[r.IntN(16)]
}

// provIdentity generates one GitHub Actions environment: mostly valid, with
// one to three variables at a boundary or unset.
func provIdentity(r *rand.Rand, sha1, sha256 provRepo, scratch string) provIdentityCase {
	values := map[string]string{
		"GITHUB_ACTIONS": "true", "GITHUB_SHA": sha1.head, "GITHUB_REPOSITORY": provRepository,
		"GITHUB_WORKFLOW_REF": provWorkflow + "@" + provRef, "GITHUB_REF": provRef, "GITHUB_RUN_ID": "123",
		"GITHUB_RUN_ATTEMPT": "1", "GITHUB_EVENT_NAME": "push",
	}
	unset := map[string]bool{}
	workspace := sha1.dir
	edits := 0
	if r.IntN(4) != 0 {
		edits = 1 + r.IntN(3)
	}
	for ; edits > 0; edits-- {
		name := provEnvNames[r.IntN(len(provEnvNames))]
		if r.IntN(8) == 0 {
			unset[name] = true
			continue
		}
		var v string
		switch name {
		case "GITHUB_ACTIONS":
			v = []string{"false", "TRUE", " true", "1", ""}[r.IntN(5)]
		case "GITHUB_SHA":
			switch r.IntN(12) {
			case 0:
				v = strings.ToUpper(sha1.head)
			case 1:
				v = sha1.head[:12]
			case 2:
				v = " " + sha1.head + "\n"
			case 3:
				v = strings.Repeat("0", 40)
			case 4:
				v = sha1.tree
			case 5:
				v = sha1.blob
			case 6, 7:
				v, workspace = sha256.head, sha256.dir
			case 8:
				v = strings.Repeat("1", 64)
			case 9:
				v = "   "
			case 10:
				v = sha256.tree
				workspace = sha256.dir
			default:
				v = sha1.head + "x"
			}
		case "GITHUB_REPOSITORY":
			v = []string{" " + provRepository + " ", "o/r/x", "o r/x", "a/b", "é/x", "o/", "/r", "a.b-c_d/e.f", "  "}[r.IntN(9)]
		case "GITHUB_WORKFLOW_REF":
			v = provIdentifier(r, provWorkflow+"@"+provRef)
		case "GITHUB_REF":
			v = provIdentifier(r, provRef)
		case "GITHUB_RUN_ID":
			v = []string{"0", "abc", " 9 ", "1.0", "-1", strings.Repeat("7", 30), "", "١٢"}[r.IntN(8)]
		case "GITHUB_RUN_ATTEMPT":
			v = []string{"1e0", "01", "0", "-1", "1.5", "abc", " 2 ", "0x10", "Infinity", "+1", ".5e1", "1e400", "0b11", "  ", "", "-0", "2\u00a0", "9007199254740993"}[r.IntN(18)]
		case "GITHUB_EVENT_NAME":
			v = []string{"pull_request", " push ", "PUSH", "push\t", "workflow_dispatch"}[r.IntN(5)]
		}
		values[name] = v
	}
	if r.IntN(15) == 0 {
		workspace = []string{filepath.Join(scratch, "missing"), scratch, filepath.Join(scratch, "file.txt")}[r.IntN(3)]
	}
	var env []provEnvVar
	for _, name := range provEnvNames {
		if unset[name] {
			env = append(env, provEnvVar{name, nil})
			continue
		}
		v := values[name]
		env = append(env, provEnvVar{name, &v})
	}
	return provIdentityCase{workspace, env}
}

func (ic provIdentityCase) value() jsjson.Value {
	env := jsjson.NewObj()
	for _, e := range ic.env {
		if e.value == nil {
			env.Set(e.name, jsjson.MakeNull())
		} else {
			env.Set(e.name, vstr(*e.value))
		}
	}
	return jsjson.MustObject("workspace", vstr(ic.workspace), "env", jsjson.MakeObject(env))
}

// clearGithubEnv unsets every GITHUB_* name (t.Setenv restores them).
func clearGithubEnv(t *testing.T) {
	for _, kv := range os.Environ() {
		if name, value, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "GITHUB_") {
			t.Setenv(name, value)
			os.Unsetenv(name)
		}
	}
}

// goIdentity runs IdentityFromEnvironment with the case's environment.
func goIdentity(ic provIdentityCase) (jsjson.Value, error) {
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "GITHUB_") {
			os.Unsetenv(name)
		}
	}
	// Node stores a JS string in the environment as UTF-8, a lone
	// surrogate as U+FFFD.
	for _, e := range ic.env {
		if e.value != nil {
			os.Setenv(e.name, jsstr.ToUTF8(*e.value))
		}
	}
	defer func() {
		for _, e := range ic.env {
			os.Unsetenv(e.name)
		}
	}()
	return provenance.IdentityFromEnvironment(ic.workspace)
}

// provGoodCI is a valid identity for repo.
func provGoodCI(repo provRepo) jsjson.Value {
	return jsjson.MustObject("provider", vstr("GITHUB_ACTIONS"), "repository", vstr(provRepository),
		"workflowRef", vstr(provWorkflow+"@"+provRef), "ref", vstr(provRef), "commit", vstr(repo.head), "tree", vstr(repo.tree),
		"runId", vstr("123"), "runAttempt", jsjson.MakeNumber(1), "eventName", vstr("push"))
}

// provMutateCI changes one member of a valid identity the ways a caller of
// createGithubProvenanceReceiptFromView could.
func provMutateCI(r *rand.Rand, ci jsjson.Value) jsjson.Value {
	ci = parseOrFatalNoT(canonicalOf(ci))
	o := ci.Obj()
	keys := o.Keys()
	switch r.IntN(8) {
	case 0:
		o.Delete(keys[r.IntN(len(keys))])
	case 1:
		o.Set("extra", vstr("x"))
	case 2:
		o.Set("runAttempt", []jsjson.Value{jsjson.MakeNumber(0), jsjson.MakeNumber(-1), jsjson.MakeNumber(1.5), vstr("1"), jsjson.MakeNull(), jsjson.MakeNumber(1e300)}[r.IntN(6)])
	case 3:
		o.Set("provider", vstr("GITLAB"))
	case 4:
		o.Set([]string{"commit", "tree"}[r.IntN(2)], vstr(strings.Repeat("A", 40)))
	case 5:
		o.Set([]string{"workflowRef", "ref"}[r.IntN(2)], vstr(" padded "))
	case 6:
		return []jsjson.Value{jsjson.MakeNull(), vstr("ci"), jsjson.MakeArray(nil)}[r.IntN(3)]
	case 7:
		o.Set("eventName", vstr("schedule"))
	}
	return ci
}

func parseOrFatalNoT(text string) jsjson.Value {
	v, err := jsjson.Parse(text)
	if err != nil {
		panic(err)
	}
	return v
}

// provCreatedAts are createdAt values: canonical, and each way
// datetime({offset: true}) or the canonical refine (or both) refuses one.
var provCreatedAts = append(slices.Clone(attestTimestamps),
	"2026-07-16T12:00:00+00:00", "2026-07-16T12:00:00.000+0000", "2026-07-16T12:00:00.123456Z", "2026-07-16T12:00Z", "2026-07-16T14:00:00.000+02:00")

// requireProvReached fails for each text that no TS result contained.
func requireProvReached(t *testing.T, outputs string, texts ...string) {
	t.Helper()
	for _, text := range texts {
		if !strings.Contains(outputs, text) {
			t.Errorf("no generated input reached %q", text)
		}
	}
}

// TestLiveProvenanceReceipt compares, for every Git base, 20 generated
// GitHub Actions environments (githubActionsIdentityFromEnvironment in a
// child node with that environment, and IdentityFromEnvironment with the
// same one) and the receipts made from each identity (or from a mutated
// valid one where the environment was refused), with edge createdAt values
// or the seeded clock. It then builds receipts from 300 edited views
// (createGithubProvenanceReceiptFromView): runs that are not native
// Docker, unpinned images, no runs, duplicate and reordered runs, several
// sandbox bindings, and invalid view facts.
func TestLiveProvenanceReceipt(t *testing.T) {
	oracle.Skip(t)
	const seed = "live-provenance-receipt"
	c := oracle.StartSeeded(t, seed)
	defer c.Close()
	clock.ResetForTest(seed)
	syncCheck := syncProvenanceClock(t, c)
	views := memoizeProvenanceViews(t)
	clearGithubEnv(t)
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sha1 := makeProvRepo(t, filepath.Join(work, "repo"), "sha1")
	sha256 := makeProvRepo(t, filepath.Join(work, "repo256"), "sha256")
	scratch := filepath.Join(work, "scratch")
	os.MkdirAll(scratch, 0o755)
	os.WriteFile(filepath.Join(scratch, "file.txt"), []byte("x"), 0o644)

	names, dirs := provBaseDirs(t)
	var outputs strings.Builder
	for _, w := range warmProvenanceBases(t, c, names, dirs, provGoodCI(sha1)) {
		outputs.WriteString(w + "\n")
	}
	syncCheck("after the bases")

	r := rand.New(rand.NewPCG(14, 20))
	identities, refused, receipts := 0, 0, 0
	for _, name := range names {
		cases := make([]provIdentityCase, 20)
		items := make([]jsjson.Value, len(cases))
		for i := range cases {
			cases[i] = provIdentity(r, sha1, sha256, scratch)
			items[i] = cases[i].value()
		}
		args := jsjson.Stringify(jsjson.MustObject("cases", jsjson.MakeArray(items)))
		want, err := jsjson.Parse(oracleCall(t, c, "githubActionsIdentityFromEnvironment", args))
		if err != nil {
			t.Fatal(err)
		}
		for i, ic := range cases {
			w := want.Items()[i].Str()
			outputs.WriteString(w + "\n")
			identity, err := goIdentity(ic)
			if got := threwOr(identity, err); got != w {
				t.Fatalf("base %s identity %d %s:\ngot  %s\nwant %s", name, i, jsjson.Stringify(ic.value()), got, w)
			}
			identities++
			ci := identity
			if err != nil {
				refused++
				ci = provMutateCI(r, provGoodCI(sha1))
			}
			var createdAt *string
			if r.IntN(3) != 0 {
				at := provCreatedAts[r.IntN(len(provCreatedAts))]
				if r.IntN(2) == 0 {
					at = provCreatedAt
				}
				createdAt = &at
			}
			cargs := provCreateArgs(dirs[name], ci, createdAt, nil)
			want := oracleText(t, c, "createGithubProvenanceReceiptFromView", cargs)
			outputs.WriteString(want + "\n")
			if got := provCreate(dirs[name], ci, createdAt); got != want {
				t.Fatalf("base %s receipt %d (%s):\ngot  %s\nwant %s", name, i, cargs, got, want)
			}
			receipts++
		}
		syncCheck("base " + name)
	}

	// Edited views: the Go View and the object TS receives hold the same facts.
	var baseViews []proofview.View
	for _, name := range []string{"git-unbound", "git-two-states", "git-sample-self-incident", "git-cjk-overlays"} {
		if v := views.views[dirs[name]]; v.err == nil {
			baseViews = append(baseViews, v.view)
		}
	}
	edited := 0
	for i := 0; i < 300; i++ {
		base := baseViews[r.IntN(len(baseViews))]
		view := proofview.View{
			RootDigest:    base.RootDigest,
			Manifest:      parseOrFatal(t, canonicalOf(base.Manifest)),
			Investigation: parseOrFatal(t, canonicalOf(base.Investigation)),
			FrozenWitness: parseOrFatal(t, canonicalOf(base.FrozenWitness)),
		}
		for n := 1 + r.IntN(3); n > 0; n-- {
			mutateProvView(r, &view)
		}
		ci := provGoodCI(sha1)
		if r.IntN(10) == 0 {
			ci = provMutateCI(r, ci)
		}
		var createdAt *string
		if r.IntN(4) != 0 {
			at := provCreatedAt
			createdAt = &at
		}
		viewValue := jsjson.MustObject("rootDigest", vstr(view.RootDigest), "manifest", view.Manifest,
			"investigation", view.Investigation, "frozenWitness", view.FrozenWitness)
		o := jsjson.NewObj()
		o.Set("view", viewValue)
		o.Set("ci", ci)
		if createdAt != nil {
			o.Set("createdAt", vstr(*createdAt))
		}
		args := jsjson.Stringify(jsjson.MakeObject(o))
		want := oracleText(t, c, "createGithubProvenanceReceiptFromView", args)
		outputs.WriteString(want + "\n")
		receipt, err := provenance.CreateFromView(view, ci, createdAt)
		if got := threwOr(receipt, err); got != want {
			t.Fatalf("edited view %d:\ngot  %s\nwant %s", i, got, want)
		}
		edited++
		if i%50 == 0 {
			syncCheck(fmt.Sprintf("edited view %d", i))
		}
	}
	requireProvReached(t, outputs.String(),
		"GitHub provenance receipts can only be created inside GitHub Actions; local metadata is not a signed CI receipt.",
		"GitHub Actions provenance requires GITHUB_SHA.", "GitHub Actions provenance requires GITHUB_RUN_ATTEMPT.",
		"GitHub Actions provenance requires GITHUB_REPOSITORY.", "GitHub Actions provenance requires GITHUB_EVENT_NAME.",
		"GitHub provenance could not resolve the checked-out commit tree: Command failed: git rev-parse ",
		"GitHub provenance could not resolve the checked-out commit tree: spawnSync git ",
		"expected a Git object id", "expected an owner/repository identifier", "must not contain control characters",
		"expected a numeric GitHub run id", "Expected number, received nan", "Expected integer, received float",
		"Number must be greater than 0", "String must contain at most 1024 character(s)",
		`Invalid literal value, expected \"push\"`, "Invalid datetime", "expected a canonical UTC ISO-8601 timestamp",
		"GitHub provenance requires a verified native-Docker proof bundle.", "GitHub provenance requires at least one recorded execution.",
		"expected a digest-pinned container image", "expected sha256:<64 lowercase hex characters>",
		"Refusing to render an invalid Git proof bundle: ", `"receiptDigest":"sha256:`, `"runAttempt":1,`)
	liveSummary(t, "provenance receipt: %d identities (%d refused), %d receipts, %d edited views", identities, refused, receipts, edited)
}

// mutateProvView edits a view's facts the ways createGithubProvenanceReceiptFromView
// reads them.
func mutateProvView(r *rand.Rand, view *proofview.View) {
	runs := view.Investigation.Get("runs").Items()
	pick := func() jsjson.Value { return runs[r.IntN(len(runs))] }
	if len(runs) == 0 {
		return
	}
	pinned := func(c string) string { return "registry.example/x@sha256:" + strings.Repeat(c, 64) }
	switch r.IntN(16) {
	case 0:
		pick().Get("result").Obj().Set("executor", vstr([]string{"INJECTED_RUNNER", "UNSAFE_LOCAL"}[r.IntN(2)]))
	case 1:
		pick().Get("sandbox").Obj().Set("kind", vstr("UNSAFE_LOCAL"))
	case 2:
		pick().Get("sandbox", "runtime").Obj().Set("image", jsjson.MakeNull())
	case 3:
		run := pick().Get("sandbox", "runtime").Obj()
		if r.IntN(3) == 0 {
			run.Delete("image")
		} else {
			run.Set("image", vstr([]string{"node:22", "x@sha256:" + strings.Repeat("A", 64), "", pinned("a") + "\n"}[r.IntN(4)]))
		}
	case 4:
		pick().Get("sandbox").Obj().Set([]string{"policyDigest", "environmentPolicyDigest"}[r.IntN(2)], vstr("sha256:xyz"))
	case 5:
		view.Investigation.Obj().Set("runs", jsjson.MakeArray(nil))
	case 6: // a reordered subset
		out := slices.Clone(runs)
		r.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
		view.Investigation.Obj().Set("runs", jsjson.MakeArray(out[:1+r.IntN(len(out))]))
	case 7: // duplicates: sortedUnique, and executionCount counts them
		view.Investigation.Obj().Set("runs", jsjson.MakeArray(append(slices.Clone(runs), runs[:1+r.IntN(len(runs))]...)))
	case 8, 9: // several sandbox bindings, sorted by their canonical JSON
		for _, run := range runs {
			if r.IntN(2) == 0 {
				run.Get("sandbox", "runtime").Obj().Set("image", vstr([]string{pinned("b"), pinned("0"), "Registry/x@sha256:" + strings.Repeat("c", 64), "a/é@sha256:" + strings.Repeat("d", 64)}[r.IntN(4)]))
			}
		}
	case 10: // timestamps the min and max sorts choose from
		for _, run := range runs {
			if r.IntN(2) == 0 {
				run.Obj().Set([]string{"startedAt", "finishedAt"}[r.IntN(2)], vstr([]string{"2025-01-01T00:00:00.000Z", "2027-12-31T23:59:59.999Z", "2026-09-25T17:20:52.36Z", "2026-02-30T00:00:00.000Z", "z"}[r.IntN(5)]))
			}
		}
	case 11: // run ids the locale sort orders
		for _, run := range runs {
			run.Obj().Set([]string{"runId", "executionId"}[r.IntN(2)], vstr([]string{"b", "B", "a", "é", "e", "sha256:" + strings.Repeat("f", 64)}[r.IntN(6)]))
		}
	case 12:
		ep := view.Manifest.Get("resolvedRange", []string{"ancestor", "descendant"}[r.IntN(2)]).Obj()
		switch r.IntN(4) {
		case 0:
			ep.Set("index", jsjson.MakeNumber(-1))
		case 1:
			ep.Set("tree", vstr(strings.ToUpper(ep.Field("tree").Str())))
		case 2:
			ep.Set("extra", jsjson.MakeBool(true))
		default:
			ep.Set("index", jsjson.MakeNumber(0.5))
		}
	case 13:
		if r.IntN(2) == 0 {
			view.FrozenWitness.Get("approval").Obj().Delete("approvalDigest")
		} else {
			view.FrozenWitness.Obj().Set([]string{"frozenDigest", "witnessDigest"}[r.IntN(2)], vstr("sha256:"))
		}
	case 14:
		view.RootDigest = []string{"sha256:abc", "", "sha256:" + strings.Repeat("e", 64)}[r.IntN(3)]
	case 15:
		view.Manifest.Obj().Set("investigationDigest", []jsjson.Value{jsjson.MakeNumber(5), vstr("sha256:" + strings.Repeat("1", 64))}[r.IntN(2)])
	}
}

// provTrust is a trust configuration that permits ci.
func provTrust(ci jsjson.Value, denySelfHosted bool, rootFile string, withSourceDigest bool) jsjson.Value {
	o := jsjson.NewObj()
	o.Set("schemaVersion", vstr(provenance.TrustSchemaVersion))
	o.Set("repository", ci.Get("repository"))
	o.Set("signerWorkflow", vstr(provWorkflow))
	o.Set("sourceRef", ci.Get("ref"))
	o.Set("eventName", vstr("push"))
	o.Set("denySelfHostedRunners", jsjson.MakeBool(denySelfHosted))
	o.Set("trustedRootFile", vstr(rootFile))
	if withSourceDigest {
		o.Set("sourceDigest", ci.Get("commit"))
	}
	return jsjson.MakeObject(o)
}

// resignReceipt recomputes receiptDigest over the receipt's other members.
func resignReceipt(receipt jsjson.Value) {
	receipt.Obj().Set("receiptDigest", vstr(jsexc.Must(canonical.DigestJSON(without(receipt, "receiptDigest")))))
}

func without(v jsjson.Value, key string) jsjson.Value {
	out := jsjson.NewObj()
	for _, k := range v.Obj().Keys() {
		if k != key {
			out.Set(k, v.Obj().Field(k))
		}
	}
	return jsjson.MakeObject(out)
}

// provPaths lists every member path in v (objects and arrays, depth first).
func provPaths(v jsjson.Value, prefix []string, out *[][]string) {
	switch v.Kind() {
	case jsjson.Object:
		for _, k := range v.Obj().Keys() {
			p := append(slices.Clone(prefix), k)
			*out = append(*out, p)
			provPaths(v.Obj().Field(k), p, out)
		}
	case jsjson.Array:
		for i, item := range v.Items() {
			p := append(slices.Clone(prefix), strconv.Itoa(i))
			*out = append(*out, p)
			provPaths(item, p, out)
		}
	}
}

// provSet replaces (or with an Undefined value deletes) the member at path.
func provSet(root jsjson.Value, path []string, value jsjson.Value) {
	parent := root
	for _, k := range path[:len(path)-1] {
		if parent.Kind() == jsjson.Array {
			i, _ := strconv.Atoi(k)
			parent = parent.Items()[i]
		} else {
			parent = parent.Obj().Field(k)
		}
	}
	last := path[len(path)-1]
	if parent.Kind() == jsjson.Array {
		i, _ := strconv.Atoi(last)
		if value.Kind() == jsjson.Undefined {
			value = jsjson.MakeNull()
		}
		parent.Items()[i] = value
		return
	}
	if value.Kind() == jsjson.Undefined {
		parent.Obj().Delete(last)
	} else {
		parent.Obj().Set(last, value)
	}
}

// provOdd is a replacement member value: another type, or a string at a
// schema boundary.
func provOdd(r *rand.Rand) jsjson.Value {
	return []jsjson.Value{
		jsjson.Value{}, jsjson.MakeNull(), jsjson.MakeNumber(0), jsjson.MakeNumber(-1), jsjson.MakeNumber(1.5), jsjson.MakeBool(true),
		jsjson.MakeArray(nil), jsjson.MustObject(), vstr(""), vstr("x"), vstr("sha256:" + strings.Repeat("A", 64)),
		vstr(strings.Repeat("a", 40)), vstr(" padded "), vstr("a\u0001b"), vstr("2026-07-16T12:00:00Z"),
	}[r.IntN(15)]
}

// mutateProvReceipt edits a receipt (in place, or by returning another value).
func mutateProvReceipt(r *rand.Rand, receipt jsjson.Value) jsjson.Value {
	var paths [][]string
	provPaths(receipt, nil, &paths)
	switch r.IntN(14) {
	case 0, 1: // any member, without re-signing
		provSet(receipt, paths[r.IntN(len(paths))], provOdd(r))
	case 2, 3: // any member, re-signed: the proof facts no longer match
		provSet(receipt, paths[r.IntN(len(paths))], provOdd(r))
		resignReceipt(receipt)
	case 4: // a digest tamper alone
		receipt.Obj().Set("receiptDigest", vstr("sha256:"+strings.Repeat("9", 64)))
	case 5: // a fact edit that keeps the old digest
		receipt.Get("proof").Obj().Set("rootDigest", vstr("sha256:"+strings.Repeat("8", 64)))
	case 6: // a fact edit, re-signed
		receipt.Get("proof", "execution").Obj().Set("executionCount", jsjson.MakeNumber(receipt.Get("proof", "execution", "executionCount").Num()+1))
		resignReceipt(receipt)
	case 7, 8: // ci edits, re-signed: the trust checks decide
		ci := receipt.Get("ci").Obj()
		switch r.IntN(5) {
		case 0:
			ci.Set("repository", vstr("other/repo"))
		case 1:
			ci.Set("ref", vstr("refs/heads/other"))
		case 2:
			ci.Set("workflowRef", vstr(provWorkflow+"@refs/heads/other"))
		case 3:
			ci.Set("commit", vstr(strings.Repeat("c", 40)))
		default:
			ci.Set("workflowRef", vstr(" "+provWorkflow+"@"+provRef+" "))
		}
		resignReceipt(receipt)
	case 9: // a createdAt edit, re-signed: still bound
		receipt.Obj().Set("createdAt", vstr([]string{"2020-01-01T00:00:00.000Z", "2026-07-16T12:00:00Z", "2024-02-29T23:59:59.999Z"}[r.IntN(3)]))
		resignReceipt(receipt)
	case 10:
		obj := receipt
		if r.IntN(2) == 0 {
			obj = receipt.Get([]string{"signing", "proof", "ci"}[r.IntN(3)])
		}
		obj.Obj().Set("extra", vstr("x"))
	case 11:
		receipt.Obj().Set([]string{"schemaVersion", "receiptKind", "limitation"}[r.IntN(3)], vstr("other"))
	case 12:
		return []jsjson.Value{jsjson.MakeNull(), vstr("receipt"), jsjson.MakeArray(nil), jsjson.MakeNumber(1)}[r.IntN(4)]
	}
	return receipt
}

// mutateProvTrust edits a trust configuration.
func mutateProvTrust(r *rand.Rand, trust jsjson.Value) jsjson.Value {
	o := trust.Obj()
	keys := o.Keys()
	switch r.IntN(12) {
	case 0:
		o.Delete(keys[r.IntN(len(keys))])
	case 1:
		o.Set(keys[r.IntN(len(keys))], provOdd(r))
	case 2:
		o.Set("repository", vstr("other/repo"))
	case 3:
		o.Set("sourceRef", vstr("refs/heads/other"))
	case 4:
		o.Set("signerWorkflow", vstr([]string{"other/workflow.yml", " " + provWorkflow + " "}[r.IntN(2)]))
	case 5:
		o.Set("sourceDigest", vstr([]string{strings.Repeat("d", 40), strings.Repeat("D", 40), "abc"}[r.IntN(3)]))
	case 6:
		o.Set("eventName", vstr("pull_request"))
	case 7:
		o.Set("trustedRootFile", vstr([]string{"", "   ", strings.Repeat("r", 4097), " root.json "}[r.IntN(4)]))
	case 8:
		o.Set("extra", jsjson.MakeBool(true))
	case 9:
		o.Set("denySelfHostedRunners", vstr("true"))
	case 10:
		return []jsjson.Value{jsjson.MakeNull(), vstr("trust"), jsjson.MakeArray(nil)}[r.IntN(3)]
	}
	return trust
}

// TestLiveProvenanceBinding verifies 600 edited receipts and trust
// configurations against four bases with verifyGithubProvenanceBinding and
// VerifyBinding: each schema member (deleted, another type, at a
// boundary), digest tampers with and without re-signing, ci edits that
// each trust check refuses, both schema failures together, a receipt from
// another base and a bundle that does not exist.
func TestLiveProvenanceBinding(t *testing.T) {
	oracle.Skip(t)
	c := oracle.Start(t)
	defer c.Close()
	memoizeProvenanceViews(t)
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := makeProvRepo(t, filepath.Join(work, "repo"), "sha1")
	_, dirs := provBaseDirs(t)
	names := []string{"git-unbound", "git-two-states", "git-sample-self-incident", "git-cjk-overlays"}
	ci := provGoodCI(repo)
	good := warmProvenanceBases(t, c, names, dirs, ci)
	r := rand.New(rand.NewPCG(14, 600))
	var outputs strings.Builder
	valid := 0
	for i := 0; i < 600; i++ {
		name := names[r.IntN(len(names))]
		bundle := dirs[name]
		receiptName := name
		if r.IntN(12) == 0 {
			receiptName = names[r.IntN(len(names))]
		}
		receipt := parseOrFatal(t, good[receiptName])
		trust := provTrust(ci, r.IntN(2) == 0, "root.json", r.IntN(2) == 0)
		switch k := r.IntN(10); {
		case k < 5:
			receipt = mutateProvReceipt(r, receipt)
		case k < 8:
			trust = mutateProvTrust(r, trust)
		case k == 8: // both
			receipt = mutateProvReceipt(r, receipt)
			trust = mutateProvTrust(r, trust)
		}
		if r.IntN(10) == 0 && receipt.Kind() == jsjson.Object { // both schemas fail
			receipt.Obj().Delete("ci")
			trust = jsjson.MakeNull()
		}
		if r.IntN(40) == 0 {
			bundle = filepath.Join(work, "missing-bundle")
		}
		args := jsjson.Stringify(jsjson.MustObject("bundle", vstr(bundle), "receipt", receipt, "trust", trust))
		want := oracleText(t, c, "verifyGithubProvenanceBinding", args)
		outputs.WriteString(want + "\n")
		var got string
		if err := jsexc.Try(func() { got = jsjson.Stringify(provenance.VerifyBinding(bundle, receipt, trust)) }); err != nil {
			got = "THREW:" + jsexc.Message(err)
		}
		if got != want {
			t.Fatalf("case %d (%s):\ngot  %s\nwant %s", i, args, got, want)
		}
		if strings.HasPrefix(want, `{"valid":true`) {
			valid++
		}
	}
	requireProvReached(t, outputs.String(),
		"GitHub provenance receipt schema validation failed: ", "GitHub attestation trust schema validation failed: ",
		`"errors":["GitHub provenance receipt schema validation failed: `+"[", "\",\"GitHub attestation trust schema validation failed: ",
		"receiptDigest does not match the canonical provenance receipt contents",
		"receipt proof facts do not exactly match the verified Git proof bundle",
		"receipt repository is not permitted by the trust configuration", "receipt ref is not permitted by the trust configuration",
		"receipt workflow reference is not the configured signer workflow at the configured source ref",
		"receipt source commit does not match the configured source digest",
		"GitHub provenance binding verification failed safely: ", `{"valid":true,`, `"proofRootDigest":null,"receipt":`)
	liveSummary(t, "provenance binding: 600 cases, %d valid", valid)
}

// provFakeGh is the scripted gh: it appends its argv (one argument per
// line, then "--") to log beside it and behaves as the mode file says.
// Only shell builtins are used, so PATH need not hold anything else.
const provFakeGh = `#!/bin/sh
dir=${0%/*}
for a in "$@"; do printf '%s\n' "$a"; done >> "$dir/log"
printf -- '--\n' >> "$dir/log"
read -r mode < "$dir/mode"
case "$mode" in
pass) printf 'Loaded digest sha256:x\n'; exit 0 ;;
pass-noisy) printf 'ok\n'; printf 'warning\n' >&2; exit 0 ;;
fail) printf 'out\n'; printf 'Error: verifying with issuer "sigstore.dev"\n' >&2; exit 1 ;;
silent) exit 1 ;;
blank) printf '  \n\n'; printf '\t\n' >&2; exit 3 ;;
long) i=0; while [ $i -lt 1500 ]; do printf 'line %d of the gh failure output\n' $i; i=$((i+1)); done; exit 2 ;;
astral) printf 'x'; i=0; while [ $i -lt 4100 ]; do printf '\360\237\230\200'; i=$((i+1)); done; exit 1 ;;
badutf8) printf 'bad \377\376 bytes\n' >&2; exit 1 ;;
signal) kill -TERM $$ ;;
esac
exit 9
`

// TestLiveGhAttestation runs 40 attestation verifications with a scripted
// gh, through verifyGithubArtifactAttestation and (with a bound receipt)
// verifySignedGithubProvenance: gh passing, failing, failing with long,
// blank, astral or invalid UTF-8 output, killed by a signal, missing from
// PATH or not executable; a missing, symlinked or directory trust root; a
// symlinked or missing receipt or attestation bundle; an invalid trust or
// source commit; denySelfHostedRunners true and false. It compares the
// results and the gh argv each side recorded, then reads trust files and
// unreadable receipts.
func TestLiveGhAttestation(t *testing.T) {
	oracle.Skip(t)
	c := oracle.Start(t)
	defer c.Close()
	memoizeProvenanceViews(t)
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := makeProvRepo(t, filepath.Join(work, "repo"), "sha1")
	_, dirs := provBaseDirs(t)
	ci := provGoodCI(repo)
	bundle := dirs["git-two-states"]
	receiptText := warmProvenanceBases(t, c, []string{"git-two-states"}, dirs, ci)["git-two-states"]

	write := func(rel, body string, mode os.FileMode) string {
		p := filepath.Join(work, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	receiptFile := write("receipt.json", receiptText+"\n", 0o600)
	write("att/bundle.sigstore.json", "{}", 0o644)
	write("att/root.json", "{}", 0o644)
	write("att/sub/root.json", "{}", 0o644)
	os.Symlink("root.json", filepath.Join(work, "att", "rootlink.json"))
	os.Mkdir(filepath.Join(work, "att", "rootdir"), 0o755)
	os.Symlink("bundle.sigstore.json", filepath.Join(work, "att", "bundle-link.json"))
	os.Symlink("receipt.json", filepath.Join(work, "receipt-link.json"))
	write("big.json", "{"+strings.Repeat(" ", 1_000_000)+"}", 0o600)
	write("notjson.json", "{\"a\":", 0o600)
	write("badschema.json", "{\"schemaVersion\":1}", 0o600)
	write("bin/gh", provFakeGh, 0o755)
	write("bin/mode", "pass\n", 0o644)
	write("noexec/gh", provFakeGh, 0o644)
	os.MkdirAll(filepath.Join(work, "nogh"), 0o755)
	logFile := filepath.Join(work, "bin", "log")
	readLog := func() string {
		b, _ := os.ReadFile(logFile)
		os.Remove(logFile)
		return string(b)
	}
	savedPath := os.Getenv("PATH")
	t.Setenv("PATH", savedPath)
	withPath := func(path string, f func()) {
		os.Setenv("PATH", path)
		defer os.Setenv("PATH", savedPath)
		f()
	}

	modes := []string{"pass", "pass", "pass-noisy", "fail", "silent", "blank", "long", "astral", "badutf8", "signal"}
	// The first runs are pinned (every one valid but for its own change),
	// the rest are random.
	type ghRun struct {
		signed      bool
		mode        string
		path        string // "", "nogh" or "noexec"
		receiptFile string // relative to work, "" for the bound receipt
		otherRepo   bool   // a trust that does not permit the receipt's repository
		commit      string // the source commit gh is given, "" for the receipt's
	}
	pinned := []ghRun{
		{true, "pass", "", "", false, ""}, {false, "pass", "", "", false, ""}, {true, "fail", "", "", false, ""}, {false, "fail", "nogh", "", false, ""},
		{true, "pass", "nogh", "", false, ""}, {false, "pass", "noexec", "", false, ""}, {true, "pass", "", "notjson.json", false, ""},
		{true, "pass", "", "big.json", false, ""}, {true, "pass", "", "badschema.json", false, ""}, {true, "long", "", "", false, ""}, {true, "pass", "", "", true, ""},
		{false, "pass", "", "receipt-link.json", false, ""}, {false, "pass", "", "", false, "abc"},
		{false, "fail", "", "missing.json", false, ""}, {false, "pass", "", "", false, "nul"},
	}
	r := rand.New(rand.NewPCG(14, 40))
	var outputs strings.Builder
	passed := 0
	for i := 0; i < 40; i++ {
		run := ghRun{signed: i%4 == 3, mode: modes[i%len(modes)]}
		random := i >= len(pinned)
		if !random {
			run = pinned[i]
		} else {
			switch r.IntN(8) {
			case 0:
				run.path = "nogh"
			case 1:
				run.path = "noexec"
			}
		}
		mode := run.mode
		write("bin/mode", mode+"\n", 0o644)
		path := filepath.Join(work, "bin")
		if run.path != "" {
			path = filepath.Join(work, run.path)
		}
		rootFile := "root.json"
		if random {
			rootFile = []string{"root.json", "root.json", filepath.Join(work, "att", "root.json"), "sub/root.json", "../att/root.json",
				"missing.json", "rootlink.json", "rootdir"}[r.IntN(8)]
		}
		if run.commit == "nul" { // lstatSync's path validation
			rootFile, run.commit = "root\u0000.json", ""
		}
		trust := provTrust(ci, i%2 == 0, rootFile, random && r.IntN(2) == 0)
		if run.otherRepo {
			trust.Obj().Set("repository", vstr("other/repo"))
		}
		receiptArg := receiptFile
		if run.receiptFile != "" {
			receiptArg = filepath.Join(work, run.receiptFile)
		}
		bundleArg := filepath.Join(work, "att", "bundle.sigstore.json")
		commit := repo.head
		if run.commit != "" {
			commit = run.commit
		}
		if random {
			switch r.IntN(10) {
			case 0:
				receiptArg = filepath.Join(work, "receipt-link.json")
			case 1:
				receiptArg = filepath.Join(work, "missing.json")
			case 2:
				bundleArg = filepath.Join(work, "att", "bundle-link.json")
			case 3:
				bundleArg = filepath.Join(work, "att", "missing.json")
			case 4:
				commit = []string{"abc", strings.ToUpper(repo.head)}[r.IntN(2)]
			case 5:
				trust = mutateProvTrust(r, trust)
			}
		}
		var op, args string
		var goCall func() jsjson.Value
		if run.signed { // the full path: the receipt read, its binding, then gh
			if random && r.IntN(3) == 0 {
				receiptArg = filepath.Join(work, []string{"big.json", "notjson.json", "badschema.json", "receipt-link.json", "missing.json"}[r.IntN(5)])
			}
			op = "verifySignedGithubProvenance"
			args = jsjson.Stringify(jsjson.MustObject("bundle", vstr(bundle), "receiptFile", vstr(receiptArg),
				"attestationBundleFile", vstr(bundleArg), "trust", trust, "path", vstr(path)))
			goCall = func() jsjson.Value { return provenance.VerifySigned(bundle, receiptArg, bundleArg, trust) }
		} else {
			op = "verifyGithubArtifactAttestation"
			args = jsjson.Stringify(jsjson.MustObject("receiptFile", vstr(receiptArg), "bundleFile", vstr(bundleArg),
				"trust", trust, "sourceCommit", vstr(commit), "path", vstr(path)))
			goCall = func() jsjson.Value { return provenance.VerifyArtifactAttestation(receiptArg, bundleArg, trust, commit) }
		}
		readLog()
		want := oracleText(t, c, op, args)
		wantLog := readLog()
		outputs.WriteString(want + "\n")
		var got string
		withPath(path, func() { got = jsjson.Stringify(goCall()) })
		gotLog := readLog()
		if got != want {
			t.Fatalf("run %d (%s, mode %s):\ngot  %s\nwant %s", i, op, mode, got, want)
		}
		if gotLog != wantLog {
			t.Fatalf("run %d (%s, mode %s): gh argv:\ngot  %q\nwant %q", i, op, mode, gotLog, wantLog)
		}
		if strings.Contains(want, `"valid":true,"command"`) {
			passed++
		}
	}
	// Trust files: regular, symlinked, too big, not JSON, schema failures.
	trustBody := jsjson.Stringify(provTrust(ci, true, " root.json ", true))
	for _, f := range []string{
		write("trust.json", trustBody, 0o600), filepath.Join(work, "trust-link.json"), filepath.Join(work, "big.json"),
		filepath.Join(work, "notjson.json"), filepath.Join(work, "badschema.json"), filepath.Join(work, "missing.json"), filepath.Join(work, "att"),
		write("trust-bad-utf8.json", "{\"a\":\"\xff\"}", 0o600), write("trust-extra.json", trustBody[:len(trustBody)-1]+",\"x\":1}", 0o600),
	} {
		if f == filepath.Join(work, "trust-link.json") {
			os.Symlink("trust.json", f)
		}
		args := jsjson.Stringify(jsjson.MustObject("path", vstr(f)))
		want := oracleText(t, c, "readGithubArtifactAttestationTrust", args)
		outputs.WriteString(want + "\n")
		trust, err := provenance.ReadTrust(f)
		got := "THREW:"
		if err != nil {
			got += jsexc.Message(err)
		} else {
			got = jsjson.Stringify(trust)
		}
		if got != want {
			t.Fatalf("readTrust(%s):\ngot  %s\nwant %s", f, got, want)
		}
	}
	requireProvReached(t, outputs.String(),
		"Could not run gh: spawnSync gh ENOENT", "Could not run gh: spawnSync gh EACCES",
		"GitHub artifact attestation verification failed: out", `"GitHub artifact attestation verification failed"]`,
		"GitHub artifact attestation verification failed safely: ", "GitHub trusted root must be a regular non-symlink file",
		"GitHub provenance receipt must be a regular non-symlink file", "GitHub artifact attestation bundle must be a regular non-symlink file",
		"ENOENT: no such file or directory, lstat ", "expected a Git object id",
		"GitHub provenance receipt could not be read safely: ", "artifact attestation was not attempted",
		"GitHub provenance receipt exceeds the 1000000-byte limit", "GitHub provenance receipt is not valid JSON: ",
		"artifact attestation was not attempted because receipt binding is invalid", "GITHUB_ARTIFACT_ATTESTATION_VERIFIED",
		"--deny-self-hosted-runners", "GitHub attestation trust configuration must be a regular non-symlink file",
		"The argument 'path' must be a string, Uint8Array, or URL without null bytes. Received ",
		"GitHub attestation trust configuration exceeds the 1000000-byte limit", "GitHub attestation trust configuration is not valid JSON: ")
	liveSummary(t, "gh attestation: 40 runs, %d passed", passed)
}

// TestLiveProvenanceWrite writes receipts in 120 generated layouts with
// writeGithubProvenanceReceipt and WriteReceipt: the default output, a
// nested one (0700 directories), one outside the root or equal to it; a
// symlinked root, .faultline or output directory; a file in the path; an
// existing output (a file, a directory, a symlink, a dangling symlink,
// which the wx open refuses with EEXIST); a read-only directory; an
// invalid receipt; a second write; relative paths from a cwd. It compares
// each result and the workspace trees.
func TestLiveProvenanceWrite(t *testing.T) {
	oracle.Skip(t)
	c := oracle.Start(t)
	defer c.Close()
	memoizeProvenanceViews(t)
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := makeProvRepo(t, filepath.Join(work, "repo"), "sha1")
	_, dirs := provBaseDirs(t)
	receiptText := warmProvenanceBases(t, c, []string{"git-two-states"}, dirs, provGoodCI(repo))["git-two-states"]
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	r := rand.New(rand.NewPCG(14, 120))
	var outputs strings.Builder
	written := 0
	const rootRel = ".faultline/provenance"
	for i := 0; i < 120; i++ {
		ts, gs := filepath.Join(work, "ts", strconv.Itoa(i)), filepath.Join(work, "go", strconv.Itoa(i))
		receipt := parseOrFatal(t, receiptText)
		output := rootRel + "/ci-receipt.json"
		root := rootRel
		useCwd := false
		writes := 1
		var setup func(dir string)
		layout := r.IntN(20)
		badDigest := r.IntN(2) == 0
		if i == 0 { // pinned: a receipt the write's schema parse refuses
			layout, badDigest = 14, true
		}
		switch layout {
		case 0, 1:
		case 2:
			output = rootRel + "/team/a/b/receipt.json"
		case 3:
			output = "receipt.json"
		case 4:
			output = rootRel
		case 5:
			output = rootRel + "/../provenance2/x.json"
		case 6:
			setup = func(dir string) {
				os.MkdirAll(filepath.Join(dir, ".faultline", "real"), 0o755)
				os.Symlink("real", filepath.Join(dir, rootRel))
			}
		case 7:
			setup = func(dir string) {
				os.MkdirAll(filepath.Join(dir, "elsewhere"), 0o755)
				os.Symlink("elsewhere", filepath.Join(dir, ".faultline"))
			}
		case 8:
			setup = func(dir string) { os.WriteFile(filepath.Join(dir, ".faultline"), []byte("x"), 0o644) }
		case 9:
			output = rootRel + "/link/r.json"
			setup = func(dir string) {
				os.MkdirAll(filepath.Join(dir, rootRel), 0o755)
				os.Symlink("..", filepath.Join(dir, rootRel, "link"))
			}
		case 10:
			output = rootRel + "/file/r.json"
			setup = func(dir string) {
				os.MkdirAll(filepath.Join(dir, rootRel), 0o755)
				os.WriteFile(filepath.Join(dir, rootRel, "file"), []byte("x"), 0o644)
			}
		case 11, 12:
			kind := r.IntN(4)
			setup = func(dir string) {
				os.MkdirAll(filepath.Join(dir, rootRel), 0o755)
				p := filepath.Join(dir, rootRel, "ci-receipt.json")
				switch kind {
				case 0:
					os.WriteFile(p, []byte("{}"), 0o644)
				case 1:
					os.Mkdir(p, 0o755)
				case 2:
					os.WriteFile(filepath.Join(dir, "target.json"), []byte("{}"), 0o644)
					os.Symlink("../../target.json", p)
				case 3: // dangling: existsSync is false, the wx open gives EEXIST
					os.Symlink("../../nowhere.json", p)
				}
			}
		case 13:
			setup = func(dir string) {
				os.MkdirAll(filepath.Join(dir, rootRel), 0o755)
				os.Chmod(filepath.Join(dir, rootRel), 0o555)
			}
		case 14:
			if badDigest {
				receipt.Obj().Set("receiptDigest", vstr("sha256:short"))
			} else {
				receipt = mutateProvReceipt(r, receipt)
			}
		case 15:
			writes = 2
		case 16:
			useCwd = true
		case 17:
			useCwd = true
			output = "nested/r.json"
			root = "nested"
		case 18: // a lone surrogate the UTF-8 encoding replaces
			receipt.Get("ci").Obj().Set("workflowRef", vstr(provWorkflow+loneSurrogate))
		case 19:
			root = rootRel + "/sub"
			output = rootRel + "/sub/x/y.json"
		}
		for _, dir := range []string{ts, gs} {
			os.MkdirAll(dir, 0o755)
			if setup != nil {
				setup(dir)
			}
		}
		abs := func(dir, p string) string {
			if useCwd {
				return p
			}
			return filepath.Join(dir, p)
		}
		for w := 0; w < writes; w++ {
			o := jsjson.NewObj()
			if useCwd {
				o.Set("cwd", vstr(ts))
			}
			o.Set("output", vstr(abs(ts, output)))
			o.Set("receipt", receipt)
			o.Set("root", vstr(abs(ts, root)))
			want := strings.ReplaceAll(oracleText(t, c, "writeGithubProvenanceReceipt", jsjson.Stringify(jsjson.MakeObject(o))), ts, gs)
			outputs.WriteString(want + "\n")
			if useCwd {
				if err := os.Chdir(gs); err != nil {
					t.Fatal(err)
				}
			}
			path, err := provenance.WriteReceipt(abs(gs, output), receipt, abs(gs, root))
			os.Chdir(cwd)
			got := path
			if err != nil {
				got = "THREW:" + jsexc.Message(err)
			} else {
				written++
			}
			if got != want {
				t.Fatalf("case %d (layout %d) write %d:\ngot  %s\nwant %s", i, layout, w, got, want)
			}
		}
		a, b := attestTree(gs), attestTree(ts)
		unlockTree(ts)
		unlockTree(gs)
		if a != b {
			t.Fatalf("case %d (layout %d): workspace trees differ:\ngo\n%s\nts\n%s", i, layout, a, b)
		}
	}
	requireProvReached(t, outputs.String(),
		"GitHub provenance output must be a file beneath ", "Provenance output cannot traverse a symbolic link or non-directory: ",
		"GitHub provenance receipt already exists and will not be replaced: ", "EACCES: permission denied, open ",
		"expected sha256:<64 lowercase hex characters>")
	liveSummary(t, "provenance write: 120 layouts, %d written", written)
}
