package proofview

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// rawExchange sends request and reads the reply until the server closes.
func rawExchange(t *testing.T, url, request string) string {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(url, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(conn, request)
	reply, _ := io.ReadAll(conn)
	return string(reply)
}

func TestStartNodeServer(t *testing.T) {
	t.Run("route factory receives the bound url", func(t *testing.T) {
		var seen string
		s, err := startNodeServer(0, func(url string) http.HandlerFunc {
			seen = url
			return func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				writeNodeBody(w, r, 200, "hit "+r.Host)
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if seen == "" || seen != s.URL() || !strings.HasPrefix(seen, "http://127.0.0.1:") || strings.HasSuffix(seen, ":0") {
			t.Fatalf("factory url %q server url %q", seen, s.URL())
		}
		host := strings.TrimPrefix(s.URL(), "http://")
		reply := rawExchange(t, s.URL(), "GET / HTTP/1.1\r\nHost: "+host+"\r\nConnection: close\r\n\r\n")
		if !strings.HasPrefix(reply, "HTTP/1.1 200 OK\r\n") || !strings.Contains(reply, "hit "+host) {
			t.Fatalf("reply:\n%s", reply)
		}
	})
	t.Run("bad port", func(t *testing.T) {
		for _, port := range []float64{-1, 1.5, 70000} {
			_, err := startNodeServer(port, func(string) http.HandlerFunc { t.Fatal("factory called"); return nil })
			if err == nil || !strings.HasPrefix(err.Error(), "options.port should be >= 0 and < 65536. Received type number (") {
				t.Errorf("port %v: %v", port, err)
			}
		}
	})
	t.Run("expect marker handling wraps routes", func(t *testing.T) {
		s, err := startNodeServer(0, func(string) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) { t.Error("handler reached") }
		})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		reply := rawExchange(t, s.URL(), "GET / HTTP/1.1\r\nHost: x\r\nExpect: nope\r\nConnection: close\r\n\r\n")
		if !strings.HasPrefix(reply, "HTTP/1.1 417 Expectation Failed\r\n") {
			t.Fatalf("reply:\n%s", reply)
		}
	})
}
