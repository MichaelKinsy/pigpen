// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The parts of the orchestrator that need a host: the command's UI calls, the truncation of a fetched body and the
// temp-file spill. upstream: web-tools.ts registerWebSearchConfigCommand's handler, spillFullContentToTempFile,
// formatTruncationFooter and the truncation accounting in registerWebFetchTool.

// commandHost is what the command needs from the host: an interactive surface or the reason there is none. upstream:
// the ctx.hasUI and ctx.ui the handler closes over.
type commandHost interface {
	hasUI() bool
	selectOne(title string, options []string) (string, bool)
	input(label, placeholder string) (string, bool)
	notify(message, level string)
}

// notification levels. upstream: the "info" and "error" kinds passed to ctx.ui.notify.
const (
	notifyInfo  = "info"
	notifyError = "error"
)

// runConfigCommand is the whole /web-tools handler: the interactive guard, --show, the picker, the prompt and the save.
// It takes the host rather than a pi context, so every branch the upstream twins drive through a mocked context is a
// direct call. upstream: web-tools.ts registerWebSearchConfigCommand.
func runConfigCommand(host commandHost, args string, cfg config, env func(string) string, path string, save func(config) bool) {
	if !host.hasUI() {
		host.notify("/"+webToolsCommandName+" requires interactive mode", notifyError)
		return
	}
	if strings.Contains(args, showFlag) {
		host.notify(strings.Join(showConfigLines(cfg, env, interceptorsConfig{}, githubEnabledFrom(cfg), strings.TrimSpace(env(gitHubTokenEnvVar))), "\n"), notifyInfo)
		return
	}

	labels, _ := providerPickerLabels(cfg, env)
	selectedLabel, ok := host.selectOne("Search provider", labels)
	if !ok {
		host.notify("Web search config unchanged", notifyInfo)
		return
	}
	selectedMeta, found := matchProviderByLabel(selectedLabel)
	if !found {
		host.notify("Web search config unchanged", notifyInfo)
		return
	}

	// A provider that declares a configure callback owns its prompt flow; the orchestrator owns persistence.
	if selectedMeta.HasConfigure {
		change, completed := configureProvider(host, selectedMeta, cfg, env)
		if !completed {
			host.notify("Web search config unchanged", notifyInfo)
			return
		}
		if !save(saveProviderConfig(cfg, selectedMeta.Name, change)) {
			host.notify(providerConfigSaveFailedMessage(selectedMeta.Label, path), notifyError)
			return
		}
		host.notify(providerConfigSavedMessage(selectedMeta.Label, path, change), notifyInfo)
		return
	}

	existingKey := existingProviderKey(cfg, selectedMeta.Name)
	answer, answered := host.input(selectedMeta.Label+" API key", keyPromptPlaceholder(existingKey))
	if !answered {
		host.notify("Web search config unchanged", notifyInfo)
		return
	}
	keyToWrite, shouldWrite := keyPromptOutcome(inputOutcome{Value: answer}, existingKey)
	if !shouldWrite {
		host.notify("Web search config unchanged", notifyInfo)
		return
	}
	if !save(saveProviderConfig(cfg, selectedMeta.Name, providerConfigChange{APIKey: keyToWrite, HasAPIKey: true})) {
		// Don't lie about persistence: a "Saved …" message followed by an auth error on the next web_search would
		// point the user at the wrong surface.
		host.notify(keySaveFailedMessage(selectedMeta.Label, path), notifyError)
		return
	}
	host.notify(keySavedMessage(selectedMeta.Label, path, strings.TrimSpace(answer) != ""), notifyInfo)
}

// configureProvider dispatches to the provider's own configure() when it has one, and to the shared key prompt
// otherwise. upstream: the configure dispatch and the plain key prompt in registerWebSearchConfigCommand.
func configureProvider(host commandHost, meta providerMeta, cfg config, env func(string) string) (providerConfigChange, bool) {
	ui := &hostConfigUI{host: host}
	current := providerConfigCurrent{
		APIKey:    resolveProviderAPIKey(meta.Name, cfg, env),
		HasAPIKey: resolveProviderAPIKey(meta.Name, cfg, env) != "",
	}
	if cfg.URLFor(meta.Name) != "" {
		current.BaseURL, current.HasBaseURL = cfg.URLFor(meta.Name), true
	} else if meta.DefaultBaseURL != "" {
		current.BaseURL, current.HasBaseURL = meta.DefaultBaseURL, true
	}
	switch meta.Name {
	case "ollama":
		return configureOllama(ui, current)
	case "searxng":
		return configureSearxng(ui, current)
	}
	// A provider without a configure callback gets the plain key prompt.
	key, hasKey, ok := promptForOptionalKey(ui, meta.Label+" API key", existingProviderKey(cfg, meta.Name), current.HasAPIKey)
	if !ok {
		return providerConfigChange{}, false
	}
	return providerConfigChange{APIKey: key, HasAPIKey: hasKey}, true
}

// providerConfigSavedMessage is the confirmation after a provider's own setup, naming the URL when it set one.
// upstream: the two ctx.ui.notify calls in the configure branch.
func providerConfigSavedMessage(label, path string, change providerConfigChange) string {
	if change.HasBaseURL && change.BaseURL != "" {
		return fmt.Sprintf("Saved %s config (url: %s) to %s", label, change.BaseURL, path)
	}
	return fmt.Sprintf("Saved %s config to %s", label, path)
}

// providerConfigSaveFailedMessage is the failure text for the configure branch. upstream: its ctx.ui.notify.
func providerConfigSaveFailedMessage(label, path string) string {
	return fmt.Sprintf("Failed to save %s config to %s — disk write failed", label, path)
}

// hostConfigUI adapts a host to the provider configure contract. upstream: ctx.ui passed straight through.
type hostConfigUI struct {
	host commandHost
}

func (u *hostConfigUI) Input(label, placeholder string) userInput {
	value, ok := u.host.input(label, placeholder)
	if !ok {
		return userInput{}
	}
	return userInput{Value: value, Set: true}
}

// URLFor is the configured base URL of a provider, or the empty string. upstream: current.baseUrls?.[name].
func (c config) URLFor(provider string) string { return c.BaseURLs[provider] }

// githubEnabledFrom reads the interceptor's opt-in out of the config, for the --show lines. upstream: the
// getActiveGitHubInterceptor check in formatShowConfigMessage.
func githubEnabledFrom(cfg config) bool {
	if cfg.Interceptors == nil || cfg.Interceptors.GitHub == nil {
		return false
	}
	gh := cfg.Interceptors.GitHub
	if gh.Object != nil {
		return gh.Object.Enabled == nil || *gh.Object.Enabled
	}
	return !gh.Disabled
}

// truncationResult is what the fetch tool accounts for after capping a body. upstream: TruncationResult.
type truncationResult struct {
	TotalLines   int
	OutputLines  int
	TotalBytes   int
	OutputBytes  int
	Truncated    bool
	TempFilePath string
}

// truncateBody caps a fetched body at maxBytes, counting lines and bytes as the original does, and spills the full
// text to a temp file when anything was cut. upstream: the truncation branch in registerWebFetchTool.
func truncateBody(content string, maxBytes int) (string, truncationResult, error) {
	total := truncationResult{
		TotalLines:  len(strings.Split(content, "\n")),
		TotalBytes:  len(content),
		OutputLines: len(strings.Split(content, "\n")),
		OutputBytes: len(content),
	}
	if len(content) <= maxBytes {
		return content, total, nil
	}
	cut := content[:maxBytes]
	if idx := strings.LastIndex(cut, "\n"); idx >= 0 {
		cut = content[:idx]
	}
	tempFile, err := spillFullContentToTempFile(content)
	if err != nil {
		return "", truncationResult{}, err
	}
	kept := strings.Split(cut, "\n")
	return cut, truncationResult{
		TotalLines:   total.TotalLines,
		OutputLines:  len(kept),
		TotalBytes:   total.TotalBytes,
		OutputBytes:  len(cut),
		Truncated:    true,
		TempFilePath: tempFile,
	}, nil
}

// spillFullContentToTempFile writes the whole body next to the tool's temp directory and returns its path. upstream:
// web-tools.ts spillFullContentToTempFile.
func spillFullContentToTempFile(content string) (string, error) {
	dir, err := os.MkdirTemp("", fetchTempDirPrefix)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, fetchTempFileName)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// formatTruncationFooter is the note under a truncated body: how much is shown, how much was left out, and where the
// full text landed. upstream: web-tools.ts formatTruncationFooter.
func formatTruncationFooter(t truncationResult) string {
	omittedLines := t.TotalLines - t.OutputLines
	omittedBytes := t.TotalBytes - t.OutputBytes
	return "\n\n[Content truncated: showing " + itoa(t.OutputLines) + " of " + itoa(t.TotalLines) + " lines" +
		" (" + formatFileSize(int64(t.OutputBytes)) + " of " + formatFileSize(int64(t.TotalBytes)) + ")." +
		" " + itoa(omittedLines) + " lines (" + formatFileSize(int64(omittedBytes)) + ") omitted." +
		" Full content saved to: " + t.TempFilePath + "]"
}
