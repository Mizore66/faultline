//go:build faultline_test

package witness

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
)

const liveSecret = "sk-abcdefghijklmnopqrstuvwxyz0123456789ABCD"

const liveProviderSchema = `{"type":"object","additionalProperties":false,"required":["behavior","command","overlayFiles","expectedExitCode","timeoutSeconds","safetyNotes"],"properties":{"behavior":{"type":"string"},"command":{"type":"string"},"overlayFiles":{"type":"array","items":{"type":"string"},"maxItems":5},"expectedExitCode":{"type":"integer","minimum":0,"maximum":255},"timeoutSeconds":{"type":"integer","minimum":1,"maximum":120},"safetyNotes":{"type":"array","items":{"type":"string"},"maxItems":8}}}`

const liveInstructions = "Propose an executable witness from the blinded incident packet. You have not been given candidate sessions, turns, diffs, timeline data, or localization results. Do not assign verdicts, name a culprit, or make causal claims. Return only the required structured proposal."

const liveProposal = `{"behavior":"b","command":"node w.mjs","overlayFiles":["w.mjs"],"expectedExitCode":0,"timeoutSeconds":30,"safetyNotes":["n"]}`

type liveRecorder struct {
	mu       sync.Mutex
	bodies   []string
	auths    []string
	status   int
	response string
}

func newLiveRecorder(t *testing.T, response string) *liveRecorder {
	t.Helper()
	r := &liveRecorder{response: response}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.bodies = append(r.bodies, string(b))
		r.auths = append(r.auths, req.Header.Get("Authorization"))
		r.mu.Unlock()
		if r.status != 0 {
			w.WriteHeader(r.status)
		}
		io.WriteString(w, r.response)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAULTLINE_TEST_OPENAI_URL", srv.URL+"/v1/responses")
	t.Setenv("OPENAI_API_KEY", "env-key")
	return r
}

func jsString(s string) string { return jsjson.Stringify(jsjson.MakeString(s)) }

func outputBody(text string) string {
	return `{"output":[{"type":"message","content":[{"type":"output_text","text":` + jsString(text) + `}]}]}`
}

func livePacket(t *testing.T, ciLog string) jsjson.Value {
	t.Helper()
	v, err := jsjson.Parse(`{"symptom":"refund fails","ciLog":` + jsString(ciLog) + `,"repositoryLanguage":"TypeScript","repositorySummary":"payments"}`)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestLiveWitnessRequest(t *testing.T) {
	r := newLiveRecorder(t, outputBody(liveProposal))
	got, err := ProposeWitnessWithGpt(livePacket(t, "token "+liveSecret+" done"), LiveWitnessOptions{APIKey: ptr("k1")})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.bodies) != 1 || r.auths[0] != "Bearer k1" {
		t.Fatalf("%v %v", r.bodies, r.auths)
	}
	body := r.bodies[0]
	if strings.Contains(body, liveSecret) {
		t.Fatalf("secret sent: %s", body)
	}
	parsed, err := jsjson.Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	if keys := strings.Join(parsed.Obj().Keys(), ","); keys != "model,instructions,input,text" {
		t.Fatal(keys)
	}
	input := parsed.Obj().Field("input")
	if input.Kind() != jsjson.String {
		t.Fatal("input is not a string")
	}
	wantPrefix := `{"model":"gpt-6.1","instructions":` + jsString(liveInstructions) + `,"input":`
	wantSuffix := `,"text":{"format":{"type":"json_schema","name":"faultline_witness_proposal","strict":true,"schema":` + liveProviderSchema + `}}}`
	if !strings.HasPrefix(body, wantPrefix) || !strings.HasSuffix(body, wantSuffix) {
		t.Fatalf("%s", body)
	}
	packet, err := jsjson.Parse(input.Str())
	if err != nil {
		t.Fatal(err)
	}
	if keys := strings.Join(packet.Obj().Keys(), ","); keys != "symptom,ciLog,repositoryLanguage,repositorySummary" {
		t.Fatal(keys)
	}
	digest, _ := canonical.DigestJSON(packet)
	if got.Obj().Field("incidentPacketDigest").Str() != digest {
		t.Fatalf("digest %s != %s", got.Obj().Field("incidentPacketDigest").Str(), digest)
	}
	if keys := strings.Join(got.Obj().Keys(), ","); keys != "proposal,incidentPacketDigest,redaction" {
		t.Fatal(keys)
	}
	if jsjson.Stringify(got.Obj().Field("proposal")) != liveProposal {
		t.Fatal(jsjson.Stringify(got.Obj().Field("proposal")))
	}
	red := got.Obj().Field("redaction").Obj()
	if red.Field("redacted").Kind() != jsjson.Bool || red.Field("redactedCount").Num() < 1 {
		t.Fatal(jsjson.Stringify(got.Obj().Field("redaction")))
	}
	if strings.Contains(jsjson.Stringify(got), liveSecret) {
		t.Fatal("secret in result")
	}
}

func modelOf(t *testing.T, body string) string {
	t.Helper()
	v, err := jsjson.Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	m := v.Obj().Field("model")
	if m.Kind() != jsjson.String {
		t.Fatal("no model")
	}
	return m.Str()
}

func TestProposeWitnessWithGpt(t *testing.T) {
	t.Run("model defaults", func(t *testing.T) {
		r := newLiveRecorder(t, outputBody(liveProposal))
		if _, err := ProposeWitnessWithGpt(livePacket(t, "x"), LiveWitnessOptions{}); err != nil {
			t.Fatal(err)
		}
		if r.auths[0] != "Bearer env-key" || modelOf(t, r.bodies[0]) != "gpt-6.1" {
			t.Fatal(r.auths, r.bodies)
		}
		if _, err := ProposeWitnessWithGpt(livePacket(t, "x"), LiveWitnessOptions{Model: ptr("")}); err != nil {
			t.Fatal(err)
		}
		if modelOf(t, r.bodies[1]) != "" {
			t.Fatal(r.bodies[1])
		}
		if _, err := ProposeWitnessWithGpt(livePacket(t, "x"), LiveWitnessOptions{Model: ptr("custom")}); err != nil || modelOf(t, r.bodies[2]) != "custom" {
			t.Fatal(err)
		}
	})
	const keyErr = "OPENAI_API_KEY is required for a live GPT-5.6 witness proposal. The judge demo does not require one."
	t.Run("key refusals", func(t *testing.T) {
		r := newLiveRecorder(t, outputBody(liveProposal))
		t.Setenv("OPENAI_API_KEY", "")
		if _, err := ProposeWitnessWithGpt(livePacket(t, "x"), LiveWitnessOptions{}); err == nil || err.Error() != keyErr {
			t.Fatalf("nil key: %v", err)
		}
		t.Setenv("OPENAI_API_KEY", "set")
		if _, err := ProposeWitnessWithGpt(livePacket(t, "x"), LiveWitnessOptions{APIKey: ptr("")}); err == nil || err.Error() != keyErr {
			t.Fatalf("empty key: %v", err)
		}
		if len(r.bodies) != 0 {
			t.Fatal("request made")
		}
	})
	t.Run("blind schema refusals", func(t *testing.T) {
		r := newLiveRecorder(t, outputBody(liveProposal))
		for _, in := range []string{
			`{"symptom":"a","ciLog":"b","repositoryLanguage":"c","repositorySummary":"d","extra":"e"}`,
			`{"symptom":"","ciLog":"b","repositoryLanguage":"c","repositorySummary":"d"}`,
			`{"symptom":"a","ciLog":"b","repositoryLanguage":"c"}`,
		} {
			v, _ := jsjson.Parse(in)
			if _, err := ProposeWitnessWithGpt(v, LiveWitnessOptions{}); err == nil {
				t.Fatalf("accepted %s", in)
			}
		}
		if len(r.bodies) != 0 {
			t.Fatal("request made")
		}
	})
	t.Run("storage oversized allowed", func(t *testing.T) {
		r := newLiveRecorder(t, outputBody(liveProposal))
		if _, err := ProposeWitnessWithGpt(livePacket(t, strings.Repeat("a", 600000)), LiveWitnessOptions{}); err != nil || len(r.bodies) != 1 {
			t.Fatal(err)
		}
	})
	t.Run("non-2xx", func(t *testing.T) {
		r := newLiveRecorder(t, "busy")
		r.status = 503
		if _, err := ProposeWitnessWithGpt(livePacket(t, "x"), LiveWitnessOptions{}); err == nil || err.Error() != "OpenAI Responses request failed: 503 busy" {
			t.Fatal(err)
		}
	})
}

func TestLiveWitnessResponse(t *testing.T) {
	quoted := jsString(liveProposal)
	good := []struct{ name, body string }{
		{"raw", outputBody(liveProposal)},
		{"fast path", `{"output_text":` + quoted + `}`},
		{"both", `{"output_text":` + quoted + `,"output":[{"content":[{"type":"output_text","text":` + quoted + `}]}]}`},
	}
	for _, c := range good {
		t.Run(c.name, func(t *testing.T) {
			newLiveRecorder(t, c.body)
			got, err := ProposeWitnessWithGpt(livePacket(t, "x"), LiveWitnessOptions{})
			if err != nil || jsjson.Stringify(got.Obj().Field("proposal")) != liveProposal {
				t.Fatal(err)
			}
		})
	}
	other := jsString(strings.Replace(liveProposal, `"b"`, `"c"`, 1))
	bad := []struct{ name, body, err string }{
		{"missing", `{}`, "OpenAI response did not contain exactly one output_text value"},
		{"empty", `{"output_text":""}`, "OpenAI response output_text fast path is missing non-empty text"},
		{"multiple", `{"output":[{"content":[{"type":"output_text","text":` + quoted + `},{"type":"output_text","text":` + quoted + `}]}]}`, "OpenAI response contains multiple output_text values; refusing an ambiguous structured result"},
		{"conflict", `{"output_text":` + other + `,"output":[{"content":[{"type":"output_text","text":` + quoted + `}]}]}`, "OpenAI response output_text fast path disagrees with raw output content"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			newLiveRecorder(t, c.body)
			got, err := ProposeWitnessWithGpt(livePacket(t, "x"), LiveWitnessOptions{})
			if err == nil || err.Error() != c.err || got.Kind() != jsjson.Undefined {
				t.Fatalf("%v", err)
			}
		})
	}
	refused := map[string]string{
		"malformed outer":  `{`,
		"malformed model":  outputBody(`{`),
		"missing field":    outputBody(`{"behavior":"b","command":"c","overlayFiles":[],"expectedExitCode":0,"timeoutSeconds":1}`),
		"empty behavior":   outputBody(strings.Replace(liveProposal, `"b"`, `""`, 1)),
		"extra key":        outputBody(strings.Replace(liveProposal, `{`, `{"x":1,`, 1)),
		"six overlays":     outputBody(strings.Replace(liveProposal, `["w.mjs"]`, `["a","b","c","d","e","f"]`, 1)),
		"nine notes":       outputBody(strings.Replace(liveProposal, `["n"]`, `["1","2","3","4","5","6","7","8","9"]`, 1)),
		"fractional exit":  outputBody(strings.Replace(liveProposal, `"expectedExitCode":0`, `"expectedExitCode":0.5`, 1)),
		"exit 256":         outputBody(strings.Replace(liveProposal, `"expectedExitCode":0`, `"expectedExitCode":256`, 1)),
		"fractional timeo": outputBody(strings.Replace(liveProposal, `"timeoutSeconds":30`, `"timeoutSeconds":1.5`, 1)),
		"timeout 0":        outputBody(strings.Replace(liveProposal, `"timeoutSeconds":30`, `"timeoutSeconds":0`, 1)),
		"timeout 121":      outputBody(strings.Replace(liveProposal, `"timeoutSeconds":30`, `"timeoutSeconds":121`, 1)),
	}
	for name, body := range refused {
		t.Run(name, func(t *testing.T) {
			newLiveRecorder(t, body)
			got, err := ProposeWitnessWithGpt(livePacket(t, "x"), LiveWitnessOptions{})
			if err == nil || got.Kind() != jsjson.Undefined {
				t.Fatal("accepted")
			}
		})
	}
	t.Run("empty overlay array allowed", func(t *testing.T) {
		newLiveRecorder(t, outputBody(`{"behavior":"b","command":"c","overlayFiles":[],"expectedExitCode":255,"timeoutSeconds":120,"safetyNotes":[]}`))
		got, err := ProposeWitnessWithGpt(livePacket(t, "x"), LiveWitnessOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if n := len(got.Obj().Field("proposal").Obj().Field("overlayFiles").Items()); n != 0 {
			t.Fatalf("overlay files = %d, want none", n)
		}
	})
	t.Run("empty string overlay entry allowed", func(t *testing.T) {
		newLiveRecorder(t, outputBody(`{"behavior":"b","command":"c","overlayFiles":[""],"expectedExitCode":255,"timeoutSeconds":120,"safetyNotes":[]}`))
		if _, err := ProposeWitnessWithGpt(livePacket(t, "x"), LiveWitnessOptions{}); err != nil {
			t.Fatal(err)
		}
	})
}
