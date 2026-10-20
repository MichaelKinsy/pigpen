package ts

import "testing"

// The dense rows must answer every (state, symbol) as ts_language_lookup does.
func TestDenseLookupMatchesLinearScan(t *testing.T) {
	l, err := BashLanguage()
	if err != nil {
		t.Fatal(err)
	}
	for state := uint32(0); state < l.stateCount; state++ {
		for symbol := uint32(0); symbol < l.symbolCount; symbol++ {
			if got, want := l.lookup(uint16(state), uint16(symbol)), l.lookupLinear(uint16(state), uint16(symbol)); got != want {
				t.Fatalf("state %d symbol %d: dense %d, linear %d", state, symbol, got, want)
			}
		}
	}
}
