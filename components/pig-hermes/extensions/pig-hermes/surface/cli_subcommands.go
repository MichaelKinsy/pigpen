package surface

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/clihook"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/cli"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/memory"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
	"github.com/MichaelKinsy/pigpen/pig-hermes/agents"
)

func init() {
	registerCLI()
}

var registerPreprocOnce sync.Once

// registerCLI installs Hermes CLI subcommands and the -p/--profile flag preprocessor
// into pig-hermes-core/cli (never calling clihook.Register directly).
func registerCLI() {
	registerPreprocOnce.Do(func() {
		cli.RegisterFlagPreprocessor(profileFlagPreprocessor)
	})
	cli.RegisterSubcommand("profile", "Manage Hermes profiles (list, current, use, create, delete)", runProfileSubcommand)
	cli.RegisterSubcommand("skills", "List and view Hermes skills (list, view)", runSkillsSubcommand)
	cli.RegisterSubcommand("cron", "List and remove scheduled Hermes cron jobs (list, remove)", runCronSubcommand)
	cli.RegisterSubcommand("hermes-status", "Show active Hermes profile, paths, memory usage, toolset, and skin", runStatusSubcommand)
	cli.RegisterSubcommand("doctor", "Check Hermes config, memories, skills, cron store, and external tools", runDoctorSubcommand)
}

func envOut(env clihook.Env) io.Writer {
	if env.Stdout != nil {
		return env.Stdout
	}
	return io.Discard
}

func envErr(env clihook.Env) io.Writer {
	if env.Stderr != nil {
		return env.Stderr
	}
	return io.Discard
}

// profileFlagPreprocessor strips -p/--profile before PiG parses remaining flags
// and sets HERMES_PROFILE so subsequent path resolution targets that profile.
func profileFlagPreprocessor(env clihook.Env, args []string) ([]string, bool, int) {
	if len(args) == 0 {
		return args, false, 0
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			out = append(out, args[i:]...)
			break
		}
		if strings.HasPrefix(arg, "--profile=") {
			val := strings.TrimSpace(strings.TrimPrefix(arg, "--profile="))
			if !paths.ValidateProfileName(val) {
				fmt.Fprintf(envErr(env), "error: invalid profile name %q\n", val)
				return nil, true, 2
			}
			_ = os.Setenv("HERMES_PROFILE", val)
			continue
		}
		if arg == "--profile" {
			if i+1 >= len(args) {
				fmt.Fprintf(envErr(env), "error: --profile requires a profile name\n")
				return nil, true, 2
			}
			val := strings.TrimSpace(args[i+1])
			if !paths.ValidateProfileName(val) {
				fmt.Fprintf(envErr(env), "error: invalid profile name %q\n", val)
				return nil, true, 2
			}
			_ = os.Setenv("HERMES_PROFILE", val)
			i++
			continue
		}
		if arg == "-p" {
			if i+1 < len(args) && isExistingProfileName(args[i+1]) {
				_ = os.Setenv("HERMES_PROFILE", args[i+1])
				i++
				continue
			}
		}
		out = append(out, arg)
	}
	return out, false, 0
}

// isExistingProfileName reports whether name is "default" or an existing
// profile directory under ProfilesDir, so `-p "hello"` still reaches PiG's print mode.
func isExistingProfileName(name string) bool {
	if strings.HasPrefix(name, "-") || !paths.ValidateProfileName(name) {
		return false
	}
	if name == "default" {
		return true
	}
	p := paths.ResolveProfilePaths("")
	info, err := os.Stat(filepath.Join(p.ProfilesDir, name))
	return err == nil && info.IsDir()
}

// ── profile subcommand ───────────────────────────────────────────────────────

func listProfileNames(p paths.ProfilePaths) []string {
	seen := map[string]bool{"default": true}
	names := []string{"default"}
	entries, err := os.ReadDir(p.ProfilesDir)
	if err == nil {
		var extra []string
		for _, e := range entries {
			if e.IsDir() && paths.ValidateProfileName(e.Name()) && !seen[e.Name()] {
				seen[e.Name()] = true
				extra = append(extra, e.Name())
			}
		}
		sort.Strings(extra)
		names = append(names, extra...)
	}
	return names
}

func runProfileSubcommand(env clihook.Env, args []string) int {
	p := paths.ResolveProfilePaths("")
	out := envOut(env)
	errOut := envErr(env)

	action := "list"
	if len(args) > 0 {
		action = strings.ToLower(strings.TrimSpace(args[0]))
	}
	rest := []string{}
	if len(args) > 1 {
		rest = args[1:]
	}

	switch action {
	case "list", "ls":
		names := listProfileNames(p)
		for _, name := range names {
			marker := " "
			if name == p.ProfileName {
				marker = "*"
			}
			home := p.RootHome
			if name != "default" {
				home = filepath.Join(p.ProfilesDir, name)
			}
			fmt.Fprintf(out, "%s %s (%s)\n", marker, name, home)
		}
		return 0

	case "current", "show":
		fmt.Fprintf(out, "%s (%s)\n", p.ProfileName, p.Home)
		return 0

	case "use":
		if len(rest) < 1 {
			fmt.Fprintf(errOut, "usage: profile use <name>\n")
			return 2
		}
		name := strings.TrimSpace(rest[0])
		if name != "default" {
			if !paths.ValidateProfileName(name) {
				fmt.Fprintf(errOut, "error: invalid profile name %q\n", name)
				return 1
			}
			dir := filepath.Join(p.ProfilesDir, name)
			info, err := os.Stat(dir)
			if err != nil || !info.IsDir() {
				fmt.Fprintf(errOut, "error: profile %q does not exist\n", name)
				return 1
			}
		}
		if err := os.MkdirAll(filepath.Dir(p.ActiveProfile), 0o755); err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		if err := os.WriteFile(p.ActiveProfile, []byte(name+"\n"), 0o644); err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		fmt.Fprintf(out, "Active profile set to %s\n", name)
		return 0

	case "create":
		if len(rest) < 1 {
			fmt.Fprintf(errOut, "usage: profile create <name>\n")
			return 2
		}
		name := strings.TrimSpace(rest[0])
		if name == "default" || !paths.ValidateProfileName(name) {
			fmt.Fprintf(errOut, "error: invalid profile name %q\n", name)
			return 1
		}
		dir := filepath.Join(p.ProfilesDir, name)
		if _, err := os.Stat(dir); err == nil {
			fmt.Fprintf(errOut, "error: profile %q already exists\n", name)
			return 1
		}
		for _, sub := range []string{"", "memories", "skills", "cron"} {
			target := dir
			if sub != "" {
				target = filepath.Join(dir, sub)
			}
			if err := os.MkdirAll(target, 0o755); err != nil {
				fmt.Fprintf(errOut, "error: %v\n", err)
				return 1
			}
		}
		fmt.Fprintf(out, "Created profile %s at %s\n", name, dir)
		return 0

	case "delete", "rm":
		if len(rest) < 1 {
			fmt.Fprintf(errOut, "usage: profile delete <name>\n")
			return 2
		}
		name := strings.TrimSpace(rest[0])
		if name == "default" {
			fmt.Fprintf(errOut, "error: cannot delete default profile\n")
			return 1
		}
		if !paths.ValidateProfileName(name) {
			fmt.Fprintf(errOut, "error: invalid profile name %q\n", name)
			return 1
		}
		dir := filepath.Join(p.ProfilesDir, name)
		if _, err := os.Stat(dir); err != nil {
			fmt.Fprintf(errOut, "error: profile %q does not exist\n", name)
			return 1
		}
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		if p.ProfileName == name {
			_ = os.WriteFile(p.ActiveProfile, []byte("default\n"), 0o644)
		}
		fmt.Fprintf(out, "Deleted profile %s\n", name)
		return 0

	default:
		fmt.Fprintf(errOut, "usage: profile <list|current|use|create|delete> [name]\n")
		return 2
	}
}

// ── skills subcommand ────────────────────────────────────────────────────────

type discoveredSkill struct {
	Name        string
	Description string
	Path        string
}

func discoverSkills(skillsDir string) []discoveredSkill {
	var out []discoveredSkill
	if skillsDir == "" {
		return out
	}
	info, err := os.Stat(skillsDir)
	if err != nil || !info.IsDir() {
		return out
	}
	_ = filepath.WalkDir(skillsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.EqualFold(d.Name(), "SKILL.md") {
			name, desc := parseSkillHeader(path, skillsDir)
			out = append(out, discoveredSkill{
				Name:        name,
				Description: desc,
				Path:        path,
			})
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func parseSkillHeader(skillFile, skillsDir string) (string, string) {
	rel, err := filepath.Rel(skillsDir, filepath.Dir(skillFile))
	name := filepath.Base(filepath.Dir(skillFile))
	if err == nil && rel != "." && rel != "" {
		name = filepath.ToSlash(rel)
	}
	raw, err := os.ReadFile(skillFile)
	if err != nil {
		return name, ""
	}
	lines := strings.Split(string(raw), "\n")
	var desc string
	inFrontmatter := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if i == 0 && trimmed == "---" {
			inFrontmatter = true
			continue
		}
		if inFrontmatter {
			if trimmed == "---" {
				inFrontmatter = false
				continue
			}
			if strings.HasPrefix(trimmed, "name:") {
				v := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "name:")), `"'`)
				if v != "" {
					name = v
				}
			} else if strings.HasPrefix(trimmed, "description:") {
				desc = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "description:")), `"'`)
			}
			continue
		}
		if desc == "" && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			desc = trimmed
			break
		}
	}
	return name, desc
}

func runSkillsSubcommand(env clihook.Env, args []string) int {
	p := paths.ResolveProfilePaths("")
	out := envOut(env)
	errOut := envErr(env)

	action := "list"
	if len(args) > 0 {
		action = strings.ToLower(strings.TrimSpace(args[0]))
	}
	rest := []string{}
	if len(args) > 1 {
		rest = args[1:]
	}

	switch action {
	case "list", "ls":
		skills := discoverSkills(p.SkillsDir)
		if len(skills) == 0 {
			fmt.Fprintf(out, "No skills found in %s\n", p.SkillsDir)
			return 0
		}
		for _, s := range skills {
			if s.Description != "" {
				fmt.Fprintf(out, "- %s: %s\n", s.Name, s.Description)
			} else {
				fmt.Fprintf(out, "- %s\n", s.Name)
			}
		}
		return 0

	case "view", "inspect":
		if len(rest) < 1 {
			fmt.Fprintf(errOut, "usage: skills view <name>\n")
			return 2
		}
		target := strings.TrimSpace(rest[0])
		skills := discoverSkills(p.SkillsDir)
		for _, s := range skills {
			if s.Name == target || strings.EqualFold(s.Name, target) {
				raw, err := os.ReadFile(s.Path)
				if err != nil {
					fmt.Fprintf(errOut, "error: %v\n", err)
					return 1
				}
				fmt.Fprint(out, string(raw))
				if len(raw) > 0 && raw[len(raw)-1] != '\n' {
					fmt.Fprintln(out)
				}
				return 0
			}
		}
		fmt.Fprintf(errOut, "error: skill %q not found\n", target)
		return 1

	default:
		fmt.Fprintf(errOut, "usage: skills <list|view> [name]\n")
		return 2
	}
}

// ── cron subcommand & store helpers ──────────────────────────────────────────

func formatCronJobs(jobs []agents.CronJob) string {
	if len(jobs) == 0 {
		return "No scheduled cron jobs."
	}
	var b strings.Builder
	for _, j := range jobs {
		status := "active"
		if !j.Enabled || strings.EqualFold(j.State, "paused") {
			status = "paused"
		} else if j.State != "" {
			status = j.State
		}
		sched := j.ScheduleDisplay
		if sched == "" {
			sched = j.Schedule.Display
		}
		if sched == "" {
			sched = j.Schedule.Expr
		}
		if sched == "" {
			sched = j.Schedule.RunAt
		}
		label := j.Prompt
		if j.Name != "" {
			label = j.Name
		}
		if len(label) > 60 {
			label = label[:57] + "..."
		}
		fmt.Fprintf(&b, "- %s [%s] (%s) %s\n", j.ID, status, sched, label)
	}
	return strings.TrimRight(b.String(), "\n")
}

func runCronSubcommand(env clihook.Env, args []string) int {
	p := paths.ResolveProfilePaths("")
	out := envOut(env)
	errOut := envErr(env)
	store := agents.NewCronStore(p.CronJobsFile)

	action := "list"
	if len(args) > 0 {
		action = strings.ToLower(strings.TrimSpace(args[0]))
	}
	rest := []string{}
	if len(args) > 1 {
		rest = args[1:]
	}

	switch action {
	case "list", "ls":
		jobs, err := store.List(true)
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		fmt.Fprintln(out, formatCronJobs(jobs))
		return 0

	case "remove", "rm", "delete":
		if len(rest) < 1 {
			fmt.Fprintf(errOut, "usage: cron remove <id>\n")
			return 2
		}
		id := strings.TrimSpace(rest[0])
		removed, err := store.Remove(id)
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		fmt.Fprintf(out, "Removed cron job %s\n", removed.ID)
		return 0

	default:
		fmt.Fprintf(errOut, "usage: cron <list|remove> [id]\n")
		return 2
	}
}

// ── status subcommand ────────────────────────────────────────────────────────

func runStatusSubcommand(env clihook.Env, _ []string) int {
	p := paths.ResolveProfilePaths("")
	cfg, _ := config.Load(p.ConfigFile, p.EnvFile)
	store := memory.NewStore(p.MemoriesDir, cfg.MemoryLimit, cfg.UserLimit)
	memEntries, memChars, memLimit, _ := store.ReadLive(memory.TargetMemory)
	userEntries, userChars, userLimit, _ := store.ReadLive(memory.TargetUser)

	memPct := calcPercent(memChars, memLimit)
	userPct := calcPercent(userChars, userLimit)

	out := envOut(env)
	fmt.Fprintf(out, "Hermes Status\n")
	fmt.Fprintf(out, "  Profile:     %s\n", p.ProfileName)
	fmt.Fprintf(out, "  HERMES_HOME: %s\n", p.Home)
	fmt.Fprintf(out, "  Config:      %s\n", p.ConfigFile)
	fmt.Fprintf(out, "  MEMORY.md:   %d/%d chars (%d%%, %d entries)\n", memChars, memLimit, memPct, len(memEntries))
	fmt.Fprintf(out, "  USER.md:     %d/%d chars (%d%%, %d entries)\n", userChars, userLimit, userPct, len(userEntries))
	fmt.Fprintf(out, "  Toolset:     %s\n", cfg.Toolset)
	fmt.Fprintf(out, "  Skin:        %s\n", cfg.Skin)
	fmt.Fprintf(out, "  YOLO:        %v\n", cfg.IsYolo())
	return 0
}

// ── doctor subcommand ────────────────────────────────────────────────────────

func runDoctorSubcommand(env clihook.Env, _ []string) int {
	p := paths.ResolveProfilePaths("")
	out := envOut(env)
	exitCode := 0

	fmt.Fprintf(out, "Hermes Doctor (%s)\n", p.ProfileName)

	// 1. Config file
	if _, err := os.Stat(p.ConfigFile); err == nil {
		fmt.Fprintf(out, "  [OK]   config: %s\n", p.ConfigFile)
	} else {
		fmt.Fprintf(out, "  [INFO] config: %s (not found; using defaults)\n", p.ConfigFile)
	}

	// 2. Memories dir
	if info, err := os.Stat(p.MemoriesDir); err == nil && info.IsDir() {
		fmt.Fprintf(out, "  [OK]   memories dir: %s\n", p.MemoriesDir)
	} else {
		fmt.Fprintf(out, "  [WARN] memories dir missing: %s\n", p.MemoriesDir)
	}

	// 3. Skills dir
	if info, err := os.Stat(p.SkillsDir); err == nil && info.IsDir() {
		skills := discoverSkills(p.SkillsDir)
		fmt.Fprintf(out, "  [OK]   skills dir: %s (%d skills)\n", p.SkillsDir, len(skills))
	} else {
		fmt.Fprintf(out, "  [INFO] skills dir missing: %s\n", p.SkillsDir)
	}

	// 4. Cron file
	if _, err := os.Stat(p.CronJobsFile); err == nil {
		jobs, parseErr := agents.NewCronStore(p.CronJobsFile).List(true)
		if parseErr != nil {
			fmt.Fprintf(out, "  [FAIL] cron file invalid JSON: %s (%v)\n", p.CronJobsFile, parseErr)
			exitCode = 1
		} else {
			fmt.Fprintf(out, "  [OK]   cron file: %s (%d jobs)\n", p.CronJobsFile, len(jobs))
		}
	} else {
		fmt.Fprintf(out, "  [INFO] cron file: %s (not initialized)\n", p.CronJobsFile)
	}

	// 5. External tools
	for _, bin := range []string{"rg", "python3", "git"} {
		if path, err := exec.LookPath(bin); err == nil {
			fmt.Fprintf(out, "  [OK]   tool %s: %s\n", bin, path)
		} else {
			fmt.Fprintf(out, "  [WARN] tool %s: not found in PATH\n", bin)
		}
	}

	return exitCode
}

func calcPercent(used, limit int) int {
	if limit <= 0 {
		return 0
	}
	return (used * 100) / limit
}
