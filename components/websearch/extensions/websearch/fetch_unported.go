package websearch

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Classification of the URLs that pi-web-access's extract.ts hands to a specialised extractor
// (YouTube, local video). This port has none of those extractors yet, so a URL that the
// original would route to one is refused instead of being fetched as a plain page. GitHub URLs are
// extracted (github_extract.go, github_issue_pr.go).

var youtubeRE = regexp.MustCompile(`(?:(?:www\.|m\.)?youtube\.com/(?:watch\?.*v=|shorts/|live/|embed/|v/)|youtu\.be/)([a-zA-Z0-9_-]{11})`)

// isYouTubeURL is youtube-extract.ts isYouTubeURL.
func isYouTubeURL(raw string) bool {
	if u, err := ParseURL(raw, nil); err == nil && u.Path == "/playlist" {
		return false
	}
	return youtubeRE.MatchString(raw)
}

var videoExtensions = map[string]bool{".mp4": true, ".mov": true, ".webm": true, ".avi": true, ".mpeg": true, ".mpg": true, ".wmv": true, ".flv": true, ".3gp": true, ".3gpp": true}

// sectionEnabled reads `<section>.enabled` from web-search.json: true unless it is a boolean false.
func sectionEnabled(section string) (bool, error) {
	root, err := readLenientConfig()
	if err != nil {
		return false, err
	}
	obj, _ := root[section].(map[string]any)
	if obj == nil {
		return true, nil
	}
	enabled, ok := obj["enabled"].(bool)
	return !ok || enabled, nil
}

// isLocalVideoFile is video-extract.ts isVideoFile: a path (or file: URL) with a video extension
// that names an existing regular file, while video.enabled is not false.
func isLocalVideoFile(input string) (bool, error) {
	enabled, err := sectionEnabled("video")
	if err != nil || !enabled {
		return false, err
	}
	if !(strings.HasPrefix(input, "/") || strings.HasPrefix(input, "./") || strings.HasPrefix(input, "../") || strings.HasPrefix(input, "file://")) {
		return false, nil
	}
	path := input
	if strings.HasPrefix(input, "file://") {
		u, err := url.Parse(input)
		if err != nil {
			return false, nil
		}
		path = u.Path
	}
	if !videoExtensions[strings.ToLower(filepath.Ext(path))] {
		return false, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, nil
	}
	st, err := os.Stat(abs)
	return err == nil && st.Mode().IsRegular(), nil
}

// unportedFetchFeature names the feature of a specialised extractor that the original would use
// for rawURL and this port lacks ("" when the URL is a plain page). The error is a configuration
// parse failure, reported like the original does.
func unportedFetchFeature(rawURL string, o ExtractOptions) (string, error) {
	local, err := isLocalVideoFile(rawURL)
	if err != nil {
		return "", err
	}
	yt := isYouTubeURL(rawURL)
	if o.Frames > 0 || o.Timestamp != "" {
		switch {
		case yt || local:
			return "video frame and timestamp extraction", nil
		case o.Timestamp != "":
			return "", &plainError{"Timestamp extraction only works with YouTube and local video files"}
		default:
			return "", &plainError{"Frame extraction only works with YouTube and local video files"}
		}
	}
	if local {
		return "local video file analysis", nil
	}
	if yt {
		if on, err := sectionEnabled("youtube"); err != nil {
			return "", err
		} else if on {
			return "YouTube video extraction", nil
		}
	}
	return "", nil
}

// plainError is an extraction failure that is reported verbatim as the URL's error.
type plainError struct{ msg string }

func (e *plainError) Error() string { return e.msg }
