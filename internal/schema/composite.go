package schema

import (
	"slices"

	"github.com/Mizore66/faultline/internal/jsjson"
)

type Field struct {
	Key    string
	Schema Schema
}

// F builds a Field; ported schemas use it to stay go-vet clean.
func F(key string, s Schema) Field { return Field{Key: key, Schema: s} }

type ObjectSchema struct {
	fields []Field
	strict bool
}

// Object is z.object(shape). zod walks Object.keys(shape), so fields are kept
// in JS property order: array-index names first (ascending), then the rest in
// declaration order; a repeated name keeps its first position, last schema.
func Object(fields ...Field) *ObjectSchema { return &ObjectSchema{fields: jsKeyOrder(fields)} }

func jsKeyOrder(fields []Field) []Field {
	shape := jsjson.NewObj()
	for i, f := range fields {
		shape.Set(f.Key, jsjson.MakeNumber(float64(i)))
	}
	out := make([]Field, 0, shape.Len())
	for _, k := range shape.Keys() {
		out = append(out, fields[int(shape.Field(k).Num())])
	}
	return out
}

func (o *ObjectSchema) Strict() *ObjectSchema {
	return &ObjectSchema{fields: slices.Clone(o.fields), strict: true}
}

// Extend is zod .extend: {...shape, ...augmentation}. Existing keys keep their
// position with the new schema; new keys are appended. Strictness is kept.
func (o *ObjectSchema) Extend(fields ...Field) *ObjectSchema {
	out := &ObjectSchema{fields: slices.Clone(o.fields), strict: o.strict}
	for _, f := range fields {
		if i := slices.IndexFunc(out.fields, func(e Field) bool { return e.Key == f.Key }); i >= 0 {
			out.fields[i] = f
		} else {
			out.fields = append(out.fields, f)
		}
	}
	out.fields = jsKeyOrder(out.fields)
	return out
}

func (o *ObjectSchema) field(key string) Schema {
	for _, f := range o.fields {
		if f.Key == key {
			return f.Schema
		}
	}
	return nil
}

func (o *ObjectSchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	if v.Kind() != jsjson.Object {
		c.invalidType(path, "object", v)
		return jsjson.Value{}, aborted
	}
	in := v.Obj()
	out := jsjson.NewObj()
	st := valid
	for _, f := range o.fields {
		child, childStatus := f.Schema.parse(c, in.Field(f.Key), extend(path, f.Key))
		st = worst(st, childStatus)
		if child.Kind() != jsjson.Undefined {
			out.Set(f.Key, child)
		}
	}
	if o.strict {
		var extra []string // for-in order: JS property order
		for _, k := range in.Keys() {
			if o.field(k) == nil {
				extra = append(extra, k)
			}
		}
		if len(extra) > 0 {
			c.addIssue(path, []kv{{"code", str("unrecognized_keys")}, {"keys", strList(extra)}}, "Unrecognized key(s) in object: "+joinValues(extra, ", "))
			st = worst(st, dirty)
		}
	}
	if st == aborted {
		return jsjson.Value{}, aborted
	}
	return jsjson.MakeObject(out), st
}

// ArrayCheck is one of zod's array length calls. .length, .min and .max each
// overwrite a single setting (the last call wins), and ZodArray._parse checks
// exact length, then min, then max, whatever the call order.
type ArrayCheck struct {
	kind byte // 'e'xact, 'm'in, 'M'ax
	n    int
}

func Length(n int) ArrayCheck   { return ArrayCheck{'e', n} }
func MinItems(n int) ArrayCheck { return ArrayCheck{'m', n} }
func MaxItems(n int) ArrayCheck { return ArrayCheck{'M', n} }

type arraySchema struct {
	item                  Schema
	exact, minLen, maxLen *int
}

func Array(item Schema, checks ...ArrayCheck) Schema {
	a := &arraySchema{item: item}
	for _, check := range checks {
		n := check.n
		switch check.kind {
		case 'e':
			a.exact = &n
		case 'm':
			a.minLen = &n
		case 'M':
			a.maxLen = &n
		}
	}
	return a
}

func (a *arraySchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	if v.Kind() != jsjson.Array {
		c.invalidType(path, "array", v)
		return jsjson.Value{}, aborted
	}
	n := len(v.Items())
	st := valid
	if a.exact != nil {
		if n < *a.exact {
			c.tooSmall(path, "array", float64(*a.exact), true, true)
			st = dirty
		} else if n > *a.exact {
			c.tooBig(path, "array", float64(*a.exact), true, true)
			st = dirty
		}
	}
	if a.minLen != nil && n < *a.minLen {
		c.tooSmall(path, "array", float64(*a.minLen), true, false)
		st = dirty
	}
	if a.maxLen != nil && n > *a.maxLen {
		c.tooBig(path, "array", float64(*a.maxLen), true, false)
		st = dirty
	}
	items := make([]jsjson.Value, n)
	for i, item := range v.Items() {
		out, itemStatus := a.item.parse(c, item, extend(path, i))
		st = worst(st, itemStatus)
		items[i] = out
	}
	if st == aborted {
		return jsjson.Value{}, aborted
	}
	return jsjson.MakeArray(items), st
}

type recordSchema struct{ value Schema }

func Record(value Schema) Schema { return &recordSchema{value} }

func (r *recordSchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	if v.Kind() != jsjson.Object {
		c.invalidType(path, "object", v)
		return jsjson.Value{}, aborted
	}
	out := jsjson.NewObj()
	st := valid
	for _, k := range v.Obj().Keys() {
		child, childStatus := r.value.parse(c, v.Obj().Field(k), extend(path, k))
		st = worst(st, childStatus)
		if k != "__proto__" {
			out.Set(k, child)
		}
	}
	if st == aborted {
		return jsjson.Value{}, aborted
	}
	return jsjson.MakeObject(out), st
}

type optionalSchema struct{ inner Schema }

func Optional(inner Schema) Schema { return &optionalSchema{inner} }

func (o *optionalSchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	if v.Kind() == jsjson.Undefined {
		return v, valid
	}
	return o.inner.parse(c, v, path)
}

type nullableSchema struct{ inner Schema }

func Nullable(inner Schema) Schema { return &nullableSchema{inner} }

func (n *nullableSchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	if v.Kind() == jsjson.Null {
		return v, valid
	}
	return n.inner.parse(c, v, path)
}

type discriminatedSchema struct {
	key     string
	options []*ObjectSchema
	values  []string
}

// DiscriminatedUnion requires each option's discriminator to be a string literal.
func DiscriminatedUnion(key string, options ...*ObjectSchema) Schema {
	d := &discriminatedSchema{key: key, options: options}
	for _, o := range options {
		d.values = append(d.values, o.field(key).(*literalSchema).value.Str())
	}
	return d
}

func (d *discriminatedSchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	if v.Kind() != jsjson.Object {
		c.invalidType(path, "object", v)
		return jsjson.Value{}, aborted
	}
	disc := v.Obj().Field(d.key)
	for i, value := range d.values {
		if disc.Kind() == jsjson.String && disc.Str() == value {
			return d.options[i].parse(c, v, path)
		}
	}
	c.addIssue(extend(path, d.key), []kv{{"code", str("invalid_union_discriminator")}, {"options", strList(d.values)}},
		"Invalid discriminator value. Expected "+joinValues(d.values, " | "))
	return jsjson.Value{}, aborted
}

type refineSchema struct {
	inner   Schema
	pred    func(jsjson.Value) bool
	message string
}

func Refine(inner Schema, pred func(jsjson.Value) bool, message string) Schema {
	return &refineSchema{inner, pred, message}
}

func (r *refineSchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	out, st := r.inner.parse(c, v, path)
	if st == aborted {
		return jsjson.Value{}, aborted
	}
	if !r.pred(out) {
		c.addIssue(path, []kv{{"code", str("custom")}, {"message", str(r.message)}}, "Invalid input")
		st = dirty
	}
	return out, st
}

type defaultSchema struct {
	inner Schema
	value jsjson.Value
}

func Default(inner Schema, v jsjson.Value) Schema { return &defaultSchema{inner, v} }

func (d *defaultSchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	if v.Kind() == jsjson.Undefined {
		v = d.value
	}
	return d.inner.parse(c, v, path)
}
