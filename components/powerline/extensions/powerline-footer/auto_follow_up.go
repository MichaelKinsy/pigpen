package powerline_footer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
	"unicode/utf16"
)

// The Jev auto follow-up classifier. upstream: auto-follow-up.ts.

const (
	// jevDefaultEndpoint is JEV_ENDPOINT (auto-follow-up.ts:1).
	jevDefaultEndpoint = "https://api.typesafe.ai/v1/systemone"
	// jevTimeout is JEV_TIMEOUT_MS: warm calls take ~100-400ms; the margin covers a cold connection (auto-follow-up.ts:3).
	jevTimeout = 1500 * time.Millisecond
	// minFollowUpConfidence is MIN_FOLLOW_UP_CONFIDENCE: a wrong follow-up lets the agent keep working without a correction, so
	// only clear calls leave the steer path (auto-follow-up.ts:5).
	minFollowUpConfidence = 0.8
)

// jevEndpoint is the endpoint the extension calls. Production never changes it; the tests point it at a local server (the
// original hardcodes the URL, so no scenario server can stand in for it).
var jevEndpoint = jevDefaultEndpoint

// followUpRouteState is upstream's FollowUpRouteState.
type followUpRouteState struct {
	CurrentTask     string
	AgentLatestText string
	NewMessage      string
}

// jevClient asks Jev whether a message can wait. client nil means http.DefaultClient.
type jevClient struct {
	endpoint string
	timeout  time.Duration
	client   *http.Client
}

func newJevClient() jevClient { return jevClient{endpoint: jevEndpoint, timeout: jevTimeout} }

// jevRequestBody is the request's JSON in the member order of auto-follow-up.ts:18-33.
type jevRequestBody struct {
	Model     string       `json:"model"`
	State     jevState     `json:"state"`
	Questions jevQuestions `json:"questions"`
}

type jevState struct {
	CurrentTask     string `json:"current_task"`
	AgentLatestText string `json:"agent_latest_text"`
	NewMessage      string `json:"new_message"`
}

type jevQuestions struct {
	Route jevRouteQuestion `json:"route"`
}

type jevRouteQuestion struct {
	Type         string      `json:"type"`
	Instructions string      `json:"instructions"`
	Criteria     jevCriteria `json:"criteria"`
}

type jevCriteria struct {
	Steer    string `json:"steer"`
	FollowUp string `json:"follow_up"`
}

// jevAbortedByTimeout is the message of AbortSignal.timeout's DOMException; jevFetchFailed is undici's for a failed connection.
const (
	jevAbortedByTimeout = "The operation was aborted due to timeout"
	jevFetchFailed      = "fetch failed"
)

// transportError names a failed request as fetch does: the timeout's own message, else "fetch failed".
func transportError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return errors.New(jevAbortedByTimeout)
	}
	return errors.New(jevFetchFailed)
}

// canWaitAsFollowUp asks Jev whether a message sent while the agent is busy can wait until the current task finishes.
// upstream: canWaitAsFollowUp (auto-follow-up.ts:15-47). The timeout covers the whole exchange, as AbortSignal.timeout does.
func (j jevClient) canWaitAsFollowUp(state followUpRouteState, apiKey string) (bool, error) {
	payload := jevRequestBody{
		Model: "jev-latest",
		State: jevState{
			CurrentTask:     orDefault(state.CurrentTask, "Unknown"),
			AgentLatestText: orDefault(state.AgentLatestText, "None yet"),
			NewMessage:      state.NewMessage,
		},
		Questions: jevQuestions{Route: jevRouteQuestion{
			Type:         "choice",
			Instructions: "A coding agent is busy with `current_task`. Should `new_message` be delivered now as steering for that task, or queued until the task finishes?",
			Criteria: jevCriteria{
				Steer:    "The message changes, constrains, corrects, adds scope to, or gives information about the task the agent is doing right now, so the agent should see it before its next step. Includes messages that mix a change to the current task with a later request.",
				FollowUp: "The message is a separate request or question that does not change how the current task should be done, so it can wait until the current task finishes.",
			},
		}},
	}
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false) // JSON.stringify leaves < > & alone
	if err := encoder.Encode(payload); err != nil {
		return false, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), j.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, j.endpoint, &body)
	if err != nil {
		return false, errors.New(jevFetchFailed)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Content-Type", "application/json")
	client := j.client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return false, transportError(ctx, err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return false, transportError(ctx, err)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return false, fmt.Errorf("Jev returned %d: %s", response.StatusCode, jsSliceUnits(string(raw), 200))
	}

	var answer any
	if err := json.Unmarshal(raw, &answer); err != nil {
		return false, err
	}
	if answer == nil {
		return false, errors.New("Cannot read properties of null (reading 'answers')")
	}
	var choice string
	var confidence float64
	if route := asMap(asMap(asMap(answer)["answers"])["route"]); route != nil {
		c, isString := route["choice"].(string)
		n, isNumber := route["confidence"].(float64)
		if isString && (c == "steer" || c == "follow_up") && isNumber {
			choice, confidence = c, n
		}
	}
	if choice == "" {
		return false, errors.New("Jev returned an unexpected route answer")
	}
	return choice == "follow_up" && confidence >= minFollowUpConfidence, nil
}

// orDefault is `value || fallback` for a string.
func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// jsSliceUnits is s.slice(0, n): the first n UTF-16 code units. Invalid UTF-8 reads as U+FFFD, as a decoder gives it, and a cut
// inside a surrogate pair leaves a lone surrogate, which a Go string holds as U+FFFD.
func jsSliceUnits(s string, n int) string {
	units := utf16.Encode([]rune(s))
	if len(units) <= n {
		return string(utf16.Decode(units))
	}
	return string(utf16.Decode(units[:n]))
}
