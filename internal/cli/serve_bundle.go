package cli

import (
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/proofview"
)

// serveBundleCommand is the `serve --bundle` case of src/cli-app.ts: every
// value flag is read before any pair is checked, pairs are checked before any
// I/O, and the bundle is verified before the port is coerced or bound.
func serveBundleCommand(e *env, args []string) error {
	raw, err := requiredOption(args, "--bundle")
	if err != nil {
		return err
	}
	directory := nodefs.Resolve(raw)
	flags := []string{"--minimization", "--repair", "--prevention", "--expect-minimization", "--expect-repair", "--expect-prevention"}
	values := make([]*string, len(flags))
	for i, flag := range flags {
		if hasFlag(args, flag) {
			value, err := requiredOption(args, flag)
			if err != nil {
				return err
			}
			values[i] = &value
		}
	}
	pairErrors := []string{
		"A shareable minimization attachment requires both --minimization <result.json> and --expect-minimization <retained-digest>.",
		"A shareable repair attachment requires both --repair <repair-brief-directory> and --expect-repair <retained-artifact-digest>.",
		"A shareable prevention attachment requires both --prevention <prevention-proof-directory> and --expect-prevention <retained-root-digest>.",
	}
	for i := 0; i < 3; i++ {
		if (values[i] == nil) != (values[i+3] == nil) {
			return fail("%s", pairErrors[i])
		}
	}
	for i := 0; i < 3; i++ {
		if values[i] != nil {
			absolute := nodefs.Resolve(*values[i])
			values[i] = &absolute
		}
	}
	v, err := proofview.LoadVerifiedGitProofViewWithAttachments(directory, optionPointer(args, "--expect-root"), proofview.AttachmentOptions{
		MinimizationFile: values[0], ExpectedMinimizationDigest: values[3],
		RepairDirectory: values[1], ExpectedRepairDigest: values[4],
		PreventionDirectory: values[2], ExpectedPreventionDigest: values[5],
	})
	if err != nil {
		return err
	}
	port, ok := option(args, "--port")
	if !ok {
		port = "4173"
	}
	return awaitBundleInterrupt(func() (func() error, error) {
		server, err := proofview.StartGitProofServer(v, jsjson.ToNumber(port))
		if err != nil {
			return nil, err
		}
		e.out("FaultLine read-only Git proof page: " + server.URL() + "\nPress Ctrl+C to stop.\n")
		return server.Close, nil
	})
}
