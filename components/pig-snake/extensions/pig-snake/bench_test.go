package pig_snake

import (
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-snake/internal/sprites"
)

// One frame of the title screen and of a running board, at a 100x30 terminal. A frame is drawn only when the
// picture changed (the clock advances a playing game; a paused or finished one draws nothing).
//
//	go test -run xxx -bench . -benchmem
func BenchmarkSnakeRender100x30(b *testing.B) {
	c := newComponentWith(highScores{}, sprites.Builtin(""), func() int { return 30 }, options{manual: true, rng: fixedRand{}})
	b.Cleanup(func() { c.Dispose() })
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = c.Render(100)
	}
}
