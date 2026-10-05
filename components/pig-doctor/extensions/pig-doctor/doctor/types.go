// Package doctor finds and safely fixes cruft in a PiG home. It is the engine
// behind the pig-doctor command line and the /doctor command and pig_doctor tool
// of the pig-doctor extension. It has no dependency on the PiG SDK.
//
// Safety model (every rule has a test, and the guards are mutation-checked):
//
//   - check is read-only. fix changes only what a Plan lists, after a Confirmer says yes.
//   - Nothing is followed through a symlink. A symlink is reported and left alone.
//   - auth.json, trust.json, models files, sessions, skills, prompts and every
//     entry the doctor cannot classify are never read, moved or deleted.
//   - Removals are moved to a timestamped backup and can be restored; only
//     regenerable caches and credential copies (after explicit confirmation) are deleted.
//   - A lock another process holds on anything a fix would change refuses the run.
package doctor

import (
	"io"
	"time"
)

// Severity orders findings in a report.
type Severity string

const (
	SevError Severity = "error"
	SevWarn  Severity = "warn"
	SevInfo  Severity = "info"
)

// Safety says how a fix may be applied.
type Safety string

const (
	// SafeAuto fixes are applied by --yes: reversible or regenerable, and provably redundant.
	SafeAuto Safety = "safe"
	// NeedsConfirm fixes are always confirmed interactively; --yes does not cover them.
	NeedsConfirm Safety = "confirm"
	// Credentials fixes delete credential copies and need an explicit typed confirmation.
	Credentials Safety = "credentials"
	// Manual findings have no automatic fix.
	Manual Safety = "manual"
)

// Finding is one problem: what, why it matters, the exact fix, and whether it can be auto-fixed.
type Finding struct {
	ID       string   `json:"id"`
	Severity Severity `json:"severity"`
	Title    string   `json:"what"`
	Why      string   `json:"why"`
	Fix      string   `json:"fix"`
	Safety   Safety   `json:"safety"`
	Group    string   `json:"group,omitempty"` // fix group; empty for Manual
	Detail   []string `json:"detail,omitempty"`
	Paths    []string `json:"paths,omitempty"`
	ops      []Op
}

// Report is the result of Check.
type Report struct {
	Home       string      `json:"home"`
	AgentDir   string      `json:"agentDir"`
	Findings   []Finding   `json:"findings"`
	Unknown    []string    `json:"unknownLeftAlone"`
	Processes  []ProcInfo  `json:"runningPigProcesses"`
	CacheSizes []CacheSize `json:"cacheSizes"`
	Notes      []string    `json:"notes,omitempty"`
	Generated  time.Time   `json:"generated"`
}

// ProcInfo is a running PiG process.
type ProcInfo struct {
	PID        int    `json:"pid"`
	Executable string `json:"executable"` // never the arguments: they can hold secrets
	AgentDir   string `json:"agentDir,omitempty"`
}

// CacheSize is the size of one cache directory.
type CacheSize struct {
	Path    string `json:"path"`
	Bytes   int64  `json:"bytes"`
	Entries int    `json:"entries"`
	InUse   int    `json:"inUse"`
	Prune   int    `json:"prunable"`
}

// OpKind names an operation a fix performs.
type OpKind string

const (
	OpMove          OpKind = "move"           // move Path into the backup (restorable)
	OpDelete        OpKind = "delete"         // delete Path (regenerable cache or credential copy; not restorable)
	OpRemovePackage OpKind = "remove-package" // remove one entry from settings.json "packages"
)

// Op is one exact operation. The dry run prints Op.String() for every op.
type Op struct {
	Kind   OpKind `json:"kind"`
	Path   string `json:"path"`
	Source string `json:"source,omitempty"` // OpRemovePackage: the entry's source string
	Index  int    `json:"index,omitempty"`  // OpRemovePackage: position at plan time
	Keep   string `json:"keep,omitempty"`   // OpRemovePackage: source of the entry that stays; it must still be there
	Bytes  int64  `json:"bytes,omitempty"`
	Note   string `json:"note,omitempty"`
}

// Group is one confirmation unit.
type Group struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Safety Safety `json:"safety"`
	Ops    []Op   `json:"ops"`
}

// Plan is the exact list of operations a fix would perform.
type Plan struct {
	Home     string  `json:"home"`
	AgentDir string  `json:"agentDir"`
	Groups   []Group `json:"groups"`
}

// ProcSource lists running processes.
type ProcSource interface {
	List() ([]Proc, error)
}

// Proc is a running process. Env is nil when it could not be read.
type Proc struct {
	PID  int
	Args []string
	Exe  string
	Cwd  string
	Env  map[string]string
}

// ExecFunc runs a command with the given environment and returns its combined stdout.
type ExecFunc func(env []string, name string, args ...string) (string, error)

// Options configures Check, Plan, Apply and Restore. Nothing is read from the
// process environment: callers pass Env explicitly, which keeps tests isolated.
type Options struct {
	Home     string   // PiG root ($PIG_HOME or ~/.pig)
	AgentDir string   // default Home/agent
	UserHome string   // the user's home: default backup location and mise data
	Env      []string // KEY=VALUE, used to find go, GOROOT, GOTOOLCHAIN, mise
	// BackupRoot defaults to UserHome/.pig-doctor-backup.
	BackupRoot string
	Now        func() time.Time
	SelfPID    int

	OrphanDays    int   // default 14
	CacheDays     int   // default 30
	SizeWarnBytes int64 // default 2 GiB

	IncludePigletCells bool   // opt in: plan pruning of piglet-cells and piglet-binary-cells
	Keep               string // for duplicate packages: keep this source (default: the broadest, then the first listed)

	Procs ProcSource
	Exec  ExecFunc
}

// IO carries the command line's streams.
type IO struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}
