package herdr

// CallTimeout lets a test change the bound on one herdr call; OriginalCallTimeout is its production value, read before any test changes it.
var (
	CallTimeout         = &callTimeout
	OriginalCallTimeout = callTimeout
)
