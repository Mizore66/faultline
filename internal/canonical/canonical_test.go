package canonical

import (
	"slices"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func TestCanonicalJSONMatchesTS(t *testing.T) {
	v, err := jsjson.Parse(`{"b":1,"a":[{"d":null,"c":"é\ud800"}],"A":2,"_":3,"-":4,"10":5,"9":6,"n":1e21,"m":-0,"x":1.5e-7}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := CanonicalJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"9":6,"10":5,"_":3,"-":4,"a":[{"c":"é\ud800","d":null}],"A":2,"b":1,"m":0,"n":1e+21,"x":1.5e-7}`
	if got != want {
		t.Fatalf("CanonicalJSON = %s\nwant           %s", got, want)
	}
	if d, _ := DigestJSON(v); d != "sha256:39378331d042e1c105552bc3b6d08b0ceed9df0af4c63c941d8d2161dad68e3f" {
		t.Fatalf("DigestJSON = %s", d)
	}
}

func TestCanonicalRejectsNonFinite(t *testing.T) {
	v, _ := jsjson.Parse(`{"a":1e400}`)
	if _, err := CanonicalJSON(v); err == nil || err.Error() != "Value is not finite JSON: number" {
		t.Fatalf("err = %v", err)
	}
}

func TestSortLocale(t *testing.T) {
	got := SortLocale([]string{"rootDigest", "root-digest", "root_digest", "b", "B", "a", "A", "x10", "x2", "x1"})
	want := []string{"a", "A", "b", "B", "root_digest", "root-digest", "rootDigest", "x1", "x10", "x2"}
	if !slices.Equal(got, want) {
		t.Fatalf("SortLocale = %q", got)
	}
}
