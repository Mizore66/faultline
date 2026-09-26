package jsjson

import "runtime"

// stackModel reproduces where V8's JsonStringifier runs out of native stack
// for zod's ZodError.message (JSON.stringify(issues, replacer, 2)) in
// `node dist/cli.js verify`. Each non-empty array or object pushes one
// frame; objects on V8's slow path (index keys, dictionary mode) cost more.
// A value overflows when the frames above it no longer fit in the budget.
// Costs are relative (a fast object is 10000) and were fitted by bisecting
// the last depth whose zod message still prints, per shape, at the demo
// verifier's analysis.json schemaVersion:
//
//	platform (Node)              []-terminated  {"a":0}  {"0":0}  alternating
//	linux/amd64 (22.22)          2233           4166     2232     2906
//	darwin/arm64 (22.23)         2747           6867     2616     3924
//	linux/arm64 (22.13, glibc)   1781           3560     1907     2373
//
// The budgets include the two frames Go charges for zod's wrapper (the
// issues array and the issue object holding `received`). The JS frames
// below the message read differ per verifier; siteOffset shifts the budget
// for the prevention and git verifiers (measured on linux/amd64: prevention
// has one object level more, git about one less).
//
// Windows and other targets use the Linux model for their architecture;
// they have not been measured (KNOWN_DIFFERENCES.md).
type stackModel struct{ budget, array, object, slowObject int }

var v8Stack = func() stackModel {
	switch {
	case runtime.GOOS == "darwin" && runtime.GOARCH == "arm64":
		return stackModel{budget: 68_672_000 + 25_000 + 10_000, array: 25_000, object: 10_000, slowObject: 26_250}
	case runtime.GOARCH == "arm64":
		return stackModel{budget: 35_605_000 + 20_000 + 10_000, array: 20_000, object: 10_000, slowObject: 18_670}
	}
	return stackModel{budget: 41_663_000 + 18_664 + 10_000, array: 18_664, object: 10_000, slowObject: 18_664}
}()

// Call sites whose JS stack differs from the demo verifier's.
const (
	SiteDemo       = 0
	SitePrevention = 10_000
	SiteGitProof   = -9_000
)

var siteOffset int

// UseCallSite makes checked stringification use the stack available at a
// verifier's call site until the returned function restores the previous
// one: `defer jsjson.UseCallSite(jsjson.SiteGitProof)()`.
func UseCallSite(offset int) func() {
	prev := siteOffset
	siteOffset = offset
	return func() { siteOffset = prev }
}
