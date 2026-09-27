package jsjson

import "runtime"

// stackModel reproduces where V8's JsonStringifier runs out of native stack
// for zod's ZodError.message (JSON.stringify(issues, replacer, 2)) in
// `node dist/cli.js verify`. Each non-empty array or object pushes one
// frame; objects on V8's slow path (index keys, dictionary mode) cost more.
// A value overflows when the frames above it no longer fit in the budget.
// Costs are relative (a fast object is 10000) and were fitted to the last
// depth whose zod message still prints, per shape and per call site
// (bisected through `node dist/cli.js verify`):
//
//	platform (Node)             site        []-ended  {"a":0}  {"0":0}  alternating
//	linux/amd64 (22.22.2)       demo        2233      4166     2232     2906
//	                            prevention  2233      4167     2232     2907
//	                            git         2232      4165     2231     2906
//	                            ledger      2232      4165     2231     2905
//	                            witness     2232      4164     2231     2905
//	darwin/arm64 (22.23.2)      demo        2747      6867     2616     3924
//	                            prevention  2748      6869     2616     3924
//	                            git         2747      6866     2615     3923
//	                            ledger      2746      6864     2614     3922
//	                            witness     2746      6863     2614     3922
//	linux/arm64 (22.13.1 glibc) demo        1781      3560     1907     2373
//	                            prevention  1781      3561     1907     -
//	                            git         1780      3559     1907     -
//	                            witness     1780      3558     1906     -
//
// linux/amd64 was measured here; the darwin/arm64 and linux/arm64 rows are
// the reviewers' measurements. [0]- and {}-terminated values cost one level
// more and less (2232 and 4167 at the linux/amd64 demo site). The ledger
// site on linux/arm64 has not been measured and uses the git offset.
//
// The budgets include the two frames Go charges for zod's wrapper (the
// issues array and the issue object holding `received`). The JS frames
// below the message read differ per call site; sites shifts the budget:
// "git" is every git verifier schema message except the lifecycle ledger,
// "witness" is the demo witness artifact (proof-bundle.ts:245).
//
// Other Node versions and libcs have different stack costs: musl builds
// (node:alpine) differ from glibc by up to 78 levels, and the arm64
// budgets shift between Node 22.13 and 22.23. Windows and other targets use
// the Linux model for their architecture. None of these have been fitted
// (KNOWN_DIFFERENCES.md).
type stackModel struct {
	budget, array, object, slowObject int
	sites                             [siteCount]int
}

var v8Stack = stackModelFor(runtime.GOOS, runtime.GOARCH)

func stackModelFor(goos, goarch string) stackModel {
	switch {
	case goos == "darwin" && goarch == "arm64":
		return stackModel{budget: 68_672_000 + 25_000 + 10_000, array: 25_000, object: 10_000, slowObject: 26_250,
			sites: [siteCount]int{SiteDemo: 0, SitePrevention: 20_000, SiteGitProof: -7_000, SiteGitLedger: -30_500, SiteDemoWitness: -35_000}}
	case goarch == "arm64":
		return stackModel{budget: 35_605_000 + 20_000 + 10_000, array: 20_000, object: 10_000, slowObject: 18_666,
			sites: [siteCount]int{SiteDemo: 0, SitePrevention: 7_000, SiteGitProof: -7_000, SiteGitLedger: -7_000, SiteDemoWitness: -20_000}}
	}
	return stackModel{budget: 41_667_000 + 18_667 + 10_000, array: 18_667, object: 10_000, slowObject: 18_664,
		sites: [siteCount]int{SiteDemo: 0, SitePrevention: 7_000, SiteGitProof: -11_500, SiteGitLedger: -15_500, SiteDemoWitness: -19_000}}
}

// Site is a JS call site that reads a zod message; see stackModel.
type Site int

const (
	SiteDemo Site = iota
	SitePrevention
	SiteGitProof
	SiteGitLedger
	SiteDemoWitness
	siteCount
)

var siteOffset int

// UseCallSite makes checked stringification use the stack available at a
// call site until the returned function restores the previous one:
// `defer jsjson.UseCallSite(jsjson.SiteGitProof)()`.
func UseCallSite(site Site) func() {
	prev := siteOffset
	siteOffset = v8Stack.sites[site]
	return func() { siteOffset = prev }
}

// AtCallSite runs f (which reads a zod message) at site.
func AtCallSite(site Site, f func() string) string {
	defer UseCallSite(site)()
	return f()
}

// V8 passes spread call arguments (list.push(...items)) on the machine stack,
// so a spread of too many elements throws "Maximum call stack size
// exceeded". The limit depends on the stack left at the call site. Limits
// were bisected through `node dist/cli.js verify` on Node 22.22.2
// linux/amd64:
//
//	demo collectFiles, directory d levels below the root: 125607 - 31*d
//	  (d=1: 125576 files; d=5: 125452)
//	git investigation semantics errors (git-proof-bundle.ts:1246): 125587
//	  (125,587 errors pass, 125,588 overflow)
//	git lifecycle ledger errors (git-proof-bundle.ts:1006): 125562
//	  (125,562 errors pass, 125,563 overflow)
//
// The other spreads Go ports cannot reach the limit: the git walker is capped
// at 2,048 files, witness errors at a few per overlay (at most 64 overlays),
// prevention semantics at 15, and repaired-runs.json has exactly 3 runs.
//
// Other platforms have not been measured: darwin/arm64 is scaled by the
// plain-Node limit the reviewers measured there (110,423 vs 125,273), and
// linux/arm64 uses the linux/amd64 values (KNOWN_DIFFERENCES.md).
type spreadModel struct{ base, perCollectLevel int }

var v8Spread = func() spreadModel {
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		return spreadModel{base: 125607 * 110423 / 125273, perCollectLevel: 31}
	}
	return spreadModel{base: 125607, perCollectLevel: 31}
}()

// Spread call sites, as the stack left there relative to the demo walker's
// root (in elements).
const (
	SpreadGitSemantics = 20
	SpreadGitLedger    = 45
)

// SpreadFits reports whether V8 can spread n elements into a call at a site
// offset elements below the demo walker's root (see v8Spread).
func SpreadFits(n, offset int) bool { return n <= v8Spread.base-offset }

// CollectSpreadOffset is the offset of the demo file walker's spread for a
// directory depth levels below the bundle root.
func CollectSpreadOffset(depth int) int { return v8Spread.perCollectLevel * depth }
