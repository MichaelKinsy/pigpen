package music

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Settings is the extension's own settings file, `settings.json` in its
// directory under PiG's agent directory. A missing file means the defaults.
type Settings struct {
	// StopOnExit stops mpv when PiG quits (default true). Used from milestone 6.
	StopOnExit *bool `json:"stopOnExit,omitempty"`
	// FooterStatus shows the current track in PiG's footer while the player is hidden (default true).
	FooterStatus *bool `json:"footerStatus,omitempty"`
	// CoverArt draws the track's cover on the Player screen (default true); false leaves only the spinning disc.
	CoverArt *bool `json:"coverArt,omitempty"`
	// Palette paints the player with colours from the cover or the track (default true; ignored without colour, NO_COLOR).
	Palette *bool `json:"palette,omitempty"`
	// Pulse moves the player with the music's real loudness (default true; needs the palette and colour; calm turns it off).
	Pulse *bool `json:"pulse,omitempty"`
	// Calm turns all motion off: no pulse, no spinning disc, no cross-fade (default false).
	Calm *bool `json:"calm,omitempty"`
	// LowPower slows the pulse to about 6 frames a second, the disc to half speed, and ends the cross-fade (default false).
	LowPower *bool `json:"lowPower,omitempty"`
	// NowPlaying is where the now-playing line shows while the player is hidden: "footer" (the status line, the default),
	// "widget" (one line above the editor, for hosts whose footer was replaced by another extension, which hides statuses),
	// or "auto" (today the same as "footer": an extension cannot tell that another one replaced the footer).
	NowPlaying string `json:"nowPlaying,omitempty"`
	// CookieBrowser names the browser yt-dlp may read cookies from. Empty means no cookies.
	CookieBrowser string `json:"cookieBrowser,omitempty"`
	// MPVPath and YtdlpPath override the binaries found on PATH.
	MPVPath   string `json:"mpvPath,omitempty"`
	YtdlpPath string `json:"ytdlpPath,omitempty"`
	// Engine picks the playback engine: "auto" (the default: mpv with yt-dlp
	// when they are healthy, else the native engine), "mpv" or "native".
	Engine string `json:"engine,omitempty"`
	// NativePath is the pigmusic program that runs the native engine's player (`pigmusic serve`).
	NativePath string `json:"nativePath,omitempty"`
	// JSRuntime names the JavaScript runtime yt-dlp uses, in its --js-runtimes syntax ("deno", "node", "node:/path").
	// Empty lets pig-music detect one.
	JSRuntime string `json:"jsRuntime,omitempty"`
}

// StopOnExitOrDefault is StopOnExit with its default of true.
func (s Settings) StopOnExitOrDefault() bool { return s.StopOnExit == nil || *s.StopOnExit }

// FooterStatusOrDefault is FooterStatus with its default of true.
func (s Settings) FooterStatusOrDefault() bool { return s.FooterStatus == nil || *s.FooterStatus }

// PaletteOrDefault is Palette with its default of true.
func (s Settings) PaletteOrDefault() bool { return s.Palette == nil || *s.Palette }

// PulseOrDefault is Pulse with its default of true.
func (s Settings) PulseOrDefault() bool { return s.Pulse == nil || *s.Pulse }

// CalmOrDefault is Calm with its default of false.
func (s Settings) CalmOrDefault() bool { return s.Calm != nil && *s.Calm }

// LowPowerOrDefault is LowPower with its default of false.
func (s Settings) LowPowerOrDefault() bool { return s.LowPower != nil && *s.LowPower }

// CoverArtOrDefault is CoverArt with its default of true.
func (s Settings) CoverArtOrDefault() bool { return s.CoverArt == nil || *s.CoverArt }

// NowPlayingMode is "footer" or "widget": the default and "auto" are the footer.
func (s Settings) NowPlayingMode() string {
	if s.NowPlaying == "widget" {
		return "widget"
	}
	return "footer"
}

// LoadSettings reads the file at path. A missing file gives the defaults; a
// malformed one is an error naming the file, never silently ignored.
func LoadSettings(path string) (Settings, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return Settings{}, fmt.Errorf("%s: %w", path, err)
	}
	switch s.NowPlaying {
	case "", "auto", "footer", "widget":
	default:
		return Settings{}, fmt.Errorf("%s: nowPlaying %q is not one of auto, footer or widget", path, s.NowPlaying)
	}
	return s, nil
}

// UpdateSettingsFile sets one boolean key in the settings file and writes it back atomically with mode 0600, keeping every
// other key as it was (including ones this version does not know). A missing file is created; a file that is not a JSON
// object is an error and is left untouched. It is the settings screen's explicit action: nothing else writes the file.
func UpdateSettingsFile(path, key string, value bool) error {
	return UpdateSettingsValue(path, key, value)
}

// UpdateSettingsValue is UpdateSettingsFile for a boolean or a string. An empty string removes the key, so the default returns.
func UpdateSettingsValue(path, key string, value any) error {
	if key == "" {
		return errors.New("settings: no key given")
	}
	doc := map[string]json.RawMessage{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("%s: %w; fix or remove it, nothing was changed", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if str, ok := value.(string); ok && str == "" {
		delete(doc, key)
	} else {
		raw, _ := json.Marshal(value)
		doc[key] = raw
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(out, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
