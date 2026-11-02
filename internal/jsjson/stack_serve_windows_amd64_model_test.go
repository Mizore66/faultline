package jsjson

import "testing"

// The rows are the actual frozen-TS last printing depths of `node dist/cli.js
// serve --bundle ...` (Node 22.22.2) on native Windows AMD64 (push run
// 41321557251, job 124892698398; the pull-request run 41321562664 repeats them
// exactly): minimization 1450/1731/1198/1578 and every other serve site
// 1451/1732/1199/1579 (arr, obj, idx, alt). Their shape ratios differ from every
// Unix row, so no offset alone reproduces them; the checks are behavioral: at
// each last depth the real stringifier succeeds and one deeper it fails with
// exactly ErrStackOverflow. The tests select the model explicitly, so they run on
// any host. Nothing here is parallel: the call-site state is process-wide.

// serveBoundary stringifies the zod-shaped message at depth n with the CURRENT
// call-site offset (no site is selected here) and returns the error.
func serveBoundary(t *testing.T, shape string, n int) error {
	t.Helper()
	_, err := StringifyIndentChecked(zodMessageValue(t, shape, n), "  ")
	return err
}

// checkLastPrinting requires success at last and exactly ErrStackOverflow at
// last+1, with the call site selected through AtCallSite.
func checkLastPrinting(t *testing.T, label string, site Site, shape string, last int) {
	t.Helper()
	var atLast, atNext error
	AtCallSite(site, func() string {
		atLast = serveBoundary(t, shape, last)
		atNext = serveBoundary(t, shape, last+1)
		return ""
	})
	if atLast != nil {
		t.Errorf("%s %s: depth %d (the measured last printing depth) failed: %v", label, shape, last, atLast)
	}
	if atNext != ErrStackOverflow {
		t.Errorf("%s %s: depth %d (one past the measured last depth) returned %v, want exactly ErrStackOverflow", label, shape, last+1, atNext)
	}
}

// restoreStackState puts the model and the call-site pair (offset and selected
// cost profile) back exactly.
func restoreStackState() func() {
	savedModel, savedOffset, savedCosts := v8Stack, siteOffset, siteCosts
	return func() { v8Stack, siteOffset, siteCosts = savedModel, savedOffset, savedCosts }
}

var windowsAMD64ServeRows = []struct {
	name string
	site Site
	want [4]int // arr, obj, idx, alt
}{
	{"minimization", SiteServeMinimization, [4]int{1450, 1731, 1198, 1578}},
	{"repair-manifest", SiteServeRepairManifest, [4]int{1451, 1732, 1199, 1579}},
	{"repair-packet", SiteServeRepairPacket, [4]int{1451, 1732, 1199, 1579}},
	{"repair-brief", SiteServeRepairBrief, [4]int{1451, 1732, 1199, 1579}},
	{"prevention-manifest", SiteServePreventionManifest, [4]int{1451, 1732, 1199, 1579}},
	{"prevention-body", SiteServePreventionBody, [4]int{1451, 1732, 1199, 1579}},
	{"prevention-repaired-runs", SiteServePreventionRepairedRuns, [4]int{1451, 1732, 1199, 1579}},
}

var windowsServeShapes = []string{"arr", "obj", "idx", "alt"}

// Catches a Windows AMD64 serve profile that is not the measured one: the
// fallback (the Unix-fitted amd64 costs at the ambient, git and prevention
// offsets) prints about 780 levels deeper for arrays and more than 2400 for
// objects than native Node does here.
func TestServeAttachmentWindowsAMD64SitesMatchMeasuredTS(t *testing.T) {
	defer restoreStackState()()
	v8Stack = stackModelFor("windows", "amd64")
	for _, row := range windowsAMD64ServeRows {
		for i, shape := range windowsServeShapes {
			t.Run("windows-amd64/"+row.name+"/"+shape, func(t *testing.T) {
				checkLastPrinting(t, "windows-amd64/"+row.name, row.site, shape, row.want[i])
			})
		}
	}
}

// Catches a serve correction that disturbs the ordinary Windows verifier sites:
// they are the existing default (amd64) rows of the model table (git
// 2232/4165/2231/2906, prevention 2233/4167/2232/2907) and must stay exactly so.
func TestServeAttachmentWindowsAMD64KeepsOrdinarySites(t *testing.T) {
	defer restoreStackState()()
	v8Stack = stackModelFor("windows", "amd64")
	ordinary := []struct {
		name string
		site Site
		want [4]int
	}{
		{"ordinary git", SiteGitProof, [4]int{2232, 4165, 2231, 2906}},
		{"ordinary prevention", SitePrevention, [4]int{2233, 4167, 2232, 2907}},
	}
	for _, row := range ordinary {
		for i, shape := range windowsServeShapes {
			t.Run("windows-amd64/"+row.name+"/"+shape, func(t *testing.T) {
				checkLastPrinting(t, "windows-amd64/"+row.name, row.site, shape, row.want[i])
			})
		}
	}
}

// Catches a serve profile that leaks into, or is lost by, nesting. Every nesting
// must restore the enclosing (offset, profile) pair at each level: serve inside
// ordinary inside serve, ordinary inside serve inside ordinary, serve inside
// serve (minimization inside another serve site and back), and restoration even
// when the scope panics. Reads are made with NO site selected inside the scope,
// so they see exactly the pair the scope left in force.
func TestServeAttachmentWindowsAMD64NestingRestores(t *testing.T) {
	defer restoreStackState()()
	v8Stack = stackModelFor("windows", "amd64")
	ordinaryGit := [4]int{2232, 4165, 2231, 2906}
	beforeOffset, beforeCosts := siteOffset, siteCosts
	pairRestored := func(t *testing.T, where string) {
		t.Helper()
		if siteOffset != beforeOffset || siteCosts != beforeCosts {
			t.Errorf("%s: (offset, profile) is (%d, %p), want the restored (%d, %p)", where, siteOffset, siteCosts, beforeOffset, beforeCosts)
		}
	}
	// readHere checks the boundary of whatever pair is currently selected.
	readHere := func(t *testing.T, label, shape string, last int) {
		t.Helper()
		if err := serveBoundary(t, shape, last); err != nil {
			t.Errorf("%s %s: depth %d (the last printing depth) failed: %v", label, shape, last, err)
		}
		if err := serveBoundary(t, shape, last+1); err != ErrStackOverflow {
			t.Errorf("%s %s: depth %d returned %v, want exactly ErrStackOverflow", label, shape, last+1, err)
		}
	}
	for _, row := range windowsAMD64ServeRows {
		for i, shape := range windowsServeShapes {
			t.Run("windows-amd64/"+row.name+"/"+shape, func(t *testing.T) {
				last, git := row.want[i], ordinaryGit[i]
				// serve -> ordinary -> serve: each level sees its own pair.
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
				// ordinary -> serve -> ordinary.
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
				// serve -> serve: another serve site (the other profile when this
				// is minimization, else minimization) and back.
				other := windowsAMD64ServeRows[0]
				if row.site == other.site {
					other = windowsAMD64ServeRows[1]
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
				// A panic inside a scope still restores the pair.
				func() {
					defer func() { _ = recover() }()
					AtCallSite(row.site, func() string { panic("scope aborted") })
				}()
				pairRestored(t, "after a panicking scope")
			})
		}
	}
}

// Catches a cost profile that reaches a platform it was not measured on: only
// windows/amd64 and windows/arm64 have one, and only on their seven serve sites;
// every other target and every ordinary site have none. Each Windows base keeps
// exactly the costs of its unchanged default model (amd64 and linux/arm64).
func TestServeCostProfileIsWindowsServeOnly(t *testing.T) {
	servers := map[Site]bool{SiteServeMinimization: true, SiteServeRepairManifest: true, SiteServeRepairPacket: true,
		SiteServeRepairBrief: true, SiteServePreventionManifest: true, SiteServePreventionBody: true, SiteServePreventionRepairedRuns: true}
	for _, p := range [][2]string{{"windows", "amd64"}, {"windows", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "amd64"}, {"darwin", "arm64"},
		{"freebsd", "amd64"}, {"linux", "riscv64"}} {
		m := stackModelFor(p[0], p[1])
		for site := Site(0); site < siteCount; site++ {
			has := m.serve[site] != nil
			if want := p[0] == "windows" && servers[site]; has != want {
				t.Errorf("%s/%s site %d: serve profile present=%v, want %v", p[0], p[1], site, has, want)
			}
		}
	}
	if w, l := stackModelFor("windows", "amd64"), stackModelFor("linux", "amd64"); w.baseCosts() != l.baseCosts() {
		t.Errorf("windows/amd64 base costs %+v differ from the default amd64 model %+v", w.baseCosts(), l.baseCosts())
	}
	if w, l := stackModelFor("windows", "arm64"), stackModelFor("linux", "arm64"); w.baseCosts() != l.baseCosts() {
		t.Errorf("windows/arm64 base costs %+v differ from the default arm64 model %+v", w.baseCosts(), l.baseCosts())
	}
}
