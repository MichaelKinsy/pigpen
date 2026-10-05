package mpv

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// maxMeta bounds the metadata file: past it, entries that are not in the queue are dropped.
const maxMeta = 5000

// loadMeta reads the video ID -> track file. A missing or damaged file is an
// empty map: titles are a convenience, the queue itself lives in mpv.
func loadMeta(path string) map[string]music.Track {
	meta := map[string]music.Track{}
	data, err := os.ReadFile(path)
	if err != nil {
		return meta
	}
	if json.Unmarshal(data, &meta) != nil {
		return map[string]music.Track{}
	}
	return meta
}

// saveMeta writes the map atomically with mode 0600.
func saveMeta(path string, meta map[string]music.Track) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tracks-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// videoID extracts the video ID of a watch URL, or "" when there is none.
func videoID(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Query().Get("v")
}
