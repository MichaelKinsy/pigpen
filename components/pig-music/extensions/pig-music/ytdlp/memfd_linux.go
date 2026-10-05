package ytdlp

import (
	"os"

	"golang.org/x/sys/unix"
)

// memFile is a file in memory with no name (memfd_create), closed on exec: yt-dlp gets it only as the descriptor it is handed.
func memFile() (*os.File, error) {
	fd, err := unix.MemfdCreate("pig-music-cookies", unix.MFD_CLOEXEC)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "pig-music-cookies"), nil
}
