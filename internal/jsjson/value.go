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

type Value struct {
	kind Kind
	b    bool
	n    float64
	s    string
	arr  []Value
	obj  *Obj
}

func MakeNull() Value               { return Value{kind: Null} }
func MakeBool(b bool) Value         { return Value{kind: Bool, b: b} }
func MakeNumber(n float64) Value    { return Value{kind: Number, n: n} }
func MakeString(s string) Value     { return Value{kind: String, s: s} }
func MakeArray(items []Value) Value { return Value{kind: Array, arr: items} }
func MakeObject(o *Obj) Value       { return Value{kind: Object, obj: o} }

func (v Value) Kind() Kind     { return v.kind }
func (v Value) Bool() bool     { return v.b }
func (v Value) Num() float64   { return v.n }
func (v Value) Str() string    { return v.s }
func (v Value) Items() []Value { return v.arr }
func (v Value) Obj() *Obj      { return v.obj }

// Get follows object keys like JS property access; a missing step or a
// non-object yields Undefined.
func (v Value) Get(keys ...string) Value {
	for _, k := range keys {
		if v.kind != Object {
			return Value{}
		}
		v = v.obj.values[k]
	}
	return v
}

// Obj keeps ordinary JS property order: array-index keys in ascending numeric
// order, then the remaining keys in first-insertion order.
type Obj struct {
	keys   []string
	values map[string]Value
	nIndex int // leading keys that are array indices
}

func NewObj() *Obj { return &Obj{values: map[string]Value{}} }

func (o *Obj) Len() int                   { return len(o.keys) }
func (o *Obj) Keys() []string             { return slices.Clone(o.keys) }
func (o *Obj) Get(k string) (Value, bool) { v, ok := o.values[k]; return v, ok }
func (o *Obj) Field(k string) Value       { return o.values[k] }

func (o *Obj) Set(k string, v Value) {
	if _, ok := o.values[k]; ok {
		o.values[k] = v
		return
	}
	o.values[k] = v
	idx, ok := arrayIndex(k)
	if !ok {
		o.keys = append(o.keys, k)
		return
	}
	pos, _ := slices.BinarySearchFunc(o.keys[:o.nIndex], idx, func(key string, target uint32) int {
		n, _ := arrayIndex(key)
		return cmp.Compare(n, target)
	})
	o.keys = slices.Insert(o.keys, pos, k)
	o.nIndex++
}

func (o *Obj) Delete(k string) {
	if _, ok := o.values[k]; !ok {
		return
	}
	delete(o.values, k)
	i := slices.Index(o.keys, k)
	o.keys = slices.Delete(o.keys, i, i+1)
	if i < o.nIndex {
		o.nIndex--
	}
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
