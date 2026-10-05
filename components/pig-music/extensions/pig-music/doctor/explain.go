package doctor

import "strings"

// LooksLikeExtractionFailure recognises what yt-dlp says when YouTube refuses it: a 403 on the stream, a format that
// is not offered (a missing JS runtime hides formats), a sign-in or bot challenge, or a failed challenge solve.
func LooksLikeExtractionFailure(text string) bool {
	t := strings.ToLower(text)
	for _, s := range []string{
		"http error 403", "requested format is not available", "sign in to confirm", "n challenge", "nsig extraction failed",
		"no video formats found", "only images are available", "po token",
	} {
		if strings.Contains(t, s) {
			return true
		}
	}
	return false
}

// Explain is the message for any 403 or missing-format error: the yt-dlp is old or has no JS runtime, and one fix.
func Explain(version string, selfManaged bool, goos string) string {
	if version == "" {
		version = "(unknown version)"
	}
	return "yt-dlp " + version + " is old or has no JS runtime: " + fixYtdlpUpdate(goos, selfManaged) + "; for the runtime, " + fixJS(goos)
}
