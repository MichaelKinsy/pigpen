package urls

import (
	"fmt"
	"path/filepath"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

var (
	regDiagMu sync.RWMutex
	regDiags  []string
)

// RegistrationDiagnostics returns any errors recorded while registering
// built-in URL handlers in RegisterExtension.
func RegistrationDiagnostics() []string {
	regDiagMu.RLock()
	defer regDiagMu.RUnlock()
	return append([]string(nil), regDiags...)
}

// RegisterExtension installs the built-in URL protocol handlers owned by this
// package (skill://, rule://, out://, local://, mcp://, omp://, help://,
// attachment://, conflict://) in omp's registration order and attaches session
// lifecycle hooks (skill snapshot caching on before_agent_start; per-session
// cleanup via internal/lifecycle and session_shutdown).
//
// Safe to call multiple times in one process (across fresh *sdk.Extension
// instances for new/reloaded/child sessions): built-in handlers are stateless
// and idempotent, live sessions' state and providers are never reset, and no
// per-extension pointers are retained.
//
// Other lanes register their own schemes (agent://, history://, proc://, cfg://,
// issue://, pr://, mr://) via urls.Register(h).
//
// It does not panic and performs no I/O.
func RegisterExtension(e *sdk.Extension) {
	builtins := []Handler{
		SkillHandler{},
		RuleHandler{},
		OutHandler{},
		LocalHandler{},
		MCPHandler{},
		OmpHandler{},
		HelpHandler{},
		AttachmentHandler{},
		ConflictHandler{},
	}
	var diags []string
	for _, h := range builtins {
		if err := Register(h); err != nil {
			diags = append(diags, fmt.Sprintf("urls: failed to register built-in handler %s://: %v", h.Scheme(), err))
			continue
		}
		mu.Lock()
		builtinSchemes[h.Scheme()] = true
		mu.Unlock()
	}
	regDiagMu.Lock()
	regDiags = diags
	regDiagMu.Unlock()

	lifecycle.OnEnd("urls", clearSessionState)
	if e == nil {
		return
	}
	e.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		opts, ok := data["systemPromptOptions"].(map[string]any)
		if !ok {
			return nil, nil
		}
		rawSkills, ok := opts["skills"].([]any)
		if !ok {
			return nil, nil
		}
		sid, err := ctx.GetSessionID()
		if err != nil || sid == "" {
			return nil, nil
		}
		skills := make([]SkillEntry, 0, len(rawSkills))
		for _, item := range rawSkills {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			name, _ := m["name"].(string)
			if name == "" {
				continue
			}
			desc, _ := m["description"].(string)
			filePath, _ := m["filePath"].(string)
			baseDir, _ := m["baseDir"].(string)
			if baseDir == "" && filePath != "" {
				baseDir = filepath.Dir(filePath)
			}
			skills = append(skills, SkillEntry{
				Name:        name,
				Description: desc,
				FilePath:    filePath,
				BaseDir:     baseDir,
			})
		}
		setSessionSkills(sid, skills)
		return nil, nil
	})
	e.OnEvent(sdk.EventSessionShutdown, func(ctx sdk.Context, _ map[string]any) (any, error) {
		if sid, err := ctx.GetSessionID(); err == nil && sid != "" {
			lifecycle.End(sid)
		}
		return nil, nil
	})
}
