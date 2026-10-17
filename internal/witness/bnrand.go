package witness

import (
	"math/big"

	"github.com/Mizore66/faultline/internal/clock"
)

// OpenSSL's random big numbers (crypto/bn/bn_rand.c) as the key-parsing
// port draws them: blinding values and random searches. The bytes come
// from internal/clock (the system CSPRNG in release builds, a seeded
// stream under the test seed), as OpenSSL's come from RAND_priv_bytes, so
// where OpenSSL's outcome depends on them (arithmetic modulo a reducible
// polynomial or a composite "prime") Go's is as random as Node's.

// bnRandTop is bnrand's top argument.
const (
	bnRandTopAny = -1 // BN_RAND_TOP_ANY
	bnRandTopOne = 0  // BN_RAND_TOP_ONE: the top bit set
)

// bnPrivRand ports BN_priv_rand_ex(rnd, bits, top, BN_RAND_BOTTOM_ANY).
func bnPrivRand(bits, top int) *big.Int {
	if bits <= 0 {
		return new(big.Int)
	}
	n := (bits + 7) / 8
	bit := (bits - 1) % 8
	buf := make([]byte, n)
	clock.RandRead(buf)
	if top == bnRandTopOne {
		buf[0] |= 1 << bit
	}
	buf[0] &^= byte(0xff << (bit + 1))
	return new(big.Int).SetBytes(buf)
}

// bnPrivRandRange ports BN_priv_rand_range_ex: uniform in [0, rng), with
// bnrand_range's one-bit-longer draw when rng is 100..._2. Its 100-try
// limit is not reached for any range with a top bit (each try succeeds
// with probability >= 1/2).
func bnPrivRandRange(rng *big.Int) *big.Int {
	n := rng.BitLen()
	if n == 1 {
		return new(big.Int)
	}
	if rng.Bit(n-2) == 0 && rng.Bit(n-3) == 0 {
		for {
			r := bnPrivRand(n+1, bnRandTopAny)
			if r.Cmp(rng) >= 0 {
				r.Sub(r, rng)
				if r.Cmp(rng) >= 0 {
					r.Sub(r, rng)
				}
			}
			if r.Cmp(rng) < 0 {
				return r
			}
		}
	}
	for {
		if r := bnPrivRand(n, bnRandTopAny); r.Cmp(rng) < 0 {
			return r
		}
	}
}

// bnPrivRandNonzero is the do { BN_priv_rand_ex(bits) } while zero loop of
// BN_GF2m_mod_inv and ec_GF2m_simple_ladder_pre.
func bnPrivRandNonzero(bits int) *big.Int {
	for {
		if r := bnPrivRand(bits, bnRandTopAny); r.Sign() != 0 {
			return r
		}
	}
}

// bnPrivRandRangeNonzero is ossl_ec_GFp_simple_ladder_pre's
// do { BN_priv_rand_range_ex(field) } while zero.
func bnPrivRandRangeNonzero(rng *big.Int) *big.Int {
	for {
		if r := bnPrivRandRange(rng); r.Sign() != 0 {
			return r
		}
	}
}
