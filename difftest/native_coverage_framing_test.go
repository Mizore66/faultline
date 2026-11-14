//go:build faultline_test

package difftest

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

type nativeChunk struct{ child, text string }

// nativeGuidedStream is one complete valid two-child guided stream whose output
// events are delivered in the given order (children's chunks may interleave).
func nativeGuidedStream(t *testing.T, order []nativeChunk) []byte {
	t.Helper()
	events := []map[string]any{
		{"Action": "start", "Package": nativeGuidedPkg},
		{"Action": "run", "Package": nativeGuidedPkg, "Test": "TestGuidedNativeDiagnostics"},
	}
	for _, name := range []string{"a", "b"} {
		events = append(events, map[string]any{"Action": "run", "Package": nativeGuidedPkg, "Test": "TestGuidedNativeDiagnostics/" + name})
	}
	for _, c := range order {
		events = append(events, map[string]any{"Action": "output", "Package": nativeGuidedPkg, "Test": "TestGuidedNativeDiagnostics/" + c.child, "Output": c.text})
	}
	for _, name := range []string{"a", "b"} {
		events = append(events, map[string]any{"Action": "pass", "Package": nativeGuidedPkg, "Test": "TestGuidedNativeDiagnostics/" + name})
	}
	events = append(events,
		map[string]any{"Action": "pass", "Package": nativeGuidedPkg, "Test": "TestGuidedNativeDiagnostics"},
		map[string]any{"Action": "pass", "Package": nativeGuidedPkg})
	return nativeEventJSON(t, events)
}

// nativeSplitRunes cuts text into pieces of at most n runes, never inside one.
func nativeSplitRunes(text string, n int) []string {
	var pieces []string
	runes := []rune(text)
	for len(runes) > 0 {
		k := min(n, len(runes))
		pieces = append(pieces, string(runes[:k]))
		runes = runes[k:]
	}
	return pieces
}

// go test -json chunks Output at arbitrary positions: a record split inside the
// marker, a quoted string, an escape or a multi-byte character must rebuild to
// exactly the same message, per (package, test), and never splice contexts.
func TestNativeOutputsReassembleChunkedRecords(t *testing.T) {
	const message = "full \"result\" \\ path\n\tünï 日本語 \U0001F600 end"
	line := "    diagnostics_test.go:1: NATIVE-DIAGNOSTIC " + strconv.Quote(message) + "\n"
	guided := []nativeEvidencePackage{nativeGuidedPackage}
	for _, size := range []int{1, 2, 3, 7, 16, len([]rune(line))} {
		var order []nativeChunk
		for _, piece := range nativeSplitRunes(line, size) {
			order = append(order, nativeChunk{"a", piece})
		}
		// Child b's own record interleaves between a's pieces.
		other := "    diagnostics_test.go:2: NATIVE-DIAGNOSTIC \"other\"\n"
		interleaved := []nativeChunk{order[0], {"b", other[:20]}}
		interleaved = append(interleaved, order[1:]...)
		interleaved = append(interleaved, nativeChunk{"b", other[20:]})
		out, err := nativeOutputsFor(nativeGuidedStream(t, interleaved), nil, guided)
		if err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		if want := strconv.Quote(message) + "\n" + strconv.Quote("other") + "\n"; out[nativeGuidedPkg] != want {
			t.Fatalf("size %d: got %q want %q", size, out[nativeGuidedPkg], want)
		}
	}
	// Ordinary non-marker lines, even split, are ignored once complete.
	ordinary := []nativeChunk{{"a", "=== RU"}, {"a", "N x\n"},
		{"a", "    t.go:1: NATIVE-DIAGNOSTIC \"a\"\n"}, {"b", "    t.go:2: NATIVE-DIAGNOSTIC \"b\"\n"}}
	if _, err := nativeOutputsFor(nativeGuidedStream(t, ordinary), nil, guided); err != nil {
		t.Fatal(err)
	}
}

func TestNativeOutputsRejectBadFraming(t *testing.T) {
	guided := []nativeEvidencePackage{nativeGuidedPackage}
	good := func(child string) []nativeChunk {
		return []nativeChunk{{child, "    t.go:1: NATIVE-DIAGNOSTIC \"" + child + "\"\n"}}
	}
	for _, tc := range []struct {
		name  string
		order []nativeChunk
	}{
		// Both children already have a complete record, so only the unfinished
		// trailing diagnostic can make these streams invalid.
		{"truncated-record-at-pass", append(append(good("a"), good("b")...), nativeChunk{"b", "    t.go:2: NATIVE-DIAGNOSTIC \"unfinis"})},
		{"complete-json-without-newline", append(append(good("a"), good("b")...), nativeChunk{"b", "    t.go:2: NATIVE-DIAGNOSTIC \"b\""})},
		{"split-marker-never-finished", append(append(good("a"), good("b")...), nativeChunk{"b", "    t.go:2: NATIVE-DIAG"}, nativeChunk{"b", "NOSTIC \"x\""})},
		{"duplicate-after-reassembly", append(good("a"), nativeChunk{"b", "t: NATIVE-DIAGNOSTIC \"x\"\nt: NATIVE-DIAGNOSTIC "}, nativeChunk{"b", "\"y\"\n"})},
		{"malformed-after-reassembly", append(good("a"), nativeChunk{"b", "t: NATIVE-DIAGNOSTIC \"x"}, nativeChunk{"b", "\n"})},
		{"empty-message", append(good("a"), nativeChunk{"b", "t: NATIVE-DIAGNOSTIC \"\"\n"})},
		{"one-child-cannot-finish-another", []nativeChunk{{"a", "t: NATIVE-DIAGNOSTIC \"a"}, {"b", "\"\n"}, {"b", "t: NATIVE-DIAGNOSTIC \"b\"\n"}}},
		{"line-over-cap", append(append(good("a"), good("b")...), nativeChunk{"b", "t: NATIVE-DIAGNOSTIC \"" + strings.Repeat("x", 1<<20+1)})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := nativeOutputsFor(nativeGuidedStream(t, tc.order), nil, guided); err == nil {
				t.Fatal("invalid framing accepted")
			}
		})
	}
	// Batch framing: partial-marker truncation, an oversized single complete
	// event, and package-level (Test == "") split markers.
	both := append(good("a"), good("b")...)
	for _, tc := range []struct {
		name  string
		extra nativeChunk
	}{
		{"partial-marker-at-pass", nativeChunk{"b", "    t.go:3: NATIVE-DIA"}},
		{"partial-marker-with-hyphen", nativeChunk{"b", "    t.go:3: NATIVE-DIAGNOST"}},
		{"marker-without-separator", nativeChunk{"b", "    t.go:3: NATIVE-DIAGNOSTIC\n"}},
		{"oversized-complete-event", nativeChunk{"b", strings.Repeat("y", 1<<20+1) + "\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := nativeOutputsFor(nativeGuidedStream(t, append(append([]nativeChunk{}, both...), tc.extra)), nil, guided); err == nil {
				t.Fatal("invalid framing accepted")
			}
		})
	}
	// A false NATIVE prefix is not a record: ordinary text ending in "NATIVE-"
	// style words that is completed by a newline is accepted.
	if _, err := nativeOutputsFor(nativeGuidedStream(t, append(append([]nativeChunk{}, both...), nativeChunk{"b", "see NATIVE-notes\n"})), nil, guided); err != nil {
		t.Fatalf("ordinary NATIVE text rejected: %v", err)
	}
	// Package-level output (Test == "") is buffered too: a marker split across two
	// unowned events is rejected, as is a complete unowned record.
	for name, parts := range map[string][]string{
		"split-unowned-marker": {"    t.go:4: NATIVE-DIAG", "NOSTIC \"x\"\n"},
		"whole-unowned-marker": {"    t.go:4: NATIVE-DIAGNOSTIC \"x\"\n"},
	} {
		// Start from a stream that is otherwise fully valid so only the unowned
		// output can be the reason for rejection.
		base := strings.Split(strings.TrimSuffix(string(nativeGuidedStream(t, both)), "\n"), "\n")
		inserted := []string{base[0]}
		for _, p := range parts {
			raw, err := json.Marshal(map[string]any{"Action": "output", "Package": nativeGuidedPkg, "Output": p})
			if err != nil {
				t.Fatal(err)
			}
			inserted = append(inserted, string(raw))
		}
		inserted = append(inserted, base[1:]...)
		if _, err := nativeOutputsFor([]byte(strings.Join(inserted, "\n")+"\n"), nil, guided); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	valid := nativeGuidedStream(t, append(good("a"), good("b")...))
	if _, err := nativeOutputsFor(valid, nil, guided); err != nil {
		t.Fatal(err)
	}
	// Foreign-package and late (after completion) output stay rejected.
	for name, extra := range map[string]map[string]any{
		"foreign": {"Action": "output", "Package": nativeCLIPkg, "Test": "TestJudgeDemoNativeDiagnostics/x", "Output": "t: NATIVE-DIAGNOSTIC \"p\"\n"},
		"late":    {"Action": "output", "Package": nativeGuidedPkg, "Test": "TestGuidedNativeDiagnostics/a", "Output": "late\n"},
	} {
		raw, err := json.Marshal(extra)
		if err != nil {
			t.Fatal(err)
		}
		data := append(append([]byte{}, valid...), append(raw, '\n')...)
		if _, err := nativeOutputsFor(data, nil, guided); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	// A parent-level emitter (the parent is still running) stays rejected.
	parent := nativeGuidedStream(t, append(good("a"), good("b")...))
	lines := strings.Split(strings.TrimSuffix(string(parent), "\n"), "\n")
	raw, err := json.Marshal(map[string]any{"Action": "output", "Package": nativeGuidedPkg, "Test": "TestGuidedNativeDiagnostics", "Output": "t: NATIVE-DIAGNOSTIC \"p\"\n"})
	if err != nil {
		t.Fatal(err)
	}
	inserted := append(append(append([]string{}, lines[:len(lines)-2]...), string(raw)), lines[len(lines)-2:]...)
	if _, err := nativeOutputsFor([]byte(strings.Join(inserted, "\n")+"\n"), nil, guided); err == nil {
		t.Fatal("parent-level emitter accepted")
	}
}
