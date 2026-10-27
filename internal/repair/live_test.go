//go:build faultline_test

package repair

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProposeBriefSendsSchemaAndRejectsHTTPError(t *testing.T) {
	packet, _ := CreateEvidencePacket(unboundInvestigation(t))
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(503)
		io.WriteString(w, "busy")
	}))
	defer srv.Close()
	t.Setenv("FAULTLINE_TEST_OPENAI_URL", srv.URL+"/v1/responses")
	t.Setenv("OPENAI_API_KEY", "fl-test-key")
	_, err := ProposeBrief(packet, "gpt-5.6")
	if err == nil || err.Error() != "OpenAI Responses request failed: 503 busy" {
		t.Fatalf("%v", err)
	}
	if !strings.HasPrefix(body, `{"model":"gpt-5.6","instructions":"You are preparing an evidence-bounded repair brief`) ||
		!strings.Contains(body, `"text":{"format":{"type":"json_schema","name":"faultline_repair_brief","strict":true,"schema":{"type":"object"`) {
		t.Fatalf("%s", body)
	}
	t.Setenv("OPENAI_API_KEY", "")
	if _, err := ProposeBrief(packet, "gpt-5.6"); err == nil || err.Error() != "OPENAI_API_KEY is required for a live GPT-5.6 repair brief." {
		t.Fatalf("%v", err)
	}
}
