// Package schemadsl builds internal/schema values from the test DSL that
// difftest/gen/schema-dsl.ts also understands.
package schemadsl

import (
	"fmt"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/schema"
)

var regexes = map[string]schema.Matcher{
	"sha256":      schema.Pattern(`^sha256:[a-f0-9]{64}$`),
	"gitObjectId": schema.Pattern(`^(?:[a-f0-9]{40}|[a-f0-9]{64})$`),
	"identifier":  schema.Pattern(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`),
}

func Build(dsl jsjson.Value) schema.Schema {
	o := dsl.Obj()
	checks := o.Field("checks").Items()
	switch t := o.Field("t").Str(); t {
	case "string":
		var cs []schema.StringCheck
		for _, c := range checks {
			switch c.Obj().Field("k").Str() {
			case "trim":
				cs = append(cs, schema.Trim())
			case "min":
				cs = append(cs, schema.MinLength(int(c.Obj().Field("n").Num())))
			case "max":
				cs = append(cs, schema.MaxLength(int(c.Obj().Field("n").Num())))
			case "regex":
				cs = append(cs, schema.Regex(regexes[c.Obj().Field("name").Str()], c.Obj().Field("msg").Str()))
			case "datetime":
				cs = append(cs, schema.Datetime())
			case "uuid":
				cs = append(cs, schema.UUID())
			}
		}
		return schema.String(cs...)
	case "number":
		var cs []schema.NumberCheck
		for _, c := range checks {
			switch c.Obj().Field("k").Str() {
			case "int":
				cs = append(cs, schema.Int())
			case "positive":
				cs = append(cs, schema.Positive())
			case "nonnegative":
				cs = append(cs, schema.Nonnegative())
			case "min":
				cs = append(cs, schema.Gte(c.Obj().Field("n").Num()))
			case "max":
				cs = append(cs, schema.Lte(c.Obj().Field("n").Num()))
			}
		}
		return schema.Number(cs...)
	case "boolean":
		return schema.Boolean()
	case "literal":
		return schema.Literal(o.Field("v"))
	case "enum":
		var values []string
		for _, v := range o.Field("values").Items() {
			values = append(values, v.Str())
		}
		return schema.Enum(values...)
	case "array":
		var cs []schema.ArrayCheck
		for _, c := range checks {
			n := int(c.Obj().Field("n").Num())
			switch c.Obj().Field("k").Str() {
			case "length":
				cs = append(cs, schema.Length(n))
			case "min":
				cs = append(cs, schema.MinItems(n))
			case "max":
				cs = append(cs, schema.MaxItems(n))
			}
		}
		return schema.Array(Build(o.Field("item")), cs...)
	case "record":
		return schema.Record(Build(o.Field("value")))
	case "optional":
		return schema.Optional(Build(o.Field("inner")))
	case "nullable":
		return schema.Nullable(Build(o.Field("inner")))
	case "object":
		return buildObject(dsl)
	case "disc":
		var options []*schema.ObjectSchema
		for _, opt := range o.Field("options").Items() {
			options = append(options, buildObject(opt))
		}
		return schema.DiscriminatedUnion(o.Field("key").Str(), options...)
	case "refine":
		pred := func(jsjson.Value) bool { return false }
		if o.Field("pred").Str() == "nonEmpty" {
			pred = func(v jsjson.Value) bool { return v.Kind() == jsjson.String && v.Str() != "" }
		}
		return schema.Refine(Build(o.Field("inner")), pred, o.Field("msg").Str())
	case "default":
		return schema.Default(Build(o.Field("inner")), o.Field("v"))
	default:
		panic(fmt.Sprintf("unknown DSL node %q", t))
	}
}

func buildObject(dsl jsjson.Value) *schema.ObjectSchema {
	var fields []schema.Field
	for _, pair := range dsl.Obj().Field("shape").Items() {
		fields = append(fields, schema.Field{Key: pair.Items()[0].Str(), Schema: Build(pair.Items()[1])})
	}
	obj := schema.Object(fields...)
	if dsl.Obj().Field("strict").Bool() {
		return obj.Strict()
	}
	return obj
}
