package jsjson

import "testing"

// Catches a serve attachment site whose last printing depth is not the depth
// the frozen TS command actually reached on native macOS Intel (darwin/amd64).
// Every row is a measurement of `node dist/cli.js serve --bundle ...` (Node
// 22.22.2, CLI 0.1.4) from the hosted native push run 41307904995 / job
// 124852003638 (macOS X64, Go 1.27.3 cgo 1, head 44d4c81, artifact 13086535901).
// The values happen to equal linux/amd64's, but they are this target's own
// retained measurements, never inferred from platform similarity. The test
// selects the darwin/amd64 model explicitly, so it runs on every host, and
// restores the previous model exactly. It asserts real last-printing behavior
// (the bisected deepest value whose zod message still prints), not offsets.
func TestServeAttachmentDarwinAMD64SitesMatchMeasuredTS(t *testing.T) {
	defer func(saved stackModel) { v8Stack = saved }(v8Stack)
	v8Stack = stackModelFor("darwin", "amd64")
	shapes := []string{"arr", "obj", "idx", "alt"}
	rows := []struct {
		name string
		site Site
		want [4]int // arr, obj, idx, alt
	}{
		{"minimization", SiteServeMinimization, [4]int{2230, 4162, 2229, 2903}},
		{"repair-manifest", SiteServeRepairManifest, [4]int{2232, 4164, 2231, 2905}},
		{"repair-packet", SiteServeRepairPacket, [4]int{2232, 4164, 2231, 2905}},
		{"repair-brief", SiteServeRepairBrief, [4]int{2232, 4164, 2231, 2905}},
		{"prevention-manifest", SiteServePreventionManifest, [4]int{2232, 4165, 2231, 2905}},
		{"prevention-body", SiteServePreventionBody, [4]int{2232, 4165, 2231, 2905}},
		{"prevention-repaired-runs", SiteServePreventionRepairedRuns, [4]int{2232, 4165, 2231, 2905}},
	}
	for _, r := range rows {
		t.Run("darwin-amd64/"+r.name, func(t *testing.T) {
			for i, shape := range shapes {
				if got := lastPrinting(t, r.site, shape); got != r.want[i] {
					t.Errorf("darwin-amd64/%s %s: last printing depth %d, want %d (frozen TS on native macOS Intel)", r.name, shape, got, r.want[i])
				}
			}
		})
	}
}
