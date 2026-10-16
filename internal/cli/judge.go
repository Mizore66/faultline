package cli

import (
	"math"
	"strconv"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/proofview"
)

// describeBundlePath ports src/proof-bundle.ts:534.
func describeBundlePath(directory string) string {
	if rel := nodefs.Relative(nodefs.Resolve("."), nodefs.Resolve(directory)); rel != "" {
		return rel
	}
	return "."
}

// bundleDirectoryOption is the --bundle handling shared by judge-proof and
// commit-proof-preview: the workspace sample, or the resolved --bundle.
func bundleDirectoryOption(args []string) (string, error) {
	if _, ok := option(args, "--bundle"); !ok {
		return proofview.DefaultSelfIncidentSampleDirectory(), nil
	}
	bundle, err := requiredOption(args, "--bundle")
	if err != nil {
		return "", err
	}
	return nodefs.Resolve(bundle), nil
}

func optionPointer(args []string, flag string) *string {
	if v, ok := option(args, flag); ok {
		return &v
	}
	return nil
}

// judgeProofCommand ports src/cli-app.ts:997 judgeProofCommand.
func judgeProofCommand(e *env, args []string) error {
	bundleDirectory, err := bundleDirectoryOption(args)
	if err != nil {
		return err
	}
	proof, err := proofview.LoadSelfIncidentProofView(bundleDirectory, optionPointer(args, "--expect-root"))
	if err != nil {
		return err
	}
	e.out("FaultLine COMMIT_PROOF sample verified.\n")
	e.out("Bundle: " + describeBundlePath(bundleDirectory) + "\n")
	e.out("Root: " + proof.RootDigest + "\n")
	e.out("External root: " + proof.ExternalRootStatus + "\n")
	e.out("This is the product Idea path (portable predicate proof), not the judge-demo fixture.\n")
	e.out(selfIncidentRootLine(proof.RootDigest))
	if hasFlag(args, "--export-only") {
		e.out("Verified root summary: " + proof.RootDigest + " (" + proof.ExternalRootStatus + ")\n")
		return nil
	}
	portText, ok := option(args, "--port")
	if !ok {
		portText = "4174"
	}
	port := jsjson.ToNumber(portText)
	var serveMs *float64
	if raw, ok := option(args, "--serve-ms"); ok {
		ms := jsjson.ToNumber(raw)
		if math.IsNaN(ms) || math.IsInf(ms, 0) || ms < 0 {
			return fail("--serve-ms must be a non-negative number of milliseconds")
		}
		serveMs = &ms
	}
	server, err := proofview.StartGitProofServer(proof, port)
	if err != nil {
		return err
	}
	e.out("FaultLine COMMIT_PROOF page: " + server.URL() + "\nPress Ctrl+C to stop.\n")
	openLocalDemoURL(e, server.URL())
	awaitServeInterrupt(server.Close, serveMs, func() {
		e.out("FaultLine judge-proof stopped. Verified root: " + proof.RootDigest + " · External root: " + proof.ExternalRootStatus + "\n")
	})
	return nil
}

// selfIncidentRootLine is the line judgeProofCommand prints after the
// verified root (src/cli-app.ts:1011-1015).
func selfIncidentRootLine(rootDigest string) string {
	if rootDigest == proofview.RecordedSelfIncidentRoot {
		return "Matched historical self-incident root.\n"
	}
	return "Note: historical self-incident root is " + proofview.RecordedSelfIncidentRoot + " (see docs/faultline-self-incident.md).\n"
}

// commitProofPreviewCommand ports src/cli-app.ts:1041 commitProofPreviewCommand.
func commitProofPreviewCommand(e *env, args []string) error {
	bundleDirectory, err := bundleDirectoryOption(args)
	if err != nil {
		return err
	}
	expectRoot := optionPointer(args, "--expect-root")
	var output string
	if _, ok := option(args, "--output"); !ok {
		output = proofview.DefaultCommitProofPreviewPath()
	} else {
		value, err := requiredOption(args, "--output")
		if err != nil {
			return err
		}
		output = nodefs.Resolve(value)
	}
	preview, err := proofview.WriteCommitProofPreview(bundleDirectory, expectRoot, output)
	if err != nil {
		return err
	}
	e.out("FaultLine static COMMIT_PROOF preview written.\n")
	e.out("Preview: " + preview.Path + "\n")
	e.out("Bytes: " + strconv.Itoa(preview.Bytes) + "\n")
	e.out("Root: " + preview.RootDigest + "\n")
	e.out("Limitation: static snapshot of a verified package — not a live Docker rerun.\n")
	return nil
}
