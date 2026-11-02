package jsjson

import "testing"

// Catches a serve attachment site whose last printing depth is not the depth
// the frozen TS command actually reached on native macOS ARM64. Every row is a
// measurement of `node dist/cli.js serve --bundle ...` (Node 22.22.2, CLI 0.1.4)
// from the hosted native push run 41307904995 / job 124852003946 (macOS ARM64,
// Go 1.27.3 cgo 1, head 44d4c81, artifact 13085102333), never a value derived
// from the model. The test selects the darwin/arm64 model explicitly, so it
// holds on any host, and restores the previous model exactly. It asserts real
// last-printing behavior (the bisected deepest value whose zod message still
// prints), not offsets. Other targets need their own actual measurements.
func TestServeAttachmentDarwinARM64SitesMatchMeasuredTS(t *testing.T) {
	defer func(saved stackModel) { v8Stack = saved }(v8Stack)
	v8Stack = stackModelFor("darwin", "arm64")
	shapes := []string{"arr", "obj", "idx", "alt"}
	rows := []struct {
		name string
		site Site
		want [4]int // arr, obj, idx, alt
	}{
		{"minimization", SiteServeMinimization, [4]int{2607, 6082, 2488, 3648}},
		{"repair-manifest", SiteServeRepairManifest, [4]int{2609, 6087, 2490, 3652}},
		{"repair-packet", SiteServeRepairPacket, [4]int{2609, 6087, 2490, 3652}},
		{"repair-brief", SiteServeRepairBrief, [4]int{2609, 6087, 2490, 3652}},
		{"prevention-manifest", SiteServePreventionManifest, [4]int{2609, 6087, 2490, 3652}},
		{"prevention-body", SiteServePreventionBody, [4]int{2609, 6087, 2490, 3652}},
		{"prevention-repaired-runs", SiteServePreventionRepairedRuns, [4]int{2609, 6087, 2490, 3652}},
	}
	for _, r := range rows {
		t.Run("darwin-arm64/"+r.name, func(t *testing.T) {
			for i, shape := range shapes {
				if got := lastPrinting(t, r.site, shape); got != r.want[i] {
					t.Errorf("darwin-arm64/%s %s: last printing depth %d, want %d (frozen TS on native macOS ARM64)", r.name, shape, got, r.want[i])
				}
			}
		})
	}
}
