package tintinweb_subagents

import (
	"strconv"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Twins of the original's test/schedule-menu.test.ts: `/agents → Scheduled jobs` identifies the job to cancel by the row
// picked, and the labels are numbered so rows that format identically after truncation stay distinct.
const fScheduleMenu = "schedule-menu"

// scheduleMenuRig is an app with a started scheduler over a temp store, and the menu bound to a scripted UI.
func scheduleMenuRig(t *testing.T, ui *fakeMenuUI) (*agentsMenu, *schedRig) {
	t.Helper()
	rig := startSched(t, nil, newMockManager())
	a := newApp(sdk.New("schedule-menu-test"), nil)
	a.schedule.sched = rig.sched
	return &agentsMenu{a: a, ui: ui, cwd: t.TempDir()}, rig
}

func addMenuJob(t *testing.T, rig *schedRig, name string) scheduledSubagent {
	t.Helper()
	job, err := rig.sched.addJob(newJobInput{Name: name, Description: name, Schedule: "0 0 9 * * 1", SubagentType: "general-purpose", Prompt: "go"})
	eq(t, err, nil)
	return job
}

func jobIDs(rig *schedRig) []string {
	var ids []string
	for _, j := range rig.sched.list() {
		ids = append(ids, j.ID)
	}
	return ids
}

func contains(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

func TestSchedulesMenu(t *testing.T) {
	tw(t, fScheduleMenu, "warns and does nothing when the scheduler is not active", func(t *testing.T) {
		ui := &fakeMenuUI{}
		a := newApp(sdk.New("schedule-menu-test"), nil) // a scheduler that was never started
		(&agentsMenu{a: a, ui: ui, cwd: t.TempDir()}).showSchedulesMenu()
		eq(t, len(ui.selects), 0)
		found := false
		for _, n := range ui.notes {
			found = found || (strings.Contains(n.Message, "not active") && n.Level == "warning")
		}
		eq(t, found, true)
	})
	tw(t, fScheduleMenu, "reports an empty list without opening a picker", func(t *testing.T) {
		ui := &fakeMenuUI{}
		m, _ := scheduleMenuRig(t, ui)
		m.showSchedulesMenu()
		eq(t, len(ui.selects), 0)
		eq(t, ui.notified("No scheduled jobs.", "info"), true)
	})
	tw(t, fScheduleMenu, "cancels the job the user selected", func(t *testing.T) {
		ui := &fakeMenuUI{pick: pickIndex(1), confirm: func(string, string) bool { return true }}
		m, rig := scheduleMenuRig(t, ui)
		a := addMenuJob(t, rig, "alpha job")
		b := addMenuJob(t, rig, "beta job")
		m.showSchedulesMenu()
		eq(t, jobIDs(rig), []string{a.ID})
		eq(t, contains(jobIDs(rig), b.ID), false)
	})

	// Names are truncated to 18 chars for column alignment, and they come from the LLM-authored `Agent` description, so
	// two rows formatting identically is ordinary. Resolving the pick by string match then cancelled whichever came first.
	const nameA, nameB = "review the auth module A", "review the auth module B"
	tw(t, fScheduleMenu, "gives every row a distinct label", func(t *testing.T) {
		ui := &fakeMenuUI{}
		m, rig := scheduleMenuRig(t, ui)
		addMenuJob(t, rig, nameA)
		addMenuJob(t, rig, nameB)
		m.showSchedulesMenu()
		labels := ui.selects[0].Options
		seen := map[string]bool{}
		for _, l := range labels {
			seen[l] = true
		}
		eq(t, len(seen), len(labels))
	})
	tw(t, fScheduleMenu, "cancels the second job when the second row is picked", func(t *testing.T) {
		ui := &fakeMenuUI{pick: pickIndex(1), confirm: func(string, string) bool { return true }}
		m, rig := scheduleMenuRig(t, ui)
		first := addMenuJob(t, rig, nameA)
		second := addMenuJob(t, rig, nameB)
		m.showSchedulesMenu()
		eq(t, jobIDs(rig), []string{first.ID})
		eq(t, contains(jobIDs(rig), second.ID), false)
	})
	tw(t, fScheduleMenu, "cancels the first job when the first row is picked", func(t *testing.T) {
		// The other direction: an off-by-one or a last-match resolver would pass the test above and fail this one.
		ui := &fakeMenuUI{pick: pickIndex(0), confirm: func(string, string) bool { return true }}
		m, rig := scheduleMenuRig(t, ui)
		first := addMenuJob(t, rig, nameA)
		second := addMenuJob(t, rig, nameB)
		m.showSchedulesMenu()
		eq(t, jobIDs(rig), []string{second.ID})
		eq(t, contains(jobIDs(rig), first.ID), false)
	})
	tw(t, fScheduleMenu, "shows the SELECTED job's untruncated details in the confirm dialog", func(t *testing.T) {
		// Guards the half of the bug the user could never catch: deleting the right job while confirming against the
		// wrong one, or vice versa.
		ui := &fakeMenuUI{pick: pickIndex(1), confirm: func(string, string) bool { return true }}
		m, rig := scheduleMenuRig(t, ui)
		addMenuJob(t, rig, nameA)
		addMenuJob(t, rig, nameB)
		m.showSchedulesMenu()
		hasStr(t, ui.confirms[0].Message, nameB)
		lacksStr(t, ui.confirms[0].Message, nameA)
	})
	tw(t, fScheduleMenu, "cancels the right job past the single-digit boundary", func(t *testing.T) {
		// 11 rows: insurance against a refactor that parses the number back out of the label and confuses "1" with "11".
		ui := &fakeMenuUI{pick: pickIndex(10), confirm: func(string, string) bool { return true }}
		m, rig := scheduleMenuRig(t, ui)
		var jobs []scheduledSubagent
		for i := range 11 {
			jobs = append(jobs, addMenuJob(t, rig, "review the auth module "+strconv.Itoa(i)))
		}
		m.showSchedulesMenu()
		surviving := jobIDs(rig)
		eq(t, len(surviving), 10)
		eq(t, contains(surviving, jobs[10].ID), false)
	})
	tw(t, fScheduleMenu, "cancels nothing when the picker returns a label we never offered", func(t *testing.T) {
		ui := &fakeMenuUI{pick: func(string, []string) (string, bool) { return "something else entirely", true }}
		m, rig := scheduleMenuRig(t, ui)
		addMenuJob(t, rig, "alpha job")
		m.showSchedulesMenu()
		eq(t, len(ui.confirms), 0)
		eq(t, len(rig.sched.list()), 1)
	})
	tw(t, fScheduleMenu, "leaves every job intact when the user escapes the picker", func(t *testing.T) {
		ui := &fakeMenuUI{pick: func(string, []string) (string, bool) { return "", false }}
		m, rig := scheduleMenuRig(t, ui)
		addMenuJob(t, rig, "alpha job")
		addMenuJob(t, rig, "beta job")
		m.showSchedulesMenu()
		eq(t, len(ui.confirms), 0)
		eq(t, len(rig.sched.list()), 2)
	})
	tw(t, fScheduleMenu, "leaves the job intact when the user declines the confirm", func(t *testing.T) {
		ui := &fakeMenuUI{pick: pickIndex(0), confirm: func(string, string) bool { return false }}
		m, rig := scheduleMenuRig(t, ui)
		addMenuJob(t, rig, "alpha job")
		m.showSchedulesMenu()
		eq(t, len(rig.sched.list()), 1)
		for _, n := range ui.notes {
			eq(t, strings.Contains(n.Message, "Cancelled") && n.Level == "info", false)
		}
	})
}
