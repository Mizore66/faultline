package cli

import (
	"strconv"

	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/proofview"
)

// judgePreviewCommand is the judge-preview case of src/cli-app.ts. Option
// parsing matches commit-proof-preview: the first --output is used, a
// trailing one is ignored, and --output=value is not recognized.
func judgePreviewCommand(e *env, args []string) error {
	output := proofview.DefaultJudgePreviewPath()
	if _, ok := option(args, "--output"); ok {
		value, err := requiredOption(args, "--output")
		if err != nil {
			return err
		}
		output = nodefs.Resolve(value)
	}
	written, err := proofview.WriteJudgePreview(output)
	if err != nil {
		return err
	}
	e.out("FaultLine deterministic static judge preview written.\n")
	e.out("Preview: " + written.Path + "\n")
	e.out("Bytes: " + strconv.Itoa(written.Bytes) + "\n")
	e.out("Limitation: this read-only replay snapshot is not a live Docker proof, a verified proof bundle, or a record of a fresh execution.\n")
	return nil
}
