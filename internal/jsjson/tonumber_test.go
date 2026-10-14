package jsjson

import (
	"encoding/json"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestToNumberTable(t *testing.T) {
	for s, want := range map[string]float64{
		"": 0, "  ": 0, "0": 0, "1e2": 100, " 5 ": 5, "\u00a05\ufeff": 5, "1.5": 1.5, ".5": 0.5, "5.": 5,
		"+3": 3, "-Infinity": math.Inf(-1), "0x10": 16, "0b101": 5, "0o17": 15, "1e400": math.Inf(1),
	} {
		if got := ToNumber(s); got != want {
			t.Errorf("ToNumber(%q) = %v, want %v", s, got, want)
		}
	}
	for _, s := range []string{"abc", "1_0", "0x", "0xg", "-0x10", "1e", "e5", ".", "+", "inf", "NaN", "1 2", "0x1p3", "Infinityx", "infinity", "\u180e5"} {
		if got := ToNumber(s); !math.IsNaN(got) {
			t.Errorf("ToNumber(%q) = %v, want NaN", s, got)
		}
	}
}

const numberScript = `
const strings = JSON.parse(require("fs").readFileSync(0, "utf8"));
const buf = new Float64Array(1);
const u = new Uint32Array(buf.buffer);
process.stdout.write(JSON.stringify(strings.map((s) => { buf[0] = Number(s); return u[1].toString(16) + ":" + u[0].toString(16); })));
`

func bitsOf(f float64) string {
	b := math.Float64bits(f)
	if math.IsNaN(f) {
		b = 0x7ff8000000000000 // V8 canonical NaN
	}
	return strconv.FormatUint(b>>32, 16) + ":" + strconv.FormatUint(b&0xffffffff, 16)
}

func TestToNumberMatchesNode(t *testing.T) {
	if os.Getenv("FAULTLINE_NODE_ORACLE") != "1" {
		t.Skip("set FAULTLINE_NODE_ORACLE=1 to compare with node")
	}
	rng := rand.New(rand.NewSource(8))
	alphabet := []string{"0", "1", "2", "9", "e", "E", "+", "-", ".", "x", "X", "o", "b", "f", "A", " ", "\t", "\u00a0", "\ufeff", " ", "\u180e", "_", "Infinity", "inf", "n", "N", "z"}
	var cases []string
	for _, s := range []string{"", " ", "0", "-0", "1e2", " 5 ", "0x", "0X1F", "0b2", "0o8", "1e1000", "-1e-1000", "0x" + strings.Repeat("f", 300), "0x" + strings.Repeat("1", 20), "9007199254740993", "0.1e-5", "Infinity", "+Infinity", "-Infinity", "infinity", "1_0"} {
		cases = append(cases, s)
	}
	for len(cases) < 20000 {
		n := rng.Intn(9)
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteString(alphabet[rng.Intn(len(alphabet))])
		}
		cases = append(cases, b.String())
	}
	// Structured numeric strings reach deeper than random soup.
	for i := 0; i < 4000; i++ {
		s := strconv.FormatFloat(rng.NormFloat64()*math.Pow(10, float64(rng.Intn(40)-20)), 'g', rng.Intn(20)-1, 64)
		if rng.Intn(3) == 0 {
			s = " " + s + " "
		}
		cases = append(cases, s)
	}
	input, _ := json.Marshal(cases)
	cmd := exec.Command("node", "-e", numberScript)
	cmd.Stdin = strings.NewReader(string(input))
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(cases) {
		t.Fatalf("%d results for %d cases", len(want), len(cases))
	}
	for i, s := range cases {
		if got := bitsOf(ToNumber(s)); got != want[i] {
			t.Errorf("ToNumber(%q) bits %s, node %s", s, got, want[i])
		}
	}
}
