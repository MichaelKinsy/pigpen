// Package pig_doctor is the pig-doctor extension: a /doctor command and a
// pig_doctor tool over the doctor engine (the same code as the pig-doctor
// command line). Nothing runs automatically. The tool is read-only; only a
// person, through /doctor fix and per-group confirmation dialogs, changes files.
package pig_doctor

import (
	"fmt"
	"os"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/MichaelKinsy/pigpen/pig-doctor/doctor"
)

const customType = "pig-doctor"

// Loader supplies the doctor's options; the default reads the process environment.
type Loader func() (doctor.Options, error)

// Extension returns the extension using the process environment.
func Extension() *sdk.Extension {
	return New(EnvLoader(os.Environ))
}

// EnvLoader reads the options from an environment. Inside pig the process the
// doctor runs in may be pig itself (a fused Piglet Binary), so it is not excluded
// from the running pig processes: its files count as in use.
func EnvLoader(environ func() []string) Loader {
	return func() (doctor.Options, error) {
		o, err := doctor.OptionsFromEnv(environ())
		o.SelfPID = 0
		return o, err
	}
}

// New returns the extension with options from load (tests inject fixture homes).
func New(load Loader) *sdk.Extension {
	e := sdk.New("pig-doctor")

	e.Command("doctor", "Find cruft in your PiG setup. /doctor (read-only report), /doctor fix (confirm each group), /doctor restore <timestamp>, /doctor backups", func(ctx sdk.Context, args string) error {
		return command(ctx, load, args)
	})

	e.Tool("pig_doctor",
		"Read-only report on the user's PiG setup: duplicate or broken extensions and Packages, Go toolchain problems, cache sizes, orphaned agent directories, legacy files, and what is unknown. mode=check reports; mode=plan lists the exact operations a fix would perform; mode=backups lists backups. It never changes anything; tell the user to run /doctor fix to apply a fix.",
		sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"mode":   map[string]any{"type": "string", "enum": []any{"check", "plan", "backups"}, "description": "check (default), plan or backups"},
				"groups": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": stringsToAny(doctor.GroupIDs())}, "description": "plan only: restrict to these fix groups"},
			},
		},
		func(ctx sdk.Context, params map[string]any) (any, error) {
			o, err := load()
			if err != nil {
				return nil, err
			}
			mode, _ := params["mode"].(string)
			switch mode {
			case "", "check":
				r, err := doctor.Check(o)
				if err != nil {
					return nil, err
				}
				return r.Text(), nil
			case "plan":
				r, err := doctor.Check(o)
				if err != nil {
					return nil, err
				}
				var groups []string
				if gs, ok := params["groups"].([]any); ok {
					for _, g := range gs {
						if s, ok := g.(string); ok {
							groups = append(groups, s)
						}
					}
				}
				p, err := doctor.BuildPlan(r, doctor.Selection{Groups: groups})
				if err != nil {
					return nil, err
				}
				return "DRY RUN: nothing was changed.\n" + p.DryRun(), nil
			case "backups":
				return backupsText(o)
			}
			return nil, fmt.Errorf("unknown mode %q (check, plan, backups)", mode)
		})
	return e
}

func stringsToAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

func backupsText(o doctor.Options) (string, error) {
	bs, err := doctor.ListBackups(o)
	if err != nil {
		return "", err
	}
	if len(bs) == 0 {
		return "no backups", nil
	}
	var b strings.Builder
	for _, x := range bs {
		fmt.Fprintf(&b, "%s  %d entries  %s\n", x.Timestamp, x.Entries, x.Path)
	}
	return b.String(), nil
}

func say(ctx sdk.Context, text string) error {
	no := false
	return ctx.SendMessage(customType, text, true, sdk.SendMessageOptions{TriggerTurn: &no})
}

func command(ctx sdk.Context, load Loader, args string) error {
	o, err := load()
	if err != nil {
		return err
	}
	fields := strings.Fields(args)
	sub := "check"
	if len(fields) > 0 && !strings.HasPrefix(fields[0], "-") {
		sub, fields = fields[0], fields[1:]
	}
	switch sub {
	case "check":
		r, err := doctor.Check(o)
		if err != nil {
			return err
		}
		return say(ctx, r.Text())
	case "backups":
		t, err := backupsText(o)
		if err != nil {
			return err
		}
		return say(ctx, t)
	case "restore":
		return restoreCmd(ctx, o, fields)
	case "fix":
		return fixCmd(ctx, o, fields)
	case "help":
		return say(ctx, "/doctor  read-only report\n/doctor fix [--group ID[,ID]] [--keep SOURCE] [--include-piglet-cells]  confirm and apply per group\n/doctor restore <timestamp> [--force]\n/doctor backups\nGroups: "+strings.Join(doctor.GroupIDs(), ", "))
	}
	return fmt.Errorf("unknown /doctor subcommand %q (try /doctor help)", sub)
}

func restoreCmd(ctx sdk.Context, o doctor.Options, fields []string) error {
	force := false
	var ts string
	for _, f := range fields {
		if f == "--force" {
			force = true
		} else {
			ts = f
		}
	}
	if ts == "" {
		return fmt.Errorf("usage: /doctor restore <timestamp> [--force]")
	}
	if !ctx.HasUI() {
		return fmt.Errorf("restore needs an interactive session; run `pig-doctor restore %s`", ts)
	}
	ok, err := ctx.Confirm("Restore backup "+ts+"?", "Moves the backed-up files back. Nothing that exists now is overwritten"+map[bool]string{true: ", except an edited settings.json (--force)", false: ""}[force]+".")
	if err != nil || !ok {
		return err
	}
	rr, err := doctor.Restore(o, ts, force)
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, p := range rr.Restored {
		fmt.Fprintf(&b, "restored %s\n", p)
	}
	for _, p := range rr.Conflict {
		fmt.Fprintf(&b, "conflict, left as it is: %s\n", p)
	}
	for _, p := range rr.NotRestorable {
		fmt.Fprintf(&b, "not restorable (deleted on purpose): %s\n", p)
	}
	if b.Len() == 0 {
		b.WriteString("nothing to restore")
	}
	return say(ctx, b.String())
}

func fixCmd(ctx sdk.Context, o doctor.Options, fields []string) error {
	var groups []string
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "--group":
			if i+1 >= len(fields) {
				return fmt.Errorf("--group needs a value")
			}
			i++
			for _, g := range strings.Split(fields[i], ",") {
				if g != "" {
					groups = append(groups, g)
				}
			}
		case "--keep":
			if i+1 >= len(fields) {
				return fmt.Errorf("--keep needs a value")
			}
			i++
			o.Keep = fields[i]
		case "--include-piglet-cells":
			o.IncludePigletCells = true
		default:
			return fmt.Errorf("unknown option %q", fields[i])
		}
	}
	r, err := doctor.Check(o)
	if err != nil {
		return err
	}
	plan, err := doctor.BuildPlan(r, doctor.Selection{Groups: groups})
	if err != nil {
		return err
	}
	if !ctx.HasUI() {
		if err := say(ctx, "DRY RUN: fixing needs an interactive session (every group is confirmed in a dialog). Nothing was changed.\n"+plan.DryRun()); err != nil {
			return err
		}
		return fmt.Errorf("/doctor fix needs an interactive session; run `pig-doctor fix` in a terminal")
	}
	if len(plan.Groups) == 0 {
		return say(ctx, "Nothing to fix.")
	}
	if err := say(ctx, "Planned operations (nothing has changed yet):\n"+plan.DryRun()); err != nil {
		return err
	}
	res, err := doctor.Apply(o, plan, func(g doctor.Group) (bool, error) {
		var lines []string
		for i, op := range g.Ops {
			if i == 15 {
				lines = append(lines, fmt.Sprintf("... and %d more", len(g.Ops)-i))
				break
			}
			lines = append(lines, op.String())
		}
		msg := strings.Join(lines, "\n")
		if g.Safety == doctor.Credentials {
			phrase, ok, err := ctx.Input("Delete credential copies WITHOUT a backup? Type '"+doctor.CredentialsPhrase+"'", msg)
			return err == nil && ok && strings.TrimSpace(phrase) == doctor.CredentialsPhrase, err
		}
		return ctx.Confirm(fmt.Sprintf("%s [%s]", g.Title, g.Safety), msg)
	})
	if err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "applied groups: %s\nskipped groups (not confirmed): %s\n", orNone(res.Applied), orNone(res.Skipped))
	for _, r := range res.Ops {
		if r.Status != "done" {
			fmt.Fprintf(&b, "%s %s: %s\n", r.Status, r.Op.Path, r.Message)
		}
	}
	if res.Backup != "" {
		fmt.Fprintf(&b, "Backup %s. Restore with /doctor restore %s or pig-doctor restore %s\n", res.Backup, res.Backup, res.Backup)
	}
	b.WriteString("Run /reload so pig picks up the changed settings.")
	return say(ctx, b.String())
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}
