package tintinweb_subagents

import (
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The manager registry: how another package of the same process (a fused Piglet Binary) reaches the running
// manager. The first activation claims the slot, later activations leave it alone, and only the owner's shutdown
// releases it. A spawn through it is detached and carries no internal capability: nesting, depth, transcript
// directory, config root, session file, reclaimed handle and blocking are what this extension issues itself, so a
// caller's values for them are dropped. upstream: index.ts (MANAGER_KEY, spawnTopLevel, registryEntry),
// docs/rpc.md "The manager registry".

// registryEntry is what a registry caller gets.
type registryEntry struct {
	app *app
}

var (
	registryMu  sync.Mutex
	activeEntry *registryEntry
)

// managerRegistry is the registered entry, or nil when no activation owns the slot.
func managerRegistry() *registryEntry {
	registryMu.Lock()
	defer registryMu.Unlock()
	return activeEntry
}

// claimManagerRegistry registers this activation if the slot is free. It returns the entry it made either way (the
// owner is the one that matches the registered one).
func claimManagerRegistry(a *app) *registryEntry {
	e := &registryEntry{app: a}
	registryMu.Lock()
	defer registryMu.Unlock()
	if activeEntry == nil {
		activeEntry = e
	}
	return e
}

// releaseManagerRegistry frees the slot if this activation owns it.
func releaseManagerRegistry(e *registryEntry) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if activeEntry == e {
		activeEntry = nil
	}
}

// WaitForAll waits for every running and queued agent.
func (e *registryEntry) WaitForAll() { e.app.mgr.waitForAll() }

// HasRunning: whether any agent is running or queued.
func (e *registryEntry) HasRunning() bool { return e.app.mgr.hasRunning() }

// GetRecord is a top-level agent's record, or nil: an owned child is not reachable from outside.
func (e *registryEntry) GetRecord(id string) *agentRecord {
	r := e.app.mgr.get(id)
	if r == nil || !r.topLevel() {
		return nil
	}
	return r
}

// Spawn starts a detached agent for another package, with the same dispatch contract as the model's: an unknown or
// disabled type is an error. upstream: index.ts spawnTopLevel, spawnResolved.
func (e *registryEntry) Spawn(ctx sdk.Context, typ, prompt string, options map[string]any) (string, error) {
	r, err := e.app.spawnTopLevel(ctx, typ, prompt, options)
	if err != nil {
		return "", err
	}
	return r.ID, nil
}

// internalSpawnOptions are the options only this extension issues.
var internalSpawnOptions = []string{"parentAgentId", "workflowId", "depth", "maxSubagentDepth", "configCwd", "rootSessionId", "resumeSessionFile", "reclaim", "blocking"}

// stripInternalSpawnOptions copies options without the internal capabilities.
func stripInternalSpawnOptions(options map[string]any) map[string]any {
	out := make(map[string]any, len(options))
	for k, v := range options {
		out[k] = v
	}
	for _, k := range internalSpawnOptions {
		delete(out, k)
	}
	return out
}
