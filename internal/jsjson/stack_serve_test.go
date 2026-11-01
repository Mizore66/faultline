package jsjson

import (
	"strings"
	"testing"
)

// serveShape builds the four deep values of the live stack tests as JSON text.
func serveShape(shape string, n int) string {
	switch shape {
	case "arr":
		return strings.Repeat("[", n) + strings.Repeat("]", n)
	case "obj":
		return strings.Repeat(`{"a":`, n) + "0" + strings.Repeat("}", n)
	case "idx":
		return strings.Repeat(`{"0":`, n) + "0" + strings.Repeat("}", n)
	}
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
}

// zodMessageValue is the shape zod stringifies for an invalid_literal at the
// schemaVersion path: [ {received: <value>, code, ...} ].
func zodMessageValue(t *testing.T, shape string, n int) Value {
	t.Helper()
	received, err := Parse(serveShape(shape, n))
	if err != nil {
		t.Fatal(err)
	}
	issue := NewObj()
	issue.Set("received", received)
	issue.Set("code", MakeString("invalid_literal"))
	return MakeArray([]Value{MakeObject(issue)})
}

// lastPrinting is the deepest n in [500, 12000] whose message still prints at
// site (the live tests' bisection).
func lastPrinting(t *testing.T, site Site, shape string) int {
	t.Helper()
	prints := func(n int) bool {
		value := zodMessageValue(t, shape, n)
		var err error
		AtCallSite(site, func() string {
			_, err = StringifyIndentChecked(value, "  ")
			return ""
		})
		return err == nil
	}
	lo, hi := 500, 12000
	if !prints(lo) || prints(hi) {
		t.Fatalf("%s: endpoints do not bracket the threshold", shape)
	}
	for hi-lo > 1 {
		if m := (lo + hi) / 2; prints(m) {
			lo = m
		} else {
			hi = m
		}
	}
	return lo
}

// Catches a serve attachment site whose offset moves the last printing depth
// off the depths measured through the actual `serve --bundle` TS command
// (linux/arm64, Node 22.22.2; arr, obj, idx, alt), and any change to the
// ordinary verifier sites that must keep their existing rows.
func TestServeAttachmentSitesMatchMeasuredTS(t *testing.T) {
	defer func(saved stackModel) { v8Stack = saved }(v8Stack)
	v8Stack = stackModelFor("linux", "arm64")
	shapes := []string{"arr", "obj", "idx", "alt"}
	rows := []struct {
		name string
		site Site
		want [4]int
	}{
		{"minimization", SiteServeMinimization, [4]int{1825, 3648, 1954, 2432}},
		{"repair-manifest", SiteServeRepairManifest, [4]int{1826, 3651, 1956, 2434}},
		{"repair-packet", SiteServeRepairPacket, [4]int{1826, 3651, 1956, 2434}},
		{"repair-brief", SiteServeRepairBrief, [4]int{1826, 3651, 1956, 2434}},
		{"prevention-manifest", SiteServePreventionManifest, [4]int{1826, 3651, 1956, 2434}},
		{"prevention-body", SiteServePreventionBody, [4]int{1826, 3651, 1956, 2434}},
		// Review I2: measured separately through the actual command.
		{"prevention-repaired-runs", SiteServePreventionRepairedRuns, [4]int{1826, 3651, 1956, 2434}},
		// Ordinary rows from the table in stack.go, unchanged.
		{"ordinary git", SiteGitProof, [4]int{1827, 3652, 1956, 2434}},
		{"ordinary prevention", SitePrevention, [4]int{1827, 3653, 1957, 2435}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			for i, shape := range shapes {
				if got := lastPrinting(t, row.site, shape); got != row.want[i] {
					t.Errorf("%s: last printing depth %d, want %d", shape, got, row.want[i])
				}
			}
		})
	}
}

// Catches a serve site that silently changes behavior on a platform whose
// serve --bundle rows have not been measured: those keep the pre-existing
// sites (minimization at the ambient 0, repair at the git row, prevention at
// the prevention row) until their own hosted measurement. linux/arm64,
// linux/amd64, darwin/arm64, darwin/amd64, windows/amd64 and windows/arm64 (the
// last two also have their own serve cost profiles) are measured (see
// TestServeAttachmentSitesMatchMeasuredTS,
// TestServeAttachmentNativeSitesMatchMeasuredTS,
// TestServeAttachmentDarwinARM64SitesMatchMeasuredTS,
// TestServeAttachmentDarwinAMD64SitesMatchMeasuredTS,
// TestServeAttachmentWindowsAMD64SitesMatchMeasuredTS and
// TestServeAttachmentWindowsARM64SitesMatchMeasuredTS) and are excluded here; a
// target leaves this list only when an actual four-shape measurement of it is
// added. The representative unmeasured targets below have no cost profile.
func TestServeAttachmentSitesFallBackOffMeasuredTargets(t *testing.T) {
	for _, platform := range [][2]string{{"freebsd", "amd64"}, {"linux", "riscv64"}} {
		m := stackModelFor(platform[0], platform[1])
		for site, profile := range m.serve {
			if profile != nil {
				t.Errorf("%s/%s site %d has a serve cost profile", platform[0], platform[1], site)
			}
		}
		want := map[Site]int{
			SiteServeMinimization:       0,
			SiteServeRepairManifest:     m.sites[SiteGitProof],
			SiteServeRepairPacket:       m.sites[SiteGitProof],
			SiteServeRepairBrief:        m.sites[SiteGitProof],
			SiteServePreventionManifest: m.sites[SitePrevention],
			SiteServePreventionBody:     m.sites[SitePrevention],

			SiteServePreventionRepairedRuns: m.sites[SitePrevention],
		}
		for site, offset := range want {
			if m.sites[site] != offset {
				t.Errorf("%s/%s site %d offset %d, want fallback %d", platform[0], platform[1], site, m.sites[site], offset)
			}
		}
	}
}

// Catches a nested site that leaks: the surrounding ordinary site must be
// restored after an attachment message is read inside it, and the previous
// offset after the outermost scope.
func TestAtCallSiteRestoresEnclosingSite(t *testing.T) {
	defer restoreStackState()()
	v8Stack = stackModelFor("linux", "arm64")
	siteOffset, siteCosts = 7, nil
	func() {
		defer UseCallSite(SiteGitProof)()
		outer := siteOffset
		if outer != v8Stack.sites[SiteGitProof] {
			t.Fatalf("outer offset %d, want the git row", outer)
		}
		AtCallSite(SiteServeRepairBrief, func() string {
			if siteOffset != -13_550 {
				t.Errorf("inner offset %d, want -13550", siteOffset)
			}
			return ""
		})
		if siteOffset != outer {
			t.Errorf("offset after nested site %d, want the enclosing %d", siteOffset, outer)
		}
	}()
	if siteOffset != 7 {
		t.Errorf("offset after outermost scope %d, want 7", siteOffset)
	}
}
