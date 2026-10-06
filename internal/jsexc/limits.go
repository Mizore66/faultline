package jsexc

import "errors"

// maxCollectionSize is V8's OrderedHashTable capacity limit: a Set or Map
// holds at most 2^24 entries, and adding one more throws a RangeError.
const maxCollectionSize = 1 << 24

var (
	errSetSize = errors.New("Set maximum size exceeded")
	errMapSize = errors.New("Map maximum size exceeded")
)

// SetAdd is set.add(k) on a JS Set modelled as map[K]bool.
func SetAdd[K comparable](set map[K]bool, k K) {
	if _, ok := set[k]; !ok {
		if len(set) >= maxCollectionSize {
			Throw(errSetSize)
		}
		set[k] = true
	}
}

// MapSet is map.set(k, v) on a JS Map.
func MapSet[K comparable, V any](m map[K]V, k K, v V) {
	if _, ok := m[k]; !ok && len(m) >= maxCollectionSize {
		Throw(errMapSize)
	}
	m[k] = v
}
