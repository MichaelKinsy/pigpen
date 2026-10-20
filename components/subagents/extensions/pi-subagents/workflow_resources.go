package pi_subagents

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// Ports of src/workflows/workflow-resources.ts (named workflow resources, workflow args) and src/shared/workflow-child-permit.ts.

var workflowResourceNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

const (
	workflowMaxArgsBytes   = 16 * 1024
	workflowMaxStringBytes = 16 * 1024
)

// stableJSONDigest is stableJsonDigest (launch-contract.ts:24): the SHA-256 of the key-sorted JSON of a value.
func stableJSONDigest(v any) string {
	sum := sha256.Sum256([]byte(stableJSON(v)))
	return hex.EncodeToString(sum[:])
}

// WorkflowResourceHostAuthority is one host command a resolved resource may run.
type WorkflowResourceHostAuthority struct {
	Key     string
	Command string
}

// WorkflowResourceAuthority is what a consumed resource permit allows. Host is nil when the resource grants no host command.
type WorkflowResourceAuthority struct {
	Host []WorkflowResourceHostAuthority
}

// WorkflowResourceProvenance is the host-generated identity of a resolved resource (types.ts).
type WorkflowResourceProvenance struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Version    int    `json:"version"`
	Invocation string `json:"invocation"`
	Expansion  string `json:"expansion"`
	ID         string `json:"id"`
}

// WorkflowResourcePermit is an opaque, in-memory grant for one resolved resource script.
type WorkflowResourcePermit struct {
	mu           sync.Mutex
	resourceName string
	scriptDigest string
	authority    WorkflowResourceAuthority
	provenance   WorkflowResourceProvenance
	consumed     bool
}

// WorkflowResourceExpansion is what a resource's resolve returns: a script and its host grants, or Error.
type WorkflowResourceExpansion struct {
	Script       string
	HostCommands []WorkflowResourceHostAuthority
	Error        string
}

// WorkflowResourceDefinition is a named resource. Resolve is trusted and synchronous; it gets its own copy of the args.
type WorkflowResourceDefinition struct {
	Name    string
	Version int
	Resolve func(args *jsObject) WorkflowResourceExpansion
}

// ResolvedWorkflowResource is a resolved resource: the script, the permit that authorizes it, and its provenance.
type ResolvedWorkflowResource struct {
	Script     string
	Permit     *WorkflowResourcePermit
	Provenance WorkflowResourceProvenance
}

// WorkflowResourceRegistration unregisters a definition.
type WorkflowResourceRegistration struct{ dispose func() }

// Dispose removes the registration (once); permits already issued stay valid.
func (r WorkflowResourceRegistration) Dispose() { r.dispose() }

type workflowResourceRegistry struct {
	mu        sync.Mutex
	bySession map[string]map[string]*workflowResourceSnapshot
}

type workflowResourceSnapshot struct {
	name    string
	version int
	resolve func(args *jsObject) WorkflowResourceExpansion
}

var workflowResources = &workflowResourceRegistry{bySession: map[string]map[string]*workflowResourceSnapshot{}}

// RegisterWorkflowResource registers a definition for one session. The session ID scopes lookup; it does not authenticate.
func RegisterWorkflowResource(sessionID string, def WorkflowResourceDefinition) (WorkflowResourceRegistration, error) {
	if sessionID == "" || jsTrim(sessionID) != sessionID || jsLen(sessionID) > 256 || strings.Contains(sessionID, "\x00") {
		return WorkflowResourceRegistration{}, errors.New("Workflow registration requires a non-empty trimmed sessionId of at most 256 characters without NUL.")
	}
	if !workflowResourceNameRe.MatchString(def.Name) {
		return WorkflowResourceRegistration{}, errors.New("Workflow definition requires a safe resource name.")
	}
	if def.Version < 1 {
		return WorkflowResourceRegistration{}, errors.New("Workflow definition version must be a positive safe integer.")
	}
	if def.Resolve == nil {
		return WorkflowResourceRegistration{}, errors.New("Workflow definition requires a synchronous resolve function.")
	}
	if builtinWorkflowResource(def.Name) != nil {
		return WorkflowResourceRegistration{}, fmt.Errorf("Workflow resource '%s' is a protected builtin.", def.Name)
	}
	r := workflowResources
	r.mu.Lock()
	defer r.mu.Unlock()
	bucket := r.bySession[sessionID]
	if bucket == nil {
		bucket = map[string]*workflowResourceSnapshot{}
	}
	if _, has := bucket[def.Name]; has {
		return WorkflowResourceRegistration{}, fmt.Errorf("Workflow resource '%s' is already registered in this session; dispose it first.", def.Name)
	}
	snap := &workflowResourceSnapshot{name: def.Name, version: def.Version, resolve: def.Resolve}
	bucket[def.Name] = snap
	r.bySession[sessionID] = bucket
	var once sync.Once
	return WorkflowResourceRegistration{dispose: func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if b := r.bySession[sessionID]; b != nil {
				if b[def.Name] == snap {
					delete(b, def.Name)
				}
				if len(b) == 0 {
					delete(r.bySession, sessionID)
				}
			}
		})
	}}, nil
}

func validatePlainJSON(value any, path string, depth int) error {
	if depth > 8 {
		return fmt.Errorf("%s is too deeply nested.", path)
	}
	switch x := value.(type) {
	case nil, bool:
		return nil
	case string:
		if jsTrim(x) == "" {
			return fmt.Errorf("%s must not be empty.", path)
		}
		if len(x) > workflowMaxStringBytes {
			return fmt.Errorf("%s exceeds %d bytes.", path, workflowMaxStringBytes)
		}
		return nil
	case []any:
		if len(x) > 64 {
			return fmt.Errorf("%s contains too many items.", path)
		}
		for i, e := range x {
			if err := validatePlainJSON(e, fmt.Sprintf("%s[%d]", path, i), depth+1); err != nil {
				return err
			}
		}
		return nil
	case *jsObject:
		if len(x.keys) > 16 {
			return fmt.Errorf("%s contains too many fields.", path)
		}
		for _, k := range x.order() {
			if jsTrim(k) == "" {
				return fmt.Errorf("%s contains an empty field name.", path)
			}
			if err := validatePlainJSON(x.vals[k], path+"."+k, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if f, ok := goFloat(value); ok {
		if f != f || f > 1.7976931348623157e308 || f < -1.7976931348623157e308 {
			return fmt.Errorf("%s must be finite.", path)
		}
		return nil
	}
	return fmt.Errorf("%s must contain plain JSON data.", path)
}

// NormalizeWorkflowArgs is normalizeWorkflowArgs: the validated, JSON-normalized copy of a request's `args` (an empty object when
// absent). undef{} stands for undefined.
func NormalizeWorkflowArgs(value any) (*jsObject, error) {
	if _, u := value.(undef); u {
		return newObject(), nil
	}
	o, ok := value.(*jsObject)
	if !ok {
		return nil, errors.New("workflow args must be a plain JSON object.")
	}
	if err := validatePlainJSON(o, "workflow args", 0); err != nil {
		return nil, err
	}
	if len(marshalJSON(o, "")) > workflowMaxArgsBytes {
		return nil, fmt.Errorf("workflow args exceed %d bytes.", workflowMaxArgsBytes)
	}
	copied, err := parseJSON([]byte(marshalJSON(o, "")))
	if err != nil {
		return nil, err
	}
	return copied.(*jsObject), nil
}

func resolveRunCI(args *jsObject) WorkflowResourceExpansion {
	var unsupported []string
	for _, k := range args.order() {
		if k != "command" && k != "timeoutMs" {
			unsupported = append(unsupported, k)
		}
	}
	if len(unsupported) > 0 {
		return WorkflowResourceExpansion{Error: fmt.Sprintf("workflow 'run-ci' args contain unsupported fields: %s.", strings.Join(unsupported, ", "))}
	}
	command := any("npm test")
	if v, ok := args.vals["command"]; ok {
		command = v
	}
	if command != "npm test" && command != "npm run typecheck" {
		return WorkflowResourceExpansion{Error: "workflow 'run-ci' args.command must be 'npm test' or 'npm run typecheck'."}
	}
	timeout := any(float64(120000))
	if v, ok := args.vals["timeoutMs"]; ok {
		timeout = v
	}
	tf, isNum := timeout.(float64)
	if !isNum || tf != float64(int64(tf)) || tf < 1 || tf > 86400000 {
		return WorkflowResourceExpansion{Error: "workflow 'run-ci' args.timeoutMs must be an integer from 1 to 86400000."}
	}
	params := jo("kind", "command", "command", command, "timeoutMs", tf, "role", "ci")
	return WorkflowResourceExpansion{
		Script:       fmt.Sprintf(`return await runs.host("ci", %s);`, marshalJSON(params, "")),
		HostCommands: []WorkflowResourceHostAuthority{{Key: "ci", Command: command.(string)}},
	}
}

func resolveReview(args *jsObject) WorkflowResourceExpansion {
	var unsupported []string
	for _, k := range args.order() {
		if k != "task" {
			unsupported = append(unsupported, k)
		}
	}
	if len(unsupported) > 0 {
		return WorkflowResourceExpansion{Error: fmt.Sprintf("workflow 'review' args contain unsupported fields: %s.", strings.Join(unsupported, ", "))}
	}
	task, ok := args.vals["task"].(string)
	if !ok || jsTrim(task) == "" {
		return WorkflowResourceExpansion{Error: "workflow 'review' requires a non-empty string args.task."}
	}
	return WorkflowResourceExpansion{Script: fmt.Sprintf(`return (await runs.run("review", { agent: "reviewer", task: %s })).output;`, marshalJSON(jsTrim(task), ""))}
}

var workflowBuiltinResources = []*workflowResourceSnapshot{
	{name: "review", version: 1, resolve: resolveReview},
	{name: "run-ci", version: 1, resolve: resolveRunCI},
}

func builtinWorkflowResource(name string) *workflowResourceSnapshot {
	for _, r := range workflowBuiltinResources {
		if r.name == name {
			return r
		}
	}
	return nil
}

func workflowResourceNames() []string {
	names := make([]string, len(workflowBuiltinResources))
	for i, r := range workflowBuiltinResources {
		names[i] = r.name
	}
	return names
}

// ResolveWorkflowResource resolves only extension-owned or session-registered resources, so policy can tell them from raw
// scripts; caller-provided script text is never consulted. The error text is bounded to 4096 UTF-16 units. argsValue is undef{}
// when absent.
func ResolveWorkflowResource(nameValue any, argsValue any, sessionID string) (res *ResolvedWorkflowResource, errText string) {
	defer func() {
		if r := recover(); r != nil {
			res, errText = nil, boundErr(fmt.Sprint(r))
			if e, ok := r.(error); ok {
				errText = boundErr(e.Error())
			}
		}
	}()
	r, msg := resolveResource(nameValue, argsValue, sessionID)
	if r == nil {
		return nil, boundErr(msg)
	}
	return r, ""
}

func boundErr(s string) string {
	if jsLen(s) > 4096 {
		return string(utf16Slice(s, 4096))
	}
	return s
}

func resolveResource(nameValue, argsValue any, sessionID string) (*ResolvedWorkflowResource, string) {
	name, ok := nameValue.(string)
	if !ok || jsTrim(name) == "" {
		return nil, "workflow must be a non-empty resource name."
	}
	name = jsTrim(name)
	if !workflowResourceNameRe.MatchString(name) {
		return nil, "workflow must use a safe resource name."
	}
	res := builtinWorkflowResource(name)
	if res == nil && sessionID != "" {
		workflowResources.mu.Lock()
		if b := workflowResources.bySession[sessionID]; b != nil {
			res = b[name]
		}
		workflowResources.mu.Unlock()
	}
	if res == nil {
		return nil, fmt.Sprintf("Unknown workflow resource '%s'. Available resources: %s.", name, strings.Join(workflowResourceNames(), ", "))
	}
	args, err := NormalizeWorkflowArgs(argsValue)
	if err != nil {
		return nil, err.Error()
	}
	resolved := res.resolve(args)
	if resolved.Error != "" {
		return nil, resolved.Error
	}
	if jsTrim(resolved.Script) == "" {
		return nil, "Workflow resource returned an invalid expansion."
	}
	authority, err := cloneWorkflowResourceAuthority(resolved.HostCommands)
	if err != nil {
		return nil, err.Error()
	}
	var idb [16]byte
	if _, err := rand.Read(idb[:]); err != nil {
		return nil, err.Error()
	}
	idb[6] = idb[6]&0x0f | 0x40
	idb[8] = idb[8]&0x3f | 0x80
	id := fmt.Sprintf("%x-%x-%x-%x-%x", idb[0:4], idb[4:6], idb[6:8], idb[8:10], idb[10:])
	prov := WorkflowResourceProvenance{Kind: "workflow", Name: res.name, Version: res.version, Invocation: "named", Expansion: "resolved", ID: id}
	permit := &WorkflowResourcePermit{resourceName: res.name, scriptDigest: stableJSONDigest(resolved.Script), authority: authority, provenance: prov}
	return &ResolvedWorkflowResource{Script: resolved.Script, Permit: permit, Provenance: prov}, ""
}

var workflowHostKeyRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func cloneWorkflowResourceAuthority(hosts []WorkflowResourceHostAuthority) (WorkflowResourceAuthority, error) {
	if hosts == nil {
		return WorkflowResourceAuthority{}, nil
	}
	if len(hosts) > 32 {
		return WorkflowResourceAuthority{}, errors.New("Workflow resource host authority must be an array of at most 32 grants.")
	}
	seen := map[string]bool{}
	out := make([]WorkflowResourceHostAuthority, 0, len(hosts))
	for _, g := range hosts {
		if !workflowHostKeyRe.MatchString(g.Key) || seen[g.Key] {
			return WorkflowResourceAuthority{}, errors.New("Workflow resource host grant requires a unique safe key.")
		}
		c := jsTrim(g.Command)
		if c == "" || len(c) > 16*1024 || strings.Contains(g.Command, "\x00") {
			return WorkflowResourceAuthority{}, errors.New("Workflow resource host grant requires a non-empty command of at most 16384 bytes without NUL.")
		}
		seen[g.Key] = true
		out = append(out, WorkflowResourceHostAuthority{Key: g.Key, Command: c})
	}
	return WorkflowResourceAuthority{Host: out}, nil
}

// ConsumeWorkflowResourcePermit is consumeWorkflowResourcePermit: it verifies the script against the permit and consumes it once.
func ConsumeWorkflowResourcePermit(p *WorkflowResourcePermit, script string) (WorkflowResourceProvenance, WorkflowResourceAuthority, string) {
	var zero WorkflowResourceProvenance
	if p == nil {
		return zero, WorkflowResourceAuthority{}, "Workflow resource permit is invalid."
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.consumed {
		return zero, WorkflowResourceAuthority{}, "Workflow resource permit is already consumed."
	}
	if stableJSONDigest(script) != p.scriptDigest {
		return zero, WorkflowResourceAuthority{}, "Workflow resource permit does not match the resolved workflow script."
	}
	p.consumed = true
	return p.provenance, p.authority, ""
}

// AuthorizeWorkflowResourceHost validates a host call against the authority of a consumed resource; "" means allowed.
func AuthorizeWorkflowResourceHost(p *WorkflowResourcePermit, key, command string) string {
	if p == nil {
		return "Workflow resource authority is unavailable."
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.consumed {
		return "Workflow resource authority is unavailable."
	}
	if p.authority.Host == nil {
		return "runs.host is not allowed for this workflow resource."
	}
	for _, g := range p.authority.Host {
		if g.Key == key && g.Command == jsTrim(command) {
			return ""
		}
	}
	return fmt.Sprintf("The command for runs.host('%s') is not allowed for workflow resource '%s'.", key, p.resourceName)
}

// ---- workflow child permit (first-slice admission) ----

// WorkflowChildPermitInput describes the one child a permit admits.
type WorkflowChildPermitInput struct {
	IssuerPackage        string
	WorkflowRunID        string
	ChildKey             string
	Agent                string
	LaunchContractDigest string
	Context              string // fresh or fork
}

// WorkflowChildPermitLaunch is the final launch a permit is consumed against.
type WorkflowChildPermitLaunch struct {
	WorkflowRunID        string
	ChildKey             string
	Agent                string
	LaunchContractDigest string
	Context              string
	Runner               string
}

// WorkflowChildPermit is an opaque, in-memory, non-serializable first-slice permit.
type WorkflowChildPermit struct {
	mu                       sync.Mutex
	workflowRunID, childKey  string
	agent                    string
	expectedProjectionDigest string
	state                    string // available, claimed, consumed
}

func workflowPermitProjectionDigest(childKey, agent, digest, context, runner string) string {
	return stableJSONDigest(jo("version", float64(1), "childKey", childKey, "agent", agent, "launchContractDigest", digest, "context", context, "runner", runner))
}

func permitRequired(value, label string) (string, error) {
	if jsTrim(value) == "" || value != jsTrim(value) {
		return "", fmt.Errorf("%s must be a non-empty trimmed string.", label)
	}
	return value, nil
}

// CreateWorkflowChildPermit is createWorkflowChildPermit.
func CreateWorkflowChildPermit(in WorkflowChildPermitInput) (*WorkflowChildPermit, error) {
	for _, f := range [][2]string{{in.IssuerPackage, "issuerPackage"}, {in.WorkflowRunID, "workflowRunId"}, {in.ChildKey, "childKey"}, {in.Agent, "agent"}, {in.LaunchContractDigest, "launchContractDigest"}} {
		if _, err := permitRequired(f[0], f[1]); err != nil {
			return nil, err
		}
	}
	return &WorkflowChildPermit{workflowRunID: in.WorkflowRunID, childKey: in.ChildKey, agent: in.Agent, state: "available",
		expectedProjectionDigest: workflowPermitProjectionDigest(in.ChildKey, in.Agent, in.LaunchContractDigest, in.Context, "pi")}, nil
}

// ValidateWorkflowChildPermitRoot checks that a permit is unused and belongs to this workflow root.
func ValidateWorkflowChildPermitRoot(p *WorkflowChildPermit, workflowRunID string) string {
	if p == nil {
		return "Workflow child permit is invalid."
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != "available" {
		return "Workflow child permit is already consumed."
	}
	if p.workflowRunID != workflowRunID {
		return "Workflow child permit does not match this workflow root."
	}
	return ""
}

// ClaimWorkflowChildPermit claims the first distinct launch attempt before its model-authored shape is validated.
func ClaimWorkflowChildPermit(p *WorkflowChildPermit, workflowRunID, childKey string) string {
	if p == nil {
		return "Workflow child permit is invalid."
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != "available" {
		return "Workflow child permit is already consumed."
	}
	if p.workflowRunID != workflowRunID {
		return "Workflow child permit does not match this workflow root."
	}
	if p.childKey == childKey {
		p.state = "claimed"
		return ""
	}
	p.state = "consumed"
	return "Workflow child permit child key mismatch."
}

// ConsumeWorkflowChildPermit verifies and permanently consumes a permit before the one native process spawn.
func ConsumeWorkflowChildPermit(p *WorkflowChildPermit, l WorkflowChildPermitLaunch) string {
	if p == nil {
		return "Workflow child permit is invalid."
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch p.state {
	case "available":
		return "Workflow child permit launch was not claimed."
	case "consumed":
		return "Workflow child permit is already consumed."
	}
	if p.workflowRunID != l.WorkflowRunID {
		return "Workflow child permit does not match this workflow root."
	}
	p.state = "consumed"
	switch {
	case p.childKey != l.ChildKey:
		return "Workflow child permit child key mismatch."
	case p.agent != l.Agent:
		return "Workflow child permit agent mismatch."
	case l.Runner != "pi":
		return "Workflow child permit supports native Pi children only."
	case p.expectedProjectionDigest != workflowPermitProjectionDigest(l.ChildKey, l.Agent, l.LaunchContractDigest, l.Context, l.Runner):
		return "Workflow child permit does not match the final launch projection."
	}
	return ""
}

// WorkflowChildPermitConsumed reports a permit that was claimed or consumed.
func WorkflowChildPermitConsumed(p *WorkflowChildPermit) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state == "claimed" || p.state == "consumed"
}

// ChildPermitClaimer adapts a permit to the sandbox's OneUsePermit: the run's workflow root is the one it claims for.
type ChildPermitClaimer struct {
	Permit        *WorkflowChildPermit
	WorkflowRunID string
}

// Claim claims the permit for the first launch (scripted-workflow.ts, runs.run admission); "" means admitted.
func (c ChildPermitClaimer) Claim(key string) string {
	return ClaimWorkflowChildPermit(c.Permit, c.WorkflowRunID, key)
}
