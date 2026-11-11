package readashboard

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// serverName is the MCP server the runtime member registers.
const serverName = "rea"

// reaServer is what the dashboard learned about the registered REA server from GetMcpServers: where its Node
// and rea.mjs are, so stateless CLI one-shots can use them. The dashboard never changes the registration.
type reaServer struct {
	Present  bool
	Disabled bool
	Node     string // the server command: the embedded node
	Entry    string // the first argument: rea.mjs
	Dir      string // the payload directory: the parent of node/ in the command, "" when the command is not in one
	Payload  payloadInfo
	Err      string // why the payload description is unavailable
}

// payloadInfo is <dir>/payload.json, written by the runtime member's unpack.
type payloadInfo struct {
	Target string `json:"target"`
	Rea    struct {
		Package string `json:"package"`
		Version string `json:"version"`
		Tag     string `json:"tag"`
		Commit  string `json:"commit"`
		Dev     string `json:"dev"` // the REA commit of a payload packed from a local checkout (REA_SOURCE); "" for a release
	} `json:"rea"`
	Node struct {
		Version string `json:"version"`
	} `json:"node"`
	Entry      string `json:"entry"`
	TreeSha256 string `json:"treeSha256"`
	Files      int    `json:"files"`
	Bytes      int64  `json:"bytes"`
}

// identity distinguishes registrations, so the doctor runs again only when the server changed.
func (s reaServer) identity() string {
	if !s.Present {
		return ""
	}
	return s.Node + "\x00" + s.Entry
}

// runnable reports whether CLI one-shots can run.
func (s reaServer) runnable() bool { return s.Present && !s.Disabled && s.Node != "" && s.Entry != "" }

// version is the REA version from the payload, "" when unknown.
func (s reaServer) version() string { return s.Payload.Rea.Version }

// findREA reads the rea server out of the host's registered servers.
func findREA(servers []sdk.RegisteredMcpServer) reaServer {
	for _, server := range servers {
		if server.Name != serverName {
			continue
		}
		cfg := server.Config
		s := reaServer{Present: true, Node: cfg.Command}
		if cfg.Enabled != nil && !*cfg.Enabled {
			s.Disabled = true
		}
		if len(cfg.Args) > 0 {
			s.Entry = cfg.Args[0]
		}
		s.Dir = payloadDir(cfg.Command)
		if s.Dir == "" {
			s.Err = "the server command is not inside an unpacked payload"
			return s
		}
		raw, err := os.ReadFile(filepath.Join(s.Dir, "payload.json"))
		if err != nil {
			s.Err = err.Error()
			return s
		}
		if err := json.Unmarshal(raw, &s.Payload); err != nil {
			s.Err = "payload.json: " + err.Error()
		}
		return s
	}
	return reaServer{}
}

// payloadDir is the parent of the node/ directory the command lives under, or "".
func payloadDir(command string) string {
	if command == "" {
		return ""
	}
	dir := filepath.Dir(command)
	for range 4 {
		if filepath.Base(dir) == "node" {
			return filepath.Dir(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}

// localPath reports whether value is an absolute local path.
func localPath(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.Contains(value, "://") {
		return false
	}
	return filepath.IsAbs(value) || len(value) > 2 && value[1] == ':' && (value[2] == '\\' || value[2] == '/')
}

var nameRuns = regexp.MustCompile(`[^a-z0-9]+`)

// sanitizePigletName makes name a piglet name: lowercase letters, digits and single dashes, none at the ends.
// An empty result is "target".
func sanitizePigletName(name string) string {
	name = strings.Trim(nameRuns.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if name == "" {
		return "target"
	}
	return name
}

// targetBase is the name of the thing a target points at: the file name without its extension, or the host of
// a URL.
func targetBase(target string) string {
	if u, err := url.Parse(target); err == nil && u.Host != "" && strings.Contains(target, "://") {
		return u.Hostname()
	}
	base := filepath.Base(strings.ReplaceAll(target, "\\", "/"))
	if ext := filepath.Ext(base); ext != "" && ext != base {
		base = strings.TrimSuffix(base, ext)
	}
	return base
}

// defaultPigletName is pig-<target basename>-like.
func defaultPigletName(target string) string {
	return "pig-" + sanitizePigletName(targetBase(target)) + "-like"
}

// investigateRequest is the message the Investigate action sends.
func investigateRequest(target, question string) string {
	return fmt.Sprintf("Investigate this target with the REA tools (MCP server \"rea\"): %s\n\nQuestion: %s\n\n"+
		"Use REA's tools to find out, and report what the evidence shows and what its limitations are.", target, strings.TrimSpace(question))
}

// deriveRequest is the message the Derive piglet action sends.
func deriveRequest(name, target string) string {
	return fmt.Sprintf("Use the pigpen-derive-a-harness skill to derive a piglet named %s that works like %s. "+
		"Keep REA output under evidence/, write the behavior spec under spec/ with each claim linked to an evidence_id, "+
		"author piglets/%s/ from spec/ only, and end the iteration with pig piglet validate and pig piglet show.",
		name, target, name)
}

// envSet reports whether name is set in this process's environment, which the registered server inherits.
func envSet(name string) bool {
	_, ok := os.LookupEnv(name)
	return ok
}
