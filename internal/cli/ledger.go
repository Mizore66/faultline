package cli

import (
	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/ledgerbind"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// ledgerCommand ports ledgerCommand (src/cli-app.ts:2412).
func ledgerCommand(e *env, args []string) error {
	var action, first string
	if len(args) > 0 {
		action = args[0]
	}
	if len(args) > 1 {
		first = args[1]
	}
	switch action {
	case "bind":
		ledgerPath, err := requiredOption(args, "--ledger")
		if err != nil {
			return err
		}
		ledger, err := gitproof.ReadVerifiedCodexLifecycleLedger(nodefs.Resolve(ledgerPath))
		if err != nil {
			return err
		}
		investigationPath, err := requiredOption(args, "--investigation")
		if err != nil {
			return err
		}
		input, err := readJSONInput(investigationPath)
		if err != nil {
			return err
		}
		investigation, err := bundle.ParseValue(input, gitproof.GitInvestigationResultSchema)
		if err != nil {
			return err
		}
		binding, err := ledgerbind.Create(ledger, investigation, ledgerPath)
		if err != nil {
			return err
		}
		outputPath, err := requiredOption(args, "--output")
		if err != nil {
			return err
		}
		path, err := ledgerbind.WriteAtomic(nodefs.Resolve(outputPath), binding)
		if err != nil {
			return err
		}
		e.printJSON(obj("status", "BOUND", "path", path, "bindingDigest", binding.Get("bindingDigest"),
			"stateCount", jsjson.MakeNumber(float64(len(binding.Get("stateBindings").Items())))))
		return nil
	case "verify":
		if first == "" {
			return fail("Usage: fl ledger verify <binding.json> [--expect-digest <sha256:...>]")
		}
		expected, provided := option(args, "--expect-digest")
		verification := ledgerbind.VerifyFile(nodefs.Resolve(first), expected, provided)
		e.printJSON(verification)
		if verification.Get("valid").Bool() {
			e.exitCode = 0
		} else {
			e.exitCode = 1
		}
		return nil
	default:
		return fail("Usage: fl ledger bind|verify ...")
	}
}
