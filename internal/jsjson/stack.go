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
//	linux/arm64 (22.22.2 glibc) demo        1827      3653     1956     2434
//	                            prevention  1827      3653     1957     2435
//	                            git         1827      3652     1956     2434
//	                            ledger      1826      3651     1956     2434
//	                            witness     1826      3651     1955     2434
//	darwin/arm64 (22.22.2)      demo        2610      6089     2491     3653
//	                            prevention  2611      6091     2491     3654
//	                            git         2610      6088     2490     3652
//	                            ledger      2609      6087     2490     3652
//	                            witness     2609      6086     2490     3652
//
// Every row is Node 22.22.2, the release CI pins; TestLiveStackThresholds-
// MatchNode bisects all five sites and four shapes against it on linux/amd64,
// linux/arm64 and darwin/arm64 in CI. linux/amd64 was measured here, the
// arm64 rows by the stack-live CI job.
// [0]- and {}-terminated values cost one level more and less (2232 and 4167
// at the linux/amd64 demo site).
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
		return stackModel{budget: 60_894_500 + 23_333 + 10_000, array: 23_333, object: 10_000, slowObject: 24_444,
			sites: [siteCount]int{SiteDemo: 0, SitePrevention: 17_700, SiteGitProof: -9_800, SiteGitLedger: -21_600, SiteDemoWitness: -26_500}}
	case goarch == "arm64":
		return stackModel{budget: 36_531_800 + 20_003 + 10_000, array: 20_003, object: 10_000, slowObject: 18_669,
			sites: [siteCount]int{SiteDemo: 0, SitePrevention: 5_800, SiteGitProof: -4_100, SiteGitLedger: -13_500, SiteDemoWitness: -16_700}}
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
// were bisected through `node dist/cli.js verify` on Node 22.22.2 (last
// list length that still passes):
//
//	                               linux/amd64  linux/arm64  darwin/arm64
//	demo collectFiles, depth d=1   125576       110189       110181
//	  each further level           -31          -32          -32
//	git investigation semantics    125587       110201       110193
//	  (git-proof-bundle.ts:1246)
//	git lifecycle ledger           125562       110175       110167
//	  (git-proof-bundle.ts:1006)
//
// linux/arm64 is glibc; Node built on musl overflows about 20 elements
// earlier (110,171 for the demo walker). windows/arm64 uses the linux/arm64
// values and every other target the linux/amd64 ones; they have not been
// measured (KNOWN_DIFFERENCES.md).
//
// The other spreads Go ports cannot reach the limit: the git walker is capped
// at 2,048 files, witness errors at a few per overlay (at most 64 overlays),
// prevention semantics at 15, and repaired-runs.json has exactly 3 runs.
type spreadModel struct{ collect1, perCollectLevel, semantics, ledger int }

var v8Spread = spreadModelFor(runtime.GOOS, runtime.GOARCH)

func spreadModelFor(goos, goarch string) spreadModel {
	switch {
	case goarch == "arm64" && goos == "darwin":
		return spreadModel{collect1: 110181, perCollectLevel: 32, semantics: 110193, ledger: 110167}
	case goarch == "arm64":
		return spreadModel{collect1: 110189, perCollectLevel: 32, semantics: 110201, ledger: 110175}
	}
	return spreadModel{collect1: 125576, perCollectLevel: 31, semantics: 125587, ledger: 125562}
}

// SpreadSite is a spread call site whose limit was measured.
type SpreadSite int

const (
	SpreadGitSemantics SpreadSite = iota
	SpreadGitLedger
)

// SpreadFits reports whether V8 can spread n elements into the call at site.
func SpreadFits(n int, site SpreadSite) bool {
	if site == SpreadGitLedger {
		return n <= v8Spread.ledger
	}
	return n <= v8Spread.semantics
}

// CollectSpreadFits reports whether V8 can spread n files into the demo
// file walker's push for a directory depth levels below the bundle root.
func CollectSpreadFits(n, depth int) bool {
	return n <= v8Spread.collect1-v8Spread.perCollectLevel*(depth-1)
}

// RimrafDepth is the deepest entry, in levels below the removed root, that
// Node 22's recursive rimrafSync (rmSync with recursive: true) reaches
// before V8's stack overflows, bisected through the git verifier's
// temporary bare repository on Node 22.22.2: 1,672 on linux/amd64 and 1,375
// on linux/arm64 (glibc and musl alike; a file or an empty directory at
// that depth is removed, anything below it overflows). darwin cannot reach
// it (PATH_MAX gives ENAMETOOLONG at about 470 levels) and uses the arm64
// value; other targets use their architecture's Linux value
// (KNOWN_DIFFERENCES.md).
var RimrafDepth = func() int {
	if runtime.GOARCH == "arm64" {
		return 1_375
	}
	return 1_672
}()
