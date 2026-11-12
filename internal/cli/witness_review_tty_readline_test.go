package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// feed delivers the trace's writes one at a time with pauses between them, so
// chunk boundaries and the escape timeout behave as they did under Node.
func feed(steps []any) *io.PipeReader {
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		for _, s := range steps {
			switch v := s.(type) {
			case string:
				pw.Write([]byte(v))
				time.Sleep(60 * time.Millisecond)
			case float64:
				time.Sleep(time.Duration(v * float64(time.Second)))
			}
		}
		time.Sleep(200 * time.Millisecond)
	}()
	return pr
}

// ttyOut stands in for the pseudo-terminal's output post-processing: the
// terminal turns every LF into CR LF (ONLCR), so what Node's trace shows as
// "\r\r\n" is the question's "\r\n".
func ttyOut(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }

// TestWitnessReviewReadlineTraces replays, through the Go port, the keys that
// were typed into Node 22.22.2's readline/promises question on a real PTY
// (testdata readline-traces.jsonl, captured from the frozen runtime and never
// produced by this code) and requires the identical terminal bytes and result.
func TestWitnessReviewReadlineTraces(t *testing.T) {
	path := filepath.Join("..", "..", "difftest", "testdata", "witness-review", "readline-traces.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	n := 0
	for sc.Scan() {
		var row struct {
			Name     string `json:"name"`
			Steps    []any  `json:"steps"`
			Cols     int    `json:"cols"`
			Terminal string `json:"terminal"`
			Result   string `json:"result"`
		}
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		n++
		t.Run(row.Name, func(t *testing.T) {
			const prompt = `Approve & freeze as "x"? [y/N] `
			var out bytes.Buffer
			cols := row.Cols
			line, ok, qerr := nodeReadlineQuestion(feed(row.Steps), &out, prompt, nodeReadlineOptions{Columns: func() int { return cols }})
			got := ttyOut(out.String())
			if got != row.Terminal {
				t.Fatalf("terminal bytes differ\n got %q\nwant %q", got, row.Terminal)
			}
			result := "ANS=" + jsQuote(line)
			if qerr != nil {
				result = "ERR=" + qerr.Error()
			}
			if !ok && qerr == nil {
				result = ""
			}
			if result != row.Result {
				t.Fatalf("result %q, want %q", result, row.Result)
			}
		})
	}
	if n < 20 {
		t.Fatalf("only %d traces", n)
	}
}

// jsQuote is JSON.stringify for the strings in the traces (no HTML escaping).
func jsQuote(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// The previous basic cases, written from the first trace round.
func TestWitnessReviewReadlineModel(t *testing.T) {
	const prompt = `Approve & freeze as "x"? [y/N] `
	const start = "\x1b[1G\x1b[0J" + prompt + "\x1b[32G"
	for _, c := range []struct {
		name, in, out, line string
		ok                  bool
		err                 string
	}{
		{"yes CR", "yes\r", start + "yes\r\n", "yes", true, ""},
		{"y LF", "y\n", start + "y\r\n", "y", true, ""},
		{"CRLF is one Enter", "y\r\n", start + "y\r\n", "y", true, ""},
		{"backspace redraws", "y\x7fn\r", start + "y\x1b[1G\x1b[0J" + prompt + "\x1b[32Gn\r\n", "n", true, ""},
		{"ctrl-h deletes", "y\x08\r", start + "y\x1b[1G\x1b[0J" + prompt + "\x1b[32G\r\n", "", true, ""},
		{"backspace on empty is silent", "\x7fy\r", start + "y\r\n", "y", true, ""},
		{"ctrl-d on empty aborts", "\x04", start, "", false, "Aborted with Ctrl+D"},
		{"ctrl-c aborts", "ab\x03", start + "ab", "", false, "Aborted with Ctrl+C"},
		{"utf-8 echo", "\xc3\xa9\r", start + "\xc3\xa9\r\n", "\xc3\xa9", true, ""},
		{"EOF settles nothing", "yes", start + "yes", "", false, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			line, ok, err := nodeReadlineQuestion(strings.NewReader(c.in), &out, prompt)
			if out.String() != c.out || line != c.line || ok != c.ok {
				t.Fatalf("out %q line %q ok %v; want %q %q %v", out.String(), line, ok, c.out, c.line, c.ok)
			}
			if (err == nil) != (c.err == "") || (err != nil && err.Error() != c.err) {
				t.Fatalf("err %v, want %q", err, c.err)
			}
		})
	}
}
