// Package rea is the runtime member of the REA harness. It unpacks the REA payload embedded in the Binary (the published
// rea-agents package with its production dependencies, on a pinned Node) into a content-addressed directory under the agent
// directory, and registers REA's MCP server as `rea setup --client pi` does, with the embedded Node instead of the one on
// PATH. It changes nothing about REA: no environment, exposure or description overrides.
package rea

import (
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// serverName is the MCP server name `rea setup` writes under mcpServers.
const serverName = "rea"

const usage = "Usage: /rea-runtime [status|verify|gc]"

// member is what the factory learned: the payload, where it is unpacked, and the error that kept REA from registering.
type member struct {
	src      payloadSource
	info     payloadInfo
	root     string
	layout   layout
	unpacked time.Duration // how long this start spent unpacking; zero when the payload was already there
	err      error
}

func Extension() *sdk.Extension {
	ext := sdk.New("rea")
	m := prepare(embeddedPayload(), sdk.AgentDir)
	if m.err == nil {
		// Before Run the registration is queued and applied when the member loads, so the first prompt already has REA.
		m.err = ext.RegisterMcpServer(serverName, m.serverConfig())
	}
	ext.OnSessionStart(m.sessionStart)
	ext.Command("rea-runtime", "REA payload: status, verify the unpacked copy against the Binary, or gc old versions.", m.command)
	return ext
}

// prepare reads the payload description and makes sure the payload is unpacked. A first start of a new REA version unpacks
// it (a few seconds, once); later starts only read the sentinel.
func prepare(src payloadSource, agentDir func() (string, error)) *member {
	m := &member{src: src}
	info, err := src.info()
	if err != nil {
		m.err = err
		return m
	}
	m.info = info
	dir, err := agentDir()
	if err != nil {
		m.err = fmt.Errorf("REA needs PiG's agent directory to unpack into: %w", err)
		return m
	}
	m.root = filepath.Join(dir, "rea")
	started := time.Now()
	l, unpacked, err := ensureUnpacked(m.root, src, info)
	if err != nil {
		m.err = fmt.Errorf("unpack REA %s into %s: %w", info.Rea.Version, m.root, err)
		return m
	}
	m.layout = l
	if unpacked {
		m.unpacked = time.Since(started)
	}
	return m
}

// serverConfig is the registration `rea setup --client pi` writes ({"type":"stdio","command":<rea.mjs>,"args":["mcp"]}), with
// the embedded Node running rea.mjs instead of the shebang's `node` from PATH.
func (m *member) serverConfig() sdk.McpServerConfig {
	return sdk.McpServerConfig{Type: "stdio", Command: m.layout.node, Args: []string{m.layout.entry, "mcp"}}
}

func (m *member) version() string {
	v := "REA " + m.info.Rea.Version
	if m.info.Rea.Dev != "" {
		v += " dev " + m.info.Rea.Dev[:min(12, len(m.info.Rea.Dev))]
	}
	return v
}

func (m *member) sessionStart(ctx sdk.Context, _ map[string]any) (any, error) {
	if m.err != nil {
		ctx.Notify("REA is unavailable: "+m.err.Error(), "error")
		if ctx.HasUI() {
			ctx.SetStatus("rea", "REA unavailable")
		}
		return nil, nil
	}
	if reason := mcpOff(ctx); reason != "" {
		ctx.Notify(reason, "warning")
		if ctx.HasUI() {
			ctx.SetStatus("rea", m.version()+" · MCP off")
		}
		return nil, nil
	}
	if ctx.HasUI() {
		ctx.SetStatus("rea", m.version())
	}
	if m.unpacked > 0 {
		ctx.Notify(fmt.Sprintf("Unpacked %s (Node %s, %d files) into %s in %.1f s. Later starts reuse it.", m.version(), m.info.Node.Version, m.info.Files, m.layout.dir, m.unpacked.Seconds()), "info")
		m.unpacked = 0
	}
	return nil, nil
}

// mcpOff explains why REA's registered server cannot connect in this run, or returns "" when PiG's MCP support is loaded. The
// built-in MCP extension registers /mcp; without it nothing connects extension-registered servers. The switches that turn
// it off are the user's, so the member names them and does not work around them.
func mcpOff(ctx sdk.Context) string {
	commands, err := ctx.GetCommands()
	if err != nil {
		return ""
	}
	if slices.ContainsFunc(commands, func(c sdk.CommandInfo) bool { return c.Name == "mcp" && c.Source == "extension" }) {
		return ""
	}
	return "REA's MCP server is registered, but PiG's MCP support is not loaded in this run, so REA's tools are unavailable. " +
		"It is off with --no-mcp, with -ne unless you add -e builtin:mcp -e builtin:codemode -e builtin:tool-search, " +
		`or with "-builtin:mcp" under extensions in settings.json.`
}

func (m *member) command(ctx sdk.Context, args string) error {
	message, err := m.run(strings.TrimSpace(args), registered(ctx))
	if err != nil {
		ctx.Notify(err.Error(), "error")
		return nil
	}
	ctx.Notify(message, "info")
	return nil
}

// registered reports whether the host lists the rea server this member registered.
func registered(ctx sdk.Context) bool {
	servers, err := ctx.GetMcpServers()
	return err == nil && slices.ContainsFunc(servers, func(s sdk.RegisteredMcpServer) bool { return s.Name == serverName })
}

func (m *member) run(action string, isRegistered bool) (string, error) {
	switch action {
	case "", "status":
		if m.err != nil {
			return "", fmt.Errorf("REA is unavailable: %w", m.err)
		}
		state := "registered as MCP server \"rea\""
		if !isRegistered {
			state = "not registered with the host"
		}
		source := m.info.Rea.Package + "@" + m.info.Rea.Version
		if m.info.Rea.Commit != "" {
			source += " (" + m.info.Rea.Tag + ", " + m.info.Rea.Commit[:12] + ")"
		}
		return fmt.Sprintf("%s on Node %s, %s.\nPackage: %s\nUnpacked: %s (%d files)\nPayload: %s/%s, sha256 %s",
			m.version(), m.info.Node.Version, state, source, m.layout.dir, m.info.Files, runtime.GOOS, runtime.GOARCH, m.info.ArchiveSha256), nil
	case "verify":
		if m.err != nil {
			return "", fmt.Errorf("REA verify: %w", m.err)
		}
		started := time.Now()
		n, err := verify(m.src, m.info, m.layout.dir)
		if err != nil {
			return "", fmt.Errorf("REA verify failed: %w", err)
		}
		return fmt.Sprintf("REA verify: %d files in %s match the payload embedded in this Binary (archive sha256 %s, tree %s), %.1f s.",
			n, m.layout.dir, m.info.ArchiveSha256[:16], m.info.TreeSha256[:16], time.Since(started).Seconds()), nil
	case "gc":
		if m.root == "" {
			return "", fmt.Errorf("REA gc: %w", m.err)
		}
		removed, err := gc(m.root, dirName(m.info), time.Now())
		if err != nil {
			return "", fmt.Errorf("REA gc removed %d, then failed: %w", len(removed), err)
		}
		if len(removed) == 0 {
			return "REA gc: nothing to remove; " + m.root + " holds only this Binary's payload.", nil
		}
		return fmt.Sprintf("REA gc: removed %s from %s. A running older REA harness re-unpacks its own payload on its next start.", strings.Join(removed, ", "), m.root), nil
	}
	return "", fmt.Errorf("%s", usage)
}
