package pi_subagents

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
)

// Port of src/runs/shared/single-output.ts: the output file of a single run (where the child writes its answer, how the run
// reads it back, and the reference the parent sees).

var readOnlyOutputTools = setOf("read", "grep", "find", "ls", "web_search", "fetch_content", "get_search_content", "source_check", "intercom", "contact_supervisor", "structured_output", "watchdog_diff")

func hasOutputWriteCapability(tools, mcpDirectTools []string, toolsSet bool) bool {
	if len(mcpDirectTools) > 0 || !toolsSet {
		return true
	}
	for _, t := range tools {
		if !readOnlyOutputTools[t] {
			return true
		}
	}
	return false
}

// nodeResolve is path.resolve(parts...): the right-most absolute part and what follows, cleaned; relative to the working
// directory when none is absolute.
func nodeResolve(parts ...string) string {
	resolved := ""
	for i := len(parts) - 1; i >= 0; i-- {
		p := parts[i]
		if p == "" {
			continue
		}
		if resolved == "" {
			resolved = p
		} else {
			resolved = filepath.Join(p, resolved)
		}
		if filepath.IsAbs(p) {
			return filepath.Clean(resolved)
		}
	}
	if resolved == "" {
		wd, _ := os.Getwd()
		return wd
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return filepath.Clean(resolved)
	}
	return abs
}

// singleOutputSnapshot is the state of the output file before a run.
type singleOutputSnapshot struct {
	Exists  bool
	MtimeMs float64
	Size    int64
	Error   string
}

func missingFileStatError(err error) bool {
	c := fsErrorCode(err)
	return c == "ENOENT" || c == "ENOTDIR"
}

func mtimeMs(st fs.FileInfo) float64 { return float64(st.ModTime().UnixNano()) / 1e6 }

// extractChildWrittenOutput is the content the child itself wrote to the output path: its last write tool call whose tool result
// reports success. Nothing (ok false) when there was no such write (bash- or edit-built files).
func extractChildWrittenOutput(messages []any, outputPath, cwd string) (string, bool) {
	if len(messages) == 0 || outputPath == "" {
		return "", false
	}
	comparable := func(p string) string {
		r := nodeResolve(cwd, p)
		if runtime.GOOS == "windows" {
			return strings.ToLower(r)
		}
		return r
	}
	if cwd == "" {
		cwd = "."
	}
	target := comparable(outputPath)
	success := map[string]bool{}
	for _, m := range messages {
		o, ok := asObject(m)
		if !ok || o.vals["role"] != "toolResult" || o.vals["isError"] != false {
			continue
		}
		if id, ok := o.vals["toolCallId"].(string); ok {
			success[id] = true
		}
	}
	content, found := "", false
	for _, m := range messages {
		o, ok := asObject(m)
		if !ok || o.vals["role"] != "assistant" {
			continue
		}
		parts, _ := asArray(o.vals["content"])
		for _, p := range parts {
			part, ok := asObject(p)
			id, _ := part.vals["id"].(string)
			if !ok || part.vals["type"] != "toolCall" || part.vals["name"] != "write" || !success[id] {
				continue
			}
			args, _ := asObject(part.vals["arguments"])
			if args == nil {
				continue
			}
			path, pok := args.vals["path"].(string)
			text, tok := args.vals["content"].(string)
			if !pok || !tok || comparable(path) != target {
				continue
			}
			content, found = text, true
		}
	}
	return content, found
}

// normalizeSingleOutputOverride: false and "false" disable output (disabled true), true and "true" are the agent's default,
// a non-empty string is itself; anything else leaves the output unset (ok false).
func normalizeSingleOutputOverride(output any, defaultOutput string, hasDefault bool) (path string, disabled, ok bool) {
	switch v := output.(type) {
	case bool:
		if !v {
			return "", true, true
		}
		return defaultOutput, false, hasDefault
	case string:
		switch {
		case v == "false":
			return "", true, true
		case v == "true":
			return defaultOutput, false, hasDefault
		case v != "":
			return v, false, true
		}
	}
	return "", false, false
}

// resolveSingleOutputPath is the absolute output path ("" for none): relative paths resolve against relativeBaseDir, else the
// requested cwd (itself resolved from the runtime cwd), else the runtime cwd.
func resolveSingleOutputPath(output any, runtimeCwd, requestedCwd, relativeBaseDir string) string {
	s, ok := output.(string)
	if !ok || s == "" || s == "false" || s == "true" {
		return ""
	}
	if filepath.IsAbs(s) {
		return s
	}
	if relativeBaseDir != "" {
		return nodeResolve(relativeBaseDir, s)
	}
	base := runtimeCwd
	if requestedCwd != "" {
		if filepath.IsAbs(requestedCwd) {
			base = requestedCwd
		} else {
			base = nodeResolve(runtimeCwd, requestedCwd)
		}
	}
	return nodeResolve(base, s)
}

// outputInstructionCapabilities is the agent's tools as the output instruction reads them (ToolsSet false: unrestricted).
type outputInstructionCapabilities struct {
	Tools          []string
	ToolsSet       bool
	McpDirectTools []string
}

func formatOutputPathInstruction(outputPath string, caps *outputInstructionCapabilities) string {
	var delivery string
	if caps == nil || hasOutputWriteCapability(caps.Tools, caps.McpDirectTools, caps.ToolsSet) {
		delivery = "Write your findings to exactly this path: " + outputPath
	} else {
		delivery = strings.Join([]string{
			"Return the complete artifact in your final response.",
			"The runtime will persist it to exactly this path: " + outputPath,
			"Do not call contact_supervisor merely because no write-capable tool is available.",
		}, "\n")
	}
	return strings.Join([]string{
		delivery,
		"This path is authoritative for this run.",
		"Ignore any other output filename or output path mentioned elsewhere, including output destinations in the base agent prompt, system prompt, or task instructions.",
	}, "\n")
}

var requestedOutputLine = regexp.MustCompile(`(?i)^\s*(?:Write your findings to(?: exactly this path)?:|The runtime will persist it to exactly this path:)\s*(.+?)\s*$`)

// requestedOutputPathFromTask finds the output path an instruction in the task names (the last such line wins).
func requestedOutputPathFromTask(task string) (string, bool) {
	lines := regexp.MustCompile(`\r?\n`).Split(task, -1)
	for i := len(lines) - 1; i >= 0; i-- {
		m := requestedOutputLine.FindStringSubmatch(lines[i])
		if m == nil || m[1] == "" {
			continue
		}
		req := jsTrim(m[1])
		if strings.HasPrefix(req, "`") && strings.HasSuffix(req, "`") && len(req) >= 2 {
			req = req[1 : len(req)-1]
		}
		return req, true
	}
	return "", false
}

type outputPathMapping struct{ RequestedPath, SavedPath string }

func outputPathMappingFromTask(task, savedPath string) (outputPathMapping, bool) {
	requested, ok := requestedOutputPathFromTask(task)
	if !ok || savedPath == "" {
		return outputPathMapping{}, false
	}
	if filepath.IsAbs(requested) && filepath.Clean(requested) == filepath.Clean(savedPath) {
		return outputPathMapping{}, false
	}
	return outputPathMapping{requested, savedPath}, true
}

func injectSingleOutputInstruction(task, outputPath string, caps *outputInstructionCapabilities) string {
	if outputPath == "" {
		return task
	}
	return task + "\n\n---\n**Output:**\n" + formatOutputPathInstruction(outputPath, caps)
}

func injectOutputPathSystemPrompt(systemPrompt, outputPath string, caps *outputInstructionCapabilities) string {
	if outputPath == "" {
		return systemPrompt
	}
	instruction := "Runtime output path override:\n" + formatOutputPathInstruction(outputPath, caps)
	if systemPrompt != "" {
		return systemPrompt + "\n\n" + instruction
	}
	return instruction
}

var lineBreak = regexp.MustCompile(`\r\n|\r|\n`)

func countLines(text string) int {
	if text == "" {
		return 0
	}
	n := len(lineBreak.FindAllString(text, -1))
	if strings.HasSuffix(text, "\n") || strings.HasSuffix(text, "\r") {
		return n
	}
	return n + 1
}

func formatByteSize(bytes int) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	value, unit := float64(bytes)/1024, 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	return jsToFixed(value, 1) + " " + units[unit]
}

// savedOutputReference is the original's SavedOutputReference.
type savedOutputReference struct {
	Path         string
	Bytes, Lines int
	Message      string
}

func formatSavedOutputReference(savedPath, fullOutput string) savedOutputReference {
	abs := nodeResolve(savedPath)
	bytes, lines := len(fullOutput), countLines(fullOutput)
	word := "lines"
	if lines == 1 {
		word = "line"
	}
	return savedOutputReference{abs, bytes, lines, fmt.Sprintf("Output saved to: %s (%s, %d %s). Read this file if needed.", abs, formatByteSize(bytes), lines, word)}
}

// validateFileOnlyOutputMode is the refusal of outputMode "file-only" without an output file ("" when fine).
func validateFileOnlyOutputMode(outputMode, outputPath, context string) string {
	if outputMode == "file-only" && outputPath == "" {
		return context + ` sets outputMode: "file-only" but does not configure an output file. Set output to a path or use outputMode: "inline".`
	}
	return ""
}

// resolveSingleOutputClaimPath is the real path of the nearest existing ancestor with the missing rest appended.
func resolveSingleOutputClaimPath(outputPath string) (string, error) {
	existing := nodeResolve(outputPath)
	var missing []string
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		if _, err := os.Stat(existing); err == nil {
			break
		}
		missing = append([]string{filepath.Base(existing)}, missing...)
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		existing = parent
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", errors.New(nodeFSErrorText(err, "realpath", existing))
	}
	return filepath.Join(append([]string{real}, missing...)...), nil
}

func outputClaimError(outputPath, expectedClaimPath string) string {
	if expectedClaimPath != "" {
		if got, err := resolveSingleOutputClaimPath(outputPath); err != nil || got != expectedClaimPath {
			return "Output path changed after it was claimed."
		}
	}
	return ""
}

// captureSingleOutputSnapshot records the output file before the run (nil when there is no output path).
func captureSingleOutputSnapshot(outputPath string) *singleOutputSnapshot {
	if outputPath == "" {
		return nil
	}
	st, err := os.Stat(outputPath)
	if err != nil {
		if missingFileStatError(err) {
			return &singleOutputSnapshot{}
		}
		return &singleOutputSnapshot{Error: nodeFSErrorText(err, "stat", outputPath)}
	}
	return &singleOutputSnapshot{Exists: true, MtimeMs: mtimeMs(st), Size: st.Size()}
}

func inspectSingleOutputChange(outputPath string, before *singleOutputSnapshot) (changed bool, errMsg string) {
	if before != nil && before.Error != "" {
		return false, before.Error
	}
	st, err := os.Stat(outputPath)
	if err != nil {
		if missingFileStatError(err) {
			return false, ""
		}
		return false, nodeFSErrorText(err, "stat", outputPath)
	}
	return before == nil || !before.Exists || mtimeMs(st) != before.MtimeMs || st.Size() != before.Size, ""
}

// hasSingleOutputChangedSinceSnapshot: whether the file changed (known false when it could not be inspected).
func hasSingleOutputChangedSinceSnapshot(outputPath string, before *singleOutputSnapshot) (changed, known bool) {
	if outputPath == "" {
		return false, false
	}
	c, e := inspectSingleOutputChange(outputPath, before)
	return c, e == ""
}

func persistSingleOutput(outputPath, fullOutput, expectedClaimPath string) (saved, errMsg string, fatal bool) {
	if outputPath == "" {
		return "", "", false
	}
	if e := outputClaimError(outputPath, expectedClaimPath); e != "" {
		return "", e, true
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o777); err != nil {
		return "", nodeFSErrorText(err, "mkdir", filepath.Dir(outputPath)), false
	}
	if err := os.WriteFile(outputPath, []byte(fullOutput), 0o666); err != nil {
		return "", nodeFSErrorText(err, "open", outputPath), false
	}
	return outputPath, "", false
}

// resolvedSingleOutput is the result of resolveSingleOutput.
type resolvedSingleOutput struct {
	FullOutput string
	SavedPath  string
	SaveError  string
	Fatal      bool
}

// resolveSingleOutput reads back the output file when the child changed it, else saves the fallback output there.
func resolveSingleOutput(outputPath, fallbackOutput string, before *singleOutputSnapshot, expectedClaimPath string) resolvedSingleOutput {
	if outputPath == "" {
		return resolvedSingleOutput{FullOutput: fallbackOutput}
	}
	if e := outputClaimError(outputPath, expectedClaimPath); e != "" {
		return resolvedSingleOutput{FullOutput: fallbackOutput, SaveError: e, Fatal: true}
	}
	changed, e := inspectSingleOutputChange(outputPath, before)
	if e != "" {
		return resolvedSingleOutput{FullOutput: fallbackOutput, SaveError: "Failed to inspect output file: " + e}
	}
	if changed {
		data, err := os.ReadFile(outputPath)
		if err != nil {
			text := nodeFSErrorText(err, "open", outputPath)
			if errors.Is(err, syscall.EISDIR) {
				text = nodeFSErrorText(err, "read", "")
			}
			return resolvedSingleOutput{FullOutput: fallbackOutput, SaveError: "Failed to read changed output file: " + text}
		}
		return resolvedSingleOutput{FullOutput: string(data), SavedPath: outputPath}
	}
	saved, serr, fatal := persistSingleOutput(outputPath, fallbackOutput, expectedClaimPath)
	if saved != "" {
		return resolvedSingleOutput{FullOutput: fallbackOutput, SavedPath: saved}
	}
	return resolvedSingleOutput{FullOutput: fallbackOutput, SaveError: serr, Fatal: fatal}
}

// finalizeSingleOutputInput is the original's finalizeSingleOutput parameters.
type finalizeSingleOutputInput struct {
	FullOutput          string
	TruncatedOutput     string
	OutputPath          string
	OutputMode          string
	ExitCode            int
	PreserveSavedOutput bool
	SavedPath           string
	OutputReference     *savedOutputReference
	SaveError           string
}

type finalizedSingleOutput struct {
	DisplayOutput   string
	SavedPath       string
	OutputReference *savedOutputReference
	SaveError       string
}

// finalizeSingleOutput formats the saved-path messaging around the already resolved output.
func finalizeSingleOutput(p finalizeSingleOutputInput) finalizedSingleOutput {
	display := p.TruncatedOutput
	if display == "" {
		display = p.FullOutput
	}
	if (p.ExitCode == 0 || p.PreserveSavedOutput) && p.SavedPath != "" {
		ref := p.OutputReference
		if ref == nil {
			r := formatSavedOutputReference(p.SavedPath, p.FullOutput)
			ref = &r
		}
		if p.OutputMode == "file-only" {
			return finalizedSingleOutput{DisplayOutput: ref.Message, SavedPath: p.SavedPath, OutputReference: ref}
		}
		if !strings.Contains(display, ref.Message) {
			display += "\n\n" + ref.Message
		}
		return finalizedSingleOutput{DisplayOutput: display, SavedPath: p.SavedPath, OutputReference: ref}
	}
	if p.ExitCode == 0 && p.SaveError != "" && p.OutputPath != "" {
		display += "\n\nOutput file error: " + p.OutputPath + "\n" + p.SaveError
		return finalizedSingleOutput{DisplayOutput: display, SaveError: p.SaveError}
	}
	return finalizedSingleOutput{DisplayOutput: display}
}
