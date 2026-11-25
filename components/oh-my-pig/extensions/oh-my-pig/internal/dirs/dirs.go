// Package dirs holds the path spelling omp shows for directories and files.
//
// upstream: packages/utils/src/dirs.ts (standardizeMacOSPath)
package dirs

import (
	"path/filepath"
	"runtime"
	"strings"
)

const privatePrefix = "/private"

// StandardizeMacOSPath is the spelling omp prints for p on macOS: without the
// leading /private when both spellings name the same place (/private/tmp/x and
// /tmp/x), and p itself otherwise and on every other system.
//
// omp standardizes its project directory once and derives other paths from
// it, so a path below that directory that does not exist yet still carries
// the short spelling. PiG hands the extension the long spelling, so this
// function decides by the deepest ancestor of p that exists.
func StandardizeMacOSPath(p string) string {
	if runtime.GOOS != "darwin" {
		return p
	}
	return standardize(p, filepath.EvalSymlinks)
}

func standardize(p string, realpath func(string) (string, error)) string {
	if !strings.HasPrefix(p, privatePrefix+"/") {
		return p
	}
	stripped := p[len(privatePrefix):]
	long, short := p, stripped
	for {
		realLong, err := realpath(long)
		if err == nil {
			realShort, err := realpath(short)
			if err == nil && realLong == realShort {
				return stripped
			}
			return p
		}
		parent := filepath.Dir(long)
		if parent == long || parent == privatePrefix {
			return p
		}
		long, short = parent, filepath.Dir(short)
	}
}
