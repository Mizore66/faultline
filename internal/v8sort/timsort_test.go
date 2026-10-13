package v8sort

import (
	"encoding/json"
	"os"
	"os/exec"
	"slices"
	"testing"
)

// cmpFor is the comparator both sides use. Mode 0 is consistent with many
// ties (checks stability); mode 1 is inconsistent (a pseudo-random sign for
// each ordered pair), so the result depends on every comparison V8 makes.
func cmpFor(mode, seed int) func(a, b int) int {
	return func(a, b int) int {
		if mode == 0 {
			return (a % 7) - (b % 7)
		}
		h := uint32(a*73856093) ^ uint32(b*19349663) ^ uint32(seed*83492791)
		h ^= h >> 13
		h *= 0x5bd1e995
		h ^= h >> 15
		return int(h%3) - 1
	}
}

const nodeScript = `
const out = [];
for (const [n, mode, seed] of JSON.parse(process.argv[1])) {
  const a = [];
  let x = (seed * 7919 + n) >>> 0;
  for (let i = 0; i < n; i++) { x = ((Math.imul(x, 1103515245) + 12345) >>> 0) & 0x7fffffff; a.push(x % (4 * n + 1)); }
  const cmp = mode === 0 ? (p, q) => (p % 7) - (q % 7) : (p, q) => {
    let h = (Math.imul(p, 73856093) ^ Math.imul(q, 19349663) ^ Math.imul(seed, 83492791)) >>> 0;
    h = (h ^ (h >>> 13)) >>> 0; h = Math.imul(h, 0x5bd1e995) >>> 0; h = (h ^ (h >>> 15)) >>> 0;
    return (h % 3) - 1;
  };
  out.push(a.sort(cmp));
}
process.stdout.write(JSON.stringify(out));
`

func TestSortMatchesV8(t *testing.T) {
	if os.Getenv("FAULTLINE_NODE_ORACLE") != "1" {
		t.Skip("set FAULTLINE_NODE_ORACLE=1 to compare with node")
	}
	var cases [][3]int
	for _, n := range []int{0, 1, 2, 3, 5, 31, 32, 33, 63, 64, 65, 100, 257, 1000, 3000, 20000} {
		for mode := 0; mode < 2; mode++ {
			for seed := 1; seed <= 3; seed++ {
				cases = append(cases, [3]int{n, mode, seed})
			}
		}
	}
	arg, _ := json.Marshal(cases)
	out, err := exec.Command("node", "-e", nodeScript, string(arg)).Output()
	if err != nil {
		t.Fatal(err)
	}
	var want [][]int
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		n, mode, seed := c[0], c[1], c[2]
		a := make([]int, 0, n)
		x := uint32(seed*7919 + n)
		for range n {
			x = (x*1103515245 + 12345) & 0x7fffffff
			a = append(a, int(x%uint32(4*n+1)))
		}
		Sort(a, cmpFor(mode, seed))
		if !slices.Equal(a, want[i]) && !(len(a) == 0 && len(want[i]) == 0) {
			t.Errorf("n=%d mode=%d seed=%d: differs from V8", n, mode, seed)
		}
	}
}
