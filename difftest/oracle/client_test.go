package oracle

import "testing"

func TestEchoRoundTripsLoneSurrogate(t *testing.T) {
	c := Start(t)
	defer c.Close()
	got, err := c.Call("echo", `{"s":"\ud800x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"s":"\ud800x"}` {
		t.Fatalf("echo = %s", got)
	}
}
