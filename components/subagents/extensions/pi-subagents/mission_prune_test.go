package pi_subagents

import (
	"path/filepath"
	"testing"
	"time"
)

// Every launch prunes terminal missions. Within the retention there is nothing to prune, so no record file is read or parsed
// (a launch with 200 retained missions used to parse all of them twice); above it the listing reads them and drops the oldest.
func TestMissionPruneReadsNoFileWithinTheRetention(t *testing.T) {
	f := newMissionFixture(t)
	loc := f.loc
	loc.RetainTerminal = new(int64(3))
	var parsed int
	missionFileParsed = func(string) { parsed++ }
	t.Cleanup(func() { missionFileParsed = nil })
	at := func(day int) time.Time { return time.Date(2026, 1, day, 0, 0, 0, 0, time.UTC) }
	var ids []string
	for day := 1; day <= 3; day++ {
		r, err := createMission(loc, MissionCreateInput{Title: "m", Objective: "o", Status: "completed"}, at(day), -1)
		eq(t, err, nil, "create")
		ids = append(ids, r.ID)
		writeFile(t, filepath.Join(loc.MissionDir, "notes.txt"), "not a record")
	}
	eq(t, parsed, 0, "three missions with a retention of three: no file read")
	_, err := createMission(loc, MissionCreateInput{Title: "m", Objective: "o", Status: "completed"}, at(4), -1)
	eq(t, err, nil, "fourth")
	eq(t, parsed, 4, "above the retention the listing reads every record")
	_, err = readMission(loc, ids[0])
	matchesRe(t, err, `was not found`)
	_, err = readMission(loc, ids[1])
	eq(t, err, nil, "the second oldest stays")
}
