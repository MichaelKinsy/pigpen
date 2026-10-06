package strictjson

import "testing"

type inner struct {
	X int `json:"x"`
}

type doc struct {
	A int              `json:"a"`
	B string           `json:"b"`
	C any              `json:"c"`
	N *inner           `json:"n"`
	L []inner          `json:"l"`
	M map[string]inner `json:"m"`
	K string           `json:"k"`
}

func TestDecode(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		strict bool
		ok     bool
	}{
		{"plain", `{"a":1,"b":"x"}`, true, true},
		{"duplicate key", `{"a":1,"a":2}`, true, false},
		{"nested duplicate key", `{"a":1,"b":"x","c":{"d":1,"d":2}}`, false, false},
		{"duplicate key in array element", `{"c":[{"d":1,"d":2}]}`, false, false},
		{"unknown field strict", `{"a":1,"z":1}`, true, false},
		{"unknown field lenient", `{"a":1,"z":1}`, false, true},
		{"trailing value", `{"a":1} {"a":2}`, true, false},
		{"trailing garbage", `{"a":1} x`, true, false},
		{"truncated", `{"a":1`, true, false},
		{"empty", ``, true, false},
		{"wrong type", `{"a":"x"}`, true, false},
		{"too deep", `{"c":` + deep(40) + `}`, false, false},
		// encoding/json folds case: a key must name a field exactly, or it would fill it, or shadow it.
		{"case-folded key strict", `{"A":1}`, true, false},
		{"case-folded key lenient", `{"A":1}`, false, false},
		{"case-folded repeat strict", `{"a":1,"A":2}`, true, false},
		{"case-folded repeat lenient", `{"b":"x","B":"y"}`, false, false},
		{"unicode-folded key", "{\"\u212a\":\"x\"}", false, false}, // KELVIN SIGN folds to k
		{"nested case-folded key", `{"n":{"x":1,"X":2}}`, true, false},
		{"case-folded key in array element", `{"l":[{"X":1}]}`, true, false},
		{"case-folded key in map value", `{"m":{"k":{"X":1}}}`, true, false},
		{"map keys are not fields", `{"m":{"K":{"x":1},"k":{"x":2}}}`, true, true},
		{"any holds anything", `{"c":{"A":1,"a":2}}`, true, true},
	}
	for _, c := range cases {
		var d doc
		err := Decode([]byte(c.in), &d, c.strict)
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func deep(n int) string {
	s := ""
	for i := 0; i < n; i++ {
		s += "["
	}
	for i := 0; i < n; i++ {
		s += "]"
	}
	return s
}
