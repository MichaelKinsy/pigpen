package pi_goal_x

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// Test ported from pi-goal-x tests/goal-settings-race.test.ts (worker: tests/settings-race-worker.mjs). The original runs two
// worker threads; here each writer is a real OS process, the test binary re-executed as the worker.

const settingsRaceWorkerEnv = "GOAL_SETTINGS_RACE_WORKER"

// TestSettingsRaceWorker is the worker script: it performs a sequence of locked mutations on ONE key of the shared global file.
func TestSettingsRaceWorker(t *testing.T) {
	file := os.Getenv(settingsRaceWorkerEnv)
	if file == "" {
		t.Skip("not a worker process")
	}
	key := os.Getenv("GOAL_SETTINGS_RACE_KEY")
	cwd, _ := os.Getwd()
	for i := range 25 {
		_, err := mutateSettingsLayer(settingsMutateInput{
			scope: settingsScopeGlobal, cwd: cwd, env: senv{piGoalGlobalSettingsFileEnv: file},
			mutation: settingsMutation{op: "set", path: []string{key}, value: fmt.Sprintf("%s-%d", key, i)},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestSettingsRaceTwins(t *testing.T) {
	tw(t, "goal-settings-race", "two concurrent processes editing different keys never lose an update", func(t *testing.T) {
		dir, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		globalFile := filepath.Join(dir, "g.json")
		settingsWrite(t, globalFile, "{}")
		const iterations = 25

		var wg sync.WaitGroup
		keys := []string{"provider", "model"}
		outs := make([][]byte, len(keys))
		errs := make([]error, len(keys))
		for i, key := range keys {
			wg.Add(1)
			go func() {
				defer wg.Done()
				cmd := exec.Command(os.Args[0], "-test.run=^TestSettingsRaceWorker$", "-test.v")
				cmd.Env = append(os.Environ(), settingsRaceWorkerEnv+"="+globalFile, "GOAL_SETTINGS_RACE_KEY="+key)
				outs[i], errs[i] = cmd.CombinedOutput()
			}()
		}
		wg.Wait()
		for i, key := range keys {
			if errs[i] != nil {
				t.Fatalf("worker %s: %v\n%s", key, errs[i], outs[i])
			}
		}

		data, err := os.ReadFile(globalFile)
		if err != nil {
			t.Fatal(err)
		}
		final := settingsPJ(t, string(data)).(*jsObject)
		eq(t, final.vals["provider"], fmt.Sprintf("provider-%d", iterations-1), "writer A's final value present")
		eq(t, final.vals["model"], fmt.Sprintf("model-%d", iterations-1), "writer B's final value present — no lost update")
		if _, err := os.Stat(globalFile + ".lock"); err == nil {
			t.Error("lock released")
		}
	})
}
