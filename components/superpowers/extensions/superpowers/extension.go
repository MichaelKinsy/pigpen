// Package superpowers is a Go port of obra/superpowers' Pi extension
// (.pi/extensions/superpowers.ts, release v6.4.2): it contributes the Skills directory and injects the
// using-superpowers bootstrap as a user message into the first model request of a session, and again into
// the first request after a compaction.
package superpowers

import (
	"path/filepath"
	"sync/atomic"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// bootstrapSkillName is the shipped Skill the bootstrap is built from; the original's directory is
// skills/using-superpowers.
const bootstrapSkillName = "skill:pigpen-superpowers-using-superpowers"

// skillsDir is the directory the package's Skills load from: the parent of the shipped using-superpowers
// Skill's directory, as the host reports it ("" when the host does not list that Skill).
//
// The original takes it from import.meta.url (<package root>/skills). A Go extension has no such location: its
// binary is built elsewhere, with -trimpath, and the SDK has no accessor for the extension's own directory,
// so the port asks the host where the package's Skills were loaded from.
func skillsDir(ctx sdk.Context) (string, error) {
	commands, err := ctx.GetCommands()
	if err != nil {
		return "", err
	}
	for _, command := range commands {
		if command.Source == "skill" && command.Name == bootstrapSkillName && command.SourceInfo.Path != "" {
			return filepath.Dir(filepath.Dir(command.SourceInfo.Path)), nil
		}
	}
	return "", nil
}

// Extension returns the bootstrap extension.
func Extension() *sdk.Extension {
	e := sdk.New("superpowers")

	// let injectBootstrap = true: one flag per loaded extension.
	var injectBootstrap atomic.Bool
	injectBootstrap.Store(true)

	// pi.on("resources_discover", async () => ({ skillPaths: [skillsDir] }))
	e.OnEvent(sdk.EventResourcesDiscover, func(ctx sdk.Context, _ map[string]any) (any, error) {
		dir, err := skillsDir(ctx)
		if err != nil || dir == "" {
			return nil, err
		}
		return map[string]any{"skillPaths": []string{dir}}, nil
	})

	// pi.on("session_start" | "session_compact", ...): inject again.
	e.OnEvent(sdk.EventSessionStart, func(_ sdk.Context, _ map[string]any) (any, error) {
		injectBootstrap.Store(true)
		return nil, nil
	})
	e.OnEvent(sdk.EventSessionCompact, func(_ sdk.Context, _ map[string]any) (any, error) {
		injectBootstrap.Store(true)
		return nil, nil
	})

	// pi.on("agent_end", ...): the first request is answered, stop injecting.
	e.OnEvent(sdk.EventAgentEnd, func(_ sdk.Context, _ map[string]any) (any, error) {
		injectBootstrap.Store(false)
		return nil, nil
	})

	// pi.on("context", ...): put the bootstrap in front of the conversation, after any compaction summaries.
	e.OnEvent(sdk.EventContext, func(_ sdk.Context, data map[string]any) (any, error) {
		if !injectBootstrap.Load() {
			return nil, nil
		}
		messages, _ := data["messages"].([]any)
		out := withBootstrap(messages)
		if out == nil {
			return nil, nil
		}
		return map[string]any{"messages": out}, nil
	})
	return e
}

// withBootstrap is the original's context handler once the flag is known to be set: the messages with the bootstrap
// inserted after the leading compaction summaries, or nil when a message already holds it or there is no bootstrap.
func withBootstrap(messages []any) []any {
	for _, message := range messages {
		if messageContainsBootstrap(message) {
			return nil
		}
	}
	bootstrap := bootstrapContent()
	if bootstrap == "" {
		return nil
	}
	bootstrapMessage := map[string]any{
		"role":      "user",
		"content":   []any{map[string]any{"type": "text", "text": bootstrap}},
		"timestamp": time.Now().UnixMilli(),
	}
	insertAt := firstNonCompactionSummaryIndex(messages)
	out := make([]any, 0, len(messages)+1)
	out = append(out, messages[:insertAt]...)
	out = append(out, bootstrapMessage)
	out = append(out, messages[insertAt:]...)
	return out
}
