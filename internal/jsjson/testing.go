//go:build faultline_test

package jsjson

// FitSiteOffsetForTest finds the call-site offsets (in the stack model's
// units, stepped by 100) for which a zod message at site prints exactly as
// deep as a measured TS run: for each shape, depth last prints and last+1
// overflows. It returns the lowest and highest such offset, ok false if
// none fits; difftest's stack live test reports it when a site's measured
// depths differ, so the offset can be refitted from a CI log.
func FitSiteOffsetForTest(shapes []func(n int) string, lasts []int) (lo, hi int, ok bool) {
	prev := siteOffset
	defer func() { siteOffset = prev }()
	overflows := func(text string) bool {
		v, err := Parse(text)
		if err != nil {
			return false
		}
		issue := NewObj()
		issue.Set("received", v)
		_, err = StringifyIndentChecked(MakeArray([]Value{MakeObject(issue)}), "")
		return err == ErrStackOverflow
	}
	for offset := -300_000; offset <= 300_000; offset += 100 {
		siteOffset = offset
		fits := true
		for i, shape := range shapes {
			if overflows(shape(lasts[i])) || !overflows(shape(lasts[i]+1)) {
				fits = false
				break
			}
		}
		if fits {
			if !ok {
				lo = offset
			}
			hi, ok = offset, true
		}
	}
	return lo, hi, ok
}
