package powerline_footer

import (
	"net/http"
	"time"
)

// The Jev auto follow-up classifier. upstream: auto-follow-up.ts.

const (
	jevDefaultEndpoint    = ""
	jevTimeout            = time.Millisecond
	minFollowUpConfidence = 0.0
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

// jevClient asks Jev whether a message can wait.
type jevClient struct {
	endpoint string
	timeout  time.Duration
	client   *http.Client
}

func newJevClient() jevClient { return jevClient{} }

// canWaitAsFollowUp asks Jev whether a message sent while the agent is busy can wait until the current task finishes.
func (j jevClient) canWaitAsFollowUp(state followUpRouteState, apiKey string) (bool, error) {
	return false, nil
}
