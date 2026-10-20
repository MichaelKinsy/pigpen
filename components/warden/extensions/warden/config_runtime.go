package warden

import (
	"os"
	"strconv"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Config sources at run time: the user file, then the project file when PiG trusts the project (upstream's
// loadConfig({ cwd, projectTrusted })). Upstream re-reads both files on every hook call, so an edit takes effect at
// once; the port keeps that by stat-ing both files on each config() call (no host call) and reloading when either
// changed. The trust decision is one host call, made once per working directory.

// readConfigFiles reads the user file at userPath and, for a trusted project, the project file. file is the user file
// over the defaults (what a command saves back); merged adds the project file. problem is the message to show for a
// file that could not be read or parsed; the configuration that applies is still returned.
func (w *warden) readConfigFiles(ctx sdk.Context, userPath string) (file, merged Config, problem string) {
	file, err := LoadConfig(userPath)
	if err != nil {
		return file, file, "warden: could not read " + userPath + ": " + err.Error() + ". Defaults are in use."
	}
	merged = file
	w.projectPath = ""
	if cwd := ctx.Cwd(); cwd != "" {
		w.projectPath = DefaultHostDirs(w.opts.Getenv).ProjectConfigPath(cwd)
		if w.trustCwd != cwd {
			trusted, terr := ctx.IsProjectTrusted()
			w.projectTrusted, w.trustCwd = terr == nil && trusted, cwd
		}
	}
	if w.projectTrusted && w.projectPath != "" {
		project, perr := readJSONObject(w.projectPath)
		if perr != nil {
			problem = "warden: could not read " + w.projectPath + ": " + perr.Error() + ". The user configuration is in use."
		} else {
			merged = ApplyProjectOverrides(file, anyOf(project))
		}
	}
	var missing []string
	merged, missing = CompleteConfig(merged)
	w.shapeMissing = missing
	return file, merged, problem
}

// configSig names the state of the files config() was built from.
func (w *warden) configSig() string {
	sig := fileSig(w.cfgPath)
	if w.projectTrusted && w.projectPath != "" {
		sig += "|" + fileSig(w.projectPath)
	}
	return sig
}

func fileSig(path string) string {
	if path == "" {
		return "-"
	}
	info, err := os.Stat(path)
	if err != nil {
		return "absent"
	}
	return strconv.FormatInt(info.ModTime().UnixNano(), 10) + ":" + strconv.FormatInt(info.Size(), 10)
}

// reloadIfChanged re-reads the files when either changed on disk since the last load. w.mu must be held.
func (w *warden) reloadIfChanged(ctx sdk.Context) {
	if w.cfgPath != "" && w.configSig() != w.cfgSig {
		w.load(ctx)
	}
}

// flushConfigNotices says once per session what the configuration could not apply: a refused backend, the config
// warnings, and sections a config lacked. It must run without w.mu held.
func (w *warden) flushConfigNotices(ctx sdk.Context, cfg Config) {
	w.mu.Lock()
	missing := w.shapeMissing
	w.mu.Unlock()
	if len(missing) > 0 {
		w.noteOnce(ctx, "config-shape", ShapeWarning(missing, nil))
	}
	if cfg.BackendRefusal != "" {
		w.noteOnce(ctx, "config-backend", "warden: judgments are off: "+cfg.BackendRefusal)
	}
	if len(cfg.Warnings) > 0 {
		w.noteOnce(ctx, "config-warnings", "warden: config warnings: "+joinStrings(cfg.Warnings, "; "))
	}
}

// configLocations is the `Config:` line of /warden status.
func (w *warden) configLocations() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	text := "Config: " + w.cfgPath
	if w.projectTrusted && w.projectPath != "" {
		text += " and " + w.projectPath
	}
	return text + "."
}
