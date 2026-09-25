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
