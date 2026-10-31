package demofixture

import (
	"bytes"
	"os"
	"testing"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
)

const frozenWitnessDigest = "sha256:3d0d826bb1fd9901b290475201303c560a961df34e2faa374cc66310cb70327c"

// Literal rows captured from the frozen TypeScript fixture: state index, id,
// contribution, session, turn, hunks, label, tree digest (from the state
// tuple) and cached run id (from the retained demo-replay analysis).
var stateRows = []struct {
	index       int
	id, contrib string
	session     string
	turn        int
	hunks       []string
	label       string
	treeDigest  string
	runID       string
}{
	{0, "contribution-atlas", "atlas", "Session Atlas", 1, []string{}, "Atlas contribution boundary", "sha256:f63b4a667914a830960e795ce8d537fb0f5b5b5978969bf31c7cd0df281c5071", "sha256:c164768eaf37dba75358c55538fe26ab0be260b36859e9bd73790d372c7d35d4"},
	{1, "contribution-birch", "birch", "Session Birch", 1, []string{}, "Birch contribution boundary", "sha256:8b794f1a3d0b6d6ae70856dd3f58a87d251d29179a270c83cb3a4191a5df4203", "sha256:62fa57a407df416aee4930165bab0a37e47ee8f05d72d437fd5d4697b87a0fe0"},
	{2, "contribution-cedar", "cedar", "Session Cedar", 5, []string{"settlement-display-default", "settlement-display-boundary"}, "Cedar contribution boundary", "sha256:0f3bdbbb3cc17593b83ff700c77b9f471331f60a4f5092a16aa92d4d7c17ae6f", "sha256:cbd94415ebca095870208dbb6fa93a67318d1a7cd763b2b219c08483051327c5"},
	{3, "contribution-delta", "delta", "Session Delta", 1, []string{"settlement-display-default", "settlement-display-boundary"}, "Delta contribution boundary", "sha256:9693a50a5300d61bf2d874eb72a53f11c20162102f1817efe4f084c6d02145c8", "sha256:817e802278690147dbc543393039300b3e4854ae3c02620129ad10902aa50e0b"},
	{4, "cedar-turn-1", "cedar", "Session Cedar", 1, []string{}, "Turn 1", "sha256:d9b9bba6f508000fbbc8b74937605485a6d902568f12f95545ecc878537f116a", "sha256:6310d384fe600b988b8589771b09a75a15003acc3ab69ca0c4abf801a875fd6b"},
	{5, "cedar-turn-2", "cedar", "Session Cedar", 2, []string{}, "Turn 2", "sha256:a81e7899b105b3dc6044dc6f95eca624ccfaf76ccd94d1341f3eaa977d05d4f3", "sha256:1be778a5d4daf93438161ec1c84f81852e1488b37a53cc9894c2c33f682b021e"},
	{6, "cedar-turn-3", "cedar", "Session Cedar", 3, []string{}, "Turn 3", "sha256:4aec7e63961b2ef330fcca5e08813b347aa0ea3bac22fa42a39c10eed9a910ef", "sha256:16b64d17e5c70e5b5b12700835168df879d1e560205af4d44ab58cbca64bcf47"},
	{7, "cedar-turn-4", "cedar", "Session Cedar", 4, []string{}, "Turn 4 — last good", "sha256:be560c9feb40be3aba1f674ae292d04ef217ee629b3e57ded07fcad489297d95", "sha256:fc115d4f2d2e400fa86d47175650b5036348c96c7fd7af8c8fb64d8bfcf85c07"},
	{8, "cedar-turn-5", "cedar", "Session Cedar", 5, []string{"settlement-display-default", "settlement-display-boundary"}, "Turn 5 — first bad", "sha256:21321c5fbb0a6d19151e8d5bb6fa8d981106510f75620e2bd147870577ee52c2", "sha256:284eb58cddde1a97894cb5f8dfabeaf1d55739103530a014dcc9ba360cba4c7f"},
	{9, "cedar-turn-6", "cedar", "Session Cedar", 6, []string{"settlement-display-default", "settlement-display-boundary"}, "Turn 6", "sha256:5a08d7db1bdb1c4c1d00f115ee55d7b9437286b2e348a50ffcbdc4fd9cd7a5ac", "sha256:c73386e605d5634d9b8f5f93b733990a3f5ceb366b18f89e90dcec83efde3830"},
	{10, "cedar-turn-7", "cedar", "Session Cedar", 7, []string{}, "Turn 7 — repaired later", "sha256:b9b4d725e96b231b267bd058f3067e96f3f535ee5861dc3777e29a33f35f2cb5", "sha256:44ae239e2089e88f8bfaf8abd921b8e09d2017b8f29e018eef21c66b2b3e2038"},
	{11, "cedar-turn-8", "cedar", "Session Cedar", 8, []string{"settlement-display-default", "settlement-display-boundary"}, "Turn 8 — reintroduced", "sha256:fd4214827557368d23db8f80d7f65d3510bda1e466ac2f310a3dcf62882b935d", "sha256:e048b81f3069bf659cbad0603f5230098a4c73c4737f783ceabbefcc4a0780f8"},
	{12, "candidate-two-hunks", "counterfactual", "Counterfactual", 1, []string{"settlement-display-default", "settlement-display-boundary"}, "Apply two implicated hunks to last good", "sha256:8a6428073ef8f15505a986118e5721921e5a2bafd8abcdc6d5798e239a5b1d87", "sha256:1f7c6e0a7555e4d1e3db3e52ed0824e9d27d3883c4f41173335300d8ad629001"},
	{13, "candidate-remove-two-hunks", "counterfactual", "Counterfactual", 5, []string{}, "Remove two implicated hunks from first bad", "sha256:bd8b5f468fb1b7a4f0e9e8d023bff66dfc33e65b5978eda544a7c5a8b0a036e9", "sha256:f8aaffd59eabd8d1efe65df774de4cd1b4638f679159b8c2565773aff7a52c43"},
	{14, "candidate-source-only", "counterfactual", "Counterfactual", 2, []string{"settlement-display-default"}, "Apply source hunk only", "sha256:cd0d822c7a4288839af26dab5ac030ef4177803a02c185a024c4e941f4cd0213", "sha256:617719ff54520fd5bd7bb215d846e50000e799136525fe57f5fcfede571e6d5e"},
	{15, "candidate-renderer-only", "counterfactual", "Counterfactual", 3, []string{"settlement-display-boundary"}, "Apply boundary hunk only", "sha256:3ee2354dde41d636b4ba12313387f71099d5a14b3841af71646a69a7aa460b1f", "sha256:80b7e643d9b31a6992a56519433a21c12391a4e4e9954a7dca6f6921f0b5f145"},
	{16, "candidate-unresolved", "counterfactual", "Counterfactual", 4, []string{"partial-renderer-hunk"}, "Apply partial renderer hunk", "sha256:ebcecec1d9e08330fb308f609a6a256ba86e2858ee122936b007b33a3339b541", "sha256:e4aa9b53762316c125bd973e2e55ee840a369a1dc91eec4adb3398ef59952dc8"},
	{17, "repair-worktree", "repair", "Repair worktree", 1, []string{}, "Repair worktree", "sha256:6edf126537b8af2f39c4b24abe804a4893604fd2006ad71971eb1f741dbdc744", "sha256:3de2858a3f414e36560335d6e592ba434b4ee79acc56e1f0360ee95b279e6cee"},
	{18, "prevention-first-bad", "prevention", "Prevention proof", 2, []string{"settlement-display-default", "settlement-display-boundary"}, "First bad state", "sha256:1a276070d919b3654062ad5c9710df9175d95c34119c24eaa4e6ffdab2408705", "sha256:f75ca76db82e0625bb7301159b23f7e1ec9ba4ebb715f7d6a4f334c56fb4b203"},
	{19, "prevention-last-good", "prevention", "Prevention proof", 1, []string{}, "Last good state", "sha256:e5c866f453d30d79d9b654b6c69fb2f2a54693cb028b779279646db3d8a2ee6d", "sha256:eb24fdbb8a220503756b24ca047d72827d66010b339c04f1ad886cccfa7434d1"},
}

func TestFrozenWitnessExact(t *testing.T) {
	want, err := os.ReadFile("../../difftest/testdata/bases/demo-replay/witness/witness.json")
	if err != nil {
		t.Fatal(err)
	}
	w := CreateFrozenWitness()
	got := []byte(jsstr.ToUTF8(jsjson.StringifyIndent(w, "  ") + "\n"))
	if !bytes.Equal(got, want) {
		t.Fatalf("witness bytes differ\nwant=%s\ngot=%s", want, got)
	}
	if d := w.Get("digest").Str(); d != frozenWitnessDigest {
		t.Fatal(d)
	}
	// The digest covers the witness without its own digest field.
	without := jsjson.NewObj()
	for _, k := range w.Obj().Keys() {
		if k != "digest" {
			without.Set(k, w.Obj().Field(k))
		}
	}
	d, err := canonical.DigestJSON(jsjson.MakeObject(without))
	if err != nil || d != frozenWitnessDigest {
		t.Fatalf("digest of witness body = %q, %v", d, err)
	}
}

func TestReplayFixtureStates(t *testing.T) {
	states := fixtureStates()
	if len(states) != len(stateRows) {
		t.Fatalf("state count %d", len(states))
	}
	for _, row := range stateRows {
		s := states[row.index]
		if got := s.Obj().Keys(); len(got) != 8 || got[0] != "id" || got[1] != "contributionId" ||
			got[2] != "sessionName" || got[3] != "turnOrdinal" || got[4] != "hunks" ||
			got[5] != "treeDigest" || got[6] != "fidelity" || got[7] != "label" {
			t.Fatalf("state %d key order %v", row.index, got)
		}
		if s.Get("id").Str() != row.id || s.Get("contributionId").Str() != row.contrib ||
			s.Get("sessionName").Str() != row.session || int(s.Get("turnOrdinal").Num()) != row.turn ||
			s.Get("label").Str() != row.label || s.Get("treeDigest").Str() != row.treeDigest ||
			s.Get("fidelity").Str() != "DEMO_SAMPLE" {
			t.Fatalf("state %d mismatch: %s", row.index, jsjson.Stringify(s))
		}
		hs := s.Get("hunks").Items()
		if len(hs) != len(row.hunks) {
			t.Fatalf("state %d hunks %s", row.index, jsjson.Stringify(s.Get("hunks")))
		}
		for i, h := range hs {
			if h.Str() != row.hunks[i] {
				t.Fatalf("state %d hunk %d = %s", row.index, i, h.Str())
			}
		}
		if got := StateLabel(row.id); got != row.label {
			t.Fatalf("StateLabel(%q) = %q", row.id, got)
		}
		run := replayRun(s, CreateFrozenWitness())
		if run.Get("id").Str() != row.runID {
			t.Fatalf("state %s run id %s", row.id, run.Get("id").Str())
		}
		if run.Get("environmentDigest").Str() != "sha256:7de997bc873dc0e1909a6aa3bf7a5a40fe4958fbb0bb66b3c624e4c75074d334" {
			t.Fatal(run.Get("environmentDigest").Str())
		}
	}
}

func TestStateLabelFallsBackToOriginalID(t *testing.T) {
	for _, id := range []string{"unknown-state", "", "Cedar-Turn-1"} {
		if got := StateLabel(id); got != id {
			t.Fatalf("StateLabel(%q) = %q", id, got)
		}
	}
}
