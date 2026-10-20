package pi_subagents

import (
	"fmt"
	"regexp"
	"runtime"
	"strings"
)

// Port of src/runs/shared/acceptance.ts: the gate shorthand, the Windows shell quoting of verify commands, the aggregate report.

// hostPlatform is process.platform.
func hostPlatform() string {
	if runtime.GOOS == "windows" {
		return "win32"
	}
	return runtime.GOOS
}

const gateInputError = `gate must be a non-empty command string or { command, output?: "json", schema?, timeoutMs? }.`

// gateObjectInput is the original's GateObjectInput.
type gateObjectInput struct {
	Command   string
	Output    string
	Schema    any
	TimeoutMs int
}

func (g gateObjectInput) object(id string) *jsObject {
	o := newObject()
	if id != "" {
		o.set("id", id)
	}
	o.set("command", g.Command)
	if g.Output != "" {
		o.set("output", g.Output)
	}
	if g.Schema != nil {
		o.set("schema", g.Schema)
	}
	if g.TimeoutMs != 0 {
		o.set("timeoutMs", float64(g.TimeoutMs))
	}
	return o
}

// parseGateInput validates the two accepted gate shapes (a command string or an object) without normalizing them.
func parseGateInput(gate any) (gateObjectInput, string) {
	if s, ok := gate.(string); ok {
		if jsTrim(s) != "" {
			return gateObjectInput{Command: jsTrim(s)}, ""
		}
		return gateObjectInput{}, gateInputError
	}
	value, ok := asObject(gate)
	if !ok {
		return gateObjectInput{}, gateInputError
	}
	for _, k := range value.order() {
		if !setOf("command", "output", "schema", "timeoutMs")[k] {
			return gateObjectInput{}, "gate." + k + " is not supported."
		}
	}
	command, isStr := value.vals["command"].(string)
	if !isStr || jsTrim(command) == "" {
		return gateObjectInput{}, gateInputError
	}
	output := value.vals["output"]
	if !isUndefined(output) && output != "json" {
		return gateObjectInput{}, `gate.output must be "json" when present.`
	}
	schema := value.vals["schema"]
	if !isUndefined(schema) {
		if output != "json" {
			return gateObjectInput{}, `gate.schema requires gate.output: "json".`
		}
		if _, isObj := asObject(schema); !isObj {
			return gateObjectInput{}, "gate.schema must be a JSON Schema object."
		}
	}
	timeout := value.vals["timeoutMs"]
	if !isUndefined(timeout) {
		if f, _ := asNumber(timeout); !isInteger(timeout) || f < 1 {
			return gateObjectInput{}, "gate.timeoutMs must be an integer >= 1."
		}
	}
	g := gateObjectInput{Command: jsTrim(command)}
	if output == "json" {
		g.Output = "json"
	}
	if !isUndefined(schema) {
		g.Schema = schema
	}
	if !isUndefined(timeout) {
		f, _ := asNumber(timeout)
		g.TimeoutMs = int(f)
	}
	return g, ""
}

// acceptanceHasTypedVerify is true when a policy declares an `output: "json"` verify command.
func acceptanceHasTypedVerify(acceptance any) bool {
	o, ok := asObject(acceptance)
	if !ok {
		return false
	}
	for _, c := range anyList(o.vals["verify"]) {
		if co, ok := asObject(c); ok && co.vals["output"] == "json" {
			return true
		}
	}
	return false
}

const typedVerifyOutputSchemaConflict = `a typed verify command (output: "json") cannot be combined with outputSchema; the child would have two structured-output sources.`

func describeGateAcceptanceConflict(gate, acceptance any) string {
	render := func(v any) string {
		enc := marshalJSON(v, "")
		if u := []rune(enc); len(u) > 120 {
			return string(u[:120]) + "..."
		}
		return enc
	}
	return " Both fields were present: gate=" + render(gate) + " acceptance=" + render(acceptance) + "."
}

// normalizeGateAcceptance is the acceptance a `gate` stands for: a verified acceptance with one verify command "gate". With no
// gate the acceptance is normalized (a JSON string decoded) as given. An error combines the gate with another acceptance.
func normalizeGateAcceptance(gate, acceptance any) (result any, errMsg string) {
	if isUndefined(gate) {
		if isUndefined(acceptance) {
			return nil, ""
		}
		v, msg := normalizeAcceptanceValue(acceptance, "acceptance")
		if msg != "" {
			return nil, msg
		}
		return v, ""
	}
	parsed, msg := parseGateInput(gate)
	if msg != "" {
		return nil, msg
	}
	if !isUndefined(acceptance) && acceptance != false {
		return nil, "gate cannot be combined with acceptance; use one gate command or acceptance.verify." + describeGateAcceptanceConflict(gate, acceptance)
	}
	out := newObject()
	out.set("level", "verified")
	out.set("verify", []any{parsed.object("gate")})
	return out, ""
}

// ---- quoteExecutableForShell ---------------------------------------------------------------------------------------------

const jsWS = `[\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`

var (
	quoteExeExt        = regexp.MustCompile(`(?i)\.(?:exe|bat|cmd|com|ps1)$`)
	quoteMatchExe      = regexp.MustCompile(`(?i)^([A-Za-z]:\\[^"<>|&*?\r\n]*?` + jsWS + `[^"<>|&*?\r\n]*?\\[^"<>|&*?\r\n]*?\.(?:exe|bat|cmd|com|ps1))(?:` + jsWS + `|$)`)
	quoteMatchSpaced   = regexp.MustCompile(`(?i)^([A-Za-z]:\\[^"<>|&*?\r\n]*?` + jsWS + `[^"<>|&*?\r\n]*?)(?:` + jsWS + `+--|$)`)
	quoteMatchBare     = regexp.MustCompile(`(?i)^([A-Za-z]:\\[^"<>|&*?\r\n]*?` + jsWS + `[^"<>|&*?\r\n]*?\\[^"<>|&*?\s\r\n]+)(?:` + jsWS + `|$)`)
	quoteDriveInside   = regexp.MustCompile(`[A-Za-z]:\\`)
	quoteFirstToken    = regexp.MustCompile(`^` + `[^\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+`)
	quoteHasSpace      = regexp.MustCompile(jsWS)
	quoteBackslashTail = regexp.MustCompile(`\\`)
)

// quoteExecutableForShell: on Windows, with a shell, cmd.exe splits an unquoted executable path with a space at the first space.
// Only an unquoted absolute drive path with a space in a directory component is safe to identify as an executable; it is quoted
// and what follows is kept as arguments. Anything else, and every other platform, is returned unchanged.
func quoteExecutableForShell(command, platform string) string {
	if platform != "win32" {
		return command
	}
	trimmed := strings.TrimLeftFunc(command, isJSSpace)
	if strings.HasPrefix(trimmed, `"`) {
		return command
	}
	firstToken := quoteFirstToken.FindString(trimmed)
	if quoteExeExt.MatchString(firstToken) {
		return command
	}
	leading := command[:len(command)-len(trimmed)]
	if m := quoteMatchExe.FindStringSubmatch(trimmed); m != nil {
		exe := m[1]
		if quoteHasSpace.MatchString(exe) && !quoteDriveInside.MatchString(exe[3:]) {
			return leading + `"` + exe + `"` + trimmed[len(exe):]
		}
	}
	if m := quoteMatchSpaced.FindStringSubmatch(trimmed); m != nil {
		name := m[1]
		if quoteHasSpace.MatchString(name[:strings.LastIndex(name, `\`)]) && !quoteDriveInside.MatchString(name[3:]) {
			return leading + `"` + name + `"` + trimmed[len(name):]
		}
	}
	if m := quoteMatchBare.FindStringSubmatch(trimmed); m != nil {
		exe := m[1]
		last := exe[strings.LastIndex(exe, `\`)+1:]
		if quoteHasSpace.MatchString(exe) && !quoteDriveInside.MatchString(exe[3:]) && !quoteHasSpace.MatchString(last) {
			return leading + `"` + exe + `"` + trimmed[len(exe):]
		}
	}
	return command
}

// ---- aggregate report --------------------------------------------------------------------------------------------------

// aggregateChild is the part of a dynamic fan-out child result the aggregate reads.
type aggregateChild struct {
	Agent    string
	ExitCode *int // nil: null
	Error    string
	Status   string // the child's acceptance status ("" unreported)
	Report   *jsObject
}

func uniqueTrimmedStrings(items []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, i := range items {
		t := jsTrim(i)
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// aggregateAcceptanceReport builds the report of a dynamic fan-out from its children's reports.
func aggregateAcceptanceReport(results []aggregateChild, notes string) *jsObject {
	var childReports []*jsObject
	var blockers []aggregateChild
	for _, r := range results {
		if r.Report != nil {
			childReports = append(childReports, r.Report)
		}
		if r.ExitCode == nil || *r.ExitCode != 0 || r.Status == "rejected" {
			blockers = append(blockers, r)
		}
	}
	success := len(results) > 0 && len(blockers) == 0
	crit := func(id, status, evidence string) any {
		o := newObject()
		o.set("id", id)
		o.set("status", status)
		o.set("evidence", evidence)
		return o
	}
	st := func(ok bool) string {
		if ok {
			return "satisfied"
		}
		return "not-satisfied"
	}
	c1, c2 := "Dynamic fanout produced no accepted child evidence.", "Dynamic fanout produced no aggregate review evidence."
	if success {
		c1 = fmt.Sprintf("All %d dynamic child run(s) completed without child or acceptance blockers.", len(results))
		c2 = "Collected child acceptance evidence for aggregate review."
	}
	criteria := []any{crit("criterion-1", st(success), c1), crit("criterion-2", st(success), c2)}
	for i, r := range results {
		reported := r.Status
		if reported == "" {
			reported = "unreported"
		}
		evidence := fmt.Sprintf("%s: acceptance %s", r.Agent, reported)
		if r.Error != "" {
			evidence += " (" + r.Error + ")"
		}
		criteria = append(criteria, crit(fmt.Sprintf("child-%d", i+1), st(r.ExitCode != nil && *r.ExitCode == 0 && r.Status != "rejected"), evidence))
	}
	collect := func(key string) []string {
		var all []string
		for _, r := range childReports {
			all = append(all, stringsOf(r.vals[key])...)
		}
		return uniqueTrimmedStrings(all)
	}
	var commands []any
	for _, r := range childReports {
		commands = append(commands, anyList(r.vals["commandsRun"])...)
	}
	risks := collect("residualRisks")
	var blockerRisks []string
	for _, b := range blockers {
		msg := b.Error
		if msg == "" {
			msg = "child or acceptance gate failed"
		}
		blockerRisks = append(blockerRisks, b.Agent+": "+msg)
	}
	allStaged := len(childReports) > 0
	for _, r := range childReports {
		allStaged = allStaged && r.vals["noStagedFiles"] == true
	}
	out := newObject()
	out.set("criteriaSatisfied", criteria)
	out.set("changedFiles", anyStrings(collect("changedFiles")))
	out.set("testsAddedOrUpdated", anyStrings(collect("testsAddedOrUpdated")))
	if commands == nil {
		commands = []any{}
	}
	out.set("commandsRun", commands)
	out.set("validationOutput", anyStrings(collect("validationOutput")))
	out.set("residualRisks", anyStrings(uniqueTrimmedStrings(append(append([]string{}, risks...), blockerRisks...))))
	out.set("noStagedFiles", allStaged)
	out.set("reviewFindings", anyStrings(collect("reviewFindings")))
	manual := notes
	if manual == "" {
		manual = fmt.Sprintf("Aggregated acceptance evidence from %d dynamic fanout child run(s).", len(results))
	}
	out.set("manualNotes", manual)
	if notes != "" {
		out.set("notes", notes)
	}
	return out
}
