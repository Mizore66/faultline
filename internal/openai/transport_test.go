package openai_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mizore66/faultline/internal/openai"
)

func TestPostJSONSendsExactRequestAndReturnsNon2xxText(t *testing.T) {
	var auth, ctype, got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		auth, ctype, got = r.Header.Get("Authorization"), r.Header.Get("Content-Type"), string(b)
		w.WriteHeader(http.StatusUnprocessableEntity)
		io.WriteString(w, `{"error":"scripted"}`)
	}))
	defer server.Close()
	status, text, err := openai.PostJSON(server.URL, "Bearer test-key", []byte(`{"model":"gpt-6.1"}`))
	if err != nil || status != http.StatusUnprocessableEntity || text != `{"error":"scripted"}` {
		t.Fatalf("status=%d text=%q err=%v", status, text, err)
	}
	if auth != "Bearer test-key" || ctype != "application/json" || got != `{"model":"gpt-6.1"}` {
		t.Fatalf("auth=%q type=%q body=%q", auth, ctype, got)
	}
}

func TestPostJSONFetchFailed(t *testing.T) {
	_, _, err := openai.PostJSON("ftp://example.invalid/", "Bearer k", nil)
	if err == nil || err.Error() != "fetch failed" {
		t.Fatalf("%v", err)
	}
}
