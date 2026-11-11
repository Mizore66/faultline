package cli

import (
	"math"
	"strconv"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/proofview"
)

// uiServer is what uiCommand needs from a started hub.
type uiServer interface {
	URL() string
	Close() error
	Artifacts() []proofview.DiscoveredFaultLineArtifact
}

// Seams for tests; production values are the real implementations.
var (
	uiStartServer = func(repository string, port float64) (uiServer, error) {
		return proofview.StartUIHubServer(repository, port)
	}
	uiOpenBrowser    = openLocalDemoURL
	uiAwaitInterrupt = awaitBundleInterrupt
)

// uiCommand is the `ui` case of src/cli-app.ts: one discovery at startup, the
// three readiness lines, the next command on stderr, the browser opener, then
// wait for SIGINT.
func uiCommand(e *env, args []string) error {
	// resolve(undefined ?? cwd) and resolve("") are both the cwd.
	raw, _ := option(args, "--repo")
	repository := nodefs.Resolve(raw)
	text, ok := option(args, "--port")
	if !ok {
		text = "4173"
	}
	port := jsjson.ToNumber(text)
	if math.IsNaN(port) || math.IsInf(port, 0) || port != math.Trunc(port) || port < 0 || port > 65535 {
		return fail("--port must be an integer from 0 through 65535.")
	}
	return uiAwaitInterrupt(func() (func() error, error) {
		hub, err := uiStartServer(repository, port)
		if err != nil {
			return nil, err
		}
		count := len(hub.Artifacts())
		e.out("FaultLine UI hub: " + hub.URL() + "\nDiscovered " + strconv.Itoa(count) + " local artifact(s) under .faultline\nPress Ctrl+C to stop.\n")
		if count == 0 {
			e.printHumanNext(proofview.UIHubEmptyNextCommand)
		} else {
			e.printHumanNext("fl ui --repo " + repository)
		}
		uiOpenBrowser(e, hub.URL())
		return hub.Close, nil
	})
}
