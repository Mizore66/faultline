package canonical

import (
	"slices"
	"sync"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// Collators keep internal buffers, so each goroutine takes its own.
var collators = sync.Pool{New: func() any { return collate.New(language.AmericanEnglish) }}

// LocaleCompare is `a.localeCompare(b)` under Node's en-US ICU defaults.
func LocaleCompare(a, b string) int {
	c := collators.Get().(*collate.Collator)
	defer collators.Put(c)
	return c.CompareString(a, b)
}

// SortLocale is `[...keys].sort((l, r) => l.localeCompare(r))`; Array.prototype.sort is stable.
func SortLocale(keys []string) []string {
	out := slices.Clone(keys)
	slices.SortStableFunc(out, LocaleCompare)
	return out
}
