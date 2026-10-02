package doctor

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// testOptionsHook lets tests inject a fake process list; nil in production.
var testOptionsHook func(*Options)

const credentialsPhrase = "remove credentials"

const usageText = `pig-doctor finds and safely fixes cruft in a PiG setup.

  pig-doctor check [flags]           read-only report (the default)
  pig-doctor fix [flags]             fix, confirmed per group; removals go to ~/.pig-doctor-backup/<timestamp>/
  pig-doctor restore TIMESTAMP       put a backup back (never overwrites; --force replaces an edited settings.json)
  pig-doctor backups                 list backups

flags (all commands):
  --home DIR              PiG home (default $PIG_HOME, else $XDG_CONFIG_HOME/pig, else ~/.pig)
  --agent-dir DIR         agent directory (default $PIG_CODING_AGENT_DIR, else <home>/agent)
  --backup-dir DIR        backup location (default ~/.pig-doctor-backup)
  --orphan-days N         an agent directory untouched this long is orphaned (default 14)
  --cache-days N          cache entries unused this long are prunable (default 30)
check:   --json  --strict (exit 1 when there are errors or warnings)
fix:     --dry-run (print the exact operations, change nothing)  --yes (apply every safe group, ask for the others)
         --group ID[,ID]  --keep SOURCE (which duplicate Package to keep)  --include-piglet-cells
         --confirm-credentials (with --yes: the explicit confirmation to delete credential copies)
restore: --force

groups: ` + "packages, legacy, caches, piglet-cells, orphans, orphans-sessions, credentials" + `
exit codes: 0 ok, 1 findings (--strict) or a failure, 2 usage, 3 refused because a lock is held.
`

// optionsFromEnv derives Options from an explicit environment (never os.Environ).
func optionsFromEnv(env []string) (Options, error) {
	// Resolved like PiG's codingagent.ConfigRoot, with a leading "~" expanded (ExpandTildePath).
	o := Options{Env: env, UserHome: envGet(env, "HOME")}
	switch {
	case envGet(env, "PIG_HOME") != "":
		o.Home = expandTilde(envGet(env, "PIG_HOME"), o.UserHome)
	case envGet(env, "XDG_CONFIG_HOME") != "":
		o.Home = filepath.Join(expandTilde(envGet(env, "XDG_CONFIG_HOME"), o.UserHome), "pig")
	case o.UserHome != "":
		o.Home = filepath.Join(o.UserHome, ".pig")
	}
	o.AgentDir = expandTilde(envGet(env, "PIG_CODING_AGENT_DIR"), o.UserHome)
	if o.Home == "" {
		return o, errors.New("cannot tell where the PiG home is: set PIG_HOME or HOME, or pass --home")
	}
	return o, nil
}

type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error {
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			*l = append(*l, s)
		}
	}
	return nil
}

type common struct {
	home, agent, backup   string
	orphanDays, cacheDays int
}

func (c *common) register(fs *flag.FlagSet) {
	fs.StringVar(&c.home, "home", "", "")
	fs.StringVar(&c.agent, "agent-dir", "", "")
	fs.StringVar(&c.backup, "backup-dir", "", "")
	fs.IntVar(&c.orphanDays, "orphan-days", 0, "")
	fs.IntVar(&c.cacheDays, "cache-days", 0, "")
}

// Run is the pig-doctor command line. It returns the process exit code.
func Run(args []string, io_ IO, env []string) int {
	out, errw := io_.Out, io_.Err
	if len(args) == 0 {
		args = []string{"check"}
	}
	cmd, rest := args[0], args[1:]
	if strings.HasPrefix(cmd, "-") {
		if cmd == "-h" || cmd == "--help" || cmd == "-help" {
			fmt.Fprint(out, usageText)
			return 0
		}
		cmd, rest = "check", args
	}
	switch cmd {
	case "help":
		fmt.Fprint(out, usageText)
		return 0
	case "check", "fix", "restore", "backups":
	default:
		fmt.Fprintf(errw, "pig-doctor: unknown command %q\n\n%s", cmd, usageText)
		return 2
	}

	fs := flag.NewFlagSet("pig-doctor "+cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var cf common
	cf.register(fs)
	var (
		jsonOut, strict, dry, yes, confirmCreds, piglet, force, help bool
		keep                                                         string
		groups                                                       listFlag
	)
	switch cmd {
	case "check":
		fs.BoolVar(&jsonOut, "json", false, "")
		fs.BoolVar(&strict, "strict", false, "")
	case "fix":
		fs.BoolVar(&dry, "dry-run", false, "")
		fs.BoolVar(&yes, "yes", false, "")
		fs.BoolVar(&confirmCreds, "confirm-credentials", false, "")
		fs.BoolVar(&piglet, "include-piglet-cells", false, "")
		fs.StringVar(&keep, "keep", "", "")
		fs.Var(&groups, "group", "")
	case "restore":
		fs.BoolVar(&force, "force", false, "")
	}
	fs.BoolVar(&help, "help", false, "")
	fs.BoolVar(&help, "h", false, "")
	var positional []string
	for {
		if err := fs.Parse(rest); err != nil {
			fmt.Fprintf(errw, "pig-doctor %s: %v\n\n%s", cmd, err, usageText)
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if help {
		fmt.Fprint(out, usageText)
		return 0
	}

	o, envErr := optionsFromEnv(env)
	if cf.home != "" {
		o.Home, envErr = cf.home, nil
	}
	if envErr != nil {
		fmt.Fprintf(errw, "pig-doctor: %v\n", envErr)
		return 2
	}
	if cf.agent != "" {
		o.AgentDir = cf.agent
	}
	o.BackupRoot = cf.backup
	o.OrphanDays, o.CacheDays = cf.orphanDays, cf.cacheDays
	if cf.orphanDays < 0 || cf.cacheDays < 0 {
		fmt.Fprintln(errw, "pig-doctor: day counts must not be negative")
		return 2
	}
	o.IncludePigletCells, o.Keep = piglet, keep
	o.SelfPID = os.Getpid()
	if testOptionsHook != nil {
		testOptionsHook(&o)
	}

	switch cmd {
	case "check":
		if len(positional) > 0 {
			fmt.Fprintf(errw, "pig-doctor check: unexpected argument %q\n", positional[0])
			return 2
		}
		r, err := Check(o)
		if err != nil {
			fmt.Fprintf(errw, "pig-doctor: %v\n", err)
			return 1
		}
		if jsonOut {
			fmt.Fprintln(out, r.JSON())
		} else {
			fmt.Fprint(out, r.Text())
		}
		if strict {
			for _, f := range r.Findings {
				if f.Severity != SevInfo {
					return 1
				}
			}
		}
		return 0
	case "backups":
		bs, err := ListBackups(o)
		if err != nil {
			fmt.Fprintf(errw, "pig-doctor: %v\n", err)
			return 1
		}
		if len(bs) == 0 {
			fmt.Fprintln(out, "no backups")
		}
		for _, b := range bs {
			fmt.Fprintf(out, "%s  %d entries  %s\n", b.Timestamp, b.Entries, b.Path)
		}
		return 0
	case "restore":
		if len(positional) != 1 {
			fmt.Fprintln(errw, "pig-doctor restore: give exactly one backup timestamp (see pig-doctor backups)")
			return 2
		}
		rr, err := Restore(o, positional[0], force)
		if err != nil {
			fmt.Fprintf(errw, "pig-doctor: %v\n", err)
			if errors.Is(err, ErrLocked) {
				return 3
			}
			return 1
		}
		for _, p := range rr.Restored {
			fmt.Fprintf(out, "restored %s\n", p)
		}
		for _, p := range rr.Conflict {
			fmt.Fprintf(out, "conflict, left as it is: %s\n", p)
		}
		for _, p := range rr.NotRestorable {
			fmt.Fprintf(out, "not restorable (deleted on purpose): %s\n", p)
		}
		if len(rr.Conflict) > 0 {
			return 1
		}
		return 0
	}

	// fix
	if len(positional) > 0 {
		fmt.Fprintf(errw, "pig-doctor fix: unexpected argument %q\n", positional[0])
		return 2
	}
	r, err := Check(o)
	if err != nil {
		fmt.Fprintf(errw, "pig-doctor: %v\n", err)
		return 1
	}
	plan, err := BuildPlan(r, Selection{Groups: groups})
	if err != nil {
		fmt.Fprintf(errw, "pig-doctor: %v\n", err)
		return 2
	}
	if dry {
		fmt.Fprint(out, "DRY RUN: nothing was changed. These are the exact operations a fix would perform.\n\n")
		fmt.Fprint(out, plan.DryRun())
		return 0
	}
	if len(plan.Groups) == 0 {
		fmt.Fprintln(out, "Nothing to fix.")
		return 0
	}
	in := bufio.NewReader(io_.In)
	ask := func(prompt string) string {
		fmt.Fprint(out, prompt)
		line, _ := in.ReadString('\n')
		return strings.TrimSpace(line)
	}
	confirm := func(g Group) (bool, error) {
		fmt.Fprintf(out, "\n%s\n  group %s [%s], %d operations:\n", g.Title, g.ID, g.Safety, len(g.Ops))
		for i, op := range g.Ops {
			if i == 12 {
				fmt.Fprintf(out, "    ... and %d more (see --dry-run for the full list)\n", len(g.Ops)-i)
				break
			}
			fmt.Fprintf(out, "    - %s\n", op)
		}
		switch g.Safety {
		case Credentials:
			if yes && confirmCreds {
				return true, nil
			}
			return ask(fmt.Sprintf("  This deletes credential files WITHOUT a backup. Type '%s' to continue, anything else skips: ", credentialsPhrase)) == credentialsPhrase, nil
		case SafeAuto:
			if yes {
				return true, nil
			}
		}
		return strings.EqualFold(ask(fmt.Sprintf("  Apply group %s? [y/N] ", g.ID)), "y"), nil
	}
	res, err := Apply(o, plan, confirm)
	if err != nil {
		if res != nil && res.Backup != "" {
			printResult(out, res, withDefaults(o)) // what was already done, and where its backup is
		}
		fmt.Fprintf(errw, "pig-doctor: %v\n", err)
		if errors.Is(err, ErrLocked) {
			fmt.Fprintln(errw, "nothing was changed by the refused check; wait for the other pig process or close it and run again")
			return 3
		}
		return 1
	}
	printResult(out, res, withDefaults(o))
	return 0
}

func printResult(out io.Writer, res *Result, o Options) {
	fmt.Fprintln(out)
	done, skipped := 0, 0
	for _, r := range res.Ops {
		if r.Status == "done" {
			done++
		} else {
			skipped++
			fmt.Fprintf(out, "skipped %s: %s\n", r.Op.Path, r.Message)
		}
	}
	fmt.Fprintf(out, "applied groups: %s\n", orNone(res.Applied))
	fmt.Fprintf(out, "skipped groups (not confirmed): %s\n", orNone(res.Skipped))
	fmt.Fprintf(out, "%d operations done, %d skipped, about %s moved or deleted\n", done, skipped, humanBytes(res.Freed))
	if res.Backup != "" {
		fmt.Fprintf(out, "Backup %s in %s\nRestore with: pig-doctor restore %s\n", res.Backup, o.BackupRoot, res.Backup)
	}
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}

// OptionsFromEnv derives Options from an explicit environment (for example os.Environ()).
func OptionsFromEnv(env []string) (Options, error) {
	o, err := optionsFromEnv(env)
	o.SelfPID = os.Getpid()
	return o, err
}

// CredentialsPhrase is what a person types to confirm deleting credential copies.
const CredentialsPhrase = credentialsPhrase

// GroupIDs lists the fix groups in the order they are applied.
func GroupIDs() []string { return append([]string(nil), GroupOrder...) }
