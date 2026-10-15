//go:build faultline_test

package difftest

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/redaction"
)

// corpusGoldenFile is the corpus replay golden in difftest/testdata/golden.
const corpusGoldenFile = "secret-corpus.jsonl"

type corpusGolden struct {
	ID           string `json:"id"`
	RedactedText string `json:"redactedText"`
	Report       string `json:"report"`
}

type corpusLabel struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

func loadCorpusGolden(t *testing.T, repo string) map[string]corpusGolden {
	f, err := os.Open(filepath.Join(repo, "difftest", "testdata", "golden", corpusGoldenFile))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[string]corpusGolden{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var g corpusGolden
		if err := json.Unmarshal(sc.Bytes(), &g); err != nil {
			t.Fatal(err)
		}
		out[g.ID] = g
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func loadCorpusLabels(t *testing.T, repo string) []corpusLabel {
	raw, err := os.ReadFile(filepath.Join(repo, "benchmarks", "secret-scanner-corpus", "labels.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []corpusLabel `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Cases
}

// TestSecretCorpusParity replays every labelled secret-scanner corpus file
// through the Go redactor and compares text and report with the TS goldens.
func TestSecretCorpusParity(t *testing.T) {
	repo := repoRoot(t)
	golden := loadCorpusGolden(t, repo)
	labels := loadCorpusLabels(t, repo)
	if len(labels) != len(golden) || len(labels) < 200 {
		t.Fatalf("labels %d goldens %d", len(labels), len(golden))
	}
	for _, c := range labels {
		text, err := nodefs.ReadText(filepath.Join(repo, "benchmarks", "secret-scanner-corpus", filepath.FromSlash(c.Path)))
		if err != nil {
			t.Fatal(err)
		}
		out, report := redaction.RedactText(text, "corpus:"+c.ID)
		want, ok := golden[c.ID]
		if !ok {
			t.Errorf("%s: no golden", c.ID)
			continue
		}
		if out != want.RedactedText || canonicalOf(report.JSON()) != want.Report {
			t.Errorf("%s: redaction differs", c.ID)
		}
	}
}
