package cli

import (
	"io"
	"strings"

	"github.com/Mizore66/faultline/internal/doctor"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// doctorRunner is the runner `fl doctor` uses; tests replace it.
var doctorRunner = doctor.NodeRunner

// printHumanNext ports src/cli-app.ts:156: the one next command, on stderr
// so stdout stays machine-parseable.
func (e *env) printHumanNext(command string) {
	io.WriteString(e.stderr, jsstr.ToUTF8(jsexc.Concat("Next: ", command, "\n")))
}

// doctorSummary ports src/cli-app.ts:302.
func doctorSummary(report jsjson.Value) string {
	var diagnostics []string
	for _, d := range report.Get("diagnostics").Items() {
		observation := ""
		if o := d.Get("observation"); o.Kind() != jsjson.Null {
			observation = jsexc.Concat(" (", o.Str(), ")")
		}
		remediation := ""
		if r := d.Get("remediation"); r.Kind() != jsjson.Null {
			remediation = jsexc.Concat("\n    Fix: ", r.Str())
		}
		diagnostics = append(diagnostics, jsexc.Concat("[", d.Get("status").Str(), "] ", d.Get("id").Str(), ": ", d.Get("summary").Str(), observation, remediation))
	}
	runtime := "unknown; choose an explicit digest-pinned image"
	if kind := report.Get("likelyRuntime", "kind").Str(); kind != "UNKNOWN" {
		var markers []string
		for _, m := range report.Get("likelyRuntime", "markers").Items() {
			markers = append(markers, m.Str())
		}
		joined := strings.Join(markers, ", ")
		if joined == "" {
			joined = "no markers"
		}
		runtime = strings.ToLower(kind) + " (" + joined + ")"
	}
	preflight := doctor.ProofReadyStatus(report)
	proofGrade := preflight + " (proof-grade Git investigation unavailable; fl judge-demo and offline verify still work)"
	if preflight == "READY" {
		proofGrade = "READY (Docker proof-grade path available)"
	}
	repository := report.Get("repositoryRoot")
	if repository.Kind() == jsjson.Null {
		repository = report.Get("repository")
	}
	localCLI := "ACTION_REQUIRED"
	if doctor.CliExitCode(report) == 0 {
		localCLI = "READY"
	}
	lines := []string{
		"FaultLine doctor",
		jsexc.Concat("Repository: ", repository.Str()),
		"Local CLI: " + localCLI,
		"Proof-grade preflight: " + proofGrade,
		"Image selection: " + report.Get("imageSelection").Str() + " (run fl runtime prepare <runtime> --yes for a reviewed base, fl runtime project plan for an explicit dependency image, or fl runtime resolve after you choose one yourself)",
		jsexc.Concat("Likely runtime: ", runtime),
		"",
	}
	lines = append(lines, diagnostics...)
	lines = append(lines, "", "Limits:")
	for _, l := range report.Get("limitations").Items() {
		lines = append(lines, "- "+l.Str())
	}
	lines = append(lines, "",
		"Exit status: 0 when Node can run the local CLI (including the no-Docker judge path). Non-zero only when Node itself is missing or unsupported. Docker gaps are reported above and do not fail this command.")
	return jsexc.Join(lines, "\n")
}

// doctorCommand ports src/cli-app.ts:331 doctorCommand.
func doctorCommand(e *env, args []string) error {
	securityOnly := hasFlag(args, "--security")
	proofReadyOnly := hasFlag(args, "--proof-ready")
	if securityOnly && proofReadyOnly {
		return fail("Usage: fl doctor [--repo <directory>] [--json] [--proof-ready | --security]")
	}
	if securityOnly {
		return doctorSecurity(e, args)
	}

	repo, ok := option(args, "--repo")
	if !ok {
		repo = jsexc.Must(nodefs.Cwd())
	}
	report := doctor.Run(nodefs.Resolve(repo), doctorRunner())
	ready := doctor.ProofReadyStatus(report) == "READY"
	if hasFlag(args, "--json") {
		o := jsjson.NewObj()
		for _, k := range report.Obj().Keys() {
			o.Set(k, report.Obj().Field(k))
		}
		o.Set("cliExitCode", jsjson.MakeNumber(float64(doctor.CliExitCode(report))))
		proofReadyExit := 1
		if ready {
			proofReadyExit = 0
		}
		o.Set("proofReadyExitCode", jsjson.MakeNumber(float64(proofReadyExit)))
		e.printJSON(jsjson.MakeObject(o))
	} else {
		e.out(jsexc.Concat(doctorSummary(report), "\n"))
		if proofReadyOnly {
			status := "NOT READY"
			if ready {
				status = "READY"
			}
			e.out("\n--proof-ready: " + status + "\n")
		}
		if ready {
			e.printHumanNext("fl judge-proof")
		} else {
			e.printHumanNext("fl doctor --proof-ready")
		}
	}
	switch {
	case !proofReadyOnly:
		e.exitCode = doctor.CliExitCode(report)
	case ready:
		e.exitCode = 0
	default:
		e.exitCode = 1
	}
	return nil
}

// doctorSecurity is the --security branch of src/cli-app.ts:331
// doctorCommand.
func doctorSecurity(e *env, args []string) error {
	security, err := doctor.RunSecurity()
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		o := jsjson.NewObj()
		for _, k := range security.Obj().Keys() {
			o.Set(k, security.Obj().Field(k))
		}
		o.Set("cliExitCode", jsjson.MakeNumber(float64(doctor.SecurityExitCode(security))))
		e.printJSON(jsjson.MakeObject(o))
	} else {
		lines := []string{"FaultLine doctor --security", "Status: " + security.Get("status").Str()}
		for _, check := range security.Get("checks").Items() {
			lines = append(lines, jsexc.Concat("- ", check.Get("id").Str(), ": ", check.Get("status").Str(), " \u2014 ", check.Get("summary").Str()))
		}
		lines = append(lines, "Next: "+security.Get("next").Str())
		e.out(jsexc.Concat(jsexc.Join(lines, "\n"), "\n"))
	}
	e.exitCode = doctor.SecurityExitCode(security)
	return nil
}
