package jsjson

import "testing"

// Catches a serve attachment site whose last printing depth is not the depth
// the frozen TS command actually reached on a native target. Each row is a
// measurement of `node dist/cli.js serve --bundle ...` (Node 22.22.2) on that
// target, never a value derived from the model: linux-amd64 is the hosted
// native push run 41307904995 / job 124852003885 (Linux X64, actual cgo 1
// launched binary, head 44d4c81). Rows for other targets are added only from
// their own actual hosted four-shape measurements, never extrapolated.
//
// The test selects the target's model explicitly, so it holds on any host, and
// restores the previous model exactly. It asserts real last-printing behavior
// (the bisected deepest value whose zod message still prints), not offsets.
func TestServeAttachmentNativeSitesMatchMeasuredTS(t *testing.T) {
	type row struct {
		name string
		site Site
		want [4]int // arr, obj, idx, alt
	}
	targets := []struct {
		name         string
		goos, goarch string
		rows         []row
	}{
		{"linux-amd64", "linux", "amd64", []row{
			{"minimization", SiteServeMinimization, [4]int{2230, 4162, 2229, 2903}},
			{"repair-manifest", SiteServeRepairManifest, [4]int{2232, 4164, 2231, 2905}},
			{"repair-packet", SiteServeRepairPacket, [4]int{2232, 4164, 2231, 2905}},
			{"repair-brief", SiteServeRepairBrief, [4]int{2232, 4164, 2231, 2905}},
			{"prevention-manifest", SiteServePreventionManifest, [4]int{2232, 4165, 2231, 2905}},
			{"prevention-body", SiteServePreventionBody, [4]int{2232, 4165, 2231, 2905}},
			{"prevention-repaired-runs", SiteServePreventionRepairedRuns, [4]int{2232, 4165, 2231, 2905}},
		}},
	}
	shapes := []string{"arr", "obj", "idx", "alt"}
	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			defer func(saved stackModel) { v8Stack = saved }(v8Stack)
			v8Stack = stackModelFor(target.goos, target.goarch)
			for _, r := range target.rows {
				t.Run(r.name, func(t *testing.T) {
					for i, shape := range shapes {
						if got := lastPrinting(t, r.site, shape); got != r.want[i] {
							t.Errorf("%s/%s %s: last printing depth %d, want %d (frozen TS on %s)", target.name, r.name, shape, got, r.want[i], target.name)
						}
					}
				})
			}
		})
	}
}
