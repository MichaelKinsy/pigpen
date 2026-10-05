package doctor

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func withDefaults(o Options) Options {
	// Absolute paths only: a relative home would make the guards and the backup
	// manifest depend on the working directory.
	for _, p := range []*string{&o.Home, &o.AgentDir, &o.BackupRoot} {
		if *p != "" && *p != "." && !filepath.IsAbs(*p) {
			if abs, err := filepath.Abs(*p); err == nil {
				*p = abs
			}
		}
	}
	if o.AgentDir == "" {
		o.AgentDir = filepath.Join(o.Home, "agent")
	}
	if o.BackupRoot == "" && o.UserHome != "" {
		o.BackupRoot = filepath.Join(o.UserHome, ".pig-doctor-backup")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.OrphanDays <= 0 {
		o.OrphanDays = 14
	}
	if o.CacheDays <= 0 {
		o.CacheDays = 30
	}
	if o.SizeWarnBytes <= 0 {
		o.SizeWarnBytes = 2 << 30
	}
	if o.Exec == nil {
		o.Exec = execCommand
	}
	if o.Procs == nil {
		o.Procs = defaultProcs(o.Exec, o.Env)
	}
	o.Home = filepath.Clean(o.Home)
	o.AgentDir = filepath.Clean(o.AgentDir)
	return o
}

// scan inspects the PiG home. It changes nothing.
func scan(o Options) (*inventory, error) {
	o = withDefaults(o)
	if o.Home == "" || o.Home == "." {
		return nil, fmt.Errorf("no PiG home given")
	}
	if _, err := readDir(o.Home); err != nil {
		return nil, fmt.Errorf("cannot read PiG home %s: %w", o.Home, err)
	}
	inv := &inventory{o: o, home: o.Home, agent: o.AgentDir, now: o.Now(), cands: map[string]*cand{}}
	inv.scanProcesses()
	inv.loadSettings()
	inv.scanLegacy()
	inv.scanTopLevel()
	tc := probeToolchain(o)
	inv.toolchainBroken = tc.Missing || tc.Mismatch
	inv.scanCaches()
	inv.toolchainFindings(tc)
	inv.scanExtensions(tc)
	sort.SliceStable(inv.findings, func(i, j int) bool { return sevRank(inv.findings[i].Severity) < sevRank(inv.findings[j].Severity) })
	sort.Strings(inv.notes)
	return inv, nil
}

func sevRank(s Severity) int {
	switch s {
	case SevError:
		return 0
	case SevWarn:
		return 1
	}
	return 2
}

func (inv *inventory) scanProcesses() {
	ps, err := inv.o.Procs.List()
	if err != nil {
		inv.notes = append(inv.notes, "could not list running processes ("+err.Error()+"): orphan detection is limited to confirmation-only fixes")
		inv.envUnknown = true
		return
	}
	for _, p := range ps {
		if p.PID == inv.o.SelfPID && p.PID != 0 {
			continue
		}
		inv.running = append(inv.running, p)
		if !isPigProcess(p, inv.home, inv.o.SelfPID) {
			continue
		}
		inv.procs = append(inv.procs, p)
		if p.Env == nil {
			inv.envUnknown = true
		}
		inv.procInfo = append(inv.procInfo, ProcInfo{PID: p.PID, Executable: procName(p), AgentDir: p.Env["PIG_CODING_AGENT_DIR"]})
	}
}

func (inv *inventory) toolchainFindings(tc toolchain) {
	add := func(f Finding) {
		f.Safety = Manual
		inv.findings = append(inv.findings, f)
	}
	if tc.Missing {
		add(Finding{ID: "go.missing", Severity: SevWarn,
			Title: "no `go` found on PATH",
			Why:   "Go extensions are built from source on first use and need a Go toolchain.",
			Fix:   "install Go (for example `mise use -g go@latest`) and make sure `go version` works in the shell that starts pig"})
		return
	}
	if tc.Mismatch {
		detail := []string{"go version: " + tc.GoVersion, "go tool compile -V: " + tc.Compile}
		why := fmt.Sprintf("Go extensions are built from source and every build fails with: compile: version %q does not match go tool version %q.", tc.Compile, tc.GoVersion)
		if tc.EnvGoroot != "" {
			detail = append(detail, "GOROOT in the environment: "+tc.EnvGoroot)
			why += " An inherited GOROOT points the compiler at a different installation than the go command."
		}
		if tc.EnvGotool != "" {
			detail = append(detail, "GOTOOLCHAIN in the environment: "+tc.EnvGotool)
		}
		add(Finding{ID: "go.mismatch", Severity: SevError, Detail: detail,
			Title: fmt.Sprintf("the Go compiler (%s) and the go command (%s) are different versions", tc.Compile, tc.GoVersion),
			Why:   why,
			Fix:   "unset GOROOT GOTOOLCHAIN; hash -r; go version; go tool compile -V   (both must print the same version; if they still differ, reinstall Go, e.g. `mise install go@" + strings.TrimPrefix(tc.GoVersion, "go") + " --force`)"})
	}
	if tc.EnvGoroot != "" && tc.NatGoroot != "" && filepath.Clean(tc.EnvGoroot) != filepath.Clean(tc.NatGoroot) {
		add(Finding{ID: "go.goroot-inherited", Severity: SevWarn,
			Detail: []string{"GOROOT=" + tc.EnvGoroot, "the go on PATH (" + tc.GoPath + ") uses " + tc.NatGoroot},
			Title:  fmt.Sprintf("GOROOT=%s is inherited but the go on PATH uses %s", tc.EnvGoroot, tc.NatGoroot),
			Why:    "pig builds Go extensions with the environment it was started in; an old GOROOT export makes the compiler and the go command come from different installations.",
			Fix:    "unset GOROOT, and remove its export from your shell profile (grep -n GOROOT ~/.zshrc ~/.zprofile ~/.bashrc ~/.profile)"})
	}
	if tc.gotoolchainForces() {
		add(Finding{ID: "go.gotoolchain", Severity: SevWarn,
			Detail: []string{"GOTOOLCHAIN=" + tc.EnvGotool, "go on PATH: " + tc.GoVersion},
			Title:  fmt.Sprintf("GOTOOLCHAIN=%s pins a different Go than the go on PATH (%s)", tc.EnvGotool, tc.GoVersion),
			Why:    "the go command switches to (or downloads) the pinned toolchain, so extension builds use a compiler you did not install.",
			Fix:    "unset GOTOOLCHAIN (or export GOTOOLCHAIN=local), and remove it from your shell profile"})
	}
	if len(tc.Mise) > 1 {
		add(Finding{ID: "go.mise-multiple", Severity: SevWarn,
			Detail: []string{"installed: " + strings.Join(tc.Mise, ", "), "go on PATH: " + tc.GoPath + " (" + tc.GoVersion + ")"},
			Title:  fmt.Sprintf("%d Go versions are installed under mise (%s)", len(tc.Mise), strings.Join(tc.Mise, ", ")),
			Why:    "with several versions a stale GOROOT, a shim or a per-directory mise setting can pick a different compiler than the go command; that is how compiler and go command end up mismatched.",
			Fix:    "mise ls go; mise use -g go@" + strings.TrimPrefix(tc.GoVersion, "go") + "; mise uninstall go@<version you do not use>"})
	}
}

func (inv *inventory) scanExtensions(tc toolchain) {
	var records []*extRecord
	records = append(records, discoverDir(filepath.Join(inv.home, "extensions"), "legacy")...)
	records = append(records, discoverDir(filepath.Join(inv.agent, "extensions"), "user")...)

	var pkgs []*pkgInfo
	for _, e := range inv.packages {
		pi := inspectPackage(e, inv.o.UserHome, inv.agent)
		pkgs = append(pkgs, pi)
		if pi.Readable {
			for _, m := range pi.Enabled["extensions"] {
				if r := inspectExtension(filepath.Join(pi.Local, filepath.FromSlash(m)), "package"); r != nil {
					records = append(records, r)
				}
			}
		}
		for _, k := range memberKinds {
			if noopFilter(e.filter(k)) {
				inv.findings = append(inv.findings, Finding{
					ID: "pkg.filter-noop", Severity: SevWarn, Safety: Manual, Paths: []string{e.Source},
					Title: fmt.Sprintf("packages[%d] (%s) filters %s with %q, which does not narrow anything", e.Index, e.Source, k, strings.Join(e.filter(k), `", "`)),
					Why:   `in a Package filter "+path" force-includes a member and "-path" excludes it; with only "+" patterns every member stays enabled, so the whole Package loads.`,
					Fix:   fmt.Sprintf(`in settings.json packages[%d].%s use the plain pattern to allowlist: %q`, e.Index, k, strings.TrimPrefix(e.filter(k)[0], "+")),
				})
			}
		}
	}
	inv.packageDuplicates(pkgs)

	logs := scanExtensionLogs(filepath.Join(inv.home, "state", "extension-logs"))
	type brokenLine struct {
		name, dir string
		causes    []string
	}
	var broken []brokenLine
	seenNames := map[string]bool{}
	for _, r := range records {
		if r.Lang == "go" {
			if tc.Mismatch {
				r.Causes = append(r.Causes, fmt.Sprintf("cannot compile: compile: version %q does not match go tool version %q", tc.Compile, tc.GoVersion))
			}
			if tc.GoVersion != "" && r.GoReq != "" && versionLess(tc.GoVersion, r.GoReq) {
				r.Causes = append(r.Causes, fmt.Sprintf("requires go %s but the go on PATH is %s", r.GoReq, tc.GoVersion))
			}
		}
		if c, ok := logs[r.Name]; ok {
			r.Causes = append(r.Causes, "logged: "+c)
			seenNames[r.Name] = true
		}
		if len(r.Causes) > 0 {
			broken = append(broken, brokenLine{r.Name, r.Dir, r.Causes})
		}
	}
	var logOnly []string
	for name := range logs {
		if !seenNames[name] {
			logOnly = append(logOnly, name)
		}
	}
	sort.Strings(logOnly)
	for _, name := range logOnly {
		broken = append(broken, brokenLine{name, "no extension directory found", []string{"logged: " + logs[name]}})
	}
	if len(broken) > 0 {
		// A cause shared by every broken extension is stated once, then each extension has one short line.
		var common []string
		if len(broken) >= 3 {
			for _, c := range broken[0].causes {
				all := true
				for _, b := range broken[1:] {
					if !contains(b.causes, c) {
						all = false
					}
				}
				if all {
					common = append(common, c)
				}
			}
		}
		var lines []string
		for _, c := range common {
			lines = append(lines, "every extension below: "+c)
		}
		for _, b := range broken {
			var own []string
			for _, c := range b.causes {
				if !contains(common, c) {
					own = append(own, c)
				}
			}
			text := strings.Join(own, "; ")
			if text == "" {
				text = "(only the shared cause above)"
			}
			lines = append(lines, fmt.Sprintf("%s: %s  [%s]", b.name, text, b.dir))
		}
		inv.findings = append(inv.findings, Finding{
			ID: "ext.broken", Severity: SevError, Safety: Manual, Detail: lines,
			Title: fmt.Sprintf("%d extension(s) cannot build or load", len(broken)),
			Why:   "an extension that fails to build is skipped at startup, so its tools and commands are silently missing.",
			Fix:   "fix the cause on each line: toolchain problems are listed under go.*; then run /reload in pig",
		})
	}

	inv.extensionDuplicates(records)
}

func (inv *inventory) packageDuplicates(pkgs []*pkgInfo) {
	for _, group := range findDuplicates(pkgs, inv.o.Keep, inv.o.UserHome, inv.agent) {
		safety := SafeAuto
		var ops []Op
		var detail, fixes, paths []string
		for _, r := range group {
			if r.Safety != SafeAuto {
				safety = NeedsConfirm
			}
			ops = append(ops, Op{Kind: OpRemovePackage, Path: inv.settingsPath(), Source: r.Remove.Entry.Source, Index: r.Remove.Entry.Index, Keep: r.Keep.Entry.Source,
				Note: fmt.Sprintf("packages[%d] duplicates packages[%d]", r.Remove.Entry.Index, r.Keep.Entry.Index)})
			detail = append(detail, fmt.Sprintf("packages[%d] %s: %s; keeping packages[%d] %s", r.Remove.Entry.Index, r.Remove.Entry.Source, r.Reason, r.Keep.Entry.Index, r.Keep.Entry.Source))
			fixes = append(fixes, fmt.Sprintf("remove packages[%d] %q from %s", r.Remove.Entry.Index, r.Remove.Entry.Source, inv.settingsPath()))
			paths = append(paths, r.Remove.Entry.Source)
		}
		inv.findings = append(inv.findings, Finding{
			ID: "pkg.duplicate", Severity: SevError, Safety: safety, Group: "packages", Detail: detail, Paths: paths, ops: ops,
			Title: fmt.Sprintf("the same Package is listed %d times in settings.json (%s)", len(group)+1, group[0].Keep.Ident),
			Why:   "PiG loads every extension of every entry, so each extension is built and registered twice; duplicate tool and command names conflict.",
			Fix:   "pig-doctor fix --group packages   (" + strings.Join(fixes, "; ") + "; use --keep <source> to keep a different one)",
		})
	}
}

func (inv *inventory) extensionDuplicates(records []*extRecord) {
	by := map[string][]string{}
	for _, r := range records {
		by[r.Name] = append(by[r.Name], r.Dir)
	}
	var names []string
	for n, dirs := range by {
		if len(dirs) > 1 {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return
	}
	var detail []string
	for _, n := range names {
		detail = append(detail, n+": "+strings.Join(by[n], ", "))
	}
	inv.findings = append(inv.findings, Finding{
		ID: "ext.duplicate", Severity: SevWarn, Safety: Manual, Detail: detail,
		Title: fmt.Sprintf("%d extension name(s) are found on more than one load path", len(names)),
		Why:   "the same extension is built and started once per copy and registers the same tools and commands again.",
		Fix:   "remove the extra copies: duplicate Packages are fixed by pig-doctor fix --group packages, the legacy directory by pig-doctor fix --group legacy; anything else by hand",
	})
}

func sortedKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
