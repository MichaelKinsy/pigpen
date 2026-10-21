package pi_background_tasks

import "encoding/json"

// Bounded task lists in tool results. `bg_task list` and `bg_status list` details land in the append-only session JSONL
// independently of the custom persistence entries, so a large fleet uses a tiny manifest while the sidecar stays canonical for
// restore. upstream: extensions/tool-result-details.ts.

// bgToolResultBounded replaces a task list that is too long or too large.
type bgToolResultBounded struct {
	Version      int              `json:"version"`
	FullSnapshot bool             `json:"fullSnapshot"`
	Reason       string           `json:"reason"`
	ByteSize     int              `json:"byteSize"`
	Fingerprint  string           `json:"fingerprint"`
	Counts       manifestCounts   `json:"counts"`
	TaskIDs      []string         `json:"taskIds"`
	Omitted      manifestCounts   `json:"omitted"`
	Thresholds   toolResultLimits `json:"thresholds"`
	UpdatedAt    int64            `json:"updatedAt"`
}

type toolResultLimits struct {
	MaxBytes int `json:"maxBytes"`
	MaxTasks int `json:"maxTasks"`
}

// toolResultOptions override the thresholds (tests).
type toolResultOptions struct{ maxBytes, maxTasks, sampleLimit int }

func isBgToolResultBoundedTasks(v any) bool {
	switch b := v.(type) {
	case *bgToolResultBounded:
		return b != nil && b.Version == 2 && !b.FullSnapshot
	case bgToolResultBounded:
		return b.Version == 2 && !b.FullSnapshot
	case map[string]any:
		version, _ := b["version"].(float64)
		full, has := b["fullSnapshot"].(bool)
		return version == 2 && has && !full
	}
	return false
}

// bgToolResultTasks is the list as it goes into tool-result details: the snapshots when they are few and small, else the
// manifest. The result is []snapshot or *bgToolResultBounded.
func bgToolResultTasks(tasks []snapshot, opts toolResultOptions) any {
	maxBytes, maxTasks, sampleLimit := opts.maxBytes, opts.maxTasks, opts.sampleLimit
	if maxBytes == 0 {
		maxBytes = bgToolResultMaxDetailsBytes
	}
	if maxTasks == 0 {
		maxTasks = bgToolResultMaxTasks
	}
	if sampleLimit == 0 {
		sampleLimit = bgToolResultSampleLimit
	}
	if tasks == nil {
		tasks = []snapshot{}
	}
	serialized, _ := json.Marshal(tasks)
	byteSize := len(serialized)
	if len(tasks) <= maxTasks && byteSize <= maxBytes {
		return tasks
	}
	ids := []string{}
	for _, t := range tasks[:min(sampleLimit, len(tasks))] {
		ids = append(ids, t.ID)
	}
	reason := "task-count-threshold"
	if byteSize > maxBytes {
		reason = "payload-too-large"
	}
	var updated int64
	for _, t := range tasks {
		updated = max(updated, t.UpdatedAt)
	}
	return &bgToolResultBounded{
		Version: 2, FullSnapshot: false, Reason: reason, ByteSize: byteSize, Fingerprint: stableSnapshotFingerprint(map[string]any{"tasks": tasks}),
		Counts: manifestCounts{Tasks: len(tasks)}, TaskIDs: ids, Omitted: manifestCounts{Tasks: max(0, len(tasks)-len(ids))},
		Thresholds: toolResultLimits{MaxBytes: maxBytes, MaxTasks: maxTasks}, UpdatedAt: updated,
	}
}

// applyBgToolResultTasksWithBarrier restores the tasks of one tool result: a manifest re-applies the loaded sidecar (and
// does nothing without one), a list applies its snapshots.
func applyBgToolResultTasksWithBarrier(detailsTasks any, sidecarLoaded bool, sidecarTasks []snapshot, clear func(), apply func(snapshot)) {
	if isBgToolResultBoundedTasks(detailsTasks) {
		if sidecarLoaded && sidecarTasks != nil {
			clear()
			for _, s := range sidecarTasks {
				apply(s)
			}
		}
		return
	}
	for _, s := range decodeSnapshots(detailsTasks) {
		apply(s)
	}
}
