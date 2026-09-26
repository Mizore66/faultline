package canonical

import (
	"github.com/Mizore66/faultline/internal/collation"
	"github.com/Mizore66/faultline/internal/v8sort"
)

// LocaleCompare is `a.localeCompare(b)` under Node 22's en-US ICU collation.
func LocaleCompare(a, b string) int { return collation.Compare(a, b) }

// SortLocale is `[...keys].sort((l, r) => l.localeCompare(r))`, with V8's
// sort algorithm: localeCompare is not always transitive, so the order can
// depend on which comparisons are made.
func SortLocale(keys []string) []string {
	prepared := make([]collation.Prepared, len(keys))
	for i, k := range keys {
		prepared[i] = collation.Prepare(k)
	}
	v8sort.Sort(prepared, collation.ComparePrepared)
	out := make([]string, len(prepared))
	for i, p := range prepared {
		out[i] = p.S
	}
	return out
}
