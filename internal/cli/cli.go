// Package cli is the Go port of src/cli.ts and src/cli-app.ts.
package cli

import (
	"fmt"
	"io"

	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
)

// topLevelCommands is the runCli switch in src/cli-app.ts:2805-3066.
var topLevelCommands = map[string]bool{
	"help": true, "--help": true, "-h": true, "advanced": true, "quickstart": true,
	"--version": true, "-V": true, "judge-demo": true, "judge-proof": true,
	"commit-proof-preview": true, "judge-preview": true, "doctor": true, "ui": true,
	"init": true, "incident": true, "runtime": true, "tutorial": true, "demo": true,
	"verify": true, "serve": true, "codex": true, "witness": true, "record": true,
	"investigate": true, "prove": true, "minimize": true, "ledger": true,
	"attest": true, "provenance": true, "repair": true, "prevention": true,
}

type env struct {
	stdout, stderr io.Writer
	exitCode       int
}

// out is process.stdout.write: WTF-8 is encoded like Node encodes JS strings.
func (e *env) out(s string) { io.WriteString(e.stdout, jsstr.ToUTF8(s)) }

// printHumanNext ports src/cli-app.ts:156: the one next command, on stderr
// so stdout stays machine-parseable.
func (e *env) printHumanNext(command string) {
	io.WriteString(e.stderr, jsstr.ToUTF8(jsexc.Concat("Next: ", command, "\n")))
}

type cliError struct{ message string }

func (c *cliError) Error() string { return c.message }

func fail(format string, a ...any) error { return &cliError{fmt.Sprintf(format, a...)} }

// Run executes the fl command line and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	e := &env{stdout: stdout, stderr: stderr}
	var err error
	if thrown := jsexc.Try(func() { err = dispatch(e, args) }); thrown != nil {
		err = thrown // a JS exception escaping the command reaches the CLI's catch
	}
	if err != nil {
		// A ZodError's message is read here, by formatCliFailure
		// (src/cli.ts:8), with the stack the CLI's catch has left.
		message := jsjson.AtCallSite(jsjson.SiteCLI, err.Error)
		io.WriteString(stderr, jsstr.ToUTF8(formatFailure(message)+"\n"))
		return 1
	}
	return e.exitCode
}

func dispatch(e *env, args []string) error {
	if len(args) == 0 {
		return fail("not yet ported: help")
	}
	command, rest := args[0], args[1:]
	switch {
	case command == "verify":
		return verifyCommand(e, rest)
	case command == "witness":
		return witnessCommand(e, rest)
	case command == "ledger":
		return ledgerCommand(e, rest)
	case command == "investigate":
		return investigateCommand(e, rest)
	case command == "minimize":
		return minimizeCommand(e, rest)
	case command == "judge-proof":
		return judgeProofCommand(e, rest)
	case command == "commit-proof-preview":
		return commitProofPreviewCommand(e, rest)
	case command == "doctor":
		return doctorCommand(e, rest)
	case command == "runtime":
		return runtimeCommand(e, rest)
	case command == "init":
		return initCommand(e, rest)
	case command == "incident":
		return incidentCommand(e, rest)
	case command == "prevention":
		return preventionCommand(e, rest)
	case command == "attest":
		return attestCommand(e, rest)
	case command == "provenance":
		return provenanceCommand(e, rest)
	case command == "repair":
		return repairCommand(e, rest)
	case topLevelCommands[command]:
		return fail("not yet ported: %s", command)
	default:
		return fail("Unknown command: %s", command)
	}
}
