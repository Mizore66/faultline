package gitproof

import (
	"errors"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	s "github.com/Mizore66/faultline/internal/schema"
)

// errorSummary ports src/ledger.ts:328: `errors[0] ?? "invalid ledger"`
// applies only to a one-element list (whose element is never undefined), so
// an empty list joins to "".
func errorSummary(errs []string) string {
	if len(errs) == 1 {
		return errs[0]
	}
	return jsexc.Join(errs, "; ")
}

// ReadCodexLifecycleLedger ports readCodexLifecycleLedger (src/ledger.ts:637).
// Failures are LedgerIntegrityError messages; resolve may throw (jsexc).
func ReadCodexLifecycleLedger(filePath string) (jsjson.Value, error) {
	absolutePath := nodefs.Resolve(filePath)
	var parsedJSON jsjson.Value
	if err := jsexc.Try(func() { parsedJSON = bundle.Must(bundle.ParseJSONFile(absolutePath)) }); err != nil {
		return jsjson.Value{}, errors.New(jsexc.Concat("Unable to read lifecycle ledger ", absolutePath, ": ", jsexc.Message(err)))
	}
	parsed, issues, ok := s.Parse(CodexLifecycleLedgerSchema, parsedJSON)
	if !ok {
		lines := make([]string, len(issues))
		for i, issue := range issues {
			lines[i] = issuePath(issue) + ": " + issue.Fields.Field("message").Str()
		}
		return jsjson.Value{}, errors.New(jsexc.Concat("Lifecycle ledger ", absolutePath, " has invalid schema: ", errorSummary(lines)))
	}
	return parsed, nil
}

// ReadVerifiedCodexLifecycleLedger ports readVerifiedCodexLifecycleLedger
// (src/ledger.ts:653).
func ReadVerifiedCodexLifecycleLedger(filePath string) (jsjson.Value, error) {
	ledger, err := ReadCodexLifecycleLedger(filePath)
	if err != nil {
		return jsjson.Value{}, err
	}
	if verification := VerifyCodexLifecycleLedger(ledger); !verification.Valid {
		return jsjson.Value{}, errors.New(jsexc.Concat("Lifecycle ledger integrity check failed: ", errorSummary(verification.Errors)))
	}
	return ledger, nil
}
