package prevention

import (
	"errors"
	"strings"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsre"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// The AGENTS.md block markers (src/agents-md.ts:4-5).
const (
	agentsMdBegin = "<!-- FAULTLINE:BEGIN -->"
	agentsMdEnd   = "<!-- FAULTLINE:END -->"
)

var (
	sha256DigestRe = jsre.MustCompile(`^sha256:[a-f0-9]{64}$`, "")
	hexDigestRe    = jsre.MustCompile(`^[a-f0-9]{40,64}$`, "")
)

// Port of src/agents-md.ts:18 assertDigestOnly.
func assertDigestOnly(value, label string) error {
	if !sha256DigestRe.Test(value) && !hexDigestRe.Test(value) {
		return errors.New(jsexc.Concat("Refusing to write non-digest ", label, " into AGENTS.md"))
	}
	return nil
}

// agentsInput is AgentsMdInvariantInput without repository.
type agentsInput struct {
	originalProofRoot, frozenWitnessDigest, preventionRootDigest string
	lastGoodRunIDs, firstBadRunIDs, repairedRunIDs               []string
}

func quotedIDs(ids []string) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = jsexc.Concat("`", id, "`")
	}
	return jsexc.Join(parts, ", ")
}

// Port of src/agents-md.ts:25 renderFaultLineAgentsBlock (classification is
// always PREVENTION_VERIFIED).
func renderAgentsBlock(in agentsInput) (string, error) {
	for _, check := range []struct{ value, label string }{
		{in.originalProofRoot, "originalProofRoot"},
		{in.frozenWitnessDigest, "frozenWitnessDigest"},
		{in.preventionRootDigest, "preventionRootDigest"},
	} {
		if err := assertDigestOnly(check.value, check.label); err != nil {
			return "", err
		}
	}
	for _, ids := range [][]string{in.lastGoodRunIDs, in.firstBadRunIDs, in.repairedRunIDs} {
		for _, id := range ids {
			if err := assertDigestOnly(id, "runId"); err != nil {
				return "", err
			}
		}
	}
	lines := []string{
		agentsMdBegin,
		"## FaultLine prevention invariants",
		"",
		"Auto-materialized from a `PREVENTION_VERIFIED` package. Digests and run IDs only — no commands, overlays, or transcripts.",
		"",
		"- Classification: `PREVENTION_VERIFIED`",
		jsexc.Concat("- Original proof root: `", in.originalProofRoot, "`"),
		jsexc.Concat("- Frozen witness digest: `", in.frozenWitnessDigest, "`"),
		jsexc.Concat("- Prevention package root: `", in.preventionRootDigest, "`"),
		jsexc.Concat("- Last-good run IDs: ", quotedIDs(in.lastGoodRunIDs)),
		jsexc.Concat("- First-bad run IDs: ", quotedIDs(in.firstBadRunIDs)),
		jsexc.Concat("- Repaired run IDs: ", quotedIDs(in.repairedRunIDs)),
		"",
		"Do not weaken these bindings without re-running `fl prevention write --from-bundle` and `fl prevention verify`.",
		agentsMdEnd,
	}
	return jsexc.Concat(jsexc.Join(lines, "\n"), "\n"), nil
}

func idStrings(v jsjson.Value) []string {
	items := v.Items()
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.Str()
	}
	return out
}

// UpsertAgentsMd ports upsertFaultLineAgentsMd (src/agents-md.ts:56) as
// cli-app.ts's upsertAgentsMdFromPrevention calls it for a
// PREVENTION_VERIFIED package: the digests come from the written body (a
// schema-parsed prevention.json), and preventionRootDigest, the written
// root, is recomputed from it (verifiedRootDigest). It returns CREATED,
// UPDATED or UNCHANGED, or the thrown error (which the CLI swallows).
func UpsertAgentsMd(repo string, body jsjson.Value) (status string, err error) {
	err = jsexc.Try(func() { status = upsertAgentsMd(repo, body, verifiedRootDigest(body)) })
	return status, err
}

// verifiedRootDigest is the rootDigest writePreventionProof signs for body
// under PREVENTION_VERIFIED (src/prevention-proof.ts:472-481): the manifest
// is a function of the body and the classification.
func verifiedRootDigest(body jsjson.Value) string {
	return jsexc.Must(canonical.DigestJSON(unsignedManifest(body, "PREVENTION_VERIFIED")))
}

func upsertAgentsMd(repository string, body jsjson.Value, rootDigest string) string {
	path := nodefs.Join(nodefs.Resolve(repository), "AGENTS.md")
	block, err := renderAgentsBlock(agentsInput{
		originalProofRoot:    body.Get("originalProofRoot").Str(),
		frozenWitnessDigest:  body.Get("frozenWitnessDigest").Str(),
		preventionRootDigest: rootDigest,
		lastGoodRunIDs:       idStrings(body.Get("lastGood", "runIds")),
		firstBadRunIDs:       idStrings(body.Get("firstBad", "runIds")),
		repairedRunIDs:       idStrings(body.Get("repaired", "runIds")),
	})
	if err != nil {
		jsexc.Throw(err)
	}
	existing := ""
	if nodefs.Exists(path) {
		existing = jsexc.Must(nodefs.ReadText(path))
	}
	next := mergeAgentsMd(existing, block)
	if next == existing {
		return "UNCHANGED"
	}
	if err := nodefs.MkdirAll(nodefs.Dirname(path), 0o777); err != nil {
		jsexc.Throw(err)
	}
	if err := nodefs.WriteFileUTF8(path, next, 0o644, false); err != nil {
		jsexc.Throw(err)
	}
	if existing == "" {
		return "CREATED"
	}
	return "UPDATED"
}

// mergeAgentsMd is the block placement of src/agents-md.ts:63-74. The
// markers are ASCII, so byte offsets of the WTF-8 text cut it where the
// UTF-16 offsets do.
func mergeAgentsMd(existing, block string) string {
	begin := strings.Index(existing, agentsMdBegin)
	end := strings.Index(existing, agentsMdEnd)
	switch {
	case begin >= 0 && end > begin:
		// existing.slice(afterEnd).replace(/^\r?\n/, "")
		trailing := existing[end+len(agentsMdEnd):]
		if strings.HasPrefix(trailing, "\r\n") {
			trailing = trailing[2:]
		} else if strings.HasPrefix(trailing, "\n") {
			trailing = trailing[1:]
		}
		return existing[:begin] + block + trailing
	case jsstr.Trim(existing) == "":
		return block
	}
	return trimTrailingWhitespace(existing) + "\n\n" + block
}

// trimTrailingWhitespace is replace(/\s*$/, ""): the trailing run of
// ECMAScript whitespace and line terminators.
func trimTrailingWhitespace(s string) string {
	units := jsstr.ToUTF16(s)
	end := len(units)
	for end > 0 && jsstr.IsWhitespace(units[end-1]) {
		end--
	}
	return jsstr.FromUTF16(units[:end])
}
