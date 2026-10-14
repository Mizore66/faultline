// Package witness is the Go port of src/witness-lock.ts (the write-once
// witness store: proposal, human approval, frozen snapshot) and
// src/incident-binding.ts (the mutable incident session binding).
//
// Internally the port keeps TS's throw/catch control flow with jsexc; the
// exported functions return the thrown error instead.
package witness

import (
	"encoding/base64"
	"errors"
	"os"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jscase"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/schema"
	"github.com/Mizore66/faultline/internal/v8sort"
)

// FreezeOptions is the freezeApprovedWitness options object: frozenAt is
// set only when HasFrozenAt.
type FreezeOptions struct {
	FrozenAt    string
	HasFrozenAt bool
}

type storePaths struct{ proposal, approval, frozen string }

func throw(message string) { jsexc.Throw(errors.New(message)) }

// parse is zod Schema.parse: it throws the ZodError.
func parse(v jsjson.Value, s schema.Schema) jsjson.Value {
	return jsexc.Must(bundle.ParseValue(v, s))
}

func now() string { return clock.ISO(clock.Now()) }

// tryValue runs fn like a TS function body whose throw becomes the error.
func tryValue(fn func() jsjson.Value) (v jsjson.Value, err error) {
	err = jsexc.Try(func() { v = fn() })
	return
}

// Port of src/witness-lock.ts:185 assertUniqueOverlayPaths.
func assertUniqueOverlayPaths(overlays []jsjson.Value) {
	seen := map[string]bool{}
	for _, overlay := range overlays {
		// Overlay content can eventually be materialized on case-insensitive
		// hosts. Rejecting aliases now keeps the frozen contract unambiguous.
		key := jscase.Lower(overlay.Get("path").Str())
		if seen[key] {
			throw(jsexc.Concat("Duplicate overlay path: ", overlay.Get("path").Str()))
		}
		jsexc.SetAdd(seen, key)
	}
}

// Port of src/witness-lock.ts:217 buildProposal.
func buildProposal(input jsjson.Value) jsjson.Value {
	parsed := parse(input, gitproof.WitnessProposalInputSchema)
	assertUniqueOverlayPaths(parsed.Get("witness", "overlays").Items())
	inputs := parsed.Get("witness", "overlays").Items()
	overlays := make([]jsjson.Value, len(inputs))
	for i, overlay := range inputs {
		// The schema admitted only canonical base64, where Node's lenient
		// decoder and the strict one agree.
		decoded, _ := base64.StdEncoding.DecodeString(overlay.Get("bytesBase64").Str())
		o := jsjson.NewObj()
		o.Set("path", overlay.Get("path"))
		o.Set("bytesBase64", overlay.Get("bytesBase64"))
		o.Set("bytesDigest", jsjson.MakeString(gitproof.ByteDigest(decoded)))
		overlays[i] = jsjson.MakeObject(o)
	}
	v8sort.Sort(overlays, func(a, b jsjson.Value) int {
		return canonical.LocaleCompare(a.Get("path").Str(), b.Get("path").Str())
	})
	unsigned := jsjson.NewObj()
	unsigned.Set("schemaVersion", jsjson.MakeString("faultline.witness-proposal.v1"))
	unsigned.Set("proposalId", parsed.Get("proposalId"))
	if at := parsed.Get("proposedAt"); at.Kind() != jsjson.Undefined {
		unsigned.Set("proposedAt", at)
	} else {
		unsigned.Set("proposedAt", jsjson.MakeString(now()))
	}
	if origin := parsed.Get("proposalOrigin"); origin.Kind() != jsjson.Undefined {
		unsigned.Set("proposalOrigin", origin)
	} else {
		unsigned.Set("proposalOrigin", jsjson.MakeString("HUMAN"))
	}
	unsigned.Set("incidentPacket", parsed.Get("incidentPacket"))
	unsigned.Set("incidentPacketDigest", jsjson.MakeString(jsexc.Must(canonical.DigestJSON(parsed.Get("incidentPacket")))))
	witness := jsjson.NewObj()
	witness.Set("behavior", parsed.Get("witness", "behavior"))
	witness.Set("command", parsed.Get("witness", "command"))
	witness.Set("commandDigest", jsjson.MakeString(gitproof.CommandDigest(parsed.Get("witness", "command").Str())))
	witness.Set("overlays", jsjson.MakeArray(overlays))
	witness.Set("overlayDigest", jsjson.MakeString(gitproof.OverlayDigest(overlays)))
	witness.Set("policy", parsed.Get("witness", "policy"))
	unsigned.Set("witness", jsjson.MakeObject(witness))
	return signed(unsigned, "proposalDigest")
}

// signed is `{ ...unsigned, [key]: digestJson(unsigned) }`.
func signed(unsigned *jsjson.Obj, key string) jsjson.Value {
	digest := jsexc.Must(canonical.DigestJSON(jsjson.MakeObject(unsigned)))
	out := jsjson.NewObj()
	for _, k := range unsigned.Keys() {
		out.Set(k, unsigned.Field(k))
	}
	out.Set(key, jsjson.MakeString(digest))
	return jsjson.MakeObject(out)
}

// Port of src/witness-lock.ts:246 assertDirectoryIsSafe.
func assertDirectoryIsSafe(directory string) {
	info := jsexc.Must(nodefs.Lstat(directory))
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		throw(jsexc.Concat("Witness lock directory must be a non-symlink directory: ", directory))
	}
}

// EnsureDirectoryIsSafe ports ensureDirectoryIsSafe (src/witness-lock.ts:253):
// mkdirSync({ recursive: true, mode: 0o700 }) when absent, then a lstat
// that refuses symlinks and non-directories.
func EnsureDirectoryIsSafe(directory string) error {
	return jsexc.Try(func() { ensureDirectoryIsSafe(directory) })
}

func ensureDirectoryIsSafe(directory string) {
	if !nodefs.Exists(directory) {
		if err := nodefs.MkdirAll(directory, 0o700); err != nil {
			jsexc.Throw(err)
		}
	}
	assertDirectoryIsSafe(directory)
}

// Port of src/witness-lock.ts:258 storePaths.
func storePathsFor(storeDirectory, proposalID string, createDirectories bool) storePaths {
	id := parse(jsjson.MakeString(proposalID), gitproof.ProposalIDSchema).Str()
	root := nodefs.Resolve(storeDirectory)
	directories := []string{root, nodefs.Join(root, "proposals"), nodefs.Join(root, "approvals"), nodefs.Join(root, "frozen")}
	if createDirectories {
		for _, directory := range directories {
			ensureDirectoryIsSafe(directory)
		}
	} else {
		// A verifier must not follow an attacker-controlled store
		// subdirectory through a link, even though it creates no records.
		for _, directory := range directories {
			if nodefs.Exists(directory) {
				assertDirectoryIsSafe(directory)
			}
		}
	}
	paths := storePaths{
		proposal: nodefs.Resolve(root, "proposals", id+".json"),
		approval: nodefs.Resolve(root, "approvals", id+".json"),
		frozen:   nodefs.Resolve(root, "frozen", id+".json"),
	}
	for _, path := range []string{paths.proposal, paths.approval, paths.frozen} {
		nested := nodefs.Relative(root, path)
		if nested == "" || strings.HasPrefix(nested, "..") || strings.Contains(nested, ":") {
			throw("Witness lock path escaped its configured store")
		}
	}
	return paths
}

// WriteOnceJSON ports writeOnceJson (src/witness-lock.ts:285): canonical JSON
// and a newline, created with flag "wx" and mode 0o600.
func WriteOnceJSON(path string, v jsjson.Value) error {
	return jsexc.Try(func() { writeOnceJSON(path, v) })
}

func writeOnceJSON(path string, v jsjson.Value) {
	text := jsexc.Must(canonical.CanonicalJSON(v))
	if err := nodefs.WriteFileUTF8(path, jsexc.Concat(text, "\n"), 0o600, true); err != nil {
		if nodefs.IsExist(err) {
			throw(jsexc.Concat("Immutable witness lock record already exists: ", path))
		}
		jsexc.Throw(err)
	}
}

// Port of src/witness-lock.ts:295 readJson.
func readJSON(path, label string) jsjson.Value {
	info := jsexc.Must(nodefs.Lstat(path))
	if !info.Mode().IsRegular() {
		throw(label + " must be a regular non-symlink file")
	}
	var v jsjson.Value
	if err := jsexc.Try(func() { v = jsexc.Must(bundle.ParseJSONFile(path)) }); err != nil {
		throw(jsexc.Concat(label, " is not valid JSON: ", jsexc.Message(err)))
	}
	return v
}

// Port of src/witness-lock.ts:305-315.
func readProposalAt(path string) jsjson.Value {
	return parse(readJSON(path, "Witness proposal"), gitproof.WitnessProposalSchema)
}

func readApprovalAt(path string) jsjson.Value {
	return parse(readJSON(path, "Witness approval"), gitproof.WitnessApprovalSchema)
}

func readFrozenAt(path string) jsjson.Value {
	return parse(readJSON(path, "Frozen witness"), gitproof.FrozenWitnessSchema)
}

// Propose ports proposeWitness (src/witness-lock.ts:383).
func Propose(storeDirectory string, input jsjson.Value) (jsjson.Value, error) {
	return tryValue(func() jsjson.Value {
		proposal := buildProposal(input)
		paths := storePathsFor(storeDirectory, proposal.Get("proposalId").Str(), true)
		writeOnceJSON(paths.proposal, proposal)
		return proposal
	})
}

// ReadProposal ports readWitnessProposal (src/witness-lock.ts:390).
func ReadProposal(storeDirectory, proposalID string) (jsjson.Value, error) {
	return tryValue(func() jsjson.Value {
		return readProposalAt(storePathsFor(storeDirectory, proposalID, false).proposal)
	})
}

// Approve ports approveWitnessProposal (src/witness-lock.ts:399).
func Approve(storeDirectory, proposalID string, input jsjson.Value) (jsjson.Value, error) {
	return tryValue(func() jsjson.Value {
		approvalInput := parse(input, gitproof.HumanApprovalInputSchema)
		paths := storePathsFor(storeDirectory, proposalID, true)
		if !nodefs.Exists(paths.proposal) {
			throw(jsexc.Concat("Cannot approve an unknown proposal: ", proposalID))
		}
		proposal := readProposalAt(paths.proposal)
		var proposalErrors []string
		gitproof.VerifyProposal(proposal, &proposalErrors)
		if len(proposalErrors) > 0 {
			throw(jsexc.Concat("Cannot approve a mutated proposal: ", jsexc.Join(proposalErrors, "; ")))
		}
		unsigned := jsjson.NewObj()
		unsigned.Set("schemaVersion", jsjson.MakeString("faultline.witness-approval.v1"))
		unsigned.Set("proposalId", proposal.Get("proposalId"))
		unsigned.Set("proposalDigest", proposal.Get("proposalDigest"))
		unsigned.Set("incidentPacketDigest", proposal.Get("incidentPacketDigest"))
		unsigned.Set("reviewedCommandDigest", proposal.Get("witness", "commandDigest"))
		unsigned.Set("reviewedOverlayDigest", proposal.Get("witness", "overlayDigest"))
		unsigned.Set("reviewerType", jsjson.MakeString("HUMAN"))
		unsigned.Set("approvedBy", approvalInput.Get("approvedBy"))
		if at := approvalInput.Get("approvedAt"); at.Kind() != jsjson.Undefined {
			unsigned.Set("approvedAt", at)
		} else {
			unsigned.Set("approvedAt", jsjson.MakeString(now()))
		}
		if note := approvalInput.Get("note"); note.Kind() != jsjson.Undefined {
			unsigned.Set("note", note)
		}
		approval := signed(unsigned, "approvalDigest")
		writeOnceJSON(paths.approval, approval)
		return approval
	})
}

// ReadApproval ports readWitnessApproval (src/witness-lock.ts:424).
func ReadApproval(storeDirectory, proposalID string) (jsjson.Value, error) {
	return tryValue(func() jsjson.Value {
		return readApprovalAt(storePathsFor(storeDirectory, proposalID, false).approval)
	})
}

// Freeze ports freezeApprovedWitness (src/witness-lock.ts:434).
func Freeze(storeDirectory, proposalID string, options FreezeOptions) (jsjson.Value, error) {
	return tryValue(func() jsjson.Value {
		raw := jsjson.NewObj()
		if options.HasFrozenAt {
			raw.Set("frozenAt", jsjson.MakeString(options.FrozenAt))
		}
		freezeOptions := parse(jsjson.MakeObject(raw), gitproof.FreezeOptionsSchema)
		paths := storePathsFor(storeDirectory, proposalID, true)
		if !nodefs.Exists(paths.proposal) {
			throw(jsexc.Concat("Cannot freeze an unknown proposal: ", proposalID))
		}
		if !nodefs.Exists(paths.approval) {
			throw(jsexc.Concat("Cannot freeze an unapproved proposal: ", proposalID))
		}
		proposal := readProposalAt(paths.proposal)
		approval := readApprovalAt(paths.approval)
		var errs []string
		gitproof.VerifyProposal(proposal, &errs)
		gitproof.VerifyApproval(approval, proposal, &errs)
		if len(errs) > 0 {
			throw(jsexc.Concat("Cannot freeze an invalid review chain: ", jsexc.Join(errs, "; ")))
		}
		unsigned := jsjson.NewObj()
		unsigned.Set("schemaVersion", jsjson.MakeString("faultline.frozen-witness.v1"))
		if at := freezeOptions.Get("frozenAt"); at.Kind() != jsjson.Undefined {
			unsigned.Set("frozenAt", at)
		} else {
			unsigned.Set("frozenAt", jsjson.MakeString(now()))
		}
		unsigned.Set("proposal", proposal)
		unsigned.Set("approval", approval)
		unsigned.Set("witnessDigest", jsjson.MakeString(jsexc.Must(canonical.DigestJSON(gitproof.WitnessPayload(proposal)))))
		frozen := signed(unsigned, "frozenDigest")
		writeOnceJSON(paths.frozen, frozen)
		return frozen
	})
}

// ReadFrozen ports readFrozenWitness (src/witness-lock.ts:457).
func ReadFrozen(storeDirectory, proposalID string) (jsjson.Value, error) {
	return tryValue(func() jsjson.Value {
		return readFrozenAt(storePathsFor(storeDirectory, proposalID, false).frozen)
	})
}

func failedVerification(message string, expectedProvided bool) gitproof.WitnessVerification {
	status := "NOT_PROVIDED"
	if expectedProvided {
		status = "MISMATCH"
	}
	return gitproof.WitnessVerification{Errors: []string{message}, ExternalDigestStatus: status}
}

// VerifyFrozen ports verifyFrozenWitness (src/witness-lock.ts:487). It never
// runs the proposed command or reads overlay paths from a repository. A
// throw TS does not catch (a ZodError message getter overflowing, say)
// propagates as a jsexc panic.
func VerifyFrozen(storeDirectory, proposalID, expected string, expectedProvided bool) gitproof.WitnessVerification {
	var paths storePaths
	if err := jsexc.Try(func() { paths = storePathsFor(storeDirectory, proposalID, false) }); err != nil {
		return failedVerification(jsexc.Concat("invalid witness lock location: ", jsexc.Message(err)), expectedProvided)
	}
	if !nodefs.Exists(paths.frozen) {
		message := "approved proposal has not been frozen"
		if !nodefs.Exists(paths.approval) {
			message = "human approval is missing; no witness may be frozen"
		}
		return failedVerification(message, expectedProvided)
	}
	var frozen jsjson.Value
	if err := jsexc.Try(func() { frozen = readFrozenAt(paths.frozen) }); err != nil {
		return failedVerification(jsexc.Concat("unable to read frozen witness: ", jsexc.Message(err)), expectedProvided)
	}
	result := gitproof.VerifyFrozenRecord(frozen, expected, expectedProvided)
	errs := append([]string{}, result.Errors...)
	if !nodefs.Exists(paths.proposal) {
		errs = append(errs, "persisted proposal is missing")
	} else if err := jsexc.Try(func() {
		if !bundle.SameCanonical(readProposalAt(paths.proposal), frozen.Get("proposal")) {
			errs = append(errs, "persisted proposal does not match frozen proposal snapshot")
		}
	}); err != nil {
		errs = append(errs, jsexc.Concat("persisted proposal cannot be verified: ", jsexc.Message(err)))
	}
	if !nodefs.Exists(paths.approval) {
		errs = append(errs, "persisted human approval is missing")
	} else if err := jsexc.Try(func() {
		if !bundle.SameCanonical(readApprovalAt(paths.approval), frozen.Get("approval")) {
			errs = append(errs, "persisted approval does not match frozen approval snapshot")
		}
	}); err != nil {
		errs = append(errs, jsexc.Concat("persisted approval cannot be verified: ", jsexc.Message(err)))
	}
	result.Valid = len(errs) == 0
	result.Errors = errs
	return result
}
