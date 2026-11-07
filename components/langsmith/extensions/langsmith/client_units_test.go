package langsmith

// Vector tests for the pure functions of client*.go against client_units.json (real SDK output,
// port/tools/gen/client-units.mjs) and unit tests of the pieces the differential scenarios do not isolate.

import (
	"bytes"
	"compress/gzip"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

func loadUnits(t testing.TB) *omap {
	b, err := os.ReadFile("testdata/client_units.json")
	if err != nil {
		t.Fatal(err)
	}
	v, ok := parseJSONOrdered(string(b))
	if !ok {
		t.Fatal("client_units.json is not JSON")
	}
	return v.(*omap)
}

func TestTrimQuotesVectors(t *testing.T) {
	for _, e := range loadUnits(t).val("trim").([]any) {
		m := e.(*omap)
		in, want := m.val("s").(string), m.val("out").(string)
		if got := trimQuotes(in); got != want {
			t.Errorf("trimQuotes(%q) = %q, want %q", in, got, want)
		}
	}
}

func jsNumVector(v any) float64 {
	switch x := v.(type) {
	case string:
		switch x {
		case "NaN":
			return math.NaN()
		case "Infinity":
			return math.Inf(1)
		case "-Infinity":
			return math.Inf(-1)
		case "-0":
			return math.Copysign(0, -1)
		}
	case float64:
		return x
	}
	panic("bad vector value")
}

func TestJSParseVectors(t *testing.T) {
	same := func(a, b float64) bool {
		return a == b && math.Signbit(a) == math.Signbit(b) || math.IsNaN(a) && math.IsNaN(b)
	}
	for _, e := range loadUnits(t).val("parse").([]any) {
		m := e.(*omap)
		s := m.val("s").(string)
		if got, want := jsParseFloat(s), jsNumVector(m.val("float")); !same(got, want) {
			t.Errorf("parseFloat(%q) = %v, want %v", s, got, want)
		}
		got, ok := jsParseInt10(s)
		if !ok {
			got = math.NaN()
		}
		if want := jsNumVector(m.val("int")); !same(got, want) && !(got == 0 && want == 0) {
			t.Errorf("parseInt(%q, 10) = %v, want %v", s, got, want)
		}
	}
}

func TestEstimateSerializedSizeVectors(t *testing.T) {
	for i, e := range loadUnits(t).val("estimate").([]any) {
		m := e.(*omap)
		v := revive(m.val("value"))
		want := int64(m.val("size").(float64))
		if got := estimateSerializedSize(v); got != want {
			b, _, _ := stringify(m.val("value"))
			t.Errorf("case %d %s: estimate = %d, want %d", i, b, got, want)
		}
	}
}

func TestAutoBatchQueueVectors(t *testing.T) {
	for ci, e := range loadUnits(t).val("queue").([]any) {
		c := e.(*omap)
		q := &autoBatchQueue{maxSizeBytes: int64(c.val("max").(float64))}
		res := c.val("result").(*omap)
		var accepted []string
		for i, n := range c.val("items").([]any) {
			id := "id-" + strconv.Itoa(i)
			it := &queueItem{payload: obj("id", id, "pad", strings.Repeat("x", int(n.(float64))))}
			it.size = estimateSerializedSize(it.payload)
			push := res.val("pushed").([]any)[i].(*omap)
			ok := q.push(it)
			if ok != push.val("accepted").(bool) || q.sizeBytes != int64(push.val("sizeBytes").(float64)) {
				t.Errorf("case %d push %d: accepted %v size %d, want %v %v", ci, i, ok, q.sizeBytes, push.val("accepted"), push.val("sizeBytes"))
			}
			if ok {
				accepted = append(accepted, id)
			}
			if ok && float64(it.size) != push.val("size").(float64) {
				t.Errorf("case %d push %d: size %d, want %v", ci, i, it.size, push.val("size"))
			}
		}
		for pi, p := range c.val("pops").([]any) {
			pair := p.([]any)
			want := res.val("pops").([]any)[pi].(*omap)
			batch, err := q.pop(pair[0].(float64), pair[1].(float64))
			if msg, isErr := want.val("error").(string); isErr {
				if err == nil || err.Error() != msg {
					t.Errorf("case %d pop %d: err %v, want %q", ci, pi, err, msg)
				}
				continue
			}
			if err != nil {
				t.Errorf("case %d pop %d: %v", ci, pi, err)
				continue
			}
			var ids []any
			for _, it := range batch {
				ids = append(ids, it.payload.val("id"))
			}
			wantIDs := want.val("ids").([]any)
			if len(ids) != len(wantIDs) {
				t.Errorf("case %d pop %d: ids %v, want %v", ci, pi, ids, wantIDs)
			} else {
				for k := range ids {
					if ids[k] != wantIDs[k] {
						t.Errorf("case %d pop %d: ids %v, want %v", ci, pi, ids, wantIDs)
						break
					}
				}
			}
			if float64(q.sizeBytes) != want.val("sizeBytes").(float64) || float64(q.len()) != want.val("left").(float64) {
				t.Errorf("case %d pop %d: queue size %d len %d, want %v %v", ci, pi, q.sizeBytes, q.len(), want.val("sizeBytes"), want.val("left"))
			}
		}
	}
}

func TestMergeRuntimeEnvIntoRunVectors(t *testing.T) {
	isolateEnv(t)
	for i, e := range loadUnits(t).val("merge").([]any) {
		c := e.(*omap)
		run := revive(c.val("run")).(*omap)
		env := c.val("env").(*omap)
		var rate *float64
		if f, ok := c.val("rate").(float64); ok {
			rate = &f
		}
		got := mergeRuntimeEnvIntoRun(run, env, c.val("omit") == true, rate)
		b, _, err := stringify(got)
		if err != nil {
			t.Fatal(err)
		}
		canonBase, canonTmp = "\x00", "\x00"
		if g, w := canonText(string(b)), c.val("result").(string); g != w {
			t.Errorf("case %d:\n got %s\nwant %s", i, g, w)
		}
	}
}

// ---- body readers ----

func newTestMultipart(t testing.TB, n int) *multipartBody {
	t.Helper()
	isolateEnv(t)
	c, err := newClient(clientOptions{APIKey: new("k"), APIURL: new("http://127.0.0.1:1")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.close)
	var creates []*omap
	for i := range n {
		creates = append(creates, obj("id", "0195b2a0-7e41-7c3d-9f00-"+strconv.Itoa(100000000000+i), "trace_id", "t", "dotted_order", "d",
			"inputs", obj("q", strings.Repeat("é", 300)), "extra", obj("metadata", obj("i", float64(i)))))
	}
	mp, err := c.buildMultipart(creates, nil)
	if err != nil {
		t.Fatal(err)
	}
	return mp
}

func TestBodyReaderStreamsTheWholeBody(t *testing.T) {
	mp := newTestMultipart(t, 20)
	defer mp.release()
	want := bytes.Join(func() [][]byte {
		var parts [][]byte
		cur := segCursor{mp: mp}
		for p := cur.next(1 << 30); p != nil; p = cur.next(1 << 30) {
			parts = append(parts, append([]byte(nil), p...))
		}
		return parts
	}(), nil)
	if int64(len(want)) != mp.total {
		t.Fatalf("segments hold %d bytes, total says %d", len(want), mp.total)
	}
	if !bytes.HasPrefix(want, []byte("--"+mp.boundary+"\r\n")) || !bytes.HasSuffix(want, []byte("\r\n--"+mp.boundary+"--\r\n")) {
		t.Fatalf("body framing is wrong: %q ... %q", want[:60], want[len(want)-60:])
	}
	for _, chunk := range []int{1, 7, 4096, 1 << 20} {
		r := mp.newReader(false)
		var got bytes.Buffer
		buf := make([]byte, chunk)
		for {
			n, err := r.Read(buf)
			got.Write(buf[:n])
			if err != nil {
				if err != io.EOF {
					t.Fatal(err)
				}
				break
			}
		}
		r.Close()
		if !bytes.Equal(got.Bytes(), want) {
			t.Fatalf("chunk %d: body differs", chunk)
		}
	}
	// gzip: the stream decodes to the same body
	r := mp.newReader(true)
	zr, err := gzip.NewReader(r)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	if !bytes.Equal(dec, want) {
		t.Fatal("gzip body decodes to something else")
	}
	if _, err := r.Read(make([]byte, 1)); err == nil {
		t.Fatal("read after close succeeded")
	}
}

func TestBodyReaderCloseReleasesBufferOnce(t *testing.T) {
	mp := newTestMultipart(t, 1)
	r1, r2 := mp.newReader(false), mp.newReader(true)
	mp.release() // the owner is done
	if mp.buf == nil {
		t.Fatal("buffer released while readers are open")
	}
	r1.Close()
	r1.Close() // idempotent
	if mp.buf == nil {
		t.Fatal("buffer released while a reader is open")
	}
	r2.Close()
	if mp.buf != nil {
		t.Fatal("buffer not released after the last reader closed")
	}
}

func TestRandomBase36(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		s := randomBase36()
		if len(s) < 9 || len(s) > 11 {
			t.Fatalf("length %d: %q", len(s), s)
		}
		for _, r := range s {
			if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z') {
				t.Fatalf("not base36: %q", s)
			}
		}
		seen[s] = true
	}
	if len(seen) < 190 {
		t.Fatalf("only %d distinct boundaries in 200", len(seen))
	}
}

func TestFilterNewTokenEvents(t *testing.T) {
	ev := []*omap{obj("name", "new_token", "time", "t", "kwargs", obj("token", "x")), obj("name", "other", "kwargs", obj("k", 1.0))}
	got := filterNewTokenEvents(ev).([]*omap)
	if b, _, _ := stringify(got); string(b) != `[{"name":"new_token","time":"t"},{"name":"other","kwargs":{"k":1}}]` {
		t.Fatalf("got %s", b)
	}
	if got[0] == ev[0] || ev[0].has("kwargs") == false {
		t.Fatal("the input event was modified")
	}
	same := []*omap{obj("name", "other")}
	if out := filterNewTokenEvents(same).([]*omap); &out[0] != &same[0] {
		t.Fatal("unchanged events should be returned as they are")
	}
	if filterNewTokenEvents(undef) != undef || filterNewTokenEvents(nil) != nil {
		t.Fatal("nil/undefined must pass through")
	}
	if b, _, _ := stringify(filterNewTokenEvents([]any{map[string]any{"name": "new_token", "kwargs": 1.0}})); string(b) != `[{"name":"new_token"}]` {
		t.Fatalf("[]any events: %s", b)
	}
}

func TestSamplingIdentifier(t *testing.T) {
	cases := []struct {
		run  *omap
		id   any
		want string
		ok   bool
	}{
		{obj("trace_id", "T", "dotted_order", "2024Zabc"), "i", "T", true},
		{obj("trace_id", nil, "dotted_order", "20240101T000000Zabc.2024Zdef"), "i", "abc", true},
		{obj("dotted_order", "no-z-here.x"), "i", "i", true},
		{obj("dotted_order", "2024ZaZb"), "i", "a", true},
		{obj("dotted_order", "2024Z"), "i", "", true},
		{obj(), undef, "", false},
		{obj("trace_id", undef), nil, "", false},
	}
	for i, c := range cases {
		got, ok := samplingIdentifier(c.run, c.id)
		if got != c.want || ok != c.ok {
			t.Errorf("case %d: got %q,%v want %q,%v", i, got, ok, c.want, c.ok)
		}
	}
}

func TestPrepareRunInPlaceHidesAndKeepsNested(t *testing.T) {
	c := &client{hideInputs: true, hideOutputs: true, hideMetadata: true}
	inputs := obj("q", "x")
	extra := obj("metadata", obj("m", 1.0), "runtime", obj("r", 1.0))
	run := obj("inputs", inputs, "outputs", undef, "extra", extra, "error", "boom")
	c.prepareRunInPlace(run)
	if b, _, _ := stringify(run); string(b) != `{"inputs":{},"extra":{"metadata":{},"runtime":{"r":1}},"error":"boom"}` {
		t.Fatalf("got %s", b)
	}
	if b, _, _ := stringify(extra); string(b) != `{"metadata":{"m":1},"runtime":{"r":1}}` {
		t.Fatalf("the shared extra object was modified: %s", b)
	}
	if b, _, _ := stringify(inputs); string(b) != `{"q":"x"}` {
		t.Fatal("the shared inputs object was modified")
	}
	// the batch's own metadata mask only touches an extra that has non-null metadata
	nullMD := obj("extra", obj("metadata", nil))
	if b, _, _ := stringify(c.maskRunMetadata(nullMD)); string(b) != `{"extra":{"metadata":null}}` {
		t.Fatalf("a null metadata must stay null: %s", b)
	}
}
