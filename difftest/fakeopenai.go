//go:build faultline_test

package difftest

import (
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
)

// startFakeOpenAI starts the writer replay's fake OpenAI Responses server
// on 127.0.0.1:0 for the duration of the test and returns its
// http://127.0.0.1:<port>/v1/responses URL (FAULTLINE_TEST_OPENAI_URL).
// For each request it appends one record to argvLog: "openai", the method,
// the request target, the Authorization and Content-Type headers ("" when
// absent) and the request body, joined by \x1f and terminated by \x1e. It
// then answers from the workspace file replyFile, read at request time:
// {"status": <int>, "contentType": "<header>", "body": "<raw response
// text>"}, the body sent as UTF-8. It reads no clock (no Date header). The
// TS twin is startFakeOpenAI in difftest/gen/fake-openai.ts.
func startFakeOpenAI(t *testing.T, ws, replyFile, argvLog string) (url string) {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		record := "openai\x1f" + req.Method + "\x1f" + req.RequestURI + "\x1f" + req.Header.Get("Authorization") + "\x1f" +
			req.Header.Get("Content-Type") + "\x1f" + string(body) + "\x1e"
		// One write per record, as the recorders do.
		if f, err := os.OpenFile(argvLog, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644); err == nil {
			f.WriteString(record)
			f.Close()
		}
		status, contentType, hasType, text := 500, "text/plain", true, "fake openai: cannot read reply file "+replyFile
		if b, err := os.ReadFile(filepath.Join(ws, filepath.FromSlash(replyFile))); err == nil {
			if v, err := jsjson.Parse(jsstr.DecodeUTF8(b)); err == nil && v.Kind() == jsjson.Object {
				status, text = int(v.Get("status").Num()), v.Get("body").Str()
				ct := v.Get("contentType")
				contentType, hasType = ct.Str(), ct.Kind() == jsjson.String
			}
		}
		out := jsstr.ToUTF8(text)
		h := w.Header()
		h["Date"] = nil // no clock read
		h.Set("Content-Length", strconv.Itoa(len(out)))
		if hasType {
			h.Set("Content-Type", contentType)
		} else {
			h["Content-Type"] = nil
		}
		w.WriteHeader(status)
		io.WriteString(w, out)
	})}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return "http://" + l.Addr().String() + "/v1/responses"
}
