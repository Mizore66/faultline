// Package canonical ports src/canonical.ts.
package canonical

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
)

// normalize ports `normalize` in src/canonical.ts. The rebuilt object is a JS
// object, so array-index keys still come first (jsjson.Obj keeps that order).
func normalize(v jsjson.Value) (jsjson.Value, error) {
	switch v.Kind() {
	case jsjson.Array:
		items := make([]jsjson.Value, len(v.Items()))
		for i, item := range v.Items() {
			n, err := normalize(item)
			if err != nil {
				return jsjson.Value{}, err
			}
			items[i] = n
		}
		return jsjson.MakeArray(items), nil
	case jsjson.Object:
		var keys []string
		for _, k := range v.Obj().Keys() {
			if v.Obj().Field(k).Kind() != jsjson.Undefined {
				keys = append(keys, k)
			}
		}
		out := jsjson.NewObj()
		for _, k := range SortLocale(keys) {
			n, err := normalize(v.Obj().Field(k))
			if err != nil {
				return jsjson.Value{}, err
			}
			// TS assigns `result[key] = …`, so "__proto__" hits the prototype
			// setter and never becomes an own property.
			if k == "__proto__" {
				continue
			}
			out.Set(k, n)
		}
		return jsjson.MakeObject(out), nil
	case jsjson.Number:
		if f := v.Num(); f-f != 0 { // NaN or ±Inf
			return jsjson.Value{}, errors.New("Value is not finite JSON: number")
		}
		return v, nil
	case jsjson.Undefined:
		return jsjson.Value{}, errors.New("Value is not JSON-serializable: undefined")
	}
	return v, nil
}

// CanonicalJSON ports canonicalJson.
func CanonicalJSON(v jsjson.Value) (string, error) {
	n, err := normalize(v)
	if err != nil {
		return "", err
	}
	return jsjson.Stringify(n), nil
}

// SHA256Hex ports sha256(string): Node hashes the UTF-8 encoding of the JS string.
func SHA256Hex(s string) string { return SHA256HexBytes([]byte(jsstr.ToUTF8(s))) }

// SHA256HexBytes ports sha256(Buffer).
func SHA256HexBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// DigestJSON ports digestJson.
func DigestJSON(v jsjson.Value) (string, error) {
	c, err := CanonicalJSON(v)
	if err != nil {
		return "", err
	}
	return "sha256:" + SHA256Hex(c), nil
}
