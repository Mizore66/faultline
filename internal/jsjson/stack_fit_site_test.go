//go:build faultline_test

package jsjson

import (
	"strings"
	"testing"
)

// Catches a refit helper that ignores a site's cost profile. A tiny synthetic
// model and short shapes keep this cheap (no thousand-level renders): the base
// costs are 10 per frame over a residual budget of 100, while the profiled site
// charges 20 per frame over a residual budget of 400. With last depths 21
// (arrays) and 20 (objects), which the profile prints exactly at offset 0, the
// profile-aware wrapper must report offset 0 and the generic helper (ambient
// base costs) must report 100; both must put the call-site pair back exactly.
func TestFitSiteOffsetAtSiteUsesTheSitesCostProfile(t *testing.T) {
	defer restoreStackState()()
	const wrapper = 20 // the two wrapper frames at the base cost of 10
	v8Stack = stackModel{budget: 100 + wrapper, array: 10, object: 10, slowObject: 10}
	v8Stack.serve[SiteServeMinimization] = &frameCosts{budget: 400 + 2*20, array: 20, object: 20, slowObject: 20}
	siteOffset, siteCosts = 7, nil // a sentinel pair that must survive

	shapes := []func(n int) string{
		func(n int) string { return strings.Repeat("[", n) + strings.Repeat("]", n) },
		func(n int) string { return strings.Repeat(`{"a":`, n) + "0" + strings.Repeat("}", n) },
	}
	lasts := []int{21, 20}

	lo, hi, ok := FitSiteOffsetAtSiteForTest(SiteServeMinimization, shapes, lasts)
	if !ok || lo != 0 || hi != 0 {
		t.Errorf("profile-aware fit = %d..%d ok=%v, want exactly offset 0", lo, hi, ok)
	}
	if siteOffset != 7 || siteCosts != nil {
		t.Errorf("after the profile-aware fit the pair is (%d, %v), want the sentinel (7, nil)", siteOffset, siteCosts)
	}

	lo, hi, ok = FitSiteOffsetForTest(shapes, lasts)
	if !ok || lo != 100 || hi != 100 {
		t.Errorf("generic fit = %d..%d ok=%v, want exactly offset 100 under the base costs", lo, hi, ok)
	}
	if siteOffset != 7 || siteCosts != nil {
		t.Errorf("after the generic fit the pair is (%d, %v), want the sentinel (7, nil)", siteOffset, siteCosts)
	}
}
