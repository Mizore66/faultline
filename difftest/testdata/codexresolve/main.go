// Command codexresolve is TestLiveResolveCodexRunner's Go child: it runs
// codexloop.ResolveRunner with the cwd, PATH and FAULTLINE_CODEX_BIN it was
// started with and prints the resolution or the error message as JSON, as
// the oracle's resolveCodexRunner op does for frozen TS.
package main

import (
	"os"

	"github.com/Mizore66/faultline/internal/codexloop"
	"github.com/Mizore66/faultline/internal/jsjson"
)

func main() {
	var out jsjson.Value
	if r, err := codexloop.ResolveRunner(); err != nil {
		out = jsjson.MustObject("error", jsjson.MakeString(err.Error()))
	} else {
		out = jsjson.MustObject("resolution", jsjson.MustObject(
			"bin", jsjson.MakeString(r.Bin),
			"version", jsjson.MakeString(r.Version),
			"source", jsjson.MakeString(r.Source),
		))
	}
	os.Stdout.WriteString(jsjson.Stringify(out))
}
