// Package jsjson reproduces JSON.parse and JSON.stringify as V8 12.4 (Node 22)
// implements them. Strings are WTF-8 (see internal/jsstr).
package jsjson

import (
	"cmp"
	"slices"
	"strconv"
)

type Kind uint8

const (
	Undefined Kind = iota // never produced by Parse; models absent JS values
	Null
	Bool
	Number
	String
	Array
	Object
)

// Value is 48 bytes: arrays sit behind a pointer so the common scalar and
// object cases stay small (JSON inputs can hold millions of values).
type Value struct {
	kind Kind
	b    bool
	n    float64
	s    string
	arr  *[]Value
	obj  *Obj
}

func MakeNull() Value               { return Value{kind: Null} }
func MakeBool(b bool) Value         { return Value{kind: Bool, b: b} }
func MakeNumber(n float64) Value    { return Value{kind: Number, n: n} }
func MakeString(s string) Value     { return Value{kind: String, s: s} }
func MakeArray(items []Value) Value { return Value{kind: Array, arr: &items} }
func MakeObject(o *Obj) Value       { return Value{kind: Object, obj: o} }

func (v Value) Kind() Kind   { return v.kind }
func (v Value) Bool() bool   { return v.b }
func (v Value) Num() float64 { return v.n }
func (v Value) Str() string  { return v.s }
func (v Value) Items() []Value {
	if v.arr == nil {
		return nil
	}
	return *v.arr
}
func (v Value) Obj() *Obj { return v.obj }

// Get follows object keys like JS property access; a missing step or a
// non-object yields Undefined.
func (v Value) Get(keys ...string) Value {
	for _, k := range keys {
		if v.kind != Object {
			return Value{}
		}
		v = v.obj.Field(k)
	}
	return v
}

// Obj keeps ordinary JS property order: array-index keys in ascending numeric
// order, then the remaining keys in first-insertion order. Index keys are
// sorted lazily, so building an object from out-of-order index keys (JSON
// input or canonical's localeCompare order) stays O(n log n). Members are
// stored as parallel slices; a lookup map is built only for objects with
// more than smallObject members, so the many small objects in real inputs
// cost a few words each instead of a map.
type Obj struct {
	idx       []indexKey
	idxSorted bool
	strs      []string
	keys      []string // insertion order, parallel to vals
	vals      []Value
	index     map[string]int // key -> position in keys; nil while small
}

const smallObject = 8

type indexKey struct {
	n uint32
	k string
}

func NewObj() *Obj { return &Obj{idxSorted: true} }

// find returns k's position in keys, or -1.
func (o *Obj) find(k string) int {
	if o.index != nil {
		if i, ok := o.index[k]; ok {
			return i
		}
		return -1
	}
	for i, key := range o.keys {
		if key == k {
			return i
		}
	}
	return -1
}

func (o *Obj) Len() int { return len(o.keys) }
func (o *Obj) Get(k string) (Value, bool) {
	if i := o.find(k); i >= 0 {
		return o.vals[i], true
	}
	return Value{}, false
}
func (o *Obj) Field(k string) Value { v, _ := o.Get(k); return v }

// Keys returns the keys in JS property order.
func (o *Obj) Keys() []string { return slices.Clone(o.orderedKeys()) }

// orderedKeys returns the keys in JS property order without copying when the
// object has no array-index keys.
func (o *Obj) orderedKeys() []string {
	if len(o.idx) == 0 {
		return o.strs
	}
	if !o.idxSorted {
		slices.SortFunc(o.idx, func(a, b indexKey) int { return cmp.Compare(a.n, b.n) })
		o.idxSorted = true
	}
	out := make([]string, 0, len(o.idx)+len(o.strs))
	for _, e := range o.idx {
		out = append(out, e.k)
	}
	return append(out, o.strs...)
}

func (o *Obj) Set(k string, v Value) {
	if i := o.find(k); i >= 0 {
		o.vals[i] = v
		return
	}
	o.keys = append(o.keys, k)
	o.vals = append(o.vals, v)
	if o.index != nil {
		o.index[k] = len(o.keys) - 1
	} else if len(o.keys) > smallObject {
		o.index = make(map[string]int, len(o.keys)*2)
		for i, key := range o.keys {
			o.index[key] = i
		}
	}
	n, ok := arrayIndex(k)
	if !ok {
		o.strs = append(o.strs, k)
		return
	}
	if l := len(o.idx); l > 0 && o.idx[l-1].n > n {
		o.idxSorted = false
	}
	o.idx = append(o.idx, indexKey{n, k})
}

func (o *Obj) Delete(k string) {
	i := o.find(k)
	if i < 0 {
		return
	}
	o.keys = slices.Delete(o.keys, i, i+1)
	o.vals = slices.Delete(o.vals, i, i+1)
	if o.index != nil {
		delete(o.index, k)
		for j := i; j < len(o.keys); j++ {
			o.index[o.keys[j]] = j
		}
	}
	if j := slices.IndexFunc(o.idx, func(e indexKey) bool { return e.k == k }); j >= 0 {
		o.idx = slices.Delete(o.idx, j, j+1)
		return
	}
	j := slices.Index(o.strs, k)
	o.strs = slices.Delete(o.strs, j, j+1)
}

// arrayIndex reports whether k is a canonical array index, 0 … 2^32−2.
func arrayIndex(k string) (uint32, bool) {
	if k == "" || len(k) > 10 || (len(k) > 1 && k[0] == '0') {
		return 0, false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < '0' || k[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(k, 10, 64)
	if err != nil || n > 4294967294 {
		return 0, false
	}
	return uint32(n), true
}
