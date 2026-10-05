package music

import (
	"fmt"
	"os"
	"path/filepath"
)

// Paths locates the files the player shares between runs.
type Paths struct {
	// Dir holds the socket and the metadata files. Mode 0700.
	Dir string
	// Socket is mpv's JSON IPC socket.
	Socket string
	// Meta maps video IDs to track metadata, so a queue read back from mpv has titles.
	Meta string
	// Search holds the last search result list of the command line.
	Search string
	// Data is the extension's persistent data directory: the yt-dlp and Deno it downloaded (in bin/) and the update
	// stamp. Unlike Dir it is not a runtime directory, so it survives a reboot.
	Data string
	// Settings is the extension's own settings file. It is read, never written, by the player core.
	Settings string
}

// maxSocketPath is the longest unix socket path portable across Linux and macOS.
const maxSocketPath = 100

// DefaultPaths chooses the directory: $XDG_RUNTIME_DIR/pig-music when that
// variable names a directory, otherwise pig-music under PiG's agent directory
// ($PIG_CODING_AGENT_DIR, or $PIG_HOME/agent, or ~/.pig/agent). getenv is
// os.Getenv in production.
func DefaultPaths(getenv func(string) string) (Paths, error) {
	agent := AgentDir(getenv)
	dir := filepath.Join(agent, "pig-music")
	if run := getenv("XDG_RUNTIME_DIR"); run != "" {
		if info, err := os.Stat(run); err == nil && info.IsDir() {
			dir = filepath.Join(run, "pig-music")
		}
	}
	p := PathsIn(dir)
	p.Settings = filepath.Join(agent, "pig-music", "settings.json")
	p.Data = filepath.Join(agent, "pig-music")
	if len(p.Socket) > maxSocketPath {
		return p, fmt.Errorf("socket path %q is %d bytes, over the unix limit of %d: set XDG_RUNTIME_DIR to a shorter directory", p.Socket, len(p.Socket), maxSocketPath)
	}
	return p, nil
}

// PathsIn puts every file in dir.
func PathsIn(dir string) Paths {
	return Paths{
		Dir:      dir,
		Socket:   filepath.Join(dir, "mpv.sock"),
		Meta:     filepath.Join(dir, "tracks.json"),
		Search:   filepath.Join(dir, "search.json"),
		Data:     dir,
		Settings: filepath.Join(dir, "settings.json"),
	}
}

// AgentDir is PiG's agent directory for the environment getenv describes.
func AgentDir(getenv func(string) string) string {
	if d := getenv("PIG_CODING_AGENT_DIR"); d != "" {
		return d
	}
	if h := getenv("PIG_HOME"); h != "" {
		return filepath.Join(h, "agent")
	}
	home := getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return filepath.Join(home, ".pig", "agent")
}

// EnsureDir creates Dir with mode 0700 and tightens an existing one.
func (p Paths) EnsureDir() error {
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(p.Dir, 0o700)
}
