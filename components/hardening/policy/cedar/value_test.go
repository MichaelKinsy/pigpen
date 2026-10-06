package cedar

import (
	"encoding/json"
	"math"
	"testing"

	cedar "github.com/cedar-policy/cedar-go"
)

type (
	kv = map[string]any
	rm = cedar.RecordMap
	cv = cedar.Value
)

func S(s string) cv  { return cedar.String(s) }
func L(i int64) cv   { return cedar.Long(i) }
func B(b bool) cv    { return cedar.Boolean(b) }
func Set(v ...cv) cv { return cedar.NewSet(v...) }
func Rec(m rm) cv    { return cedar.NewRecord(m) }

func ptr[T any](v T) *T { return &v }

func TestRecordMapping(t *testing.T) {
	cases := []struct {
		name string
		in   kv
		want rm
	}{
		{"nil map", nil, rm{}},
		{"string", kv{"a": "x"}, rm{"a": S("x")}},
		{"bool", kv{"a": true, "b": false}, rm{"a": B(true), "b": B(false)}},
		{"ints", kv{"a": 1, "b": int8(-2), "c": uint16(3), "d": int64(math.MaxInt64)}, rm{"a": L(1), "b": L(-2), "c": L(3), "d": L(math.MaxInt64)}},
		{"uint over int64", kv{"a": uint64(math.MaxInt64) + 1}, rm{"a": S("9223372036854775808")}},
		{"integral floats", kv{"a": 3.0, "b": -7.0, "c": 0.0, "d": float32(2)}, rm{"a": L(3), "b": L(-7), "c": L(0), "d": L(2)}},
		{"largest float64 below 2^63", kv{"a": 9223372036854774784.0}, rm{"a": L(9223372036854774784)}},
		{"2^63 float", kv{"a": 9223372036854775808.0}, rm{"a": S("9.223372036854776e+18")}},
		{"negative 2^63 float", kv{"a": -9223372036854775808.0}, rm{"a": L(math.MinInt64)}},
		{"fractions", kv{"a": 1.5, "b": -0.25, "c": 1e-7}, rm{"a": S("1.5"), "b": S("-0.25"), "c": S("1e-07")}},
		{"huge", kv{"a": 1e20, "b": -1e300}, rm{"a": S("1e+20"), "b": S("-1e+300")}},
		{"nan and inf", kv{"a": math.NaN(), "b": math.Inf(1), "c": math.Inf(-1)}, rm{"a": S("NaN"), "b": S("+Inf"), "c": S("-Inf")}},
		{"json.Number", kv{"a": json.Number("42"), "b": json.Number("4.5"), "c": json.Number("99999999999999999999")}, rm{"a": L(42), "b": S("4.5"), "c": S("99999999999999999999")}},
		{"null omitted", kv{"a": nil, "b": 1}, rm{"b": L(1)}},
		{"typed nil omitted", kv{"a": (*int)(nil), "b": true}, rm{"b": B(true)}},
		{"pointer", kv{"a": ptr("x")}, rm{"a": S("x")}},
		{"set", kv{"a": []any{"x", 1, true}}, rm{"a": Set(S("x"), L(1), B(true))}},
		{"string slice", kv{"a": []string{"x", "y"}}, rm{"a": Set(S("x"), S("y"))}},
		{"array", kv{"a": [2]int{1, 2}}, rm{"a": Set(L(1), L(2))}},
		{"nil elements omitted", kv{"a": []any{nil, "x", nil}}, rm{"a": Set(S("x"))}},
		{"nil slice is an empty set", kv{"a": []string(nil)}, rm{"a": Set()}},
		{"duplicates collapse", kv{"a": []any{"x", "x"}}, rm{"a": Set(S("x"))}},
		{"nested", kv{"a": kv{"b": kv{"c": []any{kv{"d": 1}}}}}, rm{"a": Rec(rm{"b": Rec(rm{"c": Set(Rec(rm{"d": L(1)}))})})}},
		{"string map", kv{"a": map[string]string{"k": "v"}}, rm{"a": Rec(rm{"k": S("v")})}},
		{"nil map value is an empty record", kv{"a": map[string]any(nil)}, rm{"a": Rec(rm{})}},
		{"empty key", kv{"": 1}, rm{"": L(1)}},
	}
	for _, c := range cases {
		got, err := Record(c.in)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if want := cedar.NewRecord(c.want); !got.Equal(want) {
			t.Errorf("%s:\n got  %s\n want %s", c.name, got.MarshalCedar(), want.MarshalCedar())
		}
	}
}
