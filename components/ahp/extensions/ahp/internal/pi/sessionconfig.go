package pi

import (
	"context"
	"encoding/json"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// ProjectTrustKey is the pre-creation config property that reports project-resource trust.
const ProjectTrustKey = "projectResources"

// SessionConfigOptions configure a [SessionConfigService].
type SessionConfigOptions struct {
	DefaultWorkingDirectory string
	ProjectTrustPolicy      ProjectTrustPolicy
	AgentDir                string
}

// SessionConfigService answers resolveSessionConfig: what a session would be like before it is
// created. The exchange is iterative: the answer changes as the user picks a directory, which is
// the whole reason the command exists (port of src/pi/session-config.ts).
type SessionConfigService struct{ opts SessionConfigOptions }

// NewSessionConfigService creates the service.
func NewSessionConfigService(opts SessionConfigOptions) *SessionConfigService {
	return &SessionConfigService{opts: opts}
}

// Resolve implements host.SessionConfigHandler.
func (s *SessionConfigService) Resolve(_ context.Context, params ahptypes.ResolveSessionConfigParams) (ahptypes.ResolveSessionConfigResult, error) {
	workingDirectory := s.opts.DefaultWorkingDirectory
	if params.WorkingDirectory != nil && *params.WorkingDirectory != "" {
		path, err := wire.FileURIToPath(*params.WorkingDirectory)
		if err != nil {
			return ahptypes.ResolveSessionConfigResult{}, wire.InvalidParams(err.Error())
		}
		workingDirectory = path
	}
	trust, err := ResolveProjectTrust(workingDirectory, s.opts.ProjectTrustPolicy, s.opts.AgentDir)
	if err != nil {
		return ahptypes.ResolveSessionConfigResult{}, err
	}
	description := "Trust-gated project resources (" + trust.Reason + ")."
	if trust.Reason == "no-project-resources" {
		description = "This folder has no trust-gated project resources."
	}
	readOnly := true
	value, _ := json.Marshal(trust.Trusted)
	return ahptypes.ResolveSessionConfigResult{
		Schema: ahptypes.SessionConfigSchema{Type: "object", Properties: map[string]ahptypes.SessionConfigPropertySchema{
			// Host policy, not a per-session choice.
			ProjectTrustKey: {Type: "boolean", Title: "Load project resources", Description: &description, ReadOnly: &readOnly},
		}},
		Values: map[string]json.RawMessage{ProjectTrustKey: value},
	}, nil
}

// Completions implements host.SessionConfigHandler: this host offers no dynamic completions.
func (s *SessionConfigService) Completions(context.Context, ahptypes.SessionConfigCompletionsParams) (ahptypes.SessionConfigCompletionsResult, error) {
	return ahptypes.SessionConfigCompletionsResult{Items: []ahptypes.SessionConfigValueItem{}}, nil
}
