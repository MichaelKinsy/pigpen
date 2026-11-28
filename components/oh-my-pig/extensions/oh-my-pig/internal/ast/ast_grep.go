package ast

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
)

// ast_grep tool implementation.
// upstream: packages/coding-agent/src/tools/ast-grep.ts, packages/tui/src/tools/ast-grep.ts

const defaultAstLimit = 50

// astGrepSchema matches omp's astGrepSchema in tools/ast-grep.ts:42-49.
func astGrepSchema() sdk.Schema {
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"pat": map[string]any{
				"type":        "string",
				"description": "ast pattern",
			},
			"path": map[string]any{
				"type":        "string",
				"description": `file, directory, glob, or internal URL to search; pass several as a semicolon-delimited list ("src; tests"). Omitted -> searches the workspace root (".")`,
			},
			"lang": map[string]any{
				"type":        "string",
				"description": "language override, e.g. cpp for ambiguous .h files",
			},
			"skip": map[string]any{
				"type":        "number",
				"description": "matches to skip",
			},
		},
		"required":             []string{"pat"},
		"additionalProperties": false,
	}
}

var parseErrorPrefixRe = regexp.MustCompile(`^.+: (.+: parse error \(syntax tree contains error nodes\))$`)

func normalizeParseError(e string) string {
	if m := parseErrorPrefixRe.FindStringSubmatch(e); len(m) == 2 {
		return m[1]
	}
	return e
}

type astGrepFileMatch struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

func (m *manager) handleAstGrep(ctx sdk.Context, params map[string]any) (any, error) {
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-runCtx.Done():
		}
	}()
	return m.runAstGrep(runCtx, ctx, ctx.Cwd(), params)
}

func (m *manager) runAstGrep(runCtx context.Context, sdkCtx sdk.Context, cwd string, params map[string]any) (sdk.ToolResult, error) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return sdk.ToolResult{}, err
		}
	}

	rawPat, _ := params["pat"].(string)
	pattern := strings.TrimSpace(rawPat)
	if pattern == "" {
		return sdk.ToolResult{}, fmt.Errorf("`pat` must be a non-empty pattern")
	}

	skip := 0
	if rawSkip, exists := params["skip"]; exists && rawSkip != nil {
		switch v := rawSkip.(type) {
		case float64:
			if math.IsNaN(v) || math.IsInf(v, 0) || math.Floor(v) < 0 {
				return sdk.ToolResult{}, fmt.Errorf("skip must be a non-negative number")
			}
			skip = int(math.Floor(v))
		case int:
			if v < 0 {
				return sdk.ToolResult{}, fmt.Errorf("skip must be a non-negative number")
			}
			skip = v
		default:
			return sdk.ToolResult{}, fmt.Errorf("skip must be a non-negative number")
		}
	}

	// Validate explicit lang against omp's alias table before scanning
	// (upstream: crates/ast/src/ops.rs:25-29, crates/natives/src/ast.rs:334-337).
	rawLang, _ := params["lang"].(string)
	rawLang = strings.TrimSpace(rawLang)
	explicitLang := ""
	if rawLang != "" {
		canon, ok := resolveLangAlias(rawLang)
		if !ok {
			return sdk.ToolResult{}, unsupportedLangError(rawLang)
		}
		explicitLang = canon
	}

	rawPath, _ := params["path"].(string)
	scopedPaths := toPathList(rawPath)
	if len(scopedPaths) == 0 {
		scopedPaths = []string{"."}
	}

	scope, err := resolveToolScope(sdkCtx, cwd, scopedPaths, "search", false)
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

	candidates, err := discoverCandidates(runCtx, cwd, scope, explicitLang)
	if err != nil {
		return sdk.ToolResult{}, err
	}

	langs, byLang, skippedLangs := groupCandidatesByLang(candidates)
	// filesSearched excludes files skipped for missing/unsupported grammars, matching
	// crates/ast/src/search/mod.rs:401-403 & 1091-1094.
	filesSearched := 0
	for _, lang := range langs {
		filesSearched += len(byLang[lang])
	}

	window := newMatchWindow(skip, defaultAstLimit)
	var parseErrors []string
	candByDisplay := make(map[string]candidateFile, len(candidates))
	for _, c := range candidates {
		candByDisplay[c.displayPath] = c
	}

	sessKey := sessionKey(sdkCtx)

	// Run stock-CLI searches per language. Files whose extension maps to a different
	// language (or no language) in stock ast-grep CLI's case-sensitive SgLang::from_path
	// are fed via `--stdin` so explicit `lang` (e.g. `cpp` for `.h`, `tsx` for `.ts`/`.js`,
	// extensionless files) and case-insensitive/dotfile extensions actually match.
	// Source: https://github.com/ast-grep/ast-grep crates/cli/src/run.rs (RunWithSpecificLang).
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

		langCompileFailed := false
		if len(directFiles) > 0 {
			lookup := buildCandidateLookup(directFiles, cwd)
			for _, batch := range batchCandidates(directFiles) {
				// Use attached `--pattern=...` and `--lang=...` so leading `-` values
				// (e.g. `-$A`, `--$I`) are never parsed as flags by clap.
				args := []string{
					"run",
					"--pattern=" + pattern,
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

				stderr, exitCode, runErr := streamAstGrepProcess(runCtx, m.procs, sessKey, binary, cwd, args, nil, func(rm sgMatchJSON) error {
					disp := rm.File
					if cand, ok := resolveCandidateForMatch(rm.File, cwd, lookup); ok {
						disp = cand.displayPath
					} else if rm.File != "" {
						abs := rm.File
						if !filepathIsAbsOrURL(abs) {
							abs = resolveToCwd(abs, cwd)
						}
						disp = formatPathRelativeToCwd(abs, cwd)
					}
					window.add(astFindMatch{
						Path:          disp,
						Text:          rm.Text,
						ByteStart:     rm.Range.ByteOffset.Start,
						ByteEnd:       rm.Range.ByteOffset.End,
						StartLine:     rm.Range.Start.Line + 1,
						StartColumn:   rm.Range.Start.Column + 1,
						EndLine:       rm.Range.End.Line + 1,
						EndColumn:     rm.Range.End.Column + 1,
						MetaVariables: extractMetaVariables(rm.MetaVariables),
					})
					return nil
				})
				if runErr != nil {
					return sdk.ToolResult{}, runErr
				}
				if exitCode == 8 {
					// Pattern failed to compile in this language; record one per-language
					// parse issue and move on to the next language (crates/ast/src/search/mod.rs:1244-1246).
					parseErrors = append(parseErrors, formatPatternCompileError(pattern, lang, stderr))
					langCompileFailed = true
					break
				}
				if stderr != "" {
					for _, d := range parseStderrDiagnostics(stderr, pattern, lang) {
						parseErrors = append(parseErrors, normalizeParseError(d))
					}
				}
			}
		}
		if langCompileFailed {
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
				"--pattern=" + pattern,
				"--lang=" + lang,
				"--json=stream",
				"--stdin",
			}
			stderr, exitCode, runErr := streamAstGrepProcess(runCtx, m.procs, sessKey, binary, cwd, args, bytes.NewReader(data), func(rm sgMatchJSON) error {
				window.add(astFindMatch{
					Path:          f.displayPath,
					Text:          rm.Text,
					ByteStart:     rm.Range.ByteOffset.Start,
					ByteEnd:       rm.Range.ByteOffset.End,
					StartLine:     rm.Range.Start.Line + 1,
					StartColumn:   rm.Range.Start.Column + 1,
					EndLine:       rm.Range.End.Line + 1,
					EndColumn:     rm.Range.End.Column + 1,
					MetaVariables: extractMetaVariables(rm.MetaVariables),
				})
				return nil
			})
			if runErr != nil {
				return sdk.ToolResult{}, runErr
			}
			if exitCode == 8 {
				parseErrors = append(parseErrors, formatPatternCompileError(pattern, lang, stderr))
				break
			}
			if stderr != "" {
				for _, d := range parseStderrDiagnostics(stderr, pattern, lang) {
					parseErrors = append(parseErrors, normalizeParseError(d))
				}
			}
		}
	}

	paged, limitReached := window.page(skip, defaultAstLimit)
	totalMatches := window.total
	filesWithMatches := len(window.files)

	cappedParseErrors, parseErrorsTotal := capParseErrors(parseErrors)
	grammarNote := missingLanguagesNote(skippedLangs)

	fileList := []string{}
	seenFiles := make(map[string]struct{})
	matchesByFile := make(map[string][]astFindMatch)
	for _, hit := range paged {
		if _, seen := seenFiles[hit.Path]; !seen {
			seenFiles[hit.Path] = struct{}{}
			fileList = append(fileList, hit.Path)
		}
		matchesByFile[hit.Path] = append(matchesByFile[hit.Path], hit)
	}

	details := map[string]any{
		"matchCount":    totalMatches,
		"fileCount":     filesWithMatches,
		"filesSearched": filesSearched,
		"limitReached":  limitReached,
		"scopePath":     scope.scopePath,
		"searchPath":    dirs.StandardizeMacOSPath(scope.searchPath),
		"cwd":           dirs.StandardizeMacOSPath(cwd),
		"files":         fileList,
		"fileMatches":   []astGrepFileMatch{},
	}
	if len(cappedParseErrors) > 0 {
		details["parseErrors"] = cappedParseErrors
		details["parseErrorsTotal"] = parseErrorsTotal
	}

	if len(paged) == 0 {
		noMatchMessage := "No matches found"
		if len(cappedParseErrors) > 0 {
			noMatchMessage = "No matches found. Parse issues mean the query may be mis-scoped; narrow `path` before concluding absence."
		}
		if len(cappedParseErrors) > 0 {
			noMatchMessage += "\n" + strings.Join(formatParseErrors(cappedParseErrors, parseErrorsTotal), "\n")
		}
		if grammarNote != "" {
			noMatchMessage += "\n" + grammarNote
		}
		return sdk.ToolResult{
			Content: noMatchMessage,
			Details: details,
		}, nil
	}

	type hashCtx struct {
		tag  string
		path string
	}
	hashContexts := make(map[string]hashCtx, len(fileList))
	for _, relPath := range fileList {
		cand, ok := candByDisplay[relPath]
		if !ok || cand.immutable {
			continue
		}
		if tag := recordSnapshotForFile(sdkCtx, cand.absPath); tag != "" {
			hashContexts[relPath] = hashCtx{tag: tag, path: cand.absPath}
		}
	}

	fileMatchCounts := make(map[string]int, len(fileList))
	rendered := formatFileMatches(fileList, scope.isDirectory, func(relPath string) fileMatchSection {
		fileMatches := matchesByFile[relPath]
		hc, hasHash := hashContexts[relPath]
		lineNumWidth := 0
		for _, hit := range fileMatches {
			lineCount := strings.Count(hit.Text, "\n") + 1
			endLine := hit.StartLine + lineCount - 1
			if w := len(fmt.Sprintf("%d", hit.StartLine)); w > lineNumWidth {
				lineNumWidth = w
			}
			if w := len(fmt.Sprintf("%d", endLine)); w > lineNumWidth {
				lineNumWidth = w
			}
		}
		var modelOut, displayOut []string
		for _, hit := range fileMatches {
			matchLines := strings.Split(hit.Text, "\n")
			for idx, line := range matchLines {
				lineNum := hit.StartLine + idx
				isFirst := idx == 0
				modelOut = append(modelOut, formatMatchLine(lineNum, line, isFirst, hasHash))
				marker := " "
				if isFirst {
					marker = "*"
				}
				displayOut = append(displayOut, formatCodeFrameLine(marker, lineNum, line, lineNumWidth))
			}
			if len(hit.MetaVariables) > 0 {
				keys := make([]string, 0, len(hit.MetaVariables))
				for k := range hit.MetaVariables {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				pairs := make([]string, len(keys))
				for i, k := range keys {
					pairs[i] = k + "=" + hit.MetaVariables[k]
				}
				serialized := strings.Join(pairs, ", ")
				modelOut = append(modelOut, "  meta: "+serialized)
				displayOut = append(displayOut, "  meta: "+singleLine(serialized))
			}
			fileMatchCounts[relPath]++
		}
		if hasHash {
			hashline.ForSession(sdkCtx).RecordSeenLinesFromBody(hc.path, hc.tag, strings.Join(modelOut, "\n"))
		}
		return fileMatchSection{
			model:   modelOut,
			display: displayOut,
			tag:     hc.tag,
		}
	})

	outputLines := rendered.model
	displayLines := rendered.display

	fileMatchesList := make([]astGrepFileMatch, len(fileList))
	for i, fp := range fileList {
		fileMatchesList[i] = astGrepFileMatch{Path: fp, Count: fileMatchCounts[fp]}
	}
	details["fileMatches"] = fileMatchesList
	details["displayContent"] = strings.Join(displayLines, "\n")

	if limitReached {
		outputLines = append(outputLines, "", "Result limit reached; narrow path or use skip to page through results.")
	}
	if len(cappedParseErrors) > 0 {
		outputLines = append(outputLines, "", strings.Join(formatParseErrors(cappedParseErrors, parseErrorsTotal), "\n"))
	}
	if grammarNote != "" {
		outputLines = append(outputLines, "", grammarNote)
	}

	return sdk.ToolResult{
		Content: strings.Join(outputLines, "\n"),
		Details: details,
	}, nil
}

func filepathIsAbsOrURL(p string) bool {
	if isURLLikePath(p) {
		return true
	}
	return len(p) > 0 && (p[0] == '/' || (len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/')))
}
