package plugins

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/clihook"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/cli"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
)

func init() {
	cli.RegisterSubcommand(
		"plugins",
		"Manage Hermes plugins (list, info <id>, enable <id>, disable <id>)",
		runPluginsSubcommand,
	)
}

func envOut(env clihook.Env) io.Writer {
	if env.Stdout != nil {
		return env.Stdout
	}
	return io.Discard
}

func envErr(env clihook.Env) io.Writer {
	if env.Stderr != nil {
		return env.Stderr
	}
	return io.Discard
}

func registerSlashCommand(ext *sdk.Extension, mgr *Manager) {
	ext.RegisterCommand("plugins", sdk.CommandOptions{
		Description: "List, inspect, enable, or disable Hermes plugins (/plugins [list|info <id>|enable <id>|disable <id>])",
		Handler: func(ctx sdk.Context, args string) error {
			p := mgr.paths
			if p.Home == "" {
				p = paths.ResolveProfilePaths("")
			}
			cwd := safeCwd(ctx)
			out, err := executePluginCommand(p, cwd, mgr, strings.Fields(args))
			if err != nil {
				showPluginMessage(ctx, fmt.Sprintf("plugins: %v", err))
				return nil
			}
			showPluginMessage(ctx, out)
			return nil
		},
	})
}

func runPluginsSubcommand(env clihook.Env, args []string) int {
	p := paths.ResolveProfilePaths("")
	cwd, _ := os.Getwd()
	out, err := executePluginCommand(p, cwd, nil, args)
	if err != nil {
		fmt.Fprintf(envErr(env), "plugins: %v\n", err)
		return 1
	}
	fmt.Fprintln(envOut(env), out)
	return 0
}

func executePluginCommand(p paths.ProfilePaths, cwd string, mgr *Manager, args []string) (string, error) {
	sub := "list"
	var rest []string
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
		rest = args[1:]
	}

	switch sub {
	case "list", "ls":
		asJSON := false
		for _, a := range rest {
			if a == "--json" {
				asJSON = true
			}
		}
		infos, err := ListPlugins(p, cwd)
		if err != nil {
			return "", err
		}
		infos = mergeRuntimeInfos(infos, mgr)
		if asJSON {
			b, err := json.MarshalIndent(infos, "", "  ")
			if err != nil {
				return "", err
			}
			return string(b), nil
		}
		return formatPluginList(infos), nil

	case "info", "show":
		if len(rest) == 0 {
			return "", fmt.Errorf("usage: plugins info <id>")
		}
		info, err := InspectPlugin(p, cwd, rest[0])
		if err != nil {
			return "", err
		}
		if mgr != nil {
			for _, live := range mgr.LoadedInfos() {
				if live.Manifest.CanonicalKey() == info.Manifest.CanonicalKey() {
					info.Loaded = live.Loaded
					info.LoadError = live.LoadError
					info.RegisteredTools = live.RegisteredTools
					info.RegisteredHooks = live.RegisteredHooks
					info.RegisteredCmds = live.RegisteredCmds
					info.RegisteredPrompt = live.RegisteredPrompt
					break
				}
			}
		}
		return formatPluginInfo(*info), nil

	case "enable":
		if len(rest) == 0 {
			return "", fmt.Errorf("usage: plugins enable <id>")
		}
		info, err := EnablePlugin(p, cwd, rest[0])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Enabled plugin %q (saved to %s). Restart session to activate.", info.Manifest.CanonicalKey(), p.ConfigFile), nil

	case "disable":
		if len(rest) == 0 {
			return "", fmt.Errorf("usage: plugins disable <id>")
		}
		info, err := DisablePlugin(p, cwd, rest[0])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Disabled plugin %q (saved to %s). Restart session to take effect.", info.Manifest.CanonicalKey(), p.ConfigFile), nil

	default:
		return "", fmt.Errorf("unknown subcommand %q (expected: list, info <id>, enable <id>, disable <id>)", sub)
	}
}

func mergeRuntimeInfos(infos []PluginInfo, mgr *Manager) []PluginInfo {
	if mgr == nil {
		return infos
	}
	liveMap := make(map[string]PluginInfo)
	for _, live := range mgr.LoadedInfos() {
		liveMap[live.Manifest.CanonicalKey()] = live
	}
	for i := range infos {
		if live, ok := liveMap[infos[i].Manifest.CanonicalKey()]; ok {
			infos[i].Loaded = live.Loaded
			infos[i].LoadError = live.LoadError
			infos[i].RegisteredTools = live.RegisteredTools
			infos[i].RegisteredHooks = live.RegisteredHooks
			infos[i].RegisteredCmds = live.RegisteredCmds
			infos[i].RegisteredPrompt = live.RegisteredPrompt
		}
	}
	return infos
}

func formatPluginList(infos []PluginInfo) string {
	if len(infos) == 0 {
		return "No Hermes plugins discovered."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Hermes Plugins (%d):\n", len(infos))
	for _, info := range infos {
		status := "disabled"
		if info.LoadError != "" {
			status = "error"
		} else if info.Loaded {
			status = "loaded"
		} else if info.Gate.Enabled {
			status = "enabled"
		}
		ver := info.Manifest.Version
		if ver != "" {
			ver = " v" + ver
		}
		desc := info.Manifest.Description
		if desc != "" {
			desc = " — " + desc
		}
		fmt.Fprintf(&b, "  %-24s [%-8s] (%s, %s)%s%s\n",
			info.Manifest.CanonicalKey(),
			status,
			info.Manifest.Source,
			info.Manifest.Kind,
			ver,
			desc,
		)
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatPluginInfo(info PluginInfo) string {
	m := info.Manifest
	var b strings.Builder
	fmt.Fprintf(&b, "Plugin: %s\n", m.CanonicalKey())
	if m.Name != "" && m.Name != m.CanonicalKey() {
		fmt.Fprintf(&b, "Name: %s\n", m.Name)
	}
	if m.Version != "" {
		fmt.Fprintf(&b, "Version: %s\n", m.Version)
	}
	if m.Description != "" {
		fmt.Fprintf(&b, "Description: %s\n", m.Description)
	}
	fmt.Fprintf(&b, "Kind: %s\n", m.Kind)
	fmt.Fprintf(&b, "Source: %s (%s)\n", m.Source, m.Dir)
	if m.Portable {
		fmt.Fprintf(&b, "Format: portable plugin.json\n")
	}
	status := "disabled"
	if info.LoadError != "" {
		status = "error: " + info.LoadError
	} else if info.Loaded {
		status = "loaded"
	} else if info.Gate.Enabled {
		status = "enabled"
	} else if info.Gate.Reason != "" {
		status = "disabled (" + info.Gate.Reason + ")"
	}
	fmt.Fprintf(&b, "Status: %s\n", status)
	if len(m.Tools) > 0 {
		fmt.Fprintf(&b, "Declared Tools: %s\n", strings.Join(m.Tools, ", "))
	}
	if len(info.RegisteredTools) > 0 {
		fmt.Fprintf(&b, "Registered Tools: %s\n", strings.Join(info.RegisteredTools, ", "))
	}
	if len(m.Hooks) > 0 {
		fmt.Fprintf(&b, "Declared Hooks: %s\n", strings.Join(m.Hooks, ", "))
	}
	if len(info.RegisteredHooks) > 0 {
		fmt.Fprintf(&b, "Registered Hooks: %s\n", strings.Join(info.RegisteredHooks, ", "))
	}
	if len(info.RegisteredCmds) > 0 {
		fmt.Fprintf(&b, "Registered Commands: %s\n", strings.Join(info.RegisteredCmds, ", "))
	}
	if len(info.RegisteredPrompt) > 0 {
		fmt.Fprintf(&b, "Prompt Sections: %s\n", strings.Join(info.RegisteredPrompt, ", "))
	}
	if len(m.MCPServers) > 0 {
		names := make([]string, 0, len(m.MCPServers))
		for k := range m.MCPServers {
			names = append(names, k)
		}
		fmt.Fprintf(&b, "MCP Servers: %s\n", strings.Join(names, ", "))
	}
	if len(m.Skills) > 0 {
		names := make([]string, 0, len(m.Skills))
		for _, sk := range m.Skills {
			names = append(names, sk.Name)
		}
		fmt.Fprintf(&b, "Skills: %s\n", strings.Join(names, ", "))
	}
	return strings.TrimRight(b.String(), "\n")
}
