package eq

import "testing"

// The surface table is the same for every file of a scan, so its rows (and the regexps derived from them) are
// built once per table, not once per ScanGaps call.
func TestParseSurfaceIsBuiltOncePerTable(t *testing.T) {
	a, b := parseSurface(embeddedSurface), parseSurface(embeddedSurface)
	if len(a) == 0 || &a[0] != &b[0] {
		t.Fatalf("expected the same parsed rows for the same table (%d rows)", len(a))
	}
	if other := parseSurface("| `pi.nothing` | x | y | missing | z | w |\n"); len(other) == len(a) {
		t.Fatal("a different table must parse to its own rows")
	}
	if allocs := testing.AllocsPerRun(20, func() { ScanGaps(benchExtension, "") }); allocs > 4000 {
		t.Fatalf("ScanGaps allocated %.0f times for one small extension; the surface table must not be re-parsed per call", allocs)
	}
}
