//go:build windows

package changeset

import (
	"errors"
	"io/fs"
	"os"
)

func openNoFollow(path string) (*os.File, error) { return os.Open(path) }

func isGoneOrLink(err error) bool { return errors.Is(err, fs.ErrNotExist) }
