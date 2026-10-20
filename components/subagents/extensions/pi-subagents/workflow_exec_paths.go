package pi_subagents

import (
	"fmt"
	"path/filepath"
)

// The output paths of a workflow's children (subagent-executor.ts:3646-3895): where each child saves its report, the claim that
// keeps two children from writing one file, and the launch request a child gets.

// wfOutputEnv is what the output path resolution of one workflow reads.
type wfOutputEnv struct {
	CtxCwd, WorkflowCwd, ArtifactsDir, WorkflowRunID string
	AggregateOutputPath                              string
	ConfiguredOutputBaseDir                          string
	Discover                                         func(cwd string, scope AgentScope) []AgentConfig
	Agents                                           []AgentConfig
	WorkflowAgentScope                               any
	SessionID                                        string
}

// workflowChildDefaultOutput is workflowChildDefaultOutput.
func workflowChildDefaultOutput(aggregate, artifactsDir, workflowRunID, key string) string {
	if aggregate != "" {
		ext := filepath.Ext(aggregate)
		base := aggregate[:len(aggregate)-len(ext)]
		if ext == "" {
			ext = ".md"
		}
		return base + "." + key + ext
	}
	return filepath.Join(artifactsDir, "outputs", sanitizeRunPathSegment(workflowRunID, 120), key+".md")
}

// workflowHostOutputClaimPath is resolveWorkflowHostOutputClaimPath: the claim of an output path is its resolved real path.
func workflowHostOutputClaimPath(outputPath string) string {
	if c, err := resolveSingleOutputClaimPath(outputPath); err == nil {
		return c
	}
	return outputPath
}

func (e *wfOutputEnv) outputBase() string {
	if e.ConfiguredOutputBaseDir != "" {
		return e.ConfiguredOutputBaseDir
	}
	return filepath.Join(e.ArtifactsDir, "outputs", sanitizeRunPathSegment(e.WorkflowRunID, 120))
}

// agentOf resolves the agent a child names, the way the output default does: from the child's own directory first, then from the
// workflow's agents.
func (e *wfOutputEnv) agentOf(params *jsObject, childCwd string) *AgentConfig {
	name, ok := params.vals["agent"].(string)
	if !ok {
		return nil
	}
	scopeValue := params.vals["agentScope"]
	if !params.has("agentScope") || scopeValue == nil {
		scopeValue = e.WorkflowAgentScope
	}
	discovered := e.Discover(childCwd, ResolveExecutionAgentScope(scopeValue))
	if a, _ := ResolveAgentName(name, discovered); a != nil {
		return a
	}
	a, _ := ResolveAgentName(name, e.Agents)
	return a
}

// resolveWorkflowChildOutputPath is resolveWorkflowChildOutputPath: the output file of one child and whether it is the agent's
// own default (inherited), which two children may share and so must be told apart.
func (e *wfOutputEnv) resolveWorkflowChildOutputPath(key string, params *jsObject) (path string, inherited bool, err error) {
	raw, hasRaw := params.vals["output"]
	if _, u := raw.(undef); u {
		hasRaw = false
	}
	_, isString := raw.(string)
	_, isBool := raw.(bool)
	hasExplicit := hasRaw && (isString || isBool)
	isTrue := raw == true || raw == "true"
	if resume, ok := params.vals["resume"].(string); ok && (!hasExplicit || isTrue) {
		rp := resumeParams{ID: jsTrim(resume)}
		if idx, ok := params.vals["index"].(float64); ok && idx == float64(int(idx)) {
			rp.Index = &idx
		}
		target, rerr := resolveAsyncResumeTarget(rp, defaultAsyncDirs(), resumeOptions{SessionID: e.SessionID})
		if rerr != nil {
			return "", false, rerr
		}
		if target.descriptor != nil {
			return target.descriptor.OutputPath, false, nil
		}
		return "", false, nil
	}
	childCwd := resolveWorkflowChildLocalCwd(e.WorkflowCwd, e.Discover, e.Agents, e.WorkflowAgentScope, params)
	agentOutput := ""
	hasAgentOutput := false
	if isTrue || (!hasExplicit && e.AggregateOutputPath == "") {
		if a := e.agentOf(params, childCwd); a != nil && a.Output != "" {
			agentOutput, hasAgentOutput = a.Output, true
		}
	}
	var output any
	switch {
	case isTrue:
		if hasAgentOutput {
			output = agentOutput
		}
	case hasExplicit:
		output = raw
	case e.AggregateOutputPath != "":
		output = workflowChildDefaultOutput(e.AggregateOutputPath, e.ArtifactsDir, e.WorkflowRunID, key)
	case hasAgentOutput:
		output = agentOutput
	}
	path = resolveSingleOutputPath(output, e.CtxCwd, childCwd, e.outputBase())
	inherited = !hasExplicit && e.AggregateOutputPath == "" && hasAgentOutput
	return path, inherited, nil
}

// wfOutputClaims is the result of workflowChildOutputClaims.
type wfOutputClaims struct {
	Claims      map[string]string // claim path -> key
	ChildClaims map[string]string // key -> claim path
	Overrides   map[string]string // key -> output
}

// workflowChildOutputClaims is workflowChildOutputClaims: two children that resolve to one output file are an error, except that
// two children inheriting their agent's one default are given distinct files.
func (e *wfOutputEnv) workflowChildOutputClaims(claimed map[string]string, entries []WorkflowCallSpec) (*wfOutputClaims, error) {
	type resolved struct {
		key       string
		path      string
		inherited bool
	}
	rs := make([]resolved, len(entries))
	for i, en := range entries {
		p, inh, err := e.resolveWorkflowChildOutputPath(en.Key, en.Params)
		if err != nil {
			return nil, err
		}
		rs[i] = resolved{en.Key, p, inh}
	}
	paths := map[string]int{}
	for c := range claimed {
		paths[c] = 1
	}
	for _, r := range rs {
		if r.path != "" {
			paths[workflowHostOutputClaimPath(r.path)]++
		}
	}
	out := &wfOutputClaims{ChildClaims: map[string]string{}, Overrides: map[string]string{}}
	for i := range rs {
		r := &rs[i]
		if r.inherited && r.path != "" && paths[workflowHostOutputClaimPath(r.path)] > 1 {
			o := workflowChildDefaultOutput(e.AggregateOutputPath, e.ArtifactsDir, e.WorkflowRunID, r.key)
			out.Overrides[r.key] = o
			r.path = o
		}
	}
	claims := map[string]string{}
	for k, v := range claimed {
		claims[k] = v
	}
	for _, r := range rs {
		if r.path == "" {
			continue
		}
		c := workflowHostOutputClaimPath(r.path)
		if prev, ok := claims[c]; ok {
			return nil, fmt.Errorf("Workflow children '%s' and '%s' resolve output to the same path: %s. Use distinct child output paths.", prev, r.key, r.path)
		}
		claims[c] = r.key
		out.ChildClaims[r.key] = c
	}
	out.Claims = claims
	return out, nil
}

// applyWorkflowChildOutputClaims replaces the claimed paths with the new claims.
func applyWorkflowChildOutputClaims(target, claims map[string]string) {
	for k := range target {
		delete(target, k)
	}
	for k, v := range claims {
		target[k] = v
	}
}

// wfChildLaunchInput is the input of prepareWorkflowChildLaunchParams.
type wfChildLaunchInput struct {
	Env               *wfOutputEnv
	WorkflowDefaults  *jsObject
	ChildParams       *jsObject
	ParentWorkflowRun string
	WorkflowKey       string
	OutputOverride    string
	HasOutputOverride bool
	OutputClaimPath   string
	Options           WorkflowLaunchOptions
}

// prepareWorkflowChildLaunchParams is prepareWorkflowChildLaunchParams: the child's output path decided, then the launch request.
func prepareWorkflowChildLaunchParams(in wfChildLaunchInput) (*jsObject, error) {
	env := in.Env
	childParams := in.ChildParams
	usesDefaultOutput := !childParams.has("output") && !childParams.has("resume")
	switch {
	case usesDefaultOutput && in.HasOutputOverride:
		childParams = lpSpread(childParams, jo("output", in.OutputOverride))
	case usesDefaultOutput && env.AggregateOutputPath != "":
		childParams = lpSpread(childParams, jo("output", workflowChildDefaultOutput(env.AggregateOutputPath, env.ArtifactsDir, in.ParentWorkflowRun, in.WorkflowKey)))
	case !childParams.has("resume") || childParams.has("output"):
		p, _, err := env.resolveWorkflowChildOutputPath(in.WorkflowKey, in.ChildParams)
		if err != nil {
			return nil, err
		}
		if p != "" {
			childParams = lpSpread(in.ChildParams, jo("output", p))
		}
	}
	childCwd := resolveWorkflowChildLocalCwd(env.WorkflowCwd, env.Discover, env.Agents, env.WorkflowAgentScope, childParams)
	external := false
	if a := env.agentOf(childParams, childCwd); a != nil {
		external = a.Runner != nil && (a.Runner.Type == "external-cli" || a.Runner.Type == "external-job")
	}
	opt := in.Options
	opt.ExternalAsyncRequired = external
	opt.OutputClaimPath = in.OutputClaimPath
	return prepareWorkflowLaunchParams(in.WorkflowDefaults, childParams, in.ParentWorkflowRun, in.WorkflowKey, opt)
}
