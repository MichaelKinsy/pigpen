package pig_music

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/pig-music/doctor"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// ExtensionWith builds the extension over a source and player the test supplies,
// and an environment it chooses. Nil source and player use the real ones.
func ExtensionWith(src music.Source, player music.Player, env map[string]string, lookPath func(string) (string, error), denv *doctor.Env) *sdk.Extension {
	d := deps{Source: src, Player: player, LookPath: lookPath, Doctor: denv}
	if env != nil {
		d.Getenv = func(k string) string { return env[k] }
	}
	return extensionWith(d)
}

// ExtensionAs is ExtensionWith in another PiG process: token stands for the process (a /reload keeps it, another PiG has
// its own).
func ExtensionAs(token string, src music.Source, player music.Player, env map[string]string) *sdk.Extension {
	d := deps{Source: src, Player: player, Token: token}
	if env != nil {
		d.Getenv = func(k string) string { return env[k] }
	}
	return extensionWith(d)
}
