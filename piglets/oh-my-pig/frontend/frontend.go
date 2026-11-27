// Package frontend is oh-my-pig's frontend member: pig-tern's member with omp's look fixed on. The drawing
// itself lives in pig-tern's package, so both Piglets share one implementation of the Tern Surface Protocol.
package frontend

import (
	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
	ternfe "github.com/MichaelKinsy/pigpen/piglets/pig-tern/frontend"
)

// Frontend is the member's factory, the symbol the Piglet build registers.
func Frontend() fe.Frontend {
	return ternfe.FrontendWith(ternfe.Options{
		Omp:       true,
		OnSurface: surface.SetNative,
		Name:      "oh-my-pig",
		Version:   surface.Version,
		// The host gives a frontend the model id; the extension knows its name.
		ModelDisplayName: surface.ModelDisplayName,
		// ModelMeta supplies per-model context window, cost, and thinking levels
		// from the extension's ModelRegistry for Tern's omp model picker.
		ModelMeta: func(provider, id string) (ternfe.ModelMeta, bool) {
			m, ok := surface.LookupModel(provider, id)
			if !ok {
				return ternfe.ModelMeta{}, false
			}
			return ternfe.ModelMeta{
				ID:              m.ID,
				Provider:        m.Provider,
				Name:            m.Name,
				ContextWindow:   m.ContextWindow,
				InputCostPer1M:  m.InputCostPer1M,
				OutputCostPer1M: m.OutputCostPer1M,
				Reasoning:       m.Reasoning,
				ThinkingLevels:  m.ThinkingLevels,
			}, true
		},
		// OnThinkingLevel applies a thinking level picked on the model picker's
		// slider immediately via the extension SDK.
		OnThinkingLevel: surface.RequestThinkingLevel,
		// Relative paths are resolved against the working directory as omp
		// spells it.
		DisplayPath: surface.DisplayPath,
	})
}
