package cli

import (
	"errors"
	"math"
	"strings"
	"time"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/codexloop"

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
	case "implement":
		return e.witnessImplement(store, args)
	case "review":
		return fail("not yet ported: witness %s", action)
	case "policy":
		return e.witnessPolicy(store, args, proposalID)
	case "resolve-autonomous":
		if proposalID == "" {
			return fail("Usage: fl witness resolve-autonomous <proposal-id> [--policy-id <id>]")
		}
		var policy *jsjson.Value
		if policyID, ok := option(args, "--policy-id"); ok {
			read, err := witness.ReadPolicy(store, policyID)
			if err != nil {
				return err
			}
			policy = &read
		}
		result, err := witness.ResolveAutonomous(store, proposalID, policy)
		if err != nil {
			return err
		}
		e.printJSON(result)
		return nil
	case "ratify":
		if proposalID == "" {
			return fail("Usage: fl witness ratify <proposal-id> --executed-digest <sha256:…> --ratified-by <actor>")
		}
		var o witness.RatifyOptions
		if note, ok := option(args, "--note"); ok {
			o.Note = &note
		}
		var err error
		if o.ExecutedWitnessDigest, err = requiredOption(args, "--executed-digest"); err != nil {
			return err
		}
		if o.RatifiedBy, err = requiredOption(args, "--ratified-by"); err != nil {
			return err
		}
		result, err := witness.Ratify(store, proposalID, o)
		if err != nil {
			return err
		}
		e.printJSON(obj("status", "APPROVED_AFTER_EXECUTION", "proposalId", proposalID,
			"ratificationDigest", result.Get("ratification", "ratificationDigest"),
			"frozenDigest", result.Get("frozen", "frozenDigest"),
			"approvedBy", result.Get("frozen", "approval", "approvedBy")))
		return nil
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
	case "sign":
		if proposalID == "" {
			return fail("Usage: fl witness sign <proposal-id> --private-key <ed25519-private.pem> --keyring <trusted-reviewers.json>")
		}
		keyOption, err := requiredOption(args, "--private-key")
		if err != nil {
			return err
		}
		privateKeyFile := nodefs.Resolve(keyOption)
		privateKeyPEM, err := nodefs.ReadText(privateKeyFile)
		if err != nil {
			return errors.New(jsexc.Concat("Unable to read reviewer private key ", privateKeyFile, ": ", jsexc.Message(err)))
		}
		keyringOption, err := requiredOption(args, "--keyring")
		if err != nil {
			return err
		}
		keyring, err := readJSONInput(keyringOption)
		if err != nil {
			return err
		}
		receipt, err := witness.SignAuthenticatedApproval(store, proposalID, witness.SignOptions{PrivateKeyPEM: privateKeyPEM, Keyring: keyring})
		if err != nil {
			return err
		}
		e.printJSON(obj("status", "AUTHENTICATED_APPROVAL_RECORDED", "proposalId", proposalID,
			"frozenDigest", receipt.Get("frozenDigest"), "keyId", receipt.Get("keyId"), "signedAt", receipt.Get("signedAt"),
			"receiptDigest", receipt.Get("receiptDigest"),
			"limitation", "The private key is never stored or printed. Trust comes only from the supplied reviewer keyring."))
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
		expected, provided := option(args, "--expect-digest")
		if hasKeyring {
			opts := witness.VerifyAuthOptions{RequireSignature: hasFlag(args, "--require-signature")}
			if provided {
				opts.ExpectedFrozenDigest = &expected
			}
			keyringValue, err := readJSONInput(keyring)
			if err != nil {
				return err
			}
			opts.Keyring = &keyringValue
			result := witness.VerifyAuthenticatedApproval(store, proposalID, opts)
			e.printJSON(result)
			if result.Get("valid").Bool() {
				e.exitCode = 0
			} else {
				e.exitCode = 1
			}
			return nil
		}
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

// witnessPolicy ports the "policy" case (src/cli-app.ts:1272).
func (e *env) witnessPolicy(store string, args []string, action string) error {
	switch action {
	case "freeze":
		policyID, err := requiredOption(args, "--policy-id")
		if err != nil {
			return err
		}
		frozenBy, err := requiredOption(args, "--frozen-by")
		if err != nil {
			return err
		}
		command, err := requiredOption(args, "--command")
		if err != nil {
			return err
		}
		timeout := "60"
		if v, ok := option(args, "--max-timeout"); ok {
			timeout = v
		}
		maxTimeout := jsjson.ToNumber(timeout)
		if math.IsNaN(maxTimeout) || math.IsInf(maxTimeout, 0) || maxTimeout != math.Trunc(maxTimeout) || maxTimeout < 1 {
			return fail("--max-timeout must be a positive integer.")
		}
		input := obj("schemaVersion", "faultline.standing-approval-policy.v1", "policyId", policyID, "frozenBy", frozenBy,
			"frozenAt", clock.ISO(clock.Now()), "allowedCommands", stringList(command),
			"allowedOverlayTemplates", jsjson.MakeArray(nil), "maxTimeoutSeconds", jsjson.MakeNumber(maxTimeout),
			"network", "disabled", "credentials", "redacted")
		policy, err := witness.BuildPolicy(input)
		if err != nil {
			return err
		}
		if _, err := witness.WritePolicy(store, policy); err != nil {
			return err
		}
		e.printJSON(obj("status", "STANDING_POLICY_FROZEN", "policyId", policy.Get("policyId"), "policyDigest", policy.Get("policyDigest")))
		return nil
	case "apply":
		var target string
		if len(args) > 2 {
			target = args[2]
		}
		if target == "" || strings.HasPrefix(target, "--") {
			return fail("Usage: fl witness policy apply <proposal-id> --policy-id <id>")
		}
		policyID, err := requiredOption(args, "--policy-id")
		if err != nil {
			return err
		}
		result, err := witness.TryAutoFreeze(store, target, policyID)
		if err != nil {
			return err
		}
		if result.Frozen == nil {
			e.printJSON(obj("status", "STANDING_POLICY_NO_MATCH", "reasons", stringList(result.Reasons...)))
			e.exitCode = 1
			return nil
		}
		e.printJSON(obj("status", "FROZEN_BY_STANDING_POLICY", "proposalId", target,
			"approvedBy", witness.PolicyApprovedBy(result.Policy),
			"frozenDigest", result.Frozen.Get("frozenDigest"), "policyDigest", result.Policy.Get("policyDigest")))
		return nil
	}
	return fail("Usage: fl witness policy freeze --policy-id <id> --frozen-by <actor> --command <exact> [--max-timeout <seconds>] | fl witness policy apply <proposal-id> --policy-id <id>")
}

// witnessImplement ports the "implement" case (src/cli-app.ts:1169-1203).
// The incident is read with a raw readFileSync + JSON.parse, so its errors
// are Node's own. --repo is resolved but unused, as in TS.
func (e *env) witnessImplement(_ string, args []string) error {
	incidentOption, err := requiredOption(args, "--incident")
	if err != nil {
		return err
	}
	incident := readRawJSON(incidentOption)
	var o codexloop.ImplementOptions
	o.Incident = incident
	if o.ProposalID, err = requiredOption(args, "--proposal-id"); err != nil {
		return err
	}
	o.Behavior = optionOr(args, "--behavior", "The human-reviewed predicate must hold.")
	overlayOut, err := requiredOption(args, "--overlay-out")
	if err != nil {
		return err
	}
	o.OverlayOut = nodefs.Resolve(overlayOut)
	o.Repository = repoOption(args)
	if hasFlag(args, "--with-codex") {
		o.Runner = codexloop.CLIRunner(hasFlag(args, "--allow-codex-full-auto"),
			"Codex drafting requires explicit --allow-codex-full-auto in addition to --with-codex.", 120*time.Second)
	}
	result, err := codexloop.Implement(o)
	if err != nil {
		return err
	}
	e.printJSON(result)
	status := result.Get("status").Str()
	if status == "OVERLAY_DRAFTED" || status == "TEMPLATE_ONLY" {
		e.exitCode = 0
	} else {
		e.exitCode = 1
	}
	return nil
}
