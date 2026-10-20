package pi_permission_system

import "sort"

// Port of src/value-guards.ts. A JavaScript object is a *jsObject; a Go map[string]any (what the host hands over) reads as one too.

// ToRecord reads value as a record: an object itself (the same pointer for a *jsObject), anything else (null, a string, a number,
// an array) an empty record.
func ToRecord(value any) *jsObject {
	switch v := value.(type) {
	case *jsObject:
		if v != nil {
			return v
		}
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		o := newObject()
		for _, k := range keys {
			o.set(k, v[k])
		}
		return o
	}
	return newObject()
}

// IsPlainRecord reports a record a structural walk should descend into: an object built by an object literal or JSON, not a class
// instance.
func IsPlainRecord(value any) bool {
	switch v := value.(type) {
	case *jsObject:
		return v != nil
	case map[string]any:
		return v != nil
	}
	return false
}

// GetNonEmptyString is the trimmed string of a string value, or ok false for anything else and for a string that trims to nothing.
func GetNonEmptyString(value any) (string, bool) {
	s, ok := value.(string)
	if !ok {
		return "", false
	}
	t := jsTrim(s)
	return t, t != ""
}

// recordString reads record[key] through GetNonEmptyString (a missing key is undefined).
func recordString(r *jsObject, key string) (string, bool) {
	v, _ := r.get(key)
	return GetNonEmptyString(v)
}
