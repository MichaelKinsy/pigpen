package ytdlp

import (
	"context"
	"os"
)

// yt-dlp copies the browser's cookie database into a directory of its own under TMPDIR while it reads it (Firefox's holds the
// values in clear) and removes it when it is done; a run killed at the timeout leaves it behind. Every run that reads the browser
// (a library listing, the cookie export) therefore gets a private TMPDIR, removed here whatever happened to the run.

type envKey struct{}

// withEnv makes ExecRunner add these variables to the environment of the program it runs.
func withEnv(ctx context.Context, env ...string) context.Context {
	return context.WithValue(ctx, envKey{}, env)
}

func envOf(ctx context.Context) []string {
	env, _ := ctx.Value(envKey{}).([]string)
	return env
}

// runtimeDir is $XDG_RUNTIME_DIR when it names a directory (a tmpfs of the user's own on most Linux systems), else "" (the
// temporary directory).
func runtimeDir() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return ""
	}
	return dir
}

// privateTemp makes a 0700 directory for one run and returns the context that points TMPDIR (TEMP and TMP on Windows) at it,
// and the function that removes it.
func privateTemp(ctx context.Context) (context.Context, func(), error) {
	dir, err := os.MkdirTemp(runtimeDir(), ".pm-ytdlp-")
	if err != nil {
		return ctx, func() {}, err
	}
	return withEnv(ctx, "TMPDIR="+dir, "TEMP="+dir, "TMP="+dir), func() { _ = os.RemoveAll(dir) }, nil
}
