package warden

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// `/warden index` (extension.ts 2958-3105): ask the session model to write the capability index, validate and
// sanitize what it wrote, and tell the user what was built. The model writes the files with its own tools; the
// action guard lets exactly those two files through while the command runs.

// conscienceIndexWrite lets the two index files through the action guard while /warden index runs: nothing else
// is exempt, and the exemption is gone when the command ends. CONSCIENCE-LANE HOOK: called from onToolCall.
func (w *warden) conscienceIndexWrite(ctx sdk.Context, cfg Config, tool string, input map[string]any) bool {
	s := &w.conscience
	s.mu.Lock()
	running, paths := s.indexRunning, s.indexWritePaths
	s.mu.Unlock()
	if !running || tool != "write" {
		return false
	}
	raw, _ := input["path"].(string)
	if raw == "" {
		return false
	}
	resolved := raw
	if strings.HasPrefix(resolved, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			resolved = home + resolved[1:]
		}
	}
	if abs, err := filepath.Abs(resolved); err == nil {
		resolved = abs
	}
	if !containsString(paths, resolved) {
		return false
	}
	w.record(ctx, cfg, GuardAction, "index write, expected", []string{"trigger: before_tool_use", "path: " + Redact(raw)}, nil)
	return true
}

func (w *warden) setIndexRunning(on bool, paths []string) {
	s := &w.conscience
	s.mu.Lock()
	s.indexRunning, s.indexWritePaths = on, paths
	s.mu.Unlock()
}

type mtimeSnap struct {
	exists  bool
	mtimeNs int64
}

func snapOf(path string) mtimeSnap {
	st, err := os.Stat(path)
	if err != nil {
		return mtimeSnap{}
	}
	return mtimeSnap{true, st.ModTime().UnixNano()}
}

// indexCommand is `/warden index`.
func (w *warden) indexCommand(ctx sdk.Context) error {
	cfg := w.config(ctx)
	if !ctx.HasUI() {
		w.say(ctx, "Index needs an interactive session to run.", "warning")
		return nil
	}
	// The host's snapshot is authoritative; the skills the last prompt saw are the fallback.
	var skills []IndexSkill
	if opts, err := ctx.GetSystemPromptOptions(); err != nil {
		w.record(ctx, cfg, GuardConscience, "snapshot unavailable: "+err.Error(), []string{"trigger: warden_index"}, nil)
	} else {
		for _, sk := range skillsFromHost(opts.Skills) {
			if !sk.DisableModelInvocation {
				skills = append(skills, IndexSkill{sk.Name, sk.Description, sk.FilePath})
			}
		}
	}
	if len(skills) == 0 {
		s := &w.conscience
		s.mu.Lock()
		cached := s.skills
		s.mu.Unlock()
		for _, sk := range cached {
			if !sk.DisableModelInvocation {
				skills = append(skills, IndexSkill{sk.Name, sk.Description, sk.FilePath})
			}
		}
	}
	if len(skills) == 0 {
		w.say(ctx, "No skills available. Run a prompt first so Pi discovers installed skills, then retry /warden index.", "warning")
		return nil
	}
	tools := capabilityTools(ctx)
	roughBytes := len(skills)*200 + len(tools)*100
	ok, err := ctx.Confirm("Build capability index?",
		"This reads every installed skill file ("+itoaInt(len(skills))+" skills) and tool description ("+itoaInt(len(tools))+" tools, ~"+itoaInt(roughBytes)+" bytes total) and runs the session model to produce index entries. It may take a minute and use real tokens.")
	if err != nil || !ok {
		return nil
	}
	dirs := w.indexDirsOf()
	globalPath, projectPath := dirs.path("global", ""), dirs.path("project", ctx.Cwd())
	_ = dirs.ensure("global")
	_ = dirs.ensure("project")
	home, _ := ctx.ConfigHome()
	prompt := buildIndexPrompt(ctx.Cwd(), skills, tools, globalPath, projectPath, home)
	// The files before the model runs, to see whether it wrote anything.
	preGlobal, preProject := snapOf(globalPath), snapOf(projectPath)
	absGlobal, _ := filepath.Abs(globalPath)
	absProject, _ := filepath.Abs(projectPath)
	w.setIndexRunning(true, []string{absGlobal, absProject})
	ctx.Notify("pi-warden: Building capability index...", "info")
	runErr := w.askAgent(ctx, prompt)
	w.setIndexRunning(false, nil)
	if runErr != nil {
		w.say(ctx, "pi-warden index failed: "+runErr.Error()+".", "error")
		return nil
	}
	postGlobal, postProject := snapOf(globalPath), snapOf(projectPath)
	globalChanged := !preGlobal.exists || postGlobal.mtimeNs > preGlobal.mtimeNs
	projectChanged := !preProject.exists || postProject.mtimeNs > preProject.mtimeNs
	if !globalChanged && !projectChanged {
		w.say(ctx, "The model did not write the index; nothing was changed.", "warning")
		return nil
	}
	rawGlobal, haveGlobal := readIndexRaw(globalPath)
	rawProject, haveProject := readIndexRaw(projectPath)
	// Only a file with the full index shape reaches validation; the entries-only retry below re-reads the same file.
	validate := func(path string, raw map[string]any, have bool) ValidationResult {
		if !have {
			return ValidationResult{}
		}
		res := validateIndex(raw)
		if res.File == nil {
			if entries, ok := entriesOnly(path); ok {
				res = validateIndex(map[string]any{"formatVersion": float64(1), "builtAt": "", "model": "", "entries": entries})
			}
		}
		return res
	}
	globalResult := validate(globalPath, rawGlobal, haveGlobal)
	projectResult := validate(projectPath, rawProject, haveProject)
	var rejections []string
	if haveGlobal && globalResult.File == nil {
		for _, r := range globalResult.Rejections {
			rejections = append(rejections, "global index: entry "+r)
		}
	}
	if haveProject && projectResult.File == nil {
		for _, r := range projectResult.Rejections {
			rejections = append(rejections, "project index: entry "+r)
		}
	}
	if len(rejections) > 0 {
		lines := make([]string, len(rejections))
		for i, r := range rejections {
			lines[i] = "  " + r
		}
		w.say(ctx, "Index rejected:\n"+strings.Join(lines, "\n")+"\nOld files kept.", "warning")
		return nil
	}
	validatedGlobal, validatedProject := globalResult.File, projectResult.File
	// The model's hashes are untrusted: they are recomputed from the skill files.
	recompute := func(f *IndexFile) {
		if f == nil {
			return
		}
		for i := range f.Entries {
			if f.Entries[i].Kind != "skill" {
				continue
			}
			for _, sk := range skills {
				if sk.Name == f.Entries[i].Name {
					if sk.FilePath != "" {
						f.Entries[i].SourceHash = fileContentHash(sk.FilePath)
					}
					break
				}
			}
		}
	}
	recompute(validatedGlobal)
	recompute(validatedProject)
	model := w.modelKey(ctx)
	if model == "" {
		model = "unknown"
	}
	builtAt := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	build := func(f *IndexFile) *IndexFile {
		return &IndexFile{FormatVersion: 1, BuiltAt: builtAt, Model: model, Entries: f.Entries}
	}
	if validatedGlobal != nil {
		_ = writeIndex(globalPath, build(validatedGlobal))
	}
	if validatedProject != nil {
		_ = writeIndex(projectPath, build(validatedProject))
	}
	s := &w.conscience
	s.mu.Lock()
	s.globalIndex, s.projectIndex = validatedGlobal, validatedProject
	s.mu.Unlock()
	empty := IndexStats{}
	gs, ps := empty, empty
	if validatedGlobal != nil {
		gs = indexStats(validatedGlobal)
	}
	if validatedProject != nil {
		ps = indexStats(validatedProject)
	}
	total := gs.GlobalEntries + ps.ProjectEntries
	thin := append(append([]string{}, gs.ThinSources...), ps.ThinSources...)
	truncated := gs.TruncatedCount + ps.TruncatedCount
	lines := []string{"Index built: " + itoaInt(total) + " entries (" + itoaInt(gs.GlobalEntries) + " global, " + itoaInt(ps.ProjectEntries) + " project)."}
	if len(thin) > 0 {
		lines = append(lines, "Thin sources: "+strings.Join(thin, ", ")+".")
	}
	if truncated > 0 {
		lines = append(lines, itoaInt(truncated)+" truncated at bullet boundary.")
	}
	quality := append(append([]SourceQualityReport{}, gs.SourceQualityReport...), ps.SourceQualityReport...)
	sort.SliceStable(quality, func(i, j int) bool { return quality[i].SourceQuality == "thin" && quality[j].SourceQuality != "thin" })
	if len(quality) == 0 {
		lines = append(lines, "", "Every description states when to use it.")
	} else {
		lines = append(lines, "", "These descriptions would recommend better with a rewrite:")
		for _, q := range quality[:min(25, len(quality))] {
			lines = append(lines, "  "+q.Name+" ("+q.Kind+"): "+q.Improve)
		}
		if len(quality) > 25 {
			lines = append(lines, "  ... and "+itoaInt(len(quality)-25)+" more.")
		}
	}
	w.say(ctx, strings.Join(lines, "\n"), "info")
	return nil
}

// entriesOnly reads the `entries` array of a file that may lack the index metadata (the original's retry, which
// only matters for a file that already has the full shape).
func entriesOnly(path string) ([]any, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var parsed map[string]any
	if json.Unmarshal([]byte(nodeUTF8(raw)), &parsed) != nil {
		return nil, false
	}
	entries, ok := parsed["entries"].([]any)
	return entries, ok
}
