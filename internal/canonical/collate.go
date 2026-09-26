package canonical

import (
	"slices"

	"github.com/Mizore66/faultline/internal/collation"
)

// LocaleCompare is `a.localeCompare(b)` under Node 22's en-US ICU collation.
func LocaleCompare(a, b string) int { return collation.Compare(a, b) }

// SortLocale is `[...keys].sort((l, r) => l.localeCompare(r))`; Array.prototype.sort is stable.
func SortLocale(keys []string) []string {
	type entry struct {
		s   string
		key collation.Key
	}
	entries := make([]entry, len(keys))
	for i, k := range keys {
		entries[i] = entry{k, collation.MakeKey(k)}
	}
	slices.SortStableFunc(entries, func(a, b entry) int { return collation.CompareKeys(a.key, b.key) })
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.s
	}
	return out
}
