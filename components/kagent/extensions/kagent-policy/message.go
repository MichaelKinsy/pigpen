package kagentpolicy

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Confirm dialog titles. The harness adapter keys on `method:"confirm"` and one of these titles.
const (
	TitleApproval = "kagent-approval"
	TitleAskUser  = "kagent-ask-user"
)

// Message kinds and the version of the confirm message JSON.
const (
	messageVersion = 1
	kindApproval   = "approval"
	kindAskUser    = "ask_user"
)

// RejectedReason is what the model reads when a gated call is not approved (kagent's human-in-the-loop wording).
const RejectedReason = "Tool call was rejected by user."

// DeclinedText is the error result of ask_user when the dialog is not answered.
const DeclinedText = "The user declined to answer."

// message is the JSON carried in the `message` of the confirm request.
type message struct {
	V          int    `json:"v"`
	Kind       string `json:"kind"`
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName,omitempty"`
	Input      any    `json:"input"`
}

// encodeMessage writes the message as compact JSON in a fixed member order, without HTML escaping (the text is a
// tool call the adapter parses, not markup).
func encodeMessage(m message) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// approved reports whether a confirm answer is an approval. PiG v0.7.0 returns the JSON value the client sent in
// `confirmed`, so only the boolean true approves: a string, a number, null and an object are not approval.
func approved(answer any) bool {
	b, ok := answer.(bool)
	return ok && b
}
