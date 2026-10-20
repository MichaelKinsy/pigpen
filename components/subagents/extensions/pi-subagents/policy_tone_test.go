package pi_subagents

import (
	"errors"
	"testing"
)

func TestRunningTone(t *testing.T) {
	const stale = "This extension ctx is stale after session replacement or reload. Do not use a captured pi or command ctx after ctx.newSession()."
	const unbound = "Extension runtime not initialized. Action methods cannot be called during extension loading."
	tw(t, "running-tone", "reads the main session's thinking level and treats a stale or unbound runtime as having none", func(t *testing.T) {
		level, err := readMainThinkingLevel(func() (string, error) { return "high", nil })
		equal(t, level, "high", "level")
		equal(t, err, nil, "error")
		level, err = readMainThinkingLevel(func() (string, error) { return "", errors.New(stale) })
		equal(t, level, "", "stale level")
		equal(t, err, nil, "stale error")
		level, err = readMainThinkingLevel(func() (string, error) { return "", errors.New(unbound) })
		equal(t, level, "", "unbound level")
		equal(t, err, nil, "unbound error")
		_, err = readMainThinkingLevel(func() (string, error) { return "", errors.New("disk on fire") })
		if err == nil {
			t.Fatal("another error must propagate")
		}
		polMatches(t, err.Error(), `disk on fire`)
	})
}
