package ansi

import (
	"fmt"
	"regexp"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

func init() {
	registerTerminal("bash", Builder{Call: termBashCall, Result: termBashResult})
}

// ═══════════════════════════════════════════════════════════════════════════
// bash tool (terminal card)
// upstream: packages/tui/src/tools/bash.ts
// ═══════════════════════════════════════════════════════════════════════════

var (
	bashTimeoutRe     = regexp.MustCompile(`^\[Command timed out(?: after .+)?\]$`)
	bashExitCodeRe    = regexp.MustCompile(`^Command exited with code (-?\d+)$`)
	bashWallTimeRe    = regexp.MustCompile(`^Wall time: [\d.]+ (?:ms|seconds?)$`)
	bashRawArtifactRe = regexp.MustCompile(`^\[raw output:\s*([^\]]+)\]$`)
)

// isBackgroundNotice mirrors packages/tui/src/tools/bash.ts:81-84:
// matches `Backgrounded as job <jobId>` followed by `;` or ` `.
func isBackgroundNotice(line, jobID string) bool {
	if jobID == "" {
		return false
	}
	prefix := "Backgrounded as job " + jobID
	if !strings.HasPrefix(line, prefix) || len(line) <= len(prefix) {
		return false
	}
	next := line[len(prefix)]
	return next == ';' || next == ' '
}

// stripBashNotices strips trailing LLM-facing notices in omp's exact order
// (packages/tui/src/tools/bash.ts:354-373):
// 1. background notice
// 2. output/truncation notice (and timeout bracket notice)
// 3. exit-code notice
// 4. wall-time notice
// 5. raw-output artifact reference (returning the exact spill URL/id)
func stripBashNotices(raw string, details map[string]any) (text string, artifactID string) {
	cur := raw

	// 1. Background notice (when async.state == "running", tools/bash.ts:359-362)
	if asyncObj := asMap(details["async"]); strField(asyncObj, "state") == "running" {
		jobID := strField(asyncObj, "jobId")
		cur = stripTrailingNotice(cur, func(line string) bool {
			return isBackgroundNotice(line, jobID)
		})
	}
	// 2. Output truncation notice (and trailing [Command timed out ...] notice)
	cur = stripOutputNotice(cur, asMap(details["meta"]))
	if timedOut, _ := boolField(details, "timedOut"); timedOut {
		cur = stripTrailingNotice(cur, func(line string) bool {
			return bashTimeoutRe.MatchString(line)
		})
	}

	// 3. Exit-code notice
	if exitCode, ok := intField(details, "exitCode"); ok {
		exact := fmt.Sprintf("Command exited with code %d", exitCode)
		cur = stripTrailingNotice(cur, func(line string) bool {
			return line == exact || bashExitCodeRe.MatchString(line)
		})
	} else {
		cur = stripTrailingNotice(cur, func(line string) bool {
			return bashExitCodeRe.MatchString(line)
		})
	}

	// 4. Wall-time notice (unless wallTimeNoticeEmitted == false)
	wallEmitted, hasWallFlag := boolField(details, "wallTimeNoticeEmitted")
	if !hasWallFlag || wallEmitted {
		cur = stripTrailingNotice(cur, func(line string) bool {
			return bashWallTimeRe.MatchString(line)
		})
	}

	// 5. Raw-output artifact reference (`[raw output: local://7.log]` -> preserve `local://7.log`)
	cur = stripTrailingNotice(cur, func(line string) bool {
		if m := bashRawArtifactRe.FindStringSubmatch(line); m != nil {
			artifactID = strings.TrimSpace(m[1])
			return true
		}
		return false
	})

	return cur, artifactID
}

// formatBashCommandLines mirrors packages/tui/src/tools/bash.ts:316-332 & 569-582:
// renders non-empty commands via the Go shell visualizer (`BuildShellVizPresentation`
// + `RenderShellVizTerminal`), prefixed with a `$ cd <dir> &&` context line when a
// non-default working directory is active. Falls back to `$ …` when command is empty.
func (p palette) formatBashCommandLines(args map[string]any, details map[string]any, sessionCwd string, contentWidth int, running bool, exitCode *int) []string {
	cmd := strField(args, "command")
	cwd := strField(args, "cwd")
	if cwd == "" {
		cwd = strField(details, "cwd")
	}
	cdSep := strField(details, "cdSeparator")
	if cdSep == "" {
		cdSep = " &&"
	}
	displayWorkdir := formatToolWorkingDirectory(cwd, sessionCwd)

	prefixParts := []string{"$"}
	if displayWorkdir != "" {
		prefixParts = append(prefixParts, fmt.Sprintf("cd %s%s", displayWorkdir, cdSep))
	}
	prefix := p.fg("dim", strings.Join(prefixParts, " ")+" ")

	if strings.TrimSpace(cmd) == "" || strings.TrimSpace(cmd) == "…" {
		if cmd == "" {
			cmd = "…"
		}
		lines := sanitizeDisplayLines(cmd)
		if len(lines) == 0 {
			return []string{strings.TrimRight(prefix, " ")}
		}
		out := make([]string, len(lines))
		for i, l := range lines {
			if i == 0 {
				out[i] = prefix + p.fg("text", l)
			} else {
				out[i] = p.fg("text", l)
			}
		}
		return out
	}

	if contentWidth < 1 {
		contentWidth = 80
	}
	pres := BuildShellVizPresentation(cmd, running, exitCode, "")
	vizLines := RenderShellVizTerminal(p, pres, contentWidth)
	if len(vizLines) == 0 {
		return []string{strings.TrimRight(prefix, " ")}
	}
	if displayWorkdir != "" {
		return append([]string{strings.TrimRight(prefix, " ")}, vizLines...)
	}
	return vizLines
}

func serviceStateSymbol(state string) string {
	low := strings.ToLower(state)
	switch {
	case strings.Contains(low, "fail"), strings.Contains(low, "error"), strings.Contains(low, "crash"), strings.Contains(low, "dead"):
		return unicodeSymbols.statusError
	case strings.Contains(low, "stop"), strings.Contains(low, "exit"), strings.Contains(low, "cancel"), strings.Contains(low, "kill"):
		return unicodeSymbols.statusAborted
	case strings.Contains(low, "start"), strings.Contains(low, "pending"), strings.Contains(low, "wait"), strings.Contains(low, "init"), strings.Contains(low, "restart"):
		return unicodeSymbols.statusPending
	case strings.Contains(low, "run"), strings.Contains(low, "ready"), strings.Contains(low, "up"), strings.Contains(low, "alive"), strings.Contains(low, "active"):
		return unicodeSymbols.statusSuccess
	default:
		return unicodeSymbols.statusInfo
	}
}

// bashStatsParts mirrors packages/tui/src/tools/bash.ts:384-452.
func (p palette) bashStatsParts(details map[string]any, artifactID string, isErr bool) []string {
	dim := func(s string) string { return p.fg("dim", s) }
	var parts []string

	if asyncObj := asMap(details["async"]); strField(asyncObj, "state") == "running" {
		if jid := strField(asyncObj, "jobId"); jid != "" {
			parts = append(parts, dim(unicodeSymbols.iconJob+" "+jid))
		}
	}
	if svc := asMap(details["service"]); svc != nil {
		if name := strField(svc, "name"); name != "" {
			parts = append(parts, dim(unicodeSymbols.iconServer+" "+name))
		}
		if st := strField(svc, "state"); st != "" {
			parts = append(parts, dim(serviceStateSymbol(st)+" "+st))
		}
		ready, _ := boolField(svc, "ready")
		svcTimedOut, _ := boolField(svc, "timedOut")
		switch {
		case ready:
			parts = append(parts, dim(unicodeSymbols.statusSuccess+" ready"))
		case svcTimedOut:
			parts = append(parts, dim(unicodeSymbols.statusWarning+" ready timed out"))
		default:
			parts = append(parts, dim(unicodeSymbols.statusError+" not ready"))
		}
		if pid, ok := intField(svc, "pid"); ok && pid > 0 {
			parts = append(parts, dim(fmt.Sprintf("%s%d", unicodeSymbols.iconPID, pid)))
		}
	}

	timeoutDisabled, _ := boolField(details, "timeoutDisabled")
	timeoutSec, hasTimeout := floatField(details, "timeoutSeconds")
	reqTimeoutSec, hasReqTimeout := floatField(details, "requestedTimeoutSeconds")
	wallTimeMs, hasWall := floatField(details, "wallTimeMs")

	timeoutLabel := ""
	if timeoutDisabled {
		timeoutLabel = "disabled"
	} else if hasTimeout && timeoutSec > 0 {
		timeoutLabel = formatDuration(int64(timeoutSec * 1000))
		if hasReqTimeout && reqTimeoutSec != timeoutSec {
			timeoutLabel += fmt.Sprintf(" (requested %s clamped)", formatDuration(int64(reqTimeoutSec*1000)))
		}
	}

	switch {
	case hasWall:
		elapsed := formatDuration(int64(wallTimeMs))
		if timeoutLabel == "" {
			parts = append(parts, dim(unicodeSymbols.iconTime+" "+elapsed))
		} else {
			parts = append(parts, dim(unicodeSymbols.iconTime+" "+elapsed+"/"+timeoutLabel))
		}
	case timeoutDisabled:
		parts = append(parts, dim(unicodeSymbols.statusDisabled+" disabled"))
	case timeoutLabel != "":
		parts = append(parts, dim(unicodeSymbols.statusPending+" "+timeoutLabel))
	}

	if artifactID != "" {
		parts = append(parts, dim(unicodeSymbols.iconOutput+" "+spillURL(artifactID)))
	}
	if meta := asMap(details["meta"]); meta != nil {
		if minObj := asMap(meta["minimized"]); minObj != nil {
			if txt := formatMinimizedMeta(minObj); txt != "" {
				parts = append(parts, dim(unicodeSymbols.iconMinimize+" "+txt))
			}
		}
	}
	if isErr {
		if exitCode, ok := intField(details, "exitCode"); ok {
			if exitCode != 0 {
				parts = append(parts, p.fg("error", fmt.Sprintf("%s %d", unicodeSymbols.statusError, exitCode)))
			} else {
				parts = append(parts, p.fg("success", fmt.Sprintf("%s %d", unicodeSymbols.statusSuccess, exitCode)))
			}
		}
	}
	return parts
}

func callIntentHeader(p palette, args map[string]any) string {
	intent := flattenLine(strField(args, "i"))
	if intent == "" {
		return ""
	}
	return p.renderStatusLine(statusLineOpts{title: intent, titleColor: "muted"})
}

// termBashCall mirrors packages/tui/src/tools/bash.ts:659-701 with showHeader: false.
func termBashCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	header := callIntentHeader(p, args)
	contentW := outputBlockContentWidth(width, nil, nil)
	cmdLines := p.formatBashCommandLines(args, nil, ctx.Cwd(), contentW, true, nil)
	capped := p.capPreviewLines(cmdLines, previewWindowFallback, false, "", true)
	rows := p.renderOutputBlock(blockOpts{
		header:      header,
		state:       "pending",
		borderColor: "accent",
		sections:    []blockSection{{lines: capped}},
		width:       width,
	})
	return termLinesNode(rows, width), nil
}

// termBashResult mirrors packages/tui/src/tools/bash.ts:703-933 with showHeader: false:
//   - Top border has NO invented "Bash" header; carries intent only when args.i is set.
//   - Always draws the `Output` section divider (even when output is empty).
//   - Wraps long output lines at contentWidth; caps collapsed tail visually with
//     `… (N earlier lines, showing M of T) ⟦Ctrl+O: Expand⟧`.
//   - Truncation warning rides inside the Output section.
//   - State/time/facts live ONLY in the bottom border (`footerMeta`), with zero
//     extra unframed run-foot rows.
func termBashResult(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	details := asMap(res.Details)
	isErr := rc.IsError
	isPartial := opts.IsPartial
	isTimeout, _ := boolField(details, "timedOut")
	success := !isPartial && !isErr

	header := callIntentHeader(p, rc.Args)

	rawOutput := resultText(res)
	stripped, artifactID := stripBashNotices(rawOutput, details)
	displayOutput := strings.TrimRight(stripped, " \t\r\n")

	statsParts := p.bashStatsParts(details, artifactID, isErr)
	footerMeta := strings.Join(statsParts, p.fg("dim", unicodeSymbols.sepDot))

	var warningLine string
	if meta := asMap(details["meta"]); meta != nil {
		if strField(meta, "artifactError") != "" || asMap(meta["truncation"]) != nil {
			warningLine = p.formatMetaTruncation(meta)
		}
	}

	contentW := outputBlockContentWidth(width, nil, nil)

	// Nested toolCommands dispatch cards (upstream: packages/tui/src/tools/bash.ts:721,867-916):
	// When a bash call's sole invocation is a mounted tool command (`reject`, `resolve`, etc.),
	// render its inner result card directly inside the bash block and suppress the duplicate Output section.
	dispatches := asSlice(details["toolCommands"])
	soleToolCmd, _ := boolField(details, "toolCommandsSole")
	var commandCardLines []string
	if len(dispatches) > 0 {
		for _, raw := range dispatches {
			dm := asMap(raw)
			if dm == nil {
				continue
			}
			tool := strField(dm, "tool")
			mode := strField(dm, "mode")
			exitCode, _ := intField(dm, "exitCode")
			inner := asMap(dm["inner"])
			if mode == "execute" && (tool == "resolve" || tool == "reject") && inner != nil {
				commandCardLines = append(commandCardLines, p.renderResolveBannerLines(inner, exitCode != 0, contentW)...)
			}
		}
	}

	var outputLines []string
	if !(soleToolCmd && len(commandCardLines) > 0) {
		var rawLines []string
		if strings.TrimSpace(displayOutput) != "" {
			rawLines = sanitizeDisplayLines(displayOutput)
		}
		paneLines, _ := p.formatOutputPaneLines(outputPaneOpts{
			lines:             rawLines,
			expanded:          opts.Expanded,
			collapsedMaxLines: min(defaultTerminalLines, previewWindowFallback),
			edge:              "tail",
			visual:            true,
			width:             contentW,
			styleLine:         func(line string) string { return p.fg("toolOutput", line) },
		})
		outputLines = append([]string(nil), paneLines...)
	}
	if warningLine != "" {
		outputLines = append(outputLines, warningLine)
	}

	var cmdLines []string
	if rc.Args != nil {
		var exitPtr *int
		if ec, ok := intField(details, "exitCode"); ok {
			exitPtr = &ec
		}
		cmdLines = p.capPreviewLines(p.formatBashCommandLines(rc.Args, details, ctx.Cwd(), contentW, isPartial, exitPtr), previewWindowFallback, opts.Expanded, "", true)
	}

	borderColor := "dim"
	switch {
	case isPartial:
		borderColor = "accent"
	case isErr && isTimeout:
		borderColor = "warning"
	case isErr:
		borderColor = "error"
	case success:
		borderColor = "dim"
	}

	sections := []blockSection{{lines: cmdLines}}
	if len(commandCardLines) > 0 {
		sections = append(sections, blockSection{lines: commandCardLines})
	}
	if !(soleToolCmd && len(commandCardLines) > 0 && len(outputLines) == 0) {
		sections = append(sections, blockSection{label: p.fg("toolTitle", "Output"), lines: outputLines})
	}

	bashState := "success"
	switch {
	case isPartial:
		bashState = "partial"
	case isErr && isTimeout:
		bashState = "warning"
	case isErr:
		bashState = "error"
	}
	rows := p.renderOutputBlock(blockOpts{
		header:      header,
		footerMeta:  footerMeta,
		state:       bashState,
		noBg:        true, // packages/tui/src/tools/bash.ts:895 (applyBg: false)
		borderColor: borderColor,
		sections:    sections,
		width:       width,
	})
	return termLinesNode(rows, width), nil
}
