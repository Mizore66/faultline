package guided

import (
	"context"
	"errors"
	"time"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/nodeproc"
	"github.com/Mizore66/faultline/internal/proofview"
	"github.com/Mizore66/faultline/internal/witness"
)

// FreezeOptions configures WaitForFreeze (defaultWaitForGuidedFreeze).
type FreezeOptions struct {
	ProposalID, WitnessStore, DraftStore string
	Port                                 *float64
	PollInterval                         *time.Duration
	OnReviewReady                        func(string)
	// Signal is consumed only by this waiter; nil is a background context.
	Signal context.Context
}

// FreezeResult is the observed write-once freeze.
type FreezeResult struct {
	FrozenDigest  string
	ReviewURL     *string
	AlreadyFrozen bool
}

const freezePollInterval = 500 * time.Millisecond

// newFreezeCancelledError builds a fresh error per cancellation, as the
// frozen JS constructs a new Error each time.
func newFreezeCancelledError() error {
	return errors.New("Guided investigate cancelled before freeze.")
}

type freezeServer interface {
	URL() string
	Close() error
}

// freezeDeps are the deterministic-ordering seams; defaults use the real
// verifier, review server and timer.
type freezeDeps struct {
	verify func(store, proposalID string) gitproof.WitnessVerification
	start  func(o proofview.WitnessReviewServerOptions, port float64) (freezeServer, error)
	sleep  func(ctx context.Context, d time.Duration) error
}

func defaultFreezeDeps() freezeDeps {
	return freezeDeps{
		verify: func(store, id string) gitproof.WitnessVerification {
			return witness.VerifyFrozen(store, id, "", false)
		},
		start: func(o proofview.WitnessReviewServerOptions, port float64) (freezeServer, error) {
			s, err := proofview.StartWitnessReviewServer(o, port)
			if err != nil {
				return nil, err
			}
			return s, nil
		},
		sleep: freezeSleep,
	}
}

// freezeSleep waits d, failing with the cancellation error as soon as ctx is
// done (including before it starts).
func freezeSleep(ctx context.Context, d time.Duration) error {
	if ctx.Err() != nil {
		return newFreezeCancelledError()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return newFreezeCancelledError()
	case <-timer.C:
		return nil
	}
}

func frozenDigestOf(v gitproof.WitnessVerification) (string, bool) {
	if v.Valid && v.FrozenDigest != nil {
		return *v.FrozenDigest, true
	}
	return "", false
}

// WaitForFreeze opens the local review workbench and polls until a human has
// approved and frozen the proposal. It never approves or freezes itself.
func WaitForFreeze(o FreezeOptions) (FreezeResult, error) {
	return waitForFreeze(o, defaultFreezeDeps())
}

func waitForFreeze(o FreezeOptions, d freezeDeps) (result FreezeResult, err error) {
	if digest, ok := frozenDigestOf(d.verify(o.WitnessStore, o.ProposalID)); ok {
		return FreezeResult{FrozenDigest: digest, AlreadyFrozen: true}, nil
	}
	ctx := o.Signal
	if ctx == nil {
		ctx = context.Background()
	}
	port := 0.0
	if o.Port != nil {
		port = *o.Port
	}
	twoStep := false
	if v, ok := nodeproc.Getenv("FAULTLINE_WITNESS_TWO_STEP"); ok && v == "1" {
		twoStep = true
	}
	server, startErr := d.start(proofview.WitnessReviewServerOptions{
		StoreDirectory: o.WitnessStore, ProposalID: o.ProposalID, DraftStore: o.DraftStore, TwoStep: twoStep,
	}, port)
	if startErr != nil {
		return FreezeResult{}, startErr
	}
	url := server.URL()
	if o.OnReviewReady != nil {
		// Outside the cleanup region: a throwing callback rejects without
		// closing the already-started server (frozen TS behavior).
		if cbErr := jsexc.Try(func() { o.OnReviewReady(url) }); cbErr != nil {
			return FreezeResult{}, cbErr
		}
	}
	poll := freezePollInterval
	if o.PollInterval != nil {
		poll = *o.PollInterval
	}
	// JS try/finally: a close failure replaces the pending return or error.
	defer func() {
		if closeErr := server.Close(); closeErr != nil {
			result, err = FreezeResult{}, closeErr
		}
	}()
	for {
		if ctx.Err() != nil {
			return FreezeResult{}, newFreezeCancelledError()
		}
		if digest, ok := frozenDigestOf(d.verify(o.WitnessStore, o.ProposalID)); ok {
			return FreezeResult{FrozenDigest: digest, ReviewURL: &url}, nil
		}
		if serr := d.sleep(ctx, poll); serr != nil {
			return FreezeResult{}, serr
		}
	}
}
