package ast

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
)

// ast_edit tool implementation, plus staged-preview resolve/reject queue.
// upstream: packages/coding-agent/src/tools/ast-edit.ts,
//           packages/coding-agent/src/tools/resolve.ts,
//           packages/tui/src/tools/ast-edit.ts

const (
	defaultMaxAstFiles = 1000

	// previewPendingNotice matches omp's PREVIEW_PENDING_NOTICE (tools/resolve.ts:36-37).
	previewPendingNotice = "Staged as a proposal — files NOT modified yet. To apply: `resolve <one-sentence reason>`. To discard: `reject <one-sentence reason>`."
)

// astEditSchema matches omp's astEditSchema in tools/ast-edit.ts:42-54.
func astEditSchema() sdk.Schema {
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"ops": map[string]any{
				"type":        "array",
				"minItems":    1,
				"description": "rewrite ops",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"pat": map[string]any{
							"type":        "string",
							"description": "ast pattern",
						},
						"out": map[string]any{
							"type":        "string",
							"description": "replacement template",
						},
					},
					"required":             []string{"pat", "out"},
					"additionalProperties": false,
				},
			},
			"paths": map[string]any{
				"type":        "array",
				"minItems":    1,
				"description": "files, directories, globs, or internal URLs to rewrite",
				"items": map[string]any{
					"type":        "string",
					"description": "file, directory, glob, or internal URL to rewrite",
				},
			},
		},
		"required":             []string{"ops", "paths"},
		"additionalProperties": false,
	}
}

func resolveSchema() sdk.Schema {
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"reason": map[string]any{
				"type":        "string",
				"description": "One-sentence reason for applying the pending staged action.",
			},
		},
		"required":             []string{"reason"},
		"additionalProperties": false,
	}
}

func rejectSchema() sdk.Schema {
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"reason": map[string]any{
				"type":        "string",
				"description": "One-sentence reason for discarding the pending staged action.",
			},
		},
		"required":             []string{"reason"},
		"additionalProperties": false,
	}
}

type astEditOp struct {
	Pat string
	Out string
}

type astEditFileReplacement struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

type astEditFilePlan struct {
	candidate candidateFile
	source    string
	updated   string
	changes   []astReplaceChange
}

type astEditPassResult struct {
	files             []astEditFilePlan
	totalReplacements int
	filesTouched      int
	filesSearched     int
	applied           bool
	limitReached      bool
	parseErrors       []string
	skippedLangs      []string
}

func maxAstFilesEnv() int {
	raw := strings.TrimSpace(os.Getenv("PI_MAX_AST_FILES"))
	if raw == "" {
		return defaultMaxAstFiles
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return defaultMaxAstFiles
	}
	return v
}

func (m *manager) handleAstEdit(ctx sdk.Context, params map[string]any) (any, error) {
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-runCtx.Done():
		}
	}()
	return m.runAstEdit(runCtx, ctx, ctx.Cwd(), params)
}

func (m *manager) runAstEdit(runCtx context.Context, sdkCtx sdk.Context, cwd string, params map[string]any) (sdk.ToolResult, error) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return sdk.ToolResult{}, err
		}
	}

	ops, err := parseAstEditOps(params["ops"])
	if err != nil {
		return sdk.ToolResult{}, err
	}
	paths, err := parseAstEditPaths(params["paths"])
	if err != nil {
		return sdk.ToolResult{}, err
	}

	maxFiles := maxAstFilesEnv()

	scope, err := resolveToolScope(sdkCtx, cwd, paths, "rewrite", true)
	if err != nil {
		return sdk.ToolResult{}, err
	}

	binary, err := findAstGrepBinary()
	if err != nil {
		return sdk.ToolResult{
			Content: MissingBinaryGuidance,
			IsError: true,
		}, nil
	}

	sessKey := sessionKey(sdkCtx)
	if sessKey == "" {
		return sdk.ToolResult{}, fmt.Errorf("ast_edit requires an active session to stage a preview")
	}

	// Preview pass (dry-run): matches omp tools/ast-edit.ts:307-317.
	preview, err := m.executeRewritePass(runCtx, sessKey, binary, cwd, scope, ops, maxFiles, true, nil)
	if err != nil {
		return sdk.ToolResult{}, err
	}

	cappedParseErrors, parseErrorsTotal := capParseErrors(preview.parseErrors)
	grammarNote := missingLanguagesNote(preview.skippedLangs)

	fileList := make([]string, 0, len(preview.files))
	fileReplacementCounts := make(map[string]int, len(preview.files))
	changesByFile := make(map[string][]astReplaceChange, len(preview.files))
	candByDisplay := make(map[string]candidateFile, len(preview.files))
	for _, fp := range preview.files {
		disp := fp.candidate.displayPath
		fileList = append(fileList, disp)
		fileReplacementCounts[disp] = len(fp.changes)
		changesByFile[disp] = fp.changes
		candByDisplay[disp] = fp.candidate
	}

	details := map[string]any{
		"totalReplacements": preview.totalReplacements,
		"filesTouched":      preview.filesTouched,
		"filesSearched":     preview.filesSearched,
		"applied":           preview.applied,
		"limitReached":      preview.limitReached,
		"scopePath":         scope.scopePath,
		"searchPath":        dirs.StandardizeMacOSPath(scope.searchPath),
		"cwd":               dirs.StandardizeMacOSPath(cwd),
		"files":             fileList,
		"fileReplacements":  []astEditFileReplacement{},
	}
	if len(cappedParseErrors) > 0 {
		details["parseErrors"] = cappedParseErrors
		details["parseErrorsTotal"] = parseErrorsTotal
	}

	if preview.totalReplacements == 0 {
		text := "No replacements made"
		if len(cappedParseErrors) > 0 {
			text += "\n" + strings.Join(formatParseErrors(cappedParseErrors, parseErrorsTotal), "\n")
		}
		if grammarNote != "" {
			text += "\n" + grammarNote
		}
		return sdk.ToolResult{
			Content: text,
			Details: details,
		}, nil
	}

	hashTags := make(map[string]string, len(fileList))
	for _, relPath := range fileList {
		cand := candByDisplay[relPath]
		if cand.immutable {
			continue
		}
		if tag := recordSnapshotForFile(sdkCtx, cand.absPath); tag != "" {
			hashTags[relPath] = tag
		}
	}

	renderChanges := func(relPath string) (modelOut []string, displayOut []string) {
		changes := changesByFile[relPath]
		_, hasHash := hashTags[relPath]
		lineNumWidth := 0
		for _, ch := range changes {
			if w := len(strconv.Itoa(ch.StartLine)); w > lineNumWidth {
				lineNumWidth = w
			}
		}
		for _, ch := range changes {
			beforeLine := clipFirstLine(ch.Before, astEditLineClip)
			afterLine := clipFirstLine(ch.After, astEditLineClip)
			if hasHash {
				modelOut = append(modelOut, fmt.Sprintf("-%d:%s", ch.StartLine, beforeLine))
				modelOut = append(modelOut, fmt.Sprintf("+%d:%s", ch.StartLine, afterLine))
			} else {
				modelOut = append(modelOut, fmt.Sprintf("-%d:%d %s", ch.StartLine, ch.StartColumn, beforeLine))
				modelOut = append(modelOut, fmt.Sprintf("+%d:%d %s", ch.StartLine, ch.StartColumn, afterLine))
			}
			displayOut = append(displayOut, formatCodeFrameLine("-", ch.StartLine, beforeLine, lineNumWidth))
			displayOut = append(displayOut, formatCodeFrameLine("+", ch.StartLine, afterLine, lineNumWidth))
		}
		return modelOut, displayOut
	}

	var outputLines, displayLines []string
	if scope.isDirectory {
		grouped := formatGroupedFiles(fileList, func(relPath string) groupedFileSection {
			mod, disp := renderChanges(relPath)
			count := fileReplacementCounts[relPath]
			hashSuffix := ""
			if tag := hashTags[relPath]; tag != "" {
				hashSuffix = "#" + tag
			}
			return groupedFileSection{
				headerSuffix: fmt.Sprintf("%s (%s)", hashSuffix, formatCount("replacement", count)),
				modelLines:   mod,
				displayLines: disp,
				skip:         len(mod) == 0,
			}
		})
		outputLines = append(outputLines, grouped.model...)
		displayLines = append(displayLines, grouped.display...)
	} else {
		for _, relPath := range fileList {
			mod, disp := renderChanges(relPath)
			if len(mod) == 0 {
				continue
			}
			if len(outputLines) > 0 {
				outputLines = append(outputLines, "")
				displayLines = append(displayLines, "")
			}
			if tag := hashTags[relPath]; tag != "" {
				outputLines = append(outputLines, formatHashlineHeader(relPath, tag))
			}
			outputLines = append(outputLines, mod...)
			displayLines = append(displayLines, disp...)
		}
	}

	fileReplacements := make([]astEditFileReplacement, len(fileList))
	for i, fp := range fileList {
		fileReplacements[i] = astEditFileReplacement{Path: fp, Count: fileReplacementCounts[fp]}
	}
	if preview.limitReached {
		outputLines = append(outputLines, "", "Limit reached; narrow paths.")
	}
	if len(cappedParseErrors) > 0 {
		outputLines = append(outputLines, "", strings.Join(formatParseErrors(cappedParseErrors, parseErrorsTotal), "\n"))
	}
	if grammarNote != "" {
		outputLines = append(outputLines, "", grammarNote)
	}

	// Stage pending action for `resolve` / `reject` (upstream: tools/ast-edit.ts:454-570).
	// Note: the closure does NOT capture sdkCtx (which dies after this tool call returns);
	// instead it receives the live resolveCtx passed when `resolve` executes.
	if !preview.applied && preview.totalReplacements > 0 {
		previewReplPlural := "s"
		if preview.totalReplacements == 1 {
			previewReplPlural = ""
		}
		previewFilePlural := "s"
		if preview.filesTouched == 1 {
			previewFilePlural = ""
		}
		label := fmt.Sprintf("AST Edit: %d replacement%s in %d file%s",
			preview.totalReplacements, previewReplPlural, preview.filesTouched, previewFilePlural)

		expectedCounts := make(map[string]int, len(fileReplacementCounts))
		for k, v := range fileReplacementCounts {
			expectedCounts[k] = v
		}
		expectedFiles := append([]string(nil), fileList...)
		expectedTotal := preview.totalReplacements
		expectedTouched := preview.filesTouched

		m.pushPending(sessKey, &PendingPreview{
			Label:          label,
			SourceToolName: "ast_edit",
			Apply: func(applyCtx context.Context, resolveCtx sdk.Context, _ string) (sdk.ToolResult, error) {
				checkGate := func(plan astEditFilePlan) error {
					req := gate.Request{
						Tool: "ast_edit",
						Op:   gate.OpEdit,
						Path: plan.candidate.absPath,
					}
					if plan.candidate.isURL {
						req.URL = plan.candidate.displayPath
					}
					return gate.Check(resolveCtx, req)
				}
				applyRes, err := m.executeRewritePass(applyCtx, sessKey, binary, cwd, scope, ops, maxFiles, false, checkGate)
				if err != nil {
					return sdk.ToolResult{}, err
				}
				cappedApplyErrs, applyErrsTotal := capParseErrors(applyRes.parseErrors)

				appliedFiles := make([]string, 0, len(applyRes.files))
				appliedCounts := make(map[string]int, len(applyRes.files))
				var freshTagLines []string
				for _, fp := range applyRes.files {
					disp := fp.candidate.displayPath
					appliedFiles = append(appliedFiles, disp)
					appliedCounts[disp] = len(fp.changes)
					if !fp.candidate.immutable {
						if freshTag := recordSnapshotForFile(resolveCtx, fp.candidate.absPath); freshTag != "" {
							freshTagLines = append(freshTagLines, formatHashlineHeader(disp, freshTag))
						}
					}
				}
				appliedReplacements := make([]astEditFileReplacement, len(appliedFiles))
				for i, fp := range appliedFiles {
					appliedReplacements[i] = astEditFileReplacement{Path: fp, Count: appliedCounts[fp]}
				}
				appliedDetails := map[string]any{
					"totalReplacements": applyRes.totalReplacements,
					"filesTouched":      applyRes.filesTouched,
					"filesSearched":     applyRes.filesSearched,
					"applied":           applyRes.applied,
					"limitReached":      applyRes.limitReached,
					"scopePath":         scope.scopePath,
					"files":             appliedFiles,
					"fileReplacements":  appliedReplacements,
				}
				if len(cappedApplyErrs) > 0 {
					appliedDetails["parseErrors"] = cappedApplyErrs
					appliedDetails["parseErrorsTotal"] = applyErrsTotal
				}

				stale := applyRes.totalReplacements != expectedTotal || applyRes.filesTouched != expectedTouched
				if !stale {
					for _, fp := range expectedFiles {
						if appliedCounts[fp] != expectedCounts[fp] {
							stale = true
							break
						}
					}
				}
				if !stale {
					for _, fp := range appliedFiles {
						if expectedCounts[fp] != appliedCounts[fp] {
							stale = true
							break
						}
					}
				}
				if stale {
					var staleText string
					switch {
					case applyRes.totalReplacements == 0:
						staleText = fmt.Sprintf("Preview is stale / no longer matches; no replacements were applied. Preview expected %d replacement%s in %d file%s.",
							expectedTotal, previewReplPlural, expectedTouched, previewFilePlural)
					case applyRes.totalReplacements < expectedTotal:
						staleText = fmt.Sprintf("Preview is stale / no longer matches; only %d of %d replacements were applied in %d of %d files.",
							applyRes.totalReplacements, expectedTotal, applyRes.filesTouched, expectedTouched)
					default:
						staleText = fmt.Sprintf("Preview is stale / no longer matches; applied %d replacements but preview expected %d.",
							applyRes.totalReplacements, expectedTotal)
					}
					if len(freshTagLines) > 0 {
						staleText += "\n" + strings.Join(freshTagLines, "\n")
					}
					return sdk.ToolResult{
						Content: staleText,
						Details: appliedDetails,
						IsError: true,
					}, nil
				}

				appliedReplPlural := "s"
				if applyRes.totalReplacements == 1 {
					appliedReplPlural = ""
				}
				appliedFilePlural := "s"
				if applyRes.filesTouched == 1 {
					appliedFilePlural = ""
				}
				appliedText := fmt.Sprintf("Applied %d replacement%s in %d file%s.",
					applyRes.totalReplacements, appliedReplPlural, applyRes.filesTouched, appliedFilePlural)
				if len(freshTagLines) > 0 {
					appliedText += "\n" + strings.Join(freshTagLines, "\n")
				}
				return sdk.ToolResult{
					Content: appliedText,
					Details: appliedDetails,
				}, nil
			},
		})

		sendResolveReminder(sdkCtx, "ast_edit")
		outputLines = append([]string{previewPendingNotice, ""}, outputLines...)
	}

	details["fileReplacements"] = fileReplacements
	details["displayContent"] = strings.Join(displayLines, "\n")

	return sdk.ToolResult{
		Content: strings.Join(outputLines, "\n"),
		Details: details,
	}, nil
}

// executeRewritePass runs ast-grep in JSON preview mode for every (language, op) pair,
// stages all edits in memory with overlap and matched-text verification, checks the
// mutation gate for every file before writing any, and writes modified files only when
// dryRun is false and every check succeeded.
// upstream: crates/natives/src/ast.rs:589-646, crates/ast/src/search/mod.rs:768-898
func (m *manager) executeRewritePass(
	ctx context.Context,
	sessKey string,
	binary string,
	cwd string,
	scope *scopeResolution,
	ops []astEditOp,
	maxFiles int,
	dryRun bool,
	checkGate func(plan astEditFilePlan) error,
) (*astEditPassResult, error) {
	candidates, err := discoverCandidates(ctx, cwd, scope, "")
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("ast_edit found no supported source files for the given path/glob")
	}

	// Sort rules by pattern string, matching crates/natives/src/ast.rs:388.
	sortedOps := make([]astEditOp, len(ops))
	copy(sortedOps, ops)
	sort.Slice(sortedOps, func(i, j int) bool {
		return sortedOps[i].Pat < sortedOps[j].Pat
	})

	langs, byLang, skippedLangs := groupCandidatesByLang(candidates)
	filesSearched := 0
	for _, lang := range langs {
		filesSearched += len(byLang[lang])
	}

	changesByAbs := make(map[string][]astReplaceChange)
	var parseErrors []string

	appendMatch := func(cand candidateFile, op astEditOp, rm sgMatchJSON) {
		after := op.Out
		if rm.Replacement != nil {
			after = *rm.Replacement
		}
		matchStart := rm.Range.ByteOffset.Start
		matchEnd := rm.Range.ByteOffset.End
		byteStart := matchStart
		byteEnd := matchEnd
		if rm.ReplacementOffsets != nil {
			byteStart = rm.ReplacementOffsets.Start
			byteEnd = rm.ReplacementOffsets.End
		}
		changesByAbs[cand.absPath] = append(changesByAbs[cand.absPath], astReplaceChange{
			Path:           cand.displayPath,
			AbsPath:        cand.absPath,
			Before:         rm.Text,
			After:          after,
			ByteStart:      byteStart,
			ByteEnd:        byteEnd,
			MatchByteStart: matchStart,
			MatchByteEnd:   matchEnd,
			DeletedLength:  byteEnd - byteStart,
			StartLine:      rm.Range.Start.Line + 1,
			StartColumn:    rm.Range.Start.Column + 1,
			EndLine:        rm.Range.End.Line + 1,
			EndColumn:      rm.Range.End.Column + 1,
		})
	}

	for _, lang := range langs {
		langFiles := byLang[lang]
		var directFiles, stdinFiles []candidateFile
		for _, f := range langFiles {
			if needsStdinForCLI(f.absPath, lang) {
				stdinFiles = append(stdinFiles, f)
			} else {
				directFiles = append(directFiles, f)
			}
		}

		for _, op := range sortedOps {
			opCompileFailed := false
			if len(directFiles) > 0 {
				lookup := buildCandidateLookup(directFiles, cwd)
				for _, batch := range batchCandidates(directFiles) {
					// Attached `--pattern=` / `--rewrite=` / `--lang=` prevent clap from
					// misinterpreting leading `-` patterns/rewrites as flags.
					args := []string{
						"run",
						"--pattern=" + op.Pat,
						"--rewrite=" + op.Out,
						"--lang=" + lang,
						"--json=stream",
						"--no-ignore=hidden",
						"--no-ignore=dot",
						"--no-ignore=exclude",
						"--no-ignore=global",
						"--no-ignore=parent",
						"--no-ignore=vcs",
						"--",
					}
					for _, f := range batch {
						args = append(args, f.absPath)
					}

					stderr, exitCode, runErr := streamAstGrepProcess(ctx, m.procs, sessKey, binary, cwd, args, nil, func(rm sgMatchJSON) error {
						cand, ok := resolveCandidateForMatch(rm.File, cwd, lookup)
						if !ok {
							return nil
						}
						appendMatch(cand, op, rm)
						return nil
					})
					if runErr != nil {
						return nil, runErr
					}
					if exitCode == 8 {
						// Pattern failed to compile in this language; record one per-language
						// parse issue and move on to the next op/language (crates/ast/src/search/mod.rs:796-811).
						parseErrors = append(parseErrors, formatPatternCompileError(op.Pat, lang, stderr))
						opCompileFailed = true
						break
					}
					if stderr != "" {
						parseErrors = append(parseErrors, parseStderrDiagnostics(stderr, op.Pat, lang)...)
					}
				}
			}
			if opCompileFailed {
				continue
			}

			for _, f := range stdinFiles {
				data, readErr := os.ReadFile(f.absPath)
				if readErr != nil {
					parseErrors = append(parseErrors, fmt.Sprintf("%s: %v", f.displayPath, readErr))
					continue
				}
				args := []string{
					"run",
					"--pattern=" + op.Pat,
					"--rewrite=" + op.Out,
					"--lang=" + lang,
					"--json=stream",
					"--stdin",
				}
				stderr, exitCode, runErr := streamAstGrepProcess(ctx, m.procs, sessKey, binary, cwd, args, bytes.NewReader(data), func(rm sgMatchJSON) error {
					appendMatch(f, op, rm)
					return nil
				})
				if runErr != nil {
					return nil, runErr
				}
				if exitCode == 8 {
					parseErrors = append(parseErrors, formatPatternCompileError(op.Pat, lang, stderr))
					break
				}
				if stderr != "" {
					parseErrors = append(parseErrors, parseStderrDiagnostics(stderr, op.Pat, lang)...)
				}
			}
		}
	}

	var plans []astEditFilePlan
	limitReached := false
	totalReplacements := 0

	for _, cand := range candidates {
		rawChanges := changesByAbs[cand.absPath]
		if len(rawChanges) == 0 {
			continue
		}
		if len(plans) >= maxFiles {
			limitReached = true
			break
		}
		sourceBytes, readErr := os.ReadFile(cand.absPath)
		if readErr != nil {
			parseErrors = append(parseErrors, fmt.Sprintf("%s: %v", cand.displayPath, readErr))
			continue
		}
		source := string(sourceBytes)
		updated, deduped, applyErr := applyEditsToSource(source, rawChanges, cand.displayPath)
		if applyErr != nil {
			var mismatch errSourceMismatch
			if errors.As(applyErr, &mismatch) {
				// Record as a non-fatal parse/mismatch issue and skip mutating this file
				// so corrupted offsets never write to disk.
				parseErrors = append(parseErrors, mismatch.msg)
				continue
			}
			return nil, applyErr
		}
		if len(deduped) == 0 {
			continue
		}
		plans = append(plans, astEditFilePlan{
			candidate: cand,
			source:    source,
			updated:   updated,
			changes:   deduped,
		})
		totalReplacements += len(deduped)
	}

	if !dryRun {
		// Check mutation gate for EVERY file before writing ANY file.
		if checkGate != nil {
			for _, plan := range plans {
				if err := checkGate(plan); err != nil {
					return nil, err
				}
			}
		}
		for _, plan := range plans {
			if plan.updated == plan.source {
				continue
			}
			info, statErr := os.Stat(plan.candidate.absPath)
			mode := os.FileMode(0o644)
			if statErr == nil {
				mode = info.Mode().Perm()
			}
			if err := os.WriteFile(plan.candidate.absPath, []byte(plan.updated), mode); err != nil {
				return nil, fmt.Errorf("Failed to write %s: %w", plan.candidate.displayPath, err)
			}
		}
	}

	return &astEditPassResult{
		files:             plans,
		totalReplacements: totalReplacements,
		filesTouched:      len(plans),
		filesSearched:     filesSearched,
		applied:           !dryRun,
		limitReached:      limitReached,
		parseErrors:       parseErrors,
		skippedLangs:      skippedLangs,
	}, nil
}

func parseAstEditOps(raw any) ([]astEditOp, error) {
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return nil, fmt.Errorf("`ops` must include at least one op entry")
	}
	ops := make([]astEditOp, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for i, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("`ops[%d]` must be an object with `pat` and `out`", i)
		}
		pat, _ := m["pat"].(string)
		out, _ := m["out"].(string)
		if pat == "" {
			return nil, fmt.Errorf("`ops[%d].pat` must be a non-empty pattern", i)
		}
		if _, dup := seen[pat]; dup {
			return nil, fmt.Errorf("Duplicate rewrite pattern: %s", pat)
		}
		seen[pat] = struct{}{}
		ops = append(ops, astEditOp{Pat: pat, Out: out})
	}
	return ops, nil
}

func parseAstEditPaths(raw any) ([]string, error) {
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return nil, fmt.Errorf("`paths` must contain at least one path or glob")
	}
	paths := make([]string, 0, len(items))
	for i, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("`paths[%d]` must be a string", i)
		}
		paths = append(paths, s)
	}
	return paths, nil
}
