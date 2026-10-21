package a2aext

import (
	"strings"
	"testing"
)

func TestTaskRetentionDaysDefaultsAndValidates(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `{"listen":"127.0.0.1:5555","insecureNoAuth":true}`)
	cfg, err := LoadConfig(LoadOptions{ConfigHome: dir, Getenv: envFrom(nil)})
	if err != nil || cfg.TaskRetentionDays != defaultTaskRetentionDays {
		t.Fatalf("default retention %d, %v", cfg.TaskRetentionDays, err)
	}
	writeConfig(t, dir, `{"listen":"127.0.0.1:5555","insecureNoAuth":true,"taskRetentionDays":3}`)
	if cfg, err = LoadConfig(LoadOptions{ConfigHome: dir, Getenv: envFrom(nil)}); err != nil || cfg.TaskRetentionDays != 3 {
		t.Fatalf("configured retention %d, %v", cfg.TaskRetentionDays, err)
	}
	writeConfig(t, dir, `{"listen":"127.0.0.1:5555","insecureNoAuth":true,"taskRetentionDays":-1}`)
	if _, err = LoadConfig(LoadOptions{ConfigHome: dir, Getenv: envFrom(nil)}); err == nil || !strings.Contains(err.Error(), "taskRetentionDays") {
		t.Fatalf("a negative retention is a mistake: %v", err)
	}
}

func TestServerKeepsFinishedTasksForTheConfiguredDays(t *testing.T) {
	cfg := persistentConfig(t)
	cfg.TaskRetentionDays = 0 // applyDefaults
	s, err := NewServer(cfg, &scriptedWorker{}, restartEnv)
	if err != nil {
		t.Fatal(err)
	}
	if s.cfg.TaskRetentionDays != defaultTaskRetentionDays {
		t.Fatalf("the server applies the default: %d", s.cfg.TaskRetentionDays)
	}
}
