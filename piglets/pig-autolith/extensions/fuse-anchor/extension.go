// Package fuseanchor is a deliberately empty Go factory. It adds no tools, commands, hooks,
// prompts or agent behavior. PiG refuses a Binary build with no fused member, and the Lisp
// extension autolith-bridge cannot be fused (PiG builds it into an SBCL image at first load),
// so this factory is what lets `pig piglet build` produce the Piglet Binary.
package fuseanchor

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

func Extension() *sdk.Extension {
	return sdk.New("fuse-anchor")
}
