package cli

import (
	"github.com/Mizore66/faultline/internal/bundle/demo"
	"github.com/Mizore66/faultline/internal/demofixture"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/proofview"
)

// Seams for tests; production values are the real implementations.
var (
	plainServeWriteBundle    = demo.Write
	plainServeStartServer    = startPlainServer
	plainServeAwaitInterrupt = awaitBundleInterrupt
	plainServeAssertOutput   = demo.AssertSafeProofOutput
)

// plainServer is what plainServeCommand needs from a started server.
type plainServer interface {
	URL() string
	Close() error
}

func startPlainServer(options proofview.PlainDemoServerOptions, port float64) (plainServer, error) {
	return proofview.StartPlainDemoServer(options, port)
}

// plainServeCommand is the bare `serve` case of src/cli-app.ts: the output is
// checked before listening, the REPLAY analysis is served without writing any
// bundle, and each RERUN writes a fresh bundle before replacing the analysis.
func plainServeCommand(e *env, args []string) error {
	raw, ok := option(args, "--output")
	if !ok {
		raw = ".faultline/bundles/judge-demo"
	}
	var output string
	if err := jsexc.Try(func() {
		output = plainServeAssertOutput(nodefs.Resolve(raw))
	}); err != nil {
		return err
	}
	port, ok := option(args, "--port")
	if !ok {
		port = "4173"
	}
	options := proofview.PlainDemoServerOptions{
		Analysis: demofixture.CreateReplayAnalysis(),
		Rerun: func() (jsjson.Value, error) {
			analysis := demofixture.CreateRerunAnalysis()
			if _, err := plainServeWriteBundle(output, analysis, demo.WriteOptions{}); err != nil {
				return jsjson.Value{}, err
			}
			return analysis, nil
		},
	}
	return plainServeAwaitInterrupt(func() (func() error, error) {
		server, err := plainServeStartServer(options, jsjson.ToNumber(port))
		if err != nil {
			return nil, err
		}
		e.out("FaultLine incident page: " + server.URL() + "\nPress Ctrl+C to stop.\n")
		return server.Close, nil
	})
}
