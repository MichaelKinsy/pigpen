// SPDX-License-Identifier: MIT

package rpiv_web_tools

import "testing"

// Named gaps of the port. Every other upstream title is claimed by an exact twin; this one has no Go counterpart.
//
// The publish-manifest case checks that the npm `files` array covers every production TypeScript module. The port
// ships one Go module whole, so there is no files array and no TypeScript tree to keep it in sync with.
func TestShipManifestNamedSkip(t *testing.T) {
	tskip(t, "ship-manifest", "`package.json` `files` array covers every production .ts module across the tree",
		"the port ships one Go module whole: there is no files array and no tree of TypeScript modules to keep in sync with it")
}
