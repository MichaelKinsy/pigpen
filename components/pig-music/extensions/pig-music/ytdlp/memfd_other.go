//go:build unix && !linux

package ytdlp

import (
	"errors"
	"os"
)

// memFile is Linux's: elsewhere the cookie file is an unlinked file (anonymousFile).
func memFile() (*os.File, error) { return nil, errors.New("no memfd here") }
