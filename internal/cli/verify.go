package cli

import (
	"fmt"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/demo"
	"github.com/Mizore66/faultline/internal/bundle/prevention"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

func verifyCommand(e *env, args []string) error {
	if len(args) == 0 || args[0] == "" {
		return fail("Usage: fl verify <proof-bundle-directory>")
	}
	root := nodefs.Resolve(args[0])
	schemaVersion := ""
	if text, err := nodefs.ReadText(nodefs.Join(root, "manifest.json")); err == nil {
		if v, err := jsjson.Parse(text); err == nil {
			if sv := v.Get("schemaVersion"); sv.Kind() == jsjson.String {
				schemaVersion = sv.Str()
			}
		}
	}
	expectedRoot, rootProvided := option(args, "--expect-root")
	switch schemaVersion {
	case "faultline.prevention-proof.v1":
		e.printPrevention(prevention.Verify(root, expectedRoot, rootProvided))
		return nil
	case "faultline.turn-proof-bundle.v1":
		return fail("not yet ported: turn proof bundle verification")
	case "faultline.git-proof-bundle.v1":
		return fail("not yet ported: git proof bundle verification")
	}
	e.printBundle("Bundle", demo.Verify(root, expectedRoot, rootProvided))
	return nil
}

func verdictWord(valid bool) string {
	if valid {
		return "VALID"
	}
	return "INVALID"
}

func orUnavailable(s *string) string {
	if s == nil {
		return "unavailable"
	}
	return *s
}

func (e *env) printErrorsAndExit(r bundle.Result) {
	if !r.Valid {
		lines := make([]string, len(r.Errors))
		for i, err := range r.Errors {
			lines[i] = "- " + err
		}
		e.out(strings.Join(lines, "\n") + "\n")
		e.exitCode = 1
		return
	}
	e.exitCode = 0
}

// printBundle is the demo/git branch of the TS verify case.
func (e *env) printBundle(label string, r bundle.Result) {
	first := "Integrity"
	if r.ExternalRootStatus == "NOT_PROVIDED" {
		first = label + " self-consistency"
	}
	e.out(first + ": " + verdictWord(r.Valid) + "\n")
	e.out(fmt.Sprintf("Declared files checked: %d\nBundle root: %s\nExternal root: %s\n", r.CheckedFiles, orUnavailable(r.RootDigest), r.ExternalRootStatus))
	e.printErrorsAndExit(r)
}

// printPrevention ports the prevention branch of the TS verify case.
func (e *env) printPrevention(r bundle.Result) {
	first := "Integrity"
	if r.ExternalRootStatus == "NOT_PROVIDED" {
		first = "Prevention proof self-consistency"
	}
	e.out(first + ": " + verdictWord(r.Valid) + "\n")
	e.out("Classification: " + orUnavailable(r.Classification) + "\nBundle root: " + orUnavailable(r.RootDigest) + "\nExternal root: " + r.ExternalRootStatus + "\n")
	if r.Valid && r.Classification != nil && *r.Classification == "PREVENTION_VERIFIED" {
		e.out("Prevention verified\n")
	} else if r.Valid {
		e.out("Prevention evidence summary\n")
	}
	e.printErrorsAndExit(r)
}
