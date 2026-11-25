// Package gate is where a mode that restricts mutation (plan mode) meets the
// tools that mutate. The check runs in the tool, after it has resolved its
// real target, so the policy and the write cannot disagree about what is
// being written.
//
// This file is the contract between areas. Exported names and signatures are
// fixed; add, do not change. The area that owns a restricting mode installs
// the policy with SetPolicy; every tool that creates, changes, moves or
// deletes a file, or that starts work able to do so, calls Check first.
//
// upstream: crates/edit/src/path_policy.rs (enforce_write), tools/write.ts,
// edit/index.ts, task/structured-subagent.ts (plan-mode agent)
package gate

import (
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Op is what a tool is about to do.
type Op string

const (
	// OpWrite creates or replaces a file.
	OpWrite Op = "write"
	// OpEdit changes a file in place.
	OpEdit Op = "edit"
	// OpDelete removes a file.
	OpDelete Op = "delete"
	// OpMove renames a file; Request.Dest is the destination.
	OpMove Op = "move"
	// OpExec runs something that can mutate outside the tool's control: a
	// shell command, an eval cell, a subagent.
	OpExec Op = "exec"
)

// Request describes one mutation, after the tool has resolved its target.
type Request struct {
	// Tool is the tool's name.
	Tool string
	Op   Op
	// Path is the absolute path of the file acted on; empty for OpExec.
	Path string
	// Dest is the absolute destination of OpMove.
	Dest string
	// URL is the internal URL the model wrote, when the target came from one
	// (a `local://` sandbox file is still a file with a Path).
	URL string
	// Detail is the command, cell or agent name of an OpExec, for the message.
	Detail string
}

// Policy decides a request. It returns nil to allow it, or an error whose
// text the model reads.
type Policy func(ctx sdk.Context, r Request) error

var (
	mu       sync.RWMutex
	policy   Policy
	planning func(ctx sdk.Context) bool
)

// SetPolicy installs the policy; nil removes it.
func SetPolicy(p Policy) {
	mu.Lock()
	policy = p
	mu.Unlock()
}

// Check asks the installed policy. With none installed everything is allowed.
func Check(ctx sdk.Context, r Request) error {
	mu.RLock()
	p := policy
	mu.RUnlock()
	if p == nil {
		return nil
	}
	return p(ctx, r)
}

// SetPlanActive installs the predicate behind PlanActive; nil removes it.
func SetPlanActive(fn func(ctx sdk.Context) bool) {
	mu.Lock()
	planning = fn
	mu.Unlock()
}

// PlanActive reports whether the calling session, or the top-level session it
// descends from, is in plan mode. Areas that must behave differently while
// planning (subagents get omp's read-only plan-mode agent) ask here.
func PlanActive(ctx sdk.Context) bool {
	mu.RLock()
	fn := planning
	mu.RUnlock()
	return fn != nil && fn(ctx)
}

// ResetForTests removes the policy and the predicate.
func ResetForTests() {
	mu.Lock()
	policy, planning = nil, nil
	mu.Unlock()
}
