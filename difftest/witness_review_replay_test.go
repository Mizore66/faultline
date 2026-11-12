//go:build faultline_test

package difftest

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// Independent verification of witness records for the live review comparison.
// Nothing here imports internal/witness, internal/canonical or any production
// digest code: canonical JSON, SHA-256 digests, the record shapes and the
// write-once file format are restated from src/witness-lock.ts and
// src/canonical.ts. The live test runs this verifier on the stores written by
// BOTH the frozen TypeScript and Go servers after every mutation, so a wrong
// digest, a stale embedded copy or a wrong file mode fails here regardless of
// which side wrote it. The self-tests below run on every faultline_test run and
// prove the verifier rejects each tamper it claims to detect.

// icuLess is the localeCompare order for the ASCII keys used by witness
// records: letters compare case-insensitively first, lowercase before
// uppercase on a tie, then by code unit.
func icuLess(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	if la != lb {
		return la < lb
	}
	return a > b // lowercase (higher code unit) sorts first on a case tie
}

// canonicalJSON is src/canonical.ts canonicalJson for decoded JSON values.
func canonicalJSON(v any) string {
	var b strings.Builder
	var write func(any)
	write = func(v any) {
		switch x := v.(type) {
		case nil:
			b.WriteString("null")
		case bool:
			fmt.Fprint(&b, x)
		case json.Number:
			b.WriteString(x.String())
		case string:
			var s bytes.Buffer
			enc := json.NewEncoder(&s)
			enc.SetEscapeHTML(false)
			_ = enc.Encode(x)
			b.WriteString(strings.TrimSuffix(s.String(), "\n"))
		case []any:
			b.WriteByte('[')
			for i, e := range x {
				if i > 0 {
					b.WriteByte(',')
				}
				write(e)
			}
			b.WriteByte(']')
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Slice(keys, func(i, j int) bool { return icuLess(keys[i], keys[j]) })
			b.WriteByte('{')
			for i, k := range keys {
				if i > 0 {
					b.WriteByte(',')
				}
				write(k)
				b.WriteByte(':')
				write(x[k])
			}
			b.WriteByte('}')
		default:
			panic(fmt.Sprintf("unsupported JSON value %T", v))
		}
	}
	write(v)
	return b.String()
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func digestJSONValue(v any) string { return digestOf([]byte(canonicalJSON(v))) }

func decodeRecord(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v map[string]any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

func omitKey(m map[string]any, key string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if k != key {
			out[k] = v
		}
	}
	return out
}

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func asString(v any) string      { s, _ := v.(string); return s }

// recordFile reads one record, requiring the write-once format: canonical JSON
// plus exactly one LF, regular file, mode 0600 (POSIX).
func recordFile(path string) (map[string]any, []byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%s is not a regular file", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		return nil, nil, fmt.Errorf("%s has mode %v, want 0600", path, info.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	rec, err := decodeRecord(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	if want := canonicalJSON(rec) + "\n"; string(raw) != want {
		return nil, nil, fmt.Errorf("%s is not canonical JSON plus one LF", path)
	}
	return rec, raw, nil
}

// verifyProposalRecord recomputes every proposal digest.
func verifyProposalRecord(p map[string]any) error {
	if asString(p["schemaVersion"]) != "faultline.witness-proposal.v1" {
		return fmt.Errorf("proposal schemaVersion %v", p["schemaVersion"])
	}
	if got := digestJSONValue(p["incidentPacket"]); got != asString(p["incidentPacketDigest"]) {
		return fmt.Errorf("incidentPacketDigest %v, recomputed %s", p["incidentPacketDigest"], got)
	}
	w := asMap(p["witness"])
	if got := digestOf([]byte(asString(w["command"]))); got != asString(w["commandDigest"]) {
		return fmt.Errorf("commandDigest %v, recomputed %s", w["commandDigest"], got)
	}
	var listing []any
	last := ""
	for i, o := range w["overlays"].([]any) {
		overlay := asMap(o)
		bytesRaw, err := base64.StdEncoding.DecodeString(asString(overlay["bytesBase64"]))
		if err != nil {
			return fmt.Errorf("overlay %d base64: %w", i, err)
		}
		if got := digestOf(bytesRaw); got != asString(overlay["bytesDigest"]) {
			return fmt.Errorf("overlay %s bytesDigest %v, recomputed %s", overlay["path"], overlay["bytesDigest"], got)
		}
		path := asString(overlay["path"])
		if i > 0 && !icuLess(last, path) {
			return fmt.Errorf("overlays not in canonical order at %s", path)
		}
		last = path
		listing = append(listing, map[string]any{"path": overlay["path"], "bytesDigest": overlay["bytesDigest"]})
	}
	if listing == nil {
		listing = []any{}
	}
	if got := digestJSONValue(listing); got != asString(w["overlayDigest"]) {
		return fmt.Errorf("overlayDigest %v, recomputed %s", w["overlayDigest"], got)
	}
	if got := digestJSONValue(omitKey(p, "proposalDigest")); got != asString(p["proposalDigest"]) {
		return fmt.Errorf("proposalDigest %v, recomputed %s", p["proposalDigest"], got)
	}
	return nil
}

func verifyApprovalRecord(a, p map[string]any) error {
	w := asMap(p["witness"])
	for _, c := range [][3]string{
		{"schemaVersion", asString(a["schemaVersion"]), "faultline.witness-approval.v1"},
		{"proposalId", asString(a["proposalId"]), asString(p["proposalId"])},
		{"proposalDigest", asString(a["proposalDigest"]), asString(p["proposalDigest"])},
		{"incidentPacketDigest", asString(a["incidentPacketDigest"]), asString(p["incidentPacketDigest"])},
		{"reviewedCommandDigest", asString(a["reviewedCommandDigest"]), asString(w["commandDigest"])},
		{"reviewedOverlayDigest", asString(a["reviewedOverlayDigest"]), asString(w["overlayDigest"])},
		{"reviewerType", asString(a["reviewerType"]), "HUMAN"},
	} {
		if c[1] != c[2] {
			return fmt.Errorf("approval %s = %q, want %q", c[0], c[1], c[2])
		}
	}
	if strings.TrimSpace(asString(a["approvedBy"])) == "" {
		return fmt.Errorf("approval approvedBy is blank")
	}
	if got := digestJSONValue(omitKey(a, "approvalDigest")); got != asString(a["approvalDigest"]) {
		return fmt.Errorf("approvalDigest %v, recomputed %s", a["approvalDigest"], got)
	}
	return nil
}

func verifyFrozenRecord(f, p, a map[string]any) error {
	if asString(f["schemaVersion"]) != "faultline.frozen-witness.v1" {
		return fmt.Errorf("frozen schemaVersion %v", f["schemaVersion"])
	}
	if canonicalJSON(f["proposal"]) != canonicalJSON(p) {
		return fmt.Errorf("frozen record embeds a proposal that differs from the stored proposal")
	}
	if canonicalJSON(f["approval"]) != canonicalJSON(a) {
		return fmt.Errorf("frozen record embeds an approval that differs from the stored approval")
	}
	w := asMap(p["witness"])
	payload := map[string]any{
		"schemaVersion": "faultline.locked-witness.v1", "proposalId": p["proposalId"],
		"proposalDigest": p["proposalDigest"], "incidentPacketDigest": p["incidentPacketDigest"],
		"behavior": w["behavior"], "command": w["command"], "commandDigest": w["commandDigest"],
		"overlays": w["overlays"], "overlayDigest": w["overlayDigest"], "policy": w["policy"],
	}
	if got := digestJSONValue(payload); got != asString(f["witnessDigest"]) {
		return fmt.Errorf("witnessDigest %v, recomputed %s", f["witnessDigest"], got)
	}
	if got := digestJSONValue(omitKey(f, "frozenDigest")); got != asString(f["frozenDigest"]) {
		return fmt.Errorf("frozenDigest %v, recomputed %s", f["frozenDigest"], got)
	}
	return nil
}

// verifyWitnessStore verifies the store for one proposal and returns the state
// found: "none" (proposal only), "approved" or "frozen". A frozen record
// without an approval, or any digest/shape/mode fault, is an error.
func verifyWitnessStore(root, id string) (string, error) {
	dir := filepath.Join(root, ".faultline", "witnesses")
	p, _, err := recordFile(filepath.Join(dir, "proposals", id+".json"))
	if err != nil {
		return "", err
	}
	if err := verifyProposalRecord(p); err != nil {
		return "", err
	}
	a, _, aerr := recordFile(filepath.Join(dir, "approvals", id+".json"))
	f, _, ferr := recordFile(filepath.Join(dir, "frozen", id+".json"))
	aMissing, fMissing := os.IsNotExist(aerr), os.IsNotExist(ferr)
	if aerr != nil && !aMissing {
		return "", aerr
	}
	if ferr != nil && !fMissing {
		return "", ferr
	}
	switch {
	case aMissing && fMissing:
		return "none", nil
	case aMissing:
		return "", fmt.Errorf("frozen record without an approval")
	}
	if err := verifyApprovalRecord(a, p); err != nil {
		return "", err
	}
	if fMissing {
		return "approved", nil
	}
	if err := verifyFrozenRecord(f, p, a); err != nil {
		return "", err
	}
	return "frozen", nil
}

// storeSnapshot is every entry under root/.faultline with its mode and bytes,
// for exact cross-implementation tree comparison.
func storeSnapshot(root string) (map[string]string, error) {
	out := map[string]string{}
	base := filepath.Join(root, ".faultline")
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(base, path)
		mode := info.Mode().Perm().String()
		if runtime.GOOS == "windows" {
			mode = "-"
		}
		if d.IsDir() {
			out[filepath.ToSlash(rel)+"/"] = mode
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = mode + " " + string(raw)
		return nil
	})
	if os.IsNotExist(err) {
		return out, nil
	}
	return out, err
}

func diffSnapshots(a, b map[string]string) string {
	var diffs []string
	for k, av := range a {
		if bv, ok := b[k]; !ok {
			diffs = append(diffs, "only in first: "+k)
		} else if av != bv {
			diffs = append(diffs, "differs: "+k+fmt.Sprintf("\n  first  %q\n  second %q", av, bv))
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			diffs = append(diffs, "only in second: "+k)
		}
	}
	sort.Strings(diffs)
	return strings.Join(diffs, "\n")
}

// ---- verifier self-tests (hand-written fixture, no production code) ----

const verifierProposal = `{"incidentPacket":{"ciLog":"Expected USD; received EUR.","repositoryLanguage":"JavaScript","repositorySummary":"A Node service that completes refunds.","symptom":"refund currency"},` +
	`"incidentPacketDigest":"sha256:INC","proposalId":"p1","proposalOrigin":"HUMAN","proposedAt":"2026-07-16T12:00:00.000Z","schemaVersion":"faultline.witness-proposal.v1",` +
	`"witness":{"behavior":"b","command":"node witness.mjs","commandDigest":"sha256:CMD","overlayDigest":"sha256:OVL",` +
	`"overlays":[{"bytesBase64":"Y29uc29sZS5sb2coMSk=","bytesDigest":"sha256:BYT","path":"witness.mjs"}],"policy":{"credentials":"redacted","network":"disabled","timeoutSeconds":30}}}`

// buildVerifierStore writes a complete valid store using only this file's
// helpers and returns its root.
func buildVerifierStore(t *testing.T, state string) string {
	t.Helper()
	root := t.TempDir()
	rec, err := decodeRecord([]byte(verifierProposal))
	if err != nil {
		t.Fatal(err)
	}
	w := asMap(rec["witness"])
	overlays := w["overlays"].([]any)
	o := asMap(overlays[0])
	raw, _ := base64.StdEncoding.DecodeString(asString(o["bytesBase64"]))
	o["bytesDigest"] = digestOf(raw)
	w["commandDigest"] = digestOf([]byte(asString(w["command"])))
	w["overlayDigest"] = digestJSONValue([]any{map[string]any{"path": o["path"], "bytesDigest": o["bytesDigest"]}})
	rec["incidentPacketDigest"] = digestJSONValue(rec["incidentPacket"])
	rec["proposalDigest"] = digestJSONValue(rec)
	write := func(kind string, v map[string]any) {
		dir := filepath.Join(root, ".faultline", "witnesses", kind)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "p1.json"), []byte(canonicalJSON(v)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("proposals", rec)
	if state == "none" {
		return root
	}
	approval := map[string]any{
		"schemaVersion": "faultline.witness-approval.v1", "proposalId": "p1", "proposalDigest": rec["proposalDigest"],
		"incidentPacketDigest": rec["incidentPacketDigest"], "reviewedCommandDigest": w["commandDigest"],
		"reviewedOverlayDigest": w["overlayDigest"], "reviewerType": "HUMAN", "approvedBy": "R", "approvedAt": "2026-07-16T12:00:00.000Z",
	}
	approval["approvalDigest"] = digestJSONValue(approval)
	write("approvals", approval)
	if state == "approved" {
		return root
	}
	frozen := map[string]any{
		"schemaVersion": "faultline.frozen-witness.v1", "frozenAt": "2026-07-16T12:00:00.001Z",
		"proposal": rec, "approval": approval,
		"witnessDigest": digestJSONValue(map[string]any{
			"schemaVersion": "faultline.locked-witness.v1", "proposalId": "p1", "proposalDigest": rec["proposalDigest"],
			"incidentPacketDigest": rec["incidentPacketDigest"], "behavior": w["behavior"], "command": w["command"],
			"commandDigest": w["commandDigest"], "overlays": w["overlays"], "overlayDigest": w["overlayDigest"], "policy": w["policy"],
		}),
	}
	frozen["frozenDigest"] = digestJSONValue(frozen)
	write("frozen", frozen)
	return root
}

func TestWitnessReviewRecordVerifier(t *testing.T) {
	for _, state := range []string{"none", "approved", "frozen"} {
		if got, err := verifyWitnessStore(buildVerifierStore(t, state), "p1"); err != nil || got != state {
			t.Fatalf("%s store: state=%q err=%v", state, got, err)
		}
	}
	rewrite := func(t *testing.T, root, kind string, edit func(string) string) {
		t.Helper()
		path := filepath.Join(root, ".faultline", "witnesses", kind, "p1.json")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		edited := edit(string(raw))
		if edited == string(raw) {
			t.Fatal("tamper edit changed nothing")
		}
		if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name, state, kind string
		edit              func(string) string
		want              string
	}{
		{"command changed", "none", "proposals", func(s string) string { return strings.Replace(s, "node witness.mjs", "node evil.mjs", 1) }, "commandDigest"},
		{"overlay bytes changed", "none", "proposals", func(s string) string { return strings.Replace(s, "Y29uc29sZS5sb2coMSk=", "Y29uc29sZS5sb2coMik=", 1) }, "bytesDigest"},
		{"packet changed", "none", "proposals", func(s string) string { return strings.Replace(s, "refund currency", "refund curren", 1) }, "incidentPacketDigest"},
		{"reviewer changed", "approved", "approvals", func(s string) string { return strings.Replace(s, `"approvedBy":"R"`, `"approvedBy":"S"`, 1) }, "approvalDigest"},
		{"approval time changed", "frozen", "approvals", func(s string) string { return strings.Replace(s, "12:00:00.000Z", "12:00:09.000Z", 1) }, "approvalDigest"},
		{"freeze time changed", "frozen", "frozen", func(s string) string { return strings.Replace(s, "12:00:00.001Z", "12:00:09.001Z", 1) }, "frozenDigest"},
		{"missing trailing LF", "approved", "approvals", func(s string) string { return strings.TrimSuffix(s, "\n") }, "canonical JSON plus one LF"},
		{"non-canonical spacing", "approved", "approvals", func(s string) string { return strings.Replace(s, `"approvedBy":"R"`, `"approvedBy": "R"`, 1) }, "canonical JSON plus one LF"},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := buildVerifierStore(t, c.state)
			rewrite(t, root, c.kind, c.edit)
			if _, err := verifyWitnessStore(root, "p1"); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to mention %q", err, c.want)
			}
		})
	}
	t.Run("frozen without approval", func(t *testing.T) {
		root := buildVerifierStore(t, "frozen")
		if err := os.Remove(filepath.Join(root, ".faultline", "witnesses", "approvals", "p1.json")); err != nil {
			t.Fatal(err)
		}
		if _, err := verifyWitnessStore(root, "p1"); err == nil || !strings.Contains(err.Error(), "without an approval") {
			t.Fatalf("err = %v", err)
		}
	})
	if runtime.GOOS != "windows" {
		t.Run("wrong mode", func(t *testing.T) {
			root := buildVerifierStore(t, "approved")
			if err := os.Chmod(filepath.Join(root, ".faultline", "witnesses", "approvals", "p1.json"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := verifyWitnessStore(root, "p1"); err == nil || !strings.Contains(err.Error(), "want 0600") {
				t.Fatalf("err = %v", err)
			}
		})
	}
	t.Run("snapshot diff", func(t *testing.T) {
		a, b := buildVerifierStore(t, "approved"), buildVerifierStore(t, "approved")
		sa, _ := storeSnapshot(a)
		sb, _ := storeSnapshot(b)
		if d := diffSnapshots(sa, sb); d != "" {
			t.Fatalf("identical stores differ: %s", d)
		}
		c := buildVerifierStore(t, "frozen")
		sc, _ := storeSnapshot(c)
		if diffSnapshots(sa, sc) == "" {
			t.Fatal("different stores compared equal")
		}
	})
}

func TestLiveWitnessReviewDateCheck(t *testing.T) {
	start := time.Date(2026, 11, 12, 3, 0, 0, 900_000_000, time.UTC)
	end := start.Add(300 * time.Millisecond)
	ok := func(d string) string {
		return "HTTP/1.1 200 OK\r\ncontent-type: text/plain\r\nDate: " + d + "\r\nConnection: close\r\n\r\nx"
	}
	for _, c := range []struct {
		name, raw string
		bad       bool
	}{
		{"start second", ok("Thu, 12 Nov 2026 03:00:00 GMT"), false},
		{"end second", ok("Thu, 12 Nov 2026 03:00:01 GMT"), false},
		{"stale", ok("Thu, 12 Nov 2026 02:59:59 GMT"), true},
		{"future", ok("Thu, 12 Nov 2026 03:00:02 GMT"), true},
		{"not a date", ok("yesterday"), true},
		{"duplicate", "HTTP/1.1 200 OK\r\ncontent-type: text/plain\r\nDate: Thu, 12 Nov 2026 03:00:00 GMT\r\nDate: Thu, 12 Nov 2026 03:00:00 GMT\r\n\r\n", true},
		{"absent on a handler response", "HTTP/1.1 200 OK\r\ncontent-type: text/plain\r\nConnection: close\r\n\r\nx", true},
		{"bare parser refusal has none", "HTTP/1.1 400 Bad Request\r\nConnection: close\r\n\r\n", false},
		{"bare refusal with a stray Date still checked", "HTTP/1.1 400 Bad Request\r\nDate: yesterday\r\nConnection: close\r\n\r\n", true},
		{"not an HTTP response at all", "", false},
	} {
		err := liveCheckDate([]byte(c.raw), start, end)
		if (err != nil) != c.bad {
			t.Errorf("%s: error %v, want bad=%v", c.name, err, c.bad)
		}
	}
}

// A real Date mismatch between the two sides is a failure, never normalized.
func TestLiveWitnessReviewDateMismatchFails(t *testing.T) {
	resp := func(date string) string {
		return liveNormalize(t, "x", []byte("HTTP/1.1 200 OK\r\ncontent-type: text/plain\r\nDate: "+date+"\r\nConnection: close\r\n\r\nx"), "", "")
	}
	same := liveRecord{transcript: []string{"step a\n" + resp("Thu, 12 Nov 2026 03:00:00 GMT")}}
	if d := diffLiveRecords("same", same, same); len(d) != 0 {
		t.Fatalf("identical Date reported as different: %v", d)
	}
	other := liveRecord{transcript: []string{"step a\n" + resp("Thu, 12 Nov 2026 03:00:01 GMT")}}
	d := diffLiveRecords("rollover", same, other)
	if len(d) != 1 || !strings.Contains(d[0], "Thu, 12 Nov 2026 03:00:00 GMT") || !strings.Contains(d[0], "Thu, 12 Nov 2026 03:00:01 GMT") {
		t.Fatalf("a one-second Date difference was not reported verbatim: %v", d)
	}
}
