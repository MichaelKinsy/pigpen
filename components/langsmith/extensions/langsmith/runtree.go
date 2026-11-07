package langsmith

// Port of run_trees.js RunTree (langsmith 0.10.5): ids, dotted order, parent/child links, replicas, the create
// and update payloads (`_convertToCreate`, `_remapForProject`, postRun, patchRun, end, addEvent), the replica
// environment parser (`_getWriteReplicasFromEnv`, `_ensureWriteReplicas`) and the context variables of
// utils/context_vars.js the reroot logic uses. Payloads are *omap with the key order of the SDK's object literals.
//
// A runTree is not safe for concurrent use: the original is single-threaded and every mutation between two awaits
// is atomic there, so the caller (the extension's event handlers) serialises its calls.
//
// Not ported, because the extension and its zod config can never reach them: the LangChain callback glue of
// createChild (`lc:child_config`, a symbol-keyed extra entry), `_awaitInputsOnPost`, `fromRunnableConfig`,
// `fromHeaders`/`toHeaders`/Baggage (distributed tracing headers), and the constructor options serialized,
// reference_example_id, attachments, end_time, caller_options, api_url, api_key, tracingEnabled.

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

const (
	// timestampLength is run_trees.js TIMESTAMP_LENGTH: the length of the uuid at the end of a dotted-order segment.
	timestampLength = 36
	// errConflictingEndpointsCode is utils/error.js ERR_CONFLICTING_ENDPOINTS.
	errConflictingEndpointsCode = "ERR_CONFLICTING_ENDPOINTS"
)

// uuidNamespaceDNS is run_trees.js UUID_NAMESPACE_DNS in bytes (6ba7b810-9dad-11d1-80b4-00c04fd430c8).
var uuidNamespaceDNS = [16]byte{0x6b, 0xa7, 0xb8, 0x10, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}

// Seams. Production code never reassigns them.
var (
	// rtNow is Date.now().
	rtNow = nowMs
	// rtRuntimeEnv is utils/env.js getRuntimeEnvironment().
	rtRuntimeEnv = getRuntimeEnvironment
)

// runClient is the part of Client a RunTree calls. *client implements it; a test records the payloads with a fake.
type runClient interface {
	createRun(ctx context.Context, run *omap, opts *destOptions) error
	updateRun(ctx context.Context, runID string, run *omap, opts *destOptions) error
}

var _ runClient = (*client)(nil)

// replicaConfig is one replica (run_trees.js RunTree.replicas entry, extension: index.ts:372-377): nil pointers
// are `undefined`. The tuple form `[projectName, updates]` of `_ensureWriteReplicas` is
// replicaConfig{ProjectName: &name, Updates: updates}.
type replicaConfig struct {
	APIURL      *string // apiUrl
	APIKey      *string // apiKey
	ProjectName *string // projectName
	WorkspaceID *string // workspaceId
	Updates     *omap   // updates, spread over the PATCH payload
	Primary     *bool   // primary
	Reroot      *bool   // reroot (stripped from the replicas a child inherits)
	Client      runClient
}

// runTreeConfig is the RunTree constructor / createChild config.
type runTreeConfig struct {
	Name    string
	RunType string // "" means "chain"
	// ProjectName is used verbatim, "" included (index.ts passes `config.project`, which `??` may leave empty).
	// DefaultProject asks for getDefaultProjectName() instead (the SDK default when project_name is absent).
	// Children inherit their parent's project and ignore both.
	ProjectName    string
	DefaultProject bool
	// Client is the client to use; nil means the shared `new Client()` (children inherit their parent's).
	Client runClient
	// Inputs: nil means absent (the SDK default, an empty object), undef is an explicit `undefined`, inputsNull
	// is JSON null.
	Inputs   any
	Metadata *omap // may be nil
	Tags     []string
	// Extra is config.extra; its `metadata` is merged over Metadata (run_trees.js:332-336).
	Extra *omap
	// Replicas: nil means `undefined` (children inherit their parent's, a root reads LANGSMITH_RUNS_ENDPOINTS);
	// a non-nil slice, even empty, is the explicit list.
	Replicas []replicaConfig

	// ID is config.id ("" means generate it from the start time). TraceID, DottedOrder and ParentRunID are the
	// config fields of the same names (a dotted_order given in the config also skips the serialized start time).
	ID          string
	TraceID     string
	DottedOrder string
	ParentRunID string
	// StartTimeMs is config.start_time in epoch milliseconds; nil is Date.now().
	StartTimeMs *int64
}

// inputsNull is a JSON null `inputs` value (a Go nil Inputs means "absent").
var inputsNull any = (*omap)(nil)

// rtContextVars is the object stored under `_LC_CONTEXT_VARIABLES_KEY` (utils/context_vars.js). A child shares its
// parent's instance, so a write through either is seen by both, as in the original.
type rtContextVars struct {
	replicaTraceRoots map[string]string // `_REPLICA_TRACE_ROOTS_KEY`; nil when the key is unset
}

// runTree is a RunTree. ID is its run id.
type runTree struct {
	ID string

	name        string
	runType     string
	projectName string
	parentRun   *runTree
	parentRunID string
	childRuns   []*runTree
	startTime   int64
	endTime     *int64
	extra       *omap
	tags        []string
	err         any // undef or a string (`error`)
	inputs      any
	outputs     any
	client      runClient
	events      []*omap
	traceID     string
	dottedOrder string

	executionOrder      int
	childExecutionOrder int
	replicas            []replicaConfig
	distributedParentID string
	serializedStartTime string // `_serialized_start_time`; "" is undefined
	ctx                 *rtContextVars
}

// emptySerialized is `serialized: {}`; it is shared and never mutated.
var emptySerialized = newOmap()

// emptyArray is `[]` for `child_runs`; it is shared and never mutated.
var emptyArray = []any{}

var rtSharedClient struct {
	mu sync.Mutex
	c  runClient
}

// getSharedClient is RunTree.getSharedClient(): one `new Client()` for the process.
func getSharedClient() (runClient, error) {
	rtSharedClient.mu.Lock()
	defer rtSharedClient.mu.Unlock()
	if rtSharedClient.c == nil {
		c, err := newClient(clientOptions{})
		if err != nil {
			return nil, err
		}
		rtSharedClient.c = c
	}
	return rtSharedClient.c, nil
}

// getDefaultProjectName is utils/project.js getDefaultProjectName.
func getDefaultProjectName() string {
	if v, ok := getLangSmithEnvironmentVariable("PROJECT"); ok {
		return v
	}
	if v, ok := getEnvironmentVariable("LANGCHAIN_SESSION"); ok {
		return v
	}
	return "default"
}

// newRunTree is `new RunTree(config)`. It fails on a malformed LANGSMITH_RUNS_ENDPOINTS exactly where the SDK
// throws (conflicting endpoints, more than one primary).
func newRunTree(cfg runTreeConfig) (*runTree, error) {
	return buildRunTree(cfg, nil, 0)
}

// buildRunTree is the constructor (run_trees.js:324-370). A non-nil parent is createChild's overrides
// (`parent_run`, `project_name`, `client`, the execution orders; `replicas` stays config.replicas, which
// createChild already resolved).
func buildRunTree(cfg runTreeConfig, parent *runTree, executionOrder int) (*runTree, error) {
	r := &runTree{
		ID:          cfg.ID,
		name:        cfg.Name,
		runType:     cfg.RunType,
		projectName: cfg.ProjectName,
		parentRun:   parent,
		parentRunID: cfg.ParentRunID,
		startTime:   0,
		err:         undef,
		inputs:      cfg.Inputs,
		outputs:     undef,
		tags:        cfg.Tags,
		traceID:     cfg.TraceID,
		dottedOrder: cfg.DottedOrder,
		client:      cfg.Client,
	}
	// RunTree.getDefaultConfig (run_trees.js:383-397).
	if cfg.StartTimeMs != nil {
		r.startTime = *cfg.StartTimeMs
	} else {
		r.startTime = rtNow()
	}
	if r.runType == "" {
		r.runType = "chain"
	}
	if cfg.DefaultProject && parent == nil {
		r.projectName = getDefaultProjectName()
	}
	if cfg.Inputs == nil {
		r.inputs = newOmap()
	}
	// metadata is merged below config.extra.metadata (run_trees.js:330-336).
	deduped := newOmap().spread(cfg.Metadata).spread(recGet(cfg.Extra, "metadata"))
	r.extra = newOmap().spread(cfg.Extra).set("metadata", deduped)
	if parent != nil {
		r.projectName = parent.projectName
		r.client = parent.client
		r.executionOrder = executionOrder
		r.childExecutionOrder = executionOrder
	} else {
		r.executionOrder = 1
		r.childExecutionOrder = 1
	}
	if r.client == nil {
		c, err := getSharedClient()
		if err != nil {
			return nil, err
		}
		r.client = c
	}
	// Generate serialized start time for ID generation (run_trees.js:343-346).
	if r.dottedOrder == "" {
		r.serializedStartTime = getMicrosecondPrecisionDatestring(r.startTime, r.executionOrder)
	}
	// uuid7FromTime(serialized ?? start_time) parses the string back to the same millisecond.
	if r.ID == "" {
		r.ID = uuid7FromTime(r.startTime)
	}
	if r.traceID == "" {
		if parent != nil && parent.traceID != "" {
			r.traceID = parent.traceID
		} else {
			r.traceID = r.ID
		}
	}
	replicas, err := ensureWriteReplicas(cfg.Replicas)
	if err != nil {
		return nil, err
	}
	r.replicas = replicas
	// Now set the dotted order with the actual ID (run_trees.js:360-369).
	if r.dottedOrder == "" {
		dotted, _ := convertToDottedOrderFormat(r.startTime, r.ID, r.executionOrder)
		if parent != nil {
			r.dottedOrder = parent.dottedOrder + "." + dotted
		} else {
			r.dottedOrder = dotted
		}
	}
	return r, nil
}

// createChild is RunTree.createChild (run_trees.js:404-468) for the fields the extension passes. It fails only
// where the constructor throws (more than one primary in cfg.Replicas).
func (r *runTree) createChild(cfg runTreeConfig) (*runTree, error) {
	childExecutionOrder := r.childExecutionOrder + 1
	// Handle replicas: the child's own, else the parent's with reroot stripped (reroot applies only to the run
	// where it is configured).
	if cfg.Replicas == nil {
		cfg.Replicas = stripReroot(r.replicas)
	}
	child, err := buildRunTree(cfg, r, childExecutionOrder)
	if err != nil {
		return nil, err
	}
	// Propagate all parent metadata; child metadata takes precedence (run_trees.js:423-431). child.extra is not
	// shared yet, so it is edited in place.
	if parentMeta, _ := r.extra.val("metadata").(*omap); parentMeta.size() > 0 {
		childMeta, _ := child.extra.val("metadata").(*omap)
		child.extra.set("metadata", parentMeta.clone().spread(childMeta))
	}
	// Copy context vars over into the new run tree (run_trees.js:433-437).
	child.ctx = r.ctx
	// Propagate child_execution_order upwards (run_trees.js:458-465).
	var visitedBuf [8]string
	visited := visitedBuf[:0]
	for current := r; current != nil && !containsString(visited, current.ID); current = current.parentRun {
		visited = append(visited, current.ID)
		if childExecutionOrder > current.childExecutionOrder {
			current.childExecutionOrder = childExecutionOrder
		}
	}
	r.childRuns = append(r.childRuns, child)
	return child, nil
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// stripReroot is `replicas?.map(({reroot, ...rest}) => rest)`: replicas are never mutated after construction,
// so the slice is shared when no entry carries a reroot.
func stripReroot(replicas []replicaConfig) []replicaConfig {
	if replicas == nil {
		return nil
	}
	has := false
	for i := range replicas {
		if replicas[i].Reroot != nil {
			has = true
			break
		}
	}
	if !has {
		return replicas
	}
	out := make([]replicaConfig, len(replicas))
	copy(out, replicas)
	for i := range out {
		out[i].Reroot = nil
	}
	return out
}

// end is RunTree.end(outputs, error, undefined, metadata). outputs may be undef; errMsg nil is `undefined`;
// metadata may be nil. The first value set wins (`this.outputs ?? outputs`).
func (r *runTree) end(outputs any, errMsg *string, metadata *omap) {
	r.endAt(outputs, errMsg, nil, metadata)
}

// endAt is end with an explicit end time (epoch ms); nil is Date.now() (run_trees.js:469-478).
func (r *runTree) endAt(outputs any, errMsg *string, endTimeMs *int64, metadata *omap) {
	if jsNullish(r.outputs) {
		r.outputs = outputs
	}
	if jsNullish(r.err) {
		if errMsg != nil {
			r.err = *errMsg
		} else {
			r.err = undef
		}
	}
	if r.endTime == nil {
		var t int64
		if endTimeMs != nil {
			t = *endTimeMs
		} else {
			t = rtNow()
		}
		r.endTime = &t
	}
	if metadata != nil && metadata.size() > 0 {
		// A new extra and a new metadata: earlier payloads share the old ones.
		merged := newOmap().spread(r.extra.val("metadata")).spread(metadata)
		r.extra = r.extra.clone().set("metadata", merged)
	}
}

// addEvent is RunTree.addEvent({name, kwargs}) with the current time (run_trees.js:838-855).
func (r *runTree) addEvent(name string, kwargs *omap) {
	var kw any = undef
	if kwargs != nil {
		kw = kwargs
	}
	r.events = append(r.events, obj("name", name, "kwargs", kw, "time", jsISOString(rtNow())))
}

// addEventMessage is RunTree.addEvent("message") (the string form).
func (r *runTree) addEventMessage(message string) {
	r.events = append(r.events, obj("name", "event", "time", jsISOString(rtNow()), "message", message))
}

// convertToCreate is RunTree._convertToCreate (run_trees.js:479-523). It adds runtimeEnv to run.extra.runtime in
// place, as the original does.
func (r *runTree) convertToCreate(run *runTree, runtimeEnv *omap, excludeChildRuns bool) *omap {
	runExtra := run.extra
	// Avoid overwriting the runtime environment if it's already set.
	rv := runExtra.val("runtime")
	runtime, _ := rv.(*omap)
	// `runtime?.library === undefined`: true for a missing or null runtime and for any runtime without a library.
	if runtime == nil || isUndef(runtime.val("library")) {
		if !rtTruthy(rv) {
			runtime = newOmap()
			runExtra.set("runtime", runtime)
		}
		// A truthy non-object runtime (JSON cannot show the difference for an array; a string throws in the
		// original) is left alone.
		if runtime != nil && runtimeEnv != nil {
			for _, k := range runtimeEnv.ownKeys() {
				if !rtTruthy(runtime.val(k)) {
					runtime.set(k, runtimeEnv.vals[k])
				}
			}
		}
	}
	var parentRunID any = undef
	if run.parentRun != nil {
		parentRunID = run.parentRun.ID
	} else if run.parentRunID != "" {
		parentRunID = run.parentRunID
	}
	var childRuns any = emptyArray
	if !excludeChildRuns {
		converted := make([]*omap, len(run.childRuns))
		for i, c := range run.childRuns {
			converted[i] = r.convertToCreate(c, runtimeEnv, excludeChildRuns)
		}
		childRuns = converted
	}
	var startTime any = run.startTime
	if run.serializedStartTime != "" {
		startTime = run.serializedStartTime
	}
	var endTime any = undef
	if run.endTime != nil {
		endTime = *run.endTime
	}
	var tags any = undef
	if run.tags != nil {
		tags = run.tags
	}
	var events any = undef
	if run.events != nil {
		events = run.events
	}
	return obj(
		"id", run.ID,
		"name", run.name,
		"start_time", startTime,
		"end_time", endTime,
		"run_type", run.runType,
		"reference_example_id", undef,
		"extra", runExtra,
		"serialized", emptySerialized,
		"error", run.err,
		"inputs", run.inputs,
		"outputs", run.outputs,
		"session_name", run.projectName,
		"child_runs", childRuns,
		"parent_run_id", parentRunID,
		"trace_id", run.traceID,
		"dotted_order", run.dottedOrder,
		"tags", tags,
		"attachments", undef,
		"events", events,
	)
}

// tail36 is `seg.slice(-TIMESTAMP_LENGTH)`; head36 is `seg.slice(0, -TIMESTAMP_LENGTH)`. Segments are ASCII.
func tail36(seg string) string {
	if len(seg) <= timestampLength {
		return seg
	}
	return seg[len(seg)-timestampLength:]
}

func head36(seg string) string {
	if len(seg) <= timestampLength {
		return ""
	}
	return seg[:len(seg)-timestampLength]
}

// strVal reads a string value of a payload; "" when it is undefined or not a string (all falsy in the original's
// truthiness checks).
func strVal(m *omap, key string) string {
	s, _ := m.val(key).(string)
	return s
}

// sliceParentID is RunTree._sliceParentId (run_trees.js:524-559): it re-roots run below parentID.
func sliceParentID(parentID string, run *omap) {
	if dotted := strVal(run, "dotted_order"); dotted != "" {
		segs := strings.Split(dotted, ".")
		startIdx := -1
		for idx, seg := range segs {
			if tail36(seg) == parentID {
				startIdx = idx
				break
			}
		}
		if startIdx >= 0 {
			// Trim segments to start after parent_id (exclusive) and rebuild the dotted order.
			trimmed := segs[startIdx+1:]
			run.set("dotted_order", strings.Join(trimmed, "."))
			if len(trimmed) > 0 {
				run.set("trace_id", tail36(trimmed[0]))
			} else {
				run.set("trace_id", run.val("id"))
			}
		}
	}
	if strVal(run, "parent_run_id") == parentID {
		// We've found the new root node.
		run.set("parent_run_id", undef)
	}
}

// setReplicaTraceRoot is RunTree._setReplicaTraceRoot (run_trees.js:560-570).
func (r *runTree) setReplicaTraceRoot(replicaKey, traceRootID string) {
	// getContextVar(this, KEY) ?? {} then setContextVar.
	var roots map[string]string
	if r.ctx != nil {
		roots = r.ctx.replicaTraceRoots
	}
	if roots == nil {
		roots = map[string]string{}
	}
	roots[replicaKey] = traceRootID
	if r.ctx == nil {
		r.ctx = &rtContextVars{}
	}
	r.ctx.replicaTraceRoots = roots
	// Recursively update all descendants to avoid race conditions around run tree creation vs processing time.
	for _, child := range r.childRuns {
		child.setReplicaTraceRoot(replicaKey, traceRootID)
	}
}

// getReplicaKey is run_trees.js:15-24 for {projectName, apiUrl, apiKey, workspaceId}: the v5 UUID (DNS namespace)
// of the sorted `key:value` pairs joined with `|`.
func getReplicaKey(projectName string, apiURL, apiKey, workspaceID *string) string {
	deref := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	var b strings.Builder
	b.WriteString("apiKey:")
	b.WriteString(deref(apiKey))
	b.WriteString("|apiUrl:")
	b.WriteString(deref(apiURL))
	b.WriteString("|projectName:")
	b.WriteString(projectName)
	b.WriteString("|workspaceId:")
	b.WriteString(deref(workspaceID))
	return uuidV5(b.String(), &uuidNamespaceDNS)
}

// uuidV5 is utils/uuid/src/v5.js (v35.js): SHA-1 of the namespace and the UTF-8 name.
func uuidV5(name string, namespace *[16]byte) string {
	h := sha1.New()
	h.Write(namespace[:])
	h.Write([]byte(name))
	var sum [sha1.Size]byte
	h.Sum(sum[:0])
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	var out [36]byte
	hex.Encode(out[0:8], sum[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], sum[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], sum[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], sum[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], sum[10:16])
	return string(out[:])
}

// remapParams is the object RunTree._remapForProject takes (run_trees.js:572).
type remapParams struct {
	projectName         string
	primary             *bool
	runtimeEnv          *omap
	excludeChildRuns    bool
	reroot              bool
	distributedParentID string
	apiURL, apiKey      *string
	workspaceID         *string
}

// remapForProject is RunTree._remapForProject (run_trees.js:571-693).
func (r *runTree) remapForProject(p remapParams) *omap {
	baseRun := r.convertToCreate(r, p.runtimeEnv, p.excludeChildRuns)
	// Preserve legacy behavior when `primary` is omitted.
	if p.primary == nil && p.projectName == r.projectName {
		return baseRun.set("session_name", p.projectName)
	}
	// Apply reroot logic before ID remapping.
	if p.reroot {
		if p.distributedParentID != "" {
			// If we have a distributed parent ID, slice at that point.
			sliceParentID(p.distributedParentID, baseRun)
		} else {
			// No distributed parent ID: make this run a root run.
			baseRun.set("parent_run_id", undef)
			if dotted := strVal(baseRun, "dotted_order"); dotted != "" {
				// Reset dotted order to just this run.
				baseRun.set("dotted_order", dotted[strings.LastIndexByte(dotted, '.')+1:])
				baseRun.set("trace_id", baseRun.val("id"))
			}
		}
		// Store this run's original ID in context vars so descendants know the new trace root.
		r.setReplicaTraceRoot(getReplicaKey(p.projectName, p.apiURL, p.apiKey, p.workspaceID), strVal(baseRun, "id"))
	}
	// If an ancestor was rerooted for this replica, update trace_id and dotted_order to reflect the new trace
	// hierarchy (tracked via context variables).
	if !p.reroot {
		var roots map[string]string
		if r.ctx != nil {
			roots = r.ctx.replicaTraceRoots
		}
		if ancestor := roots[getReplicaKey(p.projectName, p.apiURL, p.apiKey, p.workspaceID)]; ancestor != "" {
			baseRun.set("trace_id", ancestor)
			if dotted := strVal(baseRun, "dotted_order"); dotted != "" {
				segs := strings.Split(dotted, ".")
				for idx, seg := range segs {
					if tail36(seg) == ancestor {
						// Keep segments from the new trace root onwards.
						baseRun.set("dotted_order", strings.Join(segs[idx:], "."))
						break
					}
				}
			}
		}
	}
	if p.primary != nil && *p.primary {
		return baseRun.set("session_name", p.projectName)
	}
	// Remap IDs for the replica using nonCryptographicUuid7Deterministic: consistent across the runs of one replica
	// and still time-ordered.
	newID := nonCryptographicUuid7Deterministic(strVal(baseRun, "id"), p.projectName)
	newTraceID := newID
	if t := strVal(baseRun, "trace_id"); t != "" {
		newTraceID = nonCryptographicUuid7Deterministic(t, p.projectName)
	}
	var newParentID any = undef
	if pid := strVal(baseRun, "parent_run_id"); pid != "" {
		newParentID = nonCryptographicUuid7Deterministic(pid, p.projectName)
	}
	var newDottedOrder any = undef
	if dotted := strVal(baseRun, "dotted_order"); dotted != "" {
		segs := strings.Split(dotted, ".")
		for i, seg := range segs {
			// Keep the timestamp prefix, replace the uuid (the last TIMESTAMP_LENGTH characters).
			segs[i] = head36(seg) + nonCryptographicUuid7Deterministic(tail36(seg), p.projectName)
		}
		newDottedOrder = strings.Join(segs, ".")
	}
	return baseRun.
		set("id", newID).
		set("trace_id", newTraceID).
		set("parent_run_id", newParentID).
		set("dotted_order", newDottedOrder).
		set("session_name", p.projectName)
}

// replicaProject is `projectName ?? this.project_name`.
func (r *runTree) replicaProject(rep *replicaConfig) string {
	if rep.ProjectName != nil {
		return *rep.ProjectName
	}
	return r.projectName
}

func replicaDest(rep *replicaConfig) *destOptions {
	return &destOptions{APIKey: rep.APIKey, APIURL: rep.APIURL, WorkspaceID: rep.WorkspaceID}
}

func (r *runTree) replicaClient(rep *replicaConfig) runClient {
	if rep.Client != nil {
		return rep.Client
	}
	return r.client
}

// postRun is RunTree.postRun(): logs `Error in postRun for run <id>:` and carries on when the client fails.
func (r *runTree) postRun(ctx context.Context) { r.postRunWith(ctx, true) }

// postRunWith is postRun(excludeChildRuns); false is the deprecated path that posts every child (run_trees.js:726-730).
func (r *runTree) postRunWith(ctx context.Context, excludeChildRuns bool) {
	if err := r.tryPostRun(ctx, excludeChildRuns); err != nil {
		errorf("%s", "Error in postRun for run "+r.ID+": "+errString(err))
	}
}

func (r *runTree) tryPostRun(ctx context.Context, excludeChildRuns bool) error {
	runtimeEnv := rtRuntimeEnv()
	if len(r.replicas) > 0 {
		for i := range r.replicas {
			rep := &r.replicas[i]
			runCreate := r.remapForProject(remapParams{
				projectName:         r.replicaProject(rep),
				primary:             rep.Primary,
				runtimeEnv:          runtimeEnv,
				excludeChildRuns:    true,
				reroot:              rep.Reroot != nil && *rep.Reroot,
				distributedParentID: r.distributedParentID,
				apiURL:              rep.APIURL,
				apiKey:              rep.APIKey,
				workspaceID:         rep.WorkspaceID,
			})
			if err := r.replicaClient(rep).createRun(ctx, runCreate, replicaDest(rep)); err != nil {
				return err
			}
		}
	} else {
		runCreate := r.convertToCreate(r, runtimeEnv, excludeChildRuns)
		if err := r.client.createRun(ctx, runCreate, nil); err != nil {
			return err
		}
	}
	if !excludeChildRuns {
		warnOnce("Posting with excludeChildRuns=false is deprecated and will be removed in a future version.")
		for _, child := range r.childRuns {
			child.postRunWith(ctx, false)
		}
	}
	r.childRuns = nil
	return nil
}

// getExcludeInputsOnPatch is run_trees.js:137-140.
func getExcludeInputsOnPatch() bool {
	v, ok := getLangSmithEnvironmentVariable("EXCLUDE_INPUTS_ON_PATCH")
	return !ok || strings.EqualFold(v, "true") || v == "1"
}

// patchRun is RunTree.patchRun() with the default excludeInputs.
func (r *runTree) patchRun(ctx context.Context) error { return r.patchRunWith(ctx, nil) }

// patchRunWith is RunTree.patchRun({excludeInputs}); nil reads LANGSMITH_EXCLUDE_INPUTS_ON_PATCH (default: exclude).
// Without replicas a client failure is logged as `Error in patchRun for run <id>`; with replicas it is returned
// (run_trees.js:752-797 has no try/catch there, so the original rejects).
func (r *runTree) patchRunWith(ctx context.Context, excludeInputs *bool) error {
	exclude := false
	if excludeInputs != nil {
		exclude = *excludeInputs
	} else {
		exclude = getExcludeInputsOnPatch()
	}
	if len(r.replicas) > 0 {
		for i := range r.replicas {
			rep := &r.replicas[i]
			runData := r.remapForProject(remapParams{
				projectName:         r.replicaProject(rep),
				primary:             rep.Primary,
				runtimeEnv:          nil,
				excludeChildRuns:    true,
				reroot:              rep.Reroot != nil && *rep.Reroot,
				distributedParentID: r.distributedParentID,
				apiURL:              rep.APIURL,
				apiKey:              rep.APIKey,
				workspaceID:         rep.WorkspaceID,
			})
			updatePayload := obj(
				"id", runData.val("id"),
				"name", runData.val("name"),
				"run_type", runData.val("run_type"),
				"start_time", runData.val("start_time"),
				"outputs", runData.val("outputs"),
				"error", runData.val("error"),
				"parent_run_id", runData.val("parent_run_id"),
				"session_name", runData.val("session_name"),
				"reference_example_id", runData.val("reference_example_id"),
				"end_time", runData.val("end_time"),
				"dotted_order", runData.val("dotted_order"),
				"trace_id", runData.val("trace_id"),
				"events", runData.val("events"),
				"tags", runData.val("tags"),
				"extra", runData.val("extra"),
				"attachments", undef,
			).spread(rep.Updates)
			// Important that inputs is not a key in the run update if excluded because it will overwrite the run
			// create if the two operations are merged during batching.
			if !exclude {
				updatePayload.set("inputs", runData.val("inputs"))
			}
			if err := r.replicaClient(rep).updateRun(ctx, strVal(runData, "id"), updatePayload, replicaDest(rep)); err != nil {
				return err
			}
		}
	} else if err := r.client.updateRun(ctx, r.ID, r.nonReplicaUpdate(exclude), nil); err != nil {
		errorf("%s", "Error in patchRun for run "+r.ID+" "+errString(err))
	}
	r.childRuns = nil
	return nil
}

// nonReplicaUpdate is the `runUpdate` literal of run_trees.js:800-822.
func (r *runTree) nonReplicaUpdate(excludeInputs bool) *omap {
	var startTime any = r.startTime
	if r.serializedStartTime != "" {
		startTime = r.serializedStartTime
	}
	var endTime any = undef
	if r.endTime != nil {
		endTime = *r.endTime
	}
	var parentRunID any = undef
	if r.parentRun != nil {
		parentRunID = r.parentRun.ID
	} else if r.parentRunID != "" {
		parentRunID = r.parentRunID
	}
	var tags any = undef
	if r.tags != nil {
		tags = r.tags
	}
	var events any = undef
	if r.events != nil {
		events = r.events
	}
	u := obj(
		"name", r.name,
		"run_type", r.runType,
		"start_time", startTime,
		"end_time", endTime,
		"error", r.err,
		"outputs", r.outputs,
		"parent_run_id", parentRunID,
		"reference_example_id", undef,
		"extra", r.extra,
		"events", events,
		"dotted_order", r.dottedOrder,
		"trace_id", r.traceID,
		"tags", tags,
		"attachments", undef,
		"session_name", r.projectName,
	)
	if !excludeInputs {
		u.set("inputs", r.inputs)
	}
	return u
}

// toJSON is RunTree.toJSON(): the create payload with the children included.
func (r *runTree) toJSON() *omap { return r.convertToCreate(r, nil, false) }

// ensureWriteReplicas is run_trees.js:1069-1085: an absent list reads the environment; at most one primary.
func ensureWriteReplicas(replicas []replicaConfig) ([]replicaConfig, error) {
	ensured := replicas
	if replicas == nil {
		var err error
		if ensured, err = getWriteReplicasFromEnv(); err != nil {
			return nil, err
		}
	}
	primaries := 0
	for i := range ensured {
		if ensured[i].Primary != nil && *ensured[i].Primary {
			primaries++
		}
	}
	if primaries > 1 {
		return nil, errors.New("Only one replica can be marked as primary.")
	}
	return ensured, nil
}

// conflictingEndpointsError is utils/error.js ConflictingEndpointsError.
type conflictingEndpointsError struct{}

func (conflictingEndpointsError) Error() string {
	return "You cannot provide both LANGSMITH_ENDPOINT / LANGCHAIN_ENDPOINT and LANGSMITH_RUNS_ENDPOINTS."
}

// ErrorName is the error's JavaScript `name`.
func (conflictingEndpointsError) ErrorName() string { return "ConflictingEndpointsError" }

// Code is the error's `code` property.
func (conflictingEndpointsError) Code() string { return errConflictingEndpointsCode }

const runsEndpointsHint = "Invalid LANGSMITH_RUNS_ENDPOINTS \u2013 must be valid JSON array of " +
	"objects with api_url and api_key properties, or object mapping url->apiKey"

// getWriteReplicasFromEnv is run_trees.js:990-1068.
func getWriteReplicasFromEnv() ([]replicaConfig, error) {
	envVar, _ := getEnvironmentVariable("LANGSMITH_RUNS_ENDPOINTS")
	if envVar == "" {
		return []replicaConfig{}, nil
	}
	parsed, err := rtParseJSON(envVar)
	if err != nil {
		rtWarn(runsEndpointsHint)
		return []replicaConfig{}, nil
	}
	switch p := parsed.(type) {
	case []any:
		replicas := []replicaConfig{}
		for _, item := range p {
			if t := jsTypeof(item); t != "object" || item == nil {
				rtWarn("Invalid item type in LANGSMITH_RUNS_ENDPOINTS: expected object, got " + t)
				continue
			}
			apiURL, ok := recGet(item, "api_url").(string)
			if !ok {
				rtWarn("Invalid api_url type in LANGSMITH_RUNS_ENDPOINTS: expected string, got " + jsTypeof(recGet(item, "api_url")))
				continue
			}
			apiKey, ok := recGet(item, "api_key").(string)
			if !ok {
				rtWarn("Invalid api_key type in LANGSMITH_RUNS_ENDPOINTS: expected string, got " + jsTypeof(recGet(item, "api_key")))
				continue
			}
			var projectName *string
			switch pn := recGet(item, "project_name").(type) {
			case undefinedT:
			case nil:
			case string:
				projectName = &pn
			default:
				rtWarn("Invalid project_name type in LANGSMITH_RUNS_ENDPOINTS: expected string, got " + jsTypeof(pn))
				continue
			}
			var primary *bool
			switch pr := recGet(item, "primary").(type) {
			case undefinedT:
			case bool:
				primary = &pr
			default:
				rtWarn("Invalid primary type in LANGSMITH_RUNS_ENDPOINTS: expected boolean, got " + jsTypeof(pr))
				continue
			}
			url := strings.TrimSuffix(apiURL, "/")
			replicas = append(replicas, replicaConfig{APIURL: &url, APIKey: &apiKey, ProjectName: projectName, Primary: primary})
		}
		return replicas, nil
	case *omap:
		if p != nil {
			// _checkEndpointEnvUnset (run_trees.js:1086-1091).
			if p.size() > 0 {
				if v, ok := getLangSmithEnvironmentVariable("ENDPOINT"); ok && v != "" {
					return nil, conflictingEndpointsError{}
				}
			}
			replicas := []replicaConfig{}
			for _, url := range p.ownKeys() {
				cleanURL := strings.TrimSuffix(url, "/")
				if key, ok := p.vals[url].(string); ok {
					apiKey := key
					replicas = append(replicas, replicaConfig{APIURL: &cleanURL, APIKey: &apiKey})
				} else {
					rtWarn("Invalid value type in LANGSMITH_RUNS_ENDPOINTS for URL " + url + ": expected string, got " + jsTypeof(p.vals[url]))
				}
			}
			return replicas, nil
		}
	}
	rtWarn("Invalid LANGSMITH_RUNS_ENDPOINTS \u2013 must be valid JSON array of objects with api_url and api_key " +
		"properties, or object mapping url->apiKey, got " + jsTypeof(parsed))
	return []replicaConfig{}, nil
}

// rtWarn is console.warn with a message that is not a format string.
func rtWarn(msg string) { warnf("%s", msg) }

// jsNullish is `v == null`.
func jsNullish(v any) bool { return v == nil || isUndef(v) }

// rtTruthy is JavaScript truthiness for the values a run payload holds.
func rtTruthy(v any) bool {
	switch x := v.(type) {
	case nil, undefinedT:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0 && !math.IsNaN(x)
	case int:
		return x != 0
	case int64:
		return x != 0
	case *omap:
		return x != nil
	}
	return true
}

// jsTypeof is the `typeof` of a parsed JSON value (null is "object").
func jsTypeof(v any) string {
	switch v.(type) {
	case undefinedT:
		return "undefined"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64, int, int64:
		return "number"
	}
	return "object"
}

// rtParseJSON is JSON.parse for text that must keep key order: objects become *omap (a duplicate key keeps
// its first position and its last value), arrays []any, numbers float64, null nil.
func rtParseJSON(s string) (any, error) {
	p := rtJSONParser{s: s}
	v, err := p.value(0)
	if err != nil {
		return nil, err
	}
	p.ws()
	if p.i != len(p.s) {
		return nil, rtErrBadJSON
	}
	return v, nil
}

var rtErrBadJSON = errors.New("invalid JSON")

type rtJSONParser struct {
	s string
	i int
}

func (p *rtJSONParser) ws() {
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *rtJSONParser) literal(lit string, v any) (any, error) {
	if !strings.HasPrefix(p.s[p.i:], lit) {
		return nil, rtErrBadJSON
	}
	p.i += len(lit)
	return v, nil
}

func (p *rtJSONParser) value(depth int) (any, error) {
	if depth > 512 {
		return nil, rtErrBadJSON
	}
	p.ws()
	if p.i >= len(p.s) {
		return nil, rtErrBadJSON
	}
	switch c := p.s[p.i]; {
	case c == '{':
		p.i++
		m := newOmap()
		p.ws()
		if p.i < len(p.s) && p.s[p.i] == '}' {
			p.i++
			return m, nil
		}
		for {
			p.ws()
			if p.i >= len(p.s) || p.s[p.i] != '"' {
				return nil, rtErrBadJSON
			}
			key, err := p.str()
			if err != nil {
				return nil, err
			}
			p.ws()
			if p.i >= len(p.s) || p.s[p.i] != ':' {
				return nil, rtErrBadJSON
			}
			p.i++
			v, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			m.set(key, v)
			p.ws()
			if p.i >= len(p.s) {
				return nil, rtErrBadJSON
			}
			switch p.s[p.i] {
			case ',':
				p.i++
			case '}':
				p.i++
				return m, nil
			default:
				return nil, rtErrBadJSON
			}
		}
	case c == '[':
		p.i++
		arr := []any{}
		p.ws()
		if p.i < len(p.s) && p.s[p.i] == ']' {
			p.i++
			return arr, nil
		}
		for {
			v, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
			p.ws()
			if p.i >= len(p.s) {
				return nil, rtErrBadJSON
			}
			switch p.s[p.i] {
			case ',':
				p.i++
			case ']':
				p.i++
				return arr, nil
			default:
				return nil, rtErrBadJSON
			}
		}
	case c == '"':
		return p.str()
	case c == 't':
		return p.literal("true", true)
	case c == 'f':
		return p.literal("false", false)
	case c == 'n':
		return p.literal("null", nil)
	case c == '-' || c >= '0' && c <= '9':
		return p.number()
	}
	return nil, rtErrBadJSON
}

// str scans a string literal (control characters and bad escapes are errors, as in JSON.parse) and decodes it
// with the SDK codec, which keeps lone surrogates.
func (p *rtJSONParser) str() (string, error) {
	start := p.i
	p.i++
	for p.i < len(p.s) {
		switch c := p.s[p.i]; {
		case c == '"':
			p.i++
			var out string
			if err := json.Unmarshal([]byte(p.s[start:p.i]), &out); err != nil {
				return "", rtErrBadJSON
			}
			return out, nil
		case c < 0x20:
			return "", rtErrBadJSON
		case c == '\\':
			p.i++
			if p.i >= len(p.s) {
				return "", rtErrBadJSON
			}
			switch p.s[p.i] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				p.i++
			case 'u':
				if p.i+4 >= len(p.s) {
					return "", rtErrBadJSON
				}
				for _, h := range []byte(p.s[p.i+1 : p.i+5]) {
					if !(h >= '0' && h <= '9' || h >= 'a' && h <= 'f' || h >= 'A' && h <= 'F') {
						return "", rtErrBadJSON
					}
				}
				p.i += 5
			default:
				return "", rtErrBadJSON
			}
		default:
			p.i++
		}
	}
	return "", rtErrBadJSON
}

func (p *rtJSONParser) number() (any, error) {
	start := p.i
	digits := func() int {
		n := 0
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
			n++
		}
		return n
	}
	if p.s[p.i] == '-' {
		p.i++
	}
	if p.i >= len(p.s) {
		return nil, rtErrBadJSON
	}
	if p.s[p.i] == '0' {
		p.i++
	} else if digits() == 0 {
		return nil, rtErrBadJSON
	}
	if p.i < len(p.s) && p.s[p.i] == '.' {
		p.i++
		if digits() == 0 {
			return nil, rtErrBadJSON
		}
	}
	if p.i < len(p.s) && (p.s[p.i] == 'e' || p.s[p.i] == 'E') {
		p.i++
		if p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
			p.i++
		}
		if digits() == 0 {
			return nil, rtErrBadJSON
		}
	}
	f, err := strconv.ParseFloat(p.s[start:p.i], 64) // out of range is +-Inf, as in JavaScript
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return nil, rtErrBadJSON
	}
	return f, nil
}
