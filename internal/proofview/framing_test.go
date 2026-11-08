package proofview

import (
	"net/http"
	"strings"
	"testing"
)

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
}
