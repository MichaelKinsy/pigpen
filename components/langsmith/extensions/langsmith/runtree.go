package langsmith

// Port of run_trees.js RunTree (langsmith 0.10.5): ids, dotted order, parent/child links, replicas, the create
// and update payloads (`_convertToCreate`, `_remapForProject`, postRun, patchRun, end, addEvent).
// OWNER: runtree lane. Payloads are *omap with the key order of the SDK's object literals.

import "context"

// replicaConfig is one replica as the extension builds it (index.ts:372-377): nil pointers are omitted.
type replicaConfig struct {
	APIURL      *string // apiUrl
	APIKey      *string // apiKey
	ProjectName *string // projectName
	Updates     *omap   // updates, spread over the PATCH payload
}

// runTreeConfig is the RunTree constructor / createChild config the extension uses.
type runTreeConfig struct {
	Name        string
	RunType     string // "" means "chain"
	ProjectName string // root only; children inherit
	Client      *client
	Inputs      any   // JSON value; nil means {} (the SDK default is an empty object)
	Metadata    *omap // may be nil
	Tags        []string
	// Replicas: nil means `undefined` (children inherit their parent's, a root reads LANGSMITH_RUNS_ENDPOINTS);
	// a non-nil slice, even empty, is the explicit list.
	Replicas []replicaConfig
}

// runTree is a RunTree. ID is its run id.
type runTree struct {
	ID string
}

// newRunTree is `new RunTree(config)`. It fails on a malformed LANGSMITH_RUNS_ENDPOINTS exactly where the SDK
// throws (conflicting endpoints, more than one primary).
func newRunTree(cfg runTreeConfig) (*runTree, error) { panic("unported: runtree.go newRunTree") }

// createChild is RunTree.createChild for the fields the extension passes.
func (r *runTree) createChild(cfg runTreeConfig) *runTree { panic("unported: runtree.go createChild") }

// end is RunTree.end(outputs, error, undefined, metadata). outputs may be undef; errMsg nil is `undefined`;
// metadata may be nil. The first value set wins (`this.outputs ?? outputs`).
func (r *runTree) end(outputs any, errMsg *string, metadata *omap) { panic("unported: runtree.go end") }

// addEvent is RunTree.addEvent({name, kwargs}) with the current time.
func (r *runTree) addEvent(name string, kwargs *omap) { panic("unported: runtree.go addEvent") }

// postRun is RunTree.postRun(): logs `Error in postRun for run <id>:` and carries on when the client fails.
func (r *runTree) postRun(ctx context.Context) { panic("unported: runtree.go postRun") }

// patchRun is RunTree.patchRun(): logs `Error in patchRun for run <id>` when the client fails.
func (r *runTree) patchRun(ctx context.Context) { panic("unported: runtree.go patchRun") }
