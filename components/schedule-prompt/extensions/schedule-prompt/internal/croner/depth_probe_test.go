package croner

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsdate"
)

func TestDepthProbe(t *testing.T) {
	path := os.Getenv("CRONER_GOLDEN")
	if path == "" {
		t.Skip()
	}
	g := readGolden(t, path)
	loc, _ := time.LoadLocation("UTC")
	jsdate.SetLocation(loc)
	maxOK, minOver := 0, math.MaxInt
	var overExpr, okExpr string
	for ei, expr := range g.Exprs {
		raw := g.Results[0][ei]
		if len(raw) > 0 && raw[0] == '{' {
			continue
		}
		var chains [][]any
		_ = json.Unmarshal(raw, &chains)
		c, err := Parse(expr)
		if err != nil {
			continue
		}
		for ri, ref := range g.Refs[0] {
			over := false
			for _, w := range chains[ri] {
				if s, ok := w.(string); ok && s == "Maximum call stack size exceeded" {
					over = true
				}
			}
			d := 0
			DepthHook = func(n int) {
				if n > d {
					d = n
				}
			}
			_, _ = c.NextRunAfter(ref)
			DepthHook = nil
			if over {
				if d < minOver {
					minOver, overExpr = d, expr
				}
			} else if d > maxOK {
				maxOK, okExpr = d, expr
			}
		}
	}
	fmt.Printf("max depth of a success: %d (%q); min depth of an overflow: %d (%q)\n", maxOK, okExpr, minOver, overExpr)
}
