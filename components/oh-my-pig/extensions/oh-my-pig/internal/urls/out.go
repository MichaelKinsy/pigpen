package urls

import (
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// upstream: packages/coding-agent/src/internal-urls/out-protocol.ts

//go:embed prompts/out.md
var outPromptDoc string

const (
	// MaxOutReplyBytes is the largest Markdown reply out:// accepts (64 KiB).
	MaxOutReplyBytes = 64 * 1024

	outDisabledMsg = "out:// is off in this session (`out.enabled`; by default on only in the interactive TUI); reply in your final message instead."
	outSubagentMsg = "Subagents cannot reply to the user through out://; report through your result instead."
)

// OutPolicy configures whether out:// replies are allowed for a given Context.
type OutPolicy struct {
	Enabled  bool
	Subagent bool
}

var (
	outPolicyMu sync.RWMutex
	outPolicyFn func(c Context) OutPolicy
)

// SetOutPolicyProvider installs a hook that decides whether out:// is enabled
// or running in a subagent for a given Context. When nil, out:// is closed by
// default so subagents and non-TUI sessions cannot write replies.
func SetOutPolicyProvider(fn func(c Context) OutPolicy) {
	outPolicyMu.Lock()
	outPolicyFn = fn
	outPolicyMu.Unlock()
}

// ResetOutPolicyForTests clears any custom out:// policy provider.
func ResetOutPolicyForTests() {
	outPolicyMu.Lock()
	outPolicyFn = nil
	outPolicyMu.Unlock()
}

// OutHandler serves `out://`: mid-turn Markdown replies shown to the user without
// ending the agent's turn. The transcript frontend draws `reply`-scoped writes
// from the tool call's content; the handler validates and returns "Shown to the user.".
// upstream: out-protocol.ts OutProtocolHandler.
type OutHandler struct{}

func (OutHandler) Scheme() string { return "out" }

func (OutHandler) Spec() Spec {
	return Spec{
		Backing:   BackingDevice,
		Selectors: SelectorsNone,
		Immutable: true,
		Write: &WritePolicy{
			Via:      "handler",
			Payload:  "verbatim",
			Scope:    "reply",
			MaxBytes: MaxOutReplyBytes,
		},
	}
}

func (OutHandler) PromptDoc(host SchemeHost) string {
	if !host.OutReplies {
		return ""
	}
	return strings.TrimSpace(outPromptDoc)
}

func (OutHandler) Resolve(_ Context, _ URL) (*Resource, error) {
	return nil, errors.New("out:// is write-only; reading it is not allowed. Write Markdown to it to show the user a reply.")
}

func (OutHandler) Write(c Context, _ URL, content string) (*WriteResult, error) {
	outPolicyMu.RLock()
	policyFn := outPolicyFn
	outPolicyMu.RUnlock()
	if policyFn == nil {
		return nil, fmt.Errorf("Tool \"write\" is blocked by tool policy.\nReason: %s", outDisabledMsg)
	}
	policy := policyFn(c)
	if policy.Subagent {
		return nil, fmt.Errorf("Tool \"write\" is blocked by tool policy.\nReason: %s", outSubagentMsg)
	}
	if !policy.Enabled {
		return nil, fmt.Errorf("Tool \"write\" is blocked by tool policy.\nReason: %s", outDisabledMsg)
	}

	if strings.TrimSpace(content) == "" {
		return nil, errors.New("out:// needs non-empty Markdown content.")
	}
	if len(content) > MaxOutReplyBytes {
		return nil, fmt.Errorf(
			"out:// replies are capped at %d KiB (got %d bytes); shorten it or split it up.",
			MaxOutReplyBytes/1024, len(content),
		)
	}
	// upstream: out-protocol.ts:92-103 — validate and return "Shown to the user.";
	// the transcript draws reply-scoped writes from the tool call's content without
	// injecting a custom message back into the conversation.
	return &WriteResult{
		Text:    "Shown to the user.",
		Useless: true,
	}, nil
}
