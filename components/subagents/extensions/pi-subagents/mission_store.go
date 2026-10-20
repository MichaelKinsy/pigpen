package pi_subagents

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Port of src/missions/store.ts: the mission files of a project, the global index that points to them, and retention.

const defaultTerminalMissionRetention = 200

// MissionNotFoundError is the error of a missing mission (code MISSION_NOT_FOUND).
type MissionNotFoundError struct {
	MissionID  string
	MissionDir string
	msg        string
}

func (e *MissionNotFoundError) Error() string { return e.msg }

func newMissionNotFound(id string, loc MissionStoreLocation) *MissionNotFoundError {
	return &MissionNotFoundError{
		MissionID: id, MissionDir: loc.MissionDir,
		msg: fmt.Sprintf("Mission '%s' was not found in mission directory '%s' for project root '%s'. If it was created in another worktree, run the request from that worktree.", id, loc.MissionDir, loc.ProjectRoot),
	}
}

// missionUUID is crypto.randomUUID.
func missionUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("pi-subagents: no randomness: " + err.Error())
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func expandConfiguredPath(value, projectRoot string) string {
	expanded := value
	if strings.HasPrefix(value, "~/") {
		expanded = filepath.Join(homeDir(), value[2:])
	}
	if filepath.IsAbs(expanded) {
		return filepath.Clean(expanded)
	}
	return filepath.Join(projectRoot, expanded)
}

// validateMissionStoreConfig is validateMissionStoreConfig for a decoded config.missions (nil: absent).
func validateMissionStoreConfig(value any, present bool, label string) (*MissionStoreConfig, error) {
	if !present {
		return nil, nil
	}
	o, err := missionAsObject(value, label)
	if err != nil {
		return nil, err
	}
	for _, k := range o.order() {
		switch k {
		case "enabled", "directory", "globalIndex", "globalIndexDir", "retainTerminal":
		default:
			return nil, fmt.Errorf("%s.%s is unknown", label, k)
		}
	}
	cfg := &MissionStoreConfig{}
	if v, ok := dynGet(o, "enabled"); ok {
		b, isBool := v.(bool)
		if !isBool {
			return nil, fmt.Errorf("%s.enabled must be boolean", label)
		}
		cfg.Enabled = &b
	}
	if v, ok := dynGet(o, "globalIndex"); ok {
		b, isBool := v.(bool)
		if !isBool {
			return nil, fmt.Errorf("%s.globalIndex must be boolean", label)
		}
		cfg.GlobalIndex = &b
	}
	if v, ok := dynGet(o, "retainTerminal"); ok {
		f, isNum := v.(float64)
		if !isNum || f != float64(int64(f)) || f < 1 {
			return nil, fmt.Errorf("%s.retainTerminal must be a positive integer", label)
		}
		n := int64(f)
		cfg.RetainTerminal = &n
	}
	if cfg.Directory, _, err = missionOptionalString(o, "directory", label+".directory"); err != nil {
		return nil, err
	}
	if cfg.GlobalIndexDir, _, err = missionOptionalString(o, "globalIndexDir", label+".globalIndexDir"); err != nil {
		return nil, err
	}
	return cfg, nil
}

// resolveMissionStoreLocation is resolveMissionStoreLocation; agentDir "" is the user's agent directory.
func resolveMissionStoreLocation(projectRoot string, cfg *MissionStoreConfig, agentDirOverride string) MissionStoreLocation {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		root = filepath.Clean(projectRoot)
	}
	dir := agentDirOverride
	if dir == "" {
		dir = agentDir()
	}
	loc := MissionStoreLocation{ProjectRoot: root, WriteGlobalIndex: cfg == nil || cfg.GlobalIndex == nil || *cfg.GlobalIndex}
	if cfg != nil && cfg.Directory != "" {
		loc.MissionDir = expandConfiguredPath(cfg.Directory, root)
	} else {
		loc.MissionDir = filepath.Join(dir, "missions", "projects", sha256Hex(root))
	}
	if cfg != nil && cfg.GlobalIndexDir != "" {
		loc.GlobalIndexDir = expandConfiguredPath(cfg.GlobalIndexDir, root)
	} else {
		loc.GlobalIndexDir = filepath.Join(dir, "missions", "index")
	}
	if cfg != nil {
		loc.RetainTerminal = cfg.RetainTerminal
	}
	return loc
}

func (loc MissionStoreLocation) retention() int {
	if loc.RetainTerminal != nil {
		return int(*loc.RetainTerminal)
	}
	return defaultTerminalMissionRetention
}

// missionRecordPath is missionRecordPath (it validates the id).
func missionRecordPath(loc MissionStoreLocation, id string) (string, error) {
	valid, err := validateMissionID(id, "missionId")
	if err != nil {
		return "", err
	}
	return filepath.Join(loc.MissionDir, valid+".json"), nil
}

func mustRecordPath(loc MissionStoreLocation, id string) string {
	p, err := missionRecordPath(loc, id)
	if err != nil {
		panic(err)
	}
	return p
}

func missionIndexPath(loc MissionStoreLocation, id string) string {
	return filepath.Join(loc.GlobalIndexDir, sha256Hex(loc.ProjectRoot+"\x00"+id)+".json")
}

// missionParseJSON is JSON.parse with V8's error message.
func missionParseJSON(text string) (any, string) {
	if msg := workflowJSONError(text); msg != "" {
		return nil, msg
	}
	v, err := parseJSON([]byte(text))
	if err != nil {
		return nil, err.Error()
	}
	return v, ""
}

func parseMissionIndexEntry(value any, source string) (*MissionIndexEntry, error) {
	o, err := missionAsObject(value, source)
	if err != nil {
		return nil, err
	}
	if v, ok := o.vals["schemaVersion"].(float64); !ok || v != 1 {
		return nil, fmt.Errorf("%s.schemaVersion must be 1", source)
	}
	e := &MissionIndexEntry{}
	if e.MissionID, err = validateMissionID(o.vals["missionId"], source+".missionId"); err != nil {
		return nil, err
	}
	if e.ProjectRoot, err = missionRequiredString(o.vals["projectRoot"], source+".projectRoot"); err != nil {
		return nil, err
	}
	if e.RecordPath, err = missionRequiredString(o.vals["recordPath"], source+".recordPath"); err != nil {
		return nil, err
	}
	if e.Title, err = missionRequiredString(o.vals["title"], source+".title"); err != nil {
		return nil, err
	}
	if e.Status, err = missionStatusOf(o.vals["status"], source+".status"); err != nil {
		return nil, err
	}
	if e.UpdatedAt, err = missionTimestamp(o.vals["updatedAt"], source+".updatedAt"); err != nil {
		return nil, err
	}
	if e.LastRunID, _, err = missionOptionalString(o, "lastRunId", source+".lastRunId"); err != nil {
		return nil, err
	}
	return e, nil
}

func (e MissionIndexEntry) toObject() *jsObject {
	o := newObject()
	o.set("schemaVersion", float64(1))
	o.set("missionId", e.MissionID)
	o.set("projectRoot", e.ProjectRoot)
	o.set("recordPath", e.RecordPath)
	o.set("title", e.Title)
	o.set("status", e.Status)
	o.set("updatedAt", e.UpdatedAt)
	setIf(o, "lastRunId", e.LastRunID)
	return o
}

// writeMission validates a record and writes it and its index pointer.
func writeMission(loc MissionStoreLocation, record *MissionRecord) (*MissionRecord, error) {
	validated, err := parseMissionRecord(record.toObject(), "mission record")
	if err != nil {
		return nil, err
	}
	recordPath, err := missionRecordPath(loc, validated.ID)
	if err != nil {
		return nil, err
	}
	if err := writePrivateAtomicJSON(recordPath, validated.toObject()); err != nil {
		return nil, err
	}
	if loc.WriteGlobalIndex {
		entry := MissionIndexEntry{MissionID: validated.ID, ProjectRoot: loc.ProjectRoot, RecordPath: recordPath, Title: validated.Title, Status: validated.Status, UpdatedAt: validated.UpdatedAt}
		if n := len(validated.Runs); n > 0 {
			entry.LastRunID = validated.Runs[n-1].RunID
		}
		if err := writePrivateAtomicJSON(missionIndexPath(loc, validated.ID), entry.toObject()); err != nil {
			return nil, err
		}
	}
	return validated, nil
}

func pruneTerminalMissions(loc MissionStoreLocation, maxTerminal int) {
	listed, _ := listMissions(loc)
	var terminal []*MissionRecord
	for _, r := range listed.Records {
		if isTerminalMissionStatus(r.Status) {
			terminal = append(terminal, r)
		}
	}
	sort.SliceStable(terminal, func(i, j int) bool { return terminal[i].UpdatedAt > terminal[j].UpdatedAt })
	if len(terminal) <= maxTerminal {
		return
	}
	for _, r := range terminal[maxTerminal:] {
		// Retention is best-effort and must never block a launch.
		path, err := missionRecordPath(loc, r.ID)
		if err != nil {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(loc.MissionDir, r.ID))
		if loc.WriteGlobalIndex {
			_ = os.Remove(missionIndexPath(loc, r.ID))
		}
	}
}

// MissionCreateInput is MissionCreateInput.
type MissionCreateInput struct {
	Title          string
	Objective      string
	Goal           bool
	Budget         *MissionTokenBudget
	Status         string // "": planned
	Labels         []string
	HasLabels      bool
	OwnerSessionID string
}

// createMission is createMission; retain < 0 uses the location's bound.
func createMission(loc MissionStoreLocation, in MissionCreateInput, now time.Time, retain int) (*MissionRecord, error) {
	createdAt := isoMillis(now)
	title, err := missionRequiredString(in.Title, "mission.title")
	if err != nil {
		return nil, err
	}
	objective, err := missionRequiredString(in.Objective, "mission.objective")
	if err != nil {
		return nil, err
	}
	record := &MissionRecord{
		ID: missionUUID(), Title: jsTrim(title), Objective: jsTrim(objective), Status: in.Status, CreatedAt: createdAt, UpdatedAt: createdAt,
		Cwd: loc.ProjectRoot, Runs: []MissionRunLink{}, WorkflowChildren: []MissionWorkflowChild{}, Decisions: []MissionDecision{}, Artifacts: []MissionArtifact{}, Receipts: []MissionReceipt{},
	}
	if record.Status == "" {
		record.Status = "planned"
	}
	if in.Goal {
		record.Goal = &MissionGoal{Status: "active"}
		record.Usage = &MissionTokenUsage{}
	}
	if in.Budget != nil {
		if record.Budget, err = parseMissionBudget(tokensObject(in.Budget.Tokens), "mission.budget"); err != nil {
			return nil, err
		}
	}
	if in.OwnerSessionID != "" {
		if record.OwnerSessionID, err = missionRequiredString(in.OwnerSessionID, "mission.ownerSessionId"); err != nil {
			return nil, err
		}
	}
	if in.HasLabels {
		if record.Labels, err = stringArrayOf(anyStrings(in.Labels), "mission.labels"); err != nil {
			return nil, err
		}
	}
	if in.Goal && in.Budget == nil {
		return nil, errors.New("mission.budget is required when mission.goal is true")
	}
	created, err := writeMission(loc, record)
	if err != nil {
		return nil, err
	}
	if retain < 0 {
		retain = loc.retention()
	}
	pruneTerminalMissions(loc, retain)
	return created, nil
}

// readMission is readMission.
func readMission(loc MissionStoreLocation, id string) (*MissionRecord, error) {
	filePath, err := missionRecordPath(loc, id)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, newMissionNotFound(id, loc)
		}
		return nil, err
	}
	parsed, msg := missionParseJSON(string(raw))
	if msg != "" {
		return nil, fmt.Errorf("Invalid mission file '%s': %s", filePath, msg)
	}
	record, err := parseMissionRecord(parsed, filePath)
	if err != nil {
		return nil, fmt.Errorf("Invalid mission file '%s': %s", filePath, err.Error())
	}
	return record, nil
}

func jsonFileNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

func readMissionFile(filePath string) (*MissionRecord, error) {
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	parsed, msg := missionParseJSON(string(raw))
	if msg != "" {
		return nil, errors.New(msg)
	}
	return parseMissionRecord(parsed, filePath)
}

// listMissions is listMissions.
func listMissions(loc MissionStoreLocation) (MissionListResult, error) {
	res := MissionListResult{Records: []*MissionRecord{}, Warnings: []string{}}
	if _, err := os.Stat(loc.MissionDir); err != nil {
		return res, nil
	}
	for _, name := range jsonFileNames(loc.MissionDir) {
		filePath := filepath.Join(loc.MissionDir, name)
		record, err := readMissionFile(filePath)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("Skipped corrupt mission '%s': %s", filePath, err.Error()))
			continue
		}
		res.Records = append(res.Records, record)
	}
	sort.SliceStable(res.Records, func(i, j int) bool { return res.Records[i].UpdatedAt > res.Records[j].UpdatedAt })
	return res, nil
}

// listGlobalMissions is listGlobalMissions.
func listGlobalMissions(globalIndexDir string) GlobalMissionListResult {
	res := GlobalMissionListResult{Entries: []GlobalMissionIndexRecord{}, Warnings: []string{}}
	if _, err := os.Stat(globalIndexDir); err != nil {
		return res
	}
	for _, name := range jsonFileNames(globalIndexDir) {
		filePath := filepath.Join(globalIndexDir, name)
		raw, err := os.ReadFile(filePath)
		var entry *MissionIndexEntry
		if err == nil {
			parsed, msg := missionParseJSON(string(raw))
			if msg != "" {
				err = errors.New(msg)
			} else {
				entry, err = parseMissionIndexEntry(parsed, filePath)
			}
		}
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("Skipped corrupt global mission index entry '%s': %s", filePath, err.Error()))
			continue
		}
		record, rerr := readMissionFile(entry.RecordPath)
		if rerr == nil && record.ID != entry.MissionID {
			rerr = fmt.Errorf("record id '%s' does not match index id '%s'", record.ID, entry.MissionID)
		}
		switch {
		case rerr == nil:
			res.Entries = append(res.Entries, GlobalMissionIndexRecord{MissionIndexEntry: *entry})
		case errors.Is(rerr, fs.ErrNotExist):
			if rmErr := os.Remove(filePath); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
				res.Warnings = append(res.Warnings, fmt.Sprintf("Failed to remove stale global mission pointer '%s': %s", filePath, rmErr.Error()))
			} else {
				res.Warnings = append(res.Warnings, fmt.Sprintf("Removed stale global mission pointer '%s' because '%s' no longer exists.", filePath, entry.RecordPath))
			}
		default:
			res.Entries = append(res.Entries, GlobalMissionIndexRecord{MissionIndexEntry: *entry, Stale: true, StaleReason: rerr.Error()})
		}
	}
	sort.SliceStable(res.Entries, func(i, j int) bool { return res.Entries[i].UpdatedAt > res.Entries[j].UpdatedAt })
	return res
}
