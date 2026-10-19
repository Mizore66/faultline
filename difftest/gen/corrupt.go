package gen

import (
	"math/rand/v2"
	"slices"
	"strings"

	"github.com/Mizore66/faultline/internal/jsjson"
)

var deepReplacements = []string{"1", "0", "-1", "1.5", "3", `"x"`, `""`, "null", "[]", "{}", "true", "false",
	`"sha256:` + strings.Repeat("a", 64) + `"`, `"sha256:zz"`, `"` + strings.Repeat("b", 40) + `"`, `"2026-07-16T11:00:00.000Z"`, `"2026-02-30T00:00:00.000Z"`}

// CorruptDeep walks to a random node of a JSON document and deletes a key,
// adds an unknown key, drops an array element, or replaces the node.
func CorruptDeep(r *rand.Rand, text string) string {
	root, err := jsjson.Parse(text)
	if err != nil {
		return text
	}
	node := root
	for depth := 0; depth < 8 && r.IntN(4) != 0; depth++ {
		switch node.Kind() {
		case jsjson.Object:
			keys := node.Obj().Keys()
			if len(keys) == 0 {
				break
			}
			child := node.Obj().Field(keys[r.IntN(len(keys))])
			if child.Kind() != jsjson.Object && child.Kind() != jsjson.Array {
				break
			}
			node = child
		case jsjson.Array:
			if len(node.Items()) == 0 {
				break
			}
			child := node.Items()[r.IntN(len(node.Items()))]
			if child.Kind() != jsjson.Object && child.Kind() != jsjson.Array {
				break
			}
			node = child
		}
	}
	replacement, _ := jsjson.Parse(pick(r, deepReplacements...))
	switch node.Kind() {
	case jsjson.Object:
		keys := node.Obj().Keys()
		switch {
		case len(keys) == 0 || r.IntN(4) == 0:
			node.Obj().Set("zzUnknown", replacement)
		case r.IntN(2) == 0:
			node.Obj().Delete(keys[r.IntN(len(keys))])
		default:
			node.Obj().Set(keys[r.IntN(len(keys))], replacement)
		}
	case jsjson.Array:
		items := node.Items()
		if len(items) > 0 {
			items[r.IntN(len(items))] = replacement // arrays share backing storage with the tree
		}
	}
	return jsjson.Stringify(root)
}

// CorruptLeaf changes one scalar leaf or reverses one array, keeping JSON
// types. Leaves are found through nested objects and arrays, so a pick never
// lands on an object and leaves the input unchanged.
func CorruptLeaf(r *rand.Rand, text string) string {
	root, err := jsjson.Parse(text)
	if err != nil {
		return text
	}
	type leaf struct {
		get func() jsjson.Value
		set func(jsjson.Value)
	}
	var leaves []leaf
	var walk func(v jsjson.Value)
	walk = func(v jsjson.Value) {
		switch v.Kind() {
		case jsjson.Object:
			o := v.Obj()
			for _, k := range o.Keys() {
				child := o.Field(k)
				if child.Kind() != jsjson.Object {
					leaves = append(leaves, leaf{func() jsjson.Value { return o.Field(k) }, func(x jsjson.Value) { o.Set(k, x) }})
				}
				walk(child)
			}
		case jsjson.Array:
			items := v.Items()
			for i, child := range items {
				if child.Kind() != jsjson.Object {
					leaves = append(leaves, leaf{func() jsjson.Value { return items[i] }, func(x jsjson.Value) { items[i] = x }})
				}
				walk(child)
			}
		}
	}
	walk(root)
	if len(leaves) == 0 {
		return text
	}
	l := leaves[r.IntN(len(leaves))]
	switch v := l.get(); v.Kind() {
	case jsjson.String:
		l.set(jsjson.MakeString(pick(r, v.Str()+"x", "", "sha256:"+strings.Repeat("c", 64), "bad name", "IMG@sha256:"+strings.Repeat("d", 64), "FEATURE_FLAG", "lower")))
	case jsjson.Number:
		l.set(jsjson.MakeNumber(pick(r, v.Num()+1, 0, -1, 1.5, 1e12)))
	case jsjson.Bool:
		l.set(jsjson.MakeBool(!v.Bool()))
	case jsjson.Null:
		l.set(jsjson.MakeString(pick(r, "x", "/bin/sh", "none")))
	case jsjson.Array:
		items := v.Items()
		if len(items) < 2 {
			l.set(jsjson.MakeArray(append(slices.Clone(items), jsjson.MakeString(pick(r, "FEATURE_FLAG", "CI", "bad-name")))))
			break
		}
		for a, b := 0, len(items)-1; a < b; a, b = a+1, b-1 {
			items[a], items[b] = items[b], items[a]
		}
	}
	return jsjson.Stringify(root)
}

// SwapLeaf replaces one node with another node of the same JSON kind taken
// from elsewhere in the document, or duplicates, drops or swaps elements of
// one array. The result usually still matches the schema, so it reaches the
// cross-field checks (ids, digests, roles, counts) behind it.
func SwapLeaf(r *rand.Rand, text string) string {
	root, err := jsjson.Parse(text)
	if err != nil {
		return text
	}
	type node struct {
		key string // the member name, "" for an array element
		get func() jsjson.Value
		set func(jsjson.Value)
	}
	var nodes []node
	var walk func(v jsjson.Value)
	walk = func(v jsjson.Value) {
		switch v.Kind() {
		case jsjson.Object:
			o := v.Obj()
			for _, k := range o.Keys() {
				nodes = append(nodes, node{k, func() jsjson.Value { return o.Field(k) }, func(x jsjson.Value) { o.Set(k, x) }})
				walk(o.Field(k))
			}
		case jsjson.Array:
			items := v.Items()
			for i, child := range items {
				nodes = append(nodes, node{"", func() jsjson.Value { return items[i] }, func(x jsjson.Value) { items[i] = x }})
				walk(child)
			}
		}
	}
	walk(root)
	if len(nodes) == 0 {
		return text
	}
	n := nodes[r.IntN(len(nodes))]
	v := n.get()
	if v.Kind() == jsjson.Array && len(v.Items()) > 0 && r.IntN(2) == 0 {
		items := slices.Clone(v.Items())
		i := r.IntN(len(items))
		switch r.IntN(3) {
		case 0:
			items = slices.Insert(items, r.IntN(len(items)+1), items[i])
		case 1:
			items = slices.Delete(items, i, i+1)
		default:
			j := r.IntN(len(items))
			items[i], items[j] = items[j], items[i]
		}
		n.set(jsjson.MakeArray(items))
		return jsjson.Stringify(root)
	}
	// Mostly a value of a member with the same name (another run's role,
	// another certificate's direction), which keeps enums valid.
	text0 := jsjson.Stringify(v)
	var same, sameKey []jsjson.Value
	for _, other := range nodes {
		if w := other.get(); w.Kind() == v.Kind() && jsjson.Stringify(w) != text0 {
			same = append(same, w)
			if n.key != "" && other.key == n.key {
				sameKey = append(sameKey, w)
			}
		}
	}
	if len(sameKey) > 0 && r.IntN(4) != 0 {
		same = sameKey
	}
	if len(same) == 0 {
		return CorruptLeaf(r, text)
	}
	// Reparsed, so the copy does not share storage with its source.
	replacement, _ := jsjson.Parse(jsjson.Stringify(same[r.IntN(len(same))]))
	n.set(replacement)
	return jsjson.Stringify(root)
}
