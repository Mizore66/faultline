package nativefixture

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"github.com/Mizore66/faultline/internal/jsjson"
)

// RawBlob retains original bytes losslessly, distinguishing complete captures
// from prefixes. Its hash is over those bytes, not their JSON representation.
func RawBlob(data []byte, complete bool) jsjson.Value {
	sum := sha256.Sum256(data)
	return jsjson.MustObject(
		"encoding", jsjson.MakeString("base64"),
		"data", jsjson.MakeString(base64.StdEncoding.EncodeToString(data)),
		"sha256", jsjson.MakeString(fmt.Sprintf("sha256:%x", sum)),
		"complete", jsjson.MakeBool(complete),
	)
}
