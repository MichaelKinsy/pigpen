package debug

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// upstream: packages/coding-agent/src/tools/debug.ts
// upstream: packages/tui/src/tools/debug.ts

// FormatLocation formats a debugger stop location as path:line[:column].
// upstream: packages/tui/src/tools/debug.ts:63-68
func FormatLocation(snapshot *DapSessionSummary) string {
	if snapshot == nil || snapshot.Source == nil || snapshot.Source.Path == "" || snapshot.Line == nil {
		return ""
	}
	if snapshot.Column != nil {
		return fmt.Sprintf("%s:%d:%d", snapshot.Source.Path, *snapshot.Line, *snapshot.Column)
	}
	return fmt.Sprintf("%s:%d", snapshot.Source.Path, *snapshot.Line)
}

// FormatSessionSnapshot formats the session snapshot lines matching omp's formatSessionSnapshot.
// upstream: packages/tui/src/tools/debug.ts:71-91
func FormatSessionSnapshot(snapshot DapSessionSummary) []string {
	lines := []string{
		fmt.Sprintf("Session %s", snapshot.ID),
		fmt.Sprintf("Adapter: %s", snapshot.Adapter),
		fmt.Sprintf("Status: %s", snapshot.Status),
		fmt.Sprintf("CWD: %s", snapshot.Cwd),
	}
	if snapshot.Program != "" {
		lines = append(lines, fmt.Sprintf("Program: %s", snapshot.Program))
	}
	if snapshot.StopReason != "" {
		lines = append(lines, fmt.Sprintf("Stop reason: %s", snapshot.StopReason))
	}
	if snapshot.FrameName != "" {
		lines = append(lines, fmt.Sprintf("Frame: %s", snapshot.FrameName))
	}
	if snapshot.InstructionPointerReference != "" {
		lines = append(lines, fmt.Sprintf("Instruction pointer: %s", snapshot.InstructionPointerReference))
	}
	if loc := FormatLocation(&snapshot); loc != "" {
		lines = append(lines, fmt.Sprintf("Location: %s", loc))
	}
	if snapshot.NeedsConfigurationDone {
		lines = append(lines, "Configuration: pending configurationDone; set breakpoints, then continue.")
	}
	if snapshot.ExitCode != nil {
		lines = append(lines, fmt.Sprintf("Exit code: %d", *snapshot.ExitCode))
	}
	return lines
}

func formatBreakpoints(filePath string, breakpoints []DapBreakpointRecord) string {
	lines := []string{fmt.Sprintf("Breakpoints for %s:", filePath)}
	if len(breakpoints) == 0 {
		lines = append(lines, "(none)")
		return strings.Join(lines, "\n")
	}
	for _, bp := range breakpoints {
		status := "pending"
		if bp.Verified {
			status = "verified"
		}
		var b strings.Builder
		fmt.Fprintf(&b, "- line %d: %s", bp.Line, status)
		if bp.Condition != "" {
			fmt.Fprintf(&b, " if %s", bp.Condition)
		}
		if bp.Message != "" {
			fmt.Fprintf(&b, " (%s)", bp.Message)
		}
		lines = append(lines, b.String())
	}
	return strings.Join(lines, "\n")
}

func formatFunctionBreakpoints(breakpoints []DapFunctionBreakpointRecord) string {
	lines := []string{"Function breakpoints:"}
	if len(breakpoints) == 0 {
		lines = append(lines, "(none)")
		return strings.Join(lines, "\n")
	}
	for _, bp := range breakpoints {
		status := "pending"
		if bp.Verified {
			status = "verified"
		}
		var b strings.Builder
		fmt.Fprintf(&b, "- %s: %s", bp.Name, status)
		if bp.Condition != "" {
			fmt.Fprintf(&b, " if %s", bp.Condition)
		}
		if bp.Message != "" {
			fmt.Fprintf(&b, " (%s)", bp.Message)
		}
		lines = append(lines, b.String())
	}
	return strings.Join(lines, "\n")
}

func formatStackFrames(frames []DapStackFrame) string {
	lines := []string{"Stack trace:"}
	if len(frames) == 0 {
		lines = append(lines, "(empty)")
		return strings.Join(lines, "\n")
	}
	for _, frame := range frames {
		loc := fmt.Sprintf("<unknown>:%d:%d", frame.Line, frame.Column)
		if frame.Source != nil && frame.Source.Path != "" {
			loc = fmt.Sprintf("%s:%d:%d", frame.Source.Path, frame.Line, frame.Column)
		}
		lines = append(lines, fmt.Sprintf("- #%d %s @ %s", frame.ID, frame.Name, loc))
	}
	return strings.Join(lines, "\n")
}

func formatThreads(threads []DapThread) string {
	lines := []string{"Threads:"}
	if len(threads) == 0 {
		lines = append(lines, "(none)")
		return strings.Join(lines, "\n")
	}
	for _, th := range threads {
		lines = append(lines, fmt.Sprintf("- %d: %s", th.ID, th.Name))
	}
	return strings.Join(lines, "\n")
}

func formatScopes(scopes []DapScope) string {
	lines := []string{"Scopes:"}
	if len(scopes) == 0 {
		lines = append(lines, "(none)")
		return strings.Join(lines, "\n")
	}
	for _, sc := range scopes {
		exp := "no"
		if sc.Expensive {
			exp = "yes"
		}
		line := fmt.Sprintf("- %s: ref=%d, expensive=%s", sc.Name, sc.VariablesReference, exp)
		if sc.PresentationHint != "" {
			line += fmt.Sprintf(", hint=%s", sc.PresentationHint)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func formatVariables(variables []DapVariable) string {
	lines := []string{"Variables:"}
	if len(variables) == 0 {
		lines = append(lines, "(none)")
		return strings.Join(lines, "\n")
	}
	for _, v := range variables {
		var b strings.Builder
		fmt.Fprintf(&b, "- %s = %s", v.Name, v.Value)
		if v.Type != "" {
			fmt.Fprintf(&b, " (%s)", v.Type)
		}
		if v.VariablesReference > 0 {
			fmt.Fprintf(&b, " [ref=%d]", v.VariablesReference)
		}
		lines = append(lines, b.String())
	}
	return strings.Join(lines, "\n")
}

func formatSourceLabel(source *DapSource, line, column *int) string {
	if source == nil || (source.Path == "" && source.Name == "") {
		return ""
	}
	base := source.Path
	if base == "" {
		base = source.Name
	}
	if line == nil {
		return base
	}
	if column != nil {
		return fmt.Sprintf("%s:%d:%d", base, *line, *column)
	}
	return fmt.Sprintf("%s:%d", base, *line)
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

func formatDisassembly(instructions []DapDisassembledInstruction) string {
	lines := []string{"Disassembly:"}
	if len(instructions) == 0 {
		lines = append(lines, "(empty)")
		return strings.Join(lines, "\n")
	}
	addrWidth := 0
	bytesWidth := 2
	for _, ins := range instructions {
		if len(ins.Address) > addrWidth {
			addrWidth = len(ins.Address)
		}
		if len(ins.InstructionBytes) > bytesWidth {
			bytesWidth = len(ins.InstructionBytes)
		}
	}
	for _, ins := range instructions {
		loc := formatSourceLabel(ins.Location, ins.Line, ins.Column)
		parts := []string{
			padRight(ins.Address, addrWidth),
			padRight(ins.InstructionBytes, bytesWidth),
			ins.Instruction,
		}
		if ins.Symbol != "" {
			parts = append(parts, fmt.Sprintf("<%s>", ins.Symbol))
		}
		if loc != "" {
			parts = append(parts, fmt.Sprintf("[%s]", loc))
		}
		var nonEmpty []string
		for _, p := range parts {
			if len(p) > 0 {
				nonEmpty = append(nonEmpty, p)
			}
		}
		lines = append(lines, strings.TrimRight(strings.Join(nonEmpty, "  "), " "))
	}
	return strings.Join(lines, "\n")
}

func formatMemoryRead(address string, data *string, unreadableBytes *int) string {
	lines := []string{fmt.Sprintf("Memory at %s:", address)}
	var raw []byte
	if data != nil && *data != "" {
		if dec, err := base64.StdEncoding.DecodeString(*data); err == nil {
			raw = dec
		}
	}
	if len(raw) == 0 {
		lines = append(lines, "(no readable bytes)")
	} else {
		for offset := 0; offset < len(raw); offset += 16 {
			end := min(offset+16, len(raw))
			chunk := raw[offset:end]
			hexParts := make([]string, len(chunk))
			ascii := make([]byte, len(chunk))
			for i, b := range chunk {
				hexParts[i] = fmt.Sprintf("%02x", b)
				if b >= 32 && b < 127 {
					ascii[i] = b
				} else {
					ascii[i] = '.'
				}
			}
			label := address
			if offset != 0 {
				label = fmt.Sprintf("+0x%x", offset)
			}
			lines = append(lines, fmt.Sprintf("%s %s |%s|", padRight(label, 18), padRight(strings.Join(hexParts, " "), 47), string(ascii)))
		}
	}
	if unreadableBytes != nil && *unreadableBytes > 0 {
		lines = append(lines, fmt.Sprintf("Unreadable bytes: %d", *unreadableBytes))
	}
	return strings.Join(lines, "\n")
}

func formatTable(headers []string, rows [][]string) string {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i := range headers {
			if i < len(row) && len(row[i]) > widths[i] {
				widths[i] = len(row[i])
			}
		}
	}
	fmtRow := func(cells []string) string {
		parts := make([]string, len(headers))
		for i := range headers {
			c := ""
			if i < len(cells) {
				c = cells[i]
			}
			parts[i] = padRight(c, widths[i])
		}
		return strings.Join(parts, "  ")
	}
	sep := make([]string, len(headers))
	for i, w := range widths {
		sep[i] = strings.Repeat("-", w)
	}
	lines := make([]string, 0, len(rows)+2)
	lines = append(lines, fmtRow(headers))
	lines = append(lines, fmtRow(sep))
	for _, row := range rows {
		lines = append(lines, fmtRow(row))
	}
	return strings.Join(lines, "\n")
}

func formatModules(modules []DapModule) string {
	if len(modules) == 0 {
		return "Modules:\n(none)"
	}
	rows := make([][]string, len(modules))
	for i, m := range modules {
		rows[i] = []string{
			fmt.Sprint(m.ID),
			m.Name,
			m.Path,
			m.SymbolStatus,
			m.AddressRange,
		}
	}
	return "Modules:\n" + formatTable([]string{"ID", "Name", "Path", "Symbols", "Range"}, rows)
}

func formatLoadedSources(sources []DapSource) string {
	lines := []string{"Loaded sources:"}
	if len(sources) == 0 {
		lines = append(lines, "(none)")
		return strings.Join(lines, "\n")
	}
	for _, src := range sources {
		label := src.Path
		if label == "" {
			label = src.Name
		}
		if label == "" {
			label = "<unknown>"
		}
		if src.SourceReference != nil {
			lines = append(lines, fmt.Sprintf("- %s [ref=%d]", label, *src.SourceReference))
		} else {
			lines = append(lines, fmt.Sprintf("- %s", label))
		}
	}
	return strings.Join(lines, "\n")
}

func formatInstructionBreakpoints(breakpoints []DapInstructionBreakpointRecord) string {
	lines := []string{"Instruction breakpoints:"}
	if len(breakpoints) == 0 {
		lines = append(lines, "(none)")
		return strings.Join(lines, "\n")
	}
	for _, bp := range breakpoints {
		loc := bp.InstructionReference
		if bp.Offset != nil {
			loc = fmt.Sprintf("%s+%d", bp.InstructionReference, *bp.Offset)
		}
		status := "pending"
		if bp.Verified {
			status = "verified"
		}
		var b strings.Builder
		fmt.Fprintf(&b, "- %s: %s", loc, status)
		if bp.Condition != "" {
			fmt.Fprintf(&b, " if %s", bp.Condition)
		}
		if bp.HitCondition != "" {
			fmt.Fprintf(&b, " after %s", bp.HitCondition)
		}
		if bp.Message != "" {
			fmt.Fprintf(&b, " (%s)", bp.Message)
		}
		lines = append(lines, b.String())
	}
	return strings.Join(lines, "\n")
}

func formatDataBreakpointInfo(info DapDataBreakpointInfoResponse) string {
	lines := []string{fmt.Sprintf("Data breakpoint info: %s", info.Description)}
	if info.DataID != nil {
		lines = append(lines, fmt.Sprintf("Data ID: %s", *info.DataID))
	} else {
		lines = append(lines, "Data ID: (not available)")
	}
	if len(info.AccessTypes) > 0 {
		lines = append(lines, fmt.Sprintf("Access types: %s", strings.Join(info.AccessTypes, ", ")))
	}
	if info.CanPersist != nil {
		yn := "no"
		if *info.CanPersist {
			yn = "yes"
		}
		lines = append(lines, fmt.Sprintf("Persistent: %s", yn))
	}
	return strings.Join(lines, "\n")
}

func formatDataBreakpoints(breakpoints []DapDataBreakpointRecord) string {
	lines := []string{"Data breakpoints:"}
	if len(breakpoints) == 0 {
		lines = append(lines, "(none)")
		return strings.Join(lines, "\n")
	}
	for _, bp := range breakpoints {
		status := "pending"
		if bp.Verified {
			status = "verified"
		}
		var b strings.Builder
		fmt.Fprintf(&b, "- %s: %s", bp.DataID, status)
		if bp.AccessType != "" {
			fmt.Fprintf(&b, " (%s)", bp.AccessType)
		}
		if bp.Condition != "" {
			fmt.Fprintf(&b, " if %s", bp.Condition)
		}
		if bp.HitCondition != "" {
			fmt.Fprintf(&b, " after %s", bp.HitCondition)
		}
		if bp.Message != "" {
			fmt.Fprintf(&b, " (%s)", bp.Message)
		}
		lines = append(lines, b.String())
	}
	return strings.Join(lines, "\n")
}

func formatCustomResponse(command string, body any) string {
	serialized := "null"
	if b, err := json.MarshalIndent(body, "", "  "); err == nil {
		serialized = string(b)
	} else {
		serialized = fmt.Sprintf("%#v", body)
	}
	return fmt.Sprintf("%s response:\n%s", command, serialized)
}

func formatSessions(sessions []DapSessionSummary) string {
	if len(sessions) == 0 {
		return "No debug sessions."
	}
	blocks := make([]string, len(sessions))
	for i, s := range sessions {
		lines := []string{
			fmt.Sprintf("%s: %s", s.ID, s.Status),
			fmt.Sprintf("  adapter=%s", s.Adapter),
			fmt.Sprintf("  cwd=%s", s.Cwd),
		}
		if s.Program != "" {
			lines = append(lines, fmt.Sprintf("  program=%s", s.Program))
		}
		if loc := FormatLocation(&s); loc != "" {
			lines = append(lines, fmt.Sprintf("  location=%s", loc))
		}
		if s.StopReason != "" {
			lines = append(lines, fmt.Sprintf("  reason=%s", s.StopReason))
		}
		blocks[i] = strings.Join(lines, "\n")
	}
	return strings.Join(blocks, "\n\n")
}

func formatEvaluation(eval DapEvaluateResponse) string {
	lines := []string{fmt.Sprintf("Result: %s", eval.Result)}
	if eval.Type != "" {
		lines = append(lines, fmt.Sprintf("Type: %s", eval.Type))
	}
	if eval.VariablesReference > 0 {
		lines = append(lines, fmt.Sprintf("Variables ref: %d", eval.VariablesReference))
	}
	return strings.Join(lines, "\n")
}

func buildOutcomeText(outcome DapContinueOutcome, timeoutSec int, verb string) string {
	lines := FormatSessionSnapshot(outcome.Snapshot)
	if outcome.TimedOut {
		lines = append(lines, fmt.Sprintf("Program is still running after %ds. Use pause to interrupt and inspect state.", timeoutSec))
		return strings.Join(lines, "\n")
	}
	if outcome.State == "stopped" {
		loc := FormatLocation(&outcome.Snapshot)
		if loc == "" {
			loc = "unknown location"
		}
		lines = append(lines, fmt.Sprintf("%s stopped at %s.", verb, loc))
		return strings.Join(lines, "\n")
	}
	if outcome.State == "terminated" {
		if outcome.Snapshot.ExitCode != nil {
			lines = append(lines, fmt.Sprintf("Program terminated with exit code %d.", *outcome.Snapshot.ExitCode))
		} else {
			lines = append(lines, "Program terminated.")
		}
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "Program is running.")
	return strings.Join(lines, "\n")
}

var adapterUnavailableMessages = map[string]string{
	"debugpy":          "adapter 'debugpy' is not available: python not found in PATH",
	"dlv":              "adapter 'dlv' is not available: install with 'go install github.com/go-delve/delve/cmd/dlv@latest'",
	"rdbg":             "adapter 'rdbg' is not available: install with 'gem install debug'",
	"js-debug-adapter": "adapter 'js-debug-adapter' is not available: download it from https://github.com/microsoft/vscode-js-debug",
}

var adapterCanonicalCommands = map[string]string{
	"debugpy":          "python",
	"dlv":              "dlv",
	"rdbg":             "rdbg",
	"js-debug-adapter": "js-debug-adapter",
}

func replaceTabs(s string) string {
	return strings.ReplaceAll(s, "\t", "   ")
}

func truncateRunes(s string, maxWidth int) string {
	runes := []rune(s)
	if len(runes) <= maxWidth {
		return s
	}
	if maxWidth <= 1 {
		return "…"
	}
	return string(runes[:maxWidth-1]) + "…"
}

func shortenPath(p string) string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" && strings.HasPrefix(p, home) {
		rest := p[len(home):]
		if rest == "" || rest[0] == '/' || rest[0] == '\\' {
			return "~" + rest
		}
	}
	return p
}

// formatPathRelativeToCwd mirrors packages/coding-agent/src/tools/path-utils.ts:512-531.
func formatPathRelativeToCwd(target, cwd string, trailingSlash bool) string {
	display := filepath.ToSlash(target)
	if rel, err := filepath.Rel(cwd, target); err == nil {
		if rel == "" || rel == "." {
			display = "."
		} else if !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
			display = filepath.ToSlash(rel)
		}
	}
	if trailingSlash && display != "." && !strings.HasSuffix(display, "/") {
		display += "/"
	}
	return display
}

func getConfiguredAdapters(cwd string, includeProject bool) string {
	adapters := getAvailableAdaptersWithTrust(cwd, includeProject)
	names := make([]string, len(adapters))
	for i, a := range adapters {
		names[i] = a.Name
	}
	joined := "none"
	if len(names) > 0 {
		joined = strings.Join(names, ", ")
	}
	return truncateRunes(replaceTabs(joined), 100)
}

func formatAdapterUnavailable(adapterName, command, cwd string, includeProject bool) string {
	displayName := truncateRunes(replaceTabs(adapterName), 40)
	canonical, ok := adapterCanonicalCommands[adapterName]
	if !ok {
		canonical = adapterName
	}
	if command != canonical {
		displayCmd := truncateRunes(replaceTabs(shortenPath(command)), 80)
		return fmt.Sprintf("adapter '%s' is not available: configured command '%s' did not resolve. Check the DAP adapter config for this workspace.", displayName, displayCmd)
	}
	if msg, ok := adapterUnavailableMessages[adapterName]; ok {
		return msg
	}
	return fmt.Sprintf("adapter '%s' is not available. Installed adapters: %s", displayName, getConfiguredAdapters(cwd, includeProject))
}
