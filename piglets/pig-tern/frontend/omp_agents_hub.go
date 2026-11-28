package frontend

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
	"github.com/stencil-hq/tern-sdk/go/tern/ui"
)

// ompAgentHubState tracks the native Agent Hub modal and its child Agent
// Transcript Viewer overlay in Tern. All fields are guarded by session.mu.
type ompAgentHubState struct {
	open            bool
	tab             string // "agents" | "activity"
	scope           string // "all" | "running" | "done" | "parked"
	activityScope   string // "all" | "errors" | "responses" | "tools"
	treeMode        bool
	filtering       bool
	query           string
	selectedID      string
	selectedActID   string
	notice          string
	noticeTone      ui.Tone
	transcriptOpen  bool
	transcriptID    string
	transcript      []AgentHubTranscriptEntry
	transcriptDraft string
	transcriptNote  string
	transcriptStamp agentTranscriptStamp
	reloadPending   bool
	pending         map[string]bool
	keyDecoder      tern.KeyDecoder
	clockAt         time.Time
	clockNowMs      int64
}

type agentTranscriptStamp struct {
	status       string
	tools        int
	requests     int
	lastActiveMs int64
}

const (
	agentTranscriptInputID       = "layer.agent-transcript.compose.input"
	maxTranscriptViewerEntries   = 100
	maxTranscriptThinkingRunes   = 1500
	maxTranscriptToolOutputRunes = 4000
	pickerClockReuse             = 5 * time.Second
)

func (s *session) initOmpBridge() {
	if !s.ompRoles {
		return
	}
	if s.agentHubSnapshotFn != nil {
		s.agentHub = cloneFrontendAgentHubSnapshot(s.agentHubSnapshotFn())
	}
	if s.onAgentHubChangeFn != nil {
		s.agentHubUnsub = s.onAgentHubChangeFn(func(snap AgentHubSnapshot) {
			s.onAgentHubSnapshot(snap)
		})
	}
	if s.setAgentHubOpenerFn != nil {
		s.setAgentHubOpenerFn(s.openAgentHub)
	}
	if s.composerMetaFn != nil {
		s.composerMeta = s.composerMetaFn()
	}
	if s.onComposerMetaChangeFn != nil {
		s.composerMetaUnsub = s.onComposerMetaChangeFn(func(meta ComposerMeta) {
			s.onComposerMetaChange(meta)
		})
	}
	if s.progressHudSnapshotFn != nil {
		s.progressHud = cloneFrontendProgressHudSnapshot(s.progressHudSnapshotFn())
	}
	if s.onProgressHudChangeFn != nil {
		s.progressHudUnsub = s.onProgressHudChangeFn(func(snap ProgressHudSnapshot) {
			s.onProgressHudChange(snap)
		})
	}
	if s.onTurnUsageFn != nil {
		s.turnUsageUnsub = s.onTurnUsageFn(func(rec TurnUsageRecord) {
			s.onTurnUsage(rec)
		})
	}
}

func cloneFrontendProgressHudSnapshot(s ProgressHudSnapshot) ProgressHudSnapshot {
	if len(s.JudgeBatches) > 0 {
		cp := make([]JudgeBatchHudItem, len(s.JudgeBatches))
		copy(cp, s.JudgeBatches)
		s.JudgeBatches = cp
	}
	if len(s.Downloads) > 0 {
		cp := make([]DownloadHudItem, len(s.Downloads))
		copy(cp, s.Downloads)
		s.Downloads = cp
	}
	if len(s.Jobs) > 0 {
		cp := make([]JobHudItem, len(s.Jobs))
		for i, job := range s.Jobs {
			if len(job.SparkVals) > 0 {
				vals := make([]float64, len(job.SparkVals))
				copy(vals, job.SparkVals)
				job.SparkVals = vals
			}
			cp[i] = job
		}
		s.Jobs = cp
	}
	return s
}

func (s *session) onComposerMetaChange(meta ComposerMeta) {
	s.mu.Lock()
	if s.closed || s.fellBack {
		s.mu.Unlock()
		return
	}
	if s.composerMetaFn != nil {
		meta = s.composerMetaFn()
	}
	if s.composerMeta == meta {
		s.mu.Unlock()
		return
	}
	s.composerMeta = meta
	s.rebuildOmpEditor()
	_ = s.flushLocked()
	s.mu.Unlock()
}

func (s *session) onProgressHudChange(snap ProgressHudSnapshot) {
	s.mu.Lock()
	if s.closed || s.fellBack {
		s.mu.Unlock()
		return
	}
	if s.progressHudSnapshotFn != nil {
		snap = s.progressHudSnapshotFn()
	}
	s.progressHud = cloneFrontendProgressHudSnapshot(snap)
	s.dirty = true
	_ = s.flushLocked()
	s.mu.Unlock()
}

func cloneFrontendAgentHubSnapshot(s AgentHubSnapshot) AgentHubSnapshot {
	if len(s.Agents) > 0 {
		cp := make([]AgentHubItem, len(s.Agents))
		copy(cp, s.Agents)
		s.Agents = cp
	}
	if len(s.Activity) > 0 {
		cp := make([]AgentHubActivity, len(s.Activity))
		copy(cp, s.Activity)
		s.Activity = cp
	}
	return s
}

func (s *session) onAgentHubSnapshot(snap AgentHubSnapshot) {
	s.mu.Lock()
	if s.closed || s.fellBack {
		s.mu.Unlock()
		return
	}
	if s.agentHubSnapshotFn != nil {
		snap = s.agentHubSnapshotFn()
	}
	prevCost := s.agentHub.SubagentCost
	s.agentHub = cloneFrontendAgentHubSnapshot(snap)
	s.dirty = true
	if prevCost != snap.SubagentCost {
		s.rebuildOmpEditor()
	}
	if s.agentHubUI != nil && s.agentHubUI.open {
		s.agentHubUI.clockAt = time.Time{}
		s.reconcileAgentHubSelectionLocked()
		if s.agentHubUI.transcriptOpen && s.agentHubUI.transcriptID != "" && s.loadAgentTranscriptFn != nil {
			if item := s.findAgentHubItemLocked(s.agentHubUI.transcriptID); item != nil {
				stamp := agentTranscriptStamp{
					status:       item.Status,
					tools:        item.Tools,
					requests:     item.Requests,
					lastActiveMs: item.LastActiveMs,
				}
				if stamp != s.agentHubUI.transcriptStamp && !s.agentHubUI.reloadPending {
					s.agentHubUI.transcriptStamp = stamp
					s.agentHubUI.reloadPending = true
					go s.reloadAgentTranscriptAsync(s.agentHubUI.transcriptID, s.loadAgentTranscriptFn)
				}
			}
		}
	}
	_ = s.flushLocked()
	s.mu.Unlock()
}

func (s *session) reloadAgentTranscriptAsync(agentID string, loadFn func(string) []AgentHubTranscriptEntry) {
	entries := loadFn(agentID)
	s.mu.Lock()
	if s.agentHubUI != nil {
		s.agentHubUI.reloadPending = false
		if !s.closed && !s.fellBack && s.agentHubUI.open && s.agentHubUI.transcriptOpen && s.agentHubUI.transcriptID == agentID {
			s.agentHubUI.transcript = entries
			s.dirty = true
		}
		_ = s.flushLocked()
	}
	s.mu.Unlock()
}

func (s *session) hasBlockingLayerLocked() bool {
	if s.screen != nil || len(s.overlays) > 0 {
		return true
	}
	for _, e := range s.dock {
		if isLayerNode(e.node) {
			return true
		}
	}
	return false
}

func (s *session) findAgentHubItemLocked(agentID string) *AgentHubItem {
	for i := range s.agentHub.Agents {
		if s.agentHub.Agents[i].ID == agentID {
			cp := s.agentHub.Agents[i]
			return &cp
		}
	}
	return nil
}

func agentHubCanMessage(item *AgentHubItem, sendFn func(string, string) error) bool {
	return item != nil && sendFn != nil && (item.Status == "running" || item.Status == "idle" || item.CanRevive)
}

// openAgentHub opens the native Agent Hub modal in Tern. Safe to call from any
// goroutine (HUD pill click, `/agents` slash command, or tests).
func (s *session) openAgentHub() {
	s.mu.Lock()
	if !s.ompRoles || s.closed || s.fellBack || s.hasBlockingLayerLocked() {
		s.mu.Unlock()
		return
	}
	snapFn := s.agentHubSnapshotFn
	s.mu.Unlock()

	var latest *AgentHubSnapshot
	if snapFn != nil {
		cp := cloneFrontendAgentHubSnapshot(snapFn())
		latest = &cp
	}

	s.mu.Lock()
	if s.closed || s.fellBack || s.hasBlockingLayerLocked() {
		s.mu.Unlock()
		return
	}
	if latest != nil {
		prevCost := s.agentHub.SubagentCost
		s.agentHub = *latest
		if prevCost != latest.SubagentCost {
			s.rebuildOmpEditor()
		}
	}
	st := s.ensureAgentHubStateLocked()
	st.open = true
	st.transcriptOpen = false
	st.transcriptID = ""
	st.transcriptDraft = ""
	st.transcriptNote = ""
	st.notice = ""
	st.keyDecoder = tern.KeyDecoder{}
	st.clockAt = time.Time{}
	s.reconcileAgentHubSelectionLocked()
	s.queue(tern.OpFocus(""))
	s.dirty = true
	_ = s.flushLocked()
	s.mu.Unlock()
}

func (s *session) ensureAgentHubStateLocked() *ompAgentHubState {
	if s.agentHubUI == nil {
		s.agentHubUI = &ompAgentHubState{
			tab:           "agents",
			scope:         "all",
			activityScope: "all",
			pending:       make(map[string]bool),
		}
	}
	if s.agentHubUI.pending == nil {
		s.agentHubUI.pending = make(map[string]bool)
	}
	return s.agentHubUI
}

// ompHudPillsNode builds the HUD pills row (agents + jobs segments inside
// `row[omp.hud.pill]`) for ompView's first dock slot
// (upstream: interactive-mode.ts:849-879, 924-975, 1720-1760).
// Returns (_, false) when neither agents nor running jobs are present so
// idle frames stay identical to baseline.
func (s *session) ompHudPillsNode() (tern.Node, bool) {
	running := s.agentHub.RunningCount
	inspectable := s.agentHub.InspectableCount
	runningJobs := s.progressHud.RunningJobs
	if running <= 0 && inspectable <= 0 && runningJobs <= 0 {
		return tern.Node{}, false
	}
	segs := make([]tern.Element, 0, 2)
	if running > 0 || inspectable > 0 {
		var (
			title    string
			aria     string
			children []tern.Element
		)
		if running > 0 {
			noun := "agents"
			if running == 1 {
				noun = "agent"
			}
			label := fmt.Sprintf("%d %s running", running, noun)
			title = label + "  /agents"
			aria = label
			children = []tern.Element{
				ui.Icon{Common: ui.Common{Key: "icon", Tone: ui.ToneAccent}, Name: ui.IconName("users")},
				ui.Text{
					Common: ui.Common{Key: "count"},
					Spans:  []ui.Span{{T: strconv.Itoa(running), S: "num"}},
					Wrap:   ui.WrapNone,
				},
			}
		} else {
			label := fmt.Sprintf("No agents running · %d in this session", inspectable)
			title = label + "  /agents"
			aria = label
			children = []tern.Element{
				ui.Icon{Common: ui.Common{Key: "icon", Tone: ui.ToneMuted}, Name: ui.IconName("users")},
			}
		}
		segs = append(segs, ui.Row{
			Common: ui.Common{
				Key:     "agents",
				Role:    "omp.hud.seg",
				Title:   title,
				Aria:    aria,
				Actions: &ui.Actions{Click: "agents.open"},
				OnClick: func(*tern.Action) { s.openAgentHub() },
			},
			Gap:      ui.GapXS,
			Align:    ui.AlignCenter,
			Children: children,
		})
	}
	if runningJobs > 0 {
		segs = append(segs, s.ompJobsHudSegment(runningJobs, s.progressHud.Jobs))
	}
	pill := ui.Row{
		Common:   ui.Common{Key: "running", Role: "omp.hud.pill"},
		Align:    ui.AlignCenter,
		Children: segs,
	}
	hud := ui.Row{
		Common:   ui.Common{Key: "hud", Role: "omp.hud"},
		Justify:  ui.JustifyEnd,
		Children: []tern.Element{pill},
	}
	return buildNode(hud), true
}

// ompJobsHudSegment builds omp's `jobs` HUD pill segment
// (upstream: interactive-mode.ts:924-963).
func (s *session) ompJobsHudSegment(running int, jobs []JobHudItem) ui.Row {
	noun := "jobs"
	if running == 1 {
		noun = "job"
	}
	label := fmt.Sprintf("%d %s running", running, noun)
	titleLines := make([]string, 0, 1+len(jobs))
	titleLines = append(titleLines, label+"  /jobs")
	var glanceJob *JobHudItem
	for i := range jobs {
		job := &jobs[i]
		summary := strings.TrimSpace(job.Summary)
		if summary != "" {
			if job.ID != "" {
				titleLines = append(titleLines, job.ID+"  "+summary)
			} else {
				titleLines = append(titleLines, summary)
			}
		}
		if strings.TrimSpace(job.GlanceText) != "" || len(job.SparkVals) > 0 {
			glanceJob = job
		}
	}
	var glanceText string
	var sparkVals []float64
	var sparkSummary string
	if glanceJob != nil {
		glanceText = strings.TrimSpace(glanceJob.GlanceText)
		sparkVals = glanceJob.SparkVals
		sparkSummary = glanceText
		if sparkSummary == "" {
			sparkSummary = strings.TrimSpace(glanceJob.Summary)
		}
	}
	children := []tern.Element{
		ui.Icon{Common: ui.Common{Key: "icon", Tone: ui.ToneWarning}, Name: ui.IconName("job")},
		ui.Text{
			Common: ui.Common{Key: "count"},
			Spans:  []ui.Span{{T: strconv.Itoa(running), S: "num"}},
			Wrap:   ui.WrapNone,
		},
	}
	if glanceText != "" {
		children = append(children, ui.Text{
			Common: ui.Common{Key: "glance", Role: "omp.hud.glance"},
			Spans:  []ui.Span{{T: glanceText, S: "num strong"}},
			Wrap:   ui.WrapNone,
		})
	}
	if len(sparkVals) > 0 {
		pts := make([]ui.Point, len(sparkVals))
		for i, v := range sparkVals {
			pts[i] = ui.Point{Value: v}
		}
		chart := ui.Chart{
			Common: ui.Common{Key: "spark", Role: "omp.hud.spark"},
			Kind:   ui.ChartSpark,
			Size:   ui.SizeSM,
			Token:  "accent",
			Series: pts,
		}
		if sparkSummary != "" {
			chart.Summary = ui.T(sparkSummary)
		}
		children = append(children, chart)
	}
	return ui.Row{
		Common: ui.Common{
			Key:     "jobs",
			Role:    "omp.hud.seg",
			Title:   strings.Join(titleLines, "\n"),
			Aria:    label,
			Actions: &ui.Actions{Click: "jobs.open"},
			OnClick: func(*tern.Action) { s.runCommand("/jobs") },
		},
		Gap:      ui.GapXS,
		Align:    ui.AlignCenter,
		Children: children,
	}
}

// ompHudProgressRow builds one right-docked progress HUD row matching omp's
// `describeHudRow` (packages/coding-agent/src/modes/progress-hud.ts:66-81).
func ompHudProgressRow(key, label string, tail tern.Element, bar *float64, hasBar bool) tern.Node {
	children := make([]tern.Element, 0, 3)
	children = append(children, tern.Node{
		Kind: tern.KindText,
		Props: map[string]any{
			"spans":    []any{map[string]any{"t": oneLineTrim(label, 120), "s": "dim"}},
			"wrap":     "none",
			"truncate": "end",
			"shrink":   1,
		},
	})
	if hasBar {
		pProps := map[string]any{"max": map[string]any{"w": "18ch"}}
		if bar != nil {
			v := math.Min(1, math.Max(0, *bar))
			pProps["value"] = v
		} else {
			pProps["value"] = nil
		}
		children = append(children, tern.Node{Kind: tern.KindProgress, Props: pProps})
	}
	children = append(children, tail)
	return tern.Node{
		Kind: tern.KindRow,
		Props: map[string]any{
			"key":     key,
			"role":    "omp.hud.progress",
			"justify": "end",
			"gap":     "sm",
			"align":   "center",
		},
		Children: children,
	}
}

// ompJudgeHudNode builds `col[omp.hud.judge]` for dock slot `progressHud`
// (upstream: progress-hud.ts:185-197).
func (s *session) ompJudgeHudNode() tern.Node {
	rows := make([]tern.Element, 0, len(s.progressHud.JudgeBatches))
	for _, b := range s.progressHud.JudgeBatches {
		total := b.Total
		if total < 1 {
			total = 1
		}
		spans := []any{map[string]any{"t": fmt.Sprintf("%d/%d", b.Done, b.Total), "s": "strong"}}
		if b.Cost > 0 {
			spans = append(spans, map[string]any{"t": " · " + ompFormatHubCost(b.Cost), "s": "dim"})
		}
		if b.Failed > 0 {
			spans = append(spans, map[string]any{"t": fmt.Sprintf(" · %d failed", b.Failed), "s": "warning"})
		}
		tail := tern.Node{Kind: tern.KindText, Props: map[string]any{"spans": spans, "wrap": "none"}}
		frac := float64(b.Done) / float64(total)
		rows = append(rows, ompHudProgressRow(b.ID, b.Intent, tail, &frac, true))
	}
	return tern.Node{
		Kind:     tern.KindCol,
		Props:    map[string]any{"key": "judge", "role": "omp.hud.judge"},
		Children: rows,
	}
}

// ompDownloadsHudNode builds `col[omp.hud.downloads]` for dock slot `progressHud`
// (upstream: progress-hud.ts:315-350).
func (s *session) ompDownloadsHudNode() tern.Node {
	rows := make([]tern.Element, 0, len(s.progressHud.Downloads))
	for _, d := range s.progressHud.Downloads {
		switch d.State {
		case "failed":
			errText := strings.TrimSpace(d.Error)
			if errText == "" {
				errText = "unknown error"
			}
			tail := tern.Node{
				Kind:  tern.KindText,
				Props: map[string]any{"spans": []any{map[string]any{"t": "failed: " + oneLineTrim(errText, 80), "s": "warning"}}, "wrap": "none"},
			}
			rows = append(rows, ompHudProgressRow(d.ID, d.Label, tail, nil, false))
		case "done":
			tail := tern.Node{
				Kind:  tern.KindText,
				Props: map[string]any{"spans": []any{map[string]any{"t": "ready", "s": "success"}}, "wrap": "none"},
			}
			rows = append(rows, ompHudProgressRow(d.ID, d.Label, tail, nil, false))
		default:
			if d.Total > 0 {
				label := d.Label
				if d.Detail != "" {
					label += " · " + d.Detail
				}
				var progressText string
				if d.Unit == "steps" {
					progressText = fmt.Sprintf("%d/%d", d.Loaded, d.Total)
				} else {
					progressText = ompFormatBytes(d.Loaded) + " / " + ompFormatBytes(d.Total)
				}
				tail := tern.Node{
					Kind:  tern.KindText,
					Props: map[string]any{"spans": []any{map[string]any{"t": progressText, "s": "strong"}}, "wrap": "none"},
				}
				frac := float64(d.Loaded) / float64(d.Total)
				rows = append(rows, ompHudProgressRow(d.ID, label, tail, &frac, true))
			} else {
				var step string
				switch {
				case d.Loaded > 0 && d.Unit == "steps":
					step = fmt.Sprintf("%d", d.Loaded)
				case d.Loaded > 0:
					step = ompFormatBytes(d.Loaded)
				case d.Detail != "":
					step = d.Detail
				case d.Unit == "steps":
					step = "working…"
				default:
					step = "downloading…"
				}
				tail := tern.Node{
					Kind:  tern.KindText,
					Props: map[string]any{"spans": []any{map[string]any{"t": step}}, "wrap": "none"},
				}
				rows = append(rows, ompHudProgressRow(d.ID, d.Label, tail, nil, false))
			}
		}
	}
	return tern.Node{
		Kind:     tern.KindCol,
		Props:    map[string]any{"key": "downloads", "role": "omp.hud.downloads"},
		Children: rows,
	}
}

// ompCompactionHudNode builds `col[omp.hud.compaction]` for dock slot `progressHud`
// (upstream: progress-hud.ts:140-150).
func (s *session) ompCompactionHudNode() tern.Node {
	c := s.progressHud.Compaction
	if !c.Active {
		s.compactionHudCached = false
		return tern.Node{
			Kind:  tern.KindCol,
			Props: map[string]any{"key": "compaction", "role": "omp.hud.compaction"},
		}
	}
	label := c.Label
	if label == "" {
		label = "Compacting context in the background"
	}
	if s.compactionHudCached && s.compactionHudStartedAtMs == c.StartedAtMs && s.compactionHudLabel == label {
		return s.compactionHudNode
	}
	age := int64(0)
	if c.StartedAtMs > 0 {
		if d := s.now().UnixMilli() - c.StartedAtMs; d > 0 {
			age = d
		}
	}
	clock := tern.Node{
		Kind:  tern.KindElapsed,
		Props: map[string]any{"age": age, "format": string(ui.ElapsedShort)},
	}
	node := tern.Node{
		Kind:     tern.KindCol,
		Props:    map[string]any{"key": "compaction", "role": "omp.hud.compaction"},
		Children: []tern.Element{ompHudProgressRow("run", label, clock, nil, true)},
	}
	s.compactionHudCached = true
	s.compactionHudStartedAtMs = c.StartedAtMs
	s.compactionHudLabel = label
	s.compactionHudNode = node
	return node
}

func ompFormatBytes(n int64) string {
	if n < 0 {
		n = 0
	}
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case n < kb:
		return fmt.Sprintf("%dB", n)
	case n < mb:
		return fmt.Sprintf("%.1fKB", float64(n)/float64(kb))
	case n < gb:
		return fmt.Sprintf("%.1fMB", float64(n)/float64(mb))
	default:
		return fmt.Sprintf("%.1fGB", float64(n)/float64(gb))
	}
}

func (s *session) ompAgentHubLayerNode() (tern.Node, bool) {
	st := s.agentHubUI
	if st == nil || !st.open {
		return tern.Node{}, false
	}
	if st.transcriptOpen && st.transcriptID != "" {
		return s.ompAgentTranscriptOverlayNodeLocked(st), true
	}
	return s.ompAgentHubPickerNodeLocked(st), true
}

func (s *session) ompAgentHubPickerNodeLocked(st *ompAgentHubState) tern.Node {
	s.reconcileAgentHubSelectionLocked()
	isActivity := st.tab == "activity"
	tabs := []ui.Tab{
		{ID: "agents", Label: ui.T("Agents")},
		{ID: "activity", Label: ui.T("Activity")},
	}
	picker := ui.Picker{
		PickerCommon: ui.PickerCommon{
			Key: "agent-hub",
		},
		Title:    ui.T("Agents"),
		Subtitle: ui.T(ompAgentHubSubtitle(s.agentHub)),
		Icon:     ui.IconName("users"),
		Size:     ui.SizeLG,
		Tabs:     tabs,
		Tab:      st.tab,
		Focus:    ui.FocusList,
		State:    ui.StateReady,
	}

	if isActivity {
		rows := s.filteredAgentActivityLocked(st)
		agentPrio := 2.0
		timePrio := 1.0
		picker.Noun = "events"
		picker.Layout = ui.LayoutRows
		picker.Preview = ui.PaneNone
		picker.Placeholder = "Search activity…"
		picker.Empty = ui.T("No agent activity recorded yet")
		picker.Scopes = ompActivityScopes(s.agentHub.Activity)
		picker.Scope = st.activityScope
		picker.Columns = []ui.PickerColumn{
			{ID: "agent", Head: "Agent", Format: ui.FormatText, Priority: &agentPrio},
			{ID: "at", Head: "Time", Format: ui.FormatDim, Priority: &timePrio},
		}
		items := make([]ui.PickerItem, 0, len(rows))
		for _, act := range rows {
			items = append(items, ompActivityPickerItem(act))
		}
		picker.Items = items
		picker.Selected = st.selectedActID
		total := len(s.agentHub.Activity)
		picker.Total = &total
		if st.filtering || st.query != "" {
			q := st.query
			picker.Query = &q
		}
		var openDisabled any
		if st.selectedActID == "" {
			openDisabled = true
		}
		picker.Actions = []ui.PickerAction{
			{ID: "filter", Label: "Filter", Keys: []string{"/"}},
			{ID: "close", Label: "Close", Keys: []string{"esc"}, End: true},
			{ID: "open", Label: "Open transcript", Keys: []string{"enter"}, Primary: true, Disabled: openDisabled},
		}
	} else {
		rows, depths, hasChildren := s.filteredAgentsLocked(st)
		costPrio := 6.0
		timePrio := 5.0
		reqPrio := 2.0
		toolsPrio := 1.0
		tokPrio := 3.0
		ctxPrio := 4.0
		picker.Noun = "agents"
		if st.treeMode {
			picker.Layout = ui.LayoutTree
		} else {
			picker.Layout = ui.LayoutRows
		}
		picker.Preview = ui.PaneSide
		picker.Placeholder = "Filter agents…"
		picker.Empty = ui.T("No agents in this session. Finished, parked and killed subagents stay with the session that created them.")
		picker.Scopes = ompAgentStatusScopes(s.agentHub.Agents)
		picker.Scope = st.scope
		picker.Columns = []ui.PickerColumn{
			{ID: "cost", Head: "Cost", Format: ui.FormatPrice, Priority: &costPrio},
			{ID: "time", Head: "Time", Format: ui.FormatElapsed, Priority: &timePrio},
			{ID: "req", Head: "Req", Format: ui.FormatNum, Priority: &reqPrio},
			{ID: "tools", Head: "Tools", Format: ui.FormatNum, Priority: &toolsPrio},
			{ID: "tok", Head: "Tok", Format: ui.FormatNum, Priority: &tokPrio},
			{ID: "ctx", Head: "Ctx", Format: ui.FormatBar, Priority: &ctxPrio},
		}
		now := s.now()
		if st.clockAt.IsZero() || now.Sub(st.clockAt) >= pickerClockReuse {
			st.clockAt = now
			st.clockNowMs = now.UnixMilli()
		}
		nowMs := st.clockNowMs
		items := make([]ui.PickerItem, 0, len(rows))
		var selected *AgentHubItem
		for i := range rows {
			a := rows[i]
			items = append(items, ompAgentPickerItem(a, st.treeMode, depths[a.ID], hasChildren[a.ID], nowMs))
			if a.ID == st.selectedID {
				cp := a
				selected = &cp
			}
		}
		picker.Items = items
		picker.Selected = st.selectedID
		total := len(s.agentHub.Agents)
		picker.Total = &total
		if st.filtering || st.query != "" {
			q := st.query
			picker.Query = &q
		}
		picker.Children = s.ompAgentPreviewChildrenLocked(selected, st)

		var openDisabled any
		if selected == nil {
			openDisabled = true
		}
		selectedPending := selected != nil && st.pending[selected.ID]
		var reviveDisabled any = "Only parked or killed agents can be revived"
		if selected != nil && !selectedPending && selected.CanRevive && (selected.Status == "parked" || selected.Status == "aborted") {
			reviveDisabled = nil
		}
		var killDisabled any = true
		if selected != nil && !selectedPending && (selected.Status == "running" || selected.Status == "idle") {
			killDisabled = nil
		}
		picker.Actions = []ui.PickerAction{
			{ID: "revive", Label: "Revive", Keys: []string{"r"}, Disabled: reviveDisabled},
			{ID: "kill", Label: "Kill", Keys: []string{"x"}, Danger: true, Disabled: killDisabled},
			{ID: "view", Label: "By parent", Keys: []string{"t"}, On: st.treeMode},
			{ID: "filter", Label: "Filter", Keys: []string{"/"}},
			{ID: "close", Label: "Close", Keys: []string{"esc"}, End: true},
			{ID: "open", Label: "Open transcript", Keys: []string{"enter"}, Primary: true, Disabled: openDisabled},
		}
	}

	node := buildNode(picker)
	node.Handlers = s.ompAgentHubPickerHandlers()
	return node
}

func (s *session) ompAgentHubPickerHandlers() tern.Handlers {
	return tern.Handlers{
		OnSelect: func(ev *tern.Select) {
			s.mu.Lock()
			if s.agentHubUI == nil || !s.agentHubUI.open {
				s.mu.Unlock()
				return
			}
			if s.agentHubUI.tab == "activity" {
				s.agentHubUI.selectedActID = ev.Item
			} else {
				s.agentHubUI.selectedID = ev.Item
				s.agentHubUI.notice = ""
			}
			s.dirty = true
			_ = s.flushLocked()
			s.mu.Unlock()
		},
		OnActivate: func(ev *tern.Activate) {
			go s.activateAgentHubItem(ev.Item)
		},
		OnEdit: func(ev *tern.Edit) {
			s.mu.Lock()
			if s.agentHubUI == nil || !s.agentHubUI.open {
				s.mu.Unlock()
				return
			}
			s.agentHubUI.filtering = true
			s.agentHubUI.query = applyUTF16Edit(s.agentHubUI.query, ev.From, ev.To, ev.Text)
			s.reconcileAgentHubSelectionLocked()
			s.dirty = true
			_ = s.flushLocked()
			s.mu.Unlock()
		},
		OnAction: map[string]func(*tern.Action){
			"tab": func(ev *tern.Action) {
				v, _ := ev.Value.(string)
				if v != "agents" && v != "activity" {
					return
				}
				s.mu.Lock()
				if s.agentHubUI != nil && s.agentHubUI.open {
					s.agentHubUI.tab = v
					s.agentHubUI.notice = ""
					s.reconcileAgentHubSelectionLocked()
					s.dirty = true
					_ = s.flushLocked()
				}
				s.mu.Unlock()
			},
			"scope": func(ev *tern.Action) {
				v, _ := ev.Value.(string)
				s.mu.Lock()
				if s.agentHubUI != nil && s.agentHubUI.open {
					if s.agentHubUI.tab == "activity" {
						if v == "all" || v == "errors" || v == "responses" || v == "tools" {
							s.agentHubUI.activityScope = v
						}
					} else {
						if v == "all" || v == "running" || v == "done" || v == "parked" {
							s.agentHubUI.scope = v
						}
					}
					s.reconcileAgentHubSelectionLocked()
					s.dirty = true
					_ = s.flushLocked()
				}
				s.mu.Unlock()
			},
			"view": func(*tern.Action) {
				s.mu.Lock()
				if s.agentHubUI != nil && s.agentHubUI.open {
					s.agentHubUI.treeMode = !s.agentHubUI.treeMode
					s.reconcileAgentHubSelectionLocked()
					s.dirty = true
					_ = s.flushLocked()
				}
				s.mu.Unlock()
			},
			"filter": func(*tern.Action) {
				s.mu.Lock()
				if s.agentHubUI != nil && s.agentHubUI.open {
					s.agentHubUI.filtering = true
					s.dirty = true
					_ = s.flushLocked()
				}
				s.mu.Unlock()
			},
			"clear": func(*tern.Action) {
				s.mu.Lock()
				if s.agentHubUI != nil && s.agentHubUI.open {
					s.agentHubUI.query = ""
					s.agentHubUI.filtering = false
					s.reconcileAgentHubSelectionLocked()
					s.dirty = true
					_ = s.flushLocked()
				}
				s.mu.Unlock()
			},
			"open": func(*tern.Action) {
				go s.activateAgentHubItem("")
			},
			"kill": func(*tern.Action) {
				go s.runAgentHubKill()
			},
			"revive": func(*tern.Action) {
				go s.runAgentHubRevive()
			},
			"close": func(*tern.Action) {
				s.closeAgentHub()
			},
			"cancel": func(*tern.Action) {
				s.closeAgentHub()
			},
		},
	}
}

func (s *session) closeAgentHub() {
	s.mu.Lock()
	s.closeAgentHubLocked(true)
	_ = s.flushLocked()
	s.mu.Unlock()
}

func (s *session) closeAgentHubLocked(restoreEditorFocus bool) {
	if s.agentHubUI == nil || !s.agentHubUI.open {
		return
	}
	s.agentHubUI.open = false
	s.agentHubUI.transcriptOpen = false
	s.agentHubUI.transcriptID = ""
	s.agentHubUI.transcriptDraft = ""
	s.agentHubUI.transcriptNote = ""
	s.agentHubUI.filtering = false
	s.agentHubUI.notice = ""
	s.agentHubUI.keyDecoder = tern.KeyDecoder{}
	s.dirty = true
	if restoreEditorFocus && s.hasEditor {
		for _, e := range s.dock {
			if _, ok := e.node.(fe.Editor); ok {
				s.queue(tern.OpFocus(s.editorInputID(e)))
				break
			}
		}
	}
}

func (s *session) activateAgentHubItem(itemID string) {
	s.mu.Lock()
	st := s.agentHubUI
	if st == nil || !st.open {
		s.mu.Unlock()
		return
	}
	var agentID string
	if st.tab == "activity" {
		target := itemID
		if target == "" {
			target = st.selectedActID
		}
		for _, act := range s.agentHub.Activity {
			if act.ID == target {
				agentID = act.AgentID
				break
			}
		}
	} else {
		agentID = itemID
		if agentID == "" {
			agentID = st.selectedID
		}
	}
	if agentID == "" {
		s.mu.Unlock()
		return
	}
	st.selectedID = agentID
	loadFn := s.loadAgentTranscriptFn
	s.mu.Unlock()

	var entries []AgentHubTranscriptEntry
	if loadFn != nil {
		entries = loadFn(agentID)
	}

	s.mu.Lock()
	if s.closed || s.fellBack || s.agentHubUI == nil || !s.agentHubUI.open {
		s.mu.Unlock()
		return
	}
	s.openAgentTranscriptLocked(agentID, entries)
	_ = s.flushLocked()
	s.mu.Unlock()
}

func (s *session) openAgentTranscriptLocked(agentID string, entries []AgentHubTranscriptEntry) {
	st := s.ensureAgentHubStateLocked()
	st.transcriptOpen = true
	st.transcriptID = agentID
	st.transcript = entries
	st.transcriptDraft = ""
	st.transcriptNote = ""
	if item := s.findAgentHubItemLocked(agentID); item != nil {
		st.transcriptStamp = agentTranscriptStamp{
			status:       item.Status,
			tools:        item.Tools,
			requests:     item.Requests,
			lastActiveMs: item.LastActiveMs,
		}
		if agentHubCanMessage(item, s.sendAgentMessageFn) {
			s.queue(tern.OpFocus(agentTranscriptInputID))
		} else {
			s.queue(tern.OpFocus(""))
		}
	} else {
		st.transcriptStamp = agentTranscriptStamp{}
		s.queue(tern.OpFocus(""))
	}
	s.dirty = true
}

func (s *session) runAgentHubKill() {
	s.mu.Lock()
	st := s.agentHubUI
	if st == nil || !st.open || st.selectedID == "" {
		s.mu.Unlock()
		return
	}
	agentID := st.selectedID
	item := s.findAgentHubItemLocked(agentID)
	if item == nil || (item.Status != "running" && item.Status != "idle") {
		s.mu.Unlock()
		return
	}
	if st.pending[agentID] {
		s.mu.Unlock()
		return
	}
	killFn := s.killAgentFn
	if killFn == nil {
		s.mu.Unlock()
		return
	}
	st.pending[agentID] = true
	s.dirty = true
	_ = s.flushLocked()
	s.mu.Unlock()

	var msg string
	var err error
	defer func() {
		s.mu.Lock()
		if s.agentHubUI != nil {
			delete(s.agentHubUI.pending, agentID)
			if s.agentHubUI.open {
				if err != nil {
					s.agentHubUI.notice = err.Error()
					s.agentHubUI.noticeTone = ui.ToneError
				} else if msg != "" {
					s.agentHubUI.notice = msg
					s.agentHubUI.noticeTone = ui.ToneMuted
				}
			}
			s.dirty = true
			_ = s.flushLocked()
		}
		s.mu.Unlock()
	}()
	msg, err = killFn(agentID)
}

func (s *session) runAgentHubRevive() {
	s.mu.Lock()
	st := s.agentHubUI
	if st == nil || !st.open || st.selectedID == "" {
		s.mu.Unlock()
		return
	}
	agentID := st.selectedID
	item := s.findAgentHubItemLocked(agentID)
	if item == nil || !item.CanRevive || (item.Status != "parked" && item.Status != "aborted") {
		s.mu.Unlock()
		return
	}
	if st.pending[agentID] {
		s.mu.Unlock()
		return
	}
	reviveFn := s.reviveAgentFn
	if reviveFn == nil {
		s.mu.Unlock()
		return
	}
	st.pending[agentID] = true
	s.dirty = true
	_ = s.flushLocked()
	s.mu.Unlock()

	var msg string
	var err error
	defer func() {
		s.mu.Lock()
		if s.agentHubUI != nil {
			delete(s.agentHubUI.pending, agentID)
			if s.agentHubUI.open {
				if err != nil {
					s.agentHubUI.notice = err.Error()
					s.agentHubUI.noticeTone = ui.ToneError
				} else if msg != "" {
					s.agentHubUI.notice = msg
					s.agentHubUI.noticeTone = ui.ToneSuccess
				}
			}
			s.dirty = true
			_ = s.flushLocked()
		}
		s.mu.Unlock()
	}()
	msg, err = reviveFn(agentID)
}

func (s *session) startSendAgentTranscriptMessageLocked(body string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}
	st := s.agentHubUI
	if st == nil || !st.open || !st.transcriptOpen || st.transcriptID == "" {
		return
	}
	agentID := st.transcriptID
	item := s.findAgentHubItemLocked(agentID)
	if !agentHubCanMessage(item, s.sendAgentMessageFn) || st.pending[agentID] {
		return
	}
	sendFn := s.sendAgentMessageFn
	loadFn := s.loadAgentTranscriptFn
	st.pending[agentID] = true
	st.transcriptDraft = ""
	st.transcriptNote = ""
	s.dirty = true
	_ = s.flushLocked()
	go s.runSendAgentTranscriptMessage(agentID, body, sendFn, loadFn)
}

func (s *session) runSendAgentTranscriptMessage(agentID, body string, sendFn func(string, string) error, loadFn func(string) []AgentHubTranscriptEntry) {
	var err error
	var entries []AgentHubTranscriptEntry
	defer func() {
		s.mu.Lock()
		if s.agentHubUI != nil {
			delete(s.agentHubUI.pending, agentID)
			if s.agentHubUI.transcriptOpen && s.agentHubUI.transcriptID == agentID {
				if err != nil {
					s.agentHubUI.transcriptNote = err.Error()
					if s.agentHubUI.transcriptDraft == "" {
						s.agentHubUI.transcriptDraft = body
					}
				} else {
					s.agentHubUI.transcriptNote = ""
					if entries != nil {
						s.agentHubUI.transcript = entries
					}
				}
			}
			s.dirty = true
			_ = s.flushLocked()
		}
		s.mu.Unlock()
	}()
	err = sendFn(agentID, body)
	if err == nil && loadFn != nil {
		entries = loadFn(agentID)
	}
}

func (s *session) refreshAgentTranscript() {
	s.mu.Lock()
	st := s.agentHubUI
	if st == nil || !st.open || !st.transcriptOpen || st.transcriptID == "" {
		s.mu.Unlock()
		return
	}
	agentID := st.transcriptID
	loadFn := s.loadAgentTranscriptFn
	s.mu.Unlock()

	if loadFn == nil {
		return
	}
	entries := loadFn(agentID)
	s.mu.Lock()
	if s.agentHubUI != nil && s.agentHubUI.transcriptOpen && s.agentHubUI.transcriptID == agentID {
		s.agentHubUI.transcript = entries
		s.dirty = true
		_ = s.flushLocked()
	}
	s.mu.Unlock()
}

// handleAgentHubKeyLocked handles terminal keystrokes while the native Agent Hub
// or Agent Transcript Viewer overlay is open so keys never leak to PiG's editor.
// Caller holds s.mu.
func (s *session) handleAgentHubKeyLocked(keys string) bool {
	st := s.agentHubUI
	if st == nil || !st.open {
		return false
	}
	if s.hasBlockingLayerLocked() {
		return false
	}
	decoded := st.keyDecoder.Feed([]byte(keys))
	decoded = append(decoded, st.keyDecoder.Flush()...)
	if len(decoded) == 0 {
		// Consume key releases and ignored sequences so they do not leak to PiG.
		return true
	}
	for _, k := range decoded {
		if k.Is("ctrl+c") || k.Is("ctrl+d") || k.Is("ctrl+z") {
			return false
		}
	}
	for _, k := range decoded {
		s.applyAgentHubKeyLocked(st, k)
		if !st.open {
			break
		}
	}
	return true
}

func (s *session) applyAgentHubKeyLocked(st *ompAgentHubState, k tern.Key) {
	// Transcript viewer keybindings.
	if st.transcriptOpen {
		switch {
		case k.Is("escape"): // Esc -> back to Agent Hub
			st.transcriptOpen = false
			st.transcriptID = ""
			st.transcriptDraft = ""
			st.transcriptNote = ""
			s.queue(tern.OpFocus(""))
			s.dirty = true
			_ = s.flushLocked()
		case k.Is("enter"):
			s.startSendAgentTranscriptMessageLocked(st.transcriptDraft)
		case k.Name == "backspace" && !k.Alt && !k.Meta:
			item := s.findAgentHubItemLocked(st.transcriptID)
			if agentHubCanMessage(item, s.sendAgentMessageFn) && st.transcriptDraft != "" {
				_, size := utf8.DecodeLastRuneInString(st.transcriptDraft)
				st.transcriptDraft = st.transcriptDraft[:len(st.transcriptDraft)-size]
				s.dirty = true
				_ = s.flushLocked()
			}
		case k.Name == "paste" && k.Text != "":
			item := s.findAgentHubItemLocked(st.transcriptID)
			if agentHubCanMessage(item, s.sendAgentMessageFn) {
				st.transcriptDraft += k.Text
				s.dirty = true
				_ = s.flushLocked()
			}
		case k.Text != "" && !k.Ctrl && !k.Alt && !k.Meta:
			item := s.findAgentHubItemLocked(st.transcriptID)
			if agentHubCanMessage(item, s.sendAgentMessageFn) {
				st.transcriptDraft += k.Text
				s.dirty = true
				_ = s.flushLocked()
			}
		}
		return
	}

	// Filter input mode inside Agent Hub.
	if st.filtering {
		switch {
		case k.Is("escape"):
			st.filtering = false
			st.query = ""
			s.reconcileAgentHubSelectionLocked()
			s.dirty = true
			_ = s.flushLocked()
		case k.Is("enter"):
			st.filtering = false
			s.dirty = true
			_ = s.flushLocked()
		case k.Name == "backspace" && !k.Alt && !k.Meta:
			if st.query != "" {
				_, size := utf8.DecodeLastRuneInString(st.query)
				st.query = st.query[:len(st.query)-size]
				s.reconcileAgentHubSelectionLocked()
				s.dirty = true
				_ = s.flushLocked()
			}
		case k.Name == "paste" && k.Text != "":
			st.query += k.Text
			s.reconcileAgentHubSelectionLocked()
			s.dirty = true
			_ = s.flushLocked()
		case k.Text != "" && !k.Ctrl && !k.Alt && !k.Meta:
			st.query += k.Text
			s.reconcileAgentHubSelectionLocked()
			s.dirty = true
			_ = s.flushLocked()
		}
		return
	}

	switch {
	case k.Is("escape"): // Esc closes Agent Hub
		s.closeAgentHubLocked(true)
		_ = s.flushLocked()
	case k.Is("tab"): // Tab switches between Agents and Activity
		if st.tab == "activity" {
			st.tab = "agents"
		} else {
			st.tab = "activity"
		}
		st.notice = ""
		s.reconcileAgentHubSelectionLocked()
		s.dirty = true
		_ = s.flushLocked()
	case k.Text == "1" && !k.Ctrl && !k.Alt && !k.Meta:
		st.tab = "agents"
		st.notice = ""
		s.reconcileAgentHubSelectionLocked()
		s.dirty = true
		_ = s.flushLocked()
	case k.Text == "2" && !k.Ctrl && !k.Alt && !k.Meta:
		st.tab = "activity"
		st.notice = ""
		s.reconcileAgentHubSelectionLocked()
		s.dirty = true
		_ = s.flushLocked()
	case k.Is("up") || (k.Text == "k" && !k.Ctrl && !k.Alt && !k.Meta):
		s.moveAgentHubSelectionLocked(-1)
		s.dirty = true
		_ = s.flushLocked()
	case k.Is("down") || (k.Text == "j" && !k.Ctrl && !k.Alt && !k.Meta):
		s.moveAgentHubSelectionLocked(1)
		s.dirty = true
		_ = s.flushLocked()
	case k.Is("enter"): // Enter opens transcript
		go s.activateAgentHubItem("")
	case k.Text == "t" && !k.Ctrl && !k.Alt && !k.Meta:
		if st.tab == "agents" {
			st.treeMode = !st.treeMode
			s.reconcileAgentHubSelectionLocked()
			s.dirty = true
			_ = s.flushLocked()
		}
	case k.Text == "/" && !k.Ctrl && !k.Alt && !k.Meta:
		st.filtering = true
		s.dirty = true
		_ = s.flushLocked()
	case k.Text == "x" && !k.Ctrl && !k.Alt && !k.Meta:
		if st.tab == "agents" {
			go s.runAgentHubKill()
		}
	case k.Text == "r" && !k.Ctrl && !k.Alt && !k.Meta:
		if st.tab == "agents" {
			go s.runAgentHubRevive()
		}
	}
}

func (s *session) moveAgentHubSelectionLocked(delta int) {
	st := s.agentHubUI
	if st == nil {
		return
	}
	if st.tab == "activity" {
		rows := s.filteredAgentActivityLocked(st)
		if len(rows) == 0 {
			st.selectedActID = ""
			return
		}
		idx := slices.IndexFunc(rows, func(a AgentHubActivity) bool { return a.ID == st.selectedActID })
		if idx < 0 {
			idx = 0
		} else {
			idx = min(max(idx+delta, 0), len(rows)-1)
		}
		st.selectedActID = rows[idx].ID
		return
	}
	rows, _, _ := s.filteredAgentsLocked(st)
	if len(rows) == 0 {
		st.selectedID = ""
		return
	}
	idx := slices.IndexFunc(rows, func(a AgentHubItem) bool { return a.ID == st.selectedID })
	if idx < 0 {
		idx = 0
	} else {
		idx = min(max(idx+delta, 0), len(rows)-1)
	}
	st.selectedID = rows[idx].ID
	st.notice = ""
}

func (s *session) reconcileAgentHubSelectionLocked() {
	st := s.agentHubUI
	if st == nil {
		return
	}
	if st.tab == "" {
		st.tab = "agents"
	}
	if st.scope == "" {
		st.scope = "all"
	}
	if st.activityScope == "" {
		st.activityScope = "all"
	}
	rows, _, _ := s.filteredAgentsLocked(st)
	if len(rows) == 0 {
		st.selectedID = ""
	} else if !slices.ContainsFunc(rows, func(a AgentHubItem) bool { return a.ID == st.selectedID }) {
		st.selectedID = rows[0].ID
	}
	actRows := s.filteredAgentActivityLocked(st)
	if len(actRows) == 0 {
		st.selectedActID = ""
	} else if !slices.ContainsFunc(actRows, func(a AgentHubActivity) bool { return a.ID == st.selectedActID }) {
		st.selectedActID = actRows[0].ID
	}
}

func (s *session) filteredAgentsLocked(st *ompAgentHubState) ([]AgentHubItem, map[string]int, map[string]bool) {
	q := strings.ToLower(strings.TrimSpace(st.query))
	filtered := make([]AgentHubItem, 0, len(s.agentHub.Agents))
	for _, a := range s.agentHub.Agents {
		if !ompAgentMatchesScope(a, st.scope) {
			continue
		}
		if q != "" && !ompAgentMatchesQuery(a, q) {
			continue
		}
		filtered = append(filtered, a)
	}
	depths := make(map[string]int, len(filtered))
	hasChildren := make(map[string]bool, len(filtered))
	if !st.treeMode {
		return filtered, depths, hasChildren
	}
	byID := make(map[string]AgentHubItem, len(filtered))
	childrenByParent := make(map[string][]AgentHubItem, len(filtered))
	for _, a := range filtered {
		byID[a.ID] = a
	}
	var roots []AgentHubItem
	for _, a := range filtered {
		if a.ParentID != "" {
			if _, parentPresent := byID[a.ParentID]; parentPresent && a.ParentID != a.ID {
				childrenByParent[a.ParentID] = append(childrenByParent[a.ParentID], a)
				hasChildren[a.ParentID] = true
				continue
			}
		}
		roots = append(roots, a)
	}
	ordered := make([]AgentHubItem, 0, len(filtered))
	visited := make(map[string]bool, len(filtered))
	var walk func(items []AgentHubItem, depth int)
	walk = func(items []AgentHubItem, depth int) {
		for _, a := range items {
			if visited[a.ID] {
				continue
			}
			visited[a.ID] = true
			depths[a.ID] = depth
			ordered = append(ordered, a)
			if kids := childrenByParent[a.ID]; len(kids) > 0 {
				walk(kids, depth+1)
			}
		}
	}
	walk(roots, 0)
	for _, a := range filtered {
		if !visited[a.ID] {
			depths[a.ID] = 0
			ordered = append(ordered, a)
		}
	}
	return ordered, depths, hasChildren
}

func (s *session) filteredAgentActivityLocked(st *ompAgentHubState) []AgentHubActivity {
	q := strings.ToLower(strings.TrimSpace(st.query))
	out := make([]AgentHubActivity, 0, len(s.agentHub.Activity))
	for _, act := range s.agentHub.Activity {
		switch st.activityScope {
		case "errors":
			if act.Status != "error" {
				continue
			}
		case "responses":
			if act.Kind != "response" {
				continue
			}
		case "tools":
			if act.Kind != "tool" {
				continue
			}
		}
		if q != "" {
			hay := strings.ToLower(act.AgentID + " " + act.Title + " " + act.Detail + " " + act.Kind)
			if !strings.Contains(hay, q) {
				continue
			}
		}
		out = append(out, act)
	}
	return out
}

func ompAgentMatchesScope(a AgentHubItem, scope string) bool {
	switch scope {
	case "running":
		return a.Status == "running"
	case "done":
		return a.Status == "idle"
	case "parked":
		return a.Status == "parked"
	default:
		return true
	}
}

func ompAgentMatchesQuery(a AgentHubItem, q string) bool {
	hay := strings.ToLower(a.ID + " " + a.DisplayName + " " + a.AgentType + " " + a.Task + " " + a.Activity + " " + a.Model + " " + a.ParentID)
	return strings.Contains(hay, q)
}

func ompAgentStatusScopes(agents []AgentHubItem) []ui.Scope {
	all := len(agents)
	var running, done, parked int
	for _, a := range agents {
		switch a.Status {
		case "running":
			running++
		case "idle":
			done++
		case "parked":
			parked++
		}
	}
	return []ui.Scope{
		{ID: "all", Label: ui.T("All"), Icon: ui.IconName("list"), Count: &all},
		{ID: "running", Label: ui.T("Running"), Icon: ui.IconName("play"), Count: &running},
		{ID: "done", Label: ui.T("Done"), Icon: ui.IconName("check"), Count: &done},
		{ID: "parked", Label: ui.T("Parked"), Icon: ui.IconName("pause"), Count: &parked},
	}
}

func ompActivityScopes(acts []AgentHubActivity) []ui.Scope {
	all := len(acts)
	var errs, responses, tools int
	for _, act := range acts {
		if act.Status == "error" {
			errs++
		}
		if act.Kind == "response" {
			responses++
		}
		if act.Kind == "tool" {
			tools++
		}
	}
	return []ui.Scope{
		{ID: "all", Label: ui.T("All"), Icon: ui.IconName("list"), Group: "Show", Count: &all},
		{ID: "errors", Label: ui.T("Errors"), Icon: ui.IconName("x-circle"), Group: "Show", Count: &errs},
		{ID: "responses", Label: ui.T("Responses"), Icon: ui.IconName("message"), Group: "Show", Count: &responses},
		{ID: "tools", Label: ui.T("Tools"), Icon: ui.IconName("wrench"), Group: "Show", Count: &tools},
	}
}

func ompAgentStatusTone(status string) ui.Tone {
	switch status {
	case "running":
		return ui.ToneAccent
	case "idle":
		return ui.ToneSuccess
	case "aborted":
		return ui.ToneError
	default:
		return ui.ToneMuted
	}
}

func ompAgentPickerItem(a AgentHubItem, treeMode bool, depth int, hasKids bool, nowMs int64) ui.PickerItem {
	label := a.DisplayName
	if label == "" {
		label = a.ID
	}
	detail := oneLineTrim(a.Activity, 90)
	if detail == "" {
		detail = oneLineTrim(a.Task, 90)
	}
	var badges []ui.Chip
	if a.AgentType != "" {
		badges = append(badges, ui.Chip{Text: a.AgentType})
	}
	if !treeMode && a.ParentID != "" && a.ParentID != "main" {
		badges = append(badges, ui.Chip{Text: "↳ " + a.ParentID, Tone: ui.ToneMuted, Title: "Spawned by"})
	}
	if len(badges) > 3 {
		badges = badges[:3]
	}
	var chips []ui.RoleChip
	if a.Model != "" {
		chips = append(chips, ui.RoleChip{Text: a.Model})
	}
	durationMs := a.DurationMs
	if a.Status == "running" {
		if durationMs > 0 {
			if a.LastActiveMs > 0 && nowMs > a.LastActiveMs {
				durationMs += nowMs - a.LastActiveMs
			}
		} else if a.StartedAtMs > 0 && nowMs > a.StartedAtMs {
			durationMs = nowMs - a.StartedAtMs
		}
	}
	var timeFact any
	if a.Status == "running" {
		timeFact = durationMs
	} else {
		timeFact = ompFormatHubDuration(durationMs)
	}
	facts := map[string]any{
		"cost":  ompFormatHubCost(a.Cost),
		"time":  timeFact,
		"req":   a.Requests,
		"tools": a.Tools,
		"tok":   a.Tokens,
	}
	var title string
	if a.ContextWindow > 0 {
		ratio := math.Max(0, math.Min(1, float64(a.ContextTokens)/float64(a.ContextWindow)))
		facts["ctx"] = ratio
		title = fmt.Sprintf("Context %s / %s", ompFormatNumber(a.ContextTokens), ompFormatNumber(a.ContextWindow))
	}
	item := ui.PickerItem{
		ID:     a.ID,
		Label:  ui.T(label),
		Mono:   true,
		Dot:    ompAgentStatusTone(a.Status),
		Detail: ui.T(detail),
		Badges: badges,
		Chips:  chips,
		Facts:  facts,
		Title:  title,
	}
	if treeMode {
		item.Depth = depth
		if hasKids {
			open := true
			item.Open = &open
		}
	}
	return item
}

func ompActivityPickerItem(act AgentHubActivity) ui.PickerItem {
	icon := ui.IconName("activity")
	switch act.Kind {
	case "tool":
		icon = ui.IconName("wrench")
	case "response":
		icon = ui.IconName("message")
	case "irc":
		icon = ui.IconName("send")
	}
	var tone ui.Tone
	switch act.Status {
	case "error":
		tone = ui.ToneError
	case "aborted":
		tone = ui.ToneWarning
	}
	title := oneLineTrim(act.Title, 80)
	if title == "" {
		title = act.Kind
	}
	return ui.PickerItem{
		ID:     act.ID,
		Label:  ui.T(title),
		Detail: ui.T(oneLineTrim(act.Detail, 90)),
		Icon:   icon,
		Tone:   tone,
		Facts: map[string]any{
			"agent": act.AgentID,
			"at":    ompFormatActivityClock(act.TimestampMs),
		},
	}
}

func (s *session) ompAgentPreviewChildrenLocked(selected *AgentHubItem, st *ompAgentHubState) []tern.Element {
	if selected == nil {
		return nil
	}
	name := selected.DisplayName
	if name == "" {
		name = selected.ID
	}
	out := []tern.Element{
		ui.Row{
			Common: ui.Common{Key: "title", Role: "omp.hub.title"},
			Gap:    ui.GapSM,
			Align:  ui.AlignCenter,
			Children: []tern.Element{
				ui.Text{
					Common:   ui.Common{Key: "name", Role: "omp.picker.title"},
					Text:     name,
					Truncate: ui.TruncateEnd,
				},
				ui.Badge{
					Common: ui.Common{Key: "status", Tone: ompAgentStatusTone(selected.Status)},
					Text:   selected.Status,
				},
			},
		},
	}
	if st.notice != "" {
		tone := st.noticeTone
		if tone == "" {
			tone = ui.ToneError
		}
		out = append(out, ui.Text{
			Common: ui.Common{Key: "notice", Tone: tone},
			Text:   st.notice,
			Wrap:   ui.WrapWord,
		})
	}

	var pairs []ui.Pair
	if selected.Model != "" {
		modelSpans := []ui.Span{{T: selected.Model, S: "mono"}}
		if selected.ThinkingLevel != "" {
			modelSpans = append(modelSpans,
				ui.Span{T: " · ", S: "dim"},
				ui.Span{T: selected.ThinkingLevel, S: ompThinkingLevelToken(selected.ThinkingLevel)},
			)
		}
		pairs = append(pairs, ui.Pair{K: ui.T("Model"), V: ui.Spans(modelSpans...)})
	}
	if selected.ModelRole != "" {
		pairs = append(pairs, ui.Pair{K: ui.T("Role"), V: ui.T(selected.ModelRole)})
	} else if selected.AgentType != "" {
		pairs = append(pairs, ui.Pair{K: ui.T("Role"), V: ui.T(selected.AgentType)})
	}
	pairs = append(pairs,
		ui.Pair{K: ui.T("Cost"), V: ui.T(ompFormatHubCost(selected.Cost))},
		ui.Pair{K: ui.T("Time"), V: ui.T(ompFormatHubDuration(selected.DurationMs))},
		ui.Pair{K: ui.T("Requests"), V: ui.T(ompFormatNumber(selected.Requests))},
		ui.Pair{K: ui.T("Tools"), V: ui.T(ompFormatNumber(selected.Tools))},
		ui.Pair{K: ui.T("Tokens"), V: ui.T(ompFormatNumber(selected.Tokens))},
	)
	if selected.ParentID != "" {
		pairs = append(pairs, ui.Pair{K: ui.T("Parent"), V: ui.Spans(ui.Span{T: selected.ParentID, S: "mono"})})
	}
	if selected.SessionFile != "" {
		pairs = append(pairs, ui.Pair{K: ui.T("Session"), V: ui.Spans(ui.Span{T: selected.SessionFile, S: "mono muted"})})
	}
	out = append(out, ui.KV{
		Common: ui.Common{Key: "facts", Role: "omp.hub.kv"},
		Layout: ui.KVGrid,
		Items:  pairs,
	})

	if selected.ContextWindow > 0 {
		ratio := math.Max(0, math.Min(1, float64(selected.ContextTokens)/float64(selected.ContextWindow)))
		pct := int(math.Round(ratio * 100))
		label := fmt.Sprintf("%s / %s · %d%%", ompFormatNumber(selected.ContextTokens), ompFormatNumber(selected.ContextWindow), pct)
		out = append(out, ui.Meter{
			Common: ui.Common{Key: "context"},
			Value:  &ratio,
			Style:  ui.MeterBar,
			Size:   ui.SizeMD,
			Label:  ui.T(label),
		})
	}

	if strings.TrimSpace(selected.Task) != "" {
		out = append(out, ui.Section{
			Common: ui.Common{Key: "task"},
			Head:   ui.T("Task"),
			Children: []tern.Element{
				ui.Md{Common: ui.Common{Key: "body"}, Text: selected.Task},
			},
		})
	}

	var recentRows []tern.Element
	for i, act := range s.agentHub.Activity {
		if act.AgentID != selected.ID {
			continue
		}
		if len(recentRows) >= 10 {
			break
		}
		actAgentID := act.AgentID
		recentRows = append(recentRows, ui.Row{
			Common: ui.Common{
				Key:     fmt.Sprintf("act-%d", i),
				Role:    "omp.hub.activity.row",
				Title:   "Open in transcript",
				Actions: &ui.Actions{Click: "transcript"},
				OnClick: func(*tern.Action) { s.activateAgentHubItem(actAgentID) },
			},
			Gap:   ui.GapSM,
			Align: ui.AlignBaseline,
			Children: []tern.Element{
				ui.Text{Common: ui.Common{Key: "at"}, Spans: []ui.Span{{T: ompFormatActivityClock(act.TimestampMs), S: "mono dim"}}, Wrap: ui.WrapNone},
				ui.Text{Common: ui.Common{Key: "title"}, Spans: []ui.Span{{T: oneLineTrim(act.Title, 40), S: "mono muted"}}, Wrap: ui.WrapNone},
				ui.Text{Common: ui.Common{Key: "detail", Grow: 1}, Text: oneLineTrim(act.Detail, 60), Truncate: ui.TruncateEnd},
			},
		})
	}
	if len(recentRows) == 0 && strings.TrimSpace(selected.Activity) != "" {
		recentRows = append(recentRows, ui.Text{
			Common: ui.Common{Key: "latest"},
			Text:   selected.Activity,
			Wrap:   ui.WrapWord,
		})
	}
	if len(recentRows) == 0 {
		recentRows = append(recentRows, ui.Text{
			Common: ui.Common{Key: "empty"},
			Spans:  []ui.Span{{T: "No response or tool activity yet", S: "dim"}},
		})
	}
	out = append(out, ui.Section{
		Common:   ui.Common{Key: "recentActivity", Role: "omp.hub.activity"},
		Head:     ui.T("Recent activity"),
		Children: recentRows,
	})
	return out
}

func (s *session) ompAgentTranscriptOverlayNodeLocked(st *ompAgentHubState) tern.Node {
	var agent *AgentHubItem
	for i := range s.agentHub.Agents {
		if s.agentHub.Agents[i].ID == st.transcriptID {
			cp := s.agentHub.Agents[i]
			agent = &cp
			break
		}
	}
	displayID := st.transcriptID
	if agent != nil && agent.DisplayName != "" {
		displayID = agent.DisplayName
	}

	var metaChildren []tern.Element
	metaChildren = append(metaChildren, ui.Text{
		Common: ui.Common{Key: "id"},
		Spans:  []ui.Span{{T: displayID, S: "strong"}},
		Wrap:   ui.WrapNone,
	})
	if agent != nil {
		metaChildren = append(metaChildren, ui.Badge{
			Common: ui.Common{Key: "status", Tone: ompAgentStatusTone(agent.Status)},
			Text:   agent.Status,
		})
		kindTag := agent.AgentType
		if agent.ParentID != "" {
			if kindTag != "" {
				kindTag += " · of " + agent.ParentID
			} else {
				kindTag = "of " + agent.ParentID
			}
		}
		if kindTag != "" {
			metaChildren = append(metaChildren, ui.Text{
				Common:   ui.Common{Key: "kind"},
				Spans:    []ui.Span{{T: kindTag, S: "dim"}},
				Truncate: ui.TruncateEnd,
			})
		}
		if agent.Model != "" {
			metaChildren = append(metaChildren, ui.Text{
				Common:   ui.Common{Key: "model"},
				Spans:    []ui.Span{{T: agent.Model, S: "muted"}},
				Truncate: ui.TruncateEnd,
			})
		}
	}

	topRow := ui.Row{
		Common: ui.Common{Key: "top"},
		Gap:    ui.GapSM,
		Align:  ui.AlignCenter,
		Children: []tern.Element{
			ui.Row{
				Common:   ui.Common{Key: "meta", Grow: 1},
				Gap:      ui.GapSM,
				Align:    ui.AlignCenter,
				Children: metaChildren,
			},
			ui.Button{
				Common: ui.Common{
					Key:     "refresh",
					Actions: &ui.Actions{Click: "transcript.refresh"},
					OnClick: func(*tern.Action) { go s.refreshAgentTranscript() },
				},
				Text: "Refresh",
				Size: ui.SizeSM,
			},
			ui.Button{
				Common: ui.Common{
					Key:     "back",
					Actions: &ui.Actions{Click: "transcript.back"},
					OnClick: func(*tern.Action) {
						s.mu.Lock()
						if s.agentHubUI != nil {
							s.agentHubUI.transcriptOpen = false
							s.agentHubUI.transcriptID = ""
							s.agentHubUI.transcriptDraft = ""
							s.agentHubUI.transcriptNote = ""
							s.queue(tern.OpFocus(""))
							s.dirty = true
							_ = s.flushLocked()
						}
						s.mu.Unlock()
					},
				},
				Text: "Back to Agents",
				Size: ui.SizeSM,
			},
			ui.Button{
				Common: ui.Common{
					Key:     "close",
					Actions: &ui.Actions{Click: "transcript.close"},
					OnClick: func(*tern.Action) { s.closeAgentHub() },
				},
				Text: "Close",
				Keys: []string{"esc"},
				Size: ui.SizeSM,
			},
		},
	}

	var bodyChildren []tern.Element
	if len(st.transcript) == 0 {
		bodyChildren = append(bodyChildren, ui.Text{
			Common: ui.Common{Key: "placeholder"},
			Spans:  []ui.Span{{T: "No transcript messages yet.", S: "dim"}},
			Wrap:   ui.WrapWord,
		})
	} else {
		entries := st.transcript
		offset := 0
		if len(entries) > maxTranscriptViewerEntries {
			offset = len(entries) - maxTranscriptViewerEntries
			bodyChildren = append(bodyChildren, ui.Text{
				Common: ui.Common{Key: "truncated"},
				Spans:  []ui.Span{{T: fmt.Sprintf("Showing last %d of %d entries", maxTranscriptViewerEntries, len(entries)), S: "dim"}},
				Wrap:   ui.WrapNone,
			})
			entries = entries[offset:]
		}
		for i, entry := range entries {
			bodyChildren = append(bodyChildren, ompTranscriptEntryElement(offset+i, entry))
		}
	}

	children := []tern.Element{
		topRow,
		ui.Col{
			Common:   ui.Common{Key: "transcript", Grow: 1},
			Gap:      ui.GapSM,
			Children: bodyChildren,
		},
	}

	if st.transcriptNote != "" {
		children = append(children, ui.Text{
			Common:   ui.Common{Key: "notice", Tone: ui.ToneError},
			Text:     st.transcriptNote,
			Truncate: ui.TruncateEnd,
		})
	}

	canMessage := agentHubCanMessage(agent, s.sendAgentMessageFn)
	if canMessage {
		draft := st.transcriptDraft
		cursor := utf16Len(draft)
		inputNode := ui.Input{
			Common: ui.Common{Key: "input", Grow: 1},
			Field: ui.Field{
				Text:        draft,
				Cursor:      &cursor,
				Placeholder: ui.T("Send a message to " + displayID + "…"),
				Sendable:    true,
			},
		}.Node()
		inputNode.Handlers = tern.Handlers{
			OnFocus: func(*tern.Focus) {
				s.mu.Lock()
				if s.agentHubUI != nil && s.agentHubUI.open && s.agentHubUI.transcriptOpen {
					s.queue(tern.OpFocus(agentTranscriptInputID))
					_ = s.flushLocked()
				}
				s.mu.Unlock()
			},
			OnEdit: func(ev *tern.Edit) {
				s.mu.Lock()
				if s.agentHubUI != nil && s.agentHubUI.transcriptOpen {
					s.agentHubUI.transcriptDraft = applyUTF16Edit(s.agentHubUI.transcriptDraft, ev.From, ev.To, ev.Text)
					s.dirty = true
					_ = s.flushLocked()
				}
				s.mu.Unlock()
			},
			OnSend: func(ev *tern.Send) {
				s.mu.Lock()
				body := ev.Text
				if body == "" && s.agentHubUI != nil {
					body = s.agentHubUI.transcriptDraft
				}
				s.startSendAgentTranscriptMessageLocked(body)
				s.mu.Unlock()
			},
		}
		children = append(children, ui.Row{
			Common: ui.Common{Key: "compose"},
			Gap:    ui.GapSM,
			Align:  ui.AlignCenter,
			Children: []tern.Element{
				inputNode,
				ui.Button{
					Common: ui.Common{
						Key:     "send",
						Actions: &ui.Actions{Click: "transcript.send"},
						OnClick: func(*tern.Action) {
							s.mu.Lock()
							var draft string
							if s.agentHubUI != nil {
								draft = s.agentHubUI.transcriptDraft
							}
							s.startSendAgentTranscriptMessageLocked(draft)
							s.mu.Unlock()
						},
					},
					Text: "Send",
					Keys: []string{"enter"},
					Size: ui.SizeSM,
				},
			},
		})
	}

	if agent != nil {
		var statsChildren []tern.Element
		if agent.ContextWindow > 0 {
			ratio := math.Max(0, math.Min(1, float64(agent.ContextTokens)/float64(agent.ContextWindow)))
			pct := int(math.Round(ratio * 100))
			statsChildren = append(statsChildren, ui.Progress{
				Common: ui.Common{Key: "ctx"},
				Value:  &ratio,
				Label:  ui.T(fmt.Sprintf("%d%% / %s", pct, ompFormatNumber(agent.ContextWindow))),
			})
		}
		var statSpans []ui.Span
		addStat := func(text, style string) {
			if len(statSpans) > 0 {
				statSpans = append(statSpans, ui.Span{T: " · ", S: "dim"})
			}
			statSpans = append(statSpans, ui.Span{T: text, S: style})
		}
		if agent.Tools > 0 {
			addStat(fmt.Sprintf("%s tools", ompFormatNumber(agent.Tools)), "dim")
		}
		if agent.DurationMs > 0 {
			addStat(ompFormatHubDuration(agent.DurationMs), "dim")
		}
		if agent.Tokens > 0 {
			addStat(fmt.Sprintf("%s tok", ompFormatNumber(agent.Tokens)), "dim")
		}
		if agent.Cost > 0 {
			addStat(ompFormatHubCost(agent.Cost), "statusLineCost")
		}
		if len(statSpans) > 0 {
			statsChildren = append(statsChildren, ui.Text{
				Common:   ui.Common{Key: "summary"},
				Spans:    statSpans,
				Truncate: ui.TruncateEnd,
			})
		}
		if len(statsChildren) > 0 {
			children = append(children, ui.Row{
				Common:   ui.Common{Key: "stats"},
				Gap:      ui.GapMD,
				Align:    ui.AlignCenter,
				Children: statsChildren,
			})
		}
	}

	overlay := ui.Overlay{
		Common: ui.Common{
			Key:  "agent-transcript",
			Role: "omp.hub.transcript",
		},
		Size:  ui.SizeLG,
		Modal: true,
		Head: ui.Spans(
			ui.Span{T: "Agent Hub", S: "accent"},
			ui.Span{T: " · ", S: "dim"},
			ui.Span{T: displayID, S: "accent"},
		),
		Icon:     ui.IconName("users"),
		Children: children,
	}
	return buildNode(overlay)
}

func ompTranscriptEntryElement(idx int, entry AgentHubTranscriptEntry) tern.Element {
	key := fmt.Sprintf("msg-%d", idx)
	switch entry.Role {
	case "user":
		return ui.Card{
			Common: ui.Common{Key: key, Role: "omp.user", Tone: ui.ToneUser},
			Children: []tern.Element{
				ui.Md{Common: ui.Common{Key: "body"}, Text: entry.Text},
			},
		}
	case "tool":
		head := entry.ToolName
		if head == "" {
			head = "tool"
		}
		if args := oneLineTrim(entry.ToolArgs, 80); args != "" {
			head += " " + args
		}
		tone := ui.ToneMuted
		if entry.IsError {
			tone = ui.ToneError
		}
		output := entry.ToolOutput
		if output == "" {
			output = entry.Text
		}
		var body []tern.Element
		if output != "" {
			body = append(body, ui.Text{
				Common: ui.Common{Key: "out"},
				Text:   truncateRunes(output, maxTranscriptToolOutputRunes),
				Wrap:   ui.WrapWord,
			})
		}
		return ui.Section{
			Common:      ui.Common{Key: key, Role: "omp.tool", Tone: tone},
			Head:        ui.T(head),
			Collapsible: len(output) > 160,
			Collapsed:   len(output) > 160 && !entry.IsError,
			Children:    body,
		}
	default:
		var parts []tern.Element
		if strings.TrimSpace(entry.Thinking) != "" {
			parts = append(parts, ui.Section{
				Common:      ui.Common{Key: "thinking", Role: "omp.thinking"},
				Head:        ui.T("Thinking"),
				Collapsible: true,
				Collapsed:   true,
				Children: []tern.Element{
					ui.Text{Common: ui.Common{Key: "body"}, Text: truncateRunes(entry.Thinking, maxTranscriptThinkingRunes), Wrap: ui.WrapWord},
				},
			})
		}
		if strings.TrimSpace(entry.Text) != "" {
			parts = append(parts, ui.Md{Common: ui.Common{Key: "body"}, Text: entry.Text})
		}
		return ui.Col{
			Common:   ui.Common{Key: key, Role: "omp.assistant"},
			Gap:      ui.GapXS,
			Children: parts,
		}
	}
}

func ompAgentHubSubtitle(snap AgentHubSnapshot) string {
	if snap.SubagentCost <= 0 && snap.TotalDurationMs <= 0 && snap.TotalRequests <= 0 && snap.TotalTokens <= 0 {
		return "No usage reported yet"
	}
	parts := []string{ompFormatHubCost(snap.SubagentCost)}
	if snap.TotalDurationMs > 0 {
		parts = append(parts, ompFormatHubDuration(snap.TotalDurationMs))
	}
	parts = append(parts,
		fmt.Sprintf("%s req", ompFormatNumber(snap.TotalRequests)),
		fmt.Sprintf("%s tok", ompFormatNumber(snap.TotalTokens)),
	)
	return strings.Join(parts, " · ")
}

func ompFormatHubCost(cost float64) string {
	if math.IsNaN(cost) || math.IsInf(cost, 0) || cost <= 0 {
		return "$0.00"
	}
	switch {
	case cost < 0.01:
		return "$" + toFixed(cost, 4)
	case cost < 1:
		return "$" + toFixed(cost, 3)
	default:
		return "$" + toFixed(cost, 2)
	}
}

func ompFormatHubDuration(ms int64) string {
	if ms <= 0 {
		return "0s"
	}
	sec := ms / 1000
	if sec < 60 {
		if ms < 10_000 {
			return fmt.Sprintf("%.1fs", float64(ms)/1000.0)
		}
		return fmt.Sprintf("%ds", sec)
	}
	mins := sec / 60
	remSec := sec % 60
	if mins < 60 {
		return fmt.Sprintf("%dm%ds", mins, remSec)
	}
	hours := mins / 60
	remMin := mins % 60
	if hours < 24 {
		return fmt.Sprintf("%dh%dm", hours, remMin)
	}
	days := hours / 24
	remHours := hours % 24
	return fmt.Sprintf("%dd%dh", days, remHours)
}

func ompFormatActivityClock(tsMs int64) string {
	if tsMs <= 0 {
		return ""
	}
	return time.UnixMilli(tsMs).Format("15:04:05")
}

func oneLineTrim(s string, maxRunes int) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", " "), "\n", " "))
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	rs := []rune(s)
	return string(rs[:maxRunes-1]) + "…"
}

func applyUTF16Edit(s string, from, to int, replacement string) string {
	if from < 0 {
		from = 0
	}
	if to < from {
		to = from
	}
	startByte := utf16OffsetToByte(s, from)
	endByte := utf16OffsetToByte(s, to)
	return s[:startByte] + replacement + s[endByte:]
}

func utf16OffsetToByte(s string, off int) int {
	if off <= 0 {
		return 0
	}
	units := 0
	for i, r := range s {
		if units >= off {
			return i
		}
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	return len(s)
}
