// Package nodefs reproduces the Node fs/path behavior the verifiers observe.
package nodefs

import "github.com/Mizore66/faultline/internal/jsstr"

// DecodeUTF8 is Buffer.toString("utf8") (WHATWG UTF-8 decode with replacement).
func DecodeUTF8(b []byte) string { return jsstr.DecodeUTF8(b) }
