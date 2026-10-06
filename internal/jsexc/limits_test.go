package jsexc

import (
	"testing"
)

// V8 throws when adding the 2^24+1-th distinct entry; re-adding an existing
// key at the cap is fine.
func TestCollectionSizeCap(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates 2^24 entries")
	}
	set := make(map[int]bool, maxCollectionSize)
	for i := range maxCollectionSize {
		set[i] = true
	}
	if err := Try(func() { SetAdd(set, 0) }); err != nil {
		t.Fatalf("existing key: %v", err)
	}
	if err := Try(func() { SetAdd(set, -1) }); err == nil || err.Error() != "Set maximum size exceeded" {
		t.Fatalf("new key: %v", err)
	}
	m := make(map[int]int, maxCollectionSize)
	for i := range maxCollectionSize {
		m[i] = i
	}
	if err := Try(func() { MapSet(m, 5, 0) }); err != nil {
		t.Fatalf("existing key: %v", err)
	}
	if err := Try(func() { MapSet(m, -1, 0) }); err == nil || err.Error() != "Map maximum size exceeded" {
		t.Fatalf("new key: %v", err)
	}
}

func TestConcatJoinLength(t *testing.T) {
	half := string(make([]byte, MaxStringLength/2))
	if err := Try(func() { Concat(half, half) }); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
	if err := Try(func() { Concat(half, half, "x") }); err != ErrInvalidStringLength {
		t.Fatalf("over the limit: %v", err)
	}
	if err := Try(func() { Join([]string{half, half}, "") }); err != nil {
		t.Fatalf("join at the limit: %v", err)
	}
	if err := Try(func() { Join([]string{half, half}, "\n") }); err != ErrInvalidStringLength {
		t.Fatalf("join over the limit: %v", err)
	}
}
