package langsmith

// Run preparation of client.js: processInputs/Outputs/Metadata/Error, _filterNewTokenEvents,
// prepareRunCreateOrUpdateInputs, mergeRuntimeEnvIntoRun, _maskRunMetadata, the sampling identifier of
// _filterForSampling, and estimateSerializedSize (utils/fast-safe-stringify/index.js:60-261).
//
// A run payload is an *omap owned by the client from createRun/updateRun on. Nested values (extra, inputs, ...)
// are shared with the run tree and are never mutated: they are replaced, like the object spreads of the original.

import (
	"math"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// processInputs is client.js:1179-1190 for a boolean hideInputs (the extension never sets a function).
func (c *client) processInputs(inputs any) any {
	if c.hideInputs {
		return newOmap()
	}
	return inputs
}

// processOutputs is client.js:1191-1202.
func (c *client) processOutputs(outputs any) any {
	if c.hideOutputs {
		return newOmap()
	}
	return outputs
}

// processMetadata is client.js:1203-1214.
func (c *client) processMetadata(metadata any) any {
	if c.hideMetadata {
		return newOmap()
	}
	return metadata
}

// filterNewTokenEvents is client.js:1238-1251: a `new_token` event loses its kwargs. The original always returns a
// new array; this returns events itself when nothing changes (the bytes are the same).
func filterNewTokenEvents(events any) any {
	switch ev := events.(type) {
	case []*omap:
		changed := false
		for _, e := range ev {
			if isNewTokenEvent(e) {
				changed = true
				break
			}
		}
		if !changed {
			return events
		}
		out := make([]*omap, len(ev))
		for i, e := range ev {
			if isNewTokenEvent(e) {
				out[i] = e.omit("kwargs")
			} else {
				out[i] = e
			}
		}
		return out
	case []any:
		changed := false
		for _, e := range ev {
			if isNewTokenEvent(e) {
				changed = true
				break
			}
		}
		if !changed {
			return events
		}
		out := make([]any, len(ev))
		for i, e := range ev {
			if isNewTokenEvent(e) {
				m, _ := toOmap(e)
				out[i] = m.omit("kwargs")
			} else {
				out[i] = e
			}
		}
		return out
	}
	return events
}

func isNewTokenEvent(e any) bool {
	if !isRecord(e) {
		return false
	}
	name, _ := recGet(e, "name").(string)
	return name == "new_token"
}

// processError is client.js:1227-1233 without an anonymizer: the error is returned unchanged.
func (c *client) processError(err any) any { return err }

// prepareRunInPlace is prepareRunCreateOrUpdateInputs (client.js:1252-1273) on a run payload the client owns. The
// original works on a shallow copy; every step here is idempotent and replaces (never mutates) nested values, so
// working in place is not observable.
func (c *client) prepareRunInPlace(run *omap) *omap {
	if v := run.val("inputs"); !isUndef(v) {
		run.set("inputs", c.processInputs(v))
	}
	if v := run.val("outputs"); !isUndef(v) {
		run.set("outputs", c.processOutputs(v))
	}
	if v := run.val("error"); !isUndef(v) {
		run.set("error", c.processError(v))
	}
	if c.hideMetadata {
		if extra, ok := toOmap(run.val("extra")); ok && extra.has("metadata") {
			run.set("extra", extra.clone().set("metadata", c.processMetadata(extra.val("metadata"))))
		}
	}
	if v := run.val("events"); !isUndef(v) {
		run.set("events", filterNewTokenEvents(v))
	}
	return run
}

// maskRunMetadata is _maskRunMetadata (client.js:1581-1592): run.extra.metadata == null leaves the run alone.
func (c *client) maskRunMetadata(run *omap) *omap {
	if !c.hideMetadata {
		return run
	}
	extra, ok := toOmap(run.val("extra"))
	if !ok {
		return run
	}
	if md := extra.val("metadata"); md == nil || isUndef(md) {
		return run
	}
	run.set("extra", extra.clone().set("metadata", c.processMetadata(extra.val("metadata"))))
	return run
}

func newOmapCap(n int) *omap {
	return &omap{keys: make([]string, 0, n), vals: make(map[string]any, n)}
}

// mergeRuntimeEnvIntoRun is client.js:47-76. It replaces run.extra with a new object (extra's own keys, then
// runtime and metadata); cachedEnvVars nil reads the environment now.
func mergeRuntimeEnvIntoRun(run *omap, cachedEnvVars *omap, omitTracedRuntimeInfo bool, tracingSampleRate *float64) *omap {
	if omitTracedRuntimeInfo {
		return run
	}
	runtimeEnv := getRuntimeEnvironment()
	envVars := cachedEnvVars
	if envVars == nil {
		envVars = getLangSmithEnvVarsMetadata()
	}
	extra, hasExtra := run.get("extra")
	var extraRec *omap
	if hasExtra && extra != nil && !isUndef(extra) { // `run.extra ?? {}`
		extraRec, _ = toOmap(extra)
	}
	var metadata, runtimeOver any = undef, undef
	if extraRec != nil {
		metadata = extraRec.val("metadata")
		runtimeOver = extraRec.val("runtime")
	}
	runtime := newOmapCap(runtimeEnv.size() + 2).spread(runtimeEnv).spread(runtimeOver)

	md := newOmapCap(envVars.size() + 4 + sizeOf(metadata)).spread(envVars)
	// `envVars.revision_id || ("revision_id" in run && run.revision_id)` (client.js:63).
	if jsTruthy(envVars.val("revision_id")) || (run.has("revision_id") && jsTruthy(run.val("revision_id"))) {
		rid := undef
		if run.has("revision_id") {
			rid = run.val("revision_id")
		}
		if rid == nil || isUndef(rid) {
			rid = envVars.val("revision_id")
		}
		md.set("revision_id", rid)
	}
	md.spread(metadata)
	if tracingSampleRate != nil {
		md.set("ls_tracing_sample_rate", *tracingSampleRate)
	}
	newExtra := newOmapCap(extraRec.size() + 2).spread(extraRec)
	newExtra.set("runtime", runtime).set("metadata", md)
	run.set("extra", newExtra)
	return run
}

func sizeOf(v any) int {
	switch x := v.(type) {
	case *omap:
		return x.size()
	case map[string]any:
		return len(x)
	}
	return 0
}

// samplingIdentifier is the argument of _shouldSample in _filterForSampling (client.js:1363-1368):
// `run.trace_id ?? run.dotted_order?.split(".", 1)[0].split("Z")[1] ?? run.id`. ok is false when all are nullish.
func samplingIdentifier(run *omap, runID any) (id string, ok bool) {
	if s, isStr := run.val("trace_id").(string); isStr {
		return s, true
	}
	if d, isStr := run.val("dotted_order").(string); isStr {
		first, _, _ := strings.Cut(d, ".")
		if i := strings.IndexByte(first, 'Z'); i >= 0 {
			rest := first[i+1:]
			if j := strings.IndexByte(rest, 'Z'); j >= 0 {
				rest = rest[:j]
			}
			return rest, true
		}
	}
	if s, isStr := runID.(string); isStr {
		return s, true
	}
	return "", false
}

// shouldSample is _filterForSampling([run]).length > 0 with sampling.js's `identifier == null` branch.
func (c *client) shouldSample(run *omap, runID any) bool {
	r := c.tracingSampleRate
	if r == nil || *r >= 1 {
		return true
	}
	id, ok := samplingIdentifier(run, runID)
	if !ok {
		return *r > 0 || math.IsNaN(*r) // sampling.js:19-24: <= 0 is false, a missing identifier is true
	}
	return isSampledByID(id, r)
}

// estimateSerializedSize is estimateSerializedSize(value).size (fast-safe-stringify/index.js:60-261) for the
// values a payload holds. Escape expansion is not counted, as in the original. Values the original handles with
// toJSON, Map, Set, typed arrays and so on cannot occur in a Go payload; anything else is measured by
// serialising it (the original's catch branch, index.js:253-260).
func estimateSerializedSize(v any) int64 { return int64(estimateValue(v)) }

func estimateValue(v any) int {
	switch x := v.(type) {
	case nil:
		return 4
	case undefinedT:
		return 0
	case bool:
		return 5
	case string:
		return jsUTF8Len(x) + 2
	case float64:
		return estimateNumber(x)
	case float32:
		return estimateNumber(float64(x))
	case int:
		return estimateInt(int64(x))
	case int64:
		return estimateInt(x)
	case int32:
		return estimateInt(int64(x))
	case uint64:
		if x < math.MaxInt64 {
			return estimateInt(int64(x))
		}
	case *omap:
		if x == nil {
			return 4
		}
		size := 2
		emitted := 0
		for _, k := range x.keys {
			val := x.vals[k]
			if isUndef(val) {
				continue
			}
			if emitted > 0 {
				size++
			}
			size += jsUTF8Len(k) + 3 + estimateValue(val)
			emitted++
		}
		return size
	case map[string]any:
		if x == nil {
			return 4
		}
		size := 2
		emitted := 0
		for k, val := range x {
			if isUndef(val) {
				continue
			}
			if emitted > 0 {
				size++
			}
			size += jsUTF8Len(k) + 3 + estimateValue(val)
			emitted++
		}
		return size
	case []any:
		size := 2
		for i, e := range x {
			size += estimateInArray(e)
			if i < len(x)-1 {
				size++
			}
		}
		return size
	case []string:
		size := 2
		for i, e := range x {
			size += jsUTF8Len(e) + 2
			if i < len(x)-1 {
				size++
			}
		}
		return size
	case []*omap:
		size := 2
		for i, e := range x {
			size += estimateValue(e)
			if i < len(x)-1 {
				size++
			}
		}
		return size
	}
	b, err := json.AppendStringify(nil, v)
	if err != nil {
		return 16
	}
	return len(b)
}

func estimateInArray(v any) int {
	if isUndef(v) {
		return 4
	}
	return estimateValue(v)
}

func estimateInt(n int64) int {
	l := 1
	if n < 0 {
		l = 2
		n = -n
	}
	for n >= 10 {
		n /= 10
		l++
	}
	return l
}

func estimateNumber(f float64) int {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 4
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return estimateInt(int64(f))
	}
	return len(jsNumberString(f))
}
