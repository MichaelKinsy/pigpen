package fsutil

import "testing"

func TestKillPlanNeverRunsTaskkillOnAPidThatWasReleasedOrHasAJob(t *testing.T) {
	for _, c := range []struct {
		name             string
		hasJob, reaped   bool
		useJob, taskkill bool
	}{
		{"live child with a job: the job only", true, false, true, false},
		{"waited child with a job: the job only, never a pid that may be reused", true, true, true, false},
		{"live child without a job: taskkill", false, false, false, true},
		{"waited child without a job (second call): nothing", false, true, false, false},
	} {
		useJob, taskkill := killPlan(c.hasJob, c.reaped)
		if useJob != c.useJob || taskkill != c.taskkill {
			t.Errorf("%s: killPlan(%v, %v) = job %v, taskkill %v; want %v, %v", c.name, c.hasJob, c.reaped, useJob, taskkill, c.useJob, c.taskkill)
		}
	}
}
