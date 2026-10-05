package wax

import (
	"errors"

	"github.com/MichaelKinsy/pigpen/pig-music/native"
	"github.com/colespringer/waxtap/v3"
)

func init() { native.RegisterClassifier(classifyWax) }

// classifyWax recognises WaxTap's sentinel errors.
func classifyWax(err error) (native.Kind, string, bool) {
	switch {
	case errors.Is(err, waxtap.ErrNeedsPOToken):
		return native.KindNeedsToken, "YouTube wants a proof-of-origin token for this track, and the native engine does not support those. Use the mpv engine (engine = mpv) with a current yt-dlp.", true
	case errors.Is(err, waxtap.ErrCipherSolve), errors.Is(err, waxtap.ErrExtractionFailed):
		return native.KindBroken, "YouTube changed and the native engine's extractor needs an update (pig-music, WaxTap). Try again later, or use the mpv engine (engine = mpv).", true
	case errors.Is(err, waxtap.ErrURLExpired), errors.Is(err, waxtap.ErrIncompleteStream):
		return native.KindExpired, "the stream link expired and could not be renewed.", true
	case errors.Is(err, waxtap.ErrAgeRestricted), errors.Is(err, waxtap.ErrLoginRequired):
		return native.KindLogin, "this track is age-restricted or needs a sign-in, and the native engine does not sign in.", true
	case errors.Is(err, waxtap.ErrVideoRestricted):
		return native.KindUnavailable, "this track is private.", true
	case errors.Is(err, waxtap.ErrMembersOnly):
		return native.KindUnavailable, "this track is for channel members only.", true
	case errors.Is(err, waxtap.ErrGeoBlocked):
		return native.KindUnavailable, "this track is not available in your region.", true
	case errors.Is(err, waxtap.ErrNoAudioFormats):
		return native.KindUnavailable, "this track has no audio stream.", true
	case errors.Is(err, waxtap.ErrVideoUnavailable):
		return native.KindUnavailable, "this track is unavailable (removed, private or never existed).", true
	case errors.Is(err, waxtap.ErrLiveContent), errors.Is(err, waxtap.ErrLiveNotStarted):
		return native.KindLive, "this is a live or upcoming stream, which the native engine cannot play.", true
	case errors.Is(err, waxtap.ErrRateLimited):
		return native.KindRateLimited, "YouTube says too many requests came from this address. Wait a while and try again.", true
	}
	return 0, "", false
}
