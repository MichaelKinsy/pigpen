//go:build race

package websearch

// raceEnabled: the race detector changes allocation counts (sync.Pool drops entries at random, so a regexp's
// matcher is allocated again), so allocation bounds are not checked under -race.
const raceEnabled = true
