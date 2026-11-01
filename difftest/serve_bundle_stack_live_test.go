//go:build faultline_test

package difftest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/difftest/servefixture"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// Slice 5c Task 5A, phase A: the V8 stack model (internal/jsjson/stack.go) has
// never been measured through `serve --bundle`. This test drives the ACTUAL
// frozen TS CLI (`node dist/cli.js serve --bundle ...`) and the actual Go fl
// with the retained argv of case cli-serve-111 (all three attachments, with
// their retained expected digests) on fresh copies of the retained coherent
// fixture. For each of six schema-literal sites it replaces the one
// schemaVersion literal with a nested value of each of the four stack shapes
// (the constructors and the 500..12000 bisection of stack_live_test.go) so the
// verifier's zod message must be stringified at depth. The artifact is NOT
// rehashed and the retained expected digests are NOT changed, so a schema
// failure must beat the digest mismatch the edit also causes.
//
// Select one site and shape (anchored) for a bounded run:
//
//	-run '^TestLiveServeBundleStackCallSites$/^repair-brief$/^obj$'
//
// Sites: minimization, repair-manifest, repair-packet, repair-brief,
// prevention-manifest, prevention-body, and (review I2) prevention-repaired-runs,
// which probes the verified package's repaired-runs.json through the retained
// case view-prevention-verified's prevention directory and digest in the same
// cli-serve-111 argv. Shapes: arr, obj, idx, alt.
//
// Nothing here tolerates a difference. Both the printed schema failure and the
// caught stack-overflow diagnostic are ordinary completed CLI refusals (exit 1,
// empty stdout, one "FaultLine error: Refusing to attach ..." line group and
// the "Next: pnpm fl help" line), so the COMPLETE stdout, stderr and exit
// status of the two sides are compared byte for byte after the declared {ROOT}
// normalization, wherever a comparison is made. The last printing depth must be
// equal, and the sides must agree at and just past each side's last printing
// depth. Output that is neither a recognised printed schema failure nor the one
// measured caught overflow form fails with the complete output, naming the
// site, shape and depth, so the controller can capture it; it is never read as
// an overflow.
func TestLiveServeBundleStackCallSites(t *testing.T) {
	oracle.Skip(t) // first: explicit oracle enablement, nothing skips after it
	node, cli := pinServeBundleOracle(t)
	corpus := loadServeBundleCorpus(t)
	c := serveBundleFindCase(t, corpus, "cli-serve-111")
	sides := [2]liveSide{{"TS", node, []string{cli}}, {"Go", flBinary, nil}}
	t.Logf("platform %s/%s, node %s, cli %s, case %s", runtime.GOOS, runtime.GOARCH, node, cli, c.ID)

	base := c
	for _, site := range serveStackSites {
		c := serveStackCaseFor(t, corpus, base, site) // the site's retained case (cli-serve-111 unless it names another)
		t.Run(site.name, func(t *testing.T) {
			var tsLasts []int
			var shapeFns []func(int) string
			siteDiffers := false
			for _, shape := range serveStackShapeOrder {
				t.Run(shape, func(t *testing.T) {
					build := serveStackShapes[shape]
					probe := func(side liveSide, n int) serveStackOutcome {
						return runServeStackProbe(t, corpus, c, site, build, side, n)
					}
					classify := func(side liveSide, n int, o serveStackOutcome) string {
						return classifyServeStack(t, site, shape, n, side.name, o)
					}
					compare := func(n int) string {
						return compareServeStack(t, site, shape, n, probe(sides[0], n), probe(sides[1], n))
					}

					// Endpoints first: a bisection with an unestablished
					// endpoint would infer a threshold from nothing.
					for _, end := range []struct {
						n    int
						want string
						why  string
					}{
						{serveStackLo, serveStackPrinted, "no schema failure reached, nothing to bisect"},
						{serveStackHi, serveStackOverflow, "ceiling does not overflow, nothing to bisect"},
					} {
						outs := [2]serveStackOutcome{probe(sides[0], end.n), probe(sides[1], end.n)}
						for i, side := range sides {
							if class := classify(side, end.n, outs[i]); class != end.want {
								t.Fatalf("%s depth %d: class %s, want %s (%s)\nexit=%d stdout %q\nstderr %q", side.name, end.n, class, end.want, end.why, outs[i].exit, outs[i].stdout, outs[i].stderr)
							}
						}
						if outs[0] != outs[1] {
							t.Errorf("%s/%s depth %d: complete process results differ\nTS exit=%d stdout %q\nstderr %q\nGo exit=%d stdout %q\nstderr %q",
								site.name, shape, end.n, outs[0].exit, outs[0].stdout, outs[0].stderr, outs[1].exit, outs[1].stdout, outs[1].stderr)
						}
					}

					bisect := func(side liveSide) int {
						lo, hi := serveStackLo, serveStackHi
						for hi-lo > 1 {
							m := (lo + hi) / 2
							if classify(side, m, probe(side, m)) == serveStackPrinted {
								lo = m
							} else {
								hi = m
							}
						}
						return lo
					}
					ts, goLast := bisect(sides[0]), bisect(sides[1])
					t.Logf("%s/%s node-oracle site=%s shape=%s argv=%s: TS last printing depth %d, Go %d",
						runtime.GOOS, runtime.GOARCH, site.name, shape, serveStackArgvLine(t, corpus, c), ts, goLast)
					tsLasts, shapeFns = append(tsLasts, ts), append(shapeFns, build)
					if ts != goLast {
						siteDiffers = true
						t.Errorf("last printing depth differs: TS %d, Go %d", ts, goLast)
					}

					// The transition itself, on both sides' last printing depth
					// and the next one: the complete stdout, stderr and exit of
					// the two sides must be identical, printed or overflow.
					depths := []int{ts, ts + 1}
					for _, d := range []int{goLast, goLast + 1} {
						if !slices.Contains(depths, d) {
							depths = append(depths, d)
						}
					}
					for _, d := range depths {
						if msg := compare(d); msg != "" {
							t.Errorf("boundary depth %d (TS last %d, Go last %d): %s", d, ts, goLast, msg)
						}
					}
				})
			}
			// Offsets that would reproduce TS on all four shapes, logged only
			// when the site differs and all four shapes were measured in this
			// run; nothing here applies a number to production.
			if siteDiffers && len(tsLasts) == len(serveStackShapeOrder) {
				if lo, hi, ok := jsjson.FitSiteOffsetForTest(shapeFns, tsLasts); ok {
					t.Logf("%s/%s %s refit: site offsets %d to %d reproduce TS", runtime.GOOS, runtime.GOARCH, site.name, lo, hi)
				} else {
					t.Logf("%s/%s %s refit: no site offset reproduces TS on all four shapes", runtime.GOOS, runtime.GOARCH, site.name)
				}
			}
		})
	}
}

const (
	serveStackLo = 500
	serveStackHi = 12000

	serveStackPrinted  = "printed"
	serveStackOverflow = "overflow"
	serveStackUnknown  = "unknown"

	serveStackOverflowText = "Maximum call stack size exceeded"
)

// serveStackSite is one independently selectable schema-literal site of the
// cli-serve-111 inputs. The edited text is `"schemaVersion":` + literal +
// suffix; suffix disambiguates the minimization record's top-level literal
// from the per-run literals inside it. It must occur exactly once.
//
// refusal is the attachment-refusal prefix of that artifact kind (golden.jsonl
// cli-reject-view-* rows). label is the schema-validation lead-in the verifier
// puts before zod's message and tail is what follows that message before the
// "Next:" line. overflow is the COMPLETE stderr of the verifier's caught
// stack overflow. Provenance (not measurements unless stated):
//   - minimization label/tail/overflow: measured by the controller's actual
//     linux/arm64 probe (depth 500 printed form, depth 12000 overflow).
//   - repair labels and overflow: frozen src/repair-brief-store.ts lines 221,
//     239, 242 and catch at 255 via src/incident-attachments.ts loadRepair.
//   - prevention labels and overflow: frozen src/prevention-proof.ts lines 329,
//     346 and catch at 373 via src/incident-attachments.ts loadPrevention.
//
// The actual CLI comparison still proves the complete bytes or fails loudly.
//
// preventionCase, when set, names a retained case whose prevention directory and
// expected prevention digest replace cli-serve-111's (summary) pair in the argv,
// so the site is reached through that package: used for the verified package,
// which alone holds repaired-runs.json. The path and digest are read from that
// retained case; nothing is invented, rehashed or altered.
type serveStackSite struct {
	name, file, literal, suffix string
	refusal, label, tail        string
	overflow                    string
	preventionCase              string
}

func (s serveStackSite) marker() string { return `"schemaVersion":` + s.literal + s.suffix }

const (
	serveStackNext = "\nNext: pnpm fl help\n"
	serveStackFail = "FaultLine error: "
)

var serveStackSites = []serveStackSite{
	{name: "minimization", file: "minimization.json", literal: `"faultline.git-minimization.v1"`, suffix: `,"status":"COMPLETED"`,
		refusal:  serveStackFail + "Refusing to attach an invalid minimization record: ",
		label:    "minimization result schema validation failed: ",
		tail:     "; externally supplied minimization digest does not match",
		overflow: serveStackFail + "Refusing to attach an invalid minimization record: minimization result verification failed safely: Maximum call stack size exceeded; externally supplied minimization digest does not match" + serveStackNext},
	{name: "repair-manifest", file: ".faultline/repair-briefs/attached/manifest.json", literal: `"faultline.repair-brief-artifact.v1"`,
		refusal: serveStackFail + "Refusing to attach an invalid repair brief: ", label: "Repair brief manifest schema validation failed: ",
		overflow: serveStackFail + "Refusing to attach an invalid repair brief: Repair brief package verification failed safely: Maximum call stack size exceeded" + serveStackNext},
	{name: "repair-packet", file: ".faultline/repair-briefs/attached/evidence-packet.json", literal: `"faultline.repair-evidence.v2"`,
		refusal: serveStackFail + "Refusing to attach an invalid repair brief: ", label: "Repair evidence packet schema validation failed: ",
		overflow: serveStackFail + "Refusing to attach an invalid repair brief: Repair brief package verification failed safely: Maximum call stack size exceeded" + serveStackNext},
	{name: "repair-brief", file: ".faultline/repair-briefs/attached/repair-brief.json", literal: `"faultline.repair-brief.v1"`,
		refusal: serveStackFail + "Refusing to attach an invalid repair brief: ", label: "Repair brief schema validation failed: ",
		overflow: serveStackFail + "Refusing to attach an invalid repair brief: Repair brief package verification failed safely: Maximum call stack size exceeded" + serveStackNext},
	{name: "prevention-manifest", file: ".faultline/prevention-proofs/summary/manifest.json", literal: `"faultline.prevention-proof.v1"`,
		refusal: serveStackFail + "Refusing to attach an invalid prevention proof: ", label: "Prevention proof manifest schema validation failed: ",
		overflow: serveStackFail + "Refusing to attach an invalid prevention proof: Prevention proof verification failed safely: Maximum call stack size exceeded" + serveStackNext},
	{name: "prevention-body", file: ".faultline/prevention-proofs/summary/prevention.json", literal: `"faultline.prevention-proof.v1"`,
		refusal: serveStackFail + "Refusing to attach an invalid prevention proof: ", label: "Prevention proof body schema validation failed: ",
		overflow: serveStackFail + "Refusing to attach an invalid prevention proof: Prevention proof verification failed safely: Maximum call stack size exceeded" + serveStackNext},
	// Review I2: the repaired-runs.json message of the verified package, read by
	// frozen src/prevention-proof.ts (the "repaired-runs.json schema validation
	// failed:" branch, same function and catch as the manifest and body) and by
	// prevention/verify.go's repaired-runs branch. Label and overflow are
	// source-derived, not measured: the first actual run decides. Only the
	// repaired-runs schema fails (digests and the manifest root are unchanged),
	// so no tail follows the message.
	{name: "prevention-repaired-runs", file: ".faultline/prevention-proofs/verified/repaired-runs.json", literal: `"faultline.prevention-repaired-runs.v1"`,
		preventionCase: "view-prevention-verified",
		refusal:        serveStackFail + "Refusing to attach an invalid prevention proof: ", label: "repaired-runs.json schema validation failed: ",
		overflow: serveStackFail + "Refusing to attach an invalid prevention proof: Prevention proof verification failed safely: Maximum call stack size exceeded" + serveStackNext},
}

// serveStackCaseFor returns the case a site probes: base itself, or a copy whose
// prevention directory argument and expected prevention digest argument are
// exactly replaced by those of the site's named retained case. The retained
// case must supply both, and base's must each be found exactly once.
func serveStackCaseFor(t *testing.T, corpus *servefixture.Corpus, base servefixture.Case, site serveStackSite) servefixture.Case {
	t.Helper()
	if site.preventionCase == "" {
		return base
	}
	source := serveBundleFindCase(t, corpus, site.preventionCase)
	o, b := source.Options, base.Options
	if o.PreventionDirectory == nil || o.ExpectedPreventionDigest == nil || b.PreventionDirectory == nil || b.ExpectedPreventionDigest == nil {
		t.Fatalf("%s: %s or %s lacks a prevention directory and digest", site.name, source.ID, base.ID)
	}
	oldDirectory, oldDigest := "{ROOT}/"+*b.PreventionDirectory, *b.ExpectedPreventionDigest
	argv := slices.Clone(base.Argv)
	for from, to := range map[string]string{oldDirectory: "{ROOT}/" + *o.PreventionDirectory, oldDigest: *o.ExpectedPreventionDigest} {
		at := slices.Index(argv, from)
		if at < 0 || slices.Index(argv[at+1:], from) >= 0 {
			t.Fatalf("%s: %q must appear exactly once in %s's argv %q", site.name, from, base.ID, base.Argv)
		}
		argv[at] = to
	}
	out := base
	out.Argv = argv
	out.Options.PreventionDirectory, out.Options.ExpectedPreventionDigest = o.PreventionDirectory, o.ExpectedPreventionDigest
	return out
}

var serveStackShapeOrder = []string{"arr", "obj", "idx", "alt"}

// The four shape constructors of stack_live_test.go (TestLiveStackThresholds-
// MatchNode), restated because that test keeps them function-local.
var serveStackShapes = map[string]func(n int) string{
	"arr": func(n int) string { return strings.Repeat("[", n) + strings.Repeat("]", n) },
	"obj": func(n int) string { return strings.Repeat(`{"a":`, n) + "0" + strings.Repeat("}", n) },
	"idx": func(n int) string { return strings.Repeat(`{"0":`, n) + "0" + strings.Repeat("}", n) },
	"alt": func(n int) string {
		var open, closing strings.Builder
		for i := range n {
			if i%2 == 1 {
				open.WriteString(`{"a":`)
			} else {
				open.WriteString("[")
			}
		}
		for i := n - 1; i >= 0; i-- {
			if i%2 == 1 {
				closing.WriteString("}")
			} else {
				closing.WriteString("]")
			}
		}
		return open.String() + "0" + closing.String()
	},
}

type serveStackOutcome struct {
	stdout, stderr string
	exit           int
}

// runServeStackProbe copies the retained coherent fixture, replaces the one
// schema literal of site with build(n) (no rehash; the retained expected digests
// stay in the argv) and runs the side's real serve CLI to exit. Streams are
// normalized only by {ROOT}.
func runServeStackProbe(t *testing.T, corpus *servefixture.Corpus, c servefixture.Case, site serveStackSite, build func(int) string, side liveSide, n int) serveStackOutcome {
	t.Helper()
	root := liveCopy(t, corpus, c)
	path := filepath.Join(root, filepath.FromSlash(site.file))
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	marker := site.marker()
	if got := strings.Count(string(original), marker); got != 1 {
		t.Fatalf("%s: %s holds %d copies of %s, want exactly 1", site.name, site.file, got, marker)
	}
	edited := strings.Replace(string(original), marker, strings.Replace(marker, site.literal, build(n), 1), 1)
	if edited == string(original) {
		t.Fatalf("%s: depth %d edit changed nothing", site.name, n)
	}
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	argv, err := corpus.ResolveArgv(c.ID, root, 0, c.Argv)
	if err != nil {
		t.Fatal(err)
	}
	// The retained digests must be the ones the command is given, so a digest
	// error is available to (wrongly) win over the schema failure.
	for _, digest := range []*string{c.Options.ExpectedMinimizationDigest, c.Options.ExpectedRepairDigest, c.Options.ExpectedPreventionDigest} {
		if digest == nil || !slices.Contains(argv, *digest) {
			t.Fatalf("%s: argv %q lacks a retained expected digest", c.ID, argv)
		}
	}
	stdout, stderr, exit := runLiveToExit(t, side, root, argv)
	return serveStackOutcome{serveBundleNormalize(stdout, root), serveBundleNormalize(stderr, root), exit}
}

func serveStackArgvLine(t *testing.T, corpus *servefixture.Corpus, c servefixture.Case) string {
	t.Helper()
	argv, err := corpus.ResolveArgv(c.ID, "{ROOT}", 0, c.Argv)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(argv, " ")
}

// classifyServeStack names what a probe produced. Anything other than the two
// recognised ordinary refusals fails the test with the complete, untruncated
// output and the site, shape, depth and side: a missing substring is never
// read as an outcome.
func classifyServeStack(t *testing.T, site serveStackSite, shape string, n int, side string, o serveStackOutcome) string {
	t.Helper()
	class := serveStackClass(site, o)
	if class == serveStackUnknown {
		t.Fatalf("%s/%s depth %d, %s: unrecognised output, neither the printed schema-literal refusal nor the measured caught overflow refusal\nexit=%d\nstdout %q\nstderr %q", site.name, shape, n, side, o.exit, o.stdout, o.stderr)
	}
	return class
}

// serveStackClass accepts only a completed refusal: exit 1, empty stdout, and a
// stderr that is exactly one refusal for the site's artifact kind ending in the
// "Next: pnpm fl help" line.
//
//   - overflow: stderr equals the site's measured caught-overflow text exactly.
//   - printed: stderr is exactly refusal + label + zod message + tail + Next,
//     where the zod message parses as a JSON array of exactly one issue whose
//     members are exactly received (present, any value), code
//     "invalid_literal", expected the site's literal, path ["schemaVersion"]
//     and the matching zod message. Member order is not constrained (the
//     measured form lists received first). Parsing uses encoding/json, whose
//     10000-level nesting limit exceeds every depth that can print; a message
//     that does not parse is unrecognised, not printed.
func serveStackClass(site serveStackSite, o serveStackOutcome) string {
	if o.exit != 1 || o.stdout != "" || !strings.HasSuffix(o.stderr, serveStackNext) {
		return serveStackUnknown
	}
	if site.overflow != "" && o.stderr == site.overflow {
		return serveStackOverflow
	}
	head := site.refusal + site.label
	rest, ok := strings.CutPrefix(o.stderr, head)
	if !ok {
		return serveStackUnknown
	}
	message, ok := strings.CutSuffix(rest, site.tail+serveStackNext)
	if !ok {
		return serveStackUnknown
	}
	var issues []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(message), &issues); err != nil || len(issues) != 1 {
		return serveStackUnknown
	}
	issue := issues[0]
	if len(issue) != 5 {
		return serveStackUnknown
	}
	for _, key := range []string{"received", "code", "expected", "path", "message"} {
		if _, present := issue[key]; !present {
			return serveStackUnknown
		}
	}
	var code, expected, msg string
	var path []string
	literal := strings.Trim(site.literal, `"`)
	if json.Unmarshal(issue["code"], &code) != nil || code != "invalid_literal" ||
		json.Unmarshal(issue["expected"], &expected) != nil || expected != literal ||
		json.Unmarshal(issue["path"], &path) != nil || !slices.Equal(path, []string{"schemaVersion"}) ||
		json.Unmarshal(issue["message"], &msg) != nil || msg != `Invalid literal value, expected "`+literal+`"` {
		return serveStackUnknown
	}
	return serveStackPrinted
}

// Catches a classifier that accepts the wrong thing: the measured received-first
// printed form (not a member-adjacency form), an unrelated issue, path, literal
// or member set, a nonzero-but-wrong status, stdout, a different refusal, and an
// overflow that is merely a substring of other output. The overflow text is
// the one the controller measured for minimization.
func TestServeBundleStackClassifierControls(t *testing.T) {
	min, repair := serveStackSites[0], serveStackSites[3]
	issue := func(literal, path, expected, received string) string {
		return "[\n  {\n    \"received\": " + received + ",\n    \"code\": \"invalid_literal\",\n    \"expected\": " + expected +
			",\n    \"path\": [\n      " + path + "\n    ],\n    \"message\": \"Invalid literal value, expected \\\"" + literal + "\\\"\"\n  }\n]"
	}
	good := issue("faultline.git-minimization.v1", `"schemaVersion"`, `"faultline.git-minimization.v1"`, "[]")
	printedMin := min.refusal + min.label + good + min.tail + serveStackNext
	goodRepair := issue("faultline.repair-brief.v1", `"schemaVersion"`, `"faultline.repair-brief.v1"`, `{"a":0}`)
	printedRepair := repair.refusal + repair.label + goodRepair + serveStackNext
	cases := []struct {
		name string
		site serveStackSite
		out  serveStackOutcome
		want string
	}{
		{"min received-first printed", min, serveStackOutcome{"", printedMin, 1}, serveStackPrinted},
		{"repair printed", repair, serveStackOutcome{"", printedRepair, 1}, serveStackPrinted},
		{"min overflow", min, serveStackOutcome{"", min.overflow, 1}, serveStackOverflow},
		{"repair overflow", repair, serveStackOutcome{"", repair.overflow, 1}, serveStackOverflow},
		{"wrong exit", min, serveStackOutcome{"", printedMin, 2}, serveStackUnknown},
		{"exit zero", min, serveStackOutcome{"", printedMin, 0}, serveStackUnknown},
		{"stdout present", min, serveStackOutcome{"x", printedMin, 1}, serveStackUnknown},
		{"overflow text in other output", min, serveStackOutcome{"", serveStackFail + "boom: " + serveStackOverflowText + serveStackNext, 1}, serveStackUnknown},
		{"overflow with extra text", repair, serveStackOutcome{"", strings.Replace(repair.overflow, "failed safely", "failed safely again", 1), 1}, serveStackUnknown},
		{"other site's overflow", repair, serveStackOutcome{"", min.overflow, 1}, serveStackUnknown},
		{"wrong literal", min, serveStackOutcome{"", min.refusal + min.label + issue("faultline.git-minimization.v1", `"schemaVersion"`, `"faultline.other.v1"`, "[]") + min.tail + serveStackNext, 1}, serveStackUnknown},
		{"wrong path", min, serveStackOutcome{"", min.refusal + min.label + issue("faultline.git-minimization.v1", `"status"`, `"faultline.git-minimization.v1"`, "[]") + min.tail + serveStackNext, 1}, serveStackUnknown},
		{"nested path", min, serveStackOutcome{"", min.refusal + min.label + issue("faultline.git-minimization.v1", `"runs", "schemaVersion"`, `"faultline.git-minimization.v1"`, "[]") + min.tail + serveStackNext, 1}, serveStackUnknown},
		{"missing received", min, serveStackOutcome{"", min.refusal + min.label + strings.Replace(good, "\"received\": [],\n    ", "", 1) + min.tail + serveStackNext, 1}, serveStackUnknown},
		{"two issues", min, serveStackOutcome{"", min.refusal + min.label + "[" + good[1:len(good)-1] + "," + good[1:len(good)-1] + "]" + min.tail + serveStackNext, 1}, serveStackUnknown},
		{"missing tail", min, serveStackOutcome{"", min.refusal + min.label + good + serveStackNext, 1}, serveStackUnknown},
		{"different refusal", min, serveStackOutcome{"", serveStackFail + "Refusing to attach an invalid repair brief: " + min.label + good + min.tail + serveStackNext, 1}, serveStackUnknown},
		{"missing Next", min, serveStackOutcome{"", min.refusal + min.label + good + min.tail, 1}, serveStackUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := serveStackClass(tc.site, tc.out); got != tc.want {
				t.Errorf("class = %s, want %s for exit=%d stdout %q stderr %q", got, tc.want, tc.out.exit, tc.out.stdout, tc.out.stderr)
			}
		})
	}
}

// compareServeStack returns "" when the sides agree at depth n, else a
// description with both complete process results. Printed and overflow
// outcomes alike compare stdout, stderr and exit exactly, per side, in memory.
func compareServeStack(t *testing.T, site serveStackSite, shape string, n int, ts, goSide serveStackOutcome) string {
	t.Helper()
	tc, gc := classifyServeStack(t, site, shape, n, "TS", ts), classifyServeStack(t, site, shape, n, "Go", goSide)
	if tc != gc || ts != goSide {
		return fmt.Sprintf("TS %s, Go %s: complete process results differ\nTS exit=%d stdout %q\nstderr %q\nGo exit=%d stdout %q\nstderr %q",
			tc, gc, ts.exit, ts.stdout, ts.stderr, goSide.exit, goSide.stdout, goSide.stderr)
	}
	return ""
}
