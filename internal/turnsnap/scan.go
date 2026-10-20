package turnsnap

import (
	"bytes"
	"math"
	"strings"

	"github.com/Mizore66/faultline/internal/allowlist"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsre"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/redaction"
)

var (
	entropyTokenPattern = jsre.MustCompile(`[A-Za-z0-9_+\-\/=]{40,}`, "g")
	integrityPrefix     = jsre.MustCompile(`^sha(?:256|512)-`, "i")
	hexToken            = jsre.MustCompile(`^[a-f0-9]{40,}$`, "i")
)

// shannonEntropy ports src/turn-snapshot.ts:686: the sum runs over the
// distinct code points in first-seen order (a Map's iteration order), with
// V8's Math.log2, so the comparison with 4.5 rounds as in TS.
func shannonEntropy(value string) float64 {
	units := jsstr.ToUTF16(value)
	length := float64(len(units))
	if length == 0 {
		return 0
	}
	// for...of yields a surrogate pair as one character and a lone
	// surrogate as itself; key each by its code point (a lone surrogate's
	// is its unit, distinct from every pair's).
	counts := map[rune]int{}
	var order []rune
	for i := 0; i < len(units); i++ {
		c := rune(units[i])
		if c >= 0xD800 && c <= 0xDBFF && i+1 < len(units) && units[i+1] >= 0xDC00 && units[i+1] <= 0xDFFF {
			c = 0x10000 + (c-0xD800)<<10 + rune(units[i+1]-0xDC00)
			i++
		}
		if counts[c] == 0 {
			order = append(order, c)
		}
		counts[c]++
	}
	entropy := 0.0
	for _, c := range order {
		probability := float64(counts[c]) / length
		entropy -= float64(probability * jsLog2(probability))
	}
	return entropy
}

// IsSuspiciousHighEntropyToken ports src/turn-snapshot.ts:764
// isSuspiciousHighEntropyToken.
func IsSuspiciousHighEntropyToken(tok string) bool {
	if shannonEntropy(tok) < 4.5 {
		return false
	}
	if strings.Contains(tok, "/") {
		return false
	}
	if integrityPrefix.Test(tok) {
		return false
	}
	return !hexToken.Test(tok)
}

// looksBinary ports src/turn-snapshot.ts:698.
func looksBinary(b []byte) bool {
	return bytes.IndexByte(b[:min(len(b), 8_192)], 0) >= 0
}

// AssertNoSecrets runs src/turn-snapshot.ts:703 assertNoSnapshotSecrets on
// each repository-relative path under root, in order, and returns the
// first refusal (*Error) or read failure (Node's error). Paths that are not
// regular files or are empty are skipped, as TS skips missing candidates
// and zero-byte files.
func AssertNoSecrets(root string, paths []string, a allowlist.Allowlist) error {
	for _, rel := range paths {
		absolute := nodefs.Join(append([]string{root}, strings.Split(rel, "/")...)...)
		stat, err := nodefs.Lstat(absolute)
		if err != nil || !stat.Mode().IsRegular() {
			continue
		}
		if err := assertNoSnapshotSecrets(rel, absolute, int(stat.Size()), a); err != nil {
			return err
		}
	}
	return nil
}

// AssertNoSnapshotSecrets ports src/turn-snapshot.ts:703 for one file whose
// lstat size is size: the capture planner calls it with the size it
// recorded.
func AssertNoSnapshotSecrets(relativePath, absolutePath string, size int, a allowlist.Allowlist) error {
	return assertNoSnapshotSecrets(relativePath, absolutePath, size, a)
}

func assertNoSnapshotSecrets(relativePath, absolutePath string, size int, a allowlist.Allowlist) error {
	if size == 0 {
		return nil
	}
	readBytes := min(size, SecretScanBytes)
	data, err := nodefs.ReadBytes(absolutePath)
	if err != nil {
		return &Error{Message: jsexc.Concat("Could not read ", relativePath, " for snapshot secret scanning: ", jsexc.Message(err))}
	}
	buffer := data[:min(len(data), readBytes)]
	if looksBinary(buffer) {
		return nil
	}
	text := jsstr.DecodeUTF8(buffer)
	_, report := redaction.RedactText(text, relativePath)
	fileDigest, err := allowlist.FileContentDigest(absolutePath, SecretScanBytes)
	if err != nil {
		return err
	}
	remaining := allowlist.Kinds(allowlist.FilterHighOccurrences(report.Occurrences, fileDigest, relativePath, a))
	if len(remaining) > 0 {
		return &Error{Message: jsexc.Concat(
			"Refusing turn-tree snapshot: high-confidence secret material detected in ", relativePath,
			" (rules/kinds: ", jsexc.Join(remaining, ", "), "). ",
			"Exclude the path via .faultlineignore after human review, remove the secret, or add a path+kind+occurrenceDigest ",
			"entry in .faultline-secret-allowlist.json (never a bare path allow).")}
	}
	allowlistedOccurrenceDigests := map[string]bool{}
	for _, o := range report.Occurrences {
		if o.Confidence == "HIGH" && allowlist.IsHighOccurrenceAllowlisted(a, relativePath, o.Kind, o.Digest, fileDigest) {
			allowlistedOccurrenceDigests[o.Digest] = true
		}
	}
	units := jsstr.ToUTF16(text)
	for _, m := range entropyTokenPattern.FindAll(text) {
		token := jsstr.FromUTF16(units[m.Start:m.End])
		if !IsSuspiciousHighEntropyToken(token) {
			continue
		}
		// A reviewed HIGH allowlist entry already covers this exact occurrence.
		if allowlistedOccurrenceDigests["sha256:"+canonical.SHA256Hex(token)] {
			continue
		}
		if allowlist.IsEntropyTokenAllowlisted(token, fileDigest, relativePath, a) {
			continue
		}
		return &Error{Message: jsexc.Concat(
			"Refusing turn-tree snapshot: high-entropy secret-like token detected in ", relativePath,
			" (rule/kind: HIGH_ENTROPY_TOKEN). ",
			"Exclude via .faultlineignore after human review, remove the token, or add a digest-bound allowlist entry ",
			"(path + kind HIGH_ENTROPY_TOKEN + occurrenceDigest).")}
	}
	return nil
}

// jsLog2 is V8's Math.log2 (src/base/ieee754.cc log2, from FreeBSD's
// e_log2.c), which Go's math.Log2 does not round alike. Each product is
// wrapped in float64() so Go does not fuse it into an FMA.
func jsLog2(x float64) float64 {
	const (
		two54   = 1.80143985094819840000e+16
		ivln2hi = 1.44269504072144627571e+00
		ivln2lo = 1.67517131648865118353e-10
	)
	bits := math.Float64bits(x)
	hx, lx := int32(bits>>32), uint32(bits)
	k := int32(0)
	if hx < 0x00100000 {
		if (hx&0x7FFFFFFF)|int32(lx) == 0 {
			return math.Inf(-1)
		}
		if hx < 0 {
			return math.NaN()
		}
		k -= 54
		x *= two54
		hx = int32(math.Float64bits(x) >> 32)
	}
	if hx >= 0x7FF00000 {
		return x + x
	}
	if hx == 0x3FF00000 && lx == 0 {
		return 0
	}
	k += (hx >> 20) - 1023
	hx &= 0x000FFFFF
	i := (hx + 0x95F64) & 0x100000
	x = math.Float64frombits(uint64(uint32(hx|(i^0x3FF00000)))<<32 | math.Float64bits(x)&0xFFFFFFFF) // normalize x or x/2
	k += i >> 20
	y := float64(k)
	f := x - 1.0
	hfsq := float64(float64(0.5*f) * f)
	r := kLog1p(f)
	hi := f - hfsq
	hi = math.Float64frombits(math.Float64bits(hi) &^ 0xFFFFFFFF)
	lo := float64(float64(f-hi)-hfsq) + r
	valHi := float64(hi * ivln2hi)
	valLo := float64(float64(float64(lo+hi)*ivln2lo) + float64(lo*ivln2hi))
	w := y + valHi
	valLo += float64(float64(y-w) + valHi)
	valHi = w
	return valLo + valHi
}

// kLog1p is k_log1p from src/base/ieee754.cc.
func kLog1p(f float64) float64 {
	const (
		lg1 = 6.666666666666735130e-01
		lg2 = 3.999999999940941908e-01
		lg3 = 2.857142874366239149e-01
		lg4 = 2.222219843214978396e-01
		lg5 = 1.818357216161805012e-01
		lg6 = 1.531383769920937332e-01
		lg7 = 1.479819860511658591e-01
	)
	s := f / (2.0 + f)
	z := float64(s * s)
	w := float64(z * z)
	t1 := float64(w * float64(lg2+float64(w*float64(lg4+float64(w*lg6)))))
	t2 := float64(z * float64(lg1+float64(w*float64(lg3+float64(w*float64(lg5+float64(w*lg7)))))))
	r := t2 + t1
	hfsq := float64(float64(0.5*f) * f)
	return float64(s * float64(hfsq+r))
}
