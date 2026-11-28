package lsp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// upstream: packages/coding-agent/src/lsp/clients/{index,biome-client,swiftlint-client}.ts

// LinterClient is omp's interface for CLI-backed formatters/linters (Biome, SwiftLint).
type LinterClient interface {
	Format(ctx context.Context, filePath, content string) (string, error)
	Lint(ctx context.Context, filePath string) ([]Diagnostic, error)
	Dispose()
}

type LinterClientFactory func(config ServerConfig, cwd string) LinterClient

var (
	linterCacheMu sync.Mutex
	linterCache   = make(map[string]LinterClient)
)

func getLinterClient(serverName string, cfg ServerConfig, cwd string) LinterClient {
	key := serverName + ":" + cwd
	linterCacheMu.Lock()
	defer linterCacheMu.Unlock()
	if c, ok := linterCache[key]; ok {
		return c
	}
	var c LinterClient
	if cfg.CreateClient != nil {
		c = cfg.CreateClient(cfg, cwd)
	}
	if c != nil {
		linterCache[key] = c
	}
	return c
}

func clearLinterClientCache() {
	linterCacheMu.Lock()
	defer linterCacheMu.Unlock()
	for _, c := range linterCache {
		c.Dispose()
	}
	linterCache = make(map[string]LinterClient)
}

// =============================================================================
// Biome CLI Client
// upstream: packages/coding-agent/src/lsp/clients/biome-client.ts
// =============================================================================

type biomeClient struct {
	config ServerConfig
	cwd    string
}

func newBiomeClient(cfg ServerConfig, cwd string) LinterClient {
	return &biomeClient{config: cfg, cwd: cwd}
}

func (b *biomeClient) command() string {
	if b.config.ResolvedCommand != "" {
		return b.config.ResolvedCommand
	}
	return "biome"
}

func (b *biomeClient) Format(ctx context.Context, filePath, content string) (string, error) {
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		return content, err
	}
	cmd := exec.CommandContext(ctx, b.command(), "format", "--write", filePath)
	cmd.Dir = b.cwd
	configureCmdCancel(cmd)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return content, ctx.Err()
		}
		return content, nil
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return content, err
	}
	return string(data), nil
}

func (b *biomeClient) Lint(ctx context.Context, filePath string) ([]Diagnostic, error) {
	cmd := exec.CommandContext(ctx, b.command(), "lint", "--reporter=json", filePath)
	cmd.Dir = b.cwd
	configureCmdCancel(cmd)
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil && len(out) == 0 {
		return nil, nil
	}
	return parseBiomeJSON(out, b.cwd, filePath), nil
}

func (b *biomeClient) Dispose() {}

func parseBiomeJSON(data []byte, cwd, targetFile string) []Diagnostic {
	var parsed struct {
		Diagnostics []struct {
			Category string `json:"category"`
			Severity string `json:"severity"`
			Message  string `json:"message"`
			Location *struct {
				Path  string `json:"path"`
				Start *struct {
					Line   int `json:"line"`
					Column int `json:"column"`
				} `json:"start"`
				End *struct {
					Line   int `json:"line"`
					Column int `json:"column"`
				} `json:"end"`
			} `json:"location"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil
	}
	targetAbs, _ := filepath.Abs(targetFile)
	var diags []Diagnostic
	for _, d := range parsed.Diagnostics {
		if d.Location == nil || d.Location.Path == "" {
			continue
		}
		diagFile := d.Location.Path
		if !filepath.IsAbs(diagFile) {
			diagFile = filepath.Join(cwd, diagFile)
		}
		diagAbs, _ := filepath.Abs(diagFile)
		if diagAbs != targetAbs {
			continue
		}
		startLine, startCol := 1, 1
		if d.Location.Start != nil {
			if d.Location.Start.Line > 0 {
				startLine = d.Location.Start.Line
			}
			if d.Location.Start.Column > 0 {
				startCol = d.Location.Start.Column
			}
		}
		endLine, endCol := startLine, startCol
		if d.Location.End != nil {
			if d.Location.End.Line > 0 {
				endLine = d.Location.End.Line
			}
			if d.Location.End.Column > 0 {
				endCol = d.Location.End.Column
			}
		}
		sev := SeverityWarning
		switch d.Severity {
		case "error":
			sev = SeverityError
		case "warning":
			sev = SeverityWarning
		case "info":
			sev = SeverityInfo
		case "hint":
			sev = SeverityHint
		}
		diags = append(diags, Diagnostic{
			Range: Range{
				Start: Position{Line: startLine - 1, Character: startCol - 1},
				End:   Position{Line: endLine - 1, Character: endCol - 1},
			},
			Severity: sev,
			Message:  d.Message,
			Source:   "biome",
			Code:     d.Category,
		})
	}
	return diags
}

// =============================================================================
// SwiftLint CLI Client
// upstream: packages/coding-agent/src/lsp/clients/swiftlint-client.ts
// =============================================================================

type swiftLintClient struct {
	config ServerConfig
	cwd    string
}

func newSwiftLintClient(cfg ServerConfig, cwd string) LinterClient {
	return &swiftLintClient{config: cfg, cwd: cwd}
}

func (s *swiftLintClient) command() string {
	if s.config.ResolvedCommand != "" {
		return s.config.ResolvedCommand
	}
	return "swiftlint"
}

func (s *swiftLintClient) Format(_ context.Context, _ string, content string) (string, error) {
	return content, nil
}

func (s *swiftLintClient) Lint(ctx context.Context, filePath string) ([]Diagnostic, error) {
	cmd := exec.CommandContext(ctx, s.command(), "lint", "--quiet", "--reporter", "json", filePath)
	cmd.Dir = s.cwd
	configureCmdCancel(cmd)
	out, _ := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if len(out) == 0 {
		return nil, nil
	}
	var violations []struct {
		Character int    `json:"character"`
		File      string `json:"file"`
		Line      int    `json:"line"`
		Reason    string `json:"reason"`
		RuleID    string `json:"rule_id"`
		Severity  string `json:"severity"`
	}
	if err := json.Unmarshal(out, &violations); err != nil {
		return nil, nil
	}
	diags := make([]Diagnostic, 0, len(violations))
	for _, v := range violations {
		line := max(0, v.Line-1)
		col := max(0, v.Character-1)
		sev := SeverityWarning
		if v.Severity == "Error" {
			sev = SeverityError
		}
		diags = append(diags, Diagnostic{
			Range: Range{
				Start: Position{Line: line, Character: col},
				End:   Position{Line: line, Character: col},
			},
			Severity: sev,
			Message:  v.Reason,
			Source:   "swiftlint",
			Code:     v.RuleID,
		})
	}
	return diags, nil
}

func (s *swiftLintClient) Dispose() {}
