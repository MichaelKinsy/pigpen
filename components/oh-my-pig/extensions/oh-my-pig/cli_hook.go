package ohmypig

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/clihook"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ompsession"
)

// The Binary resumes omp's sessions as its own. PiG chooses the session to resume (--resume's picker,
// --continue, --session, --fork) before any extension runs, from the files of its session directory, so the
// translations have to be there first: PiG's CLI hook runs before PiG parses its command line, and a command
// line that resumes brings the translations of the project's omp sessions up to date. A command line that
// starts a new session reads nothing.
func init() {
	clihook.Register(func(h clihook.Env) clihook.Result {
		cwd, err := os.Getwd()
		home, homeErr := os.UserHomeDir()
		if err != nil || homeErr != nil {
			return clihook.Result{Args: h.Args}
		}
		return clihook.Result{Args: importOmpSessions(h.Args, home, cwd, h.Getenv, h.Stderr)}
	})
}

// importOmpSessions is the command line PiG parses: args, with an omp session file named by --session or
// --fork replaced by its translation. Problems are reported on stderr and never stop the run.
func importOmpSessions(args []string, home, cwd string, getenv func(string) string, stderr io.Writer) []string {
	var (
		resumes    bool
		sessionDir string
		named      []int // indexes of the --session and --fork values
	)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--no-session":
			return args
		case "--continue", "-c", "--resume", "-r":
			resumes = true
		case "--session", "--fork":
			if i+1 < len(args) {
				i++
				named = append(named, i)
				resumes = true
			}
		case "--session-dir":
			if i+1 < len(args) {
				i++
				sessionDir = args[i]
			}
		}
	}
	if !resumes {
		return args
	}
	dir, err := ompsession.PigSessionDir(sessionDir, home, cwd, getenv)
	if err != nil {
		return args
	}
	mirror := ompsession.OpenMirror(dir)
	out := slices.Clone(args)
	for _, i := range named {
		src := args[i]
		if src == "~" || strings.HasPrefix(src, "~/") {
			src = filepath.Join(home, src[1:])
		}
		if !filepath.IsAbs(src) {
			src = filepath.Join(cwd, src)
		}
		if !ompsession.IsOmpSession(src) {
			continue
		}
		dst, _, err := mirror.Sync(src, ompsession.Options{})
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "oh-my-pig: omp session %s was not imported: %v\n", src, err)
			continue
		}
		out[i] = dst
	}
	translated, problems := mirror.SyncAll(home, cwd)
	if err := mirror.Save(); err != nil {
		problems = append(problems, fmt.Sprintf("the index of imported omp sessions was not written: %v", err))
	}
	if translated > 0 {
		_, _ = fmt.Fprintf(stderr, "oh-my-pig: imported %d omp session(s) of this project\n", translated)
	}
	for _, problem := range problems {
		_, _ = fmt.Fprintf(stderr, "oh-my-pig: %s\n", problem)
	}
	return out
}
