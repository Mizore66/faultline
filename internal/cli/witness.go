package cli

import (
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/redaction"
	"github.com/Mizore66/faultline/internal/witness"
)

// obj builds a JS object literal from key/value pairs, in order.
func obj(pairs ...any) jsjson.Value {
	o := jsjson.NewObj()
	for i := 0; i < len(pairs); i += 2 {
		var v jsjson.Value
		switch x := pairs[i+1].(type) {
		case jsjson.Value:
			v = x
		case string:
			v = jsjson.MakeString(x)
		}
		o.Set(pairs[i].(string), v)
	}
	return jsjson.MakeObject(o)
}

// printJSON is process.stdout.write(`${JSON.stringify(v, null, 2)}\n`).
func (e *env) printJSON(v jsjson.Value) {
	e.out(jsexc.Concat(jsjson.StringifyIndent(v, "  "), "\n"))
}

func stringList(items ...string) jsjson.Value {
	values := make([]jsjson.Value, len(items))
	for i, item := range items {
		values[i] = jsjson.MakeString(item)
	}
	return jsjson.MakeArray(values)
}

// defaultWitnessProposal ports src/ai.ts:44.
func defaultWitnessProposal() jsjson.Value {
	o := jsjson.NewObj()
	o.Set("behavior", jsjson.MakeString("Completed refunds preserve settlement currency."))
	o.Set("command", jsjson.MakeString("node witness.mjs"))
	o.Set("overlayFiles", stringList("witness.mjs"))
	o.Set("expectedExitCode", jsjson.MakeNumber(0))
	o.Set("timeoutSeconds", jsjson.MakeNumber(30))
	o.Set("safetyNotes", stringList("Network disabled", "Review the exact overlay before approval", "Do not reveal candidate states to the proposal model"))
	return jsjson.MakeObject(o)
}

// witnessProposalSample ports witnessProposal (src/cli-app.ts:969); the
// --live GPT call is out of scope.
func (e *env) witnessProposalSample(args []string) error {
	if hasFlag(args, "--live") {
		return fail("not yet ported: witness propose --live")
	}
	e.printJSON(obj("source", "sample", "proposal", defaultWitnessProposal(), "note", "Pass --live to call GPT-5.6 with a blinded incident packet."))
	return nil
}

// witnessCommand ports the witness cases of src/cli-app.ts:1165-1459.
func witnessCommand(e *env, args []string) error {
	var action, proposalID string
	if len(args) > 0 {
		action = args[0]
	}
	if len(args) > 1 {
		proposalID = args[1]
	}
	store := witnessStore(args)
	switch action {
	case "implement", "review", "policy", "resolve-autonomous", "ratify", "sign":
		return fail("not yet ported: witness %s", action)
	case "propose":
		return e.witnessPropose(store, args)
	case "approve":
		if proposalID == "" {
			return fail("Usage: fl witness approve <proposal-id> --approved-by <actor>")
		}
		approvedBy, err := requiredOption(args, "--approved-by")
		if err != nil {
			return err
		}
		input := jsjson.NewObj()
		input.Set("approvedBy", jsjson.MakeString(approvedBy))
		if note, ok := option(args, "--note"); ok && note != "" {
			input.Set("note", jsjson.MakeString(note))
		}
		approval, err := witness.Approve(store, proposalID, jsjson.MakeObject(input))
		if err != nil {
			return err
		}
		e.printJSON(obj("status", "APPROVED", "proposalId", proposalID, "approvalDigest", approval.Get("approvalDigest"),
			"approvedBy", approval.Get("approvedBy"), "approvedAt", approval.Get("approvedAt")))
		return nil
	case "freeze":
		if proposalID == "" {
			return fail("Usage: fl witness freeze <proposal-id> [--repo <directory>] [--draft-store <directory>]")
		}
		frozen, err := witness.Freeze(store, proposalID, witness.FreezeOptions{})
		if err != nil {
			return err
		}
		repo, ok := option(args, "--repo")
		if !ok {
			repo = jsexc.Must(nodefs.Cwd())
		}
		repository := nodefs.Resolve(repo)
		draftStore, ok := option(args, "--draft-store")
		if !ok {
			draftStore = witness.DefaultIncidentDraftStore(repository)
		}
		draftStore = nodefs.Resolve(draftStore)
		digest := frozen.Get("frozenDigest").Str()
		// Binding write is best-effort when no draft store exists yet: TS
		// swallows every error, thrown ones included.
		_ = jsexc.Try(func() {
			_, _ = witness.WriteSessionBinding(draftStore, witness.BindingInput{IncidentID: proposalID, ExpectDigest: &digest})
		})
		e.printJSON(obj("status", "FROZEN", "proposalId", proposalID, "witnessDigest", frozen.Get("witnessDigest"),
			"frozenDigest", frozen.Get("frozenDigest"), "frozenAt", frozen.Get("frozenAt")))
		return nil
	case "verify":
		if proposalID == "" {
			return fail("Usage: fl witness verify <proposal-id> [--expect-digest <sha256:...>]")
		}
		keyring, _ := option(args, "--keyring")
		hasKeyring := keyring != "" // a truthiness test in TS
		if hasFlag(args, "--require-signature") && !hasKeyring {
			return fail("--require-signature needs --keyring <trusted-reviewers.json>.")
		}
		if hasKeyring {
			return fail("not yet ported: witness verify --keyring")
		}
		expected, provided := option(args, "--expect-digest")
		result := witness.VerifyFrozen(store, proposalID, expected, provided)
		e.printJSON(result.JSON())
		if result.Valid {
			e.exitCode = 0
		} else {
			e.exitCode = 1
		}
		return nil
	}
	return fail("Usage: fl witness propose|review|approve|freeze|sign|verify ...")
}

// witnessPropose ports the "propose" case (src/cli-app.ts:1345).
func (e *env) witnessPropose(store string, args []string) error {
	if liveIncident, ok := option(args, "--incident"); hasFlag(args, "--live") && ok && liveIncident != "" {
		return fail("not yet ported: witness propose --live")
	}
	inputFile, ok := option(args, "--input")
	if !ok || inputFile == "" {
		return e.witnessProposalSample(args[1:])
	}
	proposalInput, err := readJSONInput(inputFile)
	if err != nil {
		return err
	}
	isObject := proposalInput.Kind() == jsjson.Object
	var incidentPacket jsjson.Value
	if isObject {
		incidentPacket = proposalInput.Get("incidentPacket")
	}
	redacted, report := redaction.RedactValue(incidentPacket, "$")
	safeInput := proposalInput
	if isObject {
		// { ...proposalInput, incidentPacket: redacted.value }: the key keeps
		// its position when present and is appended otherwise.
		o := jsjson.NewObj()
		for _, k := range proposalInput.Obj().Keys() {
			o.Set(k, proposalInput.Obj().Field(k))
		}
		o.Set("incidentPacket", redacted)
		safeInput = jsjson.MakeObject(o)
	}
	proposal, err := witness.Propose(store, safeInput)
	if err != nil {
		return err
	}
	e.printJSON(obj("status", "PROPOSED", "store", store, "proposalId", proposal.Get("proposalId"),
		"proposalDigest", proposal.Get("proposalDigest"), "incidentPacketDigest", proposal.Get("incidentPacketDigest"),
		"privacy", report.JSON()))
	return nil
}
