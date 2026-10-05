package mpv

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// levelsLabel names the measuring filter, so it can be found and removed again.
const levelsLabel = "pmlv"

// levelsFilter is an astats filter that publishes the loudness of every 50 ms window as frame metadata, which mpv shows as the
// af-metadata/<label> property (measured on mpv 0.37: the property holds lavfi.astats.Overall.RMS_level and ...Peak_level in dBFS).
// It changes nothing about the sound.
const levelsFilter = "@" + levelsLabel + ":lavfi=[astats=metadata=1:reset=1:length=0.05:measure_perchannel=none:measure_overall=RMS_level+Peak_level]"

var _ music.Levels = (*Player)(nil)

// SetLevels adds the measuring filter, or removes it. It is idempotent: a filter that is already there is replaced, one that is
// already gone is not an error.
func (p *Player) SetLevels(ctx context.Context, on bool) error {
	if !on {
		_ = p.command(ctx, "af", "remove", "@"+levelsLabel) // already gone is fine
		return nil
	}
	if err := p.command(ctx, "af", "add", levelsFilter); err != nil {
		_ = p.command(ctx, "af", "remove", "@"+levelsLabel) // a duplicate label: start again
		return p.command(ctx, "af", "add", levelsFilter)
	}
	return nil
}

// Level reads the filter's latest window.
func (p *Player) Level(ctx context.Context) (music.Level, bool) {
	c, err := p.conn()
	if err != nil {
		return music.Level{}, false
	}
	raw, err := p.call(ctx, c, "get_property", "af-metadata/"+levelsLabel)
	if err != nil || len(raw) == 0 {
		return music.Level{}, false
	}
	var md map[string]string
	if json.Unmarshal(raw, &md) != nil {
		return music.Level{}, false
	}
	rms, ok1 := linear(md["lavfi.astats.Overall.RMS_level"])
	peak, ok2 := linear(md["lavfi.astats.Overall.Peak_level"])
	if !ok1 && !ok2 {
		return music.Level{}, false
	}
	return music.Level{RMS: rms, Peak: peak}, true
}

// linear converts astats' dBFS text ("-20.5", "-inf") to a linear level between 0 and 1.
func linear(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	db, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(db) {
		return 0, false
	}
	if math.IsInf(db, -1) {
		return 0, true
	}
	return math.Min(math.Pow(10, db/20), 1), true
}
