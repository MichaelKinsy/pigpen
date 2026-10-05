package music

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SaveSearch keeps the latest search's tracks, so that a result number ("play 3") means something to the next command, whether
// it came from the command line or from the player. The file is private (0600) and written atomically.
func SaveSearch(path string, tracks []Track) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(tracks)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadSearch reads what SaveSearch wrote. A missing file is an error that satisfies errors.Is(err, fs.ErrNotExist).
func LoadSearch(path string) ([]Track, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tracks []Track
	if err := json.Unmarshal(data, &tracks); err != nil {
		return nil, fmt.Errorf("%s is damaged: %w", path, err)
	}
	return tracks, nil
}
