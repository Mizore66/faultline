package cli

import (
	"bufio"
	"errors"
	"io"
	"os"

	"github.com/Mizore66/faultline/internal/jscase"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/proofview"
	"github.com/Mizore66/faultline/internal/term"
	"github.com/Mizore66/faultline/internal/witness"
)

// WitnessReviewTTYError ports WitnessReviewTtyError.
type WitnessReviewTTYError struct{ Code, Message string }

func (e *WitnessReviewTTYError) Error() string { return e.Message }

// WitnessReviewTTYOptions is the input of RunWitnessReviewTTY. The TTY flags
// are supplied by the caller (from internal/term) so tests can inject them.
type WitnessReviewTTYOptions struct {
	StoreDirectory, ProposalID             string
	Draft                                  *jsjson.Value
	ApprovedBy, Note, ApprovedAt, FrozenAt *string
	Stdin                                  io.Reader
	Stdout                                 io.Writer
	StdinIsTTY, StdoutIsTTY                bool
}

// NonTtyReviewRefusalMessage ports nonTtyReviewRefusalMessage.
func NonTtyReviewRefusalMessage(reviewURL string) string {
	urlLine := "Open the local review URL: " + reviewURL
	if reviewURL == "" {
		urlLine = "Start `fl witness review <id> --browser` on an interactive terminal and open the printed URL."
	}
	return "Refusing non-interactive approval. " + urlLine + " FaultLine never auto-approves under yes| piping."
}

// RunWitnessReviewTTY ports runWitnessReviewTty: render the review, require an
// explicit y/yes bound to the reviewer identity, then approve and freeze
// atomically. Anything else, including EOF, writes nothing.
func RunWitnessReviewTTY(options WitnessReviewTTYOptions) (approval, frozen jsjson.Value, approvedBy string, err error) {
	fail := func(code, message string) (jsjson.Value, jsjson.Value, string, error) {
		return jsjson.Value{}, jsjson.Value{}, "", &WitnessReviewTTYError{code, message}
	}
	if !options.StdinIsTTY {
		return fail("NON_TTY", "TTY-native approval requires an interactive terminal. Open the local review URL instead; FaultLine will not auto-approve under piped stdin.")
	}
	if !options.StdoutIsTTY {
		return fail("NON_TTY", "TTY-native approval requires an interactive stdout. Open the local review URL instead.")
	}
	review, err := witness.OpenWitnessReview(options.StoreDirectory, options.ProposalID)
	if err != nil {
		return jsjson.Value{}, jsjson.Value{}, "", err
	}
	if _, err := io.WriteString(options.Stdout, proofview.RenderWitnessReviewText(review, options.Draft)); err != nil {
		return jsjson.Value{}, jsjson.Value{}, "", err
	}
	identity := ""
	if options.ApprovedBy != nil {
		identity = *options.ApprovedBy
	} else {
		identity = proofview.ReadGitUserName()
	}
	identity = jsstr.Trim(identity)
	if identity == "" {
		return fail("EMPTY_ANSWER", "Reviewer identity is empty.")
	}
	answer, err := askApproval(options, `Approve & freeze as "`+identity+`"? [y/N] `)
	if err != nil {
		return jsjson.Value{}, jsjson.Value{}, "", err
	}
	if answer != "y" && answer != "yes" {
		return fail("DECLINED", "Approval declined; no records were written.")
	}
	in := jsjson.NewObj()
	in.Set("reviewDigest", review.Get("reviewDigest"))
	in.Set("approvedBy", jsjson.MakeString(identity))
	for _, f := range []struct {
		key string
		val *string
	}{{"note", options.Note}, {"approvedAt", options.ApprovedAt}, {"frozenAt", options.FrozenAt}} {
		if f.val != nil {
			in.Set(f.key, jsjson.MakeString(*f.val))
		}
	}
	approval, frozen, err = witness.ApproveAndFreezeWitnessReview(options.StoreDirectory, review, jsjson.MakeObject(in))
	if err != nil {
		return jsjson.Value{}, jsjson.Value{}, "", err
	}
	summary := "Frozen " + frozen.Get("proposal", "proposalId").Str() + " · approvedBy " +
		approval.Get("approvedBy").Str() + " · frozenDigest " + frozen.Get("frozenDigest").Str() + "\n"
	if _, err := io.WriteString(options.Stdout, summary); err != nil {
		return jsjson.Value{}, jsjson.Value{}, "", err
	}
	return approval, frozen, identity, nil
}

// askApproval asks the question. On a real terminal stdin it uses raw mode
// and the byte-exact Node readline model (nodeReadlineQuestion); otherwise
// (injected readers, Windows console) it prints the prompt once and reads one
// line. readline/promises.question never settles for an unterminated final
// line at EOF, so only a newline-terminated line can answer.
func askApproval(options WitnessReviewTTYOptions, prompt string) (string, error) {
	if f, ok := options.Stdin.(*os.File); ok && options.StdinIsTTY {
		if restore, raw := term.MakeRaw(f); raw {
			defer restore()
			line, answered, err := nodeReadlineQuestion(f, options.Stdout, prompt)
			if err != nil {
				return "", err
			}
			if !answered {
				return "", nil
			}
			return jscase.Lower(jsstr.Trim(line)), nil
		}
	}
	if _, err := io.WriteString(options.Stdout, prompt); err != nil {
		return "", err
	}
	line, readErr := bufio.NewReader(options.Stdin).ReadString('\n')
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return "", readErr
	}
	if readErr == nil {
		return jscase.Lower(jsstr.Trim(line)), nil
	}
	return "", nil
}
