package posthorse

// Helpers for the dynamic JSON the host delivers (session entries, messages, tool content,
// details): rec = map[string]any, []any, float64 numbers, string, bool, nil.

// str reads m[k] when it is a string (`typeof m[k] === "string"`).
func str(m rec, k string) (string, bool) {
	s, ok := m[k].(string)
	return s, ok
}

// num reads m[k] when it is a number (`typeof m[k] === "number"`).
func num(m rec, k string) (float64, bool) {
	f, ok := m[k].(float64)
	return f, ok
}

// boolean reads m[k] when it is a boolean.
func boolean(m rec, k string) (value, ok bool) {
	value, ok = m[k].(bool)
	return value, ok
}

// recOf is `isRecord(v)` returning the object.
func recOf(v any) (rec, bool) {
	m, ok := v.(rec)
	return m, ok
}

// arrOf is `unknownArray` (contracts.ts:97-99): the array, or false when v is not one.
func arrOf(v any) ([]any, bool) {
	a, ok := v.([]any)
	return a, ok
}

// optionalString is `optionalString` (contracts.ts:100-102).
func optionalString(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

// getPath reads nested object members, nil as soon as a step is not an object or the key is missing.
func getPath(v any, keys ...string) any {
	for _, k := range keys {
		m, ok := v.(rec)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}
