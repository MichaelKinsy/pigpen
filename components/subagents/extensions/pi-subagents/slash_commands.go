package pi_subagents

import (
	"fmt"
	"regexp"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The slash commands of agent administration (src/slash/slash-commands.ts): /subagents, /subagents-models, /subagents-profiles,
// /subagents-load-profile, /subagents-refresh-provider-models, /subagents-generate-profiles, /subagents-check-profile,
// /subagents-doctor, /subagents-guide and /subagents-refine. Results are posted as text messages; the original's live slash
// result widget is a terminal view and is not ported.

const slashTextResultType = "subagent-slash-text-result"

func slashText(ctx sdk.Context, text string) {
	_ = ctx.SendMessage(slashTextResultType, text, true, sdk.SendMessageOptions{})
}

func withSlashStatus(ctx sdk.Context, text string, run func()) {
	if ctx.HasUI() {
		ctx.SetStatus("subagent-slash-text", text)
		defer ctx.SetStatus("subagent-slash-text", "")
	}
	run()
}

// singleArg is parseSingleRequiredArg: exactly one whitespace-separated word.
func singleArg(args, usage string) (string, string) {
	parts := strings.Fields(args)
	if len(parts) != 1 {
		return "", usage
	}
	return parts[0], ""
}

// runManagementCommand runs a management action as a command does and posts its text (a failure is notified).
func runManagementCommand(ctx sdk.Context, action string, params map[string]any) {
	params["action"] = action
	res, err := actions[action].run(ctx, params)
	if err != nil {
		ctx.Notify(err.Error(), "error")
		return
	}
	switch r := res.(type) {
	case explicitOK:
		slashText(ctx, r.Content)
	default:
		slashText(ctx, fmt.Sprint(res))
	}
}

func profileHostOf(ctx sdk.Context) profileHost {
	return profileHost{
		exec: func(command string, args []string, cwd string, timeoutMs int) (string, string, int, bool, error) {
			r, err := ctx.ExecWithOptions(command, args, sdk.ExecOptions{Timeout: float64(timeoutMs), Cwd: cwd})
			if err != nil {
				return "", "", 0, false, err
			}
			return r.Stdout, r.Stderr, r.ExitCode, r.Killed, nil
		},
		models: func() []map[string]any {
			ms, _ := ctx.ModelRegistry().GetAvailable()
			return ms
		},
	}
}

func agentNameCompletions(prefix string) ([]sdk.AutocompleteItem, error) {
	adv.mu.Lock()
	cwd, ok := adv.cwd, adv.hasCtx
	adv.mu.Unlock()
	if !ok || strings.Contains(prefix, " ") {
		return nil, nil
	}
	d, err := discoverAgents(cwd, ScopeBoth, "", adv.options())
	if err != nil {
		return nil, nil
	}
	var items []sdk.AutocompleteItem
	for _, a := range d.Agents {
		if strings.HasPrefix(a.Name, prefix) {
			items = append(items, sdk.AutocompleteItem{Value: a.Name, Label: a.Name})
		}
	}
	return items, nil
}

func profileNameCompletions(prefix string) ([]sdk.AutocompleteItem, error) {
	if strings.Contains(prefix, " ") {
		return nil, nil
	}
	names, err := listSubagentProfiles()
	if err != nil {
		return nil, nil
	}
	var items []sdk.AutocompleteItem
	for _, n := range names {
		if strings.HasPrefix(n, prefix) {
			items = append(items, sdk.AutocompleteItem{Value: n, Label: n})
		}
	}
	return items, nil
}

var forceSuffixRe = regexp.MustCompile(`(?:^|\s)(?:--force|force)$`)

func pluralForm(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func init() {
	extensionHooks = append(extensionHooks, registerAdminCommands)
}

func registerAdminCommands(e *sdk.Extension) {
	e.RegisterCommand("subagents", sdk.CommandOptions{
		Description: "Administer subagents: inspect metadata and update models, thinking, or prompts",
		Handler: func(ctx sdk.Context, args string) error {
			return newAdminHost(ctx).openSubagentsAdmin(args)
		},
	})
	e.RegisterCommand("subagents-doctor", sdk.CommandOptions{
		Description: "Show subagent diagnostics",
		Handler: func(ctx sdk.Context, _ string) error {
			runManagementCommand(ctx, "doctor", map[string]any{})
			return nil
		},
	})
	e.RegisterCommand("subagents-guide", sdk.CommandOptions{
		Description: "Show a packaged subagents guide topic",
		GetArgumentCompletions: func(prefix string) ([]sdk.AutocompleteItem, error) {
			if strings.Contains(prefix, " ") {
				return nil, nil
			}
			var items []sdk.AutocompleteItem
			for _, t := range guideTopics {
				if strings.HasPrefix(t, prefix) {
					items = append(items, sdk.AutocompleteItem{Value: t, Label: t})
				}
			}
			return items, nil
		},
		Handler: func(ctx sdk.Context, args string) error {
			topic := jsTrim(args)
			if strings.Contains(topic, " ") {
				ctx.Notify("Usage: /subagents-guide [topic]", "error")
				return nil
			}
			params := map[string]any{}
			if topic != "" {
				params["topic"] = topic
			}
			runManagementCommand(ctx, "guide", params)
			return nil
		},
	})
	e.RegisterCommand("subagents-refine", sdk.CommandOptions{
		Description:            "Generate a bounded project-local refinement overlay for one subagent",
		GetArgumentCompletions: agentNameCompletions,
		Handler: func(ctx sdk.Context, args string) error {
			parts := strings.Fields(args)
			if len(parts) != 1 {
				ctx.Notify("Usage: /subagents-refine <agent>", "error")
				return nil
			}
			runManagementCommand(ctx, "refine", map[string]any{"agent": parts[0]})
			return nil
		},
	})
	e.RegisterCommand("subagents-models", sdk.CommandOptions{
		Description:            "Show model mappings for discovered subagents",
		GetArgumentCompletions: agentNameCompletions,
		Handler: func(ctx sdk.Context, args string) error {
			trimmed := jsTrim(args)
			if trimmed == "" {
				runManagementCommand(ctx, "models", map[string]any{})
				return nil
			}
			parts := strings.Fields(trimmed)
			if len(parts) != 1 {
				ctx.Notify("Usage: /subagents-models [agent-name]", "error")
				return nil
			}
			runManagementCommand(ctx, "models", map[string]any{"agent": parts[0]})
			return nil
		},
	})
	e.RegisterCommand("subagents-profiles", sdk.CommandOptions{
		Description: "List saved subagent profiles",
		Handler: func(ctx sdk.Context, _ string) error {
			profiles, err := listSubagentProfiles()
			if err != nil {
				ctx.Notify(err.Error(), "error")
				return nil
			}
			if len(profiles) == 0 {
				slashText(ctx, "Subagent profiles\n\nNo subagent profiles found in ~/.pi/agent/profiles/pi-subagents/")
				return nil
			}
			slashText(ctx, "Subagent profiles\n\n"+strings.Join(profiles, "\n"))
			return nil
		},
	})
	e.RegisterCommand("subagents-load-profile", sdk.CommandOptions{
		Description:            "Load a subagent profile into ~/.pi/agent/settings.json",
		GetArgumentCompletions: profileNameCompletions,
		Handler: func(ctx sdk.Context, args string) error {
			name, usage := singleArg(args, "Usage: /subagents-load-profile <name>")
			if usage != "" {
				ctx.Notify(usage, "error")
				return nil
			}
			withSlashStatus(ctx, "Loading profile "+name+"…", func() {
				_, profile, err := readSubagentProfile(name)
				if err != nil {
					ctx.Notify(err.Error(), "error")
					return
				}
				workerModel := ""
				if o := profile.obj("subagents").obj("agentOverrides").obj("worker"); o != nil {
					if m, ok := o.str("model"); ok {
						workerModel = jsTrim(m)
					}
				}
				file, settings, err := applySubagentProfile(name)
				if err != nil {
					ctx.Notify(err.Error(), "error")
					return
				}
				lines := []string{"Loaded subagent profile: " + name, "Profile: " + file, "Updated: " + settings}
				if workerModel != "" {
					switch yes, _ := ctx.Confirm("", "Profile loaded. Also switch this session to the profile worker model?\n\n"+workerModel); {
					case yes:
						var available []ModelInfo
						for _, m := range profileHostOf(ctx).models() {
							available = append(available, modelInfoFromRegistry(m))
						}
						info := findModelInfo(workerModel, available, "")
						if info == nil || ctx.ModelRegistry().Find(info.Provider, info.ID) == nil {
							lines = append(lines, fmt.Sprintf("Could not switch current session model: '%s' is not available in the current model registry.", workerModel))
						} else if ok, _ := ctx.SetModel(info.FullID); ok {
							lines = append(lines, "Current session model switched to: "+info.FullID)
						} else {
							lines = append(lines, fmt.Sprintf("Could not switch current session model to '%s': no API key or provider access is available.", workerModel))
						}
					}
				}
				slashText(ctx, strings.Join(lines, "\n"))
			})
			return nil
		},
	})
	e.RegisterCommand("subagents-refresh-provider-models", sdk.CommandOptions{
		Description: "Refresh the cached model catalog for one provider",
		Handler: func(ctx sdk.Context, args string) error {
			trimmed := jsTrim(args)
			force := forceSuffixRe.MatchString(trimmed)
			without := jsTrim(forceSuffixRe.ReplaceAllString(trimmed, ""))
			provider, usage := singleArg(without, "Usage: /subagents-refresh-provider-models <provider> [--force]")
			if usage != "" {
				ctx.Notify(usage, "error")
				return nil
			}
			withSlashStatus(ctx, "Refreshing provider models for "+provider+"…", func() {
				r, err := profileHostOf(ctx).refreshProviderModelCatalog(provider, refreshOptions{Force: force})
				if err != nil {
					ctx.Notify(err.Error(), "error")
					return
				}
				status := "refreshed"
				if r.Reused {
					status = "fresh cache reused"
				}
				lines := []string{"Provider model catalog", "Provider: " + provider, "Status: " + status, "File: " + r.FilePath,
					fmt.Sprintf("Models: %d", len(r.Catalog.Models)), "Refreshed at: " + r.Catalog.RefreshedAt}
				if n := r.HeuristicFallbackCount; n > 0 {
					lines = append(lines, fmt.Sprintf("Warning: %d model%s classified with name heuristics fallback.", n, pluralForm(n, " was", "s were")))
				}
				slashText(ctx, strings.Join(lines, "\n"))
			})
			return nil
		},
	})
	e.RegisterCommand("subagents-generate-profiles", sdk.CommandOptions{
		Description: "Generate <provider>.quota and <provider>.quality subagent profiles",
		Handler: func(ctx sdk.Context, args string) error {
			provider, usage := singleArg(args, "Usage: /subagents-generate-profiles <provider>")
			if usage != "" {
				ctx.Notify(usage, "error")
				return nil
			}
			withSlashStatus(ctx, "Generating profiles for "+provider+"…", func() {
				r, err := profileHostOf(ctx).generateProfilesForProvider(provider, refreshOptions{})
				if err != nil {
					ctx.Notify(err.Error(), "error")
					return
				}
				lines := []string{"Generated subagent profiles", "Provider: " + provider, "Catalog: " + r.CatalogPath, "Quota: " + r.QuotaPath,
					"  cheap=" + r.QuotaModels.Cheap, "  medium=" + r.QuotaModels.Medium, "  strong=" + r.QuotaModels.Strong,
					"Quality: " + r.QualityPath, "  cheap=" + r.QualityModels.Cheap, "  medium=" + r.QualityModels.Medium, "  strong=" + r.QualityModels.Strong}
				if n := r.SelectedHeuristicFallbackCount; n > 0 {
					lines = append(lines, fmt.Sprintf("Warning: generated profiles depend on heuristic-only classification for %d selected model%s.", n, pluralForm(n, "", "s")))
				} else if n := r.HeuristicFallbackCount; n > 0 {
					lines = append(lines, fmt.Sprintf("Warning: provider catalog still contains %d heuristic-classified model%s.", n, pluralForm(n, "", "s")))
				}
				slashText(ctx, strings.Join(lines, "\n"))
			})
			return nil
		},
	})
	e.RegisterCommand("subagents-check-profile", sdk.CommandOptions{
		Description:            "Check whether a saved profile still points to usable models",
		GetArgumentCompletions: profileNameCompletions,
		Handler: func(ctx sdk.Context, args string) error {
			name, usage := singleArg(args, "Usage: /subagents-check-profile <name>")
			if usage != "" {
				ctx.Notify(usage, "error")
				return nil
			}
			withSlashStatus(ctx, "Checking profile "+name+"…", func() {
				r, err := profileHostOf(ctx).checkSubagentProfile(name)
				if err != nil {
					ctx.Notify(err.Error(), "error")
					return
				}
				lines := []string{"Subagent profile check", "Profile: " + r.ProfileName, "File: " + r.FilePath, ""}
				for _, e := range r.Results {
					registry := "missing"
					if e.InRegistry {
						registry = "ok"
					}
					line := fmt.Sprintf("%s → %s — registry %s; probe %s", e.Agent, e.Model, registry, e.Probe.Status)
					if e.Probe.Message != "" {
						first := strings.SplitN(strings.ReplaceAll(e.Probe.Message, "\r\n", "\n"), "\n", 2)[0]
						line += " (" + first + ")"
					}
					lines = append(lines, line)
				}
				slashText(ctx, strings.Join(lines, "\n"))
			})
			return nil
		},
	})
}
