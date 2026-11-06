package langsmith

// Port of utils/profiles.js and utils/profile-lock.js (langsmith 0.10.5): the LangSmith CLI profile
// (~/.langsmith/config.json, LANGSMITH_CONFIG_FILE, LANGSMITH_PROFILE) that supplies api_url, workspace_id and an
// API key or OAuth tokens (with refresh) when the environment sets none. OWNER: profile lane.

import (
	"context"
	"net/http"
)

const defaultAPIURL = "https://api.smith.langchain.com"

// authHeader is {name, value}.
type authHeader struct{ Name, Value string }

// profileClientConfig is loadProfileClientConfig's result; nil pointers are `undefined`.
type profileClientConfig struct {
	APIURL            *string
	APIKey            *string
	WorkspaceID       *string
	OAuthAccessToken  *string
	OAuthRefreshToken *string
	Auth              *profileAuth
}

// loadProfileClientConfig is profiles.js loadProfileClientConfig; it reads the config file synchronously.
func loadProfileClientConfig() profileClientConfig {
	panic("unported: profile.go loadProfileClientConfig")
}

// hasValue is profiles.js hasValue: not undefined/null and not blank.
func hasValue(s *string) bool { panic("unported: profile.go hasValue") }

// profileAuth is the ProfileAuth class.
type profileAuth struct{}

// currentAuthHeader is ProfileAuth.currentAuthHeader (no refresh).
func (p *profileAuth) currentAuthHeader() *authHeader {
	panic("unported: profile.go currentAuthHeader")
}

// getAuthHeader is ProfileAuth.getAuthHeader: it refreshes an expiring OAuth token first (cross-process lock,
// discovery, POST to the token endpoint, atomic config rewrite), using hc for requests.
func (p *profileAuth) getAuthHeader(ctx context.Context, hc *http.Client) *authHeader {
	panic("unported: profile.go getAuthHeader")
}

// isProfileAuthorizationHeader is ProfileAuth.isProfileAuthorizationHeader.
func (p *profileAuth) isProfileAuthorizationHeader(value string) bool {
	panic("unported: profile.go isProfileAuthorizationHeader")
}
