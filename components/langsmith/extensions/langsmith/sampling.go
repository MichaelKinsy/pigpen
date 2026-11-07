package langsmith

// Port of utils/sampling.js isSampledById (langsmith 0.10.5).

import "math/bits"

const samplingHashModulus = 1000000

// samplingBucket is `XXH3_128(encoder.encode(identifier.toLowerCase())) % 1000000n` (sampling.js:25).
func samplingBucket(identifier string) uint64 {
	var stack [128]byte
	data := jsUTF8Lower(stack[:0], identifier)
	high, low := xxh3_128(data)
	// (high<<64 | low) mod m without a 128-bit integer: reduce high first so Div64's quotient fits.
	_, rem := bits.Div64(high%samplingHashModulus, low, samplingHashModulus)
	return rem
}

// isSampledByID: rate nil or >= 1 samples everything, <= 0 nothing; otherwise XXH3-128 of the lower-cased
// UTF-8 identifier modulo 1,000,000 divided by 1,000,000 must be below the rate (sampling.js:16-29).
// The `identifier == null` branch (sampling.js:22) cannot occur for a Go string. A NaN rate fails both
// comparisons, as in JavaScript, and is compared last (always false).
func isSampledByID(identifier string, samplingRate *float64) bool {
	if samplingRate == nil || *samplingRate >= 1 {
		return true
	}
	if *samplingRate <= 0 {
		return false
	}
	return float64(samplingBucket(identifier))/float64(samplingHashModulus) < *samplingRate
}
