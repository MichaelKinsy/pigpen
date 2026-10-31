package pi_subagents

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The per-call execution parameters this port can honor in its child-process
// model (the original's subagent-executor): the run deadline (timeoutMs with
// its maxRuntimeMs alias) and the durable output binding (output, outputMode).
// Everything else the schema declares stays refused (refuse.go).

func init() {
	registerRunParams("timeoutMs", "maxRuntimeMs", "output", "outputMode")
}

// resolveRunDeadlineMs is the original's alias agreement; the second return is
// the run's deadline (0 keeps the configured default).
func resolveRunDeadlineMs(params map[string]any) (int64, string) {
	timeout, hasTimeout := positiveInt64Param(params, "timeoutMs")
	maxRuntime, hasMax := positiveInt64Param(params, "maxRuntimeMs")
	if _, present := params["timeoutMs"]; present && !hasTimeout {
		return 0, "timeoutMs must be an integer >= 1."
	}
	if _, present := params["maxRuntimeMs"]; present && !hasMax {
		return 0, "maxRuntimeMs must be an integer >= 1."
	}
	if hasTimeout && hasMax && timeout != maxRuntime {
		return 0, "timeoutMs and maxRuntimeMs are aliases; provide only one value or use the same value for both."
	}
	if hasTimeout {
		return timeout, ""
	}
	if hasMax {
		return maxRuntime, ""
	}
	return 0, ""
}

// int64Param reads a positive integer parameter.
func positiveInt64Param(params map[string]any, name string) (int64, bool) {
	switch v := params[name].(type) {
	case float64:
		if v == float64(int64(v)) && v >= 1 {
			return int64(v), true
		}
	case int:
		if v >= 1 {
			return int64(v), true
		}
	case int64:
		if v >= 1 {
			return v, true
		}
	}
	return 0, false
}

// outputBinding is the resolved durable-output request of one call.
type outputBinding struct {
	Path     string // absolute path, "" when none
	Suppress bool   // output: false keeps no output in the result
	FileOnly bool
}

// resolveOutputBinding is the original's output/outputMode handling for one run.
func resolveOutputBinding(params map[string]any, cwd, context string) (outputBinding, string) {
	var out outputBinding
	if v, present := params["outputMode"]; present {
		mode, isString := v.(string)
		if !isString || (mode != "inline" && mode != "file-only") {
			return out, "outputMode must be 'inline' or 'file-only'."
		}
		out.FileOnly = mode == "file-only"
	}
	if v, present := params["output"]; present {
		switch t := v.(type) {
		case bool:
			if !t {
				out.Suppress = true
			}
		case string:
			if jsTrim(t) != "" {
				out.Path = jsTrim(t)
			}
		}
	}
	if out.FileOnly && out.Path == "" {
		return out, fmt.Sprintf("%s sets outputMode: \"file-only\" but does not configure an output file. Set output to a path or use outputMode: \"inline\".", context)
	}
	if out.Path != "" && !filepath.IsAbs(out.Path) {
		out.Path = filepath.Join(cwd, out.Path)
	}
	return out, ""
}

// injectSingleOutputInstruction is the original's task suffix when an output
// path is bound to the run.
func injectSingleOutputInstruction(task, outputPath string) string {
	if outputPath == "" {
		return task
	}
	instruction := fmt.Sprintf("Write your findings to exactly this path: %s\nThis path is authoritative for this run.\nIgnore any other output filename or output path mentioned elsewhere, including output destinations in the base agent prompt, system prompt, or task instructions.", outputPath)
	return task + "\n\n---\n**Output:**\n" + instruction
}

// countOutputLines is the original's line count (a trailing newline adds none).
func countOutputLines(text string) int {
	if text == "" {
		return 0
	}
	n := strings.Count(text, "\n")
	if strings.HasSuffix(text, "\n") {
		return n
	}
	return n + 1
}

// formatByteSize is the original's formatByteSize ("12 B", "1.5 KB").
func formatByteSize(bytes int) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	value := float64(bytes) / 1024
	i := 0
	for value >= 1024 && i < len(units)-1 {
		value /= 1024
		i++
	}
	return jsToFixed(value, 1) + " " + units[i]
}

// savedOutputReference is the original's formatSavedOutputReference: the child's
// answer lands in the bound file and the result carries this message.
func savedOutputReference(path, fullOutput string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(absolute, []byte(fullOutput), 0o644); err != nil {
		return "", err
	}
	bytes := len(fullOutput)
	lines := countOutputLines(fullOutput)
	lineWord := "lines"
	if lines == 1 {
		lineWord = "line"
	}
	return fmt.Sprintf("Output saved to: %s (%s, %d %s). Read this file if needed.", absolute, formatByteSize(bytes), lines, lineWord), nil
}
