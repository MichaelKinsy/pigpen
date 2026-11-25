package shell

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// upstream: packages/coding-agent/src/exec/non-interactive-env.ts

var nonInteractiveEnv = map[string]string{
	// Disable pagers so commands don't block on interactive views.
	"PAGER":          "cat",
	"GIT_PAGER":      "cat",
	"MANPAGER":       "cat",
	"SYSTEMD_PAGER":  "cat",
	"BAT_PAGER":      "cat",
	"DELTA_PAGER":    "cat",
	"GH_PAGER":       "cat",
	"GLAB_PAGER":     "cat",
	"PSQL_PAGER":     "cat",
	"MYSQL_PAGER":    "cat",
	"AWS_PAGER":      "",
	"HOMEBREW_PAGER": "cat",
	"LESS":           "FRX",
	// Disable terminal features that can block the process.
	"TERM":             "dumb",
	"NO_COLOR":         "1",
	"PYTHONUNBUFFERED": "1",
	// Disable editor and terminal credential prompts.
	"GIT_EDITOR":          "true",
	"VISUAL":              "true",
	"EDITOR":              "true",
	"GIT_TERMINAL_PROMPT": "0",
	"GIT_SEQUENCE_EDITOR": "true",
	"GCM_INTERACTIVE":     "never",
	"CI":                  "true",
	"AGENT":               "1",
	// Package manager defaults for unattended execution.
	"npm_config_yes":                 "true",
	"npm_config_update_notifier":     "false",
	"npm_config_fund":                "false",
	"npm_config_audit":               "false",
	"npm_config_progress":            "false",
	"PNPM_DISABLE_SELF_UPDATE_CHECK": "true",
	"PNPM_UPDATE_NOTIFIER":           "false",
	"YARN_ENABLE_TELEMETRY":          "0",
	"YARN_ENABLE_PROGRESS_BARS":      "0",
	// Cross-language/tooling non-interactive defaults.
	"CARGO_TERM_PROGRESS_WHEN":      "never",
	"DEBIAN_FRONTEND":               "noninteractive",
	"PIP_NO_INPUT":                  "1",
	"PIP_DISABLE_PIP_VERSION_CHECK": "1",
	"TF_INPUT":                      "0",
	"TF_IN_AUTOMATION":              "1",
	"GH_PROMPT_DISABLED":            "1",
	"COMPOSER_NO_INTERACTION":       "1",
	"CLOUDSDK_CORE_DISABLE_PROMPTS": "1",
}

var agentLocaleEnv = map[string]string{
	"LANG":   "C.UTF-8",
	"LC_ALL": "C.UTF-8",
}

var darwinAgentLocaleEnv = map[string]string{
	"LANG":        "C",
	"LC_ALL":      "",
	"LC_COLLATE":  "C",
	"LC_CTYPE":    "en_US.UTF-8",
	"LC_MESSAGES": "C",
	"LC_MONETARY": "C",
	"LC_NUMERIC":  "C",
	"LC_TIME":     "C",
}

var windowsPythonUTF8Env = map[string]string{
	"PYTHONIOENCODING": "utf-8",
	"PYTHONUTF8":       "1",
}

func rejectPromptCommand() string {
	if p, err := exec.LookPath("false"); err == nil && p != "" {
		return p
	}
	return "false"
}

// buildCommandEnv builds the child environment for a shell run:
//   - For non-PTY runs, overlays omp's NON_INTERACTIVE_ENV, pinned locale, and
//     Windows Python UTF-8 defaults (honouring PI_BASH_NO_CI / CLAUDE_BASH_NO_CI).
//   - For PTY services, preserves interactive terminal env (setting TERM=xterm-256color
//     if unset).
//   - Always overlays PI_SESSION_ID, PI_SESSION_FILE, PI_TOOL_CALL_ID last.
//
// upstream: packages/coding-agent/src/exec/non-interactive-env.ts:145, tools/bash.ts:1119
func buildCommandEnv(sessionID, sessionFile, toolCallID string, forPTY bool) []string {
	base := os.Environ()
	envMap := make(map[string]string, len(base)+len(nonInteractiveEnv)+16)
	order := make([]string, 0, len(base)+len(nonInteractiveEnv)+16)
	set := func(k, v string) {
		if _, exists := envMap[k]; !exists {
			order = append(order, k)
		}
		envMap[k] = v
	}
	for _, kv := range base {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		set(k, v)
	}

	if !forPTY {
		noCI := envMap["PI_BASH_NO_CI"] != "" || envMap["CLAUDE_BASH_NO_CI"] != ""
		for k, v := range nonInteractiveEnv {
			if k == "CI" && noCI {
				continue
			}
			set(k, v)
		}
		set("SSH_ASKPASS", rejectPromptCommand())

		if runtime.GOOS == "darwin" {
			for k, v := range darwinAgentLocaleEnv {
				set(k, v)
			}
		} else {
			for k, v := range agentLocaleEnv {
				set(k, v)
			}
		}
		if runtime.GOOS == "windows" {
			hasPyUTF8 := false
			for k := range windowsPythonUTF8Env {
				if _, ok := envMap[k]; ok {
					hasPyUTF8 = true
					break
				}
			}
			if !hasPyUTF8 {
				for k, v := range windowsPythonUTF8Env {
					set(k, v)
				}
			}
		}
	} else {
		if envMap["TERM"] == "" || envMap["TERM"] == "dumb" {
			set("TERM", "xterm-256color")
		}
	}

	// Session identity always wins over inherited values (even when empty).
	set("PI_SESSION_ID", sessionID)
	set("PI_SESSION_FILE", sessionFile)
	set("PI_TOOL_CALL_ID", toolCallID)

	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, k+"="+envMap[k])
	}
	return out
}
