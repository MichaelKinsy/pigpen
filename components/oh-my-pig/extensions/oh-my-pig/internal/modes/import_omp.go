package modes

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ompsession"
)

// handleImportOmpCommand implements `/import-omp [latest|<session.jsonl>] [--full]`: it switches to the
// translation of an omp session, the one a command line that resumes finds (the extension's CLI hook). Without
// a path it takes the omp session of this project that was written last.
func (m *manager) handleImportOmpCommand(ctx sdk.Context, args string) error {
	var opts ompsession.Options
	var target string
	for _, field := range strings.Fields(args) {
		switch field {
		case "--full":
			opts.Full = true
		case "--help", "-h":
			notifyInfo(ctx, "Usage: /import-omp [latest|<session.jsonl>] [--full]\n"+
				"Switches to an omp session, translated to a PiG session: the newest of this project unless a file is named.\n"+
				"The session starts at the entries its latest compaction kept; --full keeps every entry.")
			return nil
		case "latest":
		default:
			target = field
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		notifyError(ctx, fmt.Sprintf("import-omp: %v", err))
		return nil
	}
	cwd := ctx.Cwd()
	src := target
	if src == "" {
		if src, err = ompsession.Newest(home, cwd); err != nil {
			notifyError(ctx, fmt.Sprintf("import-omp: %v", err))
			return nil
		}
	} else {
		if src == "~" || strings.HasPrefix(src, "~/") {
			src = filepath.Join(home, src[1:])
		}
		if !filepath.IsAbs(src) {
			src = filepath.Join(cwd, src)
		}
	}
	dir, err := ctx.SessionManager().GetSessionDir()
	if err != nil || dir == "" {
		notifyError(ctx, "import-omp: this session is not stored in a session directory")
		return nil
	}
	mirror := ompsession.OpenMirror(dir)
	dst, status, err := mirror.Sync(src, opts)
	if err == nil {
		err = mirror.Save()
	}
	if err != nil {
		notifyError(ctx, fmt.Sprintf("import-omp: %v", err))
		return nil
	}
	cancelled, err := ctx.SwitchSession(dst, nil)
	switch {
	case err != nil:
		notifyError(ctx, fmt.Sprintf("import-omp: %s was imported as %s, but the switch to it failed: %v", src, dst, err))
	case cancelled.Cancelled:
		notifyInfo(ctx, fmt.Sprintf("import-omp: the switch was cancelled; the session is %s.", dst))
	case status == ompsession.Kept:
		notifyInfo(ctx, fmt.Sprintf("Switched to %s as it was continued here. The omp session changed since; --full imports it again beside this one.", filepath.Base(dst)))
	default:
		notifyInfo(ctx, fmt.Sprintf("Switched to omp session %s.", filepath.Base(src)))
	}
	return nil
}
