//go:build !unix

package ytdlp

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/pigpen/pig-music/account"
)

// ExportCookies is not available here: it needs a file descriptor that yt-dlp can open by path (/dev/fd/N), so the library
// keeps using yt-dlp's own listing.
func ExportCookies(context.Context, string, string) (account.Jar, error) {
	return nil, fmt.Errorf("%w: exporting cookies in memory is not available on this system", account.ErrAuth)
}
