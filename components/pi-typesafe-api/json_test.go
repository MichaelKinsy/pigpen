package pitypesafe

import (
	"math"
	"testing"
)

func TestObjectKeepsInsertionOrderThroughParseAndEncode(t *testing.T) {
	in := `{"z":1,"a":{"y":[true,null,"x"],"b":2},"m":"é\u2028\n"}`
	v, err := ParseJSON([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	obj := v.(*Object)
	if got := obj.Keys(); len(got) != 3 || got[0] != "z" || got[1] != "a" || got[2] != "m" {
		t.Fatalf("keys = %v", got)
	}
	out, err := EncodeJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	// U+2028 stays raw (JSON.stringify does not escape it), newline is escaped.
	want := "{\"z\":1,\"a\":{\"y\":[true,null,\"x\"],\"b\":2},\"m\":\"é\u2028\\n\"}"
	if string(out) != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestEncodeJSONNumbersFollowJavaScript(t *testing.T) {
	cases := map[float64]string{0: "0", math.Copysign(0, -1): "0", 1: "1", 0.5: "0.5", 1e21: "1e+21", 1.5e-7: "1.5e-7", 123456789012: "123456789012", -2.25: "-2.25"}
	for in, want := range cases {
		out, err := EncodeJSON(in)
		if err != nil || string(out) != want {
			t.Errorf("EncodeJSON(%v) = %q, %v; want %q", in, out, err, want)
		}
	}
	if _, err := EncodeJSON(math.NaN()); err == nil {
		t.Error("NaN must not encode")
	}
}

func TestSetKeepsPositionAndDeleteRemoves(t *testing.T) {
	o := NewObject()
	o.Set("a", 1.0)
	o.Set("b", 2.0)
	o.Set("a", 3.0)
	o.Delete("missing")
	if k := o.Keys(); k[0] != "a" || k[1] != "b" || o.Len() != 2 {
		t.Fatalf("keys = %v", k)
	}
	o.Delete("a")
	if k := o.Keys(); len(k) != 1 || k[0] != "b" {
		t.Fatalf("keys after delete = %v", k)
	}
}

func TestFromGoRejectsWhatJSONCannotCarry(t *testing.T) {
	cyc := map[string]any{}
	cyc["self"] = cyc
	for name, v := range map[string]any{"cycle": cyc, "nan": map[string]any{"n": math.NaN()}, "inf": math.Inf(1), "func": func() {}, "chan": make(chan int)} {
		if _, err := FromGo(v); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	tree, err := FromGo(map[string]any{"b": 1, "a": []int{1, 2}})
	if err != nil {
		t.Fatal(err)
	}
	if k := tree.(*Object).Keys(); k[0] != "a" || k[1] != "b" {
		t.Fatalf("a Go map has no order, so keys are sorted: %v", k)
	}
}

func TestParseJSONRejectsTrailingData(t *testing.T) {
	if _, err := ParseJSON([]byte(`{"a":1} x`)); err == nil {
		t.Fatal("trailing data must fail")
	}
	if _, err := ParseJSON([]byte(`{"a":`)); err == nil {
		t.Fatal("truncated input must fail")
	}
}

func TestCloneIsDeep(t *testing.T) {
	v, _ := ParseJSON([]byte(`{"a":{"b":[1,{"c":2}]}}`))
	c := v.(*Object).Clone()
	inner, _ := c.Get("a")
	inner.(*Object).Set("b", "changed")
	orig, _ := v.(*Object).Get("a")
	if b, _ := orig.(*Object).Get("b"); b == "changed" {
		t.Fatal("clone shares structure with the original")
	}
}
