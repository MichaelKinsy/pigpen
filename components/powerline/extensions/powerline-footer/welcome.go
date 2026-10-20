package powerline_footer

import (
	"context"
	"time"
)

// Red stub: signatures of welcome.go with no behavior.

type recentSession struct{ Name, TimeAgo string }
type loadedCounts struct{ ContextFiles, Skills, PromptTemplates int }
type slashCommand struct{ Name, Source string }
type welcomeData struct {
	ModelName, ProviderName string
	RecentSessions          []recentSession
	LoadedCounts            loadedCounts
	InitialContextTokens    *float64
}
type welcomeComponent struct{}
type welcomeHeader struct{}

func newWelcomeComponent(modelName, providerName string, recentSessions []recentSession, counts loadedCounts, initialContextTokens *float64) *welcomeComponent {
	return &welcomeComponent{}
}
func (w *welcomeComponent) setCountdown(seconds int)      {}
func (w *welcomeComponent) render(termWidth int) []string { return nil }
func newWelcomeHeader(modelName, providerName string, recentSessions []recentSession, counts loadedCounts, initialContextTokens *float64) *welcomeHeader {
	return &welcomeHeader{}
}
func (h *welcomeHeader) render(termWidth int) []string                          { return nil }
func countLoadedCommands(commands []slashCommand) (skills, promptTemplates int) { return 0, 0 }
func discoverLoadedCounts(commands []slashCommand) loadedCounts                 { return loadedCounts{} }

var welcomeFSHook func(op, path string)

func getRecentSessions(ctx context.Context, maxCount int) ([]recentSession, error) { return nil, nil }

var _ = time.Second
