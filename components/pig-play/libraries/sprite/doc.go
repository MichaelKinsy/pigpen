// Package sprite is the one canonical PiG pixel pig: the 16-by-14 mascot grid, its
// palette, the colour variants, the "PiG." wordmark and the persisted selection. It
// imports no SDK, so a game, a login screen or a preview can share it. Games crop and
// scale from MascotSpriteFor and MascotPalette instead of redrawing the pig.
//
// The artwork and code come from MichaelKinsy/PiG piglets/standard/extensions/piglogin
// (MIT, Copyright (c) 2026 Michael Kinsy); see CREDITS.md.
package sprite
