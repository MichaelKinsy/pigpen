package cedar

import (
	"encoding/json"
	"math"
	"reflect"
	"strconv"

	cedar "github.com/cedar-policy/cedar-go"
)

// Limits on a tool-argument record.
const (
	maxDepth   = 16
	maxNodes   = 20000
	maxStrings = 1 << 20 // one string value
)

// Record maps tool arguments to a Cedar record. The mapping is fixed:
//
//   - nil is omitted (from a record or a set);
//   - a bool is a Boolean and a string a String;
//   - a Go integer is a Long, or a String of its decimal digits when it does not fit in an int64;
//   - a float that is an integer inside the int64 range is a Long; every other float (a fraction, an out-of-range
//     integer, NaN, an infinity) is a String of its shortest decimal form;
//   - a json.Number is a Long when it is an int64, else a String of its text;
//   - a slice or array is a Set; a map with string keys is a Record;
//   - anything else, a nesting deeper than 16, more than 20,000 values, or a string over 1 MiB is an error
//     (policy_error), so the call is denied rather than judged on a partial view.
func Record(args map[string]any) (cedar.Record, error) {
	if args == nil {
		return cedar.NewRecord(cedar.RecordMap{}), nil
	}
	n := 0
	rec, err := record(reflect.ValueOf(args), 0, &n)
	if err != nil {
		return cedar.Record{}, err
	}
	return rec, nil
}

func record(v reflect.Value, depth int, nodes *int) (cedar.Record, error) {
	m := cedar.RecordMap{}
	iter := v.MapRange()
	for iter.Next() {
		if iter.Key().Kind() != reflect.String {
			return cedar.Record{}, bad()
		}
		if len(iter.Key().String()) > maxStrings {
			return cedar.Record{}, bad()
		}
		val, ok, err := convert(iter.Value(), depth+1, nodes)
		if err != nil {
			return cedar.Record{}, err
		}
		if ok {
			m[cedar.String(iter.Key().String())] = val
		}
	}
	return cedar.NewRecord(m), nil
}

func long(i int64) cedar.Value { return cedar.Long(i) }

func convert(v reflect.Value, depth int, nodes *int) (cedar.Value, bool, error) {
	if depth > maxDepth {
		return nil, false, bad()
	}
	if *nodes++; *nodes > maxNodes {
		return nil, false, bad()
	}
	for v.IsValid() && (v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer) {
		if v.IsNil() {
			return nil, false, nil
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return nil, false, nil
	}
	if v.Type() == reflect.TypeOf(json.Number("")) {
		s := v.String()
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return long(i), true, nil
		}
		return cedar.String(s), true, nil
	}
	switch v.Kind() {
	case reflect.Bool:
		return cedar.Boolean(v.Bool()), true, nil
	case reflect.String:
		if v.Len() > maxStrings {
			return nil, false, bad()
		}
		return cedar.String(v.String()), true, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return long(v.Int()), true, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		u := v.Uint()
		if u > math.MaxInt64 {
			return cedar.String(strconv.FormatUint(u, 10)), true, nil
		}
		return long(int64(u)), true, nil
	case reflect.Float32, reflect.Float64:
		f := v.Float()
		if f == math.Trunc(f) && f >= -9223372036854775808.0 && f < 9223372036854775808.0 {
			return long(int64(f)), true, nil
		}
		return cedar.String(strconv.FormatFloat(f, 'g', -1, 64)), true, nil
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return cedar.NewSet(), true, nil
		}
		items := make([]cedar.Value, 0, v.Len())
		for i := 0; i < v.Len(); i++ {
			item, ok, err := convert(v.Index(i), depth+1, nodes)
			if err != nil {
				return nil, false, err
			}
			if ok {
				items = append(items, item)
			}
		}
		return cedar.NewSet(items...), true, nil
	case reflect.Map:
		if v.IsNil() {
			return cedar.NewRecord(cedar.RecordMap{}), true, nil
		}
		rec, err := record(v, depth, nodes)
		if err != nil {
			return nil, false, err
		}
		return rec, true, nil
	}
	return nil, false, bad()
}
