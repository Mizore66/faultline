package proofview

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func testFormCapture(t *testing.T) {
	handler := func(string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			body, err := nodeFormBody(r)
			w.Header().Set("Content-Type", "text/plain")
			msg := "len=" + strconv.Itoa(len(body)) + ";marker=" + strings.Join(r.Header.Values(bodyMarker), ",") + ";path=" + nodeRequestPath(r)
			if err != nil {
				msg += ";err=" + err.Error()
			}
			if len(body) <= 16 {
				msg += ";body=" + string(body)
			}
			writeNodeBody(w, r, 200, msg)
		}
	}
	capture, err := startNodeServerWithOptions(0, nodeServerOptions{captureFormBody: true, reviewPaths: true}, handler)
	if err != nil {
		t.Fatal(err)
	}
	defer capture.Close()
	plain, err := startNodeServer(0, handler)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	const tail = "Host: x\r\nConnection: close\r\n"
	chunked := func(parts ...string) string {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(strconv.FormatInt(int64(len(p)), 16) + "\r\n" + p + "\r\n")
		}
		return b.String() + "0\r\n\r\n"
	}
	big := strings.Repeat("a", 32768)
	for _, c := range []struct {
		name, request string
		want          []string
	}{
		{"content-length", "POST / HTTP/1.1\r\n" + tail + "Content-Length: 7\r\n\r\na=1&b=2", []string{"len=7", "marker=nonzero", "body=a=1&b=2"}},
		{"chunked", "POST / HTTP/1.1\r\n" + tail + "Transfer-Encoding: chunked\r\n\r\n" + chunked("a=1", "&b=2"), []string{"len=7", "marker=nonzero", "body=a=1&b=2"}},
		{"zero body", "POST / HTTP/1.1\r\n" + tail + "Content-Length: 0\r\n\r\n", []string{"len=0", "marker=zero"}},
		{"no body", "POST / HTTP/1.1\r\n" + tail + "\r\n", []string{"len=0", "marker=zero"}},
		{"exact limit", "POST / HTTP/1.1\r\n" + tail + "Content-Length: 32768\r\n\r\n" + big, []string{"len=32768", "marker=nonzero"}},
		{"exact limit chunked", "POST / HTTP/1.1\r\n" + tail + "Transfer-Encoding: chunked\r\n\r\n" + chunked(big[:10000], big[10000:]), []string{"len=32768"}},
		{"over limit", "POST / HTTP/1.1\r\n" + tail + "Content-Length: 32769\r\n\r\n" + big + "a", []string{"len=0", "err=review form is too large"}},
		{"over limit chunked", "POST / HTTP/1.1\r\n" + tail + "Transfer-Encoding: chunked\r\n\r\n" + chunked(big, "a"), []string{"len=0", "err=review form is too large"}},
		{"review path", "POST //a///b?x=1 HTTP/1.1\r\n" + tail + "Content-Length: 0\r\n\r\n", []string{"path=/a/b"}},
		{"absolute target", "POST http://example.test//approve?x HTTP/1.1\r\n" + tail + "Content-Length: 0\r\n\r\n", []string{"path=/approve"}},
		{"backslash and dot segments kept", `POST /a\b/../c HTTP/1.1` + "\r\n" + tail + "Content-Length: 0\r\n\r\n", []string{`path=/a\b/../c`}},
	} {
		t.Run(c.name, func(t *testing.T) {
			reply := rawExchange(t, capture.URL(), c.request)
			for _, want := range c.want {
				if !strings.Contains(reply, want) {
					t.Fatalf("want %q in:\n%s", want, reply)
				}
			}
		})
	}
	t.Run("default transport keeps old behavior", func(t *testing.T) {
		reply := rawExchange(t, plain.URL(), "POST /a\\b/../c HTTP/1.1\r\n"+tail+"Content-Length: 3\r\n\r\nabc")
		for _, want := range []string{"len=0", "marker=nonzero", "err=form body not captured", "path=/a/c"} {
			if !strings.Contains(reply, want) {
				t.Fatalf("want %q in:\n%s", want, reply)
			}
		}
	})
	t.Run("keep-alive captures each body", func(t *testing.T) {
		req := func(body string) string {
			return "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body
		}
		reply := rawExchange(t, capture.URL(), req("one")+req("")+req("three")+"GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
		for _, want := range []string{"body=one", "len=0;marker=zero", "body=three"} {
			if !strings.Contains(reply, want) {
				t.Fatalf("want %q in:\n%s", want, reply)
			}
		}
	})
}

func TestNodeBodyMarker(t *testing.T) {
	s, err := startNodeServer(0, func(string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			values := r.Header.Values(bodyMarker)
			w.Header().Set("Content-Type", "text/plain")
			writeNodeBody(w, r, 200, "marker="+strings.Join(values, ",")+";n="+string(rune('0'+len(values))))
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const tail = "Host: x\r\nConnection: close\r\n"
	for _, c := range []struct{ name, head, want string }{
		{"no framing", "GET / HTTP/1.1\r\n" + tail + "\r\n", "marker=zero;n=1"},
		{"http/1.0", "GET / HTTP/1.0\r\n\r\n", "marker=zero;n=1"},
		{"zero length", "POST / HTTP/1.1\r\n" + tail + "Content-Length: 0\r\n\r\n", "marker=zero;n=1"},
		{"positive length", "POST / HTTP/1.1\r\n" + tail + "Content-Length: 3\r\n\r\nabc", "marker=nonzero;n=1"},
		{"terminal chunk", "POST / HTTP/1.1\r\n" + tail + "Transfer-Encoding: chunked\r\n\r\n0\r\n\r\n", "marker=nonzero;n=1"},
		{"empty transfer-encoding", "POST / HTTP/1.1\r\n" + tail + "Transfer-Encoding:\r\n\r\n", "marker=nonzero;n=1"},
		{"forged zero", "POST / HTTP/1.1\r\n" + tail + "Faultline-Node-Body: zero\r\nContent-Length: 3\r\n\r\nabc", "marker=nonzero;n=1"},
		{"forged duplicate", "GET / HTTP/1.1\r\n" + tail + "faultline-node-body: nonzero\r\nFaultline-Node-Body: nonzero\r\n\r\n", "marker=zero;n=1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			reply := rawExchange(t, s.URL(), c.head)
			if !strings.Contains(reply, c.want) {
				t.Fatalf("want %q in:\n%s", c.want, reply)
			}
		})
	}
	t.Run("opt-in form capture", testFormCapture)
}
