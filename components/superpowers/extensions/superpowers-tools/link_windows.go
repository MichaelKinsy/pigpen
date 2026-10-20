package superpowers_tools

import "os"

// singleLink: Node reports nlink 1 for every file on Windows.
func singleLink(os.FileInfo) bool { return true }
