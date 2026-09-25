package gen

import (
	"math/rand/v2"
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

// CorruptLeaf changes one scalar leaf or reverses one array, keeping JSON types.
func CorruptLeaf(r *rand.Rand, text string) string {
	root, err := jsjson.Parse(text)
	if err != nil {
		return text
	}
	var parents []jsjson.Value
	var keys []string
	var walk func(v jsjson.Value)
	walk = func(v jsjson.Value) {
		if v.Kind() != jsjson.Object {
			return
		}
		for _, k := range v.Obj().Keys() {
			parents = append(parents, v)
			keys = append(keys, k)
			walk(v.Obj().Field(k))
		}
	}
	walk(root)
	if len(keys) == 0 {
		return text
	}
	i := r.IntN(len(keys))
	parent, key := parents[i].Obj(), keys[i]
	switch leaf := parent.Field(key); leaf.Kind() {
	case jsjson.String:
		parent.Set(key, jsjson.MakeString(pick(r, leaf.Str()+"x", "", "sha256:"+strings.Repeat("c", 64), "bad name", "IMG@sha256:"+strings.Repeat("d", 64))))
	case jsjson.Number:
		parent.Set(key, jsjson.MakeNumber(pick(r, leaf.Num()+1, 0, -1, 1.5, 1e12)))
	case jsjson.Bool:
		parent.Set(key, jsjson.MakeBool(!leaf.Bool()))
	case jsjson.Array:
		items := leaf.Items()
		for a, b := 0, len(items)-1; a < b; a, b = a+1, b-1 {
			items[a], items[b] = items[b], items[a]
		}
	}
	return jsjson.Stringify(root)
}
