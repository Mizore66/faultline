package jsjson

import "testing"

// The rows are the actual frozen-TS last printing depths of `node dist/cli.js
// serve --bundle ...` (Node 22.22.2) on native Windows ARM64 (hosted push run
// 41321557251, job 124892698490; Go 1.27.3): minimization 1888/2104/1368/1989 and
// every other serve site 1889/2106/1369/1991 (arr, obj, idx, alt). They are this
// target's own measurements, not derived from Windows AMD64 or any Unix row. The
// checks are behavioral: at each last depth the real stringifier succeeds and one
// deeper it fails with exactly ErrStackOverflow. The tests select the model
// explicitly, so they run on any host, and are not parallel because the
// call-site state is process-wide.

var windowsARM64ServeRows = []struct {
	name string
	site Site
	want [4]int // arr, obj, idx, alt
}{
	{"minimization", SiteServeMinimization, [4]int{1888, 2104, 1368, 1989}},
	{"repair-manifest", SiteServeRepairManifest, [4]int{1889, 2106, 1369, 1991}},
	{"repair-packet", SiteServeRepairPacket, [4]int{1889, 2106, 1369, 1991}},
	{"repair-brief", SiteServeRepairBrief, [4]int{1889, 2106, 1369, 1991}},
	{"prevention-manifest", SiteServePreventionManifest, [4]int{1889, 2106, 1369, 1991}},
	{"prevention-body", SiteServePreventionBody, [4]int{1889, 2106, 1369, 1991}},
	{"prevention-repaired-runs", SiteServePreventionRepairedRuns, [4]int{1889, 2106, 1369, 1991}},
}

// Catches a Windows ARM64 serve model that is not the measured one. The
// pre-change Go fallback printed arrays too early and objects, index-key objects
// and the alternating shape too late (for example minimization
// 1827/3653/1956/2434). Those numbers are that old Go fallback's output, not
// Windows measurements; the difference in shape ratios is one no offset can
// correct.
func TestServeAttachmentWindowsARM64SitesMatchMeasuredTS(t *testing.T) {
	defer restoreStackState()()
	v8Stack = stackModelFor("windows", "arm64")
	for _, row := range windowsARM64ServeRows {
		for i, shape := range windowsServeShapes {
			t.Run("windows-arm64/"+row.name+"/"+shape, func(t *testing.T) {
				checkLastPrinting(t, "windows-arm64/"+row.name, row.site, shape, row.want[i])
			})
		}
	}
}

// Catches a serve correction that disturbs the ordinary Windows ARM64 verifier
// sites: they are the existing arm64 model rows (git 1827/3652/1956/2434,
// prevention 1827/3653/1957/2435) and must stay exactly so.
func TestServeAttachmentWindowsARM64KeepsOrdinarySites(t *testing.T) {
	defer restoreStackState()()
	v8Stack = stackModelFor("windows", "arm64")
	ordinary := []struct {
		name string
		site Site
		want [4]int
	}{
		{"ordinary git", SiteGitProof, [4]int{1827, 3652, 1956, 2434}},
		{"ordinary prevention", SitePrevention, [4]int{1827, 3653, 1957, 2435}},
	}
	for _, row := range ordinary {
		for i, shape := range windowsServeShapes {
			t.Run("windows-arm64/"+row.name+"/"+shape, func(t *testing.T) {
				checkLastPrinting(t, "windows-arm64/"+row.name, row.site, shape, row.want[i])
			})
		}
	}
}

// Catches a serve profile that leaks into, or is lost by, nesting on Windows
// ARM64: serve inside ordinary inside serve, ordinary inside serve inside
// ordinary, minimization inside another serve site and back, and restoration
// when the scope panics. Reads inside a scope select no further site, so they
// see exactly the (offset, profile) pair the scope left in force.
func TestServeAttachmentWindowsARM64NestingRestores(t *testing.T) {
	defer restoreStackState()()
	v8Stack = stackModelFor("windows", "arm64")
	ordinaryGit := [4]int{1827, 3652, 1956, 2434}
	beforeOffset, beforeCosts := siteOffset, siteCosts
	pairRestored := func(t *testing.T, where string) {
		t.Helper()
		if siteOffset != beforeOffset || siteCosts != beforeCosts {
			t.Errorf("%s: (offset, profile) is (%d, %p), want the restored (%d, %p)", where, siteOffset, siteCosts, beforeOffset, beforeCosts)
		}
	}
	readHere := func(t *testing.T, label, shape string, last int) {
		t.Helper()
		if err := serveBoundary(t, shape, last); err != nil {
			t.Errorf("%s %s: depth %d (the last printing depth) failed: %v", label, shape, last, err)
		}
		if err := serveBoundary(t, shape, last+1); err != ErrStackOverflow {
			t.Errorf("%s %s: depth %d returned %v, want exactly ErrStackOverflow", label, shape, last+1, err)
		}
	}
	for _, row := range windowsARM64ServeRows {
		for i, shape := range windowsServeShapes {
			t.Run("windows-arm64/"+row.name+"/"+shape, func(t *testing.T) {
				last, git := row.want[i], ordinaryGit[i]
				AtCallSite(row.site, func() string {
					readHere(t, "serve", shape, last)
					AtCallSite(SiteGitProof, func() string {
						readHere(t, "ordinary inside "+row.name, shape, git)
						AtCallSite(row.site, func() string {
							readHere(t, "serve inside ordinary inside "+row.name, shape, last)
							return ""
						})
						readHere(t, "ordinary after the inner serve", shape, git)
						return ""
					})
					readHere(t, "serve after the ordinary", shape, last)
					return ""
				})
				pairRestored(t, "after serve -> ordinary -> serve")
				AtCallSite(SiteGitProof, func() string {
					readHere(t, "ordinary", shape, git)
					AtCallSite(row.site, func() string {
						readHere(t, "serve inside ordinary", shape, last)
						AtCallSite(SiteGitProof, func() string {
							readHere(t, "ordinary inside serve inside ordinary", shape, git)
							return ""
						})
						return ""
					})
					readHere(t, "ordinary after the serve", shape, git)
					return ""
				})
				pairRestored(t, "after ordinary -> serve -> ordinary")
				// Minimization inside any other serve site, and another serve
				// site inside minimization.
				other := windowsARM64ServeRows[0]
				if row.site == other.site {
					other = windowsARM64ServeRows[1]
				}
				AtCallSite(row.site, func() string {
					AtCallSite(other.site, func() string {
						readHere(t, "inner serve "+other.name, shape, other.want[i])
						return ""
					})
					readHere(t, "outer serve after the inner serve", shape, last)
					return ""
				})
				pairRestored(t, "after serve -> serve")
				func() {
					defer func() { _ = recover() }()
					AtCallSite(row.site, func() string { panic("scope aborted") })
				}()
				pairRestored(t, "after a panicking scope")
			})
		}
	}
}
