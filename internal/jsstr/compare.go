package jsstr

// CompareUTF16 orders strings by UTF-16 code units, as JS's default
// Array.prototype.sort and the < operator do. Returns -1, 0 or 1.
func CompareUTF16(a, b string) int {
	ua, ub := ToUTF16(a), ToUTF16(b)
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			if ua[i] < ub[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(ua) < len(ub):
		return -1
	case len(ua) > len(ub):
		return 1
	}
	return 0
}
