package ohmyhermes

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/memory"
	hermespaths "github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/usage"
)

const omhDAGWidgetKey = "omh_dag"

type modelPreset struct {
	Label    string
	Model    string
	Thinking string
}

var omhModelPresets = []modelPreset{
	{Label: "claude-opus-5-5 (high) — Deep / Ultrabrain / Architect", Model: "claude-opus-5-5", Thinking: "high"},
	{Label: "claude-sonnet-5-5 (medium) — Balanced / Deep / Default", Model: "claude-sonnet-5-5", Thinking: "medium"},
	{Label: "claude-haiku-5-5 (low) — Fast / Quick", Model: "claude-haiku-5-5", Thinking: "low"},
	{Label: "claude-fable-5-2 (medium) — Artistry / Writing", Model: "claude-fable-5-2", Thinking: "medium"},
	{Label: "gpt-6.2-astra (high) — Ultrabrain / Reasoning", Model: "gpt-6.2-astra", Thinking: "high"},
	{Label: "gpt-6.1-sol (high) — Review / Deep", Model: "gpt-6.1-sol", Thinking: "high"},
	{Label: "gpt-6.2-sol (high) — Forensics", Model: "gpt-6.2-sol", Thinking: "high"},
	{Label: "gemini-3.1-pro (medium) — Visual / Research", Model: "gemini-3.1-pro", Thinking: "medium"},
	{Label: "gemini-4-argon (low) — Quick / Utility", Model: "gemini-4-argon", Thinking: "low"},
}

func selectString(ctx sdk.Context, title string, options []string) (string, bool, error) {
	val, ok, err := ctx.Select(title, options)
	if err != nil || !ok {
		return "", false, err
	}
	s, ok := val.(string)
	return s, ok, nil
}

func inputString(ctx sdk.Context, title, placeholder string) (string, bool, error) {
	val, ok, err := ctx.Input(title, placeholder)
	if err != nil || !ok {
		return "", false, err
	}
	s, ok := val.(string)
	return strings.TrimSpace(s), ok, nil
}

// --- 1. Interactive /omh-model ---

func (s *surfaceState) launchFullscreenOverlay(ctx sdk.Context, title string, comp sdk.RemoteComponent) (bool, error) {
	if !ctx.HasUI() {
		return false, nil
	}
	val, err := ctx.Custom(comp, sdk.RemoteOverlayOptions{
		Title:          title,
		Overlay:        true,
		WidthFraction:  0.94,
		HeightFraction: 0.9,
	})
	if err != nil {
		return false, err
	}
	// A real host returns a non-nil value or completes the interactive overlay;
	// a stub host answering `{}` to ui.custom returns (nil, nil) without running the component.
	if val == nil {
		return false, nil
	}
	return true, nil
}

func (s *surfaceState) runInteractiveModelOverlay(ctx sdk.Context) error {
	if ctx.HasUI() {
		comp := newModelTableComponent(s)
		if handled, err := s.launchFullscreenOverlay(ctx, "OMH · Model chains", comp); err == nil && handled {
			return nil
		}
	}
	for {
		chains, overridden, err := s.router.AllChains()
		if err != nil {
			s.show(ctx, "Model chains error: "+err.Error())
			return nil
		}

		options := make([]string, 0, len(chains)+1)
		optionToCat := make(map[string]string, len(chains))
		for _, spec := range chains {
			head := "(unset)"
			if len(spec.Chain) > 0 {
				h := spec.Chain[0]
				if h.ThinkingLevel != "" {
					head = fmt.Sprintf("%s (%s)", h.Model, h.ThinkingLevel)
				} else {
					head = h.Model
				}
			}
			mark := " "
			if overridden[spec.Category] {
				mark = "*"
			}
			label := fmt.Sprintf("%s %-18s -> %s", mark, spec.Category, head)
			options = append(options, label)
			optionToCat[label] = spec.Category
		}
		doneLabel := "Done — close /omh-model"
		options = append(options, doneLabel)

		picked, ok, err := selectString(ctx, "OMH Model Routing — Select Category (* = overridden)", options)
		if err != nil {
			s.show(ctx, s.renderModelChains())
			return nil
		}
		if !ok || picked == doneLabel {
			return nil
		}
		cat, found := optionToCat[picked]
		if !found {
			return nil
		}

		if err := s.editCategoryModelChain(ctx, cat, overridden[cat]); err != nil {
			return err
		}
	}
}

func (s *surfaceState) editCategoryModelChain(ctx sdk.Context, cat string, isOverridden bool) error {
	options := make([]string, 0, len(omhModelPresets)+3)
	presetByLabel := make(map[string]modelPreset, len(omhModelPresets))
	for _, p := range omhModelPresets {
		options = append(options, p.Label)
		presetByLabel[p.Label] = p
	}
	const (
		optCustom = "Custom model & thinking level..."
		optReset  = "Reset category to built-in default"
		optBack   = "← Back to categories"
	)
	options = append(options, optCustom)
	if isOverridden {
		options = append(options, optReset)
	}
	options = append(options, optBack)

	picked, ok, err := selectString(ctx, fmt.Sprintf("OMH Model Chain [%s] — Choose Model", cat), options)
	if err != nil || !ok || picked == optBack {
		return nil
	}

	if picked == optReset {
		if err := s.router.ClearOverride(cat); err != nil {
			ctx.Notify("Failed to reset "+cat+": "+err.Error(), "error")
			return nil
		}
		ctx.Notify(fmt.Sprintf("Reset OMH model chain for %q to built-in default", cat), "info")
		return nil
	}

	var model, thinking string
	if preset, exists := presetByLabel[picked]; exists {
		model = preset.Model
		thinking = preset.Thinking
	} else if picked == optCustom {
		m, ok, err := inputString(ctx, fmt.Sprintf("Model ID for category %q", cat), "e.g. claude-sonnet-4-6")
		if err != nil || !ok || m == "" {
			return nil
		}
		model = m
		thinkChoices := []string{"off", "minimal", "low", "medium", "high", "xhigh"}
		th, ok, err := selectString(ctx, fmt.Sprintf("Thinking level for %q (%s)", cat, model), thinkChoices)
		if err == nil && ok {
			thinking = th
		}
	} else {
		return nil
	}

	if err := s.router.SetOverride(cat, []ChainEntry{{Model: model, ThinkingLevel: thinking}}); err != nil {
		ctx.Notify("Failed to save "+cat+": "+err.Error(), "error")
		return nil
	}
	ctx.Notify(fmt.Sprintf("Saved OMH chain %q -> %s (%s) in routing/model-chains.json", cat, model, thinking), "info")
	return nil
}

// --- 2. Interactive /dag ---

// renderDAGWidget renders an upstream-style compact inline folding lane tree
// (`▾`/`▸`, status glyph, title, `category · model · status`) for a DAG run
// using the active OMH theme palette (defaulting to sky when themeName is empty).
func renderDAGWidget(run *DAGRun, router *RouteRouter, themeName ...string) []string {
	if run == nil {
		return nil
	}
	tName := ""
	if len(themeName) > 0 {
		tName = themeName[0]
	}
	pal := lookupThemeSpec(tName)
	layers := computeLongestPathFrontiers(run.Nodes)
	nodeByID := make(map[string]DAGNode, len(run.Nodes))
	doneCount, runCount, failCount := 0, 0, 0
	for _, n := range run.Nodes {
		nodeByID[n.ID] = n
		switch n.Status {
		case "completed":
			doneCount++
		case "running":
			runCount++
		case "failed":
			failCount++
		}
	}

	summary := fmt.Sprintf("%s · %s [%s] · %s",
		ansiBoldHex(pal.Accent, "OMH / DAG"),
		ansiBoldHex(pal.Text, run.Title),
		ansiHex(dagStatusColorHex(run.Status, pal), run.Status),
		ansiHex(pal.Success, fmt.Sprintf("Done %d/%d", doneCount, len(run.Nodes))),
	)
	if runCount > 0 {
		summary += " · " + ansiHex(omhStatusRunningHex, fmt.Sprintf("%d running", runCount))
	}
	if failCount > 0 {
		summary += " · " + ansiHex(omhStatusFailedHex, fmt.Sprintf("%d failed", failCount))
	}
	lines := []string{summary}

	for li, layer := range layers {
		layerDone := 0
		layerRunning := 0
		for _, id := range layer {
			switch nodeByID[id].Status {
			case "completed":
				layerDone++
			case "running":
				layerRunning++
			}
		}
		collapsed := layerDone == len(layer) && len(layer) > 1
		tri := "▾"
		if collapsed {
			tri = "▸"
		}
		layerHdr := fmt.Sprintf("%s %s",
			ansiHex(pal.Accent, tri),
			ansiHex(pal.Muted, fmt.Sprintf("layer %d · %d node(s)", li+1, len(layer))),
		)
		if layerRunning > 0 {
			layerHdr += " · " + ansiHex(omhStatusRunningHex, fmt.Sprintf("%d running", layerRunning))
		}
		if layerDone > 0 {
			layerHdr += " · " + ansiHex(omhStatusCompletedHex, fmt.Sprintf("%d done", layerDone))
		}
		lines = append(lines, layerHdr)
		if collapsed {
			continue
		}
		for _, id := range layer {
			n := nodeByID[id]
			glyph, statusLabel := dagStatusGlyph(n.Status)
			statusColor := dagStatusColorHex(n.Status, pal)
			title := strings.TrimSpace(n.Title)
			if title == "" {
				title = n.ID
			}
			cat := strings.TrimSpace(n.Category)
			if cat == "" && n.Role != "" {
				if role, ok := LookupBuiltinRole(n.Role); ok {
					cat = role.DefaultCategory
				}
			}
			if cat == "" {
				cat = "default"
			}
			modelName := ""
			if router != nil {
				if chain, _, err := router.ChainForCategory(cat); err == nil && len(chain) > 0 {
					modelName = chain[0].Model
				}
			}
			meta := cat
			if modelName != "" {
				meta += " · " + modelName
			}
			meta += " · " + statusLabel
			lines = append(lines, fmt.Sprintf("    %s %s  %s",
				ansiHex(statusColor, glyph),
				ansiHex(pal.Text, title),
				ansiHex(pal.Muted, meta),
			))
		}
	}
	return lines
}

func buildDAGBoxWidgetView(run *DAGRun, router *RouteRouter, themeName string) kit.View {
	lines := renderDAGWidget(run, router, themeName)
	box := kit.NewBox(1, 0,
		kit.NewDynamicBorder("borderAccent"),
		kit.NewLines(lines),
		kit.NewDynamicBorder("borderMuted"),
	)
	return kit.View{Root: box}
}

func (s *surfaceState) runInteractiveDAGOverlay(ctx sdk.Context, runID string) error {
	if ctx.HasUI() {
		runs, err := s.dags.List()
		if err == nil && len(runs) > 0 {
			comp := newDAGPagerComponent(s, runID)
			if handled, err := s.launchFullscreenOverlay(ctx, "OMH / DAG", comp); err == nil && handled {
				return nil
			}
		}
	}
	for {
		runs, err := s.dags.List()
		if err != nil {
			s.show(ctx, "DAG list error: "+err.Error())
			return nil
		}
		if len(runs) == 0 {
			s.show(ctx, "No OMH DAG runs recorded.")
			return nil
		}

		options := make([]string, 0, len(runs)+2)
		optionToRun := make(map[string]*DAGRun, len(runs))
		for _, r := range runs {
			label := fmt.Sprintf("%s [%s] %s (%d nodes, ready=%d)",
				r.RunID, r.Status, r.Title, len(r.Nodes), len(ReadyNodes(r)))
			options = append(options, label)
			optionToRun[label] = r
		}
		const (
			optClear = "Clear pinned DAG widget"
			optClose = "Close /dag"
		)
		options = append(options, optClear, optClose)

		picked, ok, err := selectString(ctx, "OMH DAG Runs — Select Run to Inspect", options)
		if err != nil {
			s.show(ctx, s.renderDAGView(""))
			return nil
		}
		if !ok || picked == optClose {
			return nil
		}
		if picked == optClear {
			_ = ctx.SetWidget(omhDAGWidgetKey, nil)
			ctx.Notify("Cleared OMH DAG widget", "info")
			return nil
		}
		run, exists := optionToRun[picked]
		if !exists {
			return nil
		}

		// Pin structured box-frontier view as widget while inspecting this DAG.
		_ = ctx.SetWidget(omhDAGWidgetKey, buildDAGBoxWidgetView(run, s.router, s.currentTheme()), sdk.WidgetOptions{"placement": "belowEditor"})

		if err := s.inspectDAGRunInteractive(ctx, run.RunID); err != nil {
			return err
		}
	}
}

func (s *surfaceState) inspectDAGRunInteractive(ctx sdk.Context, runID string) error {
	for {
		run, err := s.dags.Get(runID)
		if err != nil {
			ctx.Notify("DAG load error: "+err.Error(), "error")
			return nil
		}
		_ = ctx.SetWidget(omhDAGWidgetKey, buildDAGBoxWidgetView(run, s.router, s.currentTheme()), sdk.WidgetOptions{"placement": "belowEditor"})

		ready := ReadyNodes(run)
		options := make([]string, 0, len(ready)+4)
		readyByOpt := make(map[string]string, len(ready))
		for _, nodeID := range ready {
			label := fmt.Sprintf("Advance ready node %q (start / complete / fail)", nodeID)
			options = append(options, label)
			readyByOpt[label] = nodeID
		}
		const (
			optShowText = "Print full DAG summary to transcript"
			optClear    = "Clear pinned DAG widget"
			optBack     = "← Back to DAG runs"
		)
		options = append(options, optShowText, optClear, optBack)

		title := fmt.Sprintf("DAG %s [%s] — Ready: %d", run.RunID, run.Status, len(ready))
		picked, ok, err := selectString(ctx, title, options)
		if err != nil || !ok || picked == optBack {
			return nil
		}
		if picked == optShowText {
			s.show(ctx, s.renderDAGView(run.RunID))
			return nil
		}
		if picked == optClear {
			_ = ctx.SetWidget(omhDAGWidgetKey, nil)
			ctx.Notify("Cleared OMH DAG widget", "info")
			return nil
		}
		if nodeID, isReady := readyByOpt[picked]; isReady {
			statusPicked, ok, err := selectString(ctx,
				fmt.Sprintf("Set status for node %q in %s", nodeID, run.RunID),
				[]string{"running", "completed", "failed", "Cancel"},
			)
			if err != nil || !ok || statusPicked == "Cancel" {
				continue
			}
			updated, err := s.dags.UpdateNode(run.RunID, nodeID, statusPicked, "", "")
			if err != nil {
				ctx.Notify("DAG update error: "+err.Error(), "error")
				continue
			}
			_ = ctx.SetWidget(omhDAGWidgetKey, buildDAGBoxWidgetView(updated, s.router, s.currentTheme()), sdk.WidgetOptions{"placement": "belowEditor"})
			ctx.SetStatus("omh", s.statusText())
			ctx.Notify(fmt.Sprintf("DAG %s node %s -> %s (DAG=%s)", updated.RunID, nodeID, statusPicked, updated.Status), "info")
		}
	}
}

// --- 3. Interactive /bots (3-level drill-down: Profiles -> Sessions/Teams -> Detail) ---

type profileBoardSummary struct {
	Name        string
	Home        string
	MemoryPct   int
	UserPct     int
	ActiveDAGs  int
	ActiveTeams int
}

func (s *surfaceState) collectProfileSummaries() []profileBoardSummary {
	active := hermespaths.ResolveProfilePaths("")
	names := []string{"default"}
	if entries, err := os.ReadDir(active.ProfilesDir); err == nil {
		for _, e := range entries {
			if e.IsDir() && hermespaths.ValidateProfileName(e.Name()) && e.Name() != "default" {
				names = append(names, e.Name())
			}
		}
	}
	sort.Strings(names[1:])

	dagList, _ := s.dags.List()
	activeDAGs := 0
	for _, d := range dagList {
		if d.Status == "active" {
			activeDAGs++
		}
	}
	teamList, _ := s.teams.List()
	activeTeams := 0
	for _, t := range teamList {
		if t.Status == "active" {
			activeTeams++
		}
	}

	out := make([]profileBoardSummary, 0, len(names))
	for _, name := range names {
		p := hermespaths.ResolveProfilePaths(name)
		memPct := memoryFileFillPercent(filepath.Join(p.MemoriesDir, "MEMORY.md"), memory.DefaultMemoryCharLimit)
		userPct := memoryFileFillPercent(filepath.Join(p.MemoriesDir, "USER.md"), memory.DefaultUserCharLimit)
		dCount, tCount := 0, 0
		if name == active.ProfileName {
			dCount = activeDAGs
			tCount = activeTeams
		}
		out = append(out, profileBoardSummary{
			Name:        name,
			Home:        p.Home,
			MemoryPct:   memPct,
			UserPct:     userPct,
			ActiveDAGs:  dCount,
			ActiveTeams: tCount,
		})
	}
	return out
}

func memoryFileFillPercent(path string, limit int) int {
	if limit <= 0 {
		return 0
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return 0
	}
	pct := (len([]rune(strings.TrimSpace(string(raw)))) * 100) / limit
	if pct > 100 {
		pct = 100
	}
	return pct
}

func (s *surfaceState) runInteractiveBotsOverlay(ctx sdk.Context) error {
	if ctx.HasUI() {
		comp := newBotsBoardComponent(s)
		if handled, err := s.launchFullscreenOverlay(ctx, "Hermes bots", comp); err == nil && handled {
			return nil
		}
	}
	// Level 1: Profiles
	for {
		profiles := s.collectProfileSummaries()
		inTok, outTok, cost, turns, _ := usage.Default().Totals()

		options := make([]string, 0, len(profiles)+2)
		optToProfile := make(map[string]profileBoardSummary, len(profiles))
		for _, p := range profiles {
			label := fmt.Sprintf("%-12s | MEM %3d%% USER %3d%% | dags:%d teams:%d",
				p.Name, p.MemoryPct, p.UserPct, p.ActiveDAGs, p.ActiveTeams)
			options = append(options, label)
			optToProfile[label] = p
		}
		const (
			optSummary = "Print full /bots summary to transcript"
			optClose   = "Close /bots"
		)
		options = append(options, optSummary, optClose)

		title := fmt.Sprintf("OMH Bot Board — Profiles (usage: in=%d out=%d $%.4f turns=%d)", inTok, outTok, cost, turns)
		picked, ok, err := selectString(ctx, title, options)
		if err != nil {
			s.show(ctx, s.renderBotsBoard())
			return nil
		}
		if !ok || picked == optClose {
			return nil
		}
		if picked == optSummary {
			s.show(ctx, s.renderBotsBoard())
			return nil
		}
		prof, exists := optToProfile[picked]
		if !exists {
			return nil
		}

		// Level 2: Sessions & Teams for selected profile
		if err := s.runBotsLevel2(ctx, prof); err != nil {
			return err
		}
	}
}

func (s *surfaceState) runBotsLevel2(ctx sdk.Context, prof profileBoardSummary) error {
	for {
		sessions := usage.Default().Snapshot()
		sort.Slice(sessions, func(i, j int) bool { return sessions[i].SessionID < sessions[j].SessionID })
		teams, _ := s.teams.List()
		delegations := s.teams.Delegations()

		options := make([]string, 0, len(sessions)+len(teams)+len(delegations)+1)
		detailByOpt := make(map[string]string)

		for _, rec := range sessions {
			label := fmt.Sprintf("[session] %-20s model=%-16s tok=%d/%d $%.4f",
				rec.SessionID, rec.Model, rec.InputTokens, rec.OutputTokens, rec.Cost)
			options = append(options, label)
			detailByOpt[label] = fmt.Sprintf(
				"Profile: %s\nSession: %s\nParent: %s\nModel: %s\nTokens: in=%d out=%d\nCost: $%.4f\nTurns: %d | Tool calls: %d",
				prof.Name, rec.SessionID, rec.ParentID, rec.Model,
				rec.InputTokens, rec.OutputTokens, rec.Cost, rec.Turns, rec.ToolCalls,
			)
		}
		for _, tm := range teams {
			label := fmt.Sprintf("[team]    %-20s [%s] %s (%d lanes)",
				tm.TeamID, tm.Status, tm.Title, len(tm.Lanes))
			options = append(options, label)
			var b strings.Builder
			fmt.Fprintf(&b, "Profile: %s\nTeam: %s [%s] — %s\n", prof.Name, tm.TeamID, tm.Status, tm.Title)
			for _, lane := range tm.Lanes {
				fmt.Fprintf(&b, "  - lane=%s [%s] cat=%s child=%s %s\n",
					lane.LaneID, lane.Status, lane.Category, lane.ChildSessionID, lane.Title)
			}
			detailByOpt[label] = strings.TrimRight(b.String(), "\n")
		}
		for _, d := range delegations {
			label := fmt.Sprintf("[deleg]   %-20s [%s] cat=%s model=%s",
				d.TaskID, d.Status, d.Category, d.Model)
			options = append(options, label)
			detailByOpt[label] = fmt.Sprintf(
				"Profile: %s\nDelegation Task: %s\nChild Session: %s\nStatus: %s\nCategory: %s | Model: %s\nTokens: in=%d out=%d | Duration: %dms",
				prof.Name, d.TaskID, d.ChildSessionID, d.Status, d.Category, d.Model,
				d.InputTokens, d.OutputTokens, d.DurationMs,
			)
		}

		const optBack = "← Back to profiles"
		options = append(options, optBack)

		title := fmt.Sprintf("Bot Board [%s] — Sessions (%d) / Teams (%d) / Delegations (%d)",
			prof.Name, len(sessions), len(teams), len(delegations))
		picked, ok, err := selectString(ctx, title, options)
		if err != nil || !ok || picked == optBack {
			return nil
		}
		detail, found := detailByOpt[picked]
		if !found {
			continue
		}

		// Level 3: Detail view
		const (
			optPrint = "Print detail to transcript"
			optBack2 = "← Back to sessions & teams"
		)
		action, ok, err := selectString(ctx, detail, []string{optPrint, optBack2})
		if err != nil || !ok {
			return nil
		}
		if action == optPrint {
			s.show(ctx, detail)
			return nil
		}
	}
}
