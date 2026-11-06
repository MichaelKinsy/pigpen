package langsmith

// Port of utils/sampling.js isSampledById (langsmith 0.10.5). OWNER: wire lane.

// isSampledByID: rate nil or >= 1 samples everything, <= 0 nothing; otherwise XXH3-128 of the lower-cased
// UTF-8 identifier modulo 1,000,000 divided by 1,000,000 must be below the rate.
func isSampledByID(identifier string, samplingRate *float64) bool {
	panic("unported: sampling.go isSampledByID")
}
