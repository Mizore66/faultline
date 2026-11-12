package cli

import (
	"errors"
	"io"
	"math"
	"os"

	"github.com/Mizore66/faultline/internal/incident"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/proofview"
	"github.com/Mizore66/faultline/internal/term"
	"github.com/Mizore66/faultline/internal/witness"
)

// witnessReviewServer is what the review command needs from a started server.
type witnessReviewServer interface {
	URL() string
	Close() error
}

// Seams for tests; production values are the real implementations.
var (
	witnessReviewTerminals = func() (stdinTTY, stdoutTTY bool) {
		return term.IsTerminal(os.Stdin), term.IsTerminal(os.Stdout)
	}
	witnessReviewRunTTY = func(o WitnessReviewTTYOptions) error {
		_, _, _, err := RunWitnessReviewTTY(o)
		return err
	}
	witnessReviewStartServer = func(o proofview.WitnessReviewServerOptions, port float64) (witnessReviewServer, error) {
		return proofview.StartWitnessReviewServer(o, port)
	}
	witnessReviewAwait = awaitBundleInterrupt
)

const witnessReviewSensitivity = "This export contains the exact command and base64 overlay bytes. Treat it as sensitive incident material and do not publish it."

// witnessReviewCommand ports the `witness review` case of src/cli-app.ts:
// --json first, then port and draft-store, then TTY / server selection. No
// browser is launched and no stop summary is printed.
func witnessReviewCommand(e *env, args []string, proposalID, storeDirectory string) error {
	if proposalID == "" {
		return fail("Usage: fl witness review <proposal-id> [--json | --tty | --browser | --two-step | --port <number>] [--draft-store <directory>]")
	}
	if hasFlag(args, "--json") {
		review, err := witness.OpenWitnessReview(storeDirectory, proposalID)
		if err != nil {
			return err
		}
		e.printJSON(obj("status", "LOCAL_READ_ONLY_REVIEW", "review", review, "sensitivity", witnessReviewSensitivity))
		return nil
	}
	portText, portGiven := option(args, "--port")
	if !portGiven {
		portText = "0"
	}
	port := jsjson.ToNumber(portText)
	if math.IsNaN(port) || math.IsInf(port, 0) || port != math.Trunc(port) || port < 0 || port > 65535 {
		return fail("--port must be an integer from 0 through 65535.")
	}
	draftOption, ok := option(args, "--draft-store")
	if !ok {
		draftOption = witness.DefaultIncidentDraftStore(jsexc.Must(nodefs.Cwd()))
	}
	draftStore := nodefs.Resolve(draftOption)
	forceTTY := hasFlag(args, "--tty")
	forceBrowser := hasFlag(args, "--browser") || portGiven
	twoStep := hasFlag(args, "--two-step") || os.Getenv("FAULTLINE_WITNESS_TWO_STEP") == "1"
	stdinTTY, stdoutTTY := witnessReviewTerminals()
	serverOptions := proofview.WitnessReviewServerOptions{StoreDirectory: storeDirectory, ProposalID: proposalID, DraftStore: draftStore, TwoStep: twoStep}

	if forceTTY || (!forceBrowser && stdinTTY && stdoutTTY) {
		// The TTY catches every draft-read error: no linked draft.
		var draft *jsjson.Value
		if read, err := incident.ReadDraft(draftStore, proposalID); err == nil {
			draft = &read
		}
		options := WitnessReviewTTYOptions{StoreDirectory: storeDirectory, ProposalID: proposalID, Draft: draft,
			Stdin: e.stdin, Stdout: e.stdout, StdinIsTTY: stdinTTY, StdoutIsTTY: stdoutTTY}
		if options.Stdin == nil {
			options.Stdin = os.Stdin
		}
		err := witnessReviewRunTTY(options)
		if err == nil {
			return nil
		}
		var ttyErr *WitnessReviewTTYError
		if !errors.As(err, &ttyErr) || ttyErr.Code != "NON_TTY" {
			return err
		}
		err = witnessReviewAwait(func() (func() error, error) {
			server, err := witnessReviewStartServer(serverOptions, port)
			if err != nil {
				return nil, err
			}
			io.WriteString(e.stderr, jsstr.ToUTF8(NonTtyReviewRefusalMessage(server.URL())+"\n"))
			e.out("FaultLine local witness review: " + server.URL() + "\nPress Ctrl+C to stop.\n")
			return server.Close, nil
		})
		if err != nil {
			return err
		}
		e.exitCode = 1
		return nil
	}
	return witnessReviewAwait(func() (func() error, error) {
		server, err := witnessReviewStartServer(serverOptions, port)
		if err != nil {
			return nil, err
		}
		if twoStep {
			e.out("FaultLine local witness review: " + server.URL() + "\nTwo-step mode: Approve, then Freeze (separate clicks). Press Ctrl+C to stop.\n")
		} else {
			e.out("FaultLine local witness review: " + server.URL() + "\nApprove & freeze is one action. Press Ctrl+C to stop.\n")
		}
		return server.Close, nil
	})
}
