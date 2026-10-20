//go:build !unix

package pi_subagents

import "os"

// hardLinkCount is stat.nlink; Windows reports no link count through os.FileInfo, so a file counts as unlinked.
func hardLinkCount(st os.FileInfo) uint64 { return 1 }
