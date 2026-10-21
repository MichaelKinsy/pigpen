package tintinweb_subagents

import (
	"bufio"
	"encoding/json"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// A fake pig that delegates. It is the test binary again (FAKE_PIG_RPC=nest), and what it does in place of a model is
// run a script: the steps are calls of the nested tools of THIS package, on a nested app built from the environment the
// parent's runner gave the process, exactly as the extension of a real child would be. Its own children are started by
// the real runner (startRPCChild), so a chain of these is a chain of processes across two or three levels.
//
// FAKE_PIG_SCRIPT is a JSON file: the prompt -> {steps, answer, hold}. FAKE_PIG_LOGDIR collects one <pid>.json per
// process: how it was started and what it was given.

type nestScript map[string]nestEntry

type nestEntry struct {
	Steps  []nestStep `json:"steps"`
	Answer string     `json:"answer"`
	Hold   bool       `json:"hold"` // never settles on its own
}

type nestStep struct {
	Tool     string         `json:"tool"`
	Args     map[string]any `json:"args"`
	Write    string         `json:"write"`    // write the variable `value` to this file of the log dir
	Value    string         `json:"value"`    // ... $id
	WaitFile string         `json:"waitFile"` // wait for this file of the log dir and read it into $id
	SleepMs  int            `json:"sleepMs"`
}

type nestLog struct {
	Pid          int      `json:"pid"`
	Prompt       string   `json:"prompt"`
	Args         []string `json:"args"`
	Depth        string   `json:"depth"`
	MaxDepth     string   `json:"maxDepth"`
	Allowed      string   `json:"allowed"`
	ID           string   `json:"id"`
	ConfigCwd    string   `json:"configCwd"`
	UsageFile    string   `json:"usageFile"`
	ToolsEnabled bool     `json:"toolsEnabled"`
}

var agentIDText = regexp.MustCompile(`Agent ID:\s*(\S+)`)

func fakeNestingPig() {
	e := sdk.New("fake-nesting-pig")
	a := newApp(e, startRPCChild)
	cwd, _ := os.Getwd()
	// The nested tools are registered and called the way a real child's host does: through the SDK, over a host that
	// answers every call with an empty object.
	var host *Host
	if n := nestedConfigFromEnv(); n != nil && n.toolsEnabled() {
		a.registerNested(e, n)
		host = StartHost(&testing.T{}, e, HostOptions{Cwd: cwd})
		host.FireAll("session_start", obj{"reason": "startup"})
	} else {
		a.reload(cwd)
	}
	logDir := os.Getenv("FAKE_PIG_LOGDIR")
	var script nestScript
	if b, err := os.ReadFile(os.Getenv("FAKE_PIG_SCRIPT")); err == nil {
		_ = json.Unmarshal(b, &script)
	}
	shutdown := func() {
		if a.nested != nil {
			a.nestedShutdown()
		}
	}
	// pig runs the extensions' session_shutdown on SIGTERM and when its input ends, then exits.
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	go func() {
		<-term
		shutdown()
		os.Exit(143)
	}()

	enc := lockedEncoder{enc: json.NewEncoder(os.Stdout), mu: new(sync.Mutex)}
	var answer atomicString
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var cmd map[string]any
		_ = json.Unmarshal(sc.Bytes(), &cmd)
		resp := func(data any) {
			enc.Encode(map[string]any{"type": "response", "id": cmd["id"], "command": cmd["type"], "success": true, "data": data})
		}
		switch cmd["type"] {
		case "prompt":
			prompt, _ := cmd["message"].(string)
			resp(nil)
			l := nestLog{Pid: os.Getpid(), Prompt: prompt, Args: os.Args[1:], Depth: os.Getenv(depthEnv), MaxDepth: os.Getenv(maxDepthEnv), Allowed: os.Getenv(allowedEnv),
				ID: os.Getenv(agentIDEnv), ConfigCwd: os.Getenv(configCwdEnv), UsageFile: os.Getenv(usageEnv), ToolsEnabled: a.nested != nil}
			if b, err := json.Marshal(l); err == nil && logDir != "" {
				_ = os.WriteFile(filepath.Join(logDir, strconv.Itoa(os.Getpid())+".json"), b, 0o644)
			}
			go func() {
				entry := script[prompt]
				results := runNestSteps(host, logDir, entry.Steps)
				answer.set(entry.Answer + strings.Join(results, "\n"))
				if entry.Hold {
					return
				}
				// What the model spent: 42 tokens of input, as pig's stream reports it.
				enc.Encode(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant",
					"content": []any{map[string]any{"type": "text", "text": answer.get()}}, "usage": map[string]any{"input": 42, "output": 0}}})
				if a.nested != nil {
					a.nestedSettled() // pig's agent_settled reaches the extension
				}
				enc.Encode(map[string]any{"type": "agent_settled"})
			}()
		case "set_session_name":
			resp(nil)
		case "get_state":
			resp(map[string]any{"sessionFile": ""})
		case "new_session":
			resp(map[string]any{"cancelled": false})
		case "get_messages":
			resp(map[string]any{"messages": []any{}})
		case "get_last_assistant_text":
			resp(map[string]any{"text": answer.get()})
		case "get_session_stats":
			resp(map[string]any{"tokens": map[string]any{"total": 42}, "toolCalls": 1})
		case "steer":
			resp(nil)
		case "abort":
			resp(nil)
		}
	}
	shutdown()
}

func runNestSteps(host *Host, logDir string, steps []nestStep) []string {
	vars := map[string]string{}
	sub := func(s string) string { return strings.ReplaceAll(s, "$id", vars["id"]) }
	var results []string
	for _, st := range steps {
		switch {
		case st.Tool != "":
			args := map[string]any{}
			for k, v := range st.Args {
				if s, ok := v.(string); ok {
					v = sub(s)
				}
				args[k] = v
			}
			if host == nil {
				results = append(results, "NO NESTED TOOLS")
				continue
			}
			raw, failure := host.Tool(st.Tool, args)
			text, _ := toolJSON(raw)
			if failure != "" {
				text = "ERROR " + failure
			}
			if m := agentIDText.FindStringSubmatch(text); m != nil {
				vars["id"] = m[1]
			}
			results = append(results, text)
		case st.Write != "":
			_ = os.WriteFile(filepath.Join(logDir, st.Write), []byte(sub(st.Value)), 0o644)
		case st.WaitFile != "":
			for i := 0; i < 1000; i++ {
				if b, err := os.ReadFile(filepath.Join(logDir, st.WaitFile)); err == nil && len(b) > 0 {
					vars["id"] = string(b)
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
		case st.SleepMs > 0:
			time.Sleep(time.Duration(st.SleepMs) * time.Millisecond)
		}
	}
	return results
}

type atomicString struct {
	mu sync.Mutex
	s  string
}

func (a *atomicString) set(s string) { a.mu.Lock(); a.s = s; a.mu.Unlock() }
func (a *atomicString) get() string  { a.mu.Lock(); defer a.mu.Unlock(); return a.s }
