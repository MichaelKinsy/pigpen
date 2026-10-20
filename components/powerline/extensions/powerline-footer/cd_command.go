package powerline_footer

import "time"

// Red stub: signatures of the /cd port, no behaviour yet. upstream: cd-command.ts.

type cdTarget struct{ Path, Error string }

type cdCompletion struct{ Value, Label, Description string }

type cdNextContext interface {
	Cwd() string
	Notify(message, level string)
	SetTitle(title string)
}

type cdHost interface {
	Cwd() string
	Notify(message, level string)
	SessionFile() (string, error)
	SessionsRoot() string
	WaitForIdle() error
	SwitchSession(path string, withSession func(next cdNextContext) error) (cancelled bool, err error)
	Home() string
	Now() time.Time
}

func resolveCdTarget(args, cwd, homeDir string) *cdTarget { return nil }

func getCdArgumentCompletions(argumentPrefix, cwd, homeDir string) []cdCompletion { return nil }

func forkSessionFrom(sourcePath, targetCwd, sessionsRoot string, now time.Time) (string, error) {
	return "", nil
}

func runCdCommand(args string, ctx cdHost) error { return nil }

func nodeBasename(p string) string { return "" }

func sessionDirName(cwd string) string { return "" }

func newUUIDv7(nowMs int64) (string, error) { return "", nil }
